package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jchv/again/filenotify"
)

var (
	verbose   bool
	forcePoll bool
	watchDir  string
	regex     string
	ignore    string
	addrEnvs  string
	portMin   int
	portMax   int
	debounce  time.Duration

	portMutex sync.Mutex
	portCycle uint16

	portMap = map[uint16]*portForwarder{}
)

func nextPort() uint16 {
	portMutex.Lock()
	defer portMutex.Unlock()

	portCycle++
	if portCycle == uint16(portMax) {
		portCycle = uint16(portMin)
	}

	return portCycle
}

type portForwarder struct {
	env  string
	src  uint16
	dest uint32
}

func newPortForwarder(env string, src uint16) *portForwarder {
	p := &portForwarder{
		env: env,
		src: src,
	}

	go p.Run()

	return p
}

func (p *portForwarder) Cycle() uint16 {
	port := nextPort()
	atomic.StoreUint32(&p.dest, uint32(port))
	return port
}

func (p *portForwarder) Run() error {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", p.src))

	if err != nil {
		return err
	}

	for {
		// Wait for connections
		sock, err := l.Accept()

		if err != nil {
			return err
		}

		// Connect to remote
		dest := atomic.LoadUint32(&p.dest)
		raddr := fmt.Sprintf("localhost:%d", dest)
		remote, err := net.Dial("tcp", raddr)

		if err != nil {
			log.Println("Could not connect to", raddr)
			sock.Close()
			continue
		}

		// Read thread
		go func() {
			io.Copy(sock, remote)
			sock.Close()
		}()

		// Write thread
		go func() {
			io.Copy(remote, sock)
			sock.Close()
		}()
	}
}

func init() {
	log.SetPrefix("[AGAIN] ")
	log.SetFlags(log.Lshortfile | log.Ltime)

	flag.BoolVar(&verbose, "v", false, "Enable verbose logging.")
	flag.BoolVar(&forcePoll, "poll", false, "Use polling instead of events.")
	flag.StringVar(&watchDir, "watch", ".", "Directory to watch.")
	flag.StringVar(&regex, "regex", ".*", "Regular expression of filenames to watch.")
	flag.StringVar(&ignore, "ignore", "\\.git", "Regular expression of filenames to ignore.")
	flag.StringVar(&addrEnvs, "addr-env", "", "List of envs to forward addresses on, e.g. ADDR:8080.")
	flag.IntVar(&portMin, "port-min", 50000, "First port to allocate for forwarding.")
	flag.IntVar(&portMax, "port-max", 60000, "Last port to allocate for forwarding.")
	flag.DurationVar(&debounce, "debounce", 2*time.Second, "Amount of time to debounce events by.")
	flag.Parse()

	if flag.NArg() < 1 {
		log.Fatalln("You must provide a command to run.")
	}

	portCycle = uint16(portMin) - 1

	for _, addrEnv := range strings.Split(addrEnvs, ",") {
		parts := strings.SplitN(addrEnv, ":", 2)
		if len(parts) != 2 {
			log.Fatalln("Syntax error: addr-env pair missing port.")
		}
		env := parts[0]
		port, err := strconv.Atoi(parts[1])
		if err != nil {
			log.Fatalln("Syntax error: addr-env pair port invalid.")
		}

		portMap[uint16(port)] = newPortForwarder(env, uint16(port))
	}
}

func debouncer(delay time.Duration, input chan struct{}, output chan struct{}) {
	timer := time.NewTimer(delay)
	for {
		<-input

	BounceLoop:
		for {
			timer.Reset(delay)
			select {
			case <-input:
				break
			case <-timer.C:
				output <- struct{}{}
				break BounceLoop
			}
		}

	DrainLoop:
		for {
			select {
			case <-input:
			default:
				break DrainLoop
			}
		}
	}
}

func runner(args []string, signal chan struct{}) {
	for {
		if verbose {
			log.Println("Executing command...")
		}

		cmd := exec.Command(args[0], args[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()
		for _, mapper := range portMap {
			port := mapper.Cycle()
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=localhost:%d", mapper.env, port))
		}

		err := cmd.Start()
		if err != nil {
			log.Fatalln("Error executing command:", err)
		}

		<-signal

		log.Println("Files have changed, reloading...")

		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Process.Wait()
	}
}

func main() {
	var watcher filenotify.FileWatcher
	var err error

	// Setup watcher
	if forcePoll {
		watcher = filenotify.NewPollingWatcher()
	} else {
		watcher, err = filenotify.NewEventWatcher()
		if err != nil {
			log.Printf("Couldn't create event watcher (%s,) falling back to polling watcher.\n", err)
			log.Println("HINT: Use -poll to force polling.")
			watcher = filenotify.NewPollingWatcher()
		}
	}

	// Setup matchers
	matcher := regexp.MustCompile(regex)
	unmatcher := regexp.MustCompile(ignore)

	watch := func(path string, info os.FileInfo) {
		include := matcher.MatchString(path)
		exclude := unmatcher.MatchString(path)
		if (include || info.IsDir()) && !exclude {
			if verbose {
				log.Println("Watching", path)
			}

			watcher.Add(path)
		} else {
			if verbose {
				log.Println("Ignoring", path)
			}
		}
	}

	signalin, signalout := make(chan struct{}, 64), make(chan struct{}, 64)
	signal := func(path string) {
		include := matcher.MatchString(path)
		exclude := unmatcher.MatchString(path)

		if include && !exclude {
			signalin <- struct{}{}
		}
	}

	// Walk directories for files to watch.
	err = filepath.Walk(watchDir, func(path string, info os.FileInfo, err error) error {
		watch(path, info)
		return nil
	})

	if err != nil {
		log.Fatalln("Could not walk directories:", err)
	}

	events, errors := watcher.Events(), watcher.Errors()

	go debouncer(debounce, signalin, signalout)
	go runner(flag.Args(), signalout)

	for {
		select {
		case event := <-events:
			signal(event.Name)

			switch fsnotify.Op(event.Op) {
			case fsnotify.Create:
				info, err := os.Stat(event.Name)
				if err != nil {
					log.Println("Error:", err)
				}
				watch(event.Name, info)
			case fsnotify.Remove:
				watcher.Remove(event.Name)
			}
		case err := <-errors:
			log.Println("Error:", err)
		}
	}
}
