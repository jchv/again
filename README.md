# Again
Again is a WIP command runner. The intention is to make a general purpose auto-reload for compiled applications.

## TCP Port Forwarding
One challenge with auto-reloading Go apps is dealing with TCP sockets. TCP sockets are not immediately released for reuse. The exact delay will vary, but it can often be far too long.

To deal with this, Again provides TCP port forwarding. Unlike other auto-reloading tools I've found, it does it in a manner that's more friendly to Go applications, specifically ones that listen on multiple addresses.

The option is `-addr-env` and it works by being provided a list of environment variables mapped to the ports they should listen on. For example, if you had two variables, `LISTEN_HTTP` and `LISTEN_PPROF`, you may run something like this:

`./again -addr-env=LISTEN_HTTP:8080,LISTEN_PPROF:6600`

...and Again would provide a simple TCP proxy, listening on the given ports, while passing a throwaway address into the underlying environment variable, between `port-min` and `port-max`. By default, `port-min` and `port-max` are 50000 and 60000 respectively, making it unlikely to have port conflicts.

## TODO
  * Graceful shutdown of running command.
