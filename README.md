# slicc-cli

`slicc` is SLICC's command line for seven, the SLICC on `*.sliccy.ai`. It is one binary for macOS, Windows and Linux. It launches and links browsers, and shares a machine's network, compute and browsers with a running seven over a WebRTC link. `npx sliccy` and Sliccstart.app run this binary. It replaces slicc-node and slicc-swift.

**Status:** being built. Only `--version` and `--help` exist so far. The link protocol is drafted alongside seven's link exit and moves into this README once both sides pass a shared test.

## Commands (planned)

| command | what it does |
| --- | --- |
| `slicc` | pick browsers in a small TUI; Enter launches the best match with CDP, opens seven and links it |
| `slicc attach --cdp <url>` | link a seven that is open in an already-running CDP browser |
| `slicc launch` | launch a browser with CDP in a separate SLICC profile and print its CDP URL |
| `slicc <join-url> follow [--exit] [--runner …] [--cdp <url>]` | follow a seven's tray from another machine and offer its network, compute (over SSH) or browsers |
| `slicc browsers --json` | list running CDP endpoints and installed browsers, ranked |

## Network policy

A node checks every address it connects to for the link's `tcp`, `http` and `ssh` forwarding, and drops refused addresses from `resolve` answers. The check runs on each address actually connected, after DNS and for every Happy Eyeballs attempt, so a name can't rebind past it. A refusal is `EACCES`.

**Rules.** `--allow` and `--deny` take `CIDR` or `CIDR:port`, repeated or comma-separated: `10.0.0.0/8`, `127.0.0.1/32:9222`, `[::1]/128:9222`. A bare address is a single-host rule (`127.0.0.1`, `[::1]:22`). Host bits are masked off, and `::ffff:a.b.c.d` rules become IPv4 rules. A rule that can never match is rejected: one inside `64:ff9b::/96`, `64:ff9b:1::/48`, `2002::/16`, `2001::/32`, `0.0.0.0/8` or `::/128`.

**Precedence.** The longest prefix wins. At the same prefix, a rule with a port wins over one without. At a full tie, a user flag wins over a default rule, so `--allow 192.168.0.0/16` opens exactly that range. Between two user flags, deny wins. An address no rule matches is allowed.

**Normalization**, before matching:
- IPv4-mapped IPv6 (`::ffff:a.b.c.d`) is checked as IPv4.
- NAT64 (`64:ff9b::/96`) is checked as the IPv4 in its last 32 bits, and 6to4 (`2002::/16`) as the IPv4 in bits 16–47.
- Local-use NAT64 (`64:ff9b:1::/48`), Teredo (`2001::/32`), `0.0.0.0/8` and `::` are always denied, whatever the rules say.

**Remote default** (a node following a tray from another machine). Every public address is allowed. Denied:
- loopback: `127.0.0.0/8`, `::1/128`;
- private: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`;
- CGNAT and tailnets: `100.64.0.0/10`;
- link-local and cloud metadata: `169.254.0.0/16`, `fe80::/10`;
- ULA: `fc00::/7`;
- multicast: `224.0.0.0/4`, `ff00::/8`;
- reserved and documentation: `240.0.0.0/4`, `255.255.255.255/32`, `192.0.0.0/24`, `192.0.2.0/24`, `198.18.0.0/15`, `198.51.100.0/24`, `203.0.113.0/24`, `2001:db8::/32`, `100::/64`;
- IPv4-compatible `::/96` (deprecated) and SIIT `::ffff:0:0:0/96`.

**Local default** (the bridge to the browser `slicc` controls). The remote default, with loopback and the private ranges (`127.0.0.0/8`, `::1/128`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7`) allowed instead of denied, so this machine and the LAN are reachable. The browser's debugging port and the CLI's own ports are denied:
- on loopback, each as `127.0.0.0/8:port`, `127.0.0.1/32:port` and `[::1]/128:port`, so `--allow 127.0.0.1/32` doesn't reopen them;
- on every address of this machine, as `<address>/32:port` or `<address>/128:port`. The interfaces are enumerated at check time and cached for 5 seconds, so a new interface counts. If they can't be enumerated, those ports are denied on every address.

Only a user allow for exactly the same address and port reopens one of these. `--deny 127.0.0.0/8` and `--deny 192.168.0.0/16` take loopback and the LAN back out.

**`resolve` filtering.** An address is kept when the policy allows it on at least one port. When every address is dropped, the answer is an empty list.

**HELLO.** `policy.summary` names the default and lists the `--allow` and `--deny` rules on top of it. `policy.allow` and `policy.deny` list the `--allow` and `--deny` rules plus the local default's loopback port denies, not the default ranges or the per-interface denies.

## SSH server

A node started with a runner serves SSH (`golang.org/x/crypto/ssh`) on each `ssh` link stream. It never listens on a port.

**Auth.** `none` is accepted, because the link is the trust boundary. The user name is ignored. Everything runs as the user who started `slicc`, in their home directory.

**Host key.** One ed25519 key per machine, `slicc/ssh_host_ed25519_key` in the user config directory (`~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux, `%AppData%` on Windows). It is created on first use with mode `0600`, and a looser mode is tightened to `0600` when it is loaded. HELLO carries its public key in `authorized_keys` form as `sshHostKey`.

**No runner.** Every session channel is refused as `administratively prohibited`, with the reason.

**exec** runs `<runner argv…> <command>`, the command as one last argument. **shell** runs the runner's interactive form:
- a runner ending in `-c` drops it, and adds `-l` when the word before `-c` is `sh`, `bash`, `zsh`, `dash`, `ksh`, `mksh`, `ash` or `fish`: `bash -c` gives `bash -l`, `docker exec -i box sh -c` gives `docker exec -i box sh -l`, `python3 -c` gives `python3`;
- a runner ending in `/c`, `/C` or `-Command` drops it: `cmd.exe /c` gives `cmd.exe`;
- for any other runner, `shell` prints why on stderr and exits 1.

A command that can't start exits 127 with the reason on stderr.

**Sessions.**
- `pty-req` and `window-change`: a PTY from `creack/pty` on Unix (the command gets its own session and controlling terminal) and ConPTY on Windows. `TERM` comes from `pty-req`.
- `env`: any variable with a valid name is accepted and added to `slicc`'s environment.
- `signal`: on Unix `ABRT`, `ALRM`, `FPE`, `HUP`, `ILL`, `INT`, `KILL`, `PIPE`, `QUIT`, `SEGV`, `TERM`, `USR1` and `USR2` go to the command's process group, or with a PTY to the terminal's foreground process group. On Windows, `INT` on a PTY is `^C`, and `ABRT`, `HUP`, `INT`, `KILL`, `QUIT` and `TERM` otherwise end the process.
- The end: `exit-status`, or `exit-signal` for a Unix command killed by one of the signals above (`exit-status` 128+n for any other). After the command exits, its output is read until EOF or 2 s of silence, so a background job that keeps stdout open doesn't hold the session. A session closed while its command runs sends it `HUP`.

**`sftp` subsystem** (`pkg/sftp`): this machine's files as the user who started `slicc`, starting in the home directory. A runner that sandboxes commands (`docker exec …`) doesn't sandbox `sftp`.

**`slicc-eval` subsystem**: one persistent REPL per node (for example `python3 -i`), started on first use and shared by every session, so state survives between them. Input is read in lines. The lines available at once are written to the REPL together, and its output is forwarded (stdout and stderr apart) until it has been quiet for 500 ms. At EOF the session exits 0. `INT` interrupts the REPL's current computation on Unix (Windows ignores it), and other signals go to the REPL as for exec. If the REPL exits, the session prints its exit code on stderr and exits 1, and the next session starts a new REPL. Without a REPL the subsystem exits 1 with the reason.

**Forwarding.** `direct-tcpip` dials through the node's policy (see Network policy). A refused address is `administratively prohibited` with the policy's message, and a failed connect is `connect failed`. Without a policy every target is refused. Remote forwarding (`tcpip-forward`), agent and X11 forwarding are refused.

## Development

Go 1.26 and Node ≥ 24 (for the lint tools). `npm run build` writes `bin/slicc`. `npm run lint` runs the slicc lint tools, `gofmt`, `go mod tidy -diff`, `go vet` and golangci-lint. `npm test` runs the integration tests in `test/integration/`, which build the binary and drive it. Unit tests stay next to the code and out of git (`npm run test:unit`). Releases are cut by semantic-release on `main`.
