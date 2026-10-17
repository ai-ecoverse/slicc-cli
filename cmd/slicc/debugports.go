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
browser slicc was given or launched. The process list is refreshed in the
background when a connection to this machine finds it older than about a
second (one scan at a time, never blocking the connect), and about every
30 s while idle; DevToolsActivePort files are read again on each such
connection and every 5 s. --allow doesn't change any of that, not even --allow 127.0.0.1:9222.
Only --expose-debug-port <port> opens one, a port that is a debugging port
when slicc starts; the browsers slicc was given or launched stay closed.
`
