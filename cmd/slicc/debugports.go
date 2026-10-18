package main

const exposeUsage = `  --expose-debug-port <port>
                       let seven reach this debugging port after all
                       (repeatable; see below)
`

const exposeUsageDefault = `  --expose-debug-port <port>
                      let seven reach this debugging port after all
                      (repeatable; see below)
`

const debugPortsHelp = `
Debugging ports: seven never reaches this machine's browser debugging ports
on 127.0.0.0/8, [::1] or any other address of this machine, over net, http,
ws or ssh forwarding. They are ports 9222-9229; the port in each
DevToolsActivePort file that browser discovery reads; the port of each
running process started with --remote-debugging-port (with port 0, the one
in the DevToolsActivePort of its --user-data-dir); and the port of each
browser slicc was given or launched. DevToolsActivePort files are read
again on each connection to this machine and every 5 s. The process list
is rescanned in the background, about every second while connections to
this machine keep coming and every 30 s while idle; a connection that
finds it more than about a second old waits for a fresh scan (up to its
5 s timeout) and is refused if that scan fails. On Linux the list falls
back to /proc when ps fails; with no list at all slicc says so at startup,
and connections to this machine's own addresses are refused. No --allow
changes any of this, not even --allow 127.0.0.1:9222. Connections to other
hosts never wait. Only --expose-debug-port <port> opens one, a port that
is a debugging port when slicc starts; the browsers slicc was given or
launched stay closed.
`
