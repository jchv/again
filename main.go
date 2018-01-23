package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/moby/moby/pkg/filenotify"
)

var (
	verbose   bool
	forcePoll bool
	watchDir  string
	regex     string
	ignore    string
	debounce  time.Duration
)

func init() {
	flag.BoolVar(&verbose, "v", false, "Enable verbose logging.")
	flag.BoolVar(&forcePoll, "poll", false, "Use polling instead of events.")
	flag.StringVar(&watchDir, "watch", ".", "Directory to watch.")
	flag.StringVar(&regex, "regex", ".*", "Regular expression of filenames to watch.")
	flag.StringVar(&ignore, "ignore", "\\.git", "Regular expression of filenames to ignore.")
	flag.DurationVar(&debounce, "debounce", 1*time.Second, "Amount of time to debounce events by.")
	flag.Parse()
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
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Start()
		if err != nil {
			log.Fatalln("Error executing command:", err)
		}

		<-signal

		log.Println("Files have changed, reloading...")

		cmd.Process.Signal(os.Kill)
		cmd.Process.Wait()
	}
}

func main() {
	var watcher filenotify.FileWatcher
	var err error

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
