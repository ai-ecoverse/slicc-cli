# slicc-cli

`slicc` is SLICC's command line for seven, the SLICC on `*.sliccy.ai`. It is one binary for macOS, Windows and Linux. It launches and links browsers, and shares a machine's network, compute and browsers with a running seven over a WebRTC link. `npx sliccy` and Sliccstart.app run this binary. It replaces slicc-node and slicc-swift.

**Status:** being built. `--version`, `--help` and `update` work so far, plus a hidden diagnostic, `slicc <join-url> follow --signal-only [--timeout 2m]`. It attaches through the tray hub, prints a summary of the leader's offer (its size, media sections, candidate count and DTLS fingerprint) and the ICE server URLs (credentials are never printed), and exits without answering. The link protocol is drafted alongside seven's link exit and moves into this README once both sides pass a shared test.

## Commands (planned)

| command | what it does |
| --- | --- |
| `slicc` | pick browsers in a small TUI; Enter launches the best match with CDP, opens seven and links it |
| `slicc attach --cdp <url>` | link a seven that is open in an already-running CDP browser |
| `slicc launch` | launch a browser with CDP in a separate SLICC profile and print its CDP URL |
| `slicc <join-url> follow [--exit] [--runner …] [--cdp <url>]` | follow a seven's tray from another machine and offer its network, compute (over SSH) or browsers |
| `slicc browsers --json` | list running CDP endpoints and installed browsers, ranked |
| `slicc update [--check]` | replace this binary with the latest release (works now; see [Updates](#updates)) |

## Install

- **npm:** `npm install -g @ai-ecoverse/slicc`, or `npx @ai-ecoverse/slicc`. The package is a small Node launcher; npm picks one of the platform packages `@ai-ecoverse/slicc-<os>-<arch>` (`darwin`, `linux`, `windows` × `amd64`, `arm64`) through `optionalDependencies` and their `os` and `cpu` fields. The launcher runs that binary with the same arguments, stdio and environment plus `SLICC_LAUNCHER=npm`, forwards `SIGTERM`, `SIGHUP`, `SIGQUIT` and `SIGUSR2` to it, and `SIGINT` too when stdin is not a terminal (a terminal's Ctrl-C already reaches the binary; on Windows the console delivers Ctrl-C and Ctrl-Break to both). It exits with the binary's exit code, or dies of the same signal. An install with `--omit=optional` has no binary, and the launcher says so.
- **GitHub releases:** every release of `ai-ecoverse/slicc-cli` carries `slicc-<os>-<arch>` (`.exe` on Windows) for all six targets, plus `SHA256SUMS`. Check a download with `shasum -a 256 -c SHA256SUMS --ignore-missing` and `gh attestation verify slicc-linux-amd64 --repo ai-ecoverse/slicc-cli`.

Signing:
- **macOS:** the binaries are signed with a Developer ID, with the hardened runtime, and notarized. A bare binary can't carry a stapled ticket (only apps, disk images and installer packages can), so Gatekeeper checks the notarization online the first time it runs. Inside Sliccstart.app the binary is stapled with the app.
- **Windows:** both binaries are Authenticode-signed through Azure Trusted Signing.
- **Every binary:** a GitHub build-provenance attestation (Sigstore) and an entry in `SHA256SUMS`.

## Updates

`slicc update` replaces the running binary (symlinks resolved) with the latest GitHub release of `ai-ecoverse/slicc-cli`. It downloads the asset for this OS and CPU and checks it against the release's `SHA256SUMS`. The new binary has to answer `--version` with the release's version before anything is replaced. The swap is a rename next to the old binary; on Windows the running binary is first moved aside to `<name>.old`, which the next run deletes. `slicc update --check` only reports.

`update` refuses, with exit code 1:
- a development build (any version that isn't plain `X.Y.Z`); `--check` still prints the latest release;
- an npm install (`SLICC_LAUNCHER=npm`, or a path through `node_modules`), which updates through npm;
- a binary inside an app bundle (`*.app/Contents/…`), which the app updates.

Release builds outside npm and app bundles print a notice on stderr when a newer release exists: ``slicc X is available (you have Y); run `slicc update` ``. The notice comes from a cache (`<user cache dir>/slicc/update-check.json`), and a background check refreshes it at most once every 24 hours, so no run waits on the check for more than 2 seconds. `--version`, `--help` and `update` print no notice.

## Telemetry

Release builds send operational telemetry through [go-optel](https://github.com/ai-ecoverse/go-optel) with the app id `slicc-cli`, sampled once per process (1 in 100 by default):
- `enter`, with the subcommand as `source`, from a fixed list (`attach`, `browsers`, `follow`, `launch`, `prompt`, `update`, `watch`, `default` for no arguments, `join` for a bare join URL, otherwise `unknown`). No other argument is ever sent.
- `error`, for operational failures, from a fixed set of sources (`update`, `signal`, `link`, `browser`, `ssh`, `cdp`). Messages pass through go-optel's sanitizer, which strips URLs and paths.

`--version` and `--help` send nothing, and development builds never configure a client.

## Environment

| variable | effect |
| --- | --- |
| `SLICC_NO_TELEMETRY=1` | send no telemetry |
| `SLICC_NO_UPDATE_CHECK=1` | no update notice and no background check |
| `SLICC_UPDATE_API_BASE` | GitHub API base for `update` and the notice (default `https://api.github.com`), for mirrors and tests |
| `SLICC_TELEMETRY_URL` | telemetry collector base (default `https://rum.hlx.page/`) |
| `OPTEL_RATE`, `OPTEL_DEBUG` | go-optel's sampling rate and beacon logging |
| `SLICC_LAUNCHER` | set to `npm` by the npm launcher; removed from the environment that `slicc` passes on |

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
- The end: `exit-status`, or `exit-signal` for a Unix command killed by one of the signals above (`exit-status` 128+n for any other). After the command exits, its output is read until EOF or 2 s of silence, so a background job that keeps stdout open doesn't hold the session. A session closed while its command runs sends it `HUP`, and stopping the server sends `KILL` to every running command. On Windows each command runs in its own job object, so ending it ends everything it started, and anything it left running ends with the session.

**`sftp` subsystem** (`pkg/sftp`): this machine's files as the user who started `slicc`, starting in the home directory. A runner that sandboxes commands (`docker exec …`) doesn't sandbox `sftp`, so `sftp` is on by default only when the runner's executable is a plain local shell: `bash`, `sh`, `zsh`, `fish`, `pwsh`, `powershell` or `cmd`, by basename, ignoring case and `.exe`. When it's off, the subsystem request is refused.

**`slicc-eval` subsystem**: one persistent REPL per node (for example `python3 -i`), started on first use and shared by every session, so state survives between them. Input is read in lines. The lines available at once are written to the REPL together, and its output is forwarded (stdout and stderr apart) until it has been quiet for 500 ms. At EOF the session exits 0. `INT` interrupts the REPL's current computation on Unix (Windows ignores it), and other signals go to the REPL as for exec. If the REPL exits, the session prints its exit code on stderr and exits 1, and the next session starts a new REPL. Without a REPL the subsystem exits 1 with the reason.

**Forwarding.** `direct-tcpip` dials through the node's policy (see Network policy). A refused address is `administratively prohibited` with the policy's message, and a failed connect is `connect failed`. Without a policy every target is refused. Remote forwarding (`tcpip-forward`), agent and X11 forwarding are refused.

## Tray hub signaling (remote setup)

A node on another machine sets up its link through the tray hub, the `slicc-tray-hub` worker on `www.sliccy.ai`. seven is the tray leader and the node is a follower. The wire contract is six's `packages/shared-ts/src/tray-signaling.ts`. The node only signals over it and never speaks six's tray-sync messages.

**Join URL.** `https://<host>/join/<token>`. Plain `http` is accepted only for loopback hosts. The join URL is the secret: the node never prints it, its token, or the token's secret part, in output, logs or errors.

**Attach.** `POST <join-url>` with `{ "controllerId", "runtime": "slicc-link/1" }`. The runtime tag lets seven tell link nodes from six's followers; the hub passes it on in `follower.join_requested`, and the node sends it again on `retry`. `wait` is retried after `retryAfterMs` (clamped to 50 ms–30 s) with the same controller id. `signal` starts a bootstrap and carries the TURN and STUN `iceServers` for the peer connection. `fail` (`INVALID_JOIN_CAPABILITY`, `TRAY_EXPIRED`) is terminal.

**Supersede.** The node does not follow HTTP redirects itself. A superseded tray's replacement is read from the `Link` header (`rel="successor-version"`), then a 3xx `Location` (with `json` dropped from its query), then the body's `result.joinUrl` when `code` is `TRAY_SUPERSEDED`. The 308 `redirect` and the older 409 `fail` shapes are both accepted. A replacement must itself be a valid join URL. Each hop uses a fresh controller id, and a dial follows at most five hops before it fails with `TRAY_SUPERSEDED_CHAIN_EXHAUSTED`. A supersede seen during a bootstrap ends it with `TRAY_SUPERSEDED`, and the next dial starts at the replacement.

**Bootstrap.** The node polls `{ "action": "poll", "controllerId", "bootstrapId", "cursor" }` once a second, and takes the leader's `bootstrap.offer` and `bootstrap.ice_candidate` events in order. It posts `answer`, then its own `ice-candidate`s. Candidates gathered before the answer is posted are held and sent right after it. A `bootstrap.failed` that is `retryable`, with `retriesRemaining` above 0, is retried with `{ "action": "retry" }` after its `retryAfterMs`. That mints a new bootstrap, so the peer connection starts over. Any other failure ends the bootstrap. When no offer arrives within 30 s, the bootstrap ends with `OFFER_TIMEOUT`.

**Requests.** Each HTTP attempt has a 30 s timeout. A logical request gets 8 s and up to four attempts. Network errors, HTTP 5xx and non-JSON bodies are retried with a backoff of 1 s, 2 s, then 4 s. JSON refusals below 500 are not retried.

**Join URL state.** The node remembers the current join URL for each join URL it was given, so it reconnects after a network change or restart without walking the supersede chain again. The state lives in `<config>/slicc/tray-join.json`, where `<config>` is `~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux, and `%AppData%` on Windows. The directory is mode `0700` and the file `0600`, written atomically. Entries are keyed by the SHA-256 of the given join URL, and at most 16 are kept.

## Development

Go 1.26 and Node ≥ 24 (for the lint tools). `npm run build` writes `bin/slicc`. `npm run lint` runs the slicc lint tools, `gofmt`, `go mod tidy -diff`, `go vet` and golangci-lint. `npm test` runs the integration tests in `test/integration/`, which build the binary and drive it. Unit tests stay next to the code and out of git (`npm run test:unit`). `npm run dist` cross-builds all six targets into `artifacts/release/` with `SHA256SUMS`, unsigned. `npm run npm:stage` then stages the npm packages into `dist/npm/`, and `npm run npm:check` packs them with `npm publish --dry-run`.

## Releases

semantic-release cuts releases from `main` in `.github/workflows/release.yml`, on a macOS runner:
1. `scripts/release-plan.mjs` asks semantic-release (dry run) for the next version, and stops when there is none.
2. `scripts/package.sh <version>` builds `darwin`, `linux` and `windows` × `amd64`, `arm64` with `CGO_ENABLED=0`, `-trimpath` and `-X main.version=<version>`. It signs and notarizes the darwin binaries, signs the windows binaries with jsign against Azure Trusted Signing, and writes `SHA256SUMS`. Without the signing secrets (forks, pull requests) it warns and leaves those binaries unsigned.
3. `scripts/npm-stage.mjs` stages `@ai-ecoverse/slicc` and the six platform packages. `--name sliccy` stages the same launcher as `sliccy`, with a `sliccy` bin.
4. `actions/attest-build-provenance` attests every binary.
5. semantic-release checks the version again, tags, creates the GitHub release with every binary and `SHA256SUMS`, then `scripts/npm-publish.mjs` publishes the platform packages and the launcher through npm trusted publishing (OIDC, with provenance). Versions already on npm are skipped.

On pull requests the same workflow runs as a dry run: an unsigned build of all six targets, the npm packages packed with `--dry-run`, and each binary run with `--version` on its own OS and CPU.

The workflow reads these repository secrets. Each group is optional, and a missing group leaves its binaries unsigned:
- macOS signing: `APPLE_CERTIFICATE_BASE64`, `APPLE_DEVELOPER_CERTIFICATE_PASSWORD`;
- notarization: `APPLE_API_KEY_ID`, `APPLE_API_KEY_ISSUER_ID`, `APPLE_API_KEY_P8_BASE64`;
- Windows signing: `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_TRUSTED_SIGNING_ENDPOINT`, `AZURE_TRUSTED_SIGNING_ACCOUNT`, `AZURE_TRUSTED_SIGNING_PROFILE`, and `AZURE_CLIENT_SECRET` unless the app registration trusts this workflow through a federated credential (OIDC).

npm needs no token. `npm run trust` (fledgling) configures trusted publishing for the seven packages listed under `fledgling.include` in `package.json`.
