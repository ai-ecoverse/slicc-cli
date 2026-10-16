package main

const exposeUsage = `  --expose-debug-port <port>
                       let seven reach this debugging port after all
                       (repeatable; see below)
`

const debugPortsHelp = `
Debugging ports: seven never reaches ports 9222-9229, the ports in any
DevToolsActivePort file (one that appears later too), or the debugging port
of a browser slicc was given or launched, on 127.0.0.0/8, [::1] or any other
address of this machine, over net, http, ws, udp or ssh forwarding. --allow
doesn't change that, not even --allow 127.0.0.1:9222. Only
--expose-debug-port <port> opens one of 9222-9229 or a DevToolsActivePort
port; the browsers slicc was given or launched stay closed.
`
