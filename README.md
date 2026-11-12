# slicc-cli

`slicc` is SLICC's command line for seven, the SLICC on `*.sliccy.ai`. It is one binary for macOS, Windows and Linux. It launches and links browsers, and shares a machine's network, compute and browsers with a running seven over a WebRTC link. `npx sliccy` and Sliccstart.app run this binary. It replaces slicc-node and slicc-swift (see [Coming from slicc-node](#coming-from-slicc-node)).

**Status:** being built. `slicc` (the default flow), `slicc attach` (with the `cdp` capability), `slicc open`, `slicc pair` and `slicc unpair` (seven in your everyday browser, without CDP), `slicc <join-url> follow`, `slicc tray` (on a machine and inside seven), remote control with `prompt`, `watch`, `wait` and `abort` (also from inside seven's kernel, see [Inside seven](#inside-seven)), seven's browser as a CDP endpoint here with `slicc <join-url> cdp`, host folders with `--mount`, `slicc browsers`, `slicc launch`, `update`, `install`, `--version` and `--help` work so far, plus a hidden diagnostic, `slicc <join-url> follow --signal-only [--timeout 2m]`. It attaches through the tray hub, prints a summary of the leader's offer (its size, media sections, candidate count and DTLS fingerprint) and the ICE server URLs (credentials are never printed), and exits without answering. The link protocol is specified in [Link protocol](#link-protocol).

## Commands (planned)

| command | what it does |
| --- | --- |
| `slicc` | pick browsers in a small TUI; Enter launches or attaches to the best match, opens seven and links it. With no SLICC profile and no CDP endpoint, its best match links seven in the default browser instead, and without a terminal slicc does that by itself (works now; see [The default](#the-default-slicc)) |
| `slicc attach --cdp <url>` | link a seven that is open in an already-running CDP browser (works now; see [Local setup](#local-setup-slicc-attach)) |
| `slicc open`, `slicc --default-browser`, `slicc pair` | link a seven in your everyday browser, without CDP: open or print a pairing URL (works now; see [Linking seven in your everyday browser](#linking-seven-in-your-everyday-browser)) |
| `slicc unpair [<fingerprint> \| --all] [--json]` | list or forget the sevens paired with `open` or `pair`, and close their live links |
| `slicc tray [--json] [-q] [-a]` | list the `slicc` processes on this machine that hold a link: mode, seven, device, state and offers (works now; see [Running links](#running-links-slicc-tray)) |
| `slicc launch` | launch a browser with CDP in a separate SLICC profile and print its CDP URL |
| `slicc <join-url> follow [--exit] [--runner …] [--cdp <url>] [--mount <path>]` | follow a seven's tray from another machine and offer its network, compute (over SSH), browsers or folders (`--exit`, `--runner`, `--cdp` and `--mount` work now; see [Remote setup](#remote-setup-slicc-join-url-follow)). `-` or `@<file>` in place of `<join-url>` reads it from stdin or a file, out of sight of `ps` |
| `slicc <join-url> prompt <text>`, `watch`, `wait`, `abort` | send a prompt to seven's agent and stream its answer, follow the agent as it works, wait until it has nothing left to do, or abort its run; `--cdp <url>` instead of a join URL links locally (works now; see [Remote control](#remote-control-slicc-prompt-watch-wait-and-abort)) |
| `slicc <join-url> cdp [--port <n>] [--json]` | serve seven's browser as a CDP endpoint on this machine's loopback, for stock Playwright, Puppeteer and other CDP clients, through seven's guard; `--cdp <url>` instead of a join URL links locally (see [Driving seven's browser from another computer](#driving-sevens-browser-from-another-computer-slicc-join-url-cdp)) |
| `slicc <join-url> agents [--watch] [--json]` | list seven's agents, the active cone and its live scoops, with their status, queue and model, once or as they change; `--cdp <url>` instead of a join URL links locally (see [Agent inventory](#agent-inventory-slicc-agents)) |
| `slicc <join-url> prompt`, `watch`, `wait`, `abort` inside seven | the same commands from seven's kernel, as `@ai-ecoverse/wasi-slicc` (works now; see [Inside seven](#inside-seven)) |
| `slicc tray [url \| rotate \| unlink <device> \| retry]` inside seven | this seven's tray: its state and devices, the join URL on request, rotate it, drop a device, reconnect (see [Inside seven](#inside-seven)) |
| `slicc browsers --json` | list running CDP endpoints and installed browsers, ranked |
| `slicc update [--check]` | replace this binary with the latest release (works now; see [Updates](#updates)) |
| `slicc install jerk` | install slicc-jerk into slicc's data folder (works now; see [Companion tools](#companion-tools)) |
| `slicc capabilities --json` | what this slicc offers a program that drives it: its commands, events and control ops (works now; see [Driving slicc from a program](#driving-slicc-from-a-program-slicc-capabilities)) |

## Install

```
curl -fsSL https://get.sliccy.ai/slicc | sh
```

This installs `slicc` on macOS and Linux; see [Install with curl](#install-with-curl). Options pass through to the script: `curl -fsSL https://get.sliccy.ai/slicc | sh -s -- --help`.

Install and launch in one step: `slicc`'s own arguments follow the script's options, as in `curl -fsSL https://get.sliccy.ai/slicc | sh -s -- <join-url> cdp --port 9333`, which installs `slicc` and then runs `slicc <join-url> cdp --port 9333` (the join URL goes to it on stdin). On Windows, `npx sliccy@next <join-url> cdp --port 9333` is already one step. If `get.sliccy.ai` can't be reached, use the GitHub release URL directly:

```
curl -fsSL https://github.com/ai-ecoverse/slicc-cli/releases/latest/download/install.sh | sh
```

The alternatives:

- **npm:** `npm install -g @ai-ecoverse/slicc`, or `npx @ai-ecoverse/slicc`. The package is a small Node launcher; npm picks one of the platform packages `@ai-ecoverse/slicc-<os>-<arch>` (`darwin`, `linux`, `windows` × `amd64`, `arm64`) through `optionalDependencies` and their `os` and `cpu` fields. The launcher runs that binary with the same arguments, stdio and environment plus `SLICC_LAUNCHER=npm`, forwards `SIGTERM`, `SIGHUP`, `SIGQUIT` and `SIGUSR2` to it, and `SIGINT` too when stdin is not a terminal (a terminal's Ctrl-C already reaches the binary; on Windows the console delivers Ctrl-C and Ctrl-Break to both). It exits with the binary's exit code, or dies of the same signal. An install with `--omit=optional` has no binary, and the launcher says so. slicc-jerk comes the same way; fnox doesn't (see [Companion tools](#companion-tools)).
- **GitHub releases:** every release of `ai-ecoverse/slicc-cli` carries `slicc-<os>-<arch>` (`.exe` on Windows) for all six targets, `slicc-wasip1-wasm.wasm` for seven's kernel (see [Inside seven](#inside-seven)), plus `install.sh` and `SHA256SUMS` (which lists `install.sh` and the `.wasm` too). See [Verifying a download](#verifying-a-download).
- **Go:** `go install github.com/ai-ecoverse/slicc-cli/cmd/slicc@latest`. See [Install with Go](#install-with-go).

### Install with curl

`install.sh` is a POSIX `sh` script, attached to every release and kept in the repo root. `https://get.sliccy.ai/slicc` redirects (`302`) to the latest release's copy, `https://github.com/ai-ecoverse/slicc-cli/releases/latest/download/install.sh`, so both one-liners run the same script:

```
curl -fsSL https://get.sliccy.ai/slicc | sh
curl -fsSL https://github.com/ai-ecoverse/slicc-cli/releases/latest/download/install.sh | sh
```

Options go after `sh -s --`, as in `curl -fsSL https://get.sliccy.ai/slicc | sh -s -- --help`. The script:
- takes `VERSION` (default: the latest release; `1.2.3` or `v1.2.3`) and `INSTALL_DIR` (default: `$HOME/.local/bin`) from the environment, or `--version X` and `--dir D` as flags (`curl -fsSL https://get.sliccy.ai/slicc | sh -s -- --version 1.2.3 --dir ~/bin`), and `--help`;
- supports `Darwin` and `Linux` on `x86_64`/`amd64` and `arm64`/`aarch64` (an x86_64 shell under Rosetta gets `arm64`); anything else exits 1 and points to npm, or on Windows to the release's `.exe`;
- downloads `slicc-<os>-<arch>` and `SHA256SUMS` from the release with `curl` or `wget`, checks the hash with `sha256sum` or `shasum -a 256`, and exits 1 without installing anything when the hash differs or `SHA256SUMS` has no line for the asset;
- copies the binary to a temporary file in `INSTALL_DIR` and sets mode `755`; it exits 1 and leaves any installed `slicc` alone when that file's `--version` fails, doesn't print `slicc <version>`, or, with a version asked for, names another version;
- renames it to `slicc`, so a re-run upgrades or downgrades in place;
- prints `installed slicc <version> at <path>` from that `--version`, and, when `INSTALL_DIR` isn't on `PATH`, one line with the `export PATH=…` to add;
- takes `slicc`'s own arguments after its options: the first argument that isn't one of its options, or everything after `--`, goes to `slicc`. Any other argument starting with `-` before them is an unknown option and exits 1, so `slicc`'s own flags go after `--` (`sh -s -- -- --cdp …`). Without `slicc` arguments nothing else changes;
- with them, after a successful install, `exec`s the installed `slicc` with them, and prints its own lines on stderr so that stdout is `slicc`'s alone. A first argument that is a join URL (anything with `://`) goes to `slicc` as `-`, one line on stdin, so it isn't in `slicc`'s argv; otherwise `slicc`'s stdin is `/dev/tty` when that opens, else `/dev/null`, since the script itself came on stdin. A failed install exits 1 without launching anything.

**The limit of the one-step form:** the join URL is in `sh`'s argv, where `ps` shows it to every user of the machine, while the script downloads and checks the binary, for a few seconds, until the `exec`. On a shared machine, install first and run `slicc @<file> cdp`, with the join URL in a file only you can read.

It never uses `sudo`, never edits shell startup files and sends no telemetry. The Linux binaries are static (`CGO_ENABLED=0`), so the libc doesn't matter. The macOS binaries are the signed and notarized ones; `curl` sets no quarantine flag. `SLICC_INSTALL_BASE_URL` replaces `https://github.com/ai-ecoverse/slicc-cli/releases` (assets from `<base>/latest/download/` or `<base>/download/v<version>/`), for tests only. A curl-installed `slicc` updates itself in place with `slicc update` (see [Updates](#updates)), or by running the one-liner again.

### Install with Go

`go install github.com/ai-ecoverse/slicc-cli/cmd/slicc@latest` puts `slicc` in `$GOBIN` (default `$(go env GOPATH)/bin`). It needs Go 1.26.5 or newer, the `go` line of `go.mod`. An older Go from 1.21 on fetches that toolchain itself unless `GOTOOLCHAIN=local` is set. `@v<version>` picks a release instead of the latest.

The binary is built from source on your machine. It has no Developer ID signature, no notarization and no build attestation. Its integrity comes from the Go checksum database (`sum.golang.org`), which pins the hash of every release's source. On macOS a binary you build yourself isn't quarantined, so Gatekeeper doesn't stop it. Signed binaries come from the GitHub releases, from npm (`npx sliccy@next`, `npx @ai-ecoverse/slicc`), and from `slicc update`.

A go-installed `slicc` reports its module version (`slicc 1.25.2` for `@v1.25.2`). It counts as a release build for [telemetry](#telemetry) and the update notice, but `slicc update` doesn't replace it; see [Updates](#updates).

## Coming from SLICC six

`npx sliccy` (`sliccy@latest`) is still SLICC six: its node-server, with six's UI on `www.sliccy.ai`. `npx sliccy@next` runs this binary, which links browsers to seven on `seven.sliccy.ai`. Six stays available as `npx sliccy@6`.

**Six's flags.** `slicc` with no command stops at a flag of six's node-server before it discovers or launches anything. So does an unknown command followed by one. It prints this line on stderr and exits 2:

```
slicc: <flag> is a SLICC six option. This is the new SLICC (seven). For SLICC six, run: npx sliccy@6 <your args>
```

- `<your args>` is the command line as given, with arguments quoted where needed, for a POSIX shell or, on Windows, for PowerShell. A join URL is shown as `<join-url>`, since it is never printed.
- `--flag value` and `--flag=value` both count.
- With no command, Go's flag syntax applies, so `-electron` counts as well.
- The first six flag is the one named.

The flags come from six's `packages/node-server/src/runtime-flags.ts`, at slicc `e44ee9d2c` (6.247.17):

| six flag | six's parser |
| --- | --- |
| `-v` | `runtime-flags.ts:159` |
| `--serve-only` | `runtime-flags.ts:163` |
| `--hosted` | `runtime-flags.ts:167` |
| `--kill` | `runtime-flags.ts:171` |
| `--install-cli` | `runtime-flags.ts:175` |
| `--computer-demo` | `runtime-flags.ts:179` |
| `--cdp-port` | `runtime-flags.ts:188` |
| `--log-level` | `runtime-flags.ts:196` |
| `--log-dir` | `runtime-flags.ts:203` |
| `--prompt` | `runtime-flags.ts:207`, `:256` |
| `--env-file` | `runtime-flags.ts:211`, `:258` |
| `--install-dir` | `runtime-flags.ts:219`, `:260` |
| `--lead` | `runtime-flags.ts:223`, `:333` |
| `--join` | `runtime-flags.ts:228`, `:333` |
| `--electron-app` | `runtime-flags.ts:237`, `:336` |
| `--electron` | `runtime-flags.ts:336` |
| `--cloud` | `cloud/dispatch.ts:108` (next to `runtime-flags.ts`) |

Six's `--version`, `version`, `--profile` and `--mount` are seven flags too, and keep their seven meaning. Six's `--mount <os-path>:<vfs-path>` is refused with a hint; see [Host folders](#host-folders).

**What changed:**

| six | seven's `slicc` |
| --- | --- |
| `npx sliccy` (six's UI on `www.sliccy.ai`) | `npx sliccy@next` (seven on `seven.sliccy.ai`); six stays at `npx sliccy@6` |
| a global `slicc` from `npm i -g sliccy` | `npm i -g sliccy@next` installs `sliccy` and `slicc`, and `@ai-ecoverse/slicc` installs `slicc`. Install only one of them globally, since both provide `slicc` |
| six's Go follower, also called `slicc` (`--install-cli`, `install-cli \| sh`) | whichever `slicc` comes first on `PATH` runs; `slicc --version` prints 6.x for six's |
| `--serve-only --cdp-port=9222` | `slicc --cdp http://127.0.0.1:9222`, or `slicc attach --cdp …` |
| `--mount=~/proj:/workspace/proj` | `--mount ~/proj[:proj][:ro]`, a name rather than a path; the folder is then mounted in seven |
| `--profile leader` (QA), `SLICC_USER_DATA_DIR` | `--profile <name>`, a SLICC profile (see [`slicc launch`](#slicc-launch---browser-id---profile-name---url-url---headless---no-seven-flags---json)); `--profile six` uses six's own Chrome profile in place, `SLICC_USER_DATA_DIR` included (see [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile)) |
| `--prompt "…"` | `slicc prompt --cdp <url> "…"` once the link is up |
| `--lead [url]`, `WORKER_BASE_URL` | `--page <url>` and `--origin <origin>` |
| `--electron <app>`, `--kill` | `slicc --browser chrome --browser electron:<slug>`; quit the app first |
| `--log-level`, `--log-dir` | `SLICC_DEBUG=1`, on stderr |
| `-v` | `-V` or `--version` |
| `slicc <url> follow bash -c` | `slicc <url> follow --runner bash -c` |
| `slicc <url> follow --eval python -i` | `slicc <url> follow --runner sh -c --eval python3 -i` |
| `follow --no-banner`, `--plain` | no banner; `--plain` exists on `slicc`, `prompt` and `watch` |
| `watch <scoop>`, `exec`, `wait`, `model`, `thinking`, `new-session`, `*-cloud`, `list-sessions`, `--cloud`, `--hosted`, `--install-cli`, `--computer-demo` | not available |
| `slicc update` (six's follower) | six's follower updates from `ai-ecoverse/slicc`, and this one from `ai-ecoverse/slicc-cli`; moving over means reinstalling |

**Environment variables.**
- **Gone:** `PORT`, `CHROME_PATH`, `SLICC_USER_DATA_DIR`, `SLICC_CDP_LAUNCH_TIMEOUT_MS`, `WORKER_BASE_URL`, `SLICC_TRAY_WORKER_BASE_URL`, `SLICC_SECRETS_FILE`, `E2B_API_KEY`, `SLICC_JOIN_FILE`, `CHROME_USER_DATA_DIR`, `SLICC_CHROME_GPU`, `SLICC_CHROME_MOCK_KEYCHAIN`, `SLICC_BRIDGE_TOKEN`, `SLICC_PROMPT_SETTLE`, `SLICC_ABORT_CONFIRM`, `SLICC_NO_TUI` (use `--plain`), `SLICC_LOG_LEVEL`, `SLICC_LOG_FORMAT`, `COLUMNS`, `SLICC_INSTALL_DIR`.
- **Kept:** `SLICC_DEBUG`, `SLICC_NO_TELEMETRY`, `SLICC_NO_UPDATE_CHECK`, `SLICC_UPDATE_API_BASE`, `OPTEL_RATE`, `OPTEL_DEBUG`, `NO_COLOR`.
- **New:** `SLICC_UPDATE_WEB_BASE`, `SLICC_TELEMETRY_URL`, `SLICC_LAUNCHER` (set by npm), `SLICC_DISCOVERY` (tests).

**Files.**
- **Not read:** `~/.slicc/secrets.env`, `~/.slicc/session-id`, `~/.slicc/logs/` and `~/.slicc/cloud-sessions.json`.
- **Moved into seven:** `~/.slicc/crontab` and `~/.slicc/webhooks/` now live inside seven's kernel, not on the host.
- **Update cache:** six's Go follower caches its update check in `<user cache dir>/slicc/update-check.json`, and this binary uses `update-check-seven.json` next to it. Both files have the same shape, so with one shared file, each binary would show the other's version and hold back the other's check.
- **Kept by this binary:** `<config>/slicc/` holds `ssh_host_ed25519_key`, `tray-certs.json`, `tray-join.json`, `loopback-identity.json`, `loopback-peers.json` and `seven-profiles/`.
- **Profiles:** six's Chrome profiles are `~/Library/Application Support/Slicc/profiles/browser-coding-agent-chrome[-<port>]` on macOS, `$XDG_STATE_HOME/slicc/profiles` on Linux and `%LOCALAPPDATA%\Slicc\profiles` on Windows (`chrome-launch.ts:119-148`). seven's live in `<config>/slicc/seven-profiles`, so a SLICC `--profile <name>` never opens six's logins. Only `--profile six` does, in place; see [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile). Discovery lists six's Chrome marked `six: true`, last, and never chooses it.

## Coming from slicc-node

`npx @ai-ecoverse/slicc-node` and slicc-swift ran a local HTTP proxy, which seven reached on loopback with the URL and key from the launch URL's `#proxy=…&key=…` fragment. `slicc` links seven over WebRTC instead (see [Link](#link)). On this machine the link is set up over CDP, or with `slicc open` over a loopback pairing listener (see [Linking seven in your everyday browser](#linking-seven-in-your-everyday-browser)), and from another machine through the tray hub. There is no proxy port, and no key: `slicc open`'s `#slicc-pair=…` fragment carries a one-time token and the node's certificate fingerprint, and its listener only sets up links. The other listener is the [kernel services](#kernel-services) one, which carries the browser's own connections to `*.kernel.localhost` into the page (`--no-kernel` turns it off).

| slicc-node | `slicc` |
| --- | --- |
| `npx @ai-ecoverse/slicc-node`, which opens seven in the default browser | `slicc open` (or `slicc --default-browser`) does that, without CDP, in Chrome, Edge or Firefox; Safari is expected not to work, untested (see [Linking seven in your everyday browser](#linking-seven-in-your-everyday-browser)). `npx sliccy@next`, or `slicc` from `curl -fsSL https://get.sliccy.ai/slicc \| sh` (see [Install with curl](#install-with-curl)). It launches or attaches to a Chromium browser over CDP and opens seven there (see [The default](#the-default-slicc)) |
| `--page <url>`, `--origin <url>` | `--page <url>`, `--origin <origin>` |
| `--port`, `--rotate-key`, `--ephemeral`, `--no-open` | not needed: there is no proxy port and no key. `slicc open` listens on 17117 or the next free port up to 17126, `slicc unpair` forgets paired sevens, and `slicc pair` doesn't open a browser |
| `--mount <path>[:<name>][:ro]` | the same `--mount` (see [Host folders](#host-folders)). In seven the folder is `<device>/<name>` (`mount -t hostfs lars-mbp/project /mnt/p`) rather than `<name>` |
| `--kernel-port <n>`, `--no-kernel` | the same flags (see [Kernel services](#kernel-services)) |
| `--cdp <url>`, the `/cdp` relay to a running browser | the [`cdp`](#cdp-browser-control) capability. The browser seven runs in is always listed. More come from `--browser` (with `--cdp <url>` for an endpoint) on `slicc`, `--cdp-extra <url>` on `attach`, and `--cdp <url>` on `follow`. `--cdp-extra` and `follow --cdp` must answer when they start |
| `--quiet` | no request log. `--plain` prints status lines, and `SLICC_DEBUG=1` logs each `http` stream's method, scheme and host |
| `/api/fetch-proxy` | `http`, `tcp`, `resolve`, `ws` and `udp` streams under the [local policy](#network-policy): the internet, this machine and the LAN, but not the browsers' debugging ports or `slicc`'s own ports |
| `/api/hostfs/*`, tokens re-granted after a `403` | `fs` streams, with no tokens. In seven, a mount has no medium while the link is down, and fills in when the link is back |
| `/api/kernel-tunnel` | `kernel` streams into the page |
| `/auth/callback`, `/api/oauth-state`, `/api/oauth-result` | not needed on `https://<label>.sliccy.ai`, where seven signs in through the relay itself. A seven on another origin needs slicc-extension to sign in |
| `startProxy`, `launchUrl`, `persistentKey` (JS) | no JS API |

**Which browser.** slicc-node opened seven in the default browser, in any profile. `slicc` does that too when it finds no SLICC profile and no CDP endpoint: plain `slicc` then offers the first way below as the picker's best match, or falls back to it by itself without a terminal, and `--no-fallback` turns that off (see [The default](#the-default-slicc)). `slicc` links seven in one of three ways:
- **Your everyday browser (Chrome, Edge or Firefox):** `slicc open` (or `slicc --default-browser`) opens seven in the default browser and pairs it over loopback, with no CDP and no remote debugging. `slicc pair` prints the pairing URL for a seven that's already open. Once paired, that seven links again by itself after a reload or a restart of `slicc`. The link has the local policy, folders, kernel services and compute, but no `cdp`, since slicc controls no browser there (see [Linking seven in your everyday browser](#linking-seven-in-your-everyday-browser)). Safari is expected not to work, though untested: Playwright's WebKit blocks the loopback request as mixed content, and there seven offers the tray path below instead (see [§1.3](#13-local-setup-without-cdp-loopback-signalling)).
- **A browser `slicc` controls over CDP**, with `slicc` itself or `slicc attach`, which also offers seven `cdp` for that browser:
  - **by default,** a browser in a SLICC profile (see [`slicc launch`](#slicc-launch---browser-id---profile-name---url-url---headless---no-seven-flags---json)). Its seven is separate from the one in your everyday profile, since seven keeps its files per browser profile.
  - **or a browser that is already running with CDP** (`--cdp`). That includes your everyday Chrome once its remote debugging is turned on at `chrome://inspect/#remote-debugging`. It is then listed as `wsOnly` (see [Browsers](#browsers)).
- **A seven on another machine**, in any browser, through the tray hub: in seven's Network panel, *Link a device*, then run `slicc <join-url> follow` on this machine (see [Remote setup](#remote-setup-slicc-join-url-follow)). That link has the remote policy (public addresses only, unless `--allow`) and no kernel services.

**Restarts.** slicc-node kept its key and port, so an open page reconnected after a restart. `slicc` sets the link up again instead:
- A browser that `slicc` launched runs in its own process group. It outlives a `slicc` that crashes or is killed.
- The next `slicc` finds that browser's seven tab and links it again, with `attached` and no `launched`. It finds the tab through the profile's `DevToolsActivePort`, or through `--cdp`. Given the same `--mount` and `--kernel-port`, the folders and kernel services come back too.
- Quitting `slicc` normally (`q`, Ctrl-C, `SIGTERM`, or the control socket's `stop`) closes the browsers that run launched. The control socket's `detach`, `SIGTERM` with `--keep-browsers`, or `d` in the dashboard, leaves them running, for the next run to link again (see [Running links](#running-links-slicc-tray)).
- `u` in the dashboard updates `slicc` and restarts it in place, without closing those browsers (see [Updating from the dashboard](#updating-from-the-dashboard)).
- Every new `slicc` process has a new `node` id, so seven sees a new link from the same machine.
- `attach` sets up again on every new seven context, and `follow` reconnects by itself, with the same device certificate.
- `slicc open` and `slicc pair` keep their certificate and port, so a paired page signals again after a restart, without a new pairing.

## Using SLICC six's Chrome profile

`slicc --profile six` and `slicc launch --profile six` use SLICC six's own Chrome profile, with its logins, cookies and extensions, in place: it is never copied, moved or migrated. `--profile six:<port>` picks the profile six uses for serve port `<port>`. Nothing else ever chooses six's profile: not the default, `--yes`, `--json`, `--browser` or a `--cdp` URL.

**Where it is**, as six resolves it ([ai-ecoverse/slicc](https://github.com/ai-ecoverse/slicc) at `65c5a79b4`, v6.247.17-8; the data is in [slicc-browsers](https://github.com/ai-ecoverse/slicc-browsers#slicc-sixs-profiles)' `catalog.json`, `profiles.six`):

| | where | six's code |
| --- | --- | --- |
| macOS | `~/Library/Application Support/Slicc/profiles/<name>` | `packages/node-server/src/chrome-launch.ts:124-126`; `packages/swift-server/Sources/Browser/ChromeLauncher.swift:314-322` |
| Linux | `$XDG_STATE_HOME/slicc/profiles/<name>`, else `~/.local/state/slicc/profiles/<name>` | `chrome-launch.ts:127-130` |
| Windows | `%LOCALAPPDATA%\Slicc\profiles\<name>`, else `~\AppData\Local\Slicc\profiles\<name>` | `chrome-launch.ts:131-134` |
| `<name>` | `browser-coding-agent-chrome` for `six` and `six:5710`, `browser-coding-agent-chrome-<port>` for `six:<port>` | `chrome-launch.ts:27`, `:147`; `ChromeLauncher.swift:5-6`, `:323` |
| `SLICC_USER_DATA_DIR` | when set and not blank, trimmed, it is six's profile for every port | `chrome-launch.ts:143-146` (node-server only) |
| six's own launch | `--remote-debugging-port=9222` by default, on that profile | `chrome-launch.ts:283`, `packages/node-server/src/runtime-flags.ts:47`; `ChromeLauncher.swift:213` |

A profile that doesn't exist is an error (`SLICC six's Chrome profile doesn't exist; start SLICC six once, or pick another --profile`); slicc never creates it. A path is six's when it is one of these dirs, compared case-insensitively and after resolving symlinks, so a symlink to six's profile, or a symlinked root, counts too.

**The lock comes first.** Chrome's lock is `SingletonLock` on macOS and Linux (a symlink to `<host>-<pid>`; live when that process is), and `lockfile` on Windows (live when it can't be opened exclusively; slicc opens it for reading and closes it at once).
- **Live and debuggable**, when the profile's `DevToolsActivePort` answers `/json/version` with its websocket path (usually six's 9222), or, when that file is missing or stale, a `--remote-debugging-port` on the command line of a process running on that profile answers `/json/version`: slicc attaches to the running Chrome. `slicc` opens a seven tab in it (or uses one that is open) and links it, while six's node-server stays attached alongside. `slicc launch` prints its CDP URL, opens `--url` in a new tab when given, and waits.
- **Live and not debuggable:** slicc stops with exit 1: `six's Chrome is using this profile, and slicc can't reach its debugging port; close it, or use --browser/--profile to pick another`.
- **Free** (no lock, or a stale one): slicc launches the browser six used. That is the installed Chromium browser whose version is the profile's `Last Version` (or, without one, the version in `Local State`'s `user_experience_metrics.stability.stats_version`), the first in `slicc browsers`' id order when several match; otherwise Chrome stable. `--browser <id>` overrides it (for `slicc`, the first `--browser`; the others are extras as usual).

**Nothing is written to six's profile.** No seven prefs, no clean-exit marking, no removal of `DevToolsActivePort`, `Singleton*`, the session snapshot or any other file, no extensions added or removed, and no migration. The launch passes only runtime flags, which don't persist: `--remote-debugging-port=0 --user-data-dir=<six's profile> --no-first-run --no-default-browser-check --enable-features=ForceForegroundPriorityForAllTabs --silent-debugger-extension-api`, the throwaway flags only when six's profile is in the temp dir (see [`slicc launch`](#slicc-launch---browser-id---profile-name---url-url---headless---no-seven-flags---json)), the seven flags (left out with `slicc launch --no-seven-flags`), `--headless=new` with `--headless`, and the start page. The seven flags include `--disable-blink-features=AutomationControlled`, so pages in six's profile see `navigator.webdriver` `false` too: like the rest, it is a runtime flag and changes nothing in the profile. A `DevToolsActivePort` that was there before the launch is ignored until Chrome writes a new one.

**What does persist** is only what Chrome itself keeps: browsing data from the session, and the seven tab in session restore, so six may reopen that tab the next time it starts Chrome.

**Going back:** quit (`q`, Ctrl-C, or `slicc launch`'s Ctrl-C), then start six as usual. A Chrome slicc launched is closed as for any launch; one slicc attached to keeps running. Nothing needs undoing: slicc keeps no record that points into six's profile.

**Output.** `slicc --profile six` skips the picker and prints plain lines (or NDJSON with `--json`). When it attaches, the `foreign-browser` notice and event follow `attached`, with `profile` `six` (or `six:<port>`): `slicc: using Chrome (profile six) at http://127.0.0.1:9222, which slicc didn't launch; opened a new seven tab. …`. A launch reports `launched` with `browser` `six`, `profile` `six` and `profileDir`. `slicc launch --profile six` prints `attached` and `detached` events when it attaches (see [`slicc launch`](#slicc-launch---browser-id---profile-name---url-url---headless---no-seven-flags---json)). Errors about six's profile go to telemetry as fixed messages, never with a path.

**In the picker** a `six` row (`six:<port>` for a port variant) appears under `SLICC six profiles` when that profile exists, after the other sections (unless it was the last launch); the section header says it might lose data. It is never the best match and never preselected: Enter on it needs the cursor moved there, or the row selected with space. Picking it does the same as `--profile six`. A six endpoint in that profile is shown only through its row.

## Verifying a download

Put the binary and the release's `SHA256SUMS` in one folder, then check:
- **Checksum on macOS and Linux:** `shasum -a 256 -c SHA256SUMS --ignore-missing`. On Linux `sha256sum -c SHA256SUMS --ignore-missing` does the same.
- **Checksum on Windows (PowerShell):** `(Get-FileHash slicc-windows-amd64.exe -Algorithm SHA256).Hash.ToLower()` must equal the hash on that file's line in `SHA256SUMS` (`Select-String slicc-windows-amd64.exe SHA256SUMS`).
- **Provenance:** `gh attestation verify SHA256SUMS --repo ai-ecoverse/slicc-cli --signer-workflow ai-ecoverse/slicc-shared-web/.github/workflows/pipeline.yml` checks the GitHub build-provenance attestation (Sigstore) that the release made for `SHA256SUMS`, which then covers each file through its checksum. `slicc update` checks the same attestation itself (see [Attestations](#attestations)).

Signing:
- **macOS:** the binaries are signed with a Developer ID, with the hardened runtime, and notarized, by `rcodesign` on the release's Linux runner. A bare binary can't carry a stapled ticket (only apps, disk images and installer packages can), so Gatekeeper checks the notarization online the first time it runs. Inside Sliccstart.app the binary is stapled with the app.
- **Windows:** Windows binaries are unsigned; SmartScreen may warn on first run. AppLocker, WDAC or Intune policies can block them.

## Reproducing a release

Every release asset but the signed darwin binaries can be rebuilt byte for byte from its tag:
```sh
git clone https://github.com/ai-ecoverse/slicc-cli && cd slicc-cli && git checkout v<version>
SLICC_SIGN=0 scripts/package.sh <version>
gh release download v<version> --repo ai-ecoverse/slicc-cli -p SHA256SUMS -O - | diff - artifacts/release/SHA256SUMS
```
`diff` shows only the two darwin lines: every `linux`, `windows` and `.wasm` asset and `install.sh` is identical. `@ai-ecoverse/wasi-slicc`'s `bin/slicc.wasm` is `slicc-wasip1-wasm.wasm`. A `git archive` of the tag, or a checkout with tags made later, gives the same bytes:
- `package.sh` builds with `-buildvcs=false`, so Go stamps no tag, revision or `.git` state into the binary. Nothing reads it there: a release links its version with `-X main.version`, and only a binary with no linked version reads its Go build info (see [Updates](#updates)).
- It uses `-trimpath`, so the checkout's path doesn't matter.
- It runs the Go release that `go.mod` names (its `toolchain` line, else its `go` line, which then needs a patch version such as `go 1.26.5`; without one `package.sh` refuses to build) through `GOTOOLCHAIN`, downloading it when another Go is installed. It clears `GOFLAGS`, `GOEXPERIMENT`, `GOAMD64`, `GOARM64`, `GOWASM` and `GOFIPS140`, sets `GOENV=off` so no `go env -w` setting applies, and sets `GOWORK=off`, so the environment doesn't change the build. Its log names the Go it used.

The darwin binaries differ only by their signature: the release signs the same unsigned bytes. The integration test `TestReleaseBuildIsReproducibleFromAnyCheckout` (everywhere but Windows, so on Linux in CI, where the release builds too) builds a checkout tagged after its commit, and a `git archive` of it with `GOAMD64`, `GOARM64`, `GOFIPS140`, `GOFLAGS` and a `go env -w` file set, and requires identical `SHA256SUMS`.

Releases whose `package.sh` lacks `-buildvcs=false` were built with Go's VCS stamping, so their bytes depend on the tags present at build time: rebuild one from its commit with that tag and every later one deleted locally (`git tag -d`), as the release saw it, with the Go that `go.mod` names at that tag (`GOTOOLCHAIN=go<version>`) and a clean Go environment (`GOENV=off`, and none of the variables above set).

## Companion tools

slicc works with slicc-jerk (`jerk`), which runs on this machine and gets tokens for seven's vault. slicc finds it on `PATH` or in `bin` of its data folder: `~/Library/Application Support/slicc` on macOS, `$XDG_DATA_HOME/slicc` (default `~/.local/share/slicc`) on Linux, `%LOCALAPPDATA%\slicc` on Windows. That folder is never shared over `fs` (see [Host folders](#host-folders)). When its `bin` exists, slicc appends it to its own `PATH` at start, so its children (the SSH runner, for one) find jerk too; a copy already on `PATH` wins.

fnox (secrets on a real OS) is upstream jdx/fnox: install it from https://fnox.jdx.dev. slicc doesn't install it.

**With npm.** `@ai-ecoverse/slicc` and `sliccy` list `@ai-ecoverse/slicc-jerk` as an `optionalDependency` (`^` the latest release), but only when it is on the registry when the release is staged (`npm-stage.mjs --companions`); otherwise it is left out with a notice. The publish check that waits for the platform packages doesn't wait for it. When the package is installed, the launcher puts its `node_modules/.bin` first on `PATH` for the binary, so `jerk` resolves for slicc and its children. On Windows it puts the `bin` folder of `@ai-ecoverse/slicc-jerk-win32-<arch>`, which holds `jerk.exe`, there instead, and adds nothing when that package is missing.

**`slicc install jerk`** installs jerk into the data folder's `bin` (any other name, or none, exits 2 with `slicc install: name jerk` and the usage):
- `jerk` from the latest release of `ai-ecoverse/slicc-jerk` (0.6.0 or later, the first with the `--via-link` calls the link needs; an older latest release is refused with `slicc install: ai-ecoverse/slicc-jerk <version> is older than 0.6.0, the first release slicc works with`);
- **the release contract** (the same as slicc-cli's own, but for who attests): a plain binary `<name>-<os>-<arch>` (`.exe` on Windows; `darwin`, `linux`, `windows` × `amd64`, `arm64`), and `SHA256SUMS` with a line for it, with a build provenance attestation over the binary or over `SHA256SUMS` by one of the identities that step 5 of [Attestations](#attestations) lists for it;
- slicc checks the download against `SHA256SUMS` and verifies its attestation (see [Attestations](#attestations)), then installs it only once its `--version` names the release's version, through a rename next to the old one (as `slicc update` does);
- it says `installed <name> <version> at <path> (SHA256SUMS and the build provenance attestation by <identity>)`, or `<name> <version> is already installed at <path>`;
- `--checksum-only` skips the attestation, with a warning, and says `SHA256SUMS only; the attestation wasn't checked`;
- it finds the release, its files and its attestation as `update` does (see [Where the release comes from](#updates)), with the same `SLICC_UPDATE_WEB_BASE`, `SLICC_UPDATE_API_BASE` and token.

Only a user runs `slicc install`. Nothing on a link starts an install.

## Attestations

`slicc update` and `slicc install` verify the GitHub build provenance attestation of every binary they download, after its `SHA256SUMS` check, and refuse it when that fails. A release without an attestation fails too. `--checksum-only` is the only way around it, and it warns.

**Where the attestation comes from.** First the release's own `SHA256SUMS.sigstore.json`, the Sigstore bundle of the attestation over `SHA256SUMS` that the release pipeline attaches to the release after it attests (slicc-shared-web's `pipeline.yml` from ai-ecoverse/slicc-shared-web#470 on, once `ci.yml` pins it), downloaded like any release file, so no GitHub API call is needed. When the release has no such file (a release made before that, or in the minute before the pipeline attaches it) or its bundle doesn't verify, slicc asks `GET /repos/<repo>/attestations/sha256:<digest>` on the GitHub API, first for the binary's digest, then, when the binary has none, for the digest of the release's `SHA256SUMS`: an attestation over `SHA256SUMS` covers the binary through its line there, which slicc has already checked. Where the bundle comes from doesn't matter for trust: every bundle is verified as below, and slicc accepts the binary when one of them verifies. When none does, the error names the release's bundle as well as the API's answer.

**What is verified** (`internal/attest`, standard library only), for a Sigstore bundle v0.3 with one DSSE envelope, one Fulcio certificate and one Rekor (v1) entry:
1. the envelope is an in-toto statement whose predicate type is `https://slsa.dev/provenance/v1`, with a subject whose SHA-256 is the file's;
2. its single DSSE signature verifies with the certificate's key (ECDSA P-256 or P-384) over the DSSE pre-authentication encoding;
3. the certificate chains, as a code-signing certificate, to a Fulcio root in the trusted root that is valid at the entry's integrated time, and the certificate itself was valid then;
4. one of the certificate's embedded SCTs (`1.3.6.1.4.1.11129.2.4.2`) verifies with a certificate transparency log key in the trusted root, valid at the SCT's time, over the precertificate (the certificate body without its SCT list) and its issuer's key hash (RFC 6962);
5. the certificate's identity: issuer (Fulcio extension `1.3.6.1.4.1.57264.1.8`) `https://token.actions.githubusercontent.com`, and exactly one subject alternative name. For `update` (`ai-ecoverse/slicc-cli`) that is slicc-shared-web's release pipeline, `https://github.com/ai-ecoverse/slicc-shared-web/.github/workflows/pipeline.yml@refs/tags/v2.<minor>.<patch>`, and the certificate's build configuration (Fulcio extension `1.3.6.1.4.1.57264.1.18`) must be the caller, `https://github.com/ai-ecoverse/slicc-cli/.github/workflows/ci.yml@refs/heads/main`, so the pipeline run by another repo or from a branch doesn't count. For `install` (`ai-ecoverse/slicc-jerk`) it is one of `https://github.com/ai-ecoverse/slicc-jerk/.github/workflows/release.yml@refs/heads/main` (its releases before the shared pipeline), `https://github.com/ai-ecoverse/slicc-jerk/.github/workflows/ci.yml@refs/heads/main`, or the shared pipeline as for `update`, called from slicc-jerk's `ci.yml` at `main`; any other workflow, ref or repo is refused;
6. the Rekor entry is a `dsse` 0.0.1 entry whose payload hash, signature and verifier are the envelope's and the certificate's;
7. its signed entry timestamp verifies with the key of a Rekor log in the trusted root, valid at the integrated time;
8. its inclusion proof leads from the entry to the proof's root hash (RFC 6962), and the checkpoint carries that tree size and root hash and is signed by the same log.

Errors name the step that failed (`attestation identity: …`). A bundle from a Rekor v2 log, or any other shape, fails closed.

**Releases before the move to the shared pipeline** were attested by `ai-ecoverse/slicc-cli/.github/workflows/release.yml@refs/heads/main`. A binary from one of them refuses the later releases: update it once by hand, with `slicc update --checksum-only`, `install.sh`, or npm.

**Unsupported: Rekor v2.** Bundles from Rekor v2 logs (Ed25519 log keys, `dsse` 0.0.2 entries, RFC 3161 timestamps instead of signed entry timestamps) aren't supported. They fail closed, so `update` and `install` refuse such a release until the verifier learns them; `--checksum-only` is the way around it meanwhile.

**The trusted root.** `internal/attest/trusted_root.json` is Sigstore's public-good trusted root (Fulcio CAs, Rekor and CT log keys) (the `trusted_root.json` target of `https://tuf-repo-cdn.sigstore.dev`), embedded in the binary. `SLICC_SIGSTORE_TRUSTED_ROOT` replaces it, for tests.

**Rotating the trusted root.** The integration test `TestTheEmbeddedSigstoreTrustedRootIsCurrent` runs `node scripts/sigstore-root.mjs`, which follows the TUF repository's `timestamp.json`, snapshot and targets to the current `trusted_root.json` and compares its SHA-256 with the embedded file; when they differ, the test fails, so every pull request stays red until the root is re-embedded (it skips when the TUF repository doesn't answer, exit code 3). To rotate:
1. run `node scripts/sigstore-root.mjs --write`, which downloads the new file and checks it against the targets hash;
2. review the diff (new Fulcio certificates or Rekor keys, `validFor` ranges) against Sigstore's announcement; the script doesn't check TUF signatures, so this review is the trust decision;
3. open a `fix:` PR, so a release ships it. Binaries released before it keep the old root, and fail closed on attestations that need the new one until they update with `--checksum-only` or by hand.

## Updates

`slicc update [--check] [--checksum-only]` replaces the running binary (symlinks resolved), wherever it is, such as `~/.local/bin/slicc` from `install.sh`, with the latest GitHub release of `ai-ecoverse/slicc-cli`. It downloads the asset for this OS and CPU, checks it against the release's `SHA256SUMS`, and verifies its build provenance attestation (see [Attestations](#attestations)); `--checksum-only` skips the attestation, with a warning. The new binary has to answer `--version` with the release's version before anything is replaced. The swap is a rename next to the old binary; on Windows the running binary is first moved aside to `<name>.old`, which the next run deletes. When a `<name>.old` is still in use (a dashboard restarted after `u` keeps the old process waiting on the new one), it is moved to `<name>.old-<n>` instead, and the next run deletes those it can too. `slicc update --check` only reports.

**Where the release comes from.** The GitHub API allows 60 requests an hour per IP address without a token, which a shared network or CI runner uses up, so `update`, `install` and the notice don't need it. They ask `https://github.com/<repo>/releases/latest` without following its redirect, take the version from the `/releases/tag/v<version>` it points to (a tag that isn't `v` and dot-separated numbers is refused), and download `SHA256SUMS`, `<asset>` and `SHA256SUMS.sigstore.json` from `https://github.com/<repo>/releases/download/v<version>/`, the same files `install.sh` takes. A release whose `SHA256SUMS` has no line for this platform's asset is refused with `release <version> has no <asset>` before the asset is downloaded. Only the tag is read from the redirect; the download URLs are built from the base, never taken from the answer. When github.com doesn't name a release, they fall back to `GET /repos/<repo>/releases/latest` on the API. With `GH_TOKEN` (or else `GITHUB_TOKEN`) set, every API request carries it as a bearer token (5,000 requests an hour); it goes to `/repos/` on the API base only, never to github.com or a download. When the API answers 403 or 429 with no requests left, the error says `GitHub's API rate limit is used up`, and without a token adds `set GH_TOKEN or GITHUB_TOKEN for a higher one`.

**The version.** A release build has its version linked in (`-X main.version=<version>`). Without one, `slicc` reads its Go build info: a binary built by `go install …@<version>` (or `go run`) from a downloaded module, with a checksum in the build info, reports that module version without the leading `v`. Anything else built from a checkout reports `dev`.

`update` refuses, with exit code 1:
- a go-installed binary (no linked version, and a downloaded module with a checksum in its build info): it prints ``update it with `go install github.com/ai-ecoverse/slicc-cli/cmd/slicc@latest` ``;
- a development build (any version that isn't plain `X.Y.Z`); `--check` still prints the latest release;
- an npm install (`SLICC_LAUNCHER=npm`, or a path through `node_modules`), which updates through npm. The message names the commands: ``npm install -g @ai-ecoverse/slicc@latest`` and ``npx @ai-ecoverse/slicc@latest``, or `sliccy@next` for both when the binary came with `sliccy` (a `node_modules/sliccy` on its path, or next to its `node_modules`). Under npx (an `_npx` dir on the path) it names only the npx command;
- a binary inside an app bundle (`*.app/Contents/…`), which the app updates.

These checks come first, so `--check` refuses the same way and nothing asks GitHub.

Release builds outside npm and app bundles print a notice on stderr when a newer release exists: ``slicc X is available (you have Y); run `slicc update` ``. A go-installed release says ``run `go install github.com/ai-ecoverse/slicc-cli/cmd/slicc@latest` `` instead. The notice comes from a cache (`<user cache dir>/slicc/update-check-seven.json`; six's Go follower uses `update-check.json`, see [Coming from SLICC six](#coming-from-slicc-six)), and a background check refreshes it at most once every 24 hours, so no run waits on the check for more than 2 seconds. `--version`, `capabilities`, `update`, and any command line with `--help`, `-h` or `-help` print no notice.

### Updating from the dashboard

`u` in the dashboard updates `slicc` and restarts it, without closing the browsers it launched or the seven tab in them. It is offered only where `slicc update` would replace the binary: a release build, not go-installed, outside npm and app bundles. Elsewhere the key bar has no `u`.

1. **Update.** slicc finds the latest release, downloads it, checks it against `SHA256SUMS` and its attestation, and replaces the binary, exactly as `slicc update` does (there is no `--checksum-only` here). RECENT says `looking for a newer slicc`, then `updating slicc X to Y`. When slicc is up to date, or a step fails, RECENT says so (a failure in red) and the run goes on; `u` can be pressed again. While it runs the key bar reads `u updating…`, and another `u` does nothing.
2. **Detach.** Once the new binary is in place, RECENT says `updated to slicc Y (SHA256SUMS and attestation checked); restarting`, and slicc detaches as `d` does: `bye` to seven, every browser left running. A `q` or Ctrl-C that came first wins: slicc then quits as usual, closing its browsers, and doesn't restart; the new binary is still in place for the next run.
3. **Restart.** slicc runs the new binary with the same arguments. On macOS and Linux it replaces the process (`execve`), so the PID, the terminal and the parent stay; on Windows it starts the new binary as a child on the same console, waits for it, and exits with its exit code; the old binary, parked as `<name>.old` or `<name>.old-<n>`, stays in use until then, so a second `u` in the child parks the binary at the next free name (see [Updates](#updates)). The new process gets `SLICC_RESUME`, a JSON document of what to resume, and removes it from its environment before anything else, so nothing it starts inherits it:
   - **`browsers`:** the CDP URL and name of each browser the dashboard showed, also one launched and not yet attached, the one hosting seven first and none that was `gone`, and `launched` for the ones the old process launched (or adopted, see below).
   - **`pair`:** after `Default browser`, instead of `browsers`.
   - **`settings`:** the capabilities as the dashboard last applied them (shell, kernel port, folders, jerk), which take the place of `--mount`, `--kernel-port`, `--no-kernel` and `--no-jerk`, so changes made with `c` and `m` survive. Not after `Default browser`, whose capabilities come from the command line.
4. **Resume.** The new process adds those CDP URLs to discovery, skips the picker, and goes straight to the dashboard with those browsers, attaching to each one and linking the seven tab again, as a new `slicc` does after a `detach` (see *Restarts* in [Coming from slicc-node](#coming-from-slicc-node)). An extra that no longer answers is left out. When the browser that hosted seven doesn't answer, the picker comes up as usual. After `Default browser` it pairs again on the kept certificate and port, so the paired page signals again by itself.

**Adopted browsers.** A browser marked `launched` in `SLICC_RESUME` is adopted: the dashboard shows it as `launched`, `q` closes it (with CDP's `Browser.close`, since the new process has no handle on it), `d` and the next `u` leave it running, and `closed` lists it under `browsers` or `kept` by its endpoint id (`cdp:127.0.0.1:<port>`). So a run looks the same before and after `u`.

`SLICC_RESUME` is internal to the restart: it is read only by `slicc` with no command in the dashboard (a terminal and no `--yes`, `--browser`, `--plain` or `--json`), a value that isn't version 1 is ignored, and it grants nothing the command line can't: the same CDP URLs could be given with `--cdp`, the same folders with `--mount`.

## Telemetry

Release builds (a plain `X.Y.Z` version, linked in or, for `go install`, from the module version) send operational telemetry through [go-optel](https://github.com/ai-ecoverse/go-optel) with the app id `slicc-cli`, sampled once per process (1 in 100 by default):
- `enter`, with the subcommand as `source`, from a fixed list (`abort`, `attach`, `browsers`, `follow`, `install`, `launch`, `open`, `pair`, `prompt`, `unpair`, `update`, `wait`, `watch`, `default` for no command (no arguments, or only flags), `join` for a bare join URL, otherwise `unknown`). No other argument is ever sent.
- `error`, for operational failures, from a fixed set of sources (`update`, `signal`, `link`, `browser`, `ssh`, `cdp`). Messages pass through go-optel's sanitizer, which strips URLs and paths.

`--version`, `capabilities`, and any command line with `--help`, `-h` or `-help` (such as `slicc update --help`), send nothing, and development builds never configure a client.

## Environment

| variable | effect |
| --- | --- |
| `SLICC_NO_TELEMETRY=1` | send no telemetry |
| `SLICC_NO_UPDATE_CHECK=1` | no update notice and no background check |
| `SLICC_UPDATE_WEB_BASE` | GitHub base for `update`, `install` and the notice to find the latest release and download its files (default `https://github.com`), for mirrors and tests |
| `SLICC_UPDATE_API_BASE` | GitHub API base for `update`, `install` and the notice when the release or its attestation isn't found on the web base (default `https://api.github.com`), for mirrors and tests |
| `GH_TOKEN`, `GITHUB_TOKEN` | a token sent to the GitHub API (only) by `update`, `install` and the notice, for its higher rate limit (see [Updates](#updates)) |
| `SLICC_SIGSTORE_TRUSTED_ROOT` | a Sigstore `trusted_root.json` to verify attestations against instead of the embedded one, for tests (see [Attestations](#attestations)) |
| `SLICC_TELEMETRY_URL` | telemetry collector base (default `https://rum.hlx.page/`) |
| `OPTEL_RATE`, `OPTEL_DEBUG` | go-optel's sampling rate and beacon logging |
| `SLICC_LAUNCHER` | set to `npm` by the npm launcher; removed from the environment that `slicc` passes on |
| `SLICC_RESUME` | internal: what a dashboard restart after `u` resumes; set by `slicc` for the new binary and removed by it at start (see [Updating from the dashboard](#updating-from-the-dashboard)) |
| `SLICC_DISCOVERY` | test-only: limits browser discovery, see [Development](#development) |
| `SLICC_OPEN_COMMAND` | test-only: `slicc open` runs this program with the pairing URL as its only argument instead of opening the default browser; the integration tests always set it |
| `SLICC_PROTECTED_PORTS` | test-only: `<first>-<last>` (or one port), more [debugging ports](#network-policy) denied in every mode on top of 9222-9229 (never instead of them), so tests can prove the range on fake ports without touching the real ones. A malformed value exits 1 |
| `SLICC_CDP_GUARD_IDLE`, `SLICC_CDP_GUARD_LIMIT` | test-only: Go durations (such as `2s`) that shorten the [`cdp`](#cdp-browser-control) guard's idle bound (30 s) and outer bound (5 min) on a target's setup and on a session it doesn't know yet. A longer value counts as the default, and so does 0, a negative value or one that doesn't parse, so the variables can never widen or turn off the guard |
| `SLICC_CDP_EXTENSION_PAINT_HOLD` | test-only: a Go duration that shortens the 10 s hold on a `Page.captureScreenshot` reply while the extension-frame visibility probe runs. A longer value counts as the default, and so does 0, a negative value or one that doesn't parse |
| `SLICC_CDP_HOLD_TIMEOUT` | test-only: a Go duration that shortens the 10 s hold on an auto-attached target with no URL yet. A longer value counts as the default, and so does 0, a negative value or one that doesn't parse |
| `SLICC_CDP_OPENED_SEEN` | test-only: a positive integer that lowers the 4096 bound on stream-opened first-sight records kept for live tabs. A larger value counts as the default, and so does 0, a negative value or one that doesn't parse, so the variable can never widen the bound |
| `SLICC_LOOPBACK_ANSWER_DELAY` | test-only: a Go duration (at most 5 s) that the [loopback listener](#linking-seven-in-your-everyday-browser) waits after a signal's pin check and before it answers, so tests can unpair a page in between |
| `SLICC_LOOPBACK_PORTS` | test-only: `<first>-<last>`, the port range of the [loopback listener](#linking-seven-in-your-everyday-browser) instead of 17117-17126 |
| `SLICC_LINK_URL` | inside seven only: the link facade's address, `tcp://127.0.0.1:<port>` (see [Inside seven](#inside-seven)) |
| `SLICC_USER_DATA_DIR`, `XDG_STATE_HOME` | read, as SLICC six reads them, only to find six's Chrome profile (see [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile)) |

## Driving slicc from a program: `slicc capabilities`

```
slicc capabilities [--json]
```

`slicc capabilities --json` is the handshake of a program that drives `slicc`, such as a native app: one JSON document on stdout, from the binary's own tables, so it describes exactly the `slicc` it came from. It runs before anything else `slicc` does at start: it reads no files (not even slicc's data folder, which other commands look at for companion tools), asks no network, sends no telemetry and prints no update notice, so it works the same inside an app bundle, with no home directory at all. Without `--json` it prints the version, the contract and a table of the commands.

```
{"schema":1,"version":"<slicc version>","contract":1,"commands":{"<name>":{"json":"document"|"events"|"none","vocabulary"?:"events"|"agent"}},"events":{"schema":1,"names":[…]},"agent":{"events":[…],"own":[…]},"control":{"v":1,"ops":["status",…]}}
```

- **`schema`:** the shape of this document, 1.
- **`version`:** the version `slicc --version` prints.
- **`contract`:** 1. It goes up only for a breaking change to a contract this document describes: a field or event that changes meaning or goes away. New commands, fields, events and ops leave it alone.
- **`commands`:** every command, keyed by name: `default` for `slicc` with no command, `follow`, `prompt`, `watch`, `wait`, `abort`, `agents` and `cdp` (also after a join URL), `attach`, `open`, `pair`, `unpair`, `tray`, `browsers`, `launch`, `update`, `install`, `capabilities`, `version` and `help`. `json` says what `--json` prints: `document` (one JSON value), `events` (NDJSON, one event a line) or `none` (no `--json`). An `events` command has `vocabulary`: `events` for lines with `schema` and `event` (see [The default](#the-default-slicc) and [`slicc launch`](#slicc-launch---browser-id---profile-name---url-url---headless---no-seven-flags---json)), or `agent` for seven's agent events with `t` (see [`agent`](#agent-page-cap-agent-node--page)).
- **`events`:** `schema`, the `schema` every `event` line carries, and `names`, every `event` a `vocabulary: "events"` command can print, sorted.
- **`agent`:** `events`, the `t` of every agent event slicc knows, and `own`, the lines slicc itself adds to that stream (`settled`, `opened` and `reconnecting`, see [Remote control](#remote-control-slicc-prompt-watch-wait-and-abort)).
- **`control`:** `v`, the version of the [control socket](#running-links-slicc-tray) protocol, and `ops`, the ops it answers.

A program reads `contract` and the names it needs, and treats anything it doesn't know as absent: an older `slicc` lacks the newer commands, events and ops, and a newer one may have more. `TestCapabilitiesNameEveryCommandEventAndOp` fails when a command, an event or a control op is added to the code without appearing here, and keeps the document's fixture, [`test/integration/testdata/native/capabilities.json`](test/integration/testdata/native/capabilities.json), in step with the binary. The control socket's exchange, `status`'s `ops`, the answers to `stop` and `detach` and the `closed` that follows, is in [`control.ndjson`](test/integration/testdata/native/control.ndjson).

### Lifecycle events: `--json` on `attach`, `open`, `pair` and `follow`

With `--json`, `attach`, `open`, `pair` (and `--default-browser`, which is `open`), `follow` and `cdp` print the NDJSON of [the default's `--json`](#the-default-slicc) on stdout: one event a line, each with `"schema": 1` and `event`, from the same vocabulary. stdout carries nothing else: `open` and `pair` put the pairing URL in a `pairing` event instead of a line of its own. Status lines stay on stderr, and so does every error, which also comes as an `error` event. Fields marked `?` are left out when empty. New fields and events may be added, and existing ones keep their meaning. Without `--json` nothing changes.

- `connecting`: `mode`, `attempt?`. A dial or setup starts: for `attach`, the CDP connection (`mode` `cdp`), then each offer from seven; for `open` and `pair`, each offer (`loopback`); for `follow`, each dial of the tray hub (`tray`), with `attempt` from the first reconnect on, the `attempt` of the `reconnecting` before it.
- `pairing`: `url`, on `open` and `pair`, for the first pairing URL and each new one (Enter on stdin). It is never a join URL. The pairing URL holds a one-time token and works as a secret for 10 minutes; it is printed by design, as without `--json`.
- `linked`: `mode`, `peer?` (the name in seven's HELLO), `tab?` (the page's origin, for `attach`), `device?` (the page's certificate fingerprint, for `open` and `pair`), `caps`, `policy` (the summary), and `resources`, what this link offers, as seven sees it:
  - `browsers`: `[{"id","name","role","owned"}]`, the browsers listed for `cdp` (`role` `seven` or `extra`; `owned` is `true` for a browser this run launched, so never on these commands); `[]` without `cdp`;
  - `mounts`: `[{"name","path","readOnly"}]`, the folders shared over `fs`, with the resolved path on this machine;
  - `rejectedMounts`: `[{"path","reason"}]`, the `--mount` folders left out, with `path` as given and `reason` `not-a-folder`, `fenced` (slicc never shares it, see [Host folders](#host-folders)), `invalid-name` or `name-taken`;
  - `network`: `{"exit","policy"}`, whether `net` goes out from this machine, and the policy summary;
  - `compute`: `{"ssh","sftp"}`;
  - `jerk`: `{"offered","seal"}`, the `jerk` and `jerk-seal` caps;
  - `kernelPort?`: the local port of [kernel services](#kernel-services), when they are on.
- `link-lost`: `mode`, `reason?` when slicc or seven closed the link (`slicc is exiting`, seven's `bye` reason, `unlinked by <name>` with `code` `UNLINKED` on `follow`), or `message?` for a failure; `device?` on `open` and `pair`.
- `reconnecting`: `attempt`, `retryAt` (RFC 3339, UTC), `wait` (milliseconds), `reason?` and `code?` (the failure that led here, and the hub's code), on `follow` before each new dial. These are the `attempt`, `retryAt` and `code` of [`slicc tray --json`](#running-links-slicc-tray).
- `error`: `message`, `code?` (the hub's or seven's code, on `follow`), `mode?`, and `retryable`: `true` when slicc goes on by itself (`follow` reconnects, and an offer from seven that slicc can't answer leaves the run waiting for the next one), `false` when the run ends, with exit 1 (exit 2 for a bad `--origin` on `attach`).
- `credential`: `op`, `provider`, `leaseId`, `expiresAt?`, `by`, `upstream?`, for each credential lease this process serves over [`jerk`](#jerk-slicc-jerk-over-the-link): `op` `seal` (`by` `seven`) once a lease has gone to seven, `unseal` (`by` `seven`) once seven's `unseal` has dropped one, and `revoked` (`by` `host`) once the control socket's `unseal` has dropped one. `upstream` is `true` when jerk revoked the token at the provider and `false` when it didn't (a token jerk only read lives on there until it expires); it is left out when jerk doesn't say, as with a slicc-jerk older than 1.4.0. Never a credential, a scope or a rule.
- `listening`: `url`, on `cdp`, once the endpoint serves.
- `clients`: `count`, on `cdp`, each time the number of open WebSockets changes, `0` included.
- `closed`: `browsers: []`, the last event of every run, also one that failed or was interrupted, and `kept: []` after the control socket's `detach`.

Secrets stay out: no join URL, no tray token, and no `--cdp` path, since `resources` names browsers by id and errors are redacted as on stderr. Fixtures: [`attach.ndjson`](test/integration/testdata/native/attach.ndjson), [`credential.ndjson`](test/integration/testdata/native/credential.ndjson), [`pair.ndjson`](test/integration/testdata/native/pair.ndjson), [`follow.ndjson`](test/integration/testdata/native/follow.ndjson) and [`follow-error.ndjson`](test/integration/testdata/native/follow-error.ndjson), with values that change from run to run shown as `<…>`.

```
{"schema":1,"event":"connecting","mode":"tray"}
{"schema":1,"event":"linked","mode":"tray","peer":"seven","caps":["net","http","credentialed","ws","udp","fs","join"],"policy":"every public address","resources":{"browsers":[],"mounts":[{"name":"proj","path":"/Users/me/proj","readOnly":true}],"rejectedMounts":[],"network":{"exit":true,"policy":"every public address"},"compute":{"ssh":false,"sftp":false},"jerk":{"offered":false,"seal":false}}}
{"schema":1,"event":"link-lost","mode":"tray"}
{"schema":1,"event":"reconnecting","attempt":1,"retryAt":"2026-05-02T10:00:01Z","wait":1043}
{"schema":1,"event":"connecting","mode":"tray","attempt":1}
{"schema":1,"event":"error","message":"seven's tray has expired; get a new join URL from seven","code":"TRAY_EXPIRED","retryable":false}
{"schema":1,"event":"closed","browsers":[]}
```

## The default: `slicc`

```
slicc [--browser <id>]... [--cdp <url>]... [--page <url>] [--origin <origin>]... [--profile <name> | --profile six[:<port>]] [--headless]
      [--name <name>] [--kernel-port <n>] [--no-kernel] [--allow <rule>]... [--deny <rule>]... [--expose-debug-port <port>]... [--mount <path>[:<name>][:ro]]...
      [--yes] [--plain] [--json] [--no-fallback] [--open] [--default-browser] [--keep-browsers]
      [--runner <argv…> [--]] [--shell <argv…> [--]] [--eval <argv…> [--]] [--eval-quiet <dur>] [--sftp | --no-sftp]
```

`slicc` with no command (`npx sliccy`) finds browsers, launches or attaches to the best one, opens seven in it and links it. It serves the link until it is told to stop: `q` or Ctrl-C in the dashboard, or `SIGINT` or `SIGTERM`, or the control socket's `stop`. Then it says `bye` to seven, closes the browsers this run launched (as `slicc launch` does), leaves the browsers it attached to running, and exits 0. The control socket's `detach` (or `SIGTERM` with `--keep-browsers`) leaves the launched browsers running too (see [Running links](#running-links-slicc-tray)). If the browser that hosts seven goes away, `slicc` closes the rest and exits 0 too. Setup failures (no browser, a launch or attach that fails) exit 1, and bad flags exit 2. With `--no-fallback`, a run where nothing can host seven says ``no browser to control: run `slicc open` to link seven in your everyday browser`` and exits 1.

**The fallback to your default browser.** Plain `slicc` with no SLICC profile and no CDP endpoint falls back to [linking seven in your everyday browser](#linking-seven-in-your-everyday-browser), as `slicc pair` does. On a terminal it doesn't fall back by itself: the picker comes up, with the `Default browser` row first and the best match (see *The picker* below).
- **When:** stdin or stdout isn't a terminal, or `--yes`, `--plain` or `--json` was given; no `--browser`, `--cdp` or `--profile` was given and `--no-fallback` wasn't; and discovery found no running CDP endpoint (SLICC six's running Chrome counts too) and no installed Chromium browser with a profile in the SLICC profiles dir. A profile or endpoint, found or given, always wins, so the flow above runs instead. Since slicc never picks six's Chrome by itself, a run whose only endpoint is six's launches an installed browser in a new SLICC profile, or exits 1 with `no browser to control` when there is none.
- **What it says:** `No SLICC browser or debugging endpoint found, so seven opens in your default browser for pairing. Use --profile or --cdp to choose.` on stderr. With `--json` it adds `{"schema":1,"event":"mode","mode":"loopback","reason":"no-browser"}` after `discovered`.
- **The pairing URL:** when stdin and stdout are both terminals, slicc opens it in the default browser. Otherwise it only prints it, unless `--open` is given. Without `--json` it goes to stdout. With `--json` it comes as `{"schema":1,"event":"pairing","url":"<pairing URL>"}`, and so does each new one.
- **`--json` events** in this mode, with the shapes of the other modes plus `mode: "loopback"` and `device` (the page's certificate fingerprint):
  - `linked`: `peer?`, `caps` and `policy`, for each link that comes up;
  - `link-lost`: `reason?` or `message?`, for each one that goes;
  - `error` with `message` and `retryable: false` when the loopback setup fails (no free port, say), then `closed`;
  - `closed`, with `browsers: []`, last, when slicc exits.
- **Then** it serves loopback links with this run's `--page`, `--origin`, `--name`, `--allow`, `--deny`, `--expose-debug-port`, `--kernel-port`, `--no-kernel`, `--no-jerk`, `--mount` and compute flags, until `SIGINT` or `SIGTERM`.

**Discovery** is `slicc browsers`' discovery and ranking (see [Browsers](#browsers)), with each `--cdp` URL added and the `--page` origin counted as a seven origin.

**Going with a browser.** The first browser hosts seven:
- a running endpoint with a seven tab open: slicc attaches to it;
- a running endpoint without one: slicc opens `--page` in a new tab (`Target.createTarget`) and attaches;
- an installed browser: slicc launches it as `slicc launch --url <page>` does, in its SLICC profile (`--profile`, or the one picked with `p`; default: the browser id), with the seven flags and prefs, and `--headless=new` with `--headless`, then attaches.

Without `--browser`, the first row of the ranking that can host seven is chosen, an endpoint slicc didn't launch included, but never one in SLICC six's profile. When that happens without the picker, slicc says so on stderr, for an endpoint that isn't in a SLICC profile and that no `--cdp` named: `slicc: using Google Chrome (profile default) at http://127.0.0.1:9222, which slicc didn't launch; opened a new seven tab. Use --browser or --cdp to choose another.` It says `found a seven tab` when one was open already. The profile is `default` for a browser's default user-data dir, the base name of the user-data dir otherwise, and left out (with its parentheses) when unknown. With `--json` the line comes as the `foreign-browser` event too. Picking a row in the picker, `--browser`, or a `--cdp` for that endpoint means the user chose it, and there is no notice. An Electron app can't host seven. Each further browser is an extra: an installed one is launched without a start page, and an endpoint is used as it is. Then the link is set up as for `slicc attach` (see [Local setup](#local-setup-slicc-attach)): over CDP with the browser that hosts seven, the first page tab on an allowed origin.

**What the link offers:** `net` (tcp, resolve), `http`, `ws`, `udp`, and `cdp` with the browser that hosts seven (`seven: true`) and every extra, under the local policy with all of their debugging ports protected, and this machine's other debugging ports too (see [Network policy](#network-policy)); and kernel services on `--kernel-port` (default 80, or a spare port when this user can't have 80; `--no-kernel` turns them off) when seven offers `kernel-in` (see [Kernel services](#kernel-services)); and `ssh` only with `--runner` (see [SSH server](#ssh-server)); and `fs` with each `--mount` folder (see [Host folders](#host-folders)). seven gets full control of every listed browser, the one it runs in too. The `cdp` list (HELLO `browsers`) has only the browser that hosts seven and the extras picked with `--browser` or in the picker; the other browsers discovery found are never offered.

The cdp list follows the browsers: an extra that stops answering is taken off the list (`browser-gone`), and listed again with the same id when it answers on the same endpoint again (`browser-back`). An extra that never made the list, because it has no browser session (see [What a browser must offer](#cdp-browser-control)), gets one `browser-gone` with `reason` `no-browser-session` right after the link starts. slicc watches the browsers of a link only once it has said what that link offers (`offering …` in plain mode), so the dashboard's reset for a new link never clears a mark that link set. Plain mode says `seven can't drive <name> through slicc: it has no browser session, so slicc can't watch its tabs or keep seven's origin out of them. It isn't listed for cdp; the rest of the link works` for it, and the dashboard `<name> isn't listed for cdp: it has no browser session`, where an extra that stops answering gets `<name> quit; it is no longer listed for cdp` and `<name> quit`.

**Flags.**
- `--browser <id>`: an id from `slicc browsers` (an installed id such as `chrome`, or `cdp:<host>:<port>`), or a name, case-insensitively. Repeatable: the first hosts seven, the others are extras. Without it, the best match that can host seven. An endpoint in SLICC six's profile is refused (`<id> is SLICC six's Chrome; only --profile six uses it`).
- `--cdp <url>`: also consider this CDP endpoint, as `slicc browsers --cdp`.
- `--page <url>`: the seven page, default `https://seven.sliccy.ai/`, or `https://www.sliccy.ai/7/` when SLICC six's profile hosts seven (`--profile six`, or the six row in the picker), since that is where six upgrades in place: it offers the upgrade, or boots seven on `www.sliccy.ai` once the profile has upgraded. In that case slicc links only a tab under `https://www.sliccy.ai/7/` or `/os/`, and opens `/7/` when there is none: a `seven.sliccy.ai` tab left from an earlier run, or six's own UI elsewhere on `www.sliccy.ai`, is left alone. Navigation outside those paths stops signalling, including same-document route changes (`history.pushState`, `history.replaceState`, or a hash change). Returning to either path links it again with a fresh hello and nonce, even in the same document. Route changes within the allowed paths keep the current link; iframe route changes do not affect it. It must be `http` or `https`, and its origin is accepted as a seven origin.
- `--origin`, `--name`, `--allow`, `--deny`, `--expose-debug-port`, `--kernel-port`, `--no-kernel`, `--no-jerk`, `--mount`, and the compute flags `--runner`, `--shell`, `--eval`, `--eval-quiet`, `--sftp`, `--no-sftp`: as for `slicc attach`. Without `--runner` there is no `ssh`.
- `--profile <name>`: the SLICC profile for a launched browser that hosts seven. `--profile six` and `--profile six:<port>` use SLICC six's own Chrome profile instead, without the picker (see [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile)).
- `--headless`: launch browsers without a window.
- `--yes`, `--plain`: no picker. `--json`: NDJSON events, below.
- `--keep-browsers`: on `SIGTERM`, detach instead of stopping: say `bye` and leave the browsers this run launched running, as the [control socket](#running-links-slicc-tray)'s `detach` does. `SIGINT` and Ctrl-C still close them. Plain mode then ends with `slicc: detached; left <ids> running`. It needs `--plain`, `--yes` or `--json` (or no terminal): where the picker and dashboard would come up, `slicc --keep-browsers` says `--keep-browsers needs --plain, --yes or --json: the dashboard closes the browsers it launched when it quits` and exits 2, since the dashboard takes `SIGTERM` itself.
- `--no-fallback`: never fall back to the default browser. slicc then behaves as it did before the fallback: it launches an installed browser in a new SLICC profile, or exits 1 with `no browser to control` when nothing can host seven (see above). `--open`: when falling back without a terminal, open the pairing URL in the default browser too.
- `--default-browser`: don't look for browsers; link a seven in the default browser instead, exactly as `slicc open` does (see [Linking seven in your everyday browser](#linking-seven-in-your-everyday-browser)).
- A flag of SLICC six's points back to six and exits 2 (see [Coming from SLICC six](#coming-from-slicc-six)). Any other flag, or an argument, is an error.

**The picker** comes up when stdin and stdout are both terminals and none of `--yes`, `--browser`, `--plain` or `--json` is given. It shows the ranking in sections, each under a header that says what picking a row does: `Running` (CDP endpoints, attached over CDP), `Launch` (installed browsers slicc launches in a SLICC profile of its own, for testing SLICC seven), `SLICC six profiles` (re-use existing SLICC profile (might lose data), the one warning, in yellow) and `Pair` (seven in your everyday browser, linked over loopback without cdp), in the order their best row ranks. `Pair` has one row, `Default browser`, which links seven as `slicc open` does: it comes first and is the best match when plain `slicc` would otherwise fall back (see above), and follows the installed browsers when not. `--no-fallback`, `--cdp` and `--profile` leave it out, and it can't be selected together with other browsers. Electron apps and headless browsers follow as folded summary rows (`▸ 15 Electron apps  extras only · 1 blocks debugging`, `▸ 2 headless browsers`), opened and closed with Enter or space on the row, or with `e` and `h`; they start open when nothing else is listed. A row shows the name, the version (the major version for Chromium, major.minor otherwise; none on six's rows), its labels and, faint on the right, the port, or the browser a six profile opens in: `best match` on the row Enter takes, `last launched` (green), `seven is open`, `profile "<name>"` or `new profile "<name>"` when that SLICC profile doesn't exist yet, when a six profile was last used (`used 2 days ago`, from its `Local State`), or `running` and `seven is open` with the browser and port (`Google Chrome 155 at :9222`) when its Chrome runs, `headless`, `websocket only` and `blocks debugging`. Below the list a detail line starts with what Enter does with the highlighted row (`↵ Opens seven in Google Chrome 155 (create new Google Chrome profile "chrome" for testing SLICC seven)`, `↵ Links the seven tab in Google Chrome at :9222`, `↵ Re-uses existing SLICC profile six:51852 in Google Chrome 155 (might lose data)`, `↵ Links the seven tab in SLICC profile six, running in Google Chrome 155 at :9222`, `↵ Opens seven in your default browser and pairs it over loopback (no cdp: slicc controls no browser)`), with the profile or user-data path under it; with a selection it adds the extras (`…, with Slack and bb as extra browsers`). A browser that can't be selected says so in faint text below the list (`1Password blocks remote debugging, so it can't be picked`). Selected rows are marked `●1` (hosts seven), `●2`, … in selection order. The keys are listed at the bottom. With colour on, the highlighted row has a background bar, and the palette follows the terminal's light or dark background; on a light one faint text is a grey with at least 4.5:1 contrast. A running endpoint found in the process list is named after the app it runs from: the catalog name for a known browser, also for its helper processes (`Google Chrome for Testing` rather than `Chrome`), otherwise the macOS app bundle or the executable (`chrome-headless-shell`). A headless endpoint is one whose user agent says `HeadlessChrome` or whose process has `--headless`; unless it was named with `--cdp` (then it stays under `Running`, marked `headless`), the picker folds it into the dimmed headless rows after the installed browsers and never makes it the best match while anything else can host seven. `--yes`, `--plain` and `slicc browsers --json` keep the ranking below. The browser or SLICC six profile slicc last launched to host seven comes first, marked `last launched`, with the SLICC profile it used; slicc remembers it in `last-launch.json` next to `seven-profiles`. `--yes`, `--plain` and `--json` don't use it. A list longer than the terminal scrolls with the cursor.

| key | what it does |
| --- | --- |
| ↑ ↓ (`k`, `j`) | move |
| space | select or unselect the row (`●1` hosts seven, `●2`, … are `extra cdp` browsers, in the order selected); on a folded row, open or close it |
| Enter | go: with the selected rows, or with the highlighted row when nothing is selected; on a folded row with nothing selected, open or close it |
| `p` | choose the highlighted browser's SLICC profile: its default, an existing profile in the profiles dir, or `new profile…` with a name (letters, digits, `.`, `_`, `-`) |
| `e`, `h` | open or close the Electron apps and the headless browsers |
| Esc | back, in the profile chooser |
| `q`, Ctrl-C | quit; in the dashboard, close down as described above |
| `d` | in the dashboard: detach, as the control socket's `detach` does: say `bye` to seven, leave every browser running, the ones this run launched too, and exit 0 with `slicc: detached; left <ids> running; run slicc to link seven there again` on stderr (`slicc: detached` when it left none). Not on the dashboard after `Default browser`, which launches none |
| `u` | in the dashboard, also after `Default browser`: update `slicc` and restart it without closing its browsers (see [Updating from the dashboard](#updating-from-the-dashboard)). Offered only where `slicc update` would replace this binary |

**The dashboard** follows, in sections: BROWSERS (launched or attached, their CDP port, hosts seven or extra, `gone` while one doesn't answer, and `unlisted` for an extra left off the cdp list because it has no browser session; a row shows at most one of the two, and each new link, a relink too, clears both and marks what it reports again); SEVEN (the tab, the link state (`waiting for seven`, `connecting`, `linked` with its uptime, `reconnecting` after a link is lost, while slicc waits for seven to offer again, or `relinking` while new capabilities are applied), the policy summary, and stream counts per kind, open and total, never contents); CAPABILITIES (the network capabilities always offered, then `jerk`, `shell` with `ssh via <runner>`, `kernel` with its URL, or why it is off, and the shared folders); RECENT (the last status lines, links in green, losses in yellow, errors in red); and the update notice, which then isn't printed on stderr. Keys: `c` capabilities, `m` share a folder, `d` detach, `u` update (when offered), `q` quit. `NO_COLOR` turns colors off.

**The dashboard after `Default browser`** links seven over loopback, as `slicc open` does, and opens the pairing URL in the default browser. In place of BROWSERS it shows `Default browser  paired over loopback · no cdp  hosts seven`; SEVEN starts with the pairing URL (a link where the terminal supports it, wrapped to the terminal's width, good once, for 10 minutes) until seven links, then `↵ for a URL to pair another seven`. The other sections are as above. Its capabilities are those of the command line: the capabilities screen and `m` aren't offered, so changing them means restarting slicc. Keys: `↵` a new pairing URL, `y` copies the URL to the clipboard (OSC 52), `u` update (when offered), `q` quit.

**Capabilities at runtime.** `c` opens the capabilities screen: `jerk` (when slicc-jerk is installed), `shell` (the `--runner`; turning it on uses `$SHELL -c`, or `pwsh -Command` / `powershell -Command` on Windows), `kernel` (the `--kernel-port`, or off) and the shared folders, each `path[:name][:ro]` as for `--mount`, with tab completing folders. `space` turns a row on or off, `↵` edits it, `a` adds a folder, `r` makes a folder read-only, `x` removes it. Changes collect as pending until `s` applies them (`u` undoes them; leaving with `esc` asks first). Folder changes go live as a mount update (see [Host folders](#host-folders)) when folders are already shared and seven takes `mounts` updates. Every other change relinks: the node says `bye` with the reason `slicc is relinking`, the browsers and the CDP connection stay, and a new node with the new capabilities sends seven's tab a fresh `hello` with the same `node` id, so seven offers again and replaces the link. Streams open on the old link close. A runner changed in the TUI drops `--shell`, `--eval`, `--eval-quiet` and `--sftp` / `--no-sftp`, which belong to the runner given on the command line.

**Plain mode** (no terminal, `--yes`, `--browser` or `--plain`): no picker and no dashboard, just the default, with status lines on stderr (`slicc: <name> is still starting (slow disk?), <n> s so far`, `slicc: launched …`, `slicc: attached to …`, `slicc: offering …; policy: …; compute: ssh via <runner>`, `slicc: linked with "<name>" in <tab>`, `slicc: link lost: …`, `slicc: <name> quit …`, `slicc: <name> is back …`, `slicc: closed <ids>`). It never writes ANSI escapes or cursor codes. `SLICC_DEBUG=1` adds diagnostics.

**`--json`** prints NDJSON events on stdout and nothing else there, for Sliccstart (`slicc --json --browser <id>`). Errors are also printed on stderr. Every event has `"schema": 1` and `event`. Fields marked `?` are left out when empty; new fields and events may be added, and existing ones keep their meaning.
- `discovered`: `entries`, the ranked list in the shape of `slicc browsers --json` (always a list).
- `launched`: `browser` (the id), `name`, `version?`, `role` (`seven` or `extra`), `pid`, `profile?`, `profileDir?`, `cdp`, `webSocketDebuggerUrl`.
- `attached`: `browser`, `name`, `version?`, `role`, `launched?` (`true` when this run launched it), `cdp`, `webSocketDebuggerUrl?`. For `seven` slicc is connected to it over CDP; an `extra` is listed for `cdp`.
- `foreign-browser`: `browser`, `name`, `profile?`, `cdp`, `openedTab` (`true` when slicc opened the seven tab, `false` when one was open). It follows `attached` when the browser hosting seven is an endpoint slicc didn't launch and the user didn't choose it (see above), and always when it is SLICC six's Chrome, with `profile` `six` or `six:<port>`. The same line goes to stderr.
- `linked`: `peer?` (the name in seven's HELLO), `tab?` (the seven tab's origin, never its path or fragment), `caps`, `policy` (the summary).
- `link-lost`: `reason?` when slicc or seven closed the link (`slicc is exiting`, `slicc is relinking`, seven's `bye` reason, `replaced by a new offer`), or `message?` for a failure. seven sets the link up again after a reload, and a new `linked` follows.
- `browser-gone`, `browser-back`: `browser`, `name`, `role`. A browser stopped answering and left the cdp list, or answers again and is back on it. A `browser-gone` with `reason` `no-browser-session` is for an extra that was never listed, because it has no browser session.
- `browser-slow`: `browser`, `name`, `role`, `elapsed` (seconds). A browser slicc launched hasn't written its `DevToolsActivePort` yet, every 10 s from 10 s on (see [`slicc launch`](#slicc-launch---browser-id---profile-name---url-url---headless---no-seven-flags---json)). The dashboard and plain mode show it as `<name> is still starting (slow disk?), <n> s so far`.
- `closed`: `browsers`, the ids slicc closed (always a list), and `kept` after a detach (the control socket's `detach`, or `SIGTERM` with `--keep-browsers`): the ids of the browsers this run launched and left running, with `browsers` `[]`. It is the last event of every run that got past choosing its browsers.
- `error`: `message`, and `browser?` when closing that browser failed. A setup error ends the run with exit 1.
- `credential`: `op`, `provider`, `leaseId`, `expiresAt?`, `by`, `upstream?`, as on [`attach`](#lifecycle-events---json-on-attach-open-pair-and-follow), for each credential lease this run seals, unseals or revokes over `jerk`.
- `mode`: `mode` (`loopback`) and `reason` (`no-browser`), when the run [falls back](#the-default-slicc) to the default browser. Then come `pairing`, `linked` and `link-lost` events (each `linked` and `link-lost` with `mode` and `device`, the page's fingerprint), and `closed` with `browsers: []`.
- `pairing`: `url`, a pairing URL, after `mode`.

```
{"schema":1,"event":"discovered","entries":[…]}
{"schema":1,"event":"launched","browser":"chrome","name":"Google Chrome","version":"154.0.8037.98","role":"seven","pid":4242,"profile":"chrome","profileDir":"…/slicc/seven-profiles/chrome","cdp":"http://127.0.0.1:53211","webSocketDebuggerUrl":"ws://127.0.0.1:53211/devtools/browser/…"}
{"schema":1,"event":"attached","browser":"chrome","name":"Google Chrome","version":"154.0.8037.98","role":"seven","launched":true,"cdp":"http://127.0.0.1:53211","webSocketDebuggerUrl":"ws://127.0.0.1:53211/devtools/browser/…"}
{"schema":1,"event":"linked","tab":"https://seven.sliccy.ai","peer":"seven","caps":["net","http","credentialed","ws","cdp"],"policy":"every public address, this machine, and the local network"}
{"schema":1,"event":"link-lost","reason":"slicc is exiting"}
{"schema":1,"event":"closed","browsers":["chrome"]}
```

## Browsers

### `slicc browsers [--json] [--cdp <url>]... [--no-bundle-scan]`

Lists running CDP endpoints and installed browsers, best match first. The browser tables (ids, names, bundle ids, install and user-data paths, Electron apps), the ranking tiers, the seven flags and prefs and the process-scan rules come from [slicc-browsers](https://github.com/ai-ecoverse/slicc-browsers)' `catalog.json`, which slicc-cdp's `playwright-cli` reads too. Its conformance corpus runs in `test/integration/corpus_test.go`, with every need but `tabs`: discovery drops every page that isn't a seven tab (see below), so it reports no `tabs` list, and the cases that need one skip.

**Running endpoints** come from four places:
- `DevToolsActivePort` files in the default user-data dirs of the browsers below, in each SLICC profile under the profiles dir (see `slicc launch`), in SLICC six's profiles (see [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile)), and on Linux in the flatpak (`~/.var/app/<id>/config/…`) and snap (`~/snap/chromium/common/chromium`) dirs. The file's port must answer `/json/version` with the websocket path on the file's second line, otherwise the file is stale and ignored. A file without a second line starting with `/` is ignored too.
- `GET /json/version` on `127.0.0.1:9222` to `9229`.
- The process list (`ps -ax -o pid= -o command=`; on Linux, when that command fails (missing, or a BusyBox `ps` without `-ax`), `/proc/<pid>/cmdline` of every numeric pid, its NUL-separated arguments joined by spaces; on Windows a process snapshot (`CreateToolhelp32Snapshot`, each process's command line from `NtQueryInformationProcess`), and only when that fails `Get-CimInstance Win32_Process` with `ProcessId` and `CommandLine`, as JSON, through PowerShell within 5 s): each process started with `--remote-debugging-port=<n>` or `--remote-debugging-port <n>`. Pid 0, `slicc` itself, and commands run by a wrapper (`node`, `npx`, `tsx`, `npm`, `open`, `bash`, `zsh`, `sh`, `csh`, `fish`, `dash`, `timeout`, `env`, `sudo`, `caffeinate`, `cmd`, `powershell`, `pwsh`) are skipped. Port 0 means the port is in the `DevToolsActivePort` of the process's `--user-data-dir` (its value runs to the next whitespace followed by `--` or to the end, so paths with spaces work, and a quoted value runs to the matching quote), checked as above; without one, or with one under a `slicc/profiles` folder that isn't one of six's profiles, the process is skipped. Any other port is asked for `/json/version` on `127.0.0.1`. The exact rules are in [slicc-browsers](https://github.com/ai-ecoverse/slicc-browsers#the-process-source).
- Each `--cdp` URL: `http(s)://host:port`, `http(s)://host:port/<token>`, `ws(s)://host:port/devtools/browser/<id>`, `host:port` or a bare port. One that doesn't answer is reported on stderr and left out; the exit status stays 0.

**A token in the path.** Some browsers answer `/json/version`, `/json/list` and their socket only under a secret path, as `http://127.0.0.1:9227/<token>/json/version`, and refuse the port's root. slicc keeps the path of an `http(s)` URL, ignoring a trailing `/` and a trailing `/json/version`, `/json/list` or `/json`, and asks for each route under it. The path works like a password, so it is shown only where a program needs it to connect: `cdp` and `webSocketDebuggerUrl` in `--json`, and the same fields of `--json` events. The table, status lines, errors, `SLICC_DEBUG=1` diagnostics and what a `cdp` stream's RESET tells the page show `http://127.0.0.1:9227/…`. Such an endpoint is never found by the port probe, since its token can't be guessed: name it with `--cdp`.

Endpoints are merged by host and port. A `DevToolsActivePort` file wins over `--cdp`, which wins over the process list, which wins over the port probe. For each endpoint, `/json/version` gives the name, version and `webSocketDebuggerUrl`, and `/json/list` gives the seven tabs: `page` targets whose origin is `https://<label>.sliccy.ai`, other than the service hosts in slicc-browsers' `seven.excludedLabels` (`:443` is the same origin, any other port isn't). An endpoint's `cdp` has its host in lower case, with `localhost` and `::1` written as `127.0.0.1`. A `DevToolsActivePort` whose port is open but serves no `/json/version` (Chrome's opt-in remote debugging of a running browser) is listed as `wsOnly`, with the websocket URL from the file and no tab check.

**Discovery only reads.** Every source, the port probe included, sends each endpoint `GET /json/version` and `GET /json/list` and nothing else: no WebSocket, no navigation, no new tab. Its HTTP client never follows a redirect (a `3xx` counts as no answer) and never uses a proxy from the environment. The same client does every other read of a CDP endpoint: `/json/version` and the WebSocket handshake when `slicc` and `attach` connect, and the [`cdp`](#cdp-browser-control) capability's `/json/version` and WebSocket for each stream. A `DevToolsActivePort` or `ws://` `--cdp` URL whose port serves no `/json/version` gets a TCP connect, closed at once, to tell `wsOnly` from stale. The targets in `/json/list` that aren't seven tabs are dropped as soon as the list is parsed: their URLs, titles and ids are never logged (`SLICC_DEBUG=1` included), cached, written to disk, printed (`--json`, the picker, plain mode), sent over the link, or put in telemetry or error messages. Only the seven tabs' `id`, `url` and `title` are kept. Attaching to a browser, and anything else that talks to it over CDP, happens only once a browser has been chosen to host seven (see [The default](#the-default-slicc)).

**Installed browsers**, by id: `chrome`, `edge`, `brave`, `opera`, `vivaldi`, `arc`, `atlas`, `dia`, `chrome-beta`, `chrome-dev`, `chrome-canary`, `brave-beta`, `brave-nightly`, `edge-beta`, `edge-dev`, `edge-canary`, `vivaldi-snapshot`, `chrome-for-testing`, `chromium`. Electron apps get `electron:<slug of the name>`, such as `electron:slack`. When a browser is found twice, the first find keeps the id.
- **macOS:** `/Applications` and `~/Applications`, plus `mdfind` by bundle id. Name, version and executable come from each bundle's `Info.plist`. An app is an Electron app when it has `Contents/Frameworks/Electron Framework.framework` or `MSWebView2.framework`. `~/Applications/<Name> Debug.app` (a Sliccstart debug build) replaces `/Applications/<Name>.app`. Claude and 1Password block remote debugging and are listed with `debugSupport: "disabled"`. With `--no-bundle-scan`, nothing inside an app bundle is read except for debug builds, which avoids macOS App Management prompts: browsers are matched by bundle id or app name, Electron apps by a list of known bundle ids, and versions come from Spotlight (`mdls`). Those Electron apps have `debugSupport: "unknown"`.
- **Windows:** `StartMenuInternet` and `App Paths` in the registry (current user, then machine), then the known paths under `%ProgramFiles%`, `%ProgramFiles(x86)%` and `%LOCALAPPDATA%`. The version is the highest version-named directory next to the executable. Electron apps are directories under `%LOCALAPPDATA%\Programs`, `%ProgramFiles%` and `%ProgramFiles(x86)%` with `resources\app.asar` (or `resources\app`) and Chromium's `.pak`/`.bin` files, and Squirrel installs (`%LOCALAPPDATA%\<app>\Update.exe` with `app-<version>` dirs).
- **Linux:** snaps (`/snap/bin/<name>`), names on `PATH` (such as `google-chrome-stable`, `chromium`, `microsoft-edge-stable`, `brave-browser`), flatpaks (`~/.local/share/flatpak/app/<id>`, `/var/lib/flatpak/app/<id>`) and `.desktop` files in the XDG, flatpak and snap application dirs. Versions come from `/var/lib/dpkg/status`, a snap's `meta/snap.yaml`, or else `<browser> --version`. A `.desktop` program whose directory has `resources/app.asar` (or `resources/app`) and Chromium's data files is an Electron app.

**SLICC six's Chrome.** An endpoint that a source found in one of six's profiles is marked `six: true`: from its `DevToolsActivePort`, from a process whose `--user-data-dir` is six's (with port 0 its `DevToolsActivePort` is read), or from a port probe or `--cdp` URL whose port the process list maps to six's profile. A stale file marks nothing. It ranks last and is never chosen by itself (see [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile)).

**Ranking.** Tiers, in order: 1 an endpoint with seven open, 2 any other running endpoint, 3 Chrome stable, 4 other Chromium browsers, 5 Electron apps, 6 an endpoint in SLICC six's profile, with or without seven open. Endpoints sort by host, then port. Browsers sort in the id order above, Electron apps by name, then path. Versions below `MinChromiumMajor` (116) are left out: for endpoints by the `Chrome/<major>` in the user agent, and for installed Chrome, Edge and Chromium by their version, which is the Chromium version. Other browsers' versions are not Chromium versions and are not filtered.

**`--json`** prints one document. Fields marked `?` are left out when empty; new fields may be added, existing ones keep their meaning. `playwright-cli list --json` prints the same shape. This section is its spec; [slicc-browsers' `schema/browsers.schema.json`](https://github.com/ai-ecoverse/slicc-browsers/blob/main/schema/browsers.schema.json) is a JSON Schema copy of it.

```json
{
  "schema": 1,
  "minChromium": 116,
  "entries": [
    {
      "id": "cdp:127.0.0.1:9222",
      "kind": "endpoint",
      "tier": 1,
      "name": "Google Chrome",
      "version": "154.0.8037.98",
      "endpoint": {
        "cdp": "http://127.0.0.1:9222",
        "webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/browser/…",
        "name": "Google Chrome",
        "version": "154.0.8037.98",
        "product": "Chrome/154.0.8037.98",
        "userAgent": "Mozilla/5.0 …",
        "source": "devtools-active-port",
        "browser": "chrome",
        "userDataDir": "/Users/me/Library/Application Support/Google/Chrome",
        "sevenOpen": true,
        "sevenTabs": [{ "id": "…", "url": "https://lars.sliccy.ai", "title": "https://lars.sliccy.ai" }]
      }
    },
    {
      "id": "chrome",
      "kind": "installed",
      "tier": 3,
      "name": "Google Chrome",
      "version": "154.0.8037.98",
      "installed": {
        "id": "chrome",
        "name": "Google Chrome",
        "engine": "chromium",
        "channel": "stable",
        "version": "154.0.8037.98",
        "path": "/Applications/Google Chrome.app",
        "executable": "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
        "bundleId": "com.google.Chrome",
        "userDataDir": "/Users/me/Library/Application Support/Google/Chrome",
        "debugSupport": "supported"
      }
    }
  ]
}
```

- Entry: `id` (an installed id, or `cdp:<host>:<port>`), `kind` (`endpoint` or `installed`), `tier` (1–6), `name`, `version?`, and either `endpoint` or `installed`.
- `endpoint`: `cdp` (the HTTP base URL, with its path when it has [a token](#browsers)), `webSocketDebuggerUrl`, `name`, `version?`, `product?` and `userAgent?` (from `/json/version`), `source` (`devtools-active-port`, `flag`, `process` or `probe`), `browser?` (the installed id whose user-data dir held the file), `userDataDir?` (the dir that held the file, or a process's `--user-data-dir`), `profile?` (the SLICC profile name), `wsOnly?`, `six?` (`true` for SLICC six's Chrome), `sevenOpen`, `sevenTabs` (always a list; each tab's `url` and `title` are its origin, as the CDP relay shows them).
- `installed`: `id`, `name`, `engine` (`chromium` or `electron`), `channel?`, `version?`, `path` (the `.app`, executable or flatpak dir), `executable` (for a flatpak, the `flatpak` binary), `bundleId?`, `via?` (`snap` or `flatpak`), `package?` (the snap or flatpak id), `userDataDir?` (the browser's default), `debugSupport` (`supported`, `disabled` or `unknown`), `debugBuild?`.

Without `--json`, the same list is a table: id, name, version, state and where. The state of six's Chrome ends with `SLICC six's profile`.

### `slicc launch [--browser <id>] [--profile <name>] [--url <url>] [--headless] [--no-seven-flags] [--json]`

Launches a browser with CDP and keeps running until interrupted (Ctrl-C, `SIGINT` or `SIGTERM`; on Windows Ctrl-C or Ctrl-Break). Then it closes the browser it started and exits 0. If the browser exits by itself, `slicc launch` exits 0 too.

- `--browser` takes an installed id from `slicc browsers` (or its name, case-insensitively). The default is the best installed Chromium browser in the ranking above.
- `--profile six` and `--profile six:<port>` use SLICC six's own Chrome profile in place: `slicc launch` attaches to six's Chrome when it is running with CDP, refuses when it runs without, and otherwise launches the browser six used (or `--browser`) in it, writing nothing to the profile. None of the profile steps below apply. See [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile).
- Chromium browsers always get a separate SLICC profile, `<config dir>/slicc/seven-profiles/<name>`, because Chrome 136 and later ignore CDP flags on the default profile. The config dir is `~/Library/Application Support` on macOS, `%APPDATA%` on Windows and `$XDG_CONFIG_HOME` (or `~/.config`) on Linux. A snap's profiles live in `~/snap/<snap>/common/slicc/seven-profiles`, the only place it can write. The profiles dir is shared with slicc-cdp's `playwright-cli`, whose `open` launches into the same `<name>` dirs. Both tools reuse a browser that is still running there, through its `DevToolsActivePort`. `--profile` is the name (letters, digits, `.`, `_`, `-`; default: the browser id). A profile another browser process still holds is refused, and so is one that is SLICC six's (when `SLICC_USER_DATA_DIR` points at it, say), before anything in it is touched.
- **Older profiles.** Earlier versions kept profiles in `<config dir>/slicc/profiles`. On macOS's default case-insensitive volume, that is the same folder as SLICC six's `~/Library/Application Support/Slicc/profiles`. `slicc`, `slicc browsers` and `slicc launch` move them once, and do the same for each snap's `slicc/profiles`.
  - Each profile dir is renamed into `seven-profiles`, which is created when missing. `playwright-cli` may already have created it.
  - Once every profile has been looked at, the move writes `seven-profiles/.migrated-from-profiles` and never runs again. A run that is cut short picks up where it stopped next time.
  - A name that already exists in `seven-profiles` stays in the old folder, with the notice `slicc: profile <name> stays in <old>, since <new>/<name> already exists`.
  - Left alone: six's profiles (`browser-coding-agent-chrome` and `browser-coding-agent-chrome-<port>`, in any case, and the `SLICC_USER_DATA_DIR` dir; nothing moves at all when the old folder is in one of them), dotfiles (six's `.profile-migration-complete`), and anything that isn't a valid profile name.
  - A profile whose `SingletonLock` points at a live process stays where it is, with a one-line notice on stderr: `slicc: profile <name> is in use by process <pid>, so it stays in <old>; quit that browser, then move it to <new>`.
  - `slicc/profiles` is never scanned or launched into afterwards.
- Before the launch, a stale `DevToolsActivePort`, Chrome's `Singleton*` files and the session-restore snapshot are removed, and the last exit is marked clean, so only the start page opens.
- Before each launch, the seven prefs are merged into the profile's `Default/Preferences`, which is created if missing: `tab_freezing_enabled: false`, `performance_tuning.tab_freezing.enabled: false`, Memory Saver off (`performance_tuning.high_efficiency_mode.state: 0`), and `[*.]sliccy.ai` plus each extra allowed origin's host as tab-discard exceptions. The exceptions go into the list `performance_tuning.tab_discarding.exceptions` (six's form) and into the map `performance_tuning.tab_discarding.exceptions_with_time`, which Chrome 154 reads and shows under "Always keep these sites active". Existing keys, exceptions and timestamps are kept, and a file that already has everything isn't rewritten. A Preferences file that isn't a JSON object is left alone. `--no-seven-flags` skips these prefs too.
- Chromium flags: `--remote-debugging-port=0 --user-data-dir=<profile> --no-first-run --no-default-browser-check --silent-debugger-extension-api`, the throwaway flags for a throwaway profile, the seven flags below, `--headless=new` with `--headless`, then `--url`.
- A throwaway profile is one inside the system temp dir (Go's `os.TempDir()`: `TMPDIR` on macOS and Linux, `TMP` or `TEMP` on Windows; symlinks resolved, case ignored on macOS and Windows). It gets `--use-mock-keychain --password-store=basic` (`browser.ThrowawaySwitches`), so Chrome never asks for the macOS keychain or a Linux password store; without a default keychain, as under a temporary `HOME`, macOS would show "Keychain Not Found". Every other profile, SLICC's and six's included, headless or not, keeps the real keychain, so its saved cookies and passwords stay readable. `browser.Throwaway(dir)` tells which is which.
- The seven flags keep a seven tab's kernel running while the tab is in the background: `--disable-background-timer-throttling`, `--disable-backgrounding-occluded-windows`, `--disable-renderer-backgrounding`, `--disable-background-tracing`, and `--disable-features=LocalNetworkAccessChecks,LocalNetworkAccessChecksWebSockets,IntensiveWakeUpThrottling,HighEfficiencyModeAvailable,InfiniteTabsFreezing,InfiniteTabsFreezingOnMemoryPressure,CPUMeasurementInFreezingPolicy,MemoryMeasurementInFreezingPolicy,AllowDevtoolsConnectedDiscard`. Chromium ignores feature names it doesn't know. The seven flags also include `--disable-blink-features=AutomationControlled`: Chrome sets `navigator.webdriver` to `true` in every page of a browser started with `--remote-debugging-port`, as slicc's are, and sites read that as a bot. With the flag it is `false`, as in a browser a person started; CDP works the same. Every Chromium browser slicc launches, with or without the seven flags, also gets `--enable-features=ForceForegroundPriorityForAllTabs`, merged with any other `--enable-features` into one switch since Chromium keeps only the last, so a loaded Mac doesn't move a hidden tab's renderer into the background priority band, where the guard can't hear from it within 30 s and refuses the tab. Every one also gets `--silent-debugger-extension-api`, so Chrome shows no "started debugging this browser" bar when an extension attaches `chrome.debugger`; it is a runtime flag like the rest and is kept with `--no-seven-flags` too. `--no-seven-flags` (`LaunchOptions.NoSevenFlags`) leaves them all out, along with the seven prefs above, but not `--enable-features=ForceForegroundPriorityForAllTabs`, because the guard watches every browser slicc launches, not only seven's. Chromium keeps only one `--disable-features` value, and only one `--disable-blink-features`, so every `--disable-features` and every `--disable-blink-features` from the seven flags and from `LaunchOptions.ExtraArgs` is merged into one switch of its kind, in order and without duplicates. The CDP port is read from the profile's `DevToolsActivePort` once `/json/version` answers with the same websocket path. slicc waits for that as long as the browser it started is running, up to 2 min, since a first start from a cold or slow disk can take well over 30 s. Every 10 s of the wait it says so: `slicc: Google Chrome is still starting (slow disk?), 10 s so far` on stderr, or the `browser-slow` event with `--json`. A browser that exits first fails the launch at once, with its exit code and the end of its output (`Google Chrome exited with code 21 after 0.4s, before writing DevToolsActivePort; its output: …`). One still running after 2 min is stopped, and the launch fails with `… is still running after 2m0s but has not written <profile>/DevToolsActivePort`. Ctrl-C, `SIGINT` or `SIGTERM` during the wait stops the browser at once, and `slicc launch` says `slicc: interrupted while <name> was starting; closed it` (`{"schema":1,"event":"closed"}` with `--json`) and exits 0. A browser that `open` started on macOS isn't a child of slicc, so a launch that fails or is interrupted also stops the processes that hold the profile: the `SingletonLock` owner and every process started with its `--user-data-dir`. For an Electron app, which has no SLICC profile, it stops the process running the app's executable; the launch refuses an app that is already running, so that process is the one slicc started. One that is still running 15 s later is named in the error (`… is still running as process [<pid>]`), and `slicc launch` exits 1 instead of reporting `closed`. Electron apps get the same wait for their CDP port. On macOS a browser with a window starts through `open -n -W -a <app> --args …`, so camera and microphone permissions belong to the browser, not the terminal; `--headless` runs the executable directly. A flatpak starts with `flatpak run --filesystem=<profile> <id>`.
- Electron apps keep their own data and get `--remote-debugging-port=<port>`, the first free port from 9333 up, so they never take 9222–9229. An app that is already running is refused, since a second instance would hand off to it; quit it first. Apps with `debugSupport: "disabled"` are refused.
- Shutdown: `Browser.close` over CDP; if the browser is still running 5 s later, a signal (`SIGTERM` to the browser's process group and main process; `taskkill /T` on Windows), then after 3 s a kill (`SIGKILL`; `taskkill /F /T`). The browser runs in its own process group (`CREATE_NEW_PROCESS_GROUP` on Windows), so a terminal Ctrl-C reaches only `slicc`.

Plain output: the CDP HTTP URL on stdout, status lines on stderr. With `--json`, NDJSON events on stdout, each with `"schema": 1` and `event`, as in [the default's `--json`](#the-default-slicc) (earlier releases left `schema` out):
- `{"schema":1,"event":"launched","id","name","version","engine","pid","profile","profileDir","cdp","webSocketDebuggerUrl"}` (`version`, `profile` and `profileDir` are left out when empty);
- `{"schema":1,"event":"browser-slow","id","name","elapsed"}` every 10 s while the browser is still starting (`elapsed` in seconds, see above);
- `{"schema":1,"event":"closed"}` after `slicc launch` closed the browser;
- `{"schema":1,"event":"exited"}` when the browser exited by itself;
- with `--profile six`, when six's Chrome was already running: `{"schema":1,"event":"attached","id","name","version","engine","profile","profileDir","cdp","webSocketDebuggerUrl"}` instead of `launched`, then `{"schema":1,"event":"detached"}` on Ctrl-C (six's Chrome keeps running) or `exited` when it quits;
- `{"schema":1,"event":"error","message"}`, then exit 1.

## Local setup: `slicc attach`

```
slicc attach --cdp <url> [--cdp-extra <url>]... [--origin <origin>]... [--signal cdp|stdio] [--name <name>] [--allow <rule>]... [--deny <rule>]... [--expose-debug-port <port>]...
             [--runner <argv…> [--]] [--shell <argv…> [--]] [--eval <argv…> [--]] [--eval-quiet <dur>] [--sftp | --no-sftp] [--kernel-port <n>] [--no-kernel]
             [--mount <path>[:<name>][:ro]]... [--json]
```

`attach` links a seven that is open in a running CDP browser and serves the link until it is interrupted (`SIGINT`, `SIGTERM`), or until stdin closes with `--signal stdio`. Status lines go to stderr, and `SLICC_DEBUG=1` adds diagnostics, pion's included. pion never writes to stderr itself.

**Flags.**
- `--cdp`: the browser's endpoint, in any form `slicc browsers --cdp` takes, here and on `prompt`, `watch`, `wait` and `abort`. `http://host:port`, with [a token path](#browsers) if the browser wants one, is looked up through `GET /json/version`, and `ws://…` is used as is. `host:port` is `http://host:port`, and a bare port is `http://127.0.0.1:<port>`. As in `slicc browsers`, a trailing `/` and a trailing `/json/version`, `/json/list` or `/json` are ignored. slicc talks to the browser WebSocket only and attaches to the tab with `Target.attachToTarget { flatten: true }`. It names one browser; a second `--cdp` is an error. This browser is also listed for the [`cdp`](#cdp-browser-control) capability, with `seven: true`.
- `--cdp-extra`: another browser seven may drive over the link (repeatable), in any form `slicc browsers --cdp` takes. Each must answer when `attach` starts.
- `--origin`: also accept seven on this origin (`scheme://host[:port]`), repeatable. `https://<label>.sliccy.ai` is always accepted, except the service hosts in slicc-browsers' `seven.excludedLabels` such as `vault`. `www` is seven's, since seven runs on `www.sliccy.ai/os/` for six's in-place upgrade.
- `--signal`: `cdp` (default) or `stdio`.
- `--name`: the link's `name` and `host` in HELLO. The default name is `slicc on <host>`, with this machine's short host name, lower-cased.
- `--allow`, `--deny`: policy rules on top of the local default (see [Network policy](#network-policy)). The `--cdp` and `--cdp-extra` ports and endpoint addresses are denied, and so are this machine's other [debugging ports](#network-policy), whatever `--allow` says.
- `--expose-debug-port <port>`: let seven reach this one debugging port after all (repeatable). It must be one of this machine's debugging ports when `attach` starts (9222–9229, a `DevToolsActivePort` port, or a running browser's `--remote-debugging-port`), or `attach` exits 1. The `--cdp` and `--cdp-extra` ports can't be exposed (also exit 1).
- `--runner <argv…>`: offer compute over SSH (cap `ssh`, see [SSH server](#ssh-server)). Each command runs as `<argv…> <command>`. The argv is every word after `--runner` up to a lone `--` or the end of the command line, so `--runner bash -c` or `--runner docker exec -i box sh -c -- --name box`. An argv can't contain a lone `--` itself. `--runner=…` is refused. At start, slicc resolves the argv's first word as exec does (on `PATH`, or a path to an executable; a relative path is made absolute, since commands run in the home directory) and exits 2 before linking when it doesn't: `slicc: --runner <x>: program not found`, or, when the word holds whitespace (`--runner "bash -c"`), `slicc: --runner "bash -c": no program "bash -c"; did you mean --runner bash -c?`. Without `--runner` the link has no `ssh` capability, and the flags below are refused.
- `--shell <argv…>`: the interactive form for `ssh` without a command, in the same argv syntax. The default is derived from the runner.
- `--eval <argv…>` and `--eval-quiet <duration>`: the REPL behind the `slicc-eval` subsystem (for example `--eval python3 -i`), and the silence that ends one response (default `500ms`).
- `--sftp`, `--no-sftp`: serve the `sftp` subsystem or not. The default is on only for a plain local shell as the runner.
- `--mount <path>[:<name>][:ro]`: share this folder with seven's kernel (cap `fs`, repeatable); see [Host folders](#host-folders).
- `--kernel-port`: the local port for [kernel services](#kernel-services) (1–65535, default `80`; with `80`, a spare port in 17180–17189 when `80` can't be bound).
- `--no-kernel`: no kernel services; nothing listens.
- `--no-jerk`: don't offer this machine's slicc-jerk (caps `jerk` and `jerk-seal`, see [`jerk`](#jerk-slicc-jerk-over-the-link)).
- `--json`: NDJSON lifecycle events on stdout (see [Lifecycle events](#lifecycle-events---json-on-attach-open-pair-and-follow)). Not with `--signal stdio`, whose setup messages own stdout (exit 2).

**Setup over CDP.**
1. slicc picks the first page tab whose URL has an allowed origin. Until there is one it says `waiting for a seven tab` and watches for new tabs. After 15 s without one it says `still no seven tab` and what to do: a browser can hide seven's own tab from debugging clients, and then this setup can't work, so link through the tray ([Remote setup](#remote-setup-slicc-join-url-follow)). `SLICC_SEVEN_QUIET` (test-only, a Go duration) replaces the 15 s. A `Target.createTarget` refused by such a browser ends plain `slicc` with the same advice.
2. It attaches, calls `Inspector.enable` (answered by the browser even when the tab's renderer is gone, so a crashed tab shows up as `Inspector.targetCrashed` before anything waits on the renderer), grants the tab's origin `local-network-access`, `local-network` and `loopback-network` with `Browser.setPermission` (best effort), calls `Runtime.addBinding { name: "sliccLinkSend" }`, then `Runtime.enable`, and says `found seven at <origin>`.
3. For each new default execution context of the main frame whose origin is allowed, slicc mints a nonce (16 random bytes, base64url). It then calls `globalThis.sliccLinkReceive({ "t": "hello", "v": 1, "nonce", "node" })` in that context. `node` is a random id per `slicc` process (16 random bytes, base64url), the same in every hello and HELLO of that process, so seven can tell a second `slicc` on the same browser (a `slicc prompt --cdp …` next to the bridge) from a restart of the same one. The call is retried every 100 ms to 1 s until `sliccLinkReceive` is a function.
4. seven answers through `sliccLinkSend(JSON.stringify(message))`:
   - `{ "t": "offer", "nonce", "sdp" }`;
   - `{ "t": "candidate", "nonce", "candidate" }`, where `candidate` is `RTCIceCandidate.toJSON()`, or `null` at the end of gathering;
   - `{ "t": "bye", "nonce", "reason" }`.
5. slicc sends `answer`, its own `candidate`s and, when it exits, `bye` in the same shapes through `globalThis.sliccLinkReceive?.(message)`, in the same context.

**What setup logs.** Status lines and `SLICC_DEBUG=1` diagnostics name only allowed origins. A context on another origin (an iframe, an extension) is logged as `ignored a context on another origin`, and a seven tab that navigates to another origin as `the seven tab left seven`, without the new origin.

**Rules for incoming setup messages.**
- A binding call counts only when it comes from that main-frame context and carries its nonce. Calls from iframes, workers, other contexts and other origins, and stale nonces, are ignored.
- Each offer starts a new link and replaces the current one.
- A navigation or reload ends the link, and the next allowed context starts the setup again with a new nonce.
- A link that fails on its own while the tab and its context are still there gets a new `hello`, with a new nonce, in that same context, so seven starts a new offer. That covers ICE that fails (as when the network changes under the link), no HELLO within 10 s, a protocol violation, and an offer slicc can't answer. slicc says `link failed (<error>); asking seven to set it up again in <wait>`; the wait starts at 1 s and doubles to 30 s. A link that comes up with the current nonce resets the wait and cancels a `hello` still waiting, so an offer seven repeats during the wait isn't replaced. seven maps the new nonce to the same `local:<node>` link and replaces it; messages with the old nonce are ignored, and so is the failure of a link whose nonce is no longer current, so a superseded link that closes doesn't start another round. A `bye` from seven under the current nonce during the wait cancels the pending `hello` too. A link seven closes itself (its data channels close, as when it unlinks the device) isn't set up again, and neither is one that ends with a `bye`, with slicc exiting or relinking, with a screen-abuse close, or with a navigation, reload, closed or crashed tab, which follow their own rules here. `--signal stdio` doesn't do this; its harness asks for a new `hello` instead.
- A closed tab ends the link, and slicc waits for another seven tab.
- A tab that closes while slicc attaches to it doesn't stop slicc: each setup call has 10 s, and when the tab is gone slicc looks for another seven tab.
- A crashed tab (`Target.targetCrashed`, or `Inspector.targetCrashed` on slicc's session) isn't a closed one: slicc says `seven tab crashed`, ends the link with that reason, and doesn't set up again on the dead page, whose renderer would never answer. It reloads the tab with `Page.reload` (`reloading the seven tab`) and sets up again once the browser reports `Inspector.targetReloadedAfterCrash`, or after 30 s. A tab that is running again by the time slicc looks is set up without a reload. slicc reloads at most 3 times in 5 minutes; after that it says `the seven tab keeps crashing. Reload it and slicc links it again` and waits until someone reloads the tab, closes it, or opens another seven tab, which it then prefers.
- A setup that fails while the browser connection is up doesn't stop slicc either: a call that times out or fails on a tab that is still there is tried again from the start after 1 s, doubling to 30 s, with `couldn't link the seven tab (<error>), trying again in <wait>`. A setup that succeeds resets the wait. Only a lost browser connection, or slicc stopping, ends the setup; then `slicc` (the default) first tries to reattach, `attach` exits 1, and agent commands stop with the error.
- A lost browser connection isn't a closed browser: Chrome drops its DevTools connections when the Mac goes to sleep, lid closed included, and keeps running. `slicc` (the default) says `lost the debugging connection to <name>; reattaching`, connects to the same endpoint again every 1 s, and on success says `reattached to <name>` and links seven again. It ends as when the browser closes (`<name> closed`) once a browser it launched has exited, as soon as the endpoint refuses connections (nothing listens on its port any more), or when 30 s of other failed tries pass, time asleep not counted.

**Setup over stdio** (`--signal stdio`), for a test harness that owns CDP itself:
- The same messages go as NDJSON: slicc writes `hello`, `answer`, `candidate` and `bye` lines to stdout, and reads `offer`, `candidate` and `bye` lines from stdin. Messages with another nonce are ignored.
- The harness writes `{"t":"hello"}` to stdin to get a fresh `hello` with a new nonce, for example after it reloaded the page.
- `--cdp` is optional here. When given, it names the debugging port to deny and the `seven: true` browser for `cdp`.

**ICE.**
- No STUN or TURN, so only host candidates are used, loopback included.
- Chrome's `<uuid>.local` candidates are resolved with mDNS in query-only mode.
- The link is declared failed after 15 s without connectivity.

## Linking seven in your everyday browser

```
slicc open [--page <url>] [--origin <origin>]... [--name <name>] [--allow <rule>]... [--deny <rule>]... [--expose-debug-port <port>]...
           [--kernel-port <n>] [--no-kernel] [--no-jerk] [--mount <path>[:<name>][:ro]]...
           [--runner <argv…> [--]] [--shell <argv…> [--]] [--eval <argv…> [--]] [--eval-quiet <dur>] [--sftp | --no-sftp] [--json]
slicc pair [the same flags]
slicc --default-browser [the same flags]
slicc unpair [<fingerprint> | --all] [--json]
```

Link spec §1.3. These commands link a seven that runs in the browser you use every day (Chrome, Edge or Firefox; Safari and other WebKit browsers can't, see [§1.3](#13-local-setup-without-cdp-loopback-signalling)), without CDP or remote debugging, as slicc-node could. Only the setup is new: once the peer connection is up, the link is the same as for `slicc attach` (see [Link](#link)).

- **`slicc open`**, and `slicc --default-browser` (the same command), print the pairing URL on stdout and open it in the default browser: `open` on macOS, `xdg-open` on Linux, and `rundll32 url.dll,FileProtocolHandler` on Windows. If that fails, slicc says `could not open the default browser (…); open the URL above yourself` and goes on.
- **`slicc pair`** only prints the pairing URL, for a seven that's already open. Paste it into seven's address bar, or into its *Link this computer* field.
- **Plain `slicc`** falls back to this mode by itself when it finds no SLICC profile and no CDP endpoint (see [The default](#the-default-slicc)).
- **When no browser answers:** 30 s after the first pairing URL, with no request yet, slicc says `no browser has reached slicc yet`, why WebKit browsers can't, and what to do instead. Only the first URL gets this line. `SLICC_PAIRING_QUIET` (test-only, a Go duration) replaces the 30 s.
- Both serve links until `SIGINT` or `SIGTERM`, then exit 0. A listener that can't bind, or a missing config directory, exits 1, and bad flags exit 2. Status lines go to stderr, and `SLICC_DEBUG=1` adds diagnostics.
- **Flags** are as for `slicc attach`: `--origin`, `--name`, `--allow`, `--deny`, `--expose-debug-port`, `--kernel-port`, `--no-kernel`, `--no-jerk`, `--mount` and the compute flags. `--page <url>` is the seven page to pair (default `https://seven.sliccy.ai/`), and its origin is accepted as a seven origin. `--default-browser` takes these flags too. The flags of `slicc` that pick or launch browsers (`--browser`, `--cdp`, `--profile`, `--headless`, `--yes`, `--plain`, `--json`) are errors here. A flag of SLICC six's points back to six, as for `slicc`.

**Identity.** The node's DTLS certificate (ECDSA P-256, valid for 20 years) lives in `<config>/slicc/loopback-identity.json` (mode `0600`, directory `0700`). The same file holds the port the node last listened on. It is minted on first use, and slicc says `new loopback identity for this computer: <name> (ab12…ef), fingerprint sha-256 AB:12:…`, or `this computer: …` on later runs. It is separate from the tray hub's certificates and survives restarts and updates. A file that exists but can't be read as an identity (another version, broken JSON or PEM) is left alone: slicc says ``<path> is not a loopback identity slicc can read; …`` and exits 1. Only a certificate within a day of expiring is replaced. Whenever slicc mints a new identity (none yet, or a replacement), it forgets every pin in `loopback-peers.json` right after writing it, since every seven has to pair with the new certificate anyway. A `loopback-peers.json` that can't be read is left alone the same way: pairings and `slicc unpair` fail with ``<path> is not a list of paired sevens slicc can read; …``, and signals are refused. `<name>` is `--name`, or this machine's short host name in lower case.

**The pairing URL** is `<page>#slicc-pair=<port>.<token>.<node-fp>`:
- `<port>` is the listener's port;
- `<token>` is 128 random bits in base64url (22 characters). It is good once, for 10 minutes;
- `<node-fp>` is the SHA-256 fingerprint of the node's certificate, in lower-case hex without colons.

The URL is the secret, and it carries the node's fingerprint, so a process that squats on the port can't pass for the node. slicc prints a new pairing URL each time a line arrives on stdin, so press Enter for a new one. When the newest URL expires unused, slicc says `the pairing URL expired; press Enter for a new one`. Older URLs stay good until their own 10 minutes are up. Pairing URLs only go to stdout; telemetry and logs never get them.

**The listener** binds `127.0.0.1:<port>`, plus `[::1]:<port>` when this machine has IPv6 loopback, and never `0.0.0.0`. The port is the one it used last time if that's still free, otherwise the first port in 17117–17126 where both addresses bind. The kernel port (`--kernel-port`, unless `--no-kernel`) is skipped. With none free, it says `no free port in 17117-17126 on 127.0.0.1` and exits 1. It serves exactly two requests:

| request | body | answer |
| --- | --- | --- |
| `POST /slicc/v1/pair` | `{ "token", "offer": "<sdp>" }` | `200` `{ "answer": "<sdp>", "node": "<per-process id>", "name": "<name>" }` |
| `POST /slicc/v1/signal` | `{ "offer": "<sdp>" }` | the same |

**The gate**, on every request, in this order. Anything that fails it gets `403` with an empty body:
1. `Host` is `127.0.0.1:<port>`, `[::1]:<port>` or `localhost:<port>`, ignoring case, so DNS rebinding can't reach it.
2. The request target is exactly `/slicc/v1/pair` or `/slicc/v1/signal`: no query, no trailing slash, no absolute form.
3. There is exactly one `Origin`, and it is a seven origin under the same rule as CDP setup (`browser.IsSevenOrigin`): `https://<label>.sliccy.ai`, unless slicc-browsers' catalog lists the label in `seven.excludedLabels` or it starts with an entry of `seven.excludedLabelPrefixes`. The `--page` origin and each `--origin` count as seven origins too. `null` and missing origins fail.
4. The method is `OPTIONS` (the preflight) or `POST`.
5. A `POST` body is a JSON object of at most 64 KiB (65 536 bytes), and its `offer` holds exactly one SHA-256 fingerprint (one value, at the session or media level).

- **Preflight:** an `OPTIONS` that passes 1–3, asks for `Access-Control-Request-Method: POST`, and asks for no request header other than `Content-Type` gets `204` with `Access-Control-Allow-Origin: <that origin>` (never `*`), `Access-Control-Allow-Methods: POST`, `Access-Control-Allow-Headers: Content-Type`, `Access-Control-Allow-Private-Network: true`, `Access-Control-Max-Age: 600` and `Vary: Origin`. It never sends `Access-Control-Allow-Credentials`.
- **CORS on refusals:** a refusal after step 3 (a wrong method, body, token or pin) carries `Access-Control-Allow-Origin` and `Vary: Origin` as well, so seven can tell a `403` from a closed port. Earlier refusals carry no CORS headers. Every answer has `Cache-Control: no-store`.
- `Origin` can be forged by a process that isn't a browser, so the gate only filters browsers. Trust comes from the token or the pinned fingerprint.

**Pairing.**
- **Rate limit:** at most 10 pairing attempts a minute pass the gate. Past that, `/pair` is refused before the body is read, and slicc logs `refused a pairing: more than 10 a minute` (debug). Signals don't count.
- **The token:** the node checks it in constant time. A wrong, used or expired token burns nothing, still counts toward the limit, and gets `refused a pairing request from <origin>: the token is wrong, used or expired`. A matching token is burned at once, before the answer, even when the answer then fails.
- **The pin:** the node pins the offer's fingerprint with the request's `Origin` in `<config>/slicc/loopback-peers.json` (mode `0600`), as `{ "version": 1, "peers": { "<page-fp>": { "origin", "node", "firstSeen" } } }`, where `node` is the fingerprint of the node identity it was paired with. A new pairing of the same fingerprint replaces the entry. slicc says `paired with seven on <origin> (ab12…ef)`. Every change to the file, from this process or from `slicc unpair`, holds an exclusive OS file lock (`flock`, or `LockFileEx` on Windows) on `loopback-peers.json.lock` next to it for its read and write. Concurrent changes never overwrite each other, and since the OS releases the lock when a process exits, a crash leaves no stale lock. A change that can't get the lock within 5 s fails with `the loopback state is being changed by another slicc; try again`. The identity file is created under `loopback-identity.json.lock` the same way, so two first runs at once end up with one certificate.
- **Signals:** `/signal` is answered only for a pinned fingerprint whose origin equals the request's `Origin` (both normalized) and whose `node` is this node's current identity, so a pin that another `slicc` wrote under an older identity never counts. A `slicc` whose identity another one has since replaced can't change the pins either: its pairings and unlinks fail with `this slicc's loopback identity was replaced by another slicc; restart it`.
- **The answer:** non-trickled. The node answers through its link stack, waits until ICE gathering is complete (10 s at most, after which it refuses), and returns its SDP with every host candidate and `a=end-of-candidates`. The page checks that the answer's fingerprint is the one it pinned before it uses the answer.

**The link in this mode** is local (HELLO `mode: "local"`), with the node's loopback certificate:
- **Caps:** `net`, `http`, `credentialed`, `ws`, `udp`, `unlink-loopback`, `fs` with `--mount`, `ssh` with `--runner`, `jerk` and `jerk-seal` unless `--no-jerk`, and kernel services on `--kernel-port` for a page that offers `kernel-in` (see [Kernel services](#kernel-services)). There is no `cdp`, since the node controls no browser, so a `cdp` OPEN gets `EOPNOTSUPP`. `jerk`'s `--seven-origin` is the pinned origin.
- **Policy:** the local default (see [Network policy](#network-policy)), with this machine's [debugging ports](#network-policy) (`--allow` never reopens them, only `--expose-debug-port`), the listener's port and the kernel port denied. So a kernel program can't reach a debugging port the user turned on.
- **ICE:** host candidates, loopback included, as for `slicc attach`.
- **Several sevens:** the node keeps one link per paired page fingerprint. A new offer for a fingerprint replaces that fingerprint's link only. Each link is an exit of its own, and kernel connections go to the page that linked last, as for `slicc attach`.
- **Restarts:** the node keeps its certificate and its port, and the page keeps its certificate and its pin. A restarted `slicc open` or `slicc pair` is reachable again at once, and a paired page signals again without a new token. Each process has a new `node` id, so seven keys the device on the fingerprint.

**Unlinking.**
- **From seven:** on a live link, seven opens `OPEN { "kind": "unlink" }` (cap `unlink-loopback`). The node answers OPENED `{}` and END, deletes that page's pin, closes that link only, and says `unlinked by seven on <origin> (ab12…ef); it must pair again to link`. It keeps running and keeps its certificate.
- **On the node:** `slicc unpair` lists the pins, one line each: `<page-fp>  <origin>  paired <time>`, or `no paired sevens`. `slicc unpair <fingerprint>` forgets one. A unique prefix works, ignoring colons and case. It says `forgot ab12…ef (<origin>)`, and exits 1 when nothing matches or more than one pin does. `slicc unpair --all` forgets every pin (`forgot <n> paired sevens`). A forgotten page gets `403` on its next signal.
- **Live links close at once:** after forgetting the pins, `slicc unpair` asks every running `open`, `pair` and default-browser `slicc` of this user, over its [control socket](#running-links-slicc-tray), to close the link of each forgotten page: `{ "v": 1, "op": "revoke", "fingerprint": "<page-fp>" }`, answered `{ "v": 1, "ok": true, "closed": true }` when that process held such a link and closed it (`closed` is left out otherwise). It says `slicc unpair: closed the live link of ab12…ef` on stderr for each. A link whose offer passed the pin check just before `unpair` is closed too, as it comes up: the node checks the pin again when it sets up each link, and closes one whose page is no longer pinned (`seven on ab12…ef was unpaired while it linked; closed its link`). `--json` goes before or after the fingerprint. **A process too old to know `revoke`** (its status has no `revoke` in `ops`) **keeps its link until it drops**, as before; restart it to drop the link at once.
- **`--json`:** one JSON document on stdout, nothing else there, errors on stderr. Listing: `{"schema":1,"peers":[{"fingerprint","origin","node","firstSeen","live"}]}`, where `node` is the node identity the page paired with, and `live` is `true` while a running process holds a link with it (from the `peers` of its status). Forgetting, with `<fingerprint>` or `--all`: `{"schema":1,"removed":[<page-fp>…],"closed":[<page-fp>…]}`, the pins forgotten and the live links that closed. Fixture: [`unpair.ndjson`](test/integration/testdata/native/unpair.ndjson). Once a link is down and its page is no longer pinned, the node lets go of everything it kept for that page.

**The page side**, as the test harness implements it (`test/integration/testdata/link/loopback.js`, the reference for seven):
1. `RTCPeerConnection.generateCertificate({ name: "ECDSA", namedCurve: "P-256" })` once per origin, kept in IndexedDB (`slicc-loopback`, store `keys`, key `certificate`) and passed as `certificates` to every loopback `RTCPeerConnection`.
2. On load, the page reads `#slicc-pair=<port>.<token>.<node-fp>`, strips it with `history.replaceState`, and asks the user to confirm. Only the confirmation sends the first loopback request.
3. It creates the `slicc.link.v1` channel, sets the local offer, and waits for ICE gathering to complete.
4. It posts `{ token, offer }` to `http://127.0.0.1:<port>/slicc/v1/pair` with `Content-Type: application/json`, `mode: "cors"` and `credentials: "omit"`.
5. It accepts the answer only if its SHA-256 fingerprint is `<node-fp>`; otherwise it refuses the link (a squatter). Then it pins `{ fingerprint: <node-fp>, port, name }`.
6. To link again, it posts `{ offer }` to `/slicc/v1/signal`, first on the remembered port, then on 17117–17126, and accepts only an answer with the pinned fingerprint. After a link loss it retries with backoff, from 1 s up to 30 s.
7. To unlink, it opens `unlink` on the live link when HELLO lists `unlink-loopback`, then forgets its pin.

## Link

One WebRTC peer connection. seven offers and the node answers. The node:
- **Control channel:** expects `slicc.link.v1` from the page. It sends its HELLO as the first (text) message, and closes the link when the peer's first message isn't a valid HELLO with `role: "page"` and `v` ≥ 1, when no HELLO arrives within 10 s, or on any text message after HELLO.
- **Streams:** accepts streams on their own `slicc.link.v1.stream` channel or on the control channel. A stream lives on the channel its OPEN came on, and frames for that id on other channels are ignored.
- **Its own streams:** opens them on a channel of their own, or on the control channel once it has 256 stream channels open. It closes a stream's channel once both sides sent END, or after a RESET. A stream whose channel closes early is reset locally with `ECONNRESET`.
- **Reading:** reads every channel all the time and buffers up to the window. Stream frames that arrive before the peer's HELLO are held until it arrives. The SCTP receive buffer is 4 MiB.
- **Frames:** `type u8 | stream id u32 BE | payload`. OPEN, OPENED, DATA, END, RESET (`ERRNO` or `ERRNO: message`) and CREDIT (`u32` bytes consumed). DATA carries at most 65 536 bytes.
- **Credit:** at most 256 KiB of uncredited DATA per stream and direction. The node credits bytes once it has written them on, for `tcp` once the socket write returned.
- **Resets the stream with `EPROTO`:** DATA or END before OPENED or after the peer's END, empty DATA, DATA past the window, CREDIT of 0 or past what was sent, an unexpected OPENED, or an OPEN without a JSON `kind`.
- **Resets with `EOPNOTSUPP`:** an OPEN for a kind it doesn't serve.
- **Closes the link:** an unknown frame type, a short or malformed frame (END with a payload, CREDIT that isn't 4 bytes), stream id 0, a reused id, an id with the wrong parity (page streams are odd, node streams even), or a second OPEN on a stream channel.

HELLO from `attach`: `{ "v": 1, "role": "node", "name", "host", "version", "mode": "local", "node", "caps": ["net", "http", "credentialed", "ws", "udp", "cdp"], "routes": { "prefixes": [], "exit": true }, "policy": { "allow", "deny", "summary" }, "browsers": [ … ] }`. `cdp` and `browsers` are there when at least one browser is listed (see [`cdp`](#cdp-browser-control)). With `--runner`, `caps` adds `"ssh"` and HELLO adds `"sshHostKey": "ssh-ed25519 AAAA…"`, the host key in `authorized_keys` form, so the page can write `<host>.slicc.internal <key>` into the kernel's `known_hosts`. With `--mount`, `caps` adds `"fs"` and HELLO adds `"mounts"` (see [Host folders](#host-folders)). With host jerk, `caps` adds `"jerk"` and `"jerk-seal"` and HELLO adds `"jerk": { "version" }` (see [`jerk`](#jerk-slicc-jerk-over-the-link)). Unless `--no-kernel`, HELLO adds `"kernel": { "port" }`, the port the kernel listener uses (see [Kernel services](#kernel-services)).

**`ssh`** (cap `ssh`), page → node, with `--runner` only:
- OPEN `{ "kind": "ssh" }`, OPENED `{}` at once. The stream's bytes are one SSH connection to the node's SSH server.
- The node ends the stream with END when the SSH connection ends. If the page hasn't sent END 10 s later, the node resets it with `ECONNRESET`.
- Without `--runner` the OPEN is reset with `EOPNOTSUPP`.

**`tcp`** (cap `net`), page → node:
- OPEN `{ "kind": "tcp", "host", "port" }`. `host` is an IP literal or a name for the system resolver. The dial goes through the policy and gives up after 30 s.
- OPENED `{ "localAddr", "remoteAddr" }`.
- END half-closes the socket (`CloseWrite`), and the socket's EOF becomes END.
- RESET codes: `ECONNREFUSED`, `ENETUNREACH`, `EHOSTUNREACH`, `ETIMEDOUT`, `ECONNRESET` (also for a socket error mid-stream), `EACCES` for a policy refusal, `ENOTFOUND` for a name that doesn't resolve, and `EPROTO` for an OPEN without a host or with a port outside 1–65535.

**`resolve`** (cap `net`), page → node, usually on the control channel:
- OPEN `{ "kind": "resolve", "name", "family" }`, where `family` is `4`, `6` or `0` (or missing) for both.
- OPENED `{ "addresses": [...], "ttl": 30 }`, then END from the node. The page sends END too.
- A name goes to the system resolver. An IP literal (`[::1]` too) is answered as itself, and `::ffff:a.b.c.d` as `a.b.c.d`. Addresses are deduplicated, and those of the other family are left out.
- Addresses the policy refuses on every port are dropped (see Network policy).
- `addresses: []` means "not mine": the name doesn't exist (NXDOMAIN, or any other failure but a timeout), has no address of that family, or every address was dropped.
- RESET codes: `ETIMEDOUT` when the lookup times out (30 s at most), and `EPROTO` for an OPEN without a name or with another `family`.

**`http`** (cap `http`), page → node. One HTTP request, fetched by the node without CORS, carried on a stream:
- OPEN `{ "kind": "http" }`, answered OPENED `{}` at once.
- The page sends a `u32` BE length, that many bytes of UTF-8 JSON `{ "url", "method", "headers": [[name, value], …] }` (at most 1 MiB), then the request body, then END.
- The node sends a `u32` BE length, the JSON `{ "status", "statusText", "headers": [[name, value], …], "url" }`, then the body, then END. `url` is the request's.
- **Request head:** `url` must be `http:` or `https:` without credentials, and `method` an HTTP token other than `CONNECT`, `TRACE` and `TRACK`. `DELETE`, `GET`, `HEAD`, `OPTIONS`, `POST` and `PUT` are upper-cased in any case. Header names must be tokens, and values Latin-1 without control characters other than tab once leading and trailing whitespace is trimmed; each character is sent as one byte.
- **Request headers:** dropped are the hop-by-hop headers (`Connection`, the fields `Connection` names, `Keep-Alive`, `Proxy-Connection`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`), the ones the node owns (`Host`, `Content-Length`, `Accept-Encoding`, `Expect`), and `Proxy-Authorization`. Repeated names are joined with `, `, except `Cookie`, which joins with `; `. The node adds `Accept-Encoding: gzip, deflate, br`, or nothing when the request has `Range` or `If-Range`, so ranged bodies stay unencoded. It adds no `User-Agent`.
- **Request body:** streamed under the window, with no size cap, and not sent for `GET` and `HEAD` (it is read and dropped). A body that ends within its first 1 MiB goes with its exact `Content-Length`. A longer one goes with the head's `Content-Length` when that is one valid number, otherwise chunked (HTTP/1.1) or as an unsized HTTP/2 body.
- **Upstream:** a client that never follows redirects and never uses a proxy from the environment. It dials through the policy (connect timeout 30 s), verifies TLS with the system roots, and speaks HTTP/1.1 or HTTP/2. A body has no overall timeout.
- **Response headers:** in name order, lower-cased, repeated names joined with `, `, then each `Set-Cookie` as its own entry. Hop-by-hop headers and the fields `Connection` names are dropped. Values are read as Latin-1, one character per byte.
- **Decoding:** `gzip`, `x-gzip`, `deflate` (zlib or raw) and `br`, stacked in any order and undone from the last listed, with `identity` ignored. When every listed coding was undone, `Content-Encoding` and `Content-Length` are dropped; a list with any other coding is passed on as it came, with both headers. `Content-Encoding: identity` alone is dropped too. A response without a body (`HEAD`, `1xx`, `204`, `205`, `304`) keeps both headers and has no bytes after the head.
- **RESET before the response head:** `EPROTO` for a malformed head or one of the refusals above, and for a `206` with a coding the node would decode, since its `Content-Range` wouldn't match. A failed fetch is `<code>: fetch failed: <reason>`, with `tcp`'s codes: `EACCES` for a policy refusal, `ENOTFOUND` for a name that doesn't resolve, `ETIMEDOUT`, `ECONNRESET`, and `ECONNREFUSED` for anything else, TLS failures included.
- **After the head:** an upstream error or a broken coding mid-body is `RESET ECONNRESET`.

**`credentialed`** (node cap, no kind of its own), on every node that serves `http`. An `http` OPEN with `"credentialed": true` carries a credential the vault injected for a transit exit (link spec §5.13). For such a stream the node:
- refuses a URL that isn't `https:` on port 443 with `RESET EACCES: credentialed requests go only to https on port 443`, before anything is dialled;
- logs the request only as its method, scheme and host, as for every `http` stream, and never its path, query, headers or body, `SLICC_DEBUG` included;
- scrubs RESET messages: they carry the error's reason, never the URL's path or query, a header or a body byte;
- follows no redirects and keeps nothing: no cache, no cookies, nothing on disk.

Any value of `credentialed` other than `false` or `null` counts as true, so a malformed flag never sends a credential out as an ordinary request.

**`ws`** (cap `ws`), page → node. A WebSocket for a kernel program (link spec §5.15), which seven never opens as a browser WebSocket, because that would carry the user's cookies:
- OPEN `{ "kind": "ws", "url", "protocols": [ … ], "headers": [[name, value], …] }`. `url` is `ws:` or `wss:` without credentials (a fragment is dropped). `protocols` (optional) are tokens, none listed twice, and go out as `Sec-WebSocket-Protocol`. `headers` (optional) follow `http`'s rules for names, values (Latin-1, one byte per character), joining and the hop-by-hop drop. The node also drops `Host`, `Accept-Encoding` and every `Sec-WebSocket-*` header the handshake owns (`Key`, `Version`, `Extensions`, `Protocol`), and sends the rest, `Origin`, `Authorization` and `Cookie` included. It offers no extensions (no permessage-deflate) and adds no `User-Agent`. Anything else is `RESET EPROTO`.
- **Dial:** through the policy, so the browsers' debugging ports and the node's own ports are `EACCES`, with the system TLS roots, no proxy, HTTP/1.1, and 30 s for the connection and handshake. Redirects aren't followed.
- OPENED `{ "protocol" }`, the subprotocol the far end chose, or `""`.
- **RESET instead of OPENED:** `EACCES: <reason>` for a policy refusal, `ECONNREFUSED: HTTP <status>` for any answer but `101` (a redirect included), `ECONNREFUSED: x509: …` for a certificate the system doesn't trust, and `ECONNREFUSED: <reason>` when the dial or the handshake fails otherwise (a subprotocol or an extension that wasn't offered included).
- **Bytes, both ways:** records of a `u8` type, a `u32` BE length, then the payload, independent of DATA boundaries. `1` is a text message (UTF-8), `2` a binary one, `8` a close: a `u16` code, then a UTF-8 reason of at most 123 bytes, or an empty payload for 1005. Each record is one whole message. The node reassembles the far end's fragments before it sends a record, and passes a page record on as one message while its bytes arrive, in as many frames as that takes.
- **Limits:** a message is at most 64 MiB. A larger one from the far end, or a record header from the page announcing one, makes the node close the WebSocket with 1009. A record of another type, or a close record that is 1 byte long, has a code outside 1000–1003, 1007–1014 and 3000–4999, or a reason over 123 bytes or not UTF-8, makes it close with 1002. In both cases the node sends the page a close record with that code, then END, and ignores what the page sends after.
- **Pings:** the node answers the far end's pings itself and never relays them.
- **Page closes first:** the page sends a close record, then END. The node sends that close to the far end and relays the far end's answering close as a close record, then END. Without an answer within 5 s, it drops the connection and sends END alone.
- **Far end closes first:** the node answers the close with the same code, relays it as a close record, then END. The page's close record that follows is ignored.
- **No clean close:** END alone. The node sends it when the far end's connection drops, and drops the far end's connection when the page sends END without a close record, or inside a record. A RESET from either side drops it too.
- **Flow:** the stream window, nothing more. The node reads the far end one message at a time, and holds a message until the page's window takes it. It holds nothing of a page message but the window, so a record header alone reserves nothing.
- **Privacy:** the node logs a stream only as `ws <scheme>://<host><path>`, without the query, and never a handshake header, a message, or a close reason, `SLICC_DEBUG` included. `ws` streams are never `credentialed`.

**`udp`** (cap `udp`), page → node. A UDP socket for a kernel program (link spec [§5.12](#512-udp-cap-udp-page--node), with the node-side details there):
- OPEN `{ "kind": "udp", "family": 4 }` or `6`, OPENED `{ "localAddr" }`: one unconnected socket on an ephemeral port.
- **Bytes, both ways:** records of a `u8` type, a `u16` BE host length, the host, a `u16` BE port, a `u32` BE length, then the payload. Type `1` is a datagram, to that destination from the page or from that source to the page. Type `2`, from the node only, says a destination was refused or unreachable, with the errno text as its payload.
- **Policy:** each destination is checked as a `tcp` connect would be, protected ports included. A refused one drops that datagram with a type-2 `EACCES` record, and the socket stays open. Broadcast, multicast and the unspecified address are always refused.
- **Receiving:** only from an `ip:port` the socket has sent to.
- END from either side closes the socket, and so does 5 min without traffic. A malformed or oversize record, or the wrong family, is `RESET EPROTO`.

**`kernel`** (page cap `kernel-in`), node → page:
- OPEN `{ "kind": "kernel", "port" }`, sent only to a page whose HELLO lists `kernel-in`. The page dials that kernel port and answers OPENED or RESET (`ECONNREFUSED` when nothing listens). See [Kernel services](#kernel-services).
- Then raw bytes both ways: the node's first DATA is everything it has read from the browser's connection so far, request head included. END half-closes, as for `tcp`.

### `jerk`: slicc-jerk over the link

Link spec §5.14 (J1). slicc-jerk (`jerk`, [Companion tools](#companion-tools)) gets tokens on this machine. Over the link seven can ask it for status, start sign-ins a human completes, and seal a lease into its vault. No token crosses the link except in `seal`'s `lease` event, which only seven's link layer reads and hands to the vault.

**Offered** when host jerk is found: of the first `jerk` on `PATH` and `jerk` in the data folder's `bin`, in that order, the first whose `jerk --version` names 0.6.0 or later. 0.6.0 is the first slicc-jerk with the `--via-link` calls below. On Windows slicc only runs a `.exe`: any other candidate, such as npm's `jerk.cmd`, stands for the `@ai-ecoverse/slicc-jerk-win32-<arch>` `bin\jerk.exe` of the same npm install, and is skipped when there is none. An older jerk counts as absent: slicc says `jerk: host jerk <version> is older than 0.6.0; run slicc install jerk`, and HELLO has neither cap.
- `slicc` and `slicc attach`: caps `jerk` and `jerk-seal`, unless `--no-jerk`.
- `follow`: only with `--jerk` (cap `jerk`) or `--jerk-seal` (both).
- `prompt`, `watch`, `wait` and `abort`: never.
- slicc says `jerk: offering slicc-jerk <version> (jerk, jerk-seal)`, and HELLO adds `"jerk": { "version" }`. Without the cap an OPEN gets `EOPNOTSUPP`.

**OPEN** `{ "kind": "jerk", "op", "provider"?, "scope"?, "lease_id"?, "duration"?, "params"? }`. The page sends END right after it. slicc runs jerk as the user, with no terminal and its stderr dropped:

| op | cap | runs |
| --- | --- | --- |
| `list` | `jerk` | `jerk list --json` |
| `status` | `jerk` | `jerk <provider> get --json` (never `--reveal`) |
| `accounts` | `jerk` | `jerk <provider> list --json` |
| `create` | `jerk` | `jerk <provider> create --via-link [--seven-origin <origin>] [--scope S] [-p k=v]…` |
| `seal` | `jerk-seal` | `FNOX_LEASE_DURATION=<duration> jerk <provider> lease --via-link` (`duration` 1 to 86 400 whole seconds, default 3600) |
| `unseal` | `jerk-seal` | `FNOX_LEASE_ID=<lease_id> jerk <provider> drop --lease --via-link` |

- `--seven-origin` is the origin of the seven tab slicc set up with over CDP. A link set up over stdio or the tray hub has none, and `create` runs without it.
- **OPENED `{}`** once jerk has started. Then NDJSON events, then END:
  - `{ "event": "progress", "message" }` for `create`, from each `{"event":"progress","message"}` line jerk writes;
  - `{ "event": "lease", "credentials", "expires_at", "lease_id" }`, once, for `seal`, only when jerk exited 0 with string credentials and an RFC 3339 `expires_at`; otherwise the result is exit 1, `jerk gave no lease with an expiry`;
  - exactly one `{ "event": "result", "exit", "provider"?, "user"?, "scopes"?, "expires_at"?, "env"?, "message", "items"? }`. `items` holds the rows of `list` and `accounts`. Results and rows keep only these status fields: `provider`, `name`, `source`, `requires`, `cli`, `user`, `scopes`, `expires_at`, `env` and `id`, as strings, booleans or string lists. Anything else jerk prints (a masked token, say) is dropped.
- jerk's exits 0 to 5 are results. slicc never logs a lease or a result's contents, only `jerk: <op> <provider>: exit <n>`.

**RESETs:**
- `EPROTO`: an unknown op, a missing or malformed `provider` (`^[a-z0-9][a-z0-9-]{0,63}$`) or `lease_id`, a `duration` out of range, a `scope` that isn't printable words, or a `param` whose key isn't `^[a-z][a-z0-9_]{0,31}$` or whose value isn't printable (at most 16, 256 bytes each). On Windows, also any value for jerk (`scope`, `param`, `lease_id`) that contains `%`, `&`, `|`, `^`, `<`, `>`, `!`, `"`, `(`, `)` or anything outside ASCII. Host jerk checks the keys against the provider's own `params` and exits 2 for unknown ones.
- `EACCES`: `drop` (there's no such op; `unseal` drops what jerk minted), and `seal` or `unseal` without `jerk-seal`.
- `EBUSY`: a second `create` on a link while one runs.
- `ETIMEDOUT`: a `create` still running after 2 min 30 s, which is then killed. Other ops get 60 s and then answer exit 1, `jerk timed out`.
- `ECONNRESET`: jerk couldn't start, died of a signal, or exited with a code above 5.

A page RESET or a closed link kills the jerk it started.

**The host's say over leases.** A process that offers `jerk-seal` keeps, in memory, the leases it has sealed into seven: provider, lease id, expiry and when, never the credentials. It records each lease before sending the lease event to seven, so an immediate unseal has its provider and expiry. Its [control socket](#running-links-slicc-tray) answers four more ops, and its `--json` prints a `credential` event for each seal, unseal and revoke ([Lifecycle events](#lifecycle-events---json-on-attach-open-pair-and-follow)). Fixture: [`credential.ndjson`](test/integration/testdata/native/credential.ndjson).
- `{ "v": 1, "op": "leases" }` → `{ "v": 1, "sealing": true, "leases": [{ "provider", "leaseId", "expiresAt", "sealedAt" }] }`, the live leases this process served. Expired ones drop out, and so does one seven unsealed.
- `{ "v": 1, "op": "unseal", "leaseId" }` revokes one: slicc runs the drop that seven's `unseal` runs, with no link request, and answers `{ "v": 1, "ok": true, "revoked": true, "upstream"?: true | false }` once jerk has dropped it. Then it tells seven to forget it ([§5.14](#514-jerk-caps-jerk-and-jerk-seal-page--node-j1)). A missing or malformed `leaseId` is `EPROTO`, and a lease this process didn't serve, or has already dropped, is `ENOENT`. Unseals for the same lease run one at a time across the link and control socket. A host request that waits for a successful drop answers revoked without dropping again; a later host request for an already-dropped lease gets `ENOENT`. Repeated link requests reuse the successful result until the recorded lease expires. The host request has a 60-second total deadline, including its lock wait; if the wait uses it up, the answer is `{ "v": 1, "ok": false, "error": "EBUSY" }` and the lease stays listed. Leases remain listed while a drop is in progress. If jerk fails, the answer is `{ "v": 1, "ok": false, "error": "EIO", "exit": <n> }` and the lease stays listed.
- `{ "v": 1, "op": "seal-off" }` → `{ "v": 1, "ok": true, "sealing": false }` stops issuing: every later `seal` on this process is `RESET EACCES: the host turned off sealing on this slicc`, and HELLO leaves out `jerk-seal` from the next link on. Live leases stay, and `unseal` still works. `{ "v": 1, "op": "seal-on" }` undoes it, answered with `"sealing": true`. Neither changes what the flags allow: `follow` without `--jerk-seal` has none of these ops.
- **After a restart:** the list lives only as long as the process, so a lease served by a `slicc` that has since exited can't be revoked through slicc. jerk's own drop still works on the host, as the same user: `FNOX_LEASE_ID=<lease id> jerk <provider> drop --lease`. Seven keeps such a lease until it expires.

**Windows:** from 1.35.0 through 1.49.34, values sent over the link could reach npm's `jerk.cmd` launcher through cmd.exe, which could expand or run them. Fixed in 1.49.35 (2026-10-30): slicc runs jerk's own `jerk.exe`, and on Windows hosts refuses `% & | ^ < > ! " ( )` and non-ASCII in values bound for jerk. Upgrade to 1.49.35 or later.

### `cdp` (browser control)

> **Warning:** a seven with the `cdp` capability has full control of every listed browser: its tabs, cookies, stored passwords and logged-in sessions, files it can download, and anything a page in it can reach. That includes the browser seven runs in (`seven: true`), whether slicc launched it or not: with `slicc` or `slicc attach` on your everyday browser, seven can drive all of that browser, without a further prompt. List only browsers you would let that seven use as you. For `follow` that is a seven on another machine. The exceptions are seven's own origin and the browser's privileged pages (its settings and password manager, extension pages such as a password manager's or a wallet's, and `file://`), which the node keeps out of reach (see below).

Only browsers the user named are listed: the one seven runs in, `--cdp` and `--cdp-extra` for `attach`, the extras picked with `--browser` or in the picker for `slicc`, and `--cdp` for `follow`. A browser discovery found but nobody picked is not.

**What a browser must offer.** The filter below needs the browser to pause requests and to report targets on a session of the node's own, so a `cdp` stream needs `Target.attachToBrowserTarget`, and every page it touches needs `Fetch.enable` to take the node's URL patterns and really pause what matches. When `attach`, `follow` or plain `slicc` adds a browser, the node opens one socket and calls `Target.attachToBrowserTarget`. A `wsOnly` browser is skipped, since that is Chrome's opt-in debugging of a running browser, where a socket can raise a consent dialog for a connection that's already gone, so it opens no socket until a stream does. A browser that answers `-32601` isn't listed: slicc says `seven can't drive this browser through slicc` (plain `slicc` names the browser, and the dashboard says `<name> isn't listed for cdp`; see [The default](#the-default-slicc)), and the link goes on without it, without the `cdp` cap when it was the only one. A socket that fails or doesn't answer within 2 s doesn't count, and the stream decides. A browser that has the session but refuses `Fetch.enable` on a page gets `slicc can't keep seven's origin out of this target` on that page. An acknowledgment has to mean a real pause: measured against a browser that acknowledged `Fetch.enable` without pausing, a script's `location = …` into a protected origin loaded it, and a `Runtime.evaluate` racing the commit read the protected page before the node saw it move.

**HELLO** lists the browsers: `"browsers": [{ "id": "chrome", "name": "Chrome (headless)", "version": "154.0.8037.98", "seven": true }]`. Every entry has all four fields.
- `id` is the browser kind, then `-2`, `-3`… for a second or third of the same kind. The kind comes from `/json/version`: `electron` for an Electron user agent, else by product `chrome` (also `HeadlessChrome`, and browsers such as Brave that report `Chrome`), `edge` (`Edg`), `opera` (`OPR`), else the product lower-cased up to a `/` or `(`, else `chromium`. So `Crest (WebKit)` is `crest`.
- An id keeps its meaning for the life of the node. A removed id is never given to another browser, and the same endpoint (CDP host and port) added again gets its old id back.
- `name` and `version` come from the endpoint's `/json/version`, as in `slicc browsers`.
- `seven: true` marks the browser that hosts this seven tab (`attach --cdp`).

**OPEN** `{ "kind": "cdp", "browser": "<id>" }`, page → node. For each stream the node reads the browser's `/json/version` again and opens a fresh WebSocket to its `webSocketDebuggerUrl`, so each stream is its own CDP client with its own sessions. A `wsOnly` browser is dialled at its websocket URL directly. Dial and handshake together get 10 s.
- **OPENED:** `{ "product", "protocolVersion" }`, the `Browser` and `Protocol-Version` fields of `/json/version` (from `Browser.getVersion` for a `wsOnly` browser).
- **RESET:** `ENOENT` for an id that isn't listed, `ECONNREFUSED` when the browser doesn't answer, `EPROTO` for an OPEN without `browser`.
- A client `Browser.getVersion` result the guard forwards includes `slicc: { "route": "link" }` as a hint that a guard sits in the path (absence means no information); an inbound `slicc` key on that result is replaced, Chrome's other fields stay, and an error reply is left alone.

**Bytes**, both directions: a sequence of messages, each a `u32` BE length plus one UTF-8 CDP JSON message of at most 64 MiB (67 108 864 bytes). The node sends each page message as one WebSocket text message, and each text message from the browser as one stream message, after the filter below. A length over 64 MiB from the page, or a browser message over 64 MiB, is `RESET EMSGSIZE`, and a page END in the middle of a message is `RESET EPROTO`. Browser messages that start with `{"method":"Network.webSocketFrameReceived"` or `{"method":"Network.webSocketFrameSent"` are dropped: seven runs inside a browser it may debug, so those events would carry the CDP traffic itself and grow without bound.

**Closing.**
- The browser closes the socket with a normal close (1000): the node sends END, then discards whatever the page still sends until its END.
- The browser socket fails or closes any other way: `RESET ECONNRESET`.
- END from the page: the node closes the socket with a normal close, then sends END.
- RESET from the page, or the link going down: the socket is closed at once.

**Seven's origin is off limits.** The node relays CDP through a filter, so seven's browser control never navigates to, opens or attaches to a target on seven's own origin, and never reads or writes its cookies or storage. That holds for every listed browser, not only the one seven runs in. What is protected comes from [slicc-browsers](https://github.com/ai-ecoverse/slicc-browsers#protected-origins)' `protectedOrigins`: every `sliccy.ai` host (the apex and `www` included, any port, `https` and `http`), `vault.sliccy.ai` by name (the vault is never a CDP target for seven's tools), `blob:`, `filesystem:` and `view-source:` URLs by the URL inside them, `devtools:` URLs, and the extra origins: `--origin` for `attach`, and `--origin` and the `--page` origin for `slicc`. `follow` has none. `*.kernel.localhost` is not protected. URLs are parsed the way the browser parses them (WHATWG), and the node forwards its own re-encoding of each command, so the browser runs exactly what was checked: a key given twice counts once, as its last value, and a message that isn't a JSON object with an integer `id` and a string `method` is refused (or dropped, without an `id`).
- **Privileged pages** (slicc-browsers ≥ 1.8.0) are protected the same way, as if they were seven's: every URL whose scheme is in `protectedSchemes` (`chrome:`, `chrome-untrusted:`, `chrome-extension:`, `devtools:`, `file:`, from 1.8.5 also `chrome-search:`, `chrome-native:`, `chrome-distiller:`, `isolated-app:`, `chrome-experimental-site-token-provider:`, `externalfile:` and `content:`, and the UI schemes of the catalog's other Chromium browsers, `edge:`, `brave:`, `opera:`, `vivaldi:` and `arc:`), on any host and path, except the new tab pages in `schemeExceptions` (`chrome://newtab`, `chrome://new-tab-page` and its `chrome-untrusted://new-tab-page` frames, `edge://newtab`, `brave://newtab`, `opera://startpage`), matched by the parsed scheme and exact host, so `chrome-untrusted://new-tab-page.evil/` stays protected. `chrome-error:` is not protected: Chrome's network and frame error pages stay readable, snapshottable, reattachable and navigable away from, like the ordinary pages they stand in for. So `chrome://settings`, `chrome://password-manager`, an extension's pages, frames and workers (a password manager's or a wallet's vault included) and any local file are refused, hidden from auto-attach and detached like a seven tab. A wrapper is judged by what it wraps: `view-source:chrome://settings/` and `view-source:file:///…` by their inner URL, `blob:` and `filesystem:` by their origin. An `about:` URL other than `about:blank` and `about:srcdoc` is judged as the `chrome://` URL Chrome makes of it, so `about:settings` is `chrome://settings`. A target whose `type` is in `targetTypes` (`browser_ui`, Chrome's own UI such as `chrome://omnibox-popup.top-chrome/`) is protected whatever its URL, and hidden altogether (see *Listing*).
- **Targets.** Before the stream opens, the node attaches its own session to the browser target and calls `Target.setDiscoverTargets` and `Target.getTargets` on it with ids of its own. Neither the session nor its messages ever reach the page. From then on it knows every target's URL and opener. A target on an inheriting URL (`about:blank`, `about:srcdoc`, none) is protected when it was protected before or when its opener is protected or unknown, so a blank popup that seven opens counts as seven. On a page session, the main frame's `securityOrigin` (`Page.frameNavigated`) and the default context's `origin` (`Runtime.executionContextCreated`) count too.
- **Extension worlds.** Any other execution context on a protected origin, such as a password manager's content-script isolated world on an ordinary page, is hidden rather than cutting the page. Seven never sees it, and commands, objects and call frames that name it are refused with `slicc: refused: that execution context belongs to a browser extension` ([§5.8](#58-cdp-cap-cdp-page--node)).
- **Extension frames** (slicc-browsers ≥ 1.20.3). A child frame whose scheme is in `protectedOrigins.extensionFrames.schemes` (`chrome-extension:`), such as a password manager's autofill list, no longer locks an ordinary tab. The frame itself stays out of reach: it can't be attached or read, and its URL is blanked to `about:blank` in target discovery, page reads, snapshots, accessibility trees and evaluate results. Plain occurrences of a currently tracked extension frame's exact URL, and of that frame's `chrome-extension://<id>` origin, are replaced with `about:blank` as a substring inside every string value and every object key in every message sent to seven; every other byte stays. A page can re-encode the URL (case changes, escapes, base64, concatenation), so this removes plain occurrences only. The tab's own page can still be read. On the owner `IFRAME`/`FRAME` DOM nodes (from `DOM.getDocument`, `DOM.describeNode`, `DOM.setChildNodes`, and `DOM.childNodeInserted`) and on a `DOM.getFrameOwner` result for that frame, the guard adds a placement marker `sliccExtensionFrame: {"version":1,"kind":"extensionFrame"}` with no URL, id, origin, title, or frame id; any inbound property of that name is stripped first so only the guard can produce it. `DOM.getFrameOwner` for a tracked extension frame that belongs to the command's own session page is admitted and answers with only `backendNodeId`, `nodeId`, and that marker; attach, evaluate, `Page.createIsolatedWorld`, and every other method that names the extension frame stay refused. `Page.captureScreenshot` and every `Input.*` method in `extensionFrames.whenShown` are allowed only while every such frame is hidden (`visibility: hidden` or no layout box; opacity and off-screen position do not count) and none holds the deep active element through nested documents or closed shadow roots; otherwise they are refused with `extensionFrames.shownRefusal`. Whole-page captures and streams in `extensionFrames.whilePresent` (`Page.captureSnapshot`, `Page.printToPDF`, `Page.startScreencast`, `HeadlessExperimental.beginFrame`) are refused while any extension frame is present, with `extensionFrames.presentRefusal`. A running screencast is stopped or has its later `Page.screencastFrame` events dropped until the frame is gone. A race between the visibility or focus check and the CDP reply is accepted. A frame from an extra origin that is the program's own extension, and any other protected frame, still locks the whole tab.
- **Debugger (Stage 1).** One session per target on each relay stream owns the debugger. Only `Debugger.enable`, `setInstrumentationBreakpoint`, `disable`, `removeBreakpoint` for that owner's breakpoints, and `resume` at a published pause are admitted, on an ordinary target whose navigation guard is ready and whose reachable frames and worlds are placed and ordinary. Every other `Debugger.*`, `DOMDebugger.*` and `EventBreakpoints.*` command is refused with `slicc: refused: debugger methods other than enable, disable, setInstrumentationBreakpoint, removeBreakpoint and resume could read or run code in seven's pages or an extension's world, so the guard doesn't allow them`, including `setBreakpointByUrl`, stepping, `DOMDebugger.getEventListeners`, `getScriptSource` and `evaluateOnCallFrame`. Debugger events are not forwarded as they arrive: `scriptParsed` and `scriptFailedToParse` are held, and only the verified pause's script records, including every validated async and parent-stack script it references, are published before it; `breakpointResolved` and unrelated Debugger events are suppressed. Each `Debugger.paused` is held while the guard checks the current frame tree and the pause's context and document generation. An instrumentation pause is converted privately to a normal pause; instrumentation from an evaluation or a context already published in that document resumes privately. Only a verified ordinary pause and its scripts reach the owner. Bookkeeping for an admitted `enable` or `setInstrumentationBreakpoint` reply survives a document-only generation change, but a safety revocation still invalidates it. A verified batch that fits the stream window can wait for available credit without holding the output or guard lock. After waiting, a fresh frame-tree read orders the proof after queued browser events, then ownership, generation, root loader, contexts and admission are rechecked before a single atomic write within the original pause deadline. Failed proof, revocation or expired credit waits resume privately without publication. Anything protected, stale, unplaced or unverified resumes privately and publishes nothing. A frame-tree read has a 2 s bound; every owned pause has a 5 s deadline, after which the guard resumes it. [§5.8](#58-cdp-cap-cdp-page--node) has the ownership and publication rules.
- **Debugger memory.** All sessions on one relay share a 64 MiB retained-byte limit for script records, script identifiers and held pause data. A session that would exceed it drops its records, resumes its pause privately and refuses further Debugger work until a document change or a safety revocation clears the latch. A same-document admission failure (such as a hidden world or protected frame), or loss of the published pause’s context proof, also clears it; admission still has to become safe before work is admitted again. Commit and drop release its bytes.
- **URLs slicc can't parse** count as protected wherever the node judges a URL the browser will load or has loaded. Chrome's parser and the node's (WHATWG) disagree at the edges: Chrome loads `http://exa mple.sliccy.ai/` as `http://exa%20mple.sliccy.ai/`, a `sliccy.ai` host that gets seven's cookies, and, in some versions, `http://<U+200D>.sliccy.ai/` as `http://xn--1ug.sliccy.ai/`, and the node's parser rejects all of those forms. So a command whose destination doesn't parse (`url` of `Target.createTarget`, `Page.navigate`, `Fetch.continueRequest`, `Network.continueInterceptedRequest`, `Network.loadNetworkResource` and `PWA.launch`, `installUrlOrOrigin` of `PWA.install`, the `url` of a cookie command or any of the `urls` of `Network.getCookies` and `Page.getCookies`, and a history entry's `url` or `userTypedURL`) is refused with `slicc: refused: slicc can't parse this URL, so it could be a protected page`. An empty URL is fine (`Target.createTarget` with `url: ""` opens `about:blank`). On the browser's side, a target, a main frame's `securityOrigin`, an execution context's `origin`, a request or WebSocket, and a service worker whose URL doesn't parse are protected like seven's: left out of listings (see Listing), not attachable, detached, hidden, their document loads failed and their requests stripped. The opaque origins `null` and `://` are not affected. A `blob:null/…` URL (a blob made on an opaque origin, such as a `data:` page or a sandboxed frame) is: its inner `null/…` doesn't parse, and slicc can't name the blob's creator, so it is protected too. URLs the node only reads, such as storage keys and script URLs, are unaffected. In what the page gets, a URL that doesn't parse is cut whole (to an empty string), because it has no origin to keep. The cost: a page on a host that Chrome accepts and WHATWG doesn't (an invalid `xn--` label, a joiner such as U+200D, a space or another forbidden code point), and Chrome's error page for one, is cut too, and navigating to it is refused. URLs Chrome rejects as well (`http://[::1`, `http://a:99999/`, `http://1.2.3.4.5/`) lose nothing: Chrome answers `Page.navigate` with `Cannot navigate to invalid URL`, and `Target.createTarget` with a tab that never loads a document.
- **Refused**, with `{"id":…,"sessionId"?:…,"error":{"code":-32000,"message":"slicc: refused: target is on seven's origin"}}`, never forwarded: `Target.createTarget`, `Page.navigate` and anything else whose `url` is protected (one that doesn't parse is refused with `slicc: refused: slicc can't parse this URL, so it could be a protected page`, see above); `Page.navigateToHistoryEntry` to an entry whose URL is protected (the node looks it up with `Page.getNavigationHistory`); `Target.attachToTarget`, `activateTarget`, `autoAttachRelated`, `Browser.getWindowForTarget` and any other command whose `targetId` or `frameId` is a protected target (except `Target.closeTarget` on a protected `page` or `tab` that this stream created, touched, or opened after the touch — a popup or tab whose `Target.targetCreated` saw an opener or openerFrame this stream had already touched (`navTargets`) or already marked stream-opened; that decision is made once at creation and never walks the opener chain at close time, so a vault or seven tab the user opened from a page before this stream attached to that page stays refused even after the attach; for example attaching and navigating or restoring onto a protected origin, including a sticky error page: that close is forwarded so seven can dismiss a leftover it itself produced; closing a protected tab this stream never touched or opened, the vault tab and seven's other tabs included, the link's own seven page when slicc itself set up the link over CDP in that browser (the tab `slicc attach` / default mode signalled through — `attach --signal stdio`, loopback, and tray/`follow --cdp` have no host page the guard tracks, even when `--cdp` points at a browser that hosts seven), the browser target, workers and every other non-page/tab type stay refused); and every command on a session whose target is protected, or became so. `Target.getTargetInfo` and `Target.detachFromTarget` stay allowed, except `Target.getTargetInfo` on a hidden target (see *Listing*).
- **Origins and storage.** A command whose `origin`, `securityOrigin`, `ownerOrigin`, `issuerOrigin`, `topLevelOrigin`, `topLevelSite`, `embeddedOrigin`, `requestingOrigin`, `scopeURL` or `manifestId` is protected, or whose `storageKey` or `cacheId` names seven's storage, is refused the same way. This covers `Storage.*`, `IndexedDB.*`, `CacheStorage.*`, `DOMStorage.*`, `FileSystem.*`, `ServiceWorker.*`, `PWA.*`, `Browser.setPermission` and `Browser.grantPermissions`. Free-form values (`expression`, `arguments`, `value`, `headers`, `postData` and the like) aren't searched.
- **Cookies.** `Network.getCookies`, `Network.getAllCookies`, `Storage.getCookies` and `Page.getCookies` answer without the cookies whose domain is protected (`sliccy.ai` and every subdomain, and each extra origin's host and its parents: cookies ignore ports), and, when the browser supplies partition data, without partitioned cookies whose `partitionKey.topLevelSite` is such a site (see "Where those promises stop" below). `Network.setCookie`, `setCookies`, `deleteCookies`, `Storage.setCookies` and `Page.deleteCookie` for such a domain, a URL on such a host, or a partition key under such a site, are refused with `slicc: refused: cookies on seven's origin`. So are `Network.clearBrowserCookies` and `Storage.clearCookies`, except in a browser context this stream created with `Target.createBrowserContext`.
- **Requests a page makes to seven.** For a request whose URL is protected, `Network.*` and `Fetch.*` events lose their `Cookie`, `Set-Cookie`, `Authorization` and `Proxy-Authorization` headers (in the header maps and as lines of the raw header texts, which otherwise stay, their URLs cut as everywhere) and their cookie lists, and `requestWillBeSentExtraInfo` drops seven's cookies from `associatedCookies` for any request. Chrome can send `requestWillBeSentExtraInfo` and `responseReceivedExtraInfo` before the event that names the request's URL, so the node holds them until the request is found to be protected, gets its response, finishes or fails (a WebSocket or WebTransport once it is created); one still held after 2 seconds is passed on stripped, as for a protected request. For a WebSocket, WebTransport or EventSource on a protected origin (`ws:` and `wss:` count as `http:` and `https:`), `webSocketWillSendHandshakeRequest`, `webSocketHandshakeResponseReceived`, `webSocketFrameError`, `eventSourceMessageReceived` and `dataReceived` are dropped. Reading such a request's body (`Network.getResponseBody`, `Fetch.getResponseBody`, `takeResponseBody…AsStream`, `Audits.getEncodedResponse` and the like) is refused. A request paused at the response stage whose response headers name a protected URL (an ordinary redirect into seven, say) counts as such a request too. `Fetch.continueRequest` and `Network.continueInterceptedRequest` with a `url` that the node has seen redirect into a protected origin on that target are refused with `slicc: refused: that URL redirects to seven's origin`. So is reading a stylesheet or script that came from a protected URL, directly or through a redirect: `Page.getResourceContent` and `Page.searchInResource` for its URL, and any command naming its `styleSheetId` or `scriptId`. CSS rules and styles from it reach the page with only their `styleSheetId` and `origin`. In a dedicated, shared or service worker, the node keeps its own guard session (Network only), and the worker's commands, `Runtime.runIfWaitingForDebugger` included, wait until that session's `Network.enable` answers, within the same bounds as a page's guard. A worker script other than the worker's own is readable only if that guard saw its whole redirect chain and none of it was protected; an import from before the guard attached, such as in a worker that was already running, is refused. In a page, a stylesheet or script loaded before the node attached to the tab has no redirect chain the node saw, so it stays readable even if it came through a redirect from a protected origin (a known residual). The node also checks the answer, because the page may ask before the node has seen the request. When `Network.getResponseBody`, `searchInResponseBody`, `streamResourceContent`, `getResponseBodyForInterception`, `takeResponseBodyForInterceptionAsStream`, `getRequestPostData`, `Fetch.getResponseBody`, `Fetch.takeResponseBodyAsStream` or `Audits.getEncodedResponse` answers, the node holds the answer until the request has a response (for `getRequestPostData`, until it is sent; for an intercepted request, until it is paused). If the request is protected by then, the answer is refused, and a stream it handed out is closed. If the node still hasn't seen the request after 2 seconds, the answer is refused with `slicc: refused: slicc hasn't seen this request, so it could be seven's`. `Network.replayXHR` is never forwarded: for a protected or unknown request it keeps the body-getter refusal; for a known ordinary request it is refused with `slicc: refused: Network.replayXHR follows redirects inside the browser, so it could send a request to seven's pages or the vault`. Chrome's native replay cannot classify each redirect before sending it. This does not block page-initiated fetches, XHR or subresources, including their redirects into protected origins; C5 allows those requests while the content and credential protections still apply. A URL override through `Fetch.continueRequest`, a redirect supplied through `Fetch.fulfillRequest` or `Fetch.continueResponse`, and `Fetch.continueWithAuth` make the request client-issued. The guard correlates its network ID on that target and fails a protected hop before sending it; an unmodified page continuation does not establish client ownership. Missing correlation, an unguarded worker or exhausted ownership tracking refuses the client mutation. `Network.continueInterceptedRequest` with `url` or `rawResponse` is always refused, whatever the destination; use Fetch instead. Chrome 156 removed `Network.setRequestInterception`, so its compatibility refusal has committed wire coverage rather than a native control. `IO.read` is refused, with `slicc: refused: slicc didn't hand out this stream, so it could be seven's`, unless the node passed that handle to the page in an allowed answer (`take…AsStream`, `Page.printToPDF`, or `IO.resolveBlob` as `blob:<uuid>`). - **Paused requests to seven.** A page session's own interception never gets to touch a request whose URL is protected, whatever its kind (a document, `fetch()`, XHR, an image or any other subresource), at either stage. The node keeps `Fetch.requestPaused`, `Fetch.authRequired` and the deprecated `Network.requestIntercepted` for such a request from the page and settles it itself:
  - a document request is failed (`Fetch.failRequest` with `BlockedByClient`, or `Network.continueInterceptedRequest` with `errorReason: BlockedByClient`);
  - any other request continues untouched (`Fetch.continueRequest`, or `Network.continueInterceptedRequest` with only its `interceptionId`);
  - an auth challenge gets the browser's own handling (`Fetch.continueWithAuth`, or `Network.continueInterceptedRequest`, with `authChallengeResponse: {"response": "Default"}`).

  `Fetch.continueRequest`, `continueResponse`, `failRequest`, `fulfillRequest`, `continueWithAuth` and `Network.continueInterceptedRequest` are refused with `slicc: refused: slicc didn't pass this paused request to the page, so it could be seven's` for any `requestId` or `interceptionId` the node hasn't passed to that session in a paused event, and for one whose pause the page has already answered successfully (until the next paused event for it). So a guessed id can't fulfil, change or fail a protected request, even one that is still paused. The node remembers the last 16 384 ids it passed on; an id older than that is refused too.
- **Browser-wide commands**, refused with their own reason after `slicc: refused: `: `Target.exposeDevToolsProtocol` (it would give a page the whole browser) and `Target.sendMessageToTarget` (attach with `flatten: true` instead), on any target; `Fetch.enable` on the browser itself (a page session is fine); `Tracing.start`; `Extensions.loadUnpacked`; `Network.loadNetworkResource` on any session (Chrome follows its redirects itself, with the user's cookies, so an ordinary URL that redirects to seven would hand the page seven's page; a destination that is protected or doesn't parse keeps its usual refusal); `Page.captureSnapshot` (it serialises every frame, Chrome's error pages for seven's URLs included); and `Browser.setPermission`, `Browser.grantPermissions` without an `origin` and `Browser.resetPermissions`, except in a browser context this stream created.
- **Auto-attach.** With `Target.setAutoAttach`, a session to a protected target is detached by the node at once (`Target.detachFromTarget` on its parent session), and the page never sees its `attachedToTarget` or `detachedFromTarget`.
- **Navigation by other means.** A script's `location = …`, a link, `window.open` or a server redirect isn't a command the node can refuse, so the node guards the network instead.
  - A target the stream attached to (`Target.attachToTarget`, or auto-attach that reached the page) or created (`Target.createTarget`) is *touched*, for as long as the stream is open, even after the page detached. That includes every tab the browser opens while the stream auto-attaches to new tabs. Attaching counts as touching. A protected tab opened after that touch from a page the client drives stays closable (the opener can't tell a user's click from the client's `window.open`); one that already existed when the stream started, even with that page as opener, does not.
  - On each touched page, iframe, background page or webview target the node attaches a session of its own, which the page never sees. On it, the node enables `Fetch` for document requests to protected hosts (`resourceType: Document`, request stage), and `Network` with `Network.setBypassServiceWorker`, so a service worker can't hand out a document without a request.
  - **Browsers that can pause only documents**, such as Crest over `WKWebView`, say so twice, and the node takes neither as read:
    - The refusal of those patterns carries `error.data: "documents-only"`. Any other error ends the setup, so Chrome never reaches this.
    - The node then asks with `resourceType: Document`, and the answer must be `{ "holdsDocuments": true }`. An empty answer leaves the target refused: the node can't tell an acknowledgment from a pause, and one that acknowledged without pausing let a racing `Runtime.evaluate` read a protected page.
    - **What `holdsDocuments` promises.** The node takes these on the browser's word and can't check them, so a browser that can't keep every one must not send it:
      - each matching document, in any frame, waits for the node's answer, and one that gets no answer doesn't load, a child frame's history restore included, unless the browser reports it with `Page.frameNavigated`;
      - the hold stays in place whatever any other session does, its interception, bypass and observation included, sessionless `Fetch` and `Network` commands too;
      - `Input` stays in the main document and never reaches a child frame;
      - `Page.getFrameTree` and `DOM.getDocument` list child frames by address, and never return a child frame's content;
      - every frame the browser lists, nested ones included, has its `url` and `securityOrigin`, and the node checks an `unreachableUrl` only when one is sent;
      - `Runtime.callFunctionOn` and `Runtime.getProperties`, on any object including one for a frame's window, `Accessibility` and the other allowed `DOM` helpers stay in the main document and never cross into a child frame;
      - `Runtime.evaluate` without a selector keeps the page's same-origin rules, and every evaluation world the browser admits keeps the origin boundary;
      - cookie answers carry a truthful `domain`, cookie writes go to their explicit destination and not the active tab, and `Network.getCookies` has its standard scope;
      - target and history evidence is truthful, and `Target.createTarget` makes a fresh target with the given URL.
    - **Where those promises stop.** The node can't close these:
      - The address a browser reports isn't always what's loaded. A frame loaded before the browser's port opened isn't listed as protected and doesn't lock the target if it's nested in a cross-origin frame, or if it's a direct child that redirected into a protected origin. A direct blank child sent cross-origin stays listed as `about:blank` with `securityOrigin: "null"`, which passes as clean unless the browser also lists the protected address. Only the allowlist below covers these.
      - `securityOrigin` is no separate check on such a browser, which may derive it from the frame's `url`, or from the page for a blank or `srcdoc` frame the page can reach. A frame that redirected shows the address it asked for.
      - A browser with no partition API shows a third party's cookie partitioned under a protected site as an ordinary cookie.
      - A default `Network.getCookies` can include an unlisted protected frame's cookies only if their `domain` isn't protected, since the `domain` filter removes the rest.
    - Such a target is *documents-only*. The node reads its frame tree during setup, once the hold is in place, and again after every `Page.frameNavigated`, restores included. Commands on the target wait for that answer, checked again as they're admitted, a `Page.navigateToHistoryEntry` after its history lookup too. A tree that lists a protected `url`, `securityOrigin` or `unreachableUrl` at any depth, that leaves out a `frame`, `url` or `securityOrigin`, or that doesn't arrive, within 10 s after a commit or the setup bounds during setup, locks the target as if it had protected history. What the browser lists is filtered like any other URL, so a protected frame's address reaches the page cut to its origin. A later protected frame is held and failed, which locks the target too. A command already running in the moment between a commit and the node hearing of it can still answer from the new main document, within the allowlist; an answer or error that arrives after the lock is dropped.
    - An error from such a browser reaches the page with only its `code` and scrubbed `message`, so nothing the browser adds as `data` passes.
    - **The one exception to "a protected frame's address never reaches the stream":** a value the page itself puts in a console argument or returns from a script, such as an iframe's `src`. The page already knows it, and Chrome behaves the same.
    - Commands are refused by default, as a second layer, and these are allowed because they were measured on real Crest not to reach a preloaded protected iframe: navigation (`Page.navigate`, `reload`, `stopLoading`, `navigateToHistoryEntry`, `getNavigationHistory`, `getFrameTree`, `getLayoutMetrics`, `createIsolatedWorld`), `Runtime.evaluate`, `callFunctionOn`, `getProperties`, `releaseObject`, `releaseObjectGroup`, `DOM.getDocument`, `querySelector`, `querySelectorAll`, `describeNode`, `resolveNode`, `requestNode`, `getBoxModel`, `getContentQuads`, `focus`, `Accessibility.getFullAXTree`, `Input.dispatchMouseEvent`, `dispatchKeyEvent`, `insertText`, the cookie commands, `Network.enable`, and `enable` and `disable` of the other domains. Anything else is refused with `slicc: refused: slicc can't see inside this browser's frames, so it allows only the commands measured not to reach one; …`, screenshots and PDFs included: those did show the frame.
    - A selector must name the main frame. `frameId` must be the target's, and `contextId`, `uniqueContextId` and `executionContextId` a context the browser reported for it. `executionContextName` and `pierce` are refused. `grantUniveralAccess` and `contentSecurityPolicy` are removed from `Page.createIsolatedWorld` before it's forwarded (as on every browser). Anything else gets `slicc: refused: slicc can't see inside this browser's frames, so a command can't name a frame or an execution context here`.
    - A client's own guard must not reconfigure a hold that may be per tab, so the node answers it without forwarding: `Fetch.enable` with the one pattern `{ "urlPattern": "*", "resourceType": "Document", "requestStage": "Request" }`, `Fetch.disable`, and `Network.setBypassServiceWorker` with `bypass: true`. Other `Fetch` commands, `Network.disable` and `bypass: false` are refused.
    - A session that isn't a guarded page's gets `Browser.getVersion`, `Browser.getWindowForTarget` and the target commands only.
    - Every paused protected document locks the tab, whatever its `frameId`, from the moment the browser says `documents-only`, since these browsers share no convention for it.
    - Client-issued requests (`Fetch.continueRequest` with `url`, and the other mutations) are refused there too, because later hops of such a request can't be paused.
  - The node fails each such request into a protected origin with `Fetch.failRequest` (`BlockedByClient`), and the target shows Chrome's error page instead. Every other request continues untouched. So a touched target never loads a protected document, and nothing the stream installed on it (`Fetch`, `Network`, emulation, overrides) ever runs in a protected document: the navigation guard refuses that load (or clears the carry by never committing it). Document-persistent scripts, bindings and CSP bypass are refused even on ordinary pages, except the stock-client admits below: `Page.addScriptToEvaluateOnLoad`, `Page.setBypassCSP`, a `Page.addScriptToEvaluateOnNewDocument` whose `source` has code, an unscoped or main-world `Runtime.addBinding`, and `Page.removeScriptToEvaluateOnNewDocument` for an identifier this stream never got. Their CDP error is `slicc: refused: scripts and bindings for later documents could run in seven’s pages or the vault, so the guard doesn’t allow them; run a script in the page you see with Runtime.evaluate instead`. They are never forwarded or answered with fake success; use `Runtime.evaluate` for the current document instead.
  - **Stock client setup admits** (so Playwright and Puppeteer can open a page through this guard):
    - `Page.addScriptToEvaluateOnNewDocument` whose `source` matches `^(?:[ \t\n]|//[^\n\r\u2028\u2029]*)*$` on the whole string without the multiline flag (no code), with any `worldName` or `runImmediately`, and `Page.removeScriptToEvaluateOnNewDocument` for an identifier this stream got from such an admit;
    - `Page.createIsolatedWorld` on a frame the guard has placed as ordinary: `grantUniveralAccess` and `contentSecurityPolicy` are always removed before the call is forwarded (on every browser, not only documents-only);
    - `Runtime.addBinding` scoped to an isolated world by a non-empty `executionContextName`, or by the `executionContextId` of a context the guard has placed as ordinary and that isn't the main world; `Runtime.bindingCalled` reaches the page only from contexts placed as ordinary;
    - exactly `Emulation.setFocusEmulationEnabled`, `Emulation.setEmulatedMedia`, `Emulation.setDeviceMetricsOverride` and `Emulation.setTouchEmulationEnabled`. Every other method matching `/^Emulation\.(set|add|update|remove)[A-Z]/` stays refused with the session-settings text below.
  - Before admitting content commands on a touched tab, the node reads fresh navigation history and target info on its own sessions. An uninitialized new tab also needs a fresh current-frame decision and a history retry. Missing evidence fails closed. If the tab's history has held a protected entry during this CDP stream, content commands stay refused, even after navigation away or a history reset. A blocked main-document request quarantines the tab before the node fails it. This includes script, DOM, input, screenshots, accessibility and target-session IO. Clean tabs keep their normal command path. CDP navigation still works; open a new tab to resume content commands. This conservative rule also applies to protected URLs that failed to load and to authorization flows that redirect through a protected callback.
  - Seven's own tab and its vault frame are never touched, because attaching to them is refused.
  - Page commands on a touched target's session wait, in order, until the node's own session there is ready; commands on other sessions of the stream don't wait. No page command on that session reaches the browser before the node's session is ready. The node's `Network.enable` and `Network.setBypassServiceWorker` answer only once the target's renderer does, which a loaded host can take a minute or more to start, so the wait has no fixed length. It fails closed on no progress, or at an outer bound:
    - **Progress** for a target whose setup is under way is the answer to the node's attach to it, or any answer or event on the node's own guard session for that target. Answers and events on the page's sessions, and on other targets, don't count, so a guard that stays silent fails at the 30 s bound even while the browser and the page stay busy. The node checks the bounds every quarter of the shorter one (7.5 s by default).
    - **Idle bound:** a setup with no progress for 30 s fails, within one check of it (37.5 s). Only time the node spends waiting on the browser's socket counts toward the 30 s. The node reads the browser's messages one at a time and passes each on before it reads the next, so a page that is slow to credit its window stops the reads, and the guard's answers wait unread behind the page's messages. That time doesn't count, for setups and unknown sessions alike, and a stop over 2 s is logged under `SLICC_DEBUG`. The outer bound still counts it.
    - **Outer bound:** a setup still under way 5 min after it started fails, whatever its progress.
    - **Refusals:** When a setup call fails, the node's session is lost or the target closes, both the navigation guard and any pending document confirmation fail immediately; the waiting commands, and any after them, are refused at once with `slicc: refused: slicc can't keep seven's origin out of this target`. An inherited document whose opener or parent was never confirmed stays refused with `…: inherited document has an unknown opener or parent; slicc never confirmed it`; if that opener or parent closed, the reason is `…: inherited document has a gone opener or parent`. An in-process opener frame must map to its owning target through the node's frame or execution-context bookkeeping, and that target's guard must have finished successfully without protected history. The owning target must also have no protected frames or exhausted world tracking, and must still expose a live ordinary default execution context for that opener frame when confirmation decides. A protected or ordinary opener whose context has disappeared stays refused; unknown frame ownership stays refused. When a bound fires, the message names it: `…: its guard made no progress in 30s` or `…: its guard wasn't ready in 5m0s`.
    - **Caps:** more than 4096 waiting commands, or 64 MiB of them, on one stream reset it with `ENOBUFS`.
  - **Sessions the node doesn't know.** A page command with a `sessionId` the node hasn't seen on this stream is never forwarded as it stands. The node knows a session from a `Target.attachedToTarget` it relayed or held, from the answer to the page's `Target.attachToTarget` (for a target it knows) or `Target.attachToBrowserTarget`, and from its own sessions. Commands on an unknown session wait, in order, until the session appears, then any guard setup on its target, and are then decided as usual. So a browser that answers an attach before it announces the session can't open a gap.
    - **Progress** for an unknown session is only a message from the browser on that session id. The browser answering other commands doesn't count, because it says nothing about this session. The bounds are a target's setup bounds (30 s idle, 5 min outer, checked every quarter of the shorter), and so are the caps.
    - **Refusals:** a session that never appears is refused with `slicc: refused: slicc doesn't know which target this session belongs to: its guard made no progress in 30s` (or `…: its guard wasn't ready in 5m0s`). A session the node saw detach (`Target.detachedFromTarget`; it remembers the last 4096) is refused at once with `slicc: refused: slicc doesn't know which target this session belongs to`. Another stream's session is unknown on this one, so it waits for the idle bound before it's refused.
    - Commands without a `sessionId` don't wait.
  - In touched targets, service workers are bypassed for every request, not only for protected ones.
  - The guard covers every frame that shares the touched target's process. A cross-site iframe's navigation pauses in its parent's target, before the iframe becomes a target of its own, with the frame's id as `frameId` (the id the iframe target gets later). A popup's request pauses in the popup's target, with its `targetId` as `frameId`. A redirect into a protected origin pauses again at the request stage, with `redirectedRequestId`, even when the first URL didn't match.
  - The page's own `Fetch.enable` on a page session still works. Chrome chains the interceptors of a target's sessions, the most recently enabled first, so the page's interceptor could see a protected document's request before the node's does, or after the node failed it, at the response stage. The node fails such a request itself, and the page never sees it (see *Paused requests to seven* above).
  - When a target commits a protected origin all the same (see the limits below), the node sees it arrive (`Target.targetInfoChanged`) only after the browser committed it. The node then detaches every page session on the target, drops their events and results, and refuses further commands on them; the page gets `Target.detachedFromTarget`. A browser-level `Target.closeTarget` naming that protected `page` or `tab` is still forwarded when this stream created, touched, or opened it after the touch (see *Refused*); closing the page this link is attached to, or any protected tab this stream never touched or opened, stays refused.
  - **Error pages.** A request the node failed (or any failed load) leaves the target on Chrome's error page, which the browser still reports under the protected URL it failed to load. The error page holds no content from that URL, so it doesn't lock the target. But it does name the URL (its reload button and `loadTimeData` carry the full failed URL, which after a server redirect can hold a secret the page never saw). So from the commit (`Page.frameNavigated` with a protected `unreachableUrl`) until the next commit, a page session on the target may only:
    - navigate (not to a `javascript:` URL, which `Page.navigate` refuses everywhere with `slicc: refused: Page.navigate to a javascript: URL runs script in the page; use Runtime.evaluate`), reload (but not with a `scriptToEvaluateOnLoad`, which would run in the reloaded error page), stop, close and bring it to front (`Page.navigate`, `reload`, `stopLoading`, `close`, `bringToFront`, `navigateToHistoryEntry`);
    - read and reset its history and frame tree (`Page.getNavigationHistory`, `resetNavigationHistory`, `getFrameTree`) and set lifecycle events;
    - remove a script for new documents (`Page.removeScriptToEvaluateOnNewDocument`; adding one is refused, since it would run in the error page a reload commits again);
    - release it from the debugger (`Runtime.runIfWaitingForDebugger`);
    - enable or disable the `Page`, `Runtime`, `Target`, `Inspector`, `Network`, `Log` and `Fetch` domains, and settle its own interceptions (`Fetch.continueRequest`, `continueResponse`, `failRequest`, `fulfillRequest`, `continueWithAuth`, `Network.continueInterceptedRequest`), so a navigation away that the page intercepts never hangs;
    - and use `Target.setAutoAttach`, `detachFromTarget`, `closeTarget`, `getTargetInfo` and `activateTarget`.

    Anything else (`Runtime.evaluate`, `DOM.*`, snapshots, screenshots and so on) is refused with `slicc: refused: the target shows Chrome's error page for a protected URL, which names it; slicc only lets a page navigate, reload or close it`. Events on its sessions are filtered too: only `Target.*`, `Inspector.*`, `Network.*`, `Fetch.*`, the `Runtime` execution-context events and the `Page` navigation, loading, frame, lifecycle, window, download and interstitial events reach the page. So console messages, exceptions, `Log` entries, `DOM`, `CSS`, `Overlay` and `Debugger` events, and `Page.screencastFrame` (a screencast the page started before the tab failed would show the error page) are dropped. The node also remembers the last 16 protected URLs a target's error pages failed to load, and replaces each of them, as is and in its percent-encoded and decoded forms, with its origin in every string of every message on that target's sessions, opaque values (`value`, `preview`, `arguments`, `returnValue`) included. A command the page sent on the target's sessions before such a commit, other than the allowed ones above, can't answer from the error page either: if its answer, an error included, arrives after the commit, the page gets `slicc: refused: the target committed Chrome's error page for a protected URL while this command ran, so slicc dropped its answer` instead (a screenshot, PDF or evaluation in flight would otherwise show the error page). Its URL and title are still redacted to the origin in listings and target events. Chrome's error pages for other URLs stay fully readable. The node tracks each touched target's main frame on its guard session (`Page.enable`): the last different-document commit (`Page.frameNavigated`) was an error page when its document is `chrome-error://chromewebdata/` (slicc-browsers' `errorPage.url`, whose `target` is `previous`) and it has an `unreachableUrl`, and that holds only while the target reports exactly that URL and no navigation is under way (`Page.frameStartedNavigating`, other than same-document, until its `Page.frameNavigated`, its document request's `Network.loadingFailed`, or the frame's `Page.frameStoppedLoading`, which also ends any doubt from an event the node couldn't read or from more than 64 navigations in flight, since nothing is in flight once the frame stops loading). Otherwise, or before the guard is ready, a protected URL locks the target as above. An error page neither locks nor unlocks: a target that was already protected stays so. An error page for a URL slicc can't parse is never exempt: it locks, as the unparseable rule says. That's stricter than slicc-browsers' `errorPage` rule, which exempts any URL's error page (nothing loaded there, so the unparseable case opens no hole the parseable one doesn't). slicc-browsers accepts it as over-protection, and no shared vector forces either answer.
- **Auto-attach of targets without a URL.** A cross-site iframe can attach before its URL is known (`url: ""`, which stays empty while it waits for the debugger), and so can a `window.open` popup. So when an auto-attached session (one that doesn't answer the page's own `Target.attachToTarget`) is to a page or iframe target with an empty URL, the node holds it:
  - The page doesn't get its `attachedToTarget` yet, its events are dropped, and page commands on it are refused.
  - If the page asked for `waitForDebuggerOnStart`, the node runs `Runtime.runIfWaitingForDebugger` on it itself.
  - Once the target has a URL (`Target.targetInfoChanged`), a protected target's session is detached without the page seeing it. Otherwise the page gets `attachedToTarget` with the current `targetInfo` and `waitingForDebugger: false`. The target has already started, so a script the page adds for new documents in such a frame or popup misses its first one.
  - A target that has no URL after 10 s is treated as protected. A session that the node does forward makes the target touched (see above).
- **Page commands need a page's session,** for every browser, from the moment a stream opens. A command in a page's domain (`Page`, `Runtime`, `DOM`, `DOMSnapshot`, `Input`, `Accessibility`, `Emulation`, `CSS` and the rest) sent without a session, or on a browser session, is refused with `slicc: refused: this command needs a page's session; …`. Chrome answers those with `wasn't found` anyway, so only the error text changes. `Network` and `Memory` aren't among them: Chromium serves `Memory` at browser scope, and `Network` commands keep their own rules there. A documents-only browser refuses both once it has said so.
- **`Slicc.action`** is answered by the node on every browser and never forwarded. On a documents-only target, `Input` from a client that sent it is acknowledged and dropped too, because such a client checks `isTrusted` and dispatches its own events, so a second delivery would toggle a checkbox twice. A relay stream can't open on a page-level socket (`/devtools/page/<id>`), where Chrome answers `Target.attachToBrowserTarget` with `Not allowed`.
- **Session-wide settings are refused on every relay session, including ordinary pages.** `Network.setExtraHTTPHeaders`, `Network.setUserAgentOverride`, `Emulation.setUserAgentOverride`, `Emulation.setLocaleOverride`, `Emulation.setTimezoneOverride`, `Network.setCacheDisabled`, `Network.emulateNetworkConditions`, `Network.emulateNetworkConditionsByRule`, `Network.overrideNetworkState`, `Network.setBlockedURLs`, and every method matching `/^Emulation\.(set|add|update|remove)[A-Z]/` other than the four display admits above receive `slicc: refused: settings for a whole session (headers, user agent, locale, time zone, cache, network conditions, blocked URLs, emulation) could reach seven’s pages or the vault, so the guard doesn’t allow them; change a request through Fetch instead`. These calls are never forwarded or answered with fake success.
- **Browser-side inheriting navigation is refused.** `Page.navigate` to any `about:` URL, including `about:blank` and `about:srcdoc`, receives `slicc: refused: a browser-side navigation to about: gives the page the origin of whatever drives the browser, so the guard doesn’t send it; open a new tab on about:blank instead`. An absent or unparseable navigation URL receives the same error. Creating a fresh tab with `Target.createTarget` on `about:blank` remains available.

- **Listing.** The origin-only rule covers page and tab targets only. `Target.getTargets`, `Target.getTargetInfo`, `Target.targetCreated` and `Target.targetInfoChanged` still show protected page and tab targets, so seven knows its tab exists, but their `url` and `title` are only the origin (`https://seven.sliccy.ai`, `chrome://settings`, `chrome-extension://<id>`, or `about:blank`), or only the scheme for a URL without a host (`file:`). An iframe target is protected when its own URL is protected or when its `parentFrame`, or any ancestor frame or target, is a protected target or a protected recorded frame (from `Target.*` `parentFrameId`/`parentId` and `Page.frameAttached`/`frameNavigated`); once protected, that decision sticks for the iframe's life. A protected iframe is never listed, never answered by `Target.getTargetInfo`, never attachable, and commands that name it are refused. An auto-attached iframe whose parent target is not known yet, or whose parent target's navigation is still unresolved, is held the same way an unresolved worker is: left out of listings, not attached, and if the hold times out while that parent target is still unknown it is resumed and detached without being shown. Separately, an auto-attached iframe that still has no URL of its own stays held until that URL (or a protected decision from ancestry) is known; ancestry settling alone must not deliver a URL-less iframe. A missing `parentFrameId`/`parentId` is not treated as ordinary: the guard still walks recorded frame ancestry (`Page.frameAttached` / `frameNavigated` / the frame tree), and while that ancestry is unknown the iframe stays held or hidden from `Target.getTargets`, `Target.getTargetInfo`, target events and attach-by-id. Extension-frame targets (`chrome-extension://` iframes on an ordinary page) stay on the existing extension-frame rule (redacted and gated, not hidden from `getTargetInfo`) unless a parent or ancestor is protected. After an iframe was shown, protection still follows it: an explicit attach from the browser session is cut or refused as soon as its owner page or any ancestor turns protected or held, and on every command the guard checks both the target parent chain and the attaching session (if they disagree, the iframe is treated as protected). `parentFrameId` names a frame inside that parent, not a target; the guard resolves ancestry from the attach parent target and from recorded frames, and does not hold merely because `parentFrameId` is not a target id. A worker (`worker`, `shared_worker` or `service_worker`) is protected when its own URL is protected or a protected frame or target created it, including a `blob:` or `data:` URL and a worker auto-attached on an out-of-process iframe's session. Every `data:` worker created by a sub-frame stays hidden: only a `data:` worker whose creator is an ordinary target the guard already knows is ordinary. Once a worker is classified protected, that decision sticks for the worker's life even if its creator later navigates away or drops out of the frame map. That protected worker is not auto-attached and not listed at all: `Target.getTargets` leaves it out, its `targetCreated`, `targetInfoChanged` and `targetDestroyed` are dropped, and `Target.getTargetInfo` answers `No target with given id found`, the same answer an unknown target gets. An auto-attached worker whose URL and creator frame are not known yet is held the same way a page without a URL is: the page does not get `attachedToTarget`, the worker stays paused when `waitForDebuggerOnStart` was requested (so it does not run ahead of the decision), and it is left out of listings until its URL or that frame is known. Without `waitForDebuggerOnStart`, a held ordinary worker can run and emit early events (console, network) that the page never sees, because those events arrive while the session is still held; slicc-cdp accepts that for ordinary workers. Learning the creator frame decides the worker then, even when the worker was recorded first. If the creator is still unknown when the hold times out, the worker is resumed and detached without being shown. Detaching that page session, or turning auto-attach off on it (`autoAttach` false, `waitForDebuggerOnStart` false, `flatten` true, and no filter), does the same at once. `Target.attachToTarget` for a worker that is protected or still unresolved is refused. An ordinary worker the page learned outside listings (for example a `targetId` from `ServiceWorker.workerVersionUpdated`) may be attached by id even if it was never listed; `targetDestroyed` for a worker the page was never shown is still dropped until that attach or a listing shows it. A target whose URL slicc can't parse isn't listed at all, since it has no origin to show: `Target.getTargets` leaves it out, and its `targetCreated` and `targetInfoChanged` are dropped. A target of a `targetTypes` type (`browser_ui`) is never shown: `Target.getTargets` leaves it out, its `targetCreated`, `targetInfoChanged`, `targetDestroyed` and `targetCrashed` are dropped, and `Target.getTargetInfo` on its id is refused like any other command that names it. Service worker registrations and versions on a protected origin are left out of `ServiceWorker` events, and `Storage`, `DOMStorage` and `BackgroundService` events that name a protected origin are dropped.
- **Protected URLs in what the page gets.** In every event and result the page gets, a URL field that holds a protected URL is cut to its origin, at any depth: every key ending in `url`, `urls`, `origin` or `origins` (any case, so `documentURL`, `unreachableUrl` and `securityOrigin` too), `referrer`, `location`, `href` and `src`, and every header value in `headers`, `requestHeaders` and `responseHeaders`. That covers `Page.frameRequestedNavigation`, `Page.frameNavigated`, `Network.requestWillBeSent`, `Page.windowOpen`, `Page.getFrameTree` and the rest. Every other string (header values such as `Link` and `Refresh`, `headersText` and `requestHeadersText`, cookie and storage values, DOM attributes) keeps its text, but each protected URL inside it (a `scheme://` run up to whitespace, a quote, `<` or `>`, a URL that doesn't parse included, a percent-encoded run decoded repeatedly to a fixed point through at most five layers, and an unschemed protected `host[:port]/path` run) is cut to its origin (or host for an unschemed run); if percent-decoding still changes a string after five layers, the whole string is blanked, and next to a protected URL field every field ending in `Fragment` (`urlFragment` of a request or frame) is blanked to `""`, while ordinary URLs keep theirs, so a redirect from an ordinary page to seven's `/auth/callback?code=…` shows the page only seven's origin, wherever it names that URL. Unprotected URLs stay intact. Everything inside a `value`, `preview`, `customPreview`, `arguments` or `returnValue` (evaluation results and their previews) is left alone.
- **Limits.**
  - On Chromium, the filter relies on site isolation. A seven frame inside a page of another site is a target of its own (`iframe`), which the node protects. Frame reads, DOM, paint and input on its page ancestor are locked too, including compound frames whose protected child shares an ordinary iframe's renderer. An iframe's lock belongs to its parent root document: a different root loader stops a cached document's frames from locking the new page, and restoring that loader locks again before content commands are answered. Without generation evidence the page stays locked, unless `DOM.getFrameOwner` on the node's private page session explicitly says a directly parented iframe is absent from the current document. Missing replies, ambiguous parents and other errors keep the lock; root commits, restores and frame attachments invalidate negative ownership evidence. A same-site frame shares its parent's process and isn't a target. Every `sliccy.ai` host is protected, so this only matters for an extra origin, such as one port of `127.0.0.1` framed by a page on another.
  - A document that arrives without a request isn't failed: a page restored from the back/forward cache, and a `blob:` or `filesystem:` URL of a protected origin (which only that origin can make). Targets with protected history are content-locked before commands can race a restore, regardless of delayed commit events. The node still detaches after a protected commit. Navigation initiated outside the stream after fresh evidence, including loads already in flight before the guard was installed, remains a limit; this does not authorize content commands on a quarantined target.
  - **A browser that pauses documents through its navigation delegate** only pauses in a tab the stream touched, so a `window.open` onto a protected URL loads in the new tab before anyone can attach. The stream still can't reach it, since attaching to it is refused, and the page that opened it can't read across origins, but the request goes out with the user's cookies. Measured on Crest, where what the allowlist can inspect is the main document, while document holds cover child frames too.
  - The network guard fails only `http` and `https` documents. Chrome itself keeps a web page from navigating to `chrome:`, `chrome-untrusted:`, `devtools:` and `file:` URLs, but not to an extension's web-accessible pages. A touched page that sends itself to one with a script or a link is detached after the commit, as above.
  - If the node loses its own session (`ECONNRESET: slicc lost track of the browser's targets`), it refuses every command and resets the stream unless the browser closes within 2 s.
  - The node's own setup connection (see [Local setup](#local-setup-slicc-attach)) is not a `cdp` stream and isn't filtered.

**List changes.** The node checks every listed browser's debugging port every 2 s. One that fails twice in a row is removed: its open streams get `RESET ECONNRESET: browser quit`, and a later OPEN for it gets `ENOENT`. Once its port answers again it is listed again under the same id. A browser removed through the API (`Registry.Remove`, for the TUI) has its streams reset with `ECONNRESET: browser removed`.

**`browsers` updates** (page cap `browsers`), node → page. When the list changes while a link is up and the page's HELLO has `browsers` in `caps`, the node opens `OPEN { "kind": "browsers", "browsers": [ … ] }` on the control channel with the full current list in HELLO's shape. It replaces the previous list; there are no deltas. The page answers OPENED `{}` and END, and the node sends END. Updates are sent one at a time per link, a change that comes while one is in flight is sent after it, with the list as it is then, and an update the page doesn't finish within 10 s is reset with `ETIMEDOUT`. A change between the HELLO and the link coming up is sent as an update right away.

`test/integration/testdata/link/link.js` is a page-side implementation of the link (framing, credit, both channel layouts, the violation rules, and `resolve` and raw-fetch helpers) that the integration tests run in Chrome against `slicc attach`. `cdp.js` next to it is a CDP client over `cdp` streams, `protect.js` probes the filter that keeps seven's origin and the browser's privileged pages out of reach, `guard.js` runs [slicc-browsers' guard vectors](https://github.com/ai-ecoverse/slicc-browsers#guard-vectors) through its `guard-runner.mjs`, with a second client straight to the test Chrome as the unguarded observer, every capability (`autoAttach`, `browserSession`, `browserContexts`, `chromeUrlsAttach`, since CDP over a DevTools port reaches `chrome://` pages, and `closeProtected`), and the runner's `settleScale` at 3 on Windows, 2 on macOS and 1 on Linux, and its `refusalScale` at the default 1 (a row still running after 3 minutes fails on its own with the CDP calls it was waiting for, and the test fails after 4 minutes without a finished row, naming each row in flight and its pending calls), `fs.js` an op-shaped client for `fs` streams (`call`, `list`, `read`, `write`, `watch`), `loopback.js` is the page side of [loopback signalling](#linking-seven-in-your-everyday-browser), and `page.js` answers `browsers` and `mounts` updates. Through `linkTest.loopback`, `page.js` pairs from the fragment (`confirm`), signals again (`signal`, `reconnect`), unlinks in-band (`unlink`) and posts raw requests (`post`), with an optional `?ports=<first>-<last>` scan range. `page.js` keeps one link per `node` id from the hello. For tray links (no hello `node`) it replaces an older link with the same fingerprint only when HELLO's `node` matches, as seven does. `agent.js` is a fake agent behind the page's `agent` cap: the tests queue scripted runs, emit transcript events, hold and release a run, reset watch streams, and turn the cap off. Loaded with `?kernel`, the test page lists `kernel-in` and answers `kernel` OPENs with `kernel.js`, a small HTTP and WebSocket echo server in the page.

### Kernel services

seven's kernel runs servers (vite, `python -m http.server`), and kernel port `N` is `http://N.kernel.localhost/`. seven's service worker carries subresources itself, but not top-level navigations or WebSockets. `*.kernel.localhost` resolves to loopback, so `attach` runs a listener that carries those connections into the page as `kernel` streams.

**Listener.**
- It binds `127.0.0.1:<kernel port>` (default `80`) when the first page whose HELLO lists `kernel-in` links up, and keeps it until `attach` exits. With `--kernel-port 8080` the URLs carry the port: `http://5173.kernel.localhost:8080/`. `--no-kernel` turns it off, and remote nodes never listen.
- **macOS** refuses a normal user `127.0.0.1:80` (`permission denied`) but lets any user bind `0.0.0.0:80`: it exempts only the wildcard address from the privileged-port rule. So on macOS, when `127.0.0.1:<port>` gets `permission denied`, the listener binds `0.0.0.0:<port>` instead and refuses every connection whose peer isn't a loopback address: it resets it before reading a byte, and the page never hears of it. Only this machine reaches kernel services, as with `127.0.0.1`. The macOS firewall, when it's on, may ask once whether slicc may accept incoming connections; either answer works, since the firewall never blocks loopback. Another user can't take `127.0.0.1:80` in front of it, for the same rule.
- **Port 80 out of reach.** Linux refuses an unprivileged bind on `80` at any address (unless `net.ipv4.ip_unprivileged_port_start` allows it or slicc has `CAP_NET_BIND_SERVICE`), and something else may hold `80` on any system (`http.sys` on Windows, a local web server). So with the port `80` (the default, or `--kernel-port 80`), slicc checks at startup whether it can bind it as above, and otherwise takes the first port in 17180–17189 it can bind on `127.0.0.1`, and says `cannot listen on <address> (<reason>), so kernel services use port <n>`. The URLs then carry that port (`http://5173.kernel.localhost:17180/`), and HELLO's `kernel.port` tells seven. Any other `--kernel-port` is used as given.
- If the port can't be bound when the page links up (something took it since startup, or none of 17180–17189 was free), `attach` prints `kernel services are off: cannot listen on <address> (<reason>); try --kernel-port` and runs on without it. It tries once.
- The kernel port is denied in the [network policy](#network-policy) from startup, like the browser's debugging port, unless `--no-kernel` is given.

**Each connection.**
1. The node reads the first request head, at most 64 KiB and within 30 s. A connection that sends no complete head in time is closed without an answer.
2. The request line must be `<method token> <target> HTTP/1.0` or `HTTP/1.1`, and the head must hold exactly one `Host`.
3. The `Host` must be `<1–65535>.kernel.localhost`, in any case, with no port or the listener's own port, no leading zeros and no trailing dot.
4. Port `9222` (seven's CDP facade) is refused here, before anything is sent to the page.
5. The node opens a `kernel` stream to the page that linked most recently and is still linked. When that page goes, the one linked before it takes new connections. Open connections stay where they are.
6. It sends what it has read, then pipes bytes both ways with half-close, without parsing anything else. Keep-alive, chunked bodies and WebSocket upgrades pass through as they are, and a connection belongs to the host of its first request.

**Errors** are plain text (`Content-Type: text/plain; charset=utf-8`) with `X-Proxy-Error: 1`, `Cache-Control: no-store` and `Connection: close`:

| case | answer |
| --- | --- |
| `Host` not `<port>.kernel.localhost` | `421` `only <port>.kernel.localhost is served here` |
| malformed request line, or not exactly one `Host` | `400` `malformed request head` |
| head over 64 KiB | `431` `request head too large` |
| port `9222` | `502` `kernel port 9222: EACCES` |
| no page with `kernel-in` linked, or the link closes before `OPENED` | `502` `no seven page connected` |
| the page answers `RESET` `ECONNREFUSED` | `502` `nothing listening on kernel port N` |
| the page answers another `RESET` | `502` `kernel port N: <reason>` (printable ASCII, at most 200 characters) |
| no `OPENED` within 10 s | `504` `kernel port N did not answer` |

The browser's connection waits unread for `OPENED`.

**Security.** The `Host` check defends against DNS rebinding. Kernel services are reachable like any dev server on localhost: by any local process, and by any web page Chrome lets reach loopback. They are never reachable from another machine: the macOS wildcard bind resets every peer that isn't loopback, which a connection to this machine's own LAN address isn't either. Use `--no-kernel` to keep them inside the page.

## Host folders

`--mount <path>[:<name>][:ro]` shares a folder of this machine with seven's kernel, which mounts it with its `hostfs` driver. It is repeatable and works on `slicc`, `slicc attach` and `slicc <join-url> follow`. Each operation is an `fs` link stream (below). There are no tokens: the link is the trust boundary, as for `ssh`, and seven decides which kernel mounts it creates. With `follow`, the folders go to the seven on the other machine.

**Export.**
- A trailing `:ro` makes the folder read-only. After that, the text after the last `:` is the name if it isn't empty and has no `/` or `\`, so `C:\work` is a path and `C:\work:proj` is named `proj`. The default name is the folder's own.
- SLICC six's form, `<os-path>:<vfs-path>`, is refused with exit 2 before anything starts: `--mount <spec> is SLICC six's form, <os-path>:<vfs-path>. This is the new SLICC (seven): use --mount <path>[:<name>][:ro], such as --mount <path>:<name>, then mount the folder in seven.`, with the last component of the VFS path as the name. A spec counts as six's when, without `:ro`, it has no name and the text after its last `:` starts with `/`, unless the text before it is a drive letter (`C:/work`) or the whole spec is an existing folder.
- The folder is resolved with `realpath` at start. A missing path or a file is skipped with `--mount <spec>: not an existing folder, skipping`, a name of `.` or `..` with `invalid name, skipping`, and a second folder under a name already used with `the name <name> is taken, skipping`.
- Names never reveal host paths, and neither does anything else on the link.
- Without a folder, the node doesn't advertise `fs`, and an `fs` OPEN gets `EOPNOTSUPP`.

**Private paths.** Some folders are never shared, even inside a folder that is:
- slicc's own config, data and cache folders (`<config>/slicc`, which holds the DTLS certificates, the SSH host key, the tray state and the vault pairing keys);
- browser profiles: slicc's `seven-profiles` and `profiles`, SLICC six's profiles and `SLICC_USER_DATA_DIR`, and the user data folders of the browsers slicc knows, since the vault's storage lives there;
- slicc-jerk's files: `$XDG_CONFIG_HOME/slicc-jerk` and `$XDG_STATE_HOME/slicc-jerk` (`~/.config` and `~/.local/state` when unset, macOS included), `/etc/slicc-jerk`, and `SLICC_JERK_CONFIG`; and on Windows `slicc-jerk` under `%APPDATA%`, `%LOCALAPPDATA%` and `%ProgramData%`;
- host fnox's files (upstream jdx/fnox's defaults): `FNOX_CONFIG_DIR`, `FNOX_STATE_DIR` and `FNOX_DATA_DIR` (`$XDG_CONFIG_HOME/fnox`, `$XDG_STATE_HOME/fnox` and `$XDG_DATA_HOME/fnox`, else `~/.config/fnox`, `~/.local/state/fnox` and `~/.local/share/fnox`, macOS included, or `~\AppData\Local\fnox` on Windows), `FNOX_PLUGINS_DIR` (default `<data>/plugins`), `FNOX_AGE_KEY_FILE`, and, off Windows, the daemon's socket folders `$XDG_RUNTIME_DIR/fnox`, `$TMPDIR/fnox-<euid>` (on macOS without `TMPDIR`, the user temp folder that `getconf DARWIN_USER_TEMP_DIR` names) and `/tmp/fnox-<euid>`.

The paths are read from slicc's own environment at start. A `--mount` of one of them, or of a folder inside one, is skipped with `--mount <spec>: slicc never shares this folder, which holds its own keys or a credential tool's files, skipping`. In a folder that contains one, every operation on a private path or below it is `EACCES` with `slicc never shares this path`. That holds when the path is reached through a symlink, through a Windows 8.3 short name, or, on macOS and Windows, typed with a different case. A `rename`, `rmdir`, `unlink` or `symlink` of a folder that holds a private path (`.config`, for example) is refused too, so a private folder can't be moved out of the fence or replaced. `list` still shows a private folder's name. `watch` drops a change inside a private folder before it is batched, so it adds no line, no parent folder and no `all`.

**HELLO.** `caps` lists `fs`, and `"mounts"` lists the folders:

```json
"mounts": [{ "name": "project", "readonly": false,
             "capabilities": { "maxIo": 16777216, "symlinks": true, "chmod": true,
                               "caseInsensitive": true, "normalization": "nfd-insensitive", "ranges": true } }]
```

- `capabilities` is in the shape slicc-kernel's `hostfs` driver takes, so seven hands it to the driver unchanged.
- `symlinks` and `chmod` are `false` on Windows and `true` elsewhere.
- `caseInsensitive` is probed per folder on its own volume. The last component of the folder's path that has letters is looked up again with its case swapped, and the probe is `true` when that finds the same file. When no component has letters, it is `true` on macOS and Windows.
- `normalization` is `nfd-insensitive` on macOS and `none` elsewhere.
- `ranges: true` says `setattr` takes `size`, so the kernel reads and writes in pages.

**Streams.** Each operation is one stream, `OPEN { "kind": "fs", "mount", "readonly"?, "op", …arguments }`. An OPEN is one frame, so at most 64 KiB.
- The node answers OPENED (followed by DATA for `list` and `read`), then END. The page sends END once it has OPENED.
- An error is `RESET <ERRNO>: <message>`, before OPENED or, for `list`, `read`, `write` and `watch`, after it.
- `readonly: true` on an OPEN makes that call `EROFS` if it writes, opens for writing or changes the namespace, even on a writable folder. A `:ro` folder is that way for every call.
- `read` and `list` may go on the control channel or on their own stream channel. Pages should give reads over 64 KiB their own channel. `write` and `watch` belong on their own channel.

**Paths** are relative to the folder, `/`-separated, with `""` (or a missing or `null` `path`) for the root. Empty and `.` components are ignored.
- `..` and a leading `/` are `EACCES`, and NUL is `EINVAL`. On Windows `\` and `:` are `EINVAL` too, which also rules out alternate data streams and drive letters.
- Every operation resolves the parent directory with `realpath`, and refuses it with `EACCES` when it lies outside the folder.
- The last component gets lstat semantics. The kernel follows symlinks itself, so `stat` reports a symlink as one, `list` on a symlink is `ENOTDIR`, and `open` on a symlink is `ELOOP`.
- Operations that change the namespace run one at a time, and no other operation runs alongside them. The lock covers every link and every folder of the node, so no page can swap a directory for a symlink between the check and the use.

**Operations**, with their arguments and answers:

| op | arguments | answer |
| --- | --- | --- |
| `stat` | `path` | OPENED `attr` |
| `list` | `path` | OPENED `{}`, then NDJSON lines `{"name","attr"}` as DATA; an entry that vanishes meanwhile is left out |
| `mkdir` | `path` | `{}`; not recursive, mode `0777` minus the umask |
| `rmdir` | `path` | `{}` |
| `unlink` | `path` | `{}`; `EISDIR` for a directory |
| `rename` | `from`, `to` | `{}`, `rename(2)`; it replaces a file or an empty directory, a directory onto a non-empty one is `ENOTEMPTY`, a directory onto a file `ENOTDIR`, a file onto a directory `EISDIR` |
| `symlink` | `target`, `path` | `{}`; `target` is stored as given; `EPERM` on Windows |
| `readlink` | `path` | `{ "target" }`; `EINVAL` for anything but a symlink |
| `setattr` | `path`, `size?`, `mode?`, `mtime?` (ms) | `{}`; `size` truncates or extends with zeros, like `truncate(2)`, then `mode` and `mtime` apply. `size` on a directory is `EISDIR`, `size` or `mode` on a symlink is `EINVAL`, and `mtime` doesn't follow a symlink |
| `statfs` | | `{ "bsize", "blocks", "bfree", "bavail" }` of the folder's volume |
| `open` | `path`, `write?`, `create?`, `truncate?`, `exclusive?`, `mode?` | `{ "fh", "attr" }` |
| `read` | `fh`, `offset`, `size` (≤ `maxIo`), `ifMatch?` | OPENED `{ "etag", "size", "offset", "length" }`, then `length` bytes as DATA |
| `write` | `fh`, `offset` | OPENED `{}`; the page sends the bytes, then END, and the node ENDs once they are written |
| `release` | `fh` | `{ "attr" }` for a write handle, `{}` otherwise |
| `watch` | `mounts` (no `mount`) | OPENED `{}`, then NDJSON lines as DATA |

- `attr` is `{ "kind": "file"|"directory"|"symlink", "size", "mtime" (ms, fractional), "mode" (permission bits), "ino", "etag" }`.
- `etag` is `"<size>-<mtimeNs>-<ino>"`, quotes included, so `attr.etag` and `ifMatch` compare as plain strings.
- The folder's root can't be removed or renamed, or replaced by a rename (`EBUSY`).
- **Arguments:**
  - `write`, `create`, `truncate`, `exclusive` and `readonly` count when they are truthy, as in JavaScript.
  - Offsets and sizes are integers from 0 to 2^53−1, `mode` an integer up to `0o7777`, and `mtime` a number. `fh` is a positive integer, and any other `fh` is `EBADF`.
  - A `path`, `from`, `to` or `target` that isn't a string is `EINVAL`, and so is any other argument of the wrong type, `null` included.
  - An unknown `op`, or no `op`, is `EINVAL`. A `mount` that isn't exported is `ENOENT` with `no such folder`.

**Handles.** `open` without `write`, `create`, `truncate` or `exclusive` gives a name handle, which holds nothing open between calls. Any of those flags gives a write handle, which keeps the file open (read and write) until `release`.
- `create`, `exclusive` (with `create`), `truncate` and `mode` (default `0666` minus the umask) apply at `open`, and their errors come back there. A directory is `EISDIR` (`EEXIST` with `create` and `exclusive`), anything but a regular file `EINVAL`.
- Handles are numbered per link from 1. A link holds at most 4096 per folder, and the next `open` is `EMFILE`.
- An `fh` sent with another `mount` than the one it was opened on is `EBADF`, and so is one already released.
- When the link closes, its handles close. What was written stays, as with a crashed local process.

**Reads.** A read on a name handle opens the file again, through the path rules. When `ifMatch` is given and differs from the file's current etag, it is `ESTALE` with `file changed since it was opened`, so the kernel starts over instead of mixing two versions. A read on a write handle reads its descriptor and ignores `ifMatch`.
- OPENED carries the file's `etag` and `size` with the `offset` asked for and the `length` that follows. The read is short at EOF, and `length` is 0 past it.
- If the file shrinks while the bytes are being sent, the stream is `RESET ESTALE`.

**Writes happen in place,** like `open(2)` and `pwrite(2)`, in any order and with holes. Writing in place keeps hard links, extended attributes, ACLs and ownership. Programs that want atomic replacement write a temporary file and rename it, and those are operations too.
- Before OPENED, the node checks the call: `EROFS` for a read-only folder or `readonly: true`, `EINVAL` for a bad `offset`, `EBADF` for an unknown handle, one from another folder, or a name handle.
- Each DATA is written at its place before it is credited, so the stream window bounds the memory used. More than `maxIo` bytes in one call is `RESET EINVAL`, and what came before stays written.
- After the page's END and the last write, the node sends END. A page RESET leaves what was written.

**Watch.** `OPEN { "kind": "fs", "op": "watch", "mounts": ["project", …] }` streams NDJSON lines until the page sends END or the link closes:

```
{"mount":"project","paths":["src/a.ts","src"]}
{"mount":"project","all":true}
{"ping":1}
```

- A missing or empty `mounts`, or one that isn't a list of strings, is `EINVAL`, and a name that isn't exported is `ENOENT`.
- `paths` name what changed and its parent directory (`""` for the root), coalesced over 50 ms. Past 256 paths the line is `all`, and so is a change the watcher can't name (the root itself, an event-queue overflow).
- A watcher that fails or can't start is retried every second. It sends one `all` when it is lost and one when it is back. A folder that is renamed away or removed is lost until it comes back.
- A ping goes out every 15 s.
- Changes seven made itself come back too.
- One watcher per folder serves every watch stream on every link, and it lingers 5 s after the last stream ends, so replacing a watch is cheap.
- A page that doesn't read gets its backlog folded into one `all` per folder instead of a growing queue.

The watcher per OS:
- **Linux:** inotify through fsnotify, one watch per directory, so the node walks the folder at start and adds every new directory. Symlinks aren't followed. A tree past `fs.inotify.max_user_watches` makes the watcher fail, which is then retried every second.
- **macOS:** one FSEvents stream on the folder (`FSEventStreamCreate` with file events, through purego, so the binary stays free of cgo). It holds no descriptor on anything in the tree and never follows symlinks, so tree size doesn't matter. A dropped or coalesced batch (`MustScanSubDirs`) is an `all`, and a folder that is moved or removed (`RootChanged`) is lost until it comes back.
- **Windows:** one `ReadDirectoryChangesW` on the folder with `bWatchSubtree`, which is what libuv does. A handle on every directory would stop Windows from renaming any of their parents.

**Mount updates** (page cap `mounts`). When folders are added or removed while a link is up (`fs.Server.Add` and `Remove`, from the TUI's capabilities screen; `fs.Server.Live` says whether a linked page takes them), the node opens `OPEN { "kind": "mounts", "mounts": [ … ] }` on the control channel, with the full list in HELLO's shape. It replaces the previous list, and there are no deltas. The page answers OPENED `{}` and END, and the node sends END. Updates go one at a time per link, and one the page doesn't finish within 10 s is reset with `ETIMEDOUT`. A removed folder's handles close (`EBADF` if the name comes back), its operations are `ENOENT`, and a watch stream covering it ends with `RESET ENOENT: no such folder`. `--mount` alone never changes the set.

**Errors.** The code is the POSIX name, `EIO` when there is none. The message is libuv's lower-case text for it (`no such file or directory`), or slicc's own for the cases it names (`path escapes the folder`, `read-only folder`, `file changed since it was opened`, `the folder root cannot be moved or removed`). It never contains a host path. Windows errors are mapped the way libuv maps them, for example `ERROR_ACCESS_DENIED` to `EPERM` and `ERROR_SHARING_VIOLATION` to `EBUSY`.

**Windows.**
- Paths go through `\\?\` and `GetFinalPathNameByHandle`, so device names (`CON`, `NUL`) and 8.3 short names are plain names inside the folder.
- Files are opened with `FILE_SHARE_DELETE` and without following reparse points.
- Symlinks and junctions both show as `kind: "symlink"`, `readlink` works on both, and `unlink` removes the link itself.
- `ino` is the file index, and `mode` is `0666`, or `0444` for a read-only file, plus `0111` for a directory. `setattr` `mode` sets or clears the read-only attribute.
- `unlink` clears the read-only attribute first, as `DeleteFile` would refuse otherwise.
- `rename` gives the POSIX answers above, replacing an empty directory itself.

## Network policy

A node checks every address it connects to for the link's `tcp`, `http`, `ws` and `ssh` forwarding and every `udp` datagram's destination, and drops refused addresses from `resolve` answers. The check runs on each address actually connected, after DNS and for every Happy Eyeballs attempt, so a name can't rebind past it. A refusal is `EACCES`.

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

**Local default** (the bridge to the browser `slicc` controls). The remote default, with loopback and the private ranges (`127.0.0.0/8`, `::1/128`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7`) allowed instead of denied, so this machine and the LAN are reachable. `--deny 127.0.0.0/8` and `--deny 192.168.0.0/16` take loopback and the LAN back out.

**Protected ports**, in both defaults and in every mode (`slicc`, `attach`, `open`, `pair`, `--default-browser` and `follow`). Two kinds:
- **Debugging ports:** ports 9222–9229; every port in a `DevToolsActivePort` file that browser discovery would read (default user-data dirs, SLICC profiles and six's profiles); the port of every running process started with `--remote-debugging-port=<n>` (from the process list, as discovery reads it: wrappers skipped, and for port 0 the `DevToolsActivePort` of its `--user-data-dir`); and the debugging port of every browser the node was given or launched (`slicc`'s browsers, `attach --cdp` and `--cdp-extra`, `follow --cdp`). Nothing is probed: files and the process list are only read. The files are read at startup, every 5 s, and again whenever a `tcp`, `http`, `ws` or `ssh` connection goes to an address of this machine on a port not yet denied. The process list is read at startup, then in the background, one scan at a time: about every second while such connections keep coming (until 30 s pass without one; between two scans it pauses at least as long as the last one took), and every 30 s while idle. A connection uses the list only if its scan started less than about 1 s earlier. With an older list (the first connection after a quiet spell, say) it waits for a scan that started after it came, bounded by the scan's timeout (5 s, plus the rest of a scan already running), and if that scan fails or times out the connection is refused (`EACCES`, reason `a browser debugging port: couldn't list running browsers`). Freshness counts from the start of a scan, on purpose: on a host whose scan takes longer than about a second (a slow PowerShell `Win32_Process` query), most connections to this machine wait for a scan, which is the price of refusing the first connection to any browser that has been running for about a second. If no process list can be read at startup (no working `ps`, and on Linux no readable `/proc`), the node says so once on stderr: `slicc: can't list running browsers here (<why>); connects to this machine's own addresses are refused`. Such a host is fail-closed by design: every connection to its own addresses is refused until a scan works. A scan that fails later is reported once (`couldn't list running browsers for their debugging ports`), and again when scanning works again; the last good list is kept, but an old list never lets a connection through by itself. Connections to other hosts never wait. So a browser that starts later is denied before its first connection, once it has been running for about a second. No `--allow` reopens one, not even `--allow 127.0.0.1:9222` or `--allow 127.0.0.0/8`: a debugging port wins over every other rule. Only `--expose-debug-port <port>` (repeatable, every mode) leaves one open, and only a port that is a debugging port when the node starts; any other port, or the port of a browser the node was given or launched, is an error (exit 1), since `cdp` is the way to those browsers. So seven never reaches raw CDP on this machine, `<host>.slicc.internal:9222` included, unless the user named that port.
- **The node's own listeners** (the kernel listener and, for `open` and `pair`, the loopback listener): denied the same way, but an `--allow` for exactly the same address and port reopens one.

Both are denied:
- on loopback, each as `127.0.0.0/8:port`, `127.0.0.1/32:port` and `[::1]/128:port`, so `--allow 127.0.0.1/32` doesn't reopen them;
- on every address of this machine, as `<address>/32:port` or `<address>/128:port`. The interfaces are enumerated at check time and cached for 5 seconds, so a new interface counts. If they can't be enumerated, those ports are denied on every address.

**Browser endpoints.** Each listed `cdp` browser's own endpoints are denied too, so a browser on another machine (`--cdp http://10.0.0.5:9222 --allow 10.0.0.0/8`) is reachable only through `cdp`:
- the host and port of its CDP URL, and of the `webSocketDebuggerUrl` it reports;
- a host that is a name is resolved when the browser is added (the `--hosts` file first, then the system resolver), and every address it resolves to is denied on that port;
- while the browser is listed, its names are resolved again on every 2 s check, and new addresses are added to the old ones, so nothing is dropped while it's listed. IP literals aren't looked up again;
- when the browser is listed again after it stopped answering, its endpoints are resolved again and replace the old ones. If resolving fails, the old ones stay.

They are listed in HELLO's `policy.deny` as `<address>/32:port` or `[<address>]/128:port`.

Only a user allow for exactly the same address and port reopens one of these, and only on another machine: on this machine the browser's debugging port wins.

**`resolve` filtering.** An address is kept when the policy allows it on at least one port. When every address is dropped, the answer is an empty list.

**HELLO.** `policy.summary` names the default and lists the `--allow` and `--deny` rules on top of it, and `exposes debugging ports <ports>` after them with `--expose-debug-port`. `policy.allow` and `policy.deny` list the `--allow` and `--deny` rules plus the protected ports' loopback denies (debugging ports found at startup included, later ones not, since HELLO was sent already) and the browser endpoints, not the default ranges or the per-interface denies.

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

## Remote setup: `slicc <join-url> follow`

The initial ICE interface snapshot probe and its refreshes are bounded and panic-safe. If discovery fails or returns no addresses, slicc automatically uses offered TURN-over-TLS TCP servers with credentials, and reports an error if no suitable relay is offered. A probe that times out reuses the last good snapshot; without one, it switches to that relay only when a suitable relay is offered, and otherwise waits for the probe to finish. This fallback disables host and server-reflexive candidates and mDNS and retains certificate verification and existing sharing restrictions. Go cannot forcibly cancel an OS enumeration already in progress; a stuck call is limited to one shared in-flight probe across reconnects, and later bounded waits on that probe return immediately after its deadline. Healthy discovery retains normal ICE transports and refreshes interface snapshots through the same guarded probe, retaining the last good snapshot if a later probe fails or times out. mDNS is disabled when no interface is eligible; otherwise the healthy path retains Pion’s mDNS behavior, including its own OS address queries, which are outside this probe guard. This does not enable network, compute, browser or filesystem sharing; other sharing features may independently require interface access.

```
slicc <join-url> follow [--exit] [--allow <rule>]... [--deny <rule>]... [--expose-debug-port <port>]... [--name <name>] [--cdp <url>]...
             [--runner <argv…> [--]] [--shell <argv…> [--]] [--eval <argv…> [--]] [--eval-quiet <dur>] [--sftp | --no-sftp]
             [--mount <path>[:<name>][:ro]]... [--jerk] [--jerk-seal] [--json]
slicc - follow [flags]
slicc @<file> follow [flags]
```

`follow` links this machine to a seven on another machine through the tray hub (see [Tray hub signaling](#tray-hub-signaling-remote-setup)). It serves the link until interrupted (`SIGINT`, `SIGTERM`) and reconnects when the link drops. Status lines go to stderr, and `SLICC_DEBUG=1` adds diagnostics. The join URL is never printed.

**The join URL off the command line.** On the command line, `ps` shows the join URL to every user of this machine. In its place, `-` reads it from stdin and `@<file>` from a file: the first non-empty line, trimmed. From stdin slicc reads only up to that line's end, so the rest of stdin is left for `prompt -`. The file must be a regular file, not a symlink. On macOS and Linux it must also belong to you and be readable by no one else (`chmod 600`). Exit 2 follows when there's no join URL, when the file can't be read, breaks those rules or is over 64 KiB, or when what's there isn't a join URL. The message names the file but never shows what it holds, inside seven too. `prompt`, `watch`, `wait` and `abort` take `-` and `@<file>` the same way, inside seven too. There is no environment variable for it, because `--runner`'s processes would inherit it.

**Flags.**
- `--exit`: offer this machine's network to seven, as `tcp` and `resolve` streams (cap `net`), `http` streams (cap `http`), `ws` streams (cap `ws`) and `udp` streams (cap `udp`) under the remote policy, so public addresses only by default. Without `--exit`, all five are refused with `EOPNOTSUPP`.
- `--allow`, `--deny`: policy rules on top of the remote default (see [Network policy](#network-policy)). This machine's debugging ports stay denied on every address of it, whatever `--allow` says, `--allow 127.0.0.1/32` included.
- `--expose-debug-port <port>`: let seven reach this one debugging port after all (repeatable), as for `attach`. The `--cdp` ports stay denied.
- `--name`: the link's `name` and `host` in HELLO, as for `attach`.
- `--json`: NDJSON lifecycle events on stdout (see [Lifecycle events](#lifecycle-events---json-on-attach-open-pair-and-follow)). Not with `--signal-only` (exit 2).
- `--runner`, `--shell`, `--eval`, `--eval-quiet`, `--sftp`, `--no-sftp`: compute over SSH (cap `ssh`), exactly as for `attach`. `direct-tcpip` forwarding goes through the remote policy, so it reaches public addresses only unless `--allow` says otherwise. `--runner` works with or without `--exit`.
- `--cdp <url>`: let seven drive this browser over the link (repeatable, in any form `slicc browsers --cdp` takes; each must answer when `follow` starts). See [`cdp`](#cdp-browser-control), and its warning: the seven on the other machine gets full control of every browser listed. No browser is `seven: true`. Without `--cdp`, `follow` doesn't offer `cdp`, and a `cdp` OPEN is refused with `EOPNOTSUPP`. Each browser's debugging port and its own endpoint addresses are [denied](#network-policy) to `tcp`, `http`, `ws`, `udp` and `ssh` forwarding.
- `--mount <path>[:<name>][:ro]`: share this folder with the seven on the other machine (cap `fs`, repeatable); see [Host folders](#host-folders). It can read the folder, and change it unless `:ro`.
- `--jerk`: offer this machine's slicc-jerk for status and sign-ins (cap `jerk`). `--jerk-seal` also lets the seven on the other machine seal this machine's tokens into its vault (cap `jerk-seal`) and implies `--jerk`. Without host jerk, `follow` says `jerk: no slicc-jerk 0.6.0 or later on PATH or in slicc's data folder; not offering it` (or, for an older jerk, `jerk: host jerk <version> is older than 0.6.0; run slicc install jerk`) and runs on without it. See [`jerk`](#jerk-slicc-jerk-over-the-link).

HELLO from `follow`: `{ "v": 1, "role": "node", "name", "host", "version", "mode": "remote", "node", "caps", "routes": { "prefixes": [], "exit" }, "policy" }`. `caps` holds `net`, `http`, `credentialed`, `ws` and `udp` with `--exit`, `ssh` with `--runner` (plus `sshHostKey`), `cdp` (and HELLO `browsers`) with `--cdp`, `fs` (and HELLO `mounts`) with `--mount`, `jerk` (and HELLO `jerk`) with `--jerk`, `jerk-seal` with `--jerk-seal`, and always `join`.

**Identity.** The device is its DTLS certificate. `follow` keeps one ECDSA P-256 certificate per hub origin (`scheme://host:port`), valid for 20 years, in `<config>/slicc/tray-certs.json` (mode `0600`). Every link to that hub uses it, so the fingerprint stays the same across reconnects, restarts and updates. The first run on a hub mints it and says `new device, fingerprint sha-256 …`; later runs say `device, fingerprint …`. Every dial attaches with a fresh `controllerId`, which is only a routing handle.

**`join`** (cap `join`), page → node. OPEN `{ "kind": "join", "url" }` carries a rotated join URL (see Rotation below). The node answers OPENED `{}` then END, or `RESET EACCES` when the URL is not a join URL on the hub it follows.

**`unlink`** (cap `join`), page → node. OPEN `{ "kind": "unlink" }`. The node answers OPENED `{}` then END, deletes the join state and the certificate for that hub, closes the link, says `unlinked by <name>` with the name from seven's HELLO, and exits 0. A non-retryable `bootstrap.failed` with code `UNLINKED` does the same, with the name from the last link to that hub. seven sends it after the node's answer and then drops the peer connection. The next `follow` mints a new certificate, so seven sees a new device.

**Reconnects.** A link that drops, a link not up within 45 s of the offer, a failed bootstrap and any other signaling error lead to a new dial. Dials are spaced 1 s apart at first, doubling up to 30 s, each wait randomized by ±25 %, and back to 1 s after a link was up. A bootstrap the hub restarts (a retryable `bootstrap.failed`) starts a new peer connection without a new dial. The node keeps polling the bootstrap after its answer until seven's HELLO arrives, then stops. A link that closes before HELLO waits up to 3 s for a failure code from the hub, and without one it is an ordinary reconnect. When ICE fails with no candidate pair on a link that never came up, follow prints a retryable `error` (and the same on stderr) that names the ICE failure and hints that UDP may be blocked before it reconnects.

**Stops** with exit 1 and says why on `INVALID_JOIN_CAPABILITY`, `TRAY_EXPIRED` or `TRAY_SUPERSEDED_MISSING_JOIN_URL` from the hub, and on `UNTRUSTED`, `UNSUPPORTED_RUNTIME` or `NO_FINGERPRINT` (an internal error: the answer had no SHA-256 fingerprint) from seven. The device's fingerprint is always SHA-256.

## Remote control: `slicc prompt`, `watch`, `wait` and `abort`

```
slicc <join-url> prompt [--allsettled <quiet>] [--steer | --reject] [--thinking] [--json] [--plain] [--abort-on-interrupt] <text>|@file|-
slicc <join-url> watch [--thinking] [--json] [--plain] [--since <entry-id>]
slicc <join-url> wait [--allsettled <quiet>] [--timeout <limit>] [--json]
slicc <join-url> abort
slicc prompt --cdp <url> [--origin <origin>]... [flags] <text>|@file|-
slicc watch --cdp <url> [--origin <origin>]... [flags]
slicc wait --cdp <url> [--origin <origin>]... [flags]
slicc abort --cdp <url> [--origin <origin>]...
slicc prompt|watch|wait|abort --signal stdio [flags] …
```

These commands talk to seven's agent over a link that offers nothing to seven (`caps: []`).
- **With a join URL:** slicc dials the tray as `follow` does (see [Remote setup](#remote-setup-slicc-join-url-follow)), with the same device certificate, so seven sees the same device. A `follow` already running on this machine keeps its link, because the two processes have different `node` ids. These commands never handle `join` or `unlink`. When seven has unlinked the device they say `seven unlinked this device` and exit 1, and they leave the join state and the certificate alone.
- **With `--cdp`:** slicc links to the seven tab in that browser as `attach` does (see [Local setup](#local-setup-slicc-attach)), with `--origin` as there. A running `slicc` or `attach` on that browser keeps its link.
- **With `--signal stdio`** (for a test harness that owns CDP itself): the local setup messages go as NDJSON on stdin and stdout, exactly as for `attach --signal stdio`, and `--cdp` isn't needed.
  - stdout also carries the events, as with `--json`. A line with a `nonce` is a setup message, and every other line is an event.
  - `prompt` can't read its text from stdin (`-` exits 2).
- **Setup:** slicc waits up to 1 minute for seven's HELLO, then gives up with exit 1.
- **No permission:** without `agent` in seven's HELLO, slicc says `this seven doesn't allow remote control from this device; turn it on in seven's Linked devices` and exits 3. A local link gets `agent` by default. A remote one gets it once *Allow remote control* is on for this device.
- **Privacy:** prompt text and event contents never go to telemetry or logs, and the join URL is never printed.

**`prompt`** sends one prompt and streams the run it belongs to.
- **Prompt text:** the words after the flags, joined with spaces. `@file` reads a file, and `-` or `@-` reads stdin. Trailing newlines are dropped.
  - An empty prompt, text that isn't UTF-8, or a prompt over 16 MiB exits 2.
  - A prompt up to 60 KiB goes in the OPEN as `text`. A longer one goes as DATA after it (see `agent` below).
- **When seven is busy:** by default the prompt is queued as a follow-up (`whenBusy: "followUp"`). `--steer` steers the running turn, and `--reject` doesn't send it at all: slicc says `seven is busy` and exits 1. `--steer` and `--reject` can't be combined.
- **Output:** the assistant's text goes to stdout as it streams. Separate text entries are separated by a blank line, and stdout ends with a newline.
  - Tool calls, tool results, notices and errors go to stderr, and so does thinking with `--thinking`.
  - The prompt's own `user` event isn't shown.
- **`--json`:** the events are printed on stdout instead, one NDJSON line each, exactly as seven sent them, including `delivered` and the final `end`. Nothing is rendered.
- **Exit status:**
  - `0` when the run ends with `end` `stop`;
  - `1` for `end` `error` (`the run failed: <message>`), for `end` `aborted` (`the run was aborted`), and for a stream or link that ends before `end` (`lost the link to seven; the run goes on there`);
  - `2` for bad usage;
  - `3` without permission;
  - `130` on Ctrl-C or `SIGTERM`.
- **Ctrl-C** leaves the run going in seven. With `--abort-on-interrupt`, slicc first opens `abort`.

**`prompt --allsettled <quiet>`** (for benchmarks and scripts) goes on after the prompt's run until seven has nothing left to do. The cone can end a run while scoops it started are still working, and run again when their results come in. `<quiet>` is a duration such as `2m` or `0s`.
- **Needs** a seven whose agent is @ai-ecoverse/slicc-agent 6.15.0 or later, the first that reports `busy`. `wait` needs it too.
- **Settled** means seven says it isn't busy (`busy` false, see [`agent`](#agent-page-cap-agent-node--page) below), and nothing has come from seven for `<quiet>`. The quiet period starts once the stream is open, and every line from seven restarts it.
- **A seven that doesn't report `busy`:** before sending anything, slicc opens a `watch` with `busy`. When seven's answer doesn't say whether it's busy, slicc says `this seven doesn't report when its agent is idle; update it` and exits 1. The prompt isn't sent.
- **Output:** the prompt's run streams as usual. After its `end`, slicc opens `watch` with `busy`, and `since` the last event it showed, so the cone's later runs stream as they would in `watch`, with nothing shown twice.
- **`--json`:** the prompt's events up to its `end` line, then the watch's events, as seven sent them. The last line is slicc's own `{"t":"settled"}`.
- **Exit status:** the prompt's own run decides. An `end` with `error` or `aborted` exits 1 at once, as without `--allsettled`. Otherwise slicc exits 0 once seven has settled, whatever the later runs did.
  - A stream or link that ends before seven has settled exits 1 (`lost the link to seven; the run goes on there`).
  - Bad usage, a missing permission and Ctrl-C exit as for `prompt` above.
- **No time limit of its own.** A lick that's held in seven's outbox keeps seven busy, and so does a scoop that never finishes. Bound the wait with the caller's own timeout, or with `wait --timeout`.

**`wait`** follows the agent without sending anything, and exits once seven has settled, as `prompt --allsettled` does. `--allsettled <quiet>` sets the quiet period (default `0s`: exit as soon as seven isn't busy).
- **Output:** `slicc: seven is idle` on stderr, and nothing on stdout. With `--json`, the events as seven sent them, then `{"t":"settled"}`.
- **`--timeout <limit>`:** give up after `<limit>` (default: no limit). slicc says `seven didn't settle within <limit>` and exits 124.
- **Exit status:**
  - `0` once seven has settled;
  - `1` for a seven that doesn't report `busy`, and for a stream or link that ends first;
  - `2` for bad usage;
  - `3` without permission;
  - `124` on timeout;
  - `130` on Ctrl-C or `SIGTERM`.

**`watch`** follows the agent until Ctrl-C or `SIGTERM`, then exits 0.
- **Output:** `user` events go to stdout as `> <text>`, on one line. Assistant text goes to stdout as for `prompt`, and a line ends at each run's end. Everything else goes to stderr, as for `prompt`, plus `run ended: <reason>` for a run that didn't end with `stop`. `--json` prints the events instead, with slicc's own lines among them (below).
- **Status lines** on stderr: `slicc: watching <name>`, `slicc: lost the link to <name>; reconnecting`, and for the tray `<reason>; trying again in <wait>`.
- **Reconnects:** when the stream ends or is reset, slicc opens a new `watch` on the same link. When the link goes, it sets up a new one, through the tray again or by waiting for seven over CDP. Waits start at 1 s, double up to 30 s with ±25 % jitter, and go back to 1 s once a stream opens.
- **Resuming:** each new stream asks for `since`, the id of the last event slicc saw, so nothing still in the transcript is lost. Nothing is shown twice either. For `text` and `thinking`, slicc counts the bytes it has shown per entry and skips that many from the entry's replay. Any other entry is skipped when its id was already seen. `run` events have no id and aren't replayed. When seven answers `gap: true`, slicc prints `some events were missed`.
- **`--since <entry-id>`:** the first stream asks for `since` with this id (any event's `id`), as [`agent`](#agent-page-cap-agent-node--page)'s `watch` does, so a program that kept the last id it saw resumes there across restarts of `slicc watch`. seven replays that entry when it's `text` or `thinking`, then every entry after it, or answers `gap: true` when it no longer has it. Later streams resume from the last event slicc saw, as above.
- **slicc's own lines with `--json`**, which are slicc's and not seven's, like `prompt`'s and `wait`'s `{"t":"settled"}`: `{"t":"opened","head","gap"?,"busy"?}` first on every stream that opens, before seven's events on it, with `head`, `gap` and `busy` from seven's OPENED (so a `gap` shows on stdout, not only on stderr); and `{"t":"reconnecting","attempt","wait"}` before each new try, with `attempt` counting the tries since a stream last opened and `wait` in milliseconds. A reader that ignores unknown `t` values sees seven's events as before. `run` events still have no id. Fixture: [`watch.ndjson`](test/integration/testdata/native/watch.ndjson).
- **Exit status:** `1` when the link can't be set up for good (a terminal tray failure, or the CDP connection closing), `2` for bad usage, `3` without permission.

**`abort`** aborts the agent's current run. It prints `slicc: aborted the run`, or `slicc: nothing was running`, and exits 0. Otherwise it exits 1 on errors, 3 without permission and 130 on Ctrl-C.

**Styling.** When a stream is a terminal (and `TERM` isn't `dumb`), its lines are styled:
- tool calls `⚙ <name> <summary>`;
- results `  ↳ <summary>`, or `  ✗ <summary>` for a failure;
- notices `• <text>`, and errors `✗ <message> <detail>`;
- thinking dim and italic, and prompts in `watch` bold.

`NO_COLOR` keeps the layout without colors. When stdout and stderr are the same terminal, a line on one first ends an unfinished line on the other.

Otherwise, or with `--plain`, there are no escape codes:
- `tool <name>: <summary>`, `tool <name> ok: <summary>`, `tool <name> failed: <summary>`;
- `notice: <text>`, `error: <message> (<detail>)`;
- `thinking: <text>`.

Summaries are one line of at most 200 characters. Escape sequences and control characters other than newline and tab are removed from everything seven sends before it's shown.

## Agent inventory: `slicc agents`

```
slicc <join-url> agents [--watch] [--json]
slicc agents --cdp <url> [--origin <origin>]... [--watch] [--json]
slicc agents --signal stdio [--watch] [--json]
```

`slicc agents` lists one seven's agents: the cone its tab shows and that cone's live scoops, each with its status, its queue and its model. It's read-only. It sends nothing to the agent, and nothing it gets carries a prompt, transcript text, a tool's name or output, an agent's name or title, a path, a URL or a credential. Agents' ids are opaque (below), so they don't carry names either.

- **The link:** as for `watch` (see [Remote control](#remote-control-slicc-prompt-watch-wait-and-abort)): a control-only link (`caps: []`) through the tray or over CDP, the same 1-minute wait for HELLO, and `--signal stdio` as there.
- **Permission:** the `agent` cap, as for `watch`. Without it, slicc says `this seven doesn't allow remote control from this device; turn it on in seven's Linked devices` and exits 3.
- **A seven that doesn't know it:** slicc says `this seven doesn't report its agents; update it` and exits 1.
- **A tab without the agent** (seven still starting, or a follower tab): slicc prints seven's reason, such as `the agent is not running`, and exits 1. With `--watch` it keeps trying, as on a lost stream, and says so before each try: `slicc: <reason>; trying again` on stderr, or with `--json` `{"schema":1,"event":"error","message":"<reason>","retryable":true}` on stdout.
- **Without `--watch`:** one snapshot, then exit 0.
- **With `--watch`:** a new snapshot each time one comes from seven, until Ctrl-C or `SIGTERM`, then exit 0. Status lines, reconnects and their waits are those of `watch`. Every new stream starts with a whole snapshot, so nothing is resumed.
- **Output:** a header line, then one row per agent, the cone first, with the first 8 characters of each id (the whole id is in `--json`), and `provider/model`, or `provider` alone when seven leaves the model out:

  ```
  seven on sliccy.com: 1 running, 1 waiting, 1 idle (reported 2s ago)
  ID        KIND   PARENT    STATUS   QUEUED  MODEL                         SINCE
  hR7cZ2mV  cone   -         waiting  0       anthropic/claude-opus-4-6     1m
  x3Lk9aQe  scoop  hR7cZ2mV  running  1       anthropic/claude-sonnet-4-6   40s
  T7wPq0zR  scoop  hR7cZ2mV  idle     0       azure                         3m
  ```

  With `--watch` on a terminal the table is redrawn in place. Otherwise each snapshot is printed in full, after a blank line. Everything seven sends is stripped of escape and control characters before it's shown.
- **`--json`:** one line per snapshot, `{"schema":1,"event":"inventory",…,"receivedAt"}`, with every field of seven's snapshot (see [`inventory`](#511-agent-page-cap-agent-node--page-remote-control)) and `receivedAt`, when slicc got it (RFC 3339, UTC). With `--watch`, slicc's `{"schema":1,"event":"reconnecting","attempt","retryAt","wait"}` comes before each new try, as on `follow`. Fields seven adds later are passed through. Fixture: [`agents.ndjson`](test/integration/testdata/native/agents.ndjson).
- **Stale data:** seven repeats its snapshot at least every 30 s on a `--watch` stream, with a new `reportedAt`, so a snapshot whose `reportedAt` is more than a minute old is stale. `changedAt` and `seq` say when the agents last changed.
- **Exit status:** `0` as above; `1` for a seven that doesn't report its agents, for a link or stream that ends before the first snapshot without `--watch`, and when the link can't be set up for good; `2` for bad usage; `3` without permission; `130` on Ctrl-C without `--watch`.
- **Inside seven** it isn't available yet (`slicc: agents isn't available inside seven`).

### `agent` (page cap `agent`), node → page

- **Who may:** the node opens `agent` streams only to a page whose HELLO lists `agent`, and the page refuses them with `EACCES` otherwise.
- **Several processes, one device:** the hello and HELLO carry the process's `node` id. seven replaces an older link of the same device (any local link, or the same fingerprint over the tray) only when `node` matches. Over the tray it decides once HELLO arrives.

**`prompt`:** `OPEN { "kind": "agent", "op": "prompt", "text", "whenBusy": "followUp" | "steer" | "reject", "thinking"?: true }` on its own channel.
- **Answer:** OPENED `{ "delivered": "run" | "steer" | "follow-up" }` once the agent took it. A `reject` while busy is `RESET EBUSY`.
- **Then DATA:** NDJSON events, starting at this prompt's own `user` event, until the run that includes it ends. The last line is `{"t":"end","reason":"stop"|"aborted"|"error","message"?}`, then END. The node sends END right after OPENED.
- **Long prompts:** a prompt over 60 KiB goes as `"length": <bytes>` instead of `text`.
  - The page answers OPENED `{}` at once.
  - The node sends exactly that many bytes of UTF-8 as DATA, then END. A different count is `RESET EPROTO`.
  - The page then delivers it. Its first line is `{"t":"delivered","delivered"}`, or it resets with `EBUSY` instead.
- **Aborting:** a RESET from the node, or a closed link, doesn't abort the run. Only `abort` does.

**`watch`:** `OPEN { "kind": "agent", "op": "watch", "since"?: "<entry id>", "thinking"?: true, "busy"?: true }` on its own channel.
- **Answer:** OPENED `{ "head": "<entry id>", "gap"?: true, "busy"?: true | false }`, then NDJSON events until either side resets.
- **Without `since`:** the events start at the next one.
- **With `since`:** the page first replays `since`'s own entry when it's `text` or `thinking` (its whole text so far, as one `delta`), then every entry after it. `gap: true` means the transcript no longer holds `since`, and the events start at the next one.
- **With `busy`** (`prompt --allsettled` and `wait`): OPENED says whether the agent has anything left to do, and a `busy` line follows each time that changes. An OPENED without `busy` means seven doesn't report it.

**`abort`:** `OPEN { "kind": "agent", "op": "abort" }`, answered OPENED `{ "aborted": true | false }` (`false` when nothing was running), then END.

**`inventory`:** `OPEN { "kind": "agent", "op": "inventory", "watch"?: true }`, answered OPENED with a snapshot of the agents (§5.11). Without `watch` the page sends END next. With it, a `{"t":"inventory",…}` line with a whole new snapshot follows each change. A seven that doesn't know the op answers `EINVAL` (from before it) or `EOPNOTSUPP`, and the node treats both as unsupported.

**Events,** one JSON object per line:

| event | meaning |
| --- | --- |
| `{"t":"user","id","text","delivered"}` | a prompt |
| `{"t":"text","id","delta"}` | assistant text as it streams |
| `{"t":"thinking","id","delta"}` | thinking, only on streams opened with `thinking` |
| `{"t":"tool","id","call","name","summary"}` | a tool call, with a one-line summary such as the bash command |
| `{"t":"tool-result","id","call","ok","summary"}` | its result |
| `{"t":"run","state":"start"\|"end","reason"?}` | a run starts or ends |
| `{"t":"notice","id","text"}` | retries, compactions, rewinds |
| `{"t":"error","id","message","detail"?}` | an error |
| `{"t":"delivered","delivered"}` | a long prompt was taken (`prompt` only) |
| `{"t":"busy","busy","why"?}` | the agent became busy or idle (`watch` with `busy` only) |
| `{"t":"end","reason","message"?}` | the last line of a `prompt` stream |

- **Ids:** `id` is the transcript entry id, and `delta` lines for one `id` arrive in order.
- **Limits:** `summary` is at most 200 characters. Credentials and tool output beyond the summary are never sent.
- **Unknown events:** the node ignores events and fields it doesn't know.

## Driving seven's browser from another computer: `slicc <join-url> cdp`

```
slicc <join-url> cdp [--port <n>] [--json]
slicc - cdp [--port <n>] [--json]
slicc @<file> cdp [--port <n>] [--json]
slicc cdp --cdp <url> [--origin <origin>]... [--port <n>] [--json]
slicc cdp --signal stdio [--port <n>] [--json]
```

`slicc … cdp` serves seven's browser on this machine as a CDP endpoint, the way Chrome serves its own debugging port, so stock Playwright (`chromium.connectOverCDP("http://127.0.0.1:9333")`), Puppeteer (`puppeteer.connect({ browserURL })`), slicc-cdp's `playwright-cli --cdp 127.0.0.1:9333` and raw CDP clients drive the browser of the seven on the other machine. Every connection is a [`cdp`](#519-cdp-page-cap-cdp-in-node--page-a-cdp-endpoint-on-the-node) stream that ends in seven's page at the same hook and guard a kernel program's CDP goes through, so seven's own pages, the vault and the other protected targets stay out of reach, and calls that guard refuses are refused here too.

- **The link:** as for `prompt` (see [Remote control](#remote-control-slicc-prompt-watch-wait-and-abort)): a link that offers nothing to seven (`caps: []`), through the tray as this device with the same certificate and its own `node` id, so a running `follow` keeps its link; or over CDP with `--cdp` and `--origin`; or `--signal stdio` for a test harness. `-` and `@<file>` take the join URL off the command line. slicc waits up to 1 minute for seven's HELLO.
- **Who may:** seven checks every connection against this device's *Browser* access in its Linked devices, off by default, and against its *Browser control from linked devices* switch. A local link (`--cdp`, not paired) always has it.
- **Listening:** on `127.0.0.1:<port>`, plus `[::1]:<port>` when that can be bound. `--port` defaults to `9333`; `--port 0` picks a free port, and the URL line names it. On macOS, where binding `127.0.0.1` can be refused, slicc falls back to `0.0.0.0:<port>`.
  - A connection from anything but loopback is closed before anything is read from it, as for [kernel services](#59-kernel-listener-on-the-node-local-only). There is no token: any program on this machine can use the endpoint while it runs.
  - A port that can't be bound exits 1 with `port <n> is taken; try --port`.
- **What every request must pass,** as Chrome checks its own port:
  - **`Host`:** `127.0.0.1`, `localhost` or `[::1]`, with or without a port. Anything else is `403 Host header is specified and is not an IP address or localhost.`, Chrome's own text, which keeps DNS rebinding out.
  - **`Origin`:** none, or this endpoint's own origin (`http://127.0.0.1:<port>`, `http://localhost:<port>`, `http://[::1]:<port>`), on WebSocket upgrades and plain HTTP alike. Anything else is `403`, as Chrome 111 and later do without `--remote-allow-origins`.
  - **`Sec-Fetch-Site`:** `cross-site` or `same-site` is `403` on every request, so a page can't reach the endpoint with a request that carries no `Origin`, such as an `<img>` pointing at `/json/close/<id>`. Clients that aren't browsers send no `Sec-Fetch-Site`.
  - Together with `PUT`-only `/json/new` below, that keeps web pages in this machine's browsers out.
- **HTTP,** the part of Chrome's that clients use:
  - `GET /json/version`: `{ "Browser", "Protocol-Version", "User-Agent", "V8-Version", "WebKit-Version", "webSocketDebuggerUrl": "ws://<host>:<port>/devtools/browser/<id>" }`, from the OPENED `version` of a short stream. `<id>` is a random UUID per process, and `<host>` is the request's `Host` without its port.
  - `GET /json` and `GET /json/list`: one short stream with `Target.getTargets`, and one entry per target in Chrome's shape, `{ "description": "", "id", "title", "type", "url", "webSocketDebuggerUrl": "ws://<host>:<port>/devtools/page/<id>" }`, every type but `browser` and `tab`. seven's guard has already filtered the list: protected targets are missing, or cut to their origin.
  - `PUT /json/new?<url>`: creates `about:blank`, attaches flat, navigates to `<url>` on that session (`about:blank` without a query), detaches, and answers with that target's entry. Creating the target with the URL directly would refuse the attach while it is still loading. If attach, the session, or that navigate is refused, the node sends `Target.closeTarget` for the created id before answering, so a refused PUT leaves the target list unchanged.
  - `GET /json/activate/<id>`: `Target.activateTarget`, answered `Target activated`. `GET /json/close/<id>`: `Target.closeTarget`, answered `Target is closing`.
  - `GET /json/new` is `405`, as in Chrome 111 and later: a web page can send a GET without a preflight, but not a PUT.
  - A guard refusal or a CDP error is `500` with the error's message, and any other path is `404`.
- **`/devtools/browser/<id>`** (WebSocket): one stream per socket. Each text message is one stream message, and back, with no id or session rewritten. A binary message closes the socket with `1003`.
- **`/devtools/page/<targetId>`** (WebSocket), for raw clients that use a page socket:
  - **Setup:** one stream, then `Target.attachToTarget { targetId, flatten: true }` with id `-2`. A failure closes the socket with `1011` and the error's message.
  - **To the browser:** a message without `sessionId` gets the page's session added, and one with a `sessionId` (a child session the client attached flat) goes as it is.
  - **To the client:** messages on the page's session lose their `sessionId`, messages on the client's child sessions pass, and messages without a session (browser events) are dropped. The answer to `-2` never reaches the client.
  - **The end:** `Target.detachedFromTarget` for the page's session, or `Target.targetDestroyed` for its target, closes the socket with `1000`.
- **Startup:**
  - A HELLO without `cdp-in` exits 1 with `this seven can't share its browser yet; update it`.
  - slicc opens a first stream before it serves. `EACCES` prints seven's message (such as `browser control from this device is off; turn on Browser for it in seven's Linked devices`) and exits 3. `ENOENT` (seven has no browser to drive) prints seven's message and exits 1.
  - **Up:** stderr says `slicc: serving seven's browser at http://127.0.0.1:<port> (<seven's name>)`.
- **Later:**
  - **The link drops:** every socket closes with `1011 lost the link to seven`, and HTTP answers `502 no link to seven` until the link is back. The listener stays, and slicc dials again with `follow`'s backoff. CDP sessions don't survive; clients connect again.
  - **seven refuses later** (the owner turned *Browser* off, the switch went off, or Stop on seven's indicator): that socket closes with `1008 <seven's message>`, and HTTP answers `403 <seven's message>`. The listener stays, and the next client tries again.
  - **Stopping:** Ctrl-C, `SIGTERM` and the control socket's `stop` close every socket with `1001`, end their streams with END, and exit 0.
- **`--json`:** the [lifecycle events](#lifecycle-events---json-on-attach-open-pair-and-follow) on stdout (`connecting`, `linked`, `link-lost`, `reconnecting`, `error`, `closed`), plus `{ "schema": 1, "event": "listening", "url" }` once it serves and `{ "schema": 1, "event": "clients", "count" }` each time the number of open sockets changes.
- **This machine's other `slicc`s** treat the port as a [debugging port](#65-debugging-ports-every-mode): `follow --exit` and `--runner` forwarding can't hand the leader's seven a way back into its own browser around the kernel's stamp. They find it through the control sockets, whose `status` reports `cdp: { "port", "clients" }`, read with the debugging-port refresh and kept for about 1 s (a `slicc` that doesn't answer keeps its last known port).
  - The control socket is up as soon as the port is bound; when it can't be served, `slicc … cdp` exits 1 and doesn't serve the port unprotected. The endpoint accepts connections from the moment it binds, and closes at once, unread and unanswered, every connection accepted before it is ready: until the startup check has passed, and at least as long after the control socket came up as the other processes' cache and read of it take, plus a margin (2.5 s today). So nothing that arrived before every other `slicc` could see the port is served. The `serving` line and `listening` event come at that ready time.
  - The protection covers this user's `slicc` processes of this release and later only, because control sockets are per user and older releases don't read `cdp` from them: a `follow` started before `slicc update` doesn't know the port until it restarts.
  - **Limits of the startup window:** the protection assumes the other `slicc` processes refresh their list within the stated bound. A hung or older `slicc` process can widen it, so another seven on this machine, linked with loopback allowed, could reach the port in that gap. Other users' programs on this machine can connect to the port anyway, as under *Listening* above.
- **`slicc tray`** shows it with MODE `cdp` and OFFERS `cdp:<port>`.
- **Privacy:** no CDP contents, target URLs or join URL go to logs or telemetry. `SLICC_DEBUG` adds which socket opened and closed and which requests were refused.
- **Exit status:** `0` on Ctrl-C, `SIGTERM` or `stop`; `1` when the port is taken, seven can't share its browser, or the link can't be set up for good; `2` for bad usage; `3` when seven doesn't allow browser control from this device.
- **Inside seven** it isn't available (`slicc: cdp isn't available inside seven`).

## Running links: `slicc tray`

```
slicc tray [--json] [-q] [-a]
```

`slicc tray` lists the `slicc` processes on this machine that hold a link, as `screen -ls` and `tmux ls` do: `follow` (remote, through the tray hub), `cdp` ([a CDP endpoint](#driving-sevens-browser-from-another-computer-slicc-join-url-cdp), through the tray hub or over CDP), and the default flow, `attach`, `open` and `pair` (local). It replaces SLICC six's `host` on the machine side. Inside seven, `slicc tray` is that seven's own tray instead (see [Inside seven](#inside-seven)).

```
PID   MODE    SEVEN           DEVICE     STATE                   OFFERS
4121  follow  SLICC on macOS  3fa9…c210  connected 2h            exit,ssh,fs:proj
5530  follow  SLICC on Linux  88d1…07be  reconnecting (#4, 30s)  ssh
6012  attach  seven           -          connected 1d            exit,cdp,kernel:80
7104  cdp     SLICC on macOS  3fa9…c210  connected 5m            cdp:9333
```

- **`SEVEN`:** the name from seven's HELLO, else its origin's host, else the hub's host in parentheses. Never the join URL.
- **`DEVICE`:** this machine's device, as `follow` and seven's tray see it: for `follow`, the DTLS certificate for that hub, and for `open` and `pair`, the loopback identity. It is the SHA-256 fingerprint, shown as its first and last four hex digits, so a row here matches a row in the leader's `slicc tray`. Over CDP there is none (`-`).
- **`STATE`:** `waiting` (a local mode, until seven links), `dialing`, `waiting for offer`, `connecting`, `connected` with how long, `reconnecting (#<attempt>, <next wait>)`, or `stopped (<code>)` with the hub's or seven's code (`TRAY_EXPIRED`, `UNLINKED`, …) as the process exits.
- **`OFFERS`:** `exit` (the network, `--exit` for `follow`), `ssh`, `cdp`, `jerk`, `fs:<name>` per folder, `kernel:<port>`, and `cdp:<port>`, the port a `slicc … cdp` serves.
- **`--json`:** a JSON array, one object per process: `{ "v": 1, "pid", "mode", "version", "started", "seven"?, "origin"?, "hub"?, "device"?, "state", "since", "attempt"?, "retryAt"?, "code"?, "error"?, "offers", "dials", "links", "lastLinked"?, "ops"?, "peers"?, "cdp"? }`. `cdp` is `{ "port", "clients" }` on a `slicc … cdp`: the port it listens on and the WebSockets open on it. `ops` lists the control ops the process answers (see *Control sockets* below); it is missing from a process older than that field. `hub` is the hub's origin, `error` the last failure with secrets redacted, `dials` the dials so far and `links` the links that came up. These are SLICC six's follower counters.
- **`-q`:** only the PIDs, one per line, for `slicc tray -q | xargs kill`. There's no `stop`.
- **`peers`:** on `open`, `pair` and the default flow's default-browser mode, the fingerprints of the pages it holds a link with (left out when none).
- **`-a`:** also the trays this machine knows but isn't following now, from `tray-certs.json` and `tray-join.json`, as `docker ps -a` does: PID `-`, state `not running`, the leader's name from the last link and the device for that hub. Neither file's join URL is shown.

**Control sockets.** Each of those processes serves a control socket while it runs, `<dir>/<pid>.sock`, and removes it on exit.
- **Where `<dir>` is:**
  - **Linux:** `$XDG_RUNTIME_DIR/slicc`, otherwise `$TMPDIR/slicc-<uid>`.
  - **macOS:** `$TMPDIR/slicc-<uid>`.
  - **Either, when that path is too long for a socket:** `/tmp/slicc-<uid>`.
  - **Windows:** `%LocalAppData%\slicc\run`, or `%UserProfile%\.slicc-run` when that is too long, as AF_UNIX (Windows 10 and later).
- **Who can reach it:**
  - **macOS and Linux:** the directory is `0700`, must belong to the user and not be a symlink, and is tightened when it's more open. Each connection checks the peer's uid (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` on macOS) and closes on anyone else.
  - **Windows:** slicc sets the directory's DACL to the user and SYSTEM only, protected from inheritance, so the sockets in it inherit it. `slicc tray` skips a directory whose DACL is anything else.
  - **Either way,** a process that can't serve a socket there runs on without one.
- **The protocol:** one line of JSON each way, then the process closes the connection. The ops, same-uid only as above (`status` lists the ones a process answers):
  - `{ "v": 1, "op": "status" }`, answered with the status object above. Its `ops` lists the ops this process answers, `status` first, so a client can tell what a running `slicc` of another version supports; a process from before `ops` existed answers only `status`.
  - `{ "v": 1, "op": "stop" }`: exactly what `SIGTERM` does. The process says `bye` to seven, closes the browsers this run launched, and exits 0; `closed.browsers` lists them with `--json`.
  - `{ "v": 1, "op": "detach" }`: the process says `bye` to seven, stops the link, and leaves every browser running, the ones it launched too; with `--json`, `closed` has `browsers: []` and `kept`, the ids of the launched browsers left running (`[]` on `attach`, `open`, `pair` and `follow`, which launch none). A kept browser is found again by the next run: its `DevToolsActivePort` in its SLICC profile leads discovery to it with its seven tab open, and the next `slicc` attaches to it (`attached` with no `launched`) and links that tab.
  - `{ "v": 1, "op": "revoke", "fingerprint": "<page-fp>" }`, on `open`, `pair` and the default-browser mode: close the link with that page, if any, and answer `{ "v": 1, "ok": true, "closed"?: true }` once it has. `slicc unpair` sends it (see [Linking seven in your everyday browser](#linking-seven-in-your-everyday-browser)). A missing fingerprint is `EINVAL`.
  - `{ "v": 1, "op": "leases" }`, `{ "v": 1, "op": "unseal", "leaseId" }`, `{ "v": 1, "op": "seal-off" }` and `{ "v": 1, "op": "seal-on" }`, on a process that offers `jerk-seal`: list, revoke and stop issuing credential leases (see [`jerk`](#jerk-slicc-jerk-over-the-link)). `unseal` answers once jerk is done, within 75 s.
  - `stop` and `detach` are answered `{ "v": 1, "ok": true }` before the process acts. A process that doesn't know an op answers `{ "v": 1, "error": "EINVAL" }`, and a line that isn't a request of version 1 gets `EPROTO`.
  - Signals don't change: `SIGINT` and `SIGTERM` still stop, except that `slicc --keep-browsers` makes `SIGTERM` detach.
- **Stale sockets:** liveness comes from connecting, not from the PID, so a reused PID can't fool `slicc tray`. A socket that refuses a connection is stale, and `slicc tray` removes it and says so on stderr, as `screen -wipe` does.
  - **The lock:** each process holds an `flock` on `<pid>.sock.lock` while it runs, and `slicc tray` removes a socket only while it holds that lock itself and the socket still refuses. So it never removes a live socket.
  - **A lock file deleted underneath:** both sides check after locking that the lock file on disk is still the one they hold, and start over when it isn't.
- **Untrusted text:** `slicc tray` and `--json` strip control characters and terminal escapes from what a process reports, and replace any join URL with `<join-url>`.
- **Why sockets:** link state changes all the time, and a socket answers with the current state where a state file would lag. A `ps` scan sees no link state.

## Inside seven

`slicc` also runs inside seven's kernel, so that one seven's agent or user can prompt, watch and abort another seven's agent, and see and manage this seven's own tray. That build is `slicc.wasm`, slicc-cli's client commands compiled with `GOOS=wasip1 GOARCH=wasm` and `CGO_ENABLED=0`, and it ships as the npm package `@ai-ecoverse/wasi-slicc`, at slicc-cli's own version. seven's page holds the WebRTC link; slicc only reaches the page's link facade.

```
slicc <join-url> prompt [--allsettled <quiet>] [--steer | --reject] [--thinking] [--json] [--plain] <text>|@file|-
slicc <join-url> watch [--thinking] [--json] [--plain] [--since <entry-id>]
slicc <join-url> wait [--allsettled <quiet>] [--timeout <limit>] [--json]
slicc <join-url> abort
slicc tray [--json]
slicc tray url
slicc tray rotate
slicc tray unlink <device>
slicc tray retry
slicc --version
slicc --help
```

- **Commands:** `prompt`, `watch`, `wait` and `abort` with a join URL behave as in [Remote control](#remote-control-slicc-prompt-watch-wait-and-abort): the same flags, output, styling, `--json` events, resuming and exit codes, from the same code. The differences:
  - **Ctrl-C** ends slicc with 130 (the kernel's default for `SIGINT`), `watch` included, and a running prompt goes on in the other seven. `--abort-on-interrupt` isn't offered.
  - **Exit 3** covers the facade's own refusals too. When the facade gives a message (`unlock the vault to continue`, or the other seven's refusal), slicc prints it instead of the Linked devices hint.
  - **A terminal** is a standard stream that is a character device. Styling then follows `TERM`, `NO_COLOR` and `--plain` as usual.
- **`slicc tray`:** this seven's tray, the leader's view (SLICC six's `host`). It asks the facade with the `tray` kind ([§5.16](#516-the-link-facade-page-side-not-on-the-wire-the-in-kernel-slicc)), so it takes no join URL.
  - **`slicc tray`** prints the tray's state (`off`, `connecting`, `ready`, `reconnecting` or `failed`) and its hub origin, the current exit when it is a device or a tailnet node, and one line per device: `DEVICE` (its fingerprint, as `3fa9…c210`), `NAME`, `HOST` (`<label>.slicc.internal`), `MODE`, `STATE` (`connecting`, `connected` or `offline`) and `OFFERS` (`exit` when the device offers to be the exit). `--json` prints seven's answer as one line of JSON.
  - **`slicc tray url`** prints the join URL and nothing else, so `slicc tray url > /dev/clipboard` copies it. It is the one deliberate exception to "the join URL is never printed": nothing else prints it, `slicc tray` and `--json` included, so it reaches a transcript only when asked for. It prints only an answer that is a valid join URL without control characters, and exits 1 otherwise.
  - **Whatever seven answers**, slicc doesn't trust it to keep the rule. `slicc tray`, `--json` and the error messages replace any join URL in the answer with `<join-url>`, and strip control characters and terminal escapes from names and messages. `--json` also drops `url`, `join`, `joinUrl`, `joinCommand`, `followUrl` and `webhookUrl` keys, wherever they are, and prints one line.
  - **`slicc tray rotate`** makes a new join URL; the old one stops working (SLICC six's `host reset`). It prints nothing on stdout, and `made a new join URL; …` on stderr. `unlink` and `retry` also confirm on stderr.
  - **`slicc tray unlink <device>`** drops one device, as Linked devices' Unlink does. `<device>` is a fingerprint, a unique prefix of one (colons and case ignored), or exactly the short form `slicc tray` shows (`3fa9…c210`, or `3fa9...c210`). slicc asks for the status first and matches there, then sends the full id. No match or more than one exits 1.
  - **`slicc tray retry`** reconnects the tray's links, as the Reconnect button does.
  - **Not here:** choosing the exit (that's routing, not tray state), and SLICC six's `host join` and `host leave`: a seven follower runs no kernel or agent of its own, so following stays with `#follow=` and the UI's Leave.
  - **Exit status:** 0, 1 on failure (`ENOTCONN` when the tray isn't connected, a facade that doesn't answer within 1 minute, or 5 minutes for `rotate` and `unlink`, which seven may ask you to approve), 2 for bad usage, 3 when seven refuses (`EACCES`).
- **Everything else** exits 2 with `slicc: <command> isn't available inside seven`: `follow`, `attach`, `open`, `browsers`, the default flow, `update`, `install`, and `prompt`, `watch`, `wait` or `abort` without a join URL. `--help` lists only what works there.
- **What the build leaves out** (build constraints): pion and the link, CDP, SSH, mounts, the kernel listener, the policy, browser discovery, telemetry and updates.

**The link facade.** slicc reads its address from `SLICC_LINK_URL` only, as `tcp://127.0.0.1:<port>` on the kernel's loopback (any `127.x` address, or `localhost`). Without it, or with another shape, slicc exits 1. Each `agent` stream (see [`agent`](#agent-page-cap-agent-node--page)) is one connection, carrying records of a `u8` type, a `u32` big-endian length and the payload, both ways:
- **`1`, control:** JSON, at most 64 KiB.
  - slicc's first record is `{ "v": 1, "join": "<join-url>", "open": { "kind": "agent", "op": … } }`, where `open` is the stream's OPEN, `length` for long prompts included. The join URL goes only there, never in an address, a log or an error.
  - For `slicc tray`, it is `{ "v": 1, "open": { "kind": "tray", "op": … } }`, without `join`. The page answers with one control record, `{ "opened": <result> }` or a `reset`, and slicc closes the connection (see [§5.16](#516-the-link-facade-page-side-not-on-the-wire-the-in-kernel-slicc)).
  - The page answers `{ "opened": { … } }` (the OPENED) or `{ "reset": "ERRNO: message" }`. A `reset` may also come later, as the stream's RESET.
  - Without an answer within 1 minute, slicc gives up: `seven's link facade didn't answer within 1m0s`, exit 1 (`watch` reconnects). `slicc tray rotate` and `unlink` wait 5 minutes, since seven may ask for approval first.
- **`2`, data:** the stream's DATA: a long prompt's bytes up, and NDJSON events down. slicc sends records of at most 64 KiB, and takes records of up to 16 MiB; record boundaries carry no meaning.
- **`3`, end:** that side's END. slicc closes the connection after both ends or a reset.
- **Errors:** an unknown type, a control record over 64 KiB, any record over 16 MiB, a control record that isn't JSON, or a later control record that isn't a `reset` makes slicc send `{ "reset": "EPROTO" }` and fail the stream. `ENOTCONN` (the tray can't be reached) fails `prompt` and `abort` with exit 1; `watch` says `lost the link to seven; reconnecting` and retries with its usual backoff, as it does when a connection drops.

**The host module.** Go on `wasip1` can't open sockets, so the package's `host/slicc-host.mjs` gives `slicc.wasm` the `slicc_host` imports, on the kernel's loopback, through the imports-module API's `ctx.net.connect` and `ctx.net.ready` and the `fd-read`, `fd-write`, `fd-select` and `fd-close` calls (slicc-kernel 1.92.0 or later, the package's `engines.slicc-kernel`): `probe`, `connect(host, port)` (loopback only, non-blocking; `localhost` is connected as `127.0.0.1`, never by name, so `/etc/hosts` can't point it elsewhere), `connected(fd, timeout)`, `read(fd, buffer, timeout)`, `write(fd, buffer, timeout)` (non-blocking) and `close(fd)`. Connects, reads and writes wait at most 100 ms at a time, so signals, timeouts and a reset that arrives while a long prompt uploads get through. Without the module, slicc exits 1 with `this runtime has no slicc_host module`. On a kernel older than 1.92.0, which has no `ctx.net`, `connect` answers `ENOSYS`, so slicc exits 1 with `can't reach seven's link facade`.

**The package.** `@ai-ecoverse/wasi-slicc` holds `bin/slicc.wasm`, `host/slicc-host.mjs`, this section as its README, and the LICENSE. Its `package.json` has `"slicc": { "abi": "wasi", "commands": { "slicc": { "wasm": "bin/slicc.wasm", "imports": "host/slicc-host.mjs" } } }` and `engines.slicc-kernel`. The release attaches `slicc.wasm` as `slicc-wasip1-wasm.wasm`, in `SHA256SUMS` and its attestation (see [Releases](#releases)).

**Tests.** `test/integration/wasi_test.go` builds `slicc.wasm`, stages the package with `npm-stage.mjs --wasi-only`, and runs it in `@ai-ecoverse/slicc-kernel`'s Node kernel (an exact-pinned devDependency, so `npm ci` first) against a fake facade, a kernel JS program in `testdata/wasi/fakefacade/`. One case writes `/etc/hosts` as root to map `localhost` to `203.0.113.5` and connects to `tcp://localhost:<port>`; `testdata/wasi/hostmodule.mjs` also loads the staged `host/slicc-host.mjs` with a `ctx.net` that records the host it is asked for. `wasi_realfacade_test.go` runs it against the kernel's own link facade (`createNodeKernel({ facades: { link } })`), served by a test page hook, `testdata/wasi/page.mjs`, that implements [§5.16](#516-the-link-facade-page-side-not-on-the-wire-the-in-kernel-slicc): it checks the stamp the hook receives (root and a cone in `sudo` pass, a scoop gets `EACCES`) and that the kernel sets `SLICC_LINK_URL`. `wasi_tray_test.go` runs `slicc tray` against both, and checks that its openings carry no join URL. `cmd/slicc` builds for both: a file there that needs a native-only package carries `//go:build !wasip1`, and `wasi_test.go` fails when the `wasip1` build breaks or links one of the left-out packages.

## Tray hub signaling (remote setup)

A node on another machine sets up its link through the tray hub, the `slicc-tray-hub` worker on `www.sliccy.ai`. seven is the tray leader and the node is a follower. The wire contract is six's `packages/shared-ts/src/tray-signaling.ts`. The node only signals over it and never speaks six's tray-sync messages.

**Join URL.** `https://<host>/join/<token>`. Plain `http` is accepted only for loopback hosts. The join URL is the secret: the node never prints it, its token, or the token's secret part, in output, logs or errors. The one exception is `slicc tray url` inside seven, which prints the leader's own join URL when asked to (see [Inside seven](#inside-seven)).

**Attach.** `POST <join-url>` with `{ "controllerId", "runtime": "slicc-link/1" }`. The runtime tag lets seven tell link nodes from six's followers; the hub passes it on in `follower.join_requested`, and the node sends it again on `retry`. `wait` is retried after `retryAfterMs` (clamped to 50 ms–30 s) with the same controller id. `signal` starts a bootstrap and carries the TURN and STUN `iceServers` for the peer connection. `fail` (`INVALID_JOIN_CAPABILITY`, `TRAY_EXPIRED`) is terminal.

**Supersede.** The node does not follow HTTP redirects itself. A superseded tray's replacement is read from the `Link` header (`rel="successor-version"`), then a 3xx `Location` (with `json` dropped from its query), then the body's `result.joinUrl` when `code` is `TRAY_SUPERSEDED`. The 308 `redirect` and the older 409 `fail` shapes are both accepted. A replacement must itself be a valid join URL. Each hop uses a fresh controller id, and a dial follows at most five hops before it fails with `TRAY_SUPERSEDED_CHAIN_EXHAUSTED`. A supersede seen during a bootstrap ends it with `TRAY_SUPERSEDED`, and the next dial starts at the replacement.

**Bootstrap.** The node polls `{ "action": "poll", "controllerId", "bootstrapId", "cursor" }` once a second, and takes the leader's `bootstrap.offer` and `bootstrap.ice_candidate` events in order. It posts `answer`, then its own `ice-candidate`s. Candidates gathered before the answer is posted are held and sent right after it. A `bootstrap.failed` that is `retryable`, with `retriesRemaining` above 0, is retried with `{ "action": "retry" }` after its `retryAfterMs`. That mints a new bootstrap, so the peer connection starts over. Any other failure ends the bootstrap. When no offer arrives within 30 s, the bootstrap ends with `OFFER_TIMEOUT`.

**Rotation.** When seven creates a new join URL it doesn't supersede the old tray, so a follower learns the new URL from seven over its link. The node accepts it only on the hub origin it currently follows (same scheme, host and port), and only as a valid join URL. It stores the URL as current for the join URL it was given, keeps the running link, and dials the new URL on its next reconnect. It never prints the URL.

**Requests.** Each HTTP attempt has a 30 s timeout. A logical request gets 8 s and up to four attempts. Network errors, HTTP 5xx and non-JSON bodies are retried with a backoff of 1 s, 2 s, then 4 s. JSON refusals below 500 are not retried.

**Join URL state.** The node remembers the current join URL for each join URL it was given, so it reconnects after a network change or restart without walking the supersede chain again. The state lives in `<config>/slicc/tray-join.json`, where `<config>` is `~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux, and `%AppData%` on Windows. The directory is mode `0700` and the file `0600`, written atomically. Entries are keyed by the SHA-256 of the given join URL, and at most 16 are kept.

## Link protocol

The wire protocol between seven (the page) and `slicc` (the node), v1. seven's side is the `link` exit in [slicc-bios](https://github.com/ai-ecoverse/slicc-bios), and both sides must stay in step with this section, the same way [slicc-node's README](https://github.com/ai-ecoverse/slicc-node#protocol) holds the local-proxy protocol.

**Decisions:**
- **WebRTC is the only link layer, local and remote.** CDP only carries the setup messages for a local link ([§1.1](#11-local-setup-over-cdp)). For a remote link, the tray hub carries them, using six's normal join and follow URL ([§1.2](#12-remote-setup-over-the-tray-hub)).
- **No Tailscale in the CLI.** It would need an account, so it feels less owned. The rendezvous and TURN are sliccy.ai's own.
- **Go** for the CLI, in `ai-ecoverse/slicc-cli`. [`go-optel`](https://github.com/ai-ecoverse/go-optel) gets its own repo.
- **No six compatibility.** The CLI doesn't speak six's tray-sync messages inside the data channel. It reuses only the tray hub's signaling.
- **Compute goes over SSH** on the link ([§5.5](#55-ssh-cap-ssh-page--node)), not a hand-rolled exec protocol. The kernel has an `ssh` client.

### 1. Peers and setup

- **page**: seven. It owns the `RTCPeerConnection` and always sends the **offer**.
- **node**: the `slicc` binary (pion). It always sends the **answer**.
- A **link** is one peer connection. A page can hold several links, one exit each.

#### 1.1 Local setup over CDP

Used when the node already holds a CDP connection to the browser that runs seven. No key in a URL, and nothing listening on loopback.

1. The node finds the seven target. That's a page target whose main-frame origin is allowed, using the same origin rule as slicc-node's gate: `https://<label>.sliccy.ai` other than the service hosts in slicc-browsers' `seven.excludedLabels`, plus any `--origin`.
2. It attaches (`flatten: true`), calls `Inspector.enable`, `Runtime.addBinding { name: "sliccLinkSend" }` and `Runtime.enable`.
3. It sends `{ "t": "hello", "v": 1, "nonce": "<base64url 16 bytes>", "node": "<random per-process id>" }` (see [§5.11](#511-agent-page-cap-agent-node--page-remote-control) for how the page uses `node`) by calling `Runtime.evaluate` on `globalThis.sliccLinkReceive?.(<json>)`.
4. The page answers through the binding, `sliccLinkSend(<json>)`, with:
   - `{ "t": "offer", "nonce", "sdp" }`
   - `{ "t": "candidate", "nonce", "candidate" }` (trickled; `null` at the end)
5. The node sends the following through `sliccLinkReceive`:
   - `{ "t": "answer", "nonce", "sdp" }`
   - its own `candidate` messages
6. Either side can send `{ "t": "bye", "nonce", "reason" }`.

Rules:
- **The node checks where a binding call came from.** It accepts one only when `Runtime.bindingCalled.executionContextId` is the main frame's default context and that context's origin is allowed (from `Runtime.executionContextCreated`). It ignores calls from iframes, workers, other origins, or with a stale `nonce`.
- **Hello delivery** is retried until both `globalThis.sliccLinkReceive` and the `sliccLinkSend` binding exist in the main frame's context. The node calls `addBinding` again when the binding is missing (seen on macOS around the about:blank → page swap). The page should also check that `sliccLinkSend` exists before calling it.
- **On exit** the node sends `bye`.
- **Navigating or reloading seven drops the link.** The node runs the setup again when a new allowed context appears.
- **A link that fails on its own is set up again in the same tab.** When the link ends with an error while the context is still current (ICE failed, no HELLO, a protocol violation, an offer it couldn't answer), the node sends that context a fresh `hello` with a new nonce, after 1 s, doubling to 30 s until a link comes up with the current nonce, which also cancels a pending `hello`. A link the page closes itself (its data channels close) isn't set up again.
- **A crashed seven tab is reloaded.** On `Target.targetCrashed` or `Inspector.targetCrashed` the node drops the link and reloads the tab with `Page.reload`, at most 3 times in 5 minutes, then runs the setup again once the tab is back. The page gets a fresh `hello` as after any reload.
- **ICE:** host candidates only. There's no STUN or TURN, since both peers are on the same machine.
  - Chrome hides host candidates behind mDNS (`<uuid>.local`), so the node resolves them (pion `MulticastDNSMode` query-only).
- **Candidates:** a `candidate` message carries `RTCIceCandidateInit` JSON (`candidate.toJSON()`: `candidate`, `sdpMid`, `sdpMLineIndex`, `usernameFragment`), and `null` once gathering ends.
- **Local Network Access:** Chrome 154 stable doesn't gate WebRTC with it yet. The check exists behind `--enable-features=LocalNetworkAccessChecksWebRTC` and will ship.
  - So before setup, the node grants the permission for the seven origin over CDP, best effort: `Browser.setPermission` with `local-network-access`, `local-network` and `loopback-network` set to `granted`. Errors are ignored on Chromes without them.
  - `Browser.grantPermissions` would replace the origin's other grants, so it isn't used.
  - Measured on Chrome 154 and macOS: the link comes up in 0.3–0.9 s. Loopback throughput is about 40–45 MB/s Chrome→pion and about 70–80 MB/s pion↔pion; the CPU limits it, not the window.
- **No data fallback.** CDP carries only the setup, never link data.
- **HELLO timeout:** each side closes the link if the peer's HELLO hasn't arrived 10 s after the control channel opens.
- **`--signal stdio`** (tests): the same messages as NDJSON on stdin and stdout. Each `offer` starts a new link and replaces the current one. A harness may write `{"t":"hello"}` to get a fresh `hello` and nonce, for example after it reloaded the page.

#### 1.2 Remote setup over the tray hub

The scenario: `slicc` on a laptop launches Chrome there. seven runs on a desktop, in a browser no CDP client controls. After the CLI follows the desktop's tray, seven drives the laptop's Chrome and uses the laptop's network.

- **Rendezvous: the tray hub.** seven is a **tray leader** on the existing tray hub (six's `slicc-tray-hub` worker), and every linked CLI is a **follower**.
  - The wire contract is [`packages/shared-ts/src/tray-signaling.ts`](https://github.com/ai-ecoverse/slicc/blob/main/packages/shared-ts/src/tray-signaling.ts). The leader creates the tray with `POST /tray` (joinToken + controllerToken) and handles the bootstrap over its control WebSocket.
  - Followers attach at `/join/<token>` and run the bootstrap poll, answer and candidate exchange.
  - TURN credentials come from the hub. When a credentialed server lists `turn.cloudflare.com` and does not already include `turns:turn.cloudflare.com:443?transport=tcp`, the node adds that URL with the same username and credential. TLS certificate verification stays on. Networks that block UDP, and TURN TLS on 5349, can still allocate a relay on 443; when a direct or other relay candidate works, ICE keeps using it. The hub sees only SDP and candidates.
- **Pairing:** seven's Network panel → *Link a device* shows the normal join URL and the command, `npx sliccy <join-url> follow`, in six's grammar. One join URL serves any number of devices, as in six.
- **The join URL is the secret**, long-lived like six's. Rotating the tray replaces it.
  - **Supersede:** the deployed hub answers with HTTP `308`, a `Location` header, `Link: <…>; rel="successor-version"` and `result.action: "redirect"`. The CLI never lets its HTTP client follow it. It reads `Link`, then `Location`, then the body, accepts the legacy `409 TRAY_SUPERSEDED` shape too, and follows at most five hops per dial.
  - **State:** the CLI keeps the current URL in `<config>/slicc/tray-join.json` (mode `0600`). Entries are keyed by the SHA-256 of the URL the user gave, so that URL itself isn't stored. That way `follow` reconnects after a network change or restart.
- **Runtime tag:** link followers attach with `runtime: "slicc-link/1"`, so the leader can tell them from six's followers (`slicc-cli`, iOS).
- **Identity is the DTLS certificate.** Each CLI install keeps one persistent DTLS certificate per hub origin in `<config>/slicc/` (mode `0600`). It survives restarts and updates.
  - The page identifies a device by the certificate's SHA-256 fingerprint from the follower's answer SDP (lower-case hex, no colons). The Network panel, the default-exit choice (`link:<fp>`) and the unlink list are all keyed on it.
  - A reconnect under a new `controllerId` replaces the old link and keeps its address.
  - An answer without a SHA-256 fingerprint gets `bootstrap.failed` `NO_FINGERPRINT`.
  - A new user account, machine or config dir is a new device.
- **`controllerId` is only a per-dial routing id.** The CLI mints a fresh one for every dial, as six does. A stable one would make the hub hand back a stale `connected` bootstrap on a quick reconnect (`BootstrapCoordinator.ensure()`).
- **Unlinked devices:** when an answer carries a fingerprint the leader has unlinked, the leader fails that bootstrap with `bootstrap.failed { code: "UNLINKED", retryable: false }` ([§5.7](#57-unlink-cap-join-or-unlink-loopback-on-13-links-page--node)), then closes the peer connection.
  - The hub allows a failure even after `connected`, so the follower keeps polling the bootstrap after posting its answer, at least until HELLO arrives. Over the tray the page drops its `null` end-of-candidates, which pion doesn't need.

#### 1.3 Local setup without CDP: loopback signalling

The scenario: seven is open in the user's **everyday browser** (Chrome, Edge or Firefox; no CDP, no remote debugging), on the same machine as `slicc`, as slicc-node allowed. Only the signalling and trust setup are new. Once the peer connection is up, the link is [§2](#2-channels)–[§5](#5-stream-kinds) unchanged.

**Pairing and trust**
- **What anchors trust:** two pinned certificates.
  - **The node's** persistent DTLS certificate. It gets a loopback identity of its own in `<config>/slicc/` (mode `0600`), as for the tray hub ([§1.2](#12-remote-setup-over-the-tray-hub)).
  - **The page's** persistent `RTCCertificate`: seven generates it once per origin with `RTCPeerConnection.generateCertificate`, keeps it in IndexedDB, and uses it for every loopback link.
  - Each side pins the other's SHA-256 fingerprint at pairing. After that, the node answers only offers whose SDP fingerprint it has paired, and the page accepts only an answer whose fingerprint it pinned. This replaces [§1.1](#11-local-setup-over-cdp)'s CDP binding and per-context nonce.
- **Pairing URL:** `slicc open` (or `slicc --default-browser`) opens `https://seven.sliccy.ai/#slicc-pair=<port>.<token>.<node-fp>` in the default browser. `slicc pair` only prints it, for a seven that's already open (the user pastes it into seven's address bar, or seven's *Link this computer* field).
  - **The token:** 128 random bits, base64url, one-time, and valid for 10 minutes.
  - **The URL is the secret**, as slicc-node's fragment was. It carries the node's fingerprint, so a process squatting the port can't pass as the node.
  - **On the page:** seven reads and strips the fragment at once (`history.replaceState`), then shows a tray confirmation: "Link **<hostname>** (`ab12…ef`)? This computer's network, folders and kernel services become available to seven." [Link] [Cancel]. slicc prints the same hostname and short fingerprint. Only [Link] (a user gesture) starts the first loopback request, which also satisfies Chrome's Local Network Access prompt (below).
- **Pairing exchange** (one request): `POST http://127.0.0.1:<port>/slicc/v1/pair` with `{ "token", "offer": "<sdp>" }`. The offer is non-trickled: the page waits for ICE gathering to complete. The node checks the token, burns it, and pins the offer's fingerprint together with the request's `Origin`. It answers `{ "answer": "<sdp>", "node": "<per-process id>", "name": "<hostname>" }`, and the page checks that the answer's fingerprint equals `<node-fp>` from the URL.
- **Reconnect** (no token): `POST /slicc/v1/signal` with `{ "offer" }`. The node answers only for a paired page fingerprint whose pinned origin equals the request's `Origin`; otherwise it returns `403`. The page checks the answer against its pin. There's no other endpoint.
- **Unpairing:** seven's Network panel *Unlink* forgets the pin and, if it's linked, first opens an **`unlink` stream** ([§5.7](#57-unlink-cap-join-or-unlink-loopback-on-13-links-page--node)) on the live link. A loopback node advertises cap `unlink-loopback` for this. The node answers OPENED `{}` then END, deletes that page's pin, and closes that link; unlike [§5.7](#57-unlink-cap-join-or-unlink-loopback-on-13-links-page--node) it keeps running and keeps its certificate. A page that unlinks while not linked just forgets its pin, and the node's stale pin is harmless (the page won't signal again) until `slicc unpair`. `slicc unpair [--all]` removes pins on the node. Pins live in `<config>/slicc/loopback-peers.json` (mode `0600`), keyed by page fingerprint, with origin and first-seen time.

**The listener**
- **Address:** `127.0.0.1` only (and `[::1]`), never `0.0.0.0`. Port **17117** (slicc-node's), or the first free port in 17117–17126. The pairing URL carries the actual port; on reconnect the page tries the remembered port, then the range.
- **Gate, on every request:**
  - `Host` must be `127.0.0.1:<port>`, `[::1]:<port>` or `localhost:<port>` (against DNS rebinding);
  - the method and the two paths are exact;
  - `Origin` must pass exactly the same rule as CDP setup ([§1.1](#11-local-setup-over-cdp)): slicc-cli's `IsSevenOrigin` with slicc-browsers' catalog `ExcludedLabels` (so `vault`, `mcpcat`, `get` and every other service label never pair or signal; `www` does, since seven runs there for six's in-place upgrade), plus `--origin`;
  - the body is JSON, at most 64 KiB;
  - **preflight:** `OPTIONS` answers only for allowed origins, with `Access-Control-Allow-Private-Network: true`, `Access-Control-Allow-Origin: <that origin>` (never `*`), no credentials, and `Vary: Origin`.
  - Everything else gets `403` with no body. The listener isn't a proxy and serves nothing else. `Origin` can be forged by non-browser processes, but the gate only filters browsers; trust is the token or the pinned fingerprint.
- **Rate limit:** at most 10 pairing attempts a minute; a wrong token burns nothing and counts.

**Local Network Access**
- Chrome's LNA prompt appears on seven's first request to `127.0.0.1`. seven only makes loopback requests after a pairing [Link] click, or on load when it has a pin, so users who never pair are never prompted.
- If the permission is denied, the page says how to allow it in site settings.
- **Mixed content:** `http://127.0.0.1` is potentially trustworthy, so Chrome, Edge and Firefox allow it from https pages; bios CI tests Chrome and Firefox. WebKit doesn't. From an https page, `fetch` to `http://127.0.0.1` and `http://localhost` and a `ws://127.0.0.1` socket fail, and nothing reaches slicc, so this setup doesn't work there. Measured in Mobile Safari 27.0 on the iOS Simulator, where an `http` page reached the same listener, in a `WKWebView` app, where App Transport Security exceptions don't change it, and in Playwright's WebKit. Safari on macOS hasn't been measured. In such a browser the page says it can't pair there and offers the tray path ([§1.2](#12-remote-setup-over-the-tray-hub)), or Chrome, Edge or Firefox. slicc can't tell which browser a URL went to, so it [says the same](#linking-seven-in-your-everyday-browser) when nothing reaches it.
- **Why the listener stays `http`:** `https://127.0.0.1` needs a certificate the browser trusts, and the node has only a self-signed one. Installing a root in the user's trust store is out of the question, and a certificate for a public name that resolves to loopback would put its private key in every copy of slicc. A `WKWebView` app can accept one server's certificate in its navigation delegate, but only for that app's own server: it has no way to learn a certificate from a pairing URL, and teaching it would let a page decide what its browser trusts.

**The link in this mode**
- **ICE:** host candidates on loopback only, as in [§1.1](#11-local-setup-over-cdp).
- **Caps:** `net`, `http`, `ws`, `udp`, `credentialed`, `fs` (with `--mount`), `kernel-in` services ([§5.9](#59-kernel-listener-on-the-node-local-only), listener on `--kernel-port`), `ssh` with `--runner`, `jerk`/`jerk-seal`, and `agent`. **No `cdp`:** the node controls no browser.
- **Policy:** the local default ([§6.4](#64-local-default-slicc-the-bridge-to-the-browser-it-controls)), with the debugging ports of [§6.5](#65-debugging-ports-every-mode) (no served browser here, so 9222–9229, every `DevToolsActivePort` port and every running browser's `--remote-debugging-port`) and the node's own listeners denied. A kernel program can't reach a debugging port the user enabled.
- **PRE-1** ([§5.8](#58-cdp-cap-cdp-page--node)) protects seven's origin from seven's own browser control. In this mode there's none, so PRE-1 doesn't apply. The `http` cap is the node's own client and carries no browser cookies or storage, so fetching a `*.sliccy.ai` URL through it exposes nothing of the user's session.
- **Restarts:** the node keeps its certificate and port, and the page keeps its certificate and pin. On link loss the page retries `/signal` with backoff (1 s up to 30 s, and on `online` or `visibilitychange`). A restarted `slicc` is reachable again with no new pairing. The `node` id changes, and the page keys the device on the fingerprint, so the name stays.
- **Several sevens:** the node holds one link per paired page fingerprint, and each is an exit on its own.

**Entry points** (automatic fallback):
- **Plain `slicc` on a terminal** offers this mode as the picker's `Default browser` row, first and the best match when there is no SLICC profile and no CDP endpoint (none launched, none found, none given).
- **Plain `slicc` without a terminal**, or with `--yes`, `--plain` or `--json`, falls back to this mode by itself when there is no SLICC profile and no CDP endpoint: it prints the pairing URL (and opens it in the default browser with `--open`, or when on a terminal) and links over loopback.
  - A status line on stderr (with `--json` also an event `{"event":"mode","mode":"loopback","reason":"no-browser"}`, then `{"event":"pairing","url"}` for each pairing URL, and the same `linked` and `closed` events as other modes) says: "No SLICC browser or debugging endpoint found, so seven opens in your default browser for pairing. Use --profile or --cdp to choose."
- **Picking `Default browser`** runs this mode in the dashboard (see [The default](#the-default-slicc)): the pairing URL, a new one with Enter, the link state and the capabilities.
- **`--no-fallback`** keeps the pre-fallback behaviour, for scripts: launch an installed Chromium into a new SLICC profile when one exists, and exit with "no browser to control" only when nothing can host seven. Without it, non-interactive runs (no TTY) still fall back, but print the pairing URL instead of opening a browser unless `--open` is given. With it, nothing falls back, TTY or not.
- **`slicc open`, `slicc --default-browser` and `slicc pair`** stay as the explicit forms.
- **A found or given profile or CDP endpoint always wins,** SLICC six's running Chrome included; the fallback only triggers when both are absent.

#### 1.4 A seven as follower: leader to leader

One seven (A) follows another seven's (B) tray, so that a program in A's kernel, the in-kernel `slicc` ([§5.16](#516-the-link-facade-page-side-not-on-the-wire-the-in-kernel-slicc)), can prompt, watch and abort B's agent. A's page plays the node's part of [§1.2](#12-remote-setup-over-the-tray-hub) and of the link; B needs no change.

- **Signalling:** A's page joins B's tray at `/join/<token>` as a follower, exactly as the CLI does ([§1.2](#12-remote-setup-over-the-tray-hub)): bootstrap poll, answer, candidates, TURN from the hub.
  - **The runtime tag:** `slicc-link/1`.
  - **The answer:** A's `RTCPeerConnection` answers B's offer.
- **Identity:** one persistent `RTCCertificate` per (A's origin, hub origin), kept in IndexedDB and passed to every `RTCPeerConnection` A opens toward that hub.
  - **How B sees A:** B identifies and gates A by its fingerprint, as any device.
  - **On `unlink` from B ([§5.7](#57-unlink-cap-join-or-unlink-loopback-on-13-links-page--node)):** A deletes that certificate and its follower state for the hub.
- **Follower state:** keyed by the SHA-256 of the join URL the program gave (as the CLI's `tray-join.json`). It follows hub supersedes ([§1.2](#12-remote-setup-over-the-tray-hub)). The URL itself is never stored in the clear and never logged.
- **HELLO:** A sends `caps: []` (control-only, [§5.11](#511-agent-page-cap-agent-node--page-remote-control)), a per-link `node` id, and its name: the seven's own name, or its origin's label.
- **Streams:** A opens only `agent` streams ([§5.11](#511-agent-page-cap-agent-node--page-remote-control)), and refuses every stream B opens with `RESET EACCES`.
- **Lifetime:** one link per join URL, opened on demand, kept while a facade stream uses it, and closed 10 minutes after the last one ends.
- **Only under kernel users `enforce`:** A's page follows trays and serves the facade only when its kernel runs users `enforce`. Under `off` or `report`, every process is effectively root.

### 2. Channels

- **Control channel:** label `slicc.link.v1`. The page creates it, ordered and reliable. Each side's **first message on it is HELLO** (text, JSON, [§4](#4-hello)). Every later message on any channel is a binary frame ([§3](#3-frames)). A text message after HELLO closes the link.
- **Stream channels:** label `slicc.link.v1.stream`, ordered and reliable. A stream's opener may create a new channel for it. That channel then carries exactly that stream and is closed once the stream has ended in both directions or been reset.
- **Shared streams:** an opener may instead put a stream on the control channel, for short streams like `resolve` or once it has 256 stream channels open. Chrome caps SCTP stream ids at 1023, so about 512 per side.
- **A stream lives on the channel its OPEN arrived on.** Receivers must handle both layouts. Closing a stream channel resets any stream that hasn't ended.
  - A second OPEN on a stream channel closes the link.
  - Frames for a stream that arrive on any other channel are ignored.
- **Message size:** at most 65 541 bytes (5-byte header plus 65 536). That's below what Chrome and pion advertise for `max-message-size`.
- **Keep reading.** A receiver reads every channel all the time and buffers up to the stream's window ([§3.3](#33-flow-control)). It never applies backpressure by not reading. SCTP has one receive window per association, so one unread channel would stall every channel. The node raises pion's SCTP receive buffer to 4 MiB.

### 3. Frames

slicc-node's kernel-tunnel framing, unchanged on the wire:

| byte 0 | bytes 1–4 | rest |
|---|---|---|
| type (`u8`) | stream id (`u32` BE) | payload |

| type | name | payload |
|---|---|---|
| 1 | OPEN | UTF-8 JSON `{ "kind", … }` ([§5](#5-stream-kinds)) |
| 2 | OPENED | UTF-8 JSON, `{}` when there is nothing to say |
| 3 | DATA | 1 to 65 536 bytes |
| 4 | END | empty: the sender half-closes (`SHUT_WR`) |
| 5 | RESET | UTF-8 `ERRNO` or `ERRNO: message`, aborts both directions |
| 6 | CREDIT | `u32` BE bytes consumed, at least 1 |

#### 3.1 Stream ids
Page-opened streams are odd and node-opened streams even, never 0, and never reused on a link.

#### 3.2 Lifecycle
- The receiver of OPEN answers OPENED or RESET. DATA and END flow only after OPENED, in both directions.
- A stream is over after END in both directions, or a RESET from either side.
- Frames for an unknown id are ignored, because they may cross a RESET.

#### 3.3 Flow control
- **Window:** at most 256 KiB of DATA per stream and direction that hasn't been credited.
- **Credit:** the receiver credits bytes once it has handed them on (a socket write returned, `writer.write()` resolved).
- **Stopping:** a sender that's out of window stops reading its source.

#### 3.4 Violations
- **Reset the stream with `EPROTO`:**
  - DATA or END before OPENED, or after the peer's END;
  - empty DATA;
  - DATA past the window, CREDIT of 0, or CREDIT past what was sent;
  - an OPEN whose payload isn't JSON with a `kind`.
- **Close the link:** an unknown type, a short or malformed frame, a duplicate id, or an id with the wrong parity.

### 4. HELLO

```json
{
  "v": 1,
  "role": "node",
  "name": "slicc on laptop",
  "host": "laptop",
  "version": "1.4.0",
  "mode": "local",
  "caps": ["net", "http"],
  "routes": { "prefixes": [], "exit": true },
  "policy": { "allow": [], "deny": ["127.0.0.1/32:9222"], "summary": "every public address, this machine, and the local network" },
  "kernel": { "port": 80 }
}
```

- **`role`:** `page` or `node`.
- **`name`:** for display. **`host`** (node only): the machine's short hostname, lower-cased, as a hint for the DNS label. The page makes it DNS-safe and unique per link (`laptop-2`) and serves it as `<host>.slicc.internal` on a synthetic address in `198.18.57.0/24`. `--name` overrides both.
- **`mode`:** `local` (CDP setup) or `remote` (hub).
- **`caps`:** what this side accepts OPENs for.
  - node: `net` (`tcp` and `resolve`), `http`, `ws` ([§5.15](#515-ws-cap-ws-page--node-websockets-for-kernel-programs)), `udp` ([§5.12](#512-udp-cap-udp-page--node)), `ssh`, `cdp`, `fs`, `join`, `jerk` and `jerk-seal` ([§5.14](#514-jerk-caps-jerk-and-jerk-seal-page--node-j1)).
  - node, without a kind: `credentialed` ([§5.13](#513-credentialed-node-cap-http-streams-that-carry-a-value)), a promise about how the node handles `http` streams.
  - page: `kernel-in` ([§5.4](#54-kernel-page-cap-kernel-in-node--page)) and `jerk-revoke` ([§5.14](#514-jerk-caps-jerk-and-jerk-seal-page--node-j1)).
- **`routes`** (node only): the prefixes it offers as specific routes, plus `exit: true` when it's willing to be a default exit.
- **`policy`:** for display only; the node enforces it ([§6](#6-policy-node)).
- **`kernel`** (local node only, with kernel services): the port of the node's kernel listener ([§5.9](#59-kernel-listener-on-the-node-local-only)).
- **Versions:** each side ignores fields it doesn't know. If `v` differs, the side with the lower `v` decides, or closes the link when it can't.
- **Before HELLO:** neither side sends OPEN until it has received the peer's HELLO.
- **Unsupported kinds:** OPEN for a kind the receiver didn't advertise gets RESET `EOPNOTSUPP`.

### 5. Stream kinds

#### 5.1 `tcp` (cap `net`), page → node
- **OPEN:** `{ "kind": "tcp", "host": "example.com", "port": 443 }`. `host` is an IP literal or a name, and a name is resolved by the node with the system resolver.
- **OPENED:** `{ "localAddr": "192.168.1.20:53122", "remoteAddr": "93.184.215.14:443" }` once `connect()` succeeded.
- **RESET codes:**
  - `ECONNREFUSED`, `ENETUNREACH`, `EHOSTUNREACH`, `ETIMEDOUT` (connect timeout 30 s), `ECONNRESET`;
  - `EACCES` for a policy refusal;
  - `ENOTFOUND` when a name doesn't resolve (the page maps it to whatever the kernel uplink wants).
- **During the stream:** END maps to `closeWrite()` on both sides, and a socket error mid-stream is `RESET ECONNRESET`.

#### 5.2 `resolve` (cap `net`), page → node
- **OPEN:** `{ "kind": "resolve", "name": "example.com", "family": 0 }`, where `family` is `4`, `6` or `0` for both.
- **OPENED:** `{ "addresses": ["93.184.215.14", "2606:2800:21f:cb07:6820:80da:af6b:8b2c"], "ttl": 30 }`, immediately followed by END from the node. The page then sends END too.
- **Filtering:** addresses the node's policy would refuse are dropped before answering.
- **Empty list:** `addresses: []` means "not mine", whether the name doesn't exist or every address was filtered, so the page can try another exit.
- **TTL:** Go's resolver doesn't report TTLs, so v1 always sends `30`.
- **Details:**
  - IP literals come back as themselves when allowed, and `::ffff:a.b.c.d` as `a.b.c.d`.
  - Results are deduplicated and filtered to the requested family.
  - A lookup **timeout** is `RESET ETIMEDOUT`, so the page doesn't cache a transient failure as "not mine".
  - Other failures (e.g. SERVFAIL) are `[]`.
  - A missing name or a bad family is `RESET EPROTO`.
  - **Loopback answers:** a `resolve` answer with a loopback address is useless to the page. The kernel never sends loopback out through an exit, so it would dial its own loopback. Test names in `--hosts` should map to a non-loopback address of the node's machine. For `http` by name, the node resolves it itself, so loopback works there.

#### 5.3 `http` (cap `http`), page → node
- **OPEN:** `{ "kind": "http" }`. The node answers OPENED `{}` at once.
- **Bytes page → node:** slicc-node's raw-fetch request, a `u32` BE length, the JSON head `{ "url", "method", "headers" }`, then the body, then END.
- **Bytes node → page:** a `u32` BE length, the JSON head `{ "status", "statusText", "headers", "url" }`, then the decoded body, then END.
- **Same rules as slicc-node's `fetch-proxy`:**
  - the header drop and fold rules, and `Accept-Encoding`;
  - redirects not followed;
  - decoding, and the `206`-with-a-decoded-coding refusal, which becomes `RESET EPROTO`.
- **Differences from slicc-node:**
  - The request body streams under the window, so there's no 256 MiB buffer.
  - Errors before the response head are RESETs: `ECONNREFUSED: fetch failed: …`, `EACCES` for policy, `EPROTO` for a malformed head.
- **Policy and TLS:** the node's HTTP client dials through the same policy-checked dialer as `tcp` and verifies TLS with the system roots.
- **Details** ([slicc-cli#21](https://github.com/ai-ecoverse/slicc-cli/pull/21)):
  - Header bytes are Latin-1 both ways, as fetch's ByteString is.
  - The request head is at most 1 MiB.
  - A body that ends within 1 MiB is sent with its exact `Content-Length`. A longer one uses the page head's `Content-Length` when that is one valid number, and is otherwise sent chunked.
  - Methods are upper-cased the way fetch does it.
  - `CONNECT`, `TRACE`, `TRACK`, URLs with credentials, and header values past U+00FF or with control characters are `RESET EPROTO`.
  - No default `User-Agent` is added.
  - A TLS failure is `ECONNREFUSED: fetch failed: x509…`.
  - An upstream error mid-body, or a broken coding, is `RESET ECONNRESET`.
- **Credentialed streams:** `{ "kind": "http", "credentialed": true }`, only to nodes with the `credentialed` cap ([§5.13](#513-credentialed-node-cap-http-streams-that-carry-a-value)).

#### 5.4 `kernel` (page cap `kernel-in`), node → page
- **OPEN:** `{ "kind": "kernel", "port": 5173 }`. This is how the node carries `<port>.kernel.localhost` navigations and WebSockets into the page, as slicc-node's kernel tunnel does today.
- **Answer:** the page calls `kernel.dial({ port })` and answers OPENED or `RESET ECONNREFUSED`.
- **The page refuses port 9222** (the CDP facade) with `EACCES`, and so does any port the kernel reserves.
- **Nothing else comes in.** The page refuses every other node-opened kind with `EOPNOTSUPP`.

#### 5.5 `ssh` (cap `ssh`), page → node
- **OPEN:** `{ "kind": "ssh" }`. The stream is a raw byte stream into the node's embedded SSH server (`golang.org/x/crypto/ssh`). That server never listens on a real port.
- **Addressing** (page router): each link gets `<host>.slicc.internal` and a synthetic address in `198.18.57.0/24`. A kernel connect to port 22 there becomes this stream kind. Any other port becomes `OPEN { kind: 'tcp', host: '127.0.0.1', port }` under the node's policy. So `ssh laptop.slicc.internal` and `git` over ssh work unchanged.
- **Auth:** the link is the trust boundary, so the server accepts `none` authentication on link streams, and ignores the user name (`$USER` in the kernel). Commands run as the user who started `slicc`.
- **Host key:** one ed25519 key per node, kept `0600` in its config directory so it survives restarts. HELLO carries it as `"sshHostKey": "ssh-ed25519 AAAA…"`. The page writes `<host>.slicc.internal <key>` into the kernel's **system-wide `/etc/ssh/ssh_known_hosts`**, because seven manages those keys and wasix-openssh's per-user file sits under the passwd home, not `$HOME`. So `ssh` never has to trust a first-seen key.
- **Client limits** (wasix-openssh 10.6.0-1): no ControlMaster, so each `ssh` is its own stream. Interactive `^C` may stop the local client until [slicc-kernel#247](https://github.com/ai-ecoverse/slicc-kernel/issues/247); exec mode is unaffected.
- **What a session can do:**
  - `exec` and `shell` run through the node's runner, the first `--runner` argv (for example `bash -c`) as a forced command, with PTYs: `creack/pty`, ConPTY on Windows.
  - The `sftp` subsystem serves files.
  - `direct-tcpip` forwarding is checked against the same policy as `tcp`.
  - The persistent REPL from six's `follow --eval` is the `slicc-eval` subsystem: one REPL per node, started on first use and shared by every session. Each eval ends after a quiet window (default 500 ms). Its framing is in [SSH server](#ssh-server).
  - `sftp` is on by default only when the runner is a plain local shell (`bash`, `sh`, `zsh`, `fish`, `pwsh`, `powershell`, `cmd`). Otherwise it is off, because sftp serves the host's files as the user and would get around a sandboxing runner such as `docker exec …`. `--sftp` / `--no-sftp` override.
  - A refused `direct-tcpip` is answered `administratively prohibited`, with the policy message.
- **Off by default:** a node without `--runner` doesn't advertise `ssh`, and refuses the OPEN with `EOPNOTSUPP`. If a session reaches the server anyway, it is refused the same way as a direct-tcpip.

#### 5.6 `join` (cap `join`), page → node, remote links only
- **Why:** "New join URL" in seven creates a fresh tray and does **not** supersede the old one, so a leaked URL stops working. Devices linked at that moment learn the new URL over the link.
- **OPEN:** `{ "kind": "join", "url": "https://www.sliccy.ai/join/<token>" }`, on the control channel.
- **The node:**
  - It accepts only a join URL on the **same hub origin** as the one it followed. Anything else gets `RESET EACCES`.
  - It stores the URL as its current one (`tray-join.json`, mode `0600`), replacing the old one.
  - It answers OPENED `{}`, then END.
  - It keeps the current link, and uses the new URL on its next reconnect.
  - It never prints the URL.
- **Advertised:** only by nodes that set up through the tray hub. A local (CDP) node doesn't advertise `join`, and refuses the OPEN with `EOPNOTSUPP`.
- **The page:** it sends `join` to every linked device advertising the cap, waits for OPENED (or a RESET or timeout) from each, then retires the old tray. A device that was offline at that moment must be linked again, and that is the point of revocation.

#### 5.7 `unlink` (cap `join`, or `unlink-loopback` on §1.3 links), page → node
On a [§1.3](#13-local-setup-without-cdp-loopback-signalling) loopback link, `unlink` removes that page's pin and closes the link only ([§1.3](#13-local-setup-without-cdp-loopback-signalling) *Unpairing*). The rest of this section is for remote links.
- **OPEN:** `{ "kind": "unlink" }` on the control channel. The node answers OPENED `{}`, then END.
- **Then the node:**
  - deletes its join state and DTLS certificate for that hub origin;
  - closes the link;
  - stops following, printing `unlinked by <leader name>`, and exits 0.
- **A node offline at the time:** it gets `bootstrap.failed` `UNLINKED` once its next answer reveals the unlinked fingerprint, and does the same.
- **Re-linking:** running `slicc <join-url> follow` again then mints a new certificate and shows up as a new device, which is what the user asked for by running it. The leader keeps refusing the old fingerprint.

#### 5.8 `cdp` (cap `cdp`), page → node
- **HELLO:** a node with `cdp` lists its browsers: `"browsers": [{ "id": "chrome", "name": "Google Chrome", "version": "154.0.8037.98", "seven": true }]`.
  - `id` is stable for the life of the node: the browser kind, with `-2`, `-3`… for a second or third of the same kind.
  - `seven: true` marks the browser that hosts this seven tab (local setup only).
- **OPEN:** `{ "kind": "cdp", "browser": "<id>" }`. The node opens a **fresh** WebSocket to that browser's `webSocketDebuggerUrl` for each stream, so every stream is its own CDP client.
  - **OPENED:** `{ "product", "protocolVersion" }` from `/json/version`.
  - **RESET:** `ENOENT` for an unknown id; `ECONNREFUSED` when the browser doesn't answer.
- **Bytes, both directions:** a sequence of messages. Each is a `u32` BE length plus one UTF-8 CDP JSON message, at most 64 MiB, otherwise `RESET EMSGSIZE`. The node relays each one as a WebSocket text message.
- **Filtered:** the node doesn't relay `Network.webSocketFrameReceived` or `Network.webSocketFrameSent` (parity with slicc-node's `/cdp`).
- **Errors:**
  - An OPEN without `browser`, or a page END in the middle of a message, is `RESET EPROTO`.
  - `EMSGSIZE` applies in both directions, including a browser message over 64 MiB.
- **Chrome's opt-in debugging of a running browser** (`wsOnly`, no `/json/version`): OPENED comes from `Browser.getVersion`.
- **Closing:**
  - A **clean** browser close (close frame 1000) gives END. After it, the node discards page bytes until the page's END.
  - Any other browser close gives `RESET ECONNRESET`.
  - A page END closes the socket with 1000, then the node sends END. A page RESET closes it at once.
- **Kernel side:** seven maps its port-9222 facade to these streams. `playwright-cli --runtime <id>` (slicc-cdp) picks the browser.
- **Which browsers:**
  - **`attach`:** the browser it set up through (`--cdp <url>`, single-valued, `seven: true`), plus every `--cdp-extra <url>` (repeatable); the TUI's multi-select passes those. A second `--cdp` is an error that points to `--cdp-extra`.
  - **`follow`:** only the browsers named with `--cdp <url>`. Without one, `follow` doesn't advertise `cdp`, because a remote seven gets full control of every browser listed. `follow` never marks a browser `seven: true`.
  - **Protected ports:** every listed browser's debugging port is protected in the policy on both commands, so a `tcp` stream can't reach the raw port even under `--allow` or `--expose-debug-port` ([§6.5](#65-debugging-ports-every-mode)).

- **Protected origins (PRE-1):** the node filters every `cdp` stream against `protectedOrigins` from slicc-browsers (≥ 1.4.0: `*.sliccy.ai` at any depth, any port, http and https; `vault.sliccy.ai` by name; plus the setup's extra origins). It refuses navigating to, attaching to, or driving targets on those origins, and storage, cookie and permission access for them, as **CDP errors** `{"error":{"code":-32000,"message":"slicc: refused: …"}}`, not RESETs. It detaches sessions that land on them and redacts their `url`/`title` in listings to the origin. A target on Chrome's error page for a protected URL isn't locked (it holds no content from that URL), but its `url`/`title` are still redacted, and since the error page names the URL, page commands on it are limited to navigating, reloading, closing and enabling domains (others are refused, see [`cdp`](#cdp-browser-control)).
  - **Privileged browser pages** (≥ 1.8.0) count as protected origins: every URL in `protectedSchemes` (`chrome:`, `chrome-untrusted:`, `chrome-extension:`, `devtools:`, `file:`, from 1.8.5 `chrome-search:`, `chrome-native:`, `chrome-distiller:`, `isolated-app:`, `chrome-experimental-site-token-provider:`, `externalfile:` and `content:`, `edge:`, `brave:`, `opera:`, `vivaldi:`, `arc:`) except the new tab pages in `schemeExceptions`, and never `chrome-error:`; `view-source:`, `blob:` and `filesystem:` by what they wrap; `about:` aliases as the `chrome://` URL they become (`aboutScheme`). Targets of a `targetTypes` type (`browser_ui`) are protected whatever their URL, and never listed or announced to the page. The refusal text is the same.
  - **URLs that don't parse** (WHATWG) count as protected wherever the node judges a URL the browser will load or has loaded: a command's destination (`Target.createTarget`, `Page.navigate`, `Fetch.continueRequest`, `Network.continueInterceptedRequest`, `Network.loadNetworkResource`, `PWA.launch`, `PWA.install`, cookie commands' `url` or `urls`, history entries) is refused with `slicc: refused: slicc can't parse this URL, so it could be a protected page`, and a target, main frame, execution context, request, WebSocket or service worker the browser reports on such a URL is redacted, refused, detached or hidden like seven's. An empty URL and the opaque origins `null` and `://` are not affected (a `blob:null/…` URL is: its inner `null/…` doesn't parse), and a target on such a URL isn't listed at all, and URLs the node only reads (storage keys, script URLs) keep "unparseable isn't protected", and in what the page gets such a URL is cut whole. Chrome canonicalises some URLs WHATWG rejects (`http://exa mple.sliccy.ai/` → `http://exa%20mple.sliccy.ai/`, U+200D → `xn--1ug`, invalid `xn--` labels), so this is what keeps those `sliccy.ai` hosts protected. The cost: pages on such exotic hosts, and Chrome's error pages for them, are cut even when they aren't protected. The rule and its refusal come from slicc-browsers' catalog (`protectedOrigins.unparseable` and `unparseableRefusal`, ≥ 1.10.0). [`cdp` (browser control)](#cdp-browser-control) has the details.
  - **In targets the stream touched** (attached to or created): the node fails every protected document load with `net::ERR_BLOCKED_BY_CLIENT`, so instrumentation installed before a navigation can never run in a protected document. That covers redirects, service-worker-served navigations and the page's own Fetch interception.
  - **Redirects into protected URLs:** a protected URL that reaches the page inside any string it gets, such as a redirect's `Location`, `Link` or `Refresh` header or a `headersText` line, plain, unschemed (`host[:port]/path`) or percent-encoded (decoded repeatedly to a fixed point through at most five layers), is cut to its origin or unschemed host; a string that still changes after five decoding layers is blanked in full, and its `urlFragment` is blanked; unprotected URLs stay intact. A stylesheet or script whose URL, or any hop of whose redirect chain (as the node's own guard session saw it on that target, compared in WHATWG form without the fragment), is protected can't be read: `Page.getResourceContent` and `Page.searchInResource` for such a URL (or one that doesn't parse), and any command naming such a `styleSheetId` or `scriptId` (`CSS.getStyleSheetText`, `Debugger.getScriptSource`, `searchInContent` and the rest), are refused with `slicc: refused: the response came from seven's origin`, and the CSS rules and styles from such a stylesheet reach the page with only their `styleSheetId` and `origin`. `Network.loadNetworkResource` (Chrome follows its redirects with the user's cookies) and `Page.captureSnapshot` (it serialises every frame) are refused on every session. [`cdp` (browser control)](#cdp-browser-control) has the details.
  - **Workers:** a touched dedicated, shared or service worker gets a guard session too (Network only, to see its imports' redirect chains), and its commands wait for it the same way, except `Runtime.runIfWaitingForDebugger` on a shared or service worker, which goes ahead so the worker can't stall on its own guard; its imports from before the guard was ready then count as unseen and can't be read. A worker script other than the worker's own whose chain that guard didn't see, or which touched a protected URL, can't be read. In pages, scripts and stylesheets loaded before the node attached are a known residual.
  - **CDP command ids:** ids at or above `0x70000000` (1879048192) belong to the CLI and are refused from clients. `0x40000000` (2^30) through `0x6FFFFFFF` is reserved for seven's (bios) guard; other clients use ids below 2^30.
  - **Waiting for that guard:** page commands on a touched target's session wait while the node sets its guard up there. The wait fails closed after 30 s without progress (an answer or event on that target's own guard session, or the answer to the node's attach to it; answers and events elsewhere on the stream don't count) or 5 min in all, whichever comes first, and the refusal names the bound. At most 4096 commands or 64 MiB wait on a stream; more is `RESET ENOBUFS`.
  - **Sessions the node doesn't know:** a page command whose `sessionId` the node hasn't seen on this stream (in a `Target.attachedToTarget`, in the answer to the page's `Target.attachToTarget` or `Target.attachToBrowserTarget`, or its own) is held, not forwarded, until the session appears, and is then decided as usual. Only messages on that session id count as progress. The same bounds and caps apply, and the refusal is `slicc: refused: slicc doesn't know which target this session belongs to: …`, naming the bound. A session that detached is refused at once, without the suffix. Commands without a `sessionId` are unaffected.
  - **Auto-attached targets with no URL yet** (OOPIFs, popups) are held until the URL is known, then detached if protected. After 10 s with no URL they are treated as protected. The node's own setup connection ([§1.1](#11-local-setup-over-cdp)) isn't a `cdp` stream and is the one exception. [`cdp` (browser control)](#cdp-browser-control) has the details.
  - **Protection follows the target's own effective origin. Execution contexts on a protected origin are hidden, not cut,** unless the context is the target's own main-frame default context; that one still cuts the target as above. The typical case is an extension's content-script isolated world (`chrome-extension://<id>`) on an ordinary page. The page stays usable, and the hidden world is invisible and out of reach. A page that also holds a protected frame (below) loses the frame-scoped and rendering commands listed there.
    - **Hidden:** the node learns every context of a touched target on its own guard session, whatever the page enabled. It drops the world's `Runtime.executionContextCreated` and `executionContextDestroyed`, its `consoleAPICalled`, `exceptionThrown` and `bindingCalled`. Debugger events follow the Stage 1 publication gate below; a pause in a hidden world resumes privately and publishes nothing.
    - **Refused** with `slicc: refused: that execution context belongs to a browser extension`: any command that names the world through a `contextId` or `executionContextId`, `uniqueContextId`, an `executionContextName` the world has, a remote `objectId` or `*ObjectId` (call arguments included) or `callFrameId` whose injected-script part is the world, or the `scriptId` of a script in it. So `Runtime.evaluate`, `callFunctionOn`, `compileScript`, `runScript`, `globalLexicalScopeNames`, `queryObjects`, `getProperties`, `addBinding`, `Debugger.evaluateOnCallFrame`, `setVariableValue`, `setScriptSource` and `DOM.resolveNode` there are all refused. The default world, and isolated worlds the page creates itself, are unaffected by this selector rule; the Stage 1 Debugger refusals below apply in every world. A `uniqueContextId`, or a numeric `contextId` or `executionContextId`, on a target's session that names no live context the node has seen announced on that target, on its guard session or the page's (say one from a closed tab or another live tab, or a guess), is refused with `slicc: refused: that execution context is not exposed`, rather than passed to Chrome. `Runtime.addBinding` is refused on every relay session as a document-persistent command, regardless of its context selector.
    - **Lifetime:** a hidden world is forgotten only when Chrome reports it gone (`executionContextDestroyed`, or `executionContextsCleared` on a session that reported it), never to make room. A target with more than 4096 live hidden worlds is refused every command there that names an execution context, object, call frame, script or context name, until its contexts are cleared. Seen contexts follow the same lifetime and bound; once a target passes 4096, contexts announced after that are refused as not exposed until its contexts are cleared, even if some are destroyed in between.
    - **Protected frames in the page's own process:** a default execution context on a protected origin whose frame isn't the target's main frame, such as an `about:blank` or `srcdoc` child that inherits seven's origin, marks the whole target as holding a protected frame. So does a target past the 4096-hidden-world bound, or a hidden default context reported without a `frameId`. While that holds, the node refuses with `slicc: refused: target is on seven's origin` every command that names that frame's `frameId`; every `DOM`, `DOMSnapshot`, `Accessibility` and `Input` command except `enable` and `disable`; `Page.getFrameTree`, `getResourceTree`, `getResourceContent`, `captureSnapshot`, `printToPDF`, `captureScreenshot` and `startScreencast`; the `LayerTree` snapshot commands (`makeSnapshot`, `loadSnapshot`, `replaySnapshot`, `profileSnapshot`, `snapshotCommandLog`); `HeadlessExperimental.beginFrame`; and `Cast.startTabMirroring`. A screencast already running when that state begins is stopped (`Page.stopScreencast` on the page's session), and its `Page.screencastFrame` events are dropped until the page starts a new cast, which works again only once the state has ended. A screenshot, layer replay, profile, command log or begin-frame answer that arrives after the state began is replaced with the same refusal. The state ends when Chrome reports the frame's context gone or the contexts cleared. Meanwhile a document that inherits its origin from one of the target's in-process child frames is refused; [`cdp` (browser control)](#cdp-browser-control) has the rules for inherited documents and their openers. A protected iframe target in another process also puts its page ancestor in this state, including an ordinary-origin iframe target whose renderer holds a protected local frame. The node resolves its `parentFrameId` from main-frame target ids, observed frame ids and execution-context frame ids; for preloaded frames it also reads frame trees through private sessions. Those internal answers and session attach/detach events are not forwarded to the client. An unresolved or missing parent locks every page target in the same browser context until ownership is resolved. The parent lock belongs to the root document the iframe is in: a new root document releases the old one's frames, which may stay frozen in the back/forward cache, and restoring that document locks the page again at once. When the node didn't see which document a protected iframe directly under the page belongs to, it asks Chrome privately which element owns the frame. Only Chrome's explicit answer that the frame isn't there releases the lock, until the root document changes or the frame attaches again; any other answer, or none, keeps it. The lock also ends when the iframe target goes away or ceases to be protected, including when its protected local-frame state ends. The rest of the page stays drivable, with the event filtering above: navigation, `Runtime` in the default world, `Network`, `Fetch` and the other domains.
    - **Heap profiling:** a page's heap snapshot covers its whole renderer, other worlds included. So on page, iframe, background page and webview sessions every `HeapProfiler` command except `enable`, `disable`, `collectGarbage` and `getHeapObjectId` is refused with `slicc: refused: HeapProfiler reads the page's whole renderer, browser extensions' worlds included`, whether or not an extension is there.
    - **Debugger (Stage 1):** one session per target on each relay stream owns the debugger. Only `Debugger.enable`, `setInstrumentationBreakpoint`, `disable`, `removeBreakpoint` for the owner's breakpoints, and `resume` at that owner's published pause are admitted. Admission requires an ordinary target, a ready navigation guard, no hidden world and the whole-target check used by `Page.getFrameTree`. Every other command in `Debugger`, `DOMDebugger` and `EventBreakpoints` is refused with `slicc: refused: debugger methods other than enable, disable, setInstrumentationBreakpoint, removeBreakpoint and resume could read or run code in seven's pages or an extension's world, so the guard doesn't allow them`: URL breakpoints (`setBreakpointByUrl`), stepping, `DOMDebugger.getEventListeners`, `getScriptSource` and `evaluateOnCallFrame` included. No client can send `Debugger.pause`. `scriptParsed` and `scriptFailedToParse` are held, not forwarded on arrival: only the verified pause's script records, including every validated async and parent-stack script it references, are published before it; `breakpointResolved` and unrelated Debugger events are suppressed. Each `Debugger.paused` is held behind a live frame-tree read, bounded to 2 s, that verifies the owner's document generation and the placed ordinary main-world contexts of every call frame. An instrumentation pause is converted privately to a normal pause with the same owner, generation and deadline; evaluations and contexts already published in that document resume privately without conversion. An unplaced child may settle within the pause's deadline. Anything protected, stale or unverified resumes privately with nothing published. An owned pause has a 5 s deadline and guard-owned resume on expiry; a commit, hidden world or protected frame revokes its publication. A `Debugger.resumed` reaches the owner only for its published pause while that proof still holds. Persistent `enable` and `setInstrumentationBreakpoint` replies retain bookkeeping across a document-only generation change, with safety revocations still invalidating replies. A verified batch that fits the window waits for available credit with no output or guard lock held. Output-lock contention also requires a fresh barrier before retry. Each wait is followed by a fresh live frame-tree read through the same placement check, ordering the proof after queued browser events, and then a full ownership, generation, root-loader, context and admission recheck before one atomic write. Failed proof, revocation or expiry resumes privately without publication, within the original 5 s deadline.
      A batch larger than the 256 KiB stream window resumes privately and publishes nothing; [#625](https://github.com/ai-ecoverse/slicc-cli/issues/625) tracks this limit.
    - **Profiles, coverage, stacks and initiators** can name any script in the isolate. So the node scrubs every message it sends the page, events and command results alike, outside opaque values (`value`, `preview`, `customPreview`, `arguments`, `returnValue`). Any `callFrames` list loses the frames on a protected URL or in a hidden world's script. That covers console, exception, pause, async, node-creation and initiator stacks, whatever the method. A coverage or type-profile list (entries with `scriptId` and `functions` or `entries`) also loses its entries for scripts without a URL. A profile node's `callFrame` in such a script becomes `(program)` and loses its `positionTicks`. An `initiator` on a protected `url` loses that `url` and its position.
    - **Messages without a context:** on page-like sessions, the node drops `Console.messageAdded` whose `url` is empty or protected. It drops `Log.entryAdded` on a protected `url` or with a stack through such a frame. It drops `Profiler.consoleProfileStarted` and `consoleProfileFinished` altogether: they name no context, so the node can't tell whose `console.profile` they are.
    - **Requests and sockets a hidden world starts:** a `Network.requestWillBeSent`, `webSocketCreated` or `webTransportCreated` whose initiator stack runs through a frame on a protected URL or in a hidden script, or whose initiator `url` is protected, marks the request as the hidden world's (a WebTransport by its `transportId`). The node learns this on its own guard session as well as on the stream's sessions. Every later message to the page that names that request anywhere in its params or result, by `requestId`, `networkId`, `transportId` or any `…RequestId` (such as `Log`'s `networkRequestId`), is cut as a whole, and a `Log.entryAdded` about it is dropped. That includes extra-info events and nested references such as an `Audits` issue's affected request:
      - every URL is cut to its origin;
      - header maps are emptied;
      - `postData`, `postDataEntries`, `headersText`, `requestHeadersText` and the cookie lists are removed.

      The request is also treated like one to a protected origin on every session of the target, including sessions that missed its start, so its bodies and post data are refused and it can't be replayed. A pause of such a request (`Fetch.requestPaused`/`authRequired`, `Network.requestIntercepted`) is continued by the node and never shown to the page. A `Fetch.authRequired`, which carries no `networkId`, is answered by the node (`Default`) when its Fetch `requestId` belongs to such a pause. A pause whose request (`networkId` for `Fetch`, `requestId` for `Network.requestIntercepted`) the guard session hasn't reported yet waits up to 2 s for it. Past 4096 waiting pauses, the node continues further ones itself instead of showing them. A target with more than 65536 such requests fails closed: every request there is cut.

#### 5.8.1 `browsers` updates (page cap `browsers`), node → page
- **When:** the browser list changes while a link is up (a browser quits, or the TUI adds one). The node then opens `OPEN { "kind": "browsers", "browsers": [ … ] }` on the control channel, carrying the **full** current list in HELLO's shape. It replaces the previous list; there are no deltas.
- **Answer:** the page answers OPENED `{}`, then END, and the node sends END. A RESET is ignored, since the next update carries the full list anyway.
- **Only to pages that advertise `browsers`.**
- **Ids never change meaning:** a removed id is never reused for another browser, and re-adding the same endpoint (same CDP host:port) gets its old id back.
- **Removal:** a later `OPEN cdp` for a removed id gets `ENOENT`, and the node resets that browser's open `cdp` streams with `ECONNRESET: browser removed`.
- **Automatic removal:** the node checks each browser's debugging port every 2 s. After two misses it removes the browser (`ECONNRESET: browser quit`). Once the port answers again, it lists the browser again under the same id.

#### 5.9 `kernel` listener on the node (local only)
- **When:** a local node whose peer advertises `kernel-in` listens on `127.0.0.1:<kernel port>` (default `80`, or `--kernel-port`; `--no-kernel` turns it off). It serves `http://<port>.kernel.localhost/`, with slicc-node's rules ([its README, *Kernel services*](https://github.com/ai-ecoverse/slicc-node#kernel-services)):
  - the request head is read for at most 64 KiB and 30 s;
  - exactly one `Host` of the form `<1–65535>.kernel.localhost`, with no port or the listener's own port, no leading zeros and no trailing dot;
  - errors: `421`, `400`, `431`, `502 no seven page connected`, `502 nothing listening on kernel port N`, `504`.
- **Per connection:** the node opens `OPEN { "kind": "kernel", "port": N }` to the page, sends the bytes it has read, then pipes both ways.
  - The node refuses port 9222 itself, before any OPEN.
  - Its listener port joins the policy's protected ports.
- **Binding:** on macOS, where an unprivileged user may bind port 80 only on the wildcard address, a `permission denied` on `127.0.0.1` makes the node bind `0.0.0.0:<kernel port>` and reset every connection from a peer that isn't loopback, before reading from it.
  - With the default port 80 out of reach (Linux without `CAP_NET_BIND_SERVICE`, or taken), the node picks the first port in 17180–17189 it can bind, at startup.
  - **HELLO:** a local node with kernel services adds `"kernel": { "port": <n> }`, the port it listens on (80, a spare port or `--kernel-port`). The page should build kernel URLs from it: `http://<kernel port>.kernel.localhost/` for 80, `http://<kernel port>.kernel.localhost:<n>/` otherwise. A node without the field listens on 80, if at all.
  - If the port can't be bound, the node prints `kernel services are off: … try --kernel-port` and goes on without them.
- **Remote nodes** never listen.

#### 5.10 `fs` (cap `fs`), page → node: host folders over the link
slicc-node's hostfs ([its README, *Host folders*](https://github.com/ai-ecoverse/slicc-node#host-folders)), carried as link streams instead of HTTP. The kernel's `hostfs` driver keeps its operations and semantics. [Host folders](#host-folders) is the detailed spec, and this section is the summary.

- **Export:** `--mount <path>[:<name>][:ro]` (repeatable) on `attach`, `follow` and the default flow.
  - The root is `realpath`'d at start. A missing path, a file, or a taken name is skipped with a warning.
  - Names never reveal host paths.
  - Without `--mount`, the node doesn't advertise `fs`.
- **HELLO:** `"mounts": [{ "name", "readonly", "capabilities": { "maxIo", "symlinks", "chmod", "caseInsensitive", "normalization", "ranges": true } }]`.
  - `capabilities` is exactly slicc-node's grant answer, probed **per mount** on that folder's volume, so seven passes it to the driver unchanged.
- **No tokens:** the link is the trust boundary.
- **Read-only per OPEN:** the page may set `readonly: true` on any OPEN, and the node enforces it per OPEN (`EROFS` for any write, write open or namespace change), even on a writable export.
- **Stream shape:** one stream per op, `OPEN { "kind": "fs", "mount", "readonly"?, "op", …args }` with slicc-node's ops and arguments. The OPEN is at most one frame (64 KiB).
  - The node answers OPENED (plus DATA for `list` and `read`), then END. The page sends END once it has OPENED.
  - **Errors:** `RESET <ERRNO>: <message>`, where the message is libuv's lower-case strerror text and never contains a path. Wrongly typed or out-of-range arguments, and an unknown `op`, are `EINVAL`. An unknown `mount` is `ENOENT`.
- **Op answers:**
  - Most ops answer in OPENED with slicc-node's answer JSON.
  - **`list`:** OPENED `{}`, then NDJSON as DATA (`{"name","attr"}` per line), then END.
    - It's streamed in batches with bounded memory, so an entry that vanishes between batches is left out.
    - If the directory itself is moved or replaced mid-list, the stream ends with `RESET ESTALE`.
  - **`read`:** OPENED `{ "etag", "size", "offset", "length" }`, then exactly `length` bytes as DATA, then END. The read is short at EOF, `length: 0` past it, and `RESET ESTALE` if the file shrinks during the read.
  - **Etags** keep slicc-node's quoting, so `attr.etag` and `ifMatch` compare as plain strings.
  - **Layout:** the page should give reads over 64 KiB their own channel, and the node accepts either layout.
- **`write`:** `OPEN { "kind": "fs", "mount", "op": "write", "fh", "offset" }`, on its own stream channel.
  - The node first runs the checks that need no I/O, and RESETs before OPENED with `EROFS`, `EBADF` (unknown fh, or one from another mount or without write flags) or `EINVAL`.
  - Then OPENED `{}`, and DATA, credited once each `pwrite` returns.
  - More than `maxIo` in total is `RESET EINVAL`, and what came before stays written.
  - The node sends END after the page's END, once the last `pwrite` has returned. A page RESET leaves what was written.
- **Watch:** `OPEN { "kind": "fs", "op": "watch", "mounts": [ … ] }` on its own channel. A missing or empty list is `EINVAL`, and an unknown name `ENOENT`.
  - OPENED `{}`, then slicc-node's NDJSON lines as DATA.
  - A page that doesn't read gets its backlog folded into one `{"mount","all":true}` per mount, instead of a dropped stream.
- **Watch replacement:** a watch OPEN is cheap to replace. The page typically keeps one per link covering every mounted name, and restarts it when the list changes.
- **Mount updates** (page cap `mounts`): when the node's export set changes, it opens `OPEN { "kind": "mounts", "mounts": [ … ] }` on the control channel, the full list in HELLO's shape, like [§5.8.1](#581-browsers-updates-page-cap-browsers-node--page). The page answers OPENED `{}` then END.
  - Handles on a removed mount get `EBADF`.
  - Its ops get `ENOENT`.
  - A watch covering it ends with `RESET ENOENT`.
- **Handles:** numbered per link from 1, at most 4096 open per link and mount (`EMFILE`). An fh used with another mount is `EBADF`. All of them close with the link.
- **Locking:** one node-wide lock across all links and mounts, as in slicc-node. Namespace changes and write opens are exclusive. Name opens, `read`'s re-open, `stat`, `list`, `readlink` and `statfs` are shared. `pwrite` takes no lock.
- **Path rules and safety:** exactly slicc-node's:
  - `realpath` the parent, then lstat semantics for the last component;
  - `..`, a leading `/` and NUL refused;
  - namespace changes serialized;
  - the root can't be removed or renamed (`EBUSY`);
  - writes in place.
- **Windows:**
  - `symlinks: false` and `chmod: false`, and the `symlink` op is `EPERM`.
  - Existing symlinks and junctions show as `kind: "symlink"`, and `readlink` works on them.
  - `\` and `:` in a path are `EINVAL`.
  - Paths go through `\\?\`, so device names and 8.3 names are plain names inside the folder.

#### 5.11 `agent` (page cap `agent`), node → page: remote control
`slicc prompt`, `slicc watch`, `slicc wait` and `slicc abort` talk to seven's agent, the cone, which slicc-agent hosts. The page serves each stream through `remoteControl(connection)` from `@ai-ecoverse/slicc-agent/page`, on top of its `AgentConnection`, so the CLI never speaks pi's protocol.

- **Who may:** the page advertises `agent` only to links the user allowed to control it.
  - A **local** link ([§1.1](#11-local-setup-over-cdp)) gets it by default, because a CLI holding the browser's CDP can drive the page anyway.
  - A **remote** link gets it only after the user turns on *Allow remote control* for that device in Linked devices. It's off by default, and keyed on the fingerprint.
  - Seven lists every device that has linked once, even one that's offline or offered no caps, so the toggle can be found after a refused `prompt`.
  - Without the cap, the node doesn't open `agent` streams, and the page refuses them with `EACCES`.
- **Control-only links:** `prompt`, `watch`, `wait` and `abort` links advertise `caps: []`. The page gives them no `<host>.slicc.internal`, no exit entry, and opens no `kernel` streams to them.
- **One device, several processes:** the node's HELLO ([§4](#4-hello)) and the [§1.1](#11-local-setup-over-cdp) hello both carry a random per-process `"node"` id.
  - The page replaces an older link of the same device (local: any local link; remote: the same fingerprint) only when `node` matches.
  - Over the tray, the page learns `node` at HELLO, so any replacement waits for HELLO.
  - So `slicc <join> prompt` doesn't kick that machine's running `follow`, and `slicc prompt --cdp …` doesn't kick `slicc`'s bridge.
- **`prompt`:** `OPEN { "kind": "agent", "op": "prompt", "text": "…", "whenBusy": "steer" | "followUp" | "reject", "thinking"?: true }` on its own channel. The default `whenBusy` is `followUp`.
  - **Long prompts:** a prompt that doesn't fit in the OPEN frame sends `"length": <bytes>` instead of `text`.
    - The page answers OPENED `{}` at once. The node then sends exactly that many bytes of UTF-8 as DATA, then END. A different count is `RESET EPROTO`.
    - The page then delivers the prompt. The first NDJSON line is `{"t":"delivered","delivered":…}`, or `RESET EBUSY` for a `reject` while busy, then the events as usual.
    - The node uses this past about 60 KiB, up to 16 MiB.
  - **OPENED** (inline `text` only): `{ "delivered": "run" | "steer" | "follow-up" }`, once the agent has accepted the send. A `reject` while busy is `RESET EBUSY`. A `length` prompt gets OPENED `{}` at once instead, and its delivery comes as the first NDJSON line (above).
  - **Then DATA:** NDJSON events, **starting at this prompt's own `user` event**. Nothing from before it is sent. They run until the run that includes it ends.
  - **The last line** is `{"t":"end","reason":"stop"|"aborted"|"error","message"?}`, then END.
  - **Aborting:** a page RESET or a closed CLI doesn't abort the run. Aborting is explicit.
- **`abort`:** `OPEN { "kind": "agent", "op": "abort" }`, answered OPENED `{ "aborted": true | false }` (false when nothing was running), then END.
- **`watch`:** `OPEN { "kind": "agent", "op": "watch", "since"?: "<entry id>", "thinking"?: true }` on its own channel.
  - **OPENED:** `{ "head": "<entry id>", "gap"?: true }`, then NDJSON events forever.
  - **Without `since`,** the stream starts at the next event.
  - **With `since`,** the page first replays `since`'s own entry when it's `text` or `thinking`, as one `delta` with its whole text so far. Then it replays every entry after it. Clients skip what they already printed.
  - **`gap: true`** means the transcript no longer holds `since` (a new session, or a compaction). The stream then starts at the next event.
  - **`busy`** (`"busy": true` in the OPEN) is for `slicc prompt --allsettled` and `slicc wait`. The cone can end a run while scoops it started are still working, and run again when their results come in as `scoop-notify` licks. A client that sees only the cone's events can't tell that apart from being done.
    - **OPENED** carries `"busy": true | false`, the state after any replay. A page that doesn't support it leaves `busy` out, and the node then reports that seven doesn't support it.
    - **Then** the page sends `{"t":"busy","busy":true|false}` each time the value changes, in order with the other events. It has no `id`, and it's never replayed.
    - **`busy` is true** while, for the cone the stream belongs to:
      - the cone has a run or a queued input;
      - a live scoop of that cone has a run, a queued input or a feed;
      - a lick for the cone is queued or still to be delivered (a `scoop-notify` result, or a scoop `wait`'s delivery). That includes a lick held in the agent's outbox until its conversation's model is known. If the model never comes, `busy` stays true, so the client bounds its wait.
    - **It turns false** only when none of that holds. A lick from outside (a cron, a file watch) counts once it's queued, not before.
    - **`why`:** a `busy: true` line may carry `"why"`, a list of what keeps the agent busy, to be logged. The node doesn't use it, and it's left out when `busy` is false. The values come from a fixed list:
      - `"cone"`: the cone has a run or a queued input;
      - `"scoop:<name>"`: that live scoop (its name, as seven shows it) has a run, a queued input or a feed;
      - `"pending-lick"`: a lick for the cone is queued or still to be delivered.
    - **Read-only status:** `busy` lines carry only the boolean and `why`. They never carry prompt text, tool arguments or output, and asking for `busy` changes nothing in the agent.
    - **Ordering:** a `run start` never comes before the `busy: true` that covers it, and the `busy: false` comes after the last `run end`.
    - **Computed** from the durable documents' subscriptions (the cone's and its scoops' `pi.live`, `ScoopWorkDoc.feeds` and the licks outbox), and a line goes out only when the value flips.
- **`inventory`:** `OPEN { "kind": "agent", "op": "inventory", "watch"?: true }` on its own channel, for `slicc agents` and a native app that shows seven's agents. The page serves it through `remoteControl(connection).inventory()` from `@ai-ecoverse/slicc-agent/page`, which reads slicc-agent's owner-only inventory service.
  - **OPENED:** one snapshot:

    ```json
    {
      "v": 1,
      "instance": { "id": "pL4vT2nQ8sK1mX6zR0bYcA", "name": "seven on sliccy.com" },
      "seq": 42,
      "changedAt": 1767268800000,
      "reportedAt": 1767268802000,
      "counts": { "running": 1, "queued": 0, "waiting": 1, "idle": 1, "error": 0 },
      "agents": [
        { "id": "hR7cZ2mVq9LwX4eN1sKj0A", "kind": "cone", "status": "waiting", "queued": 0, "provider": "anthropic", "model": "claude-opus-4-6", "since": 1767268740000 },
        { "id": "x3Lk9aQeV2bN0cR5tY8uWg", "kind": "scoop", "parent": "hR7cZ2mVq9LwX4eN1sKj0A", "status": "running", "queued": 1, "provider": "anthropic", "model": "claude-sonnet-4-6", "since": 1767268760000 }
      ]
    }
    ```

  - **Without `watch`,** the page sends END after OPENED.
  - **With `watch`,** each new snapshot follows as one DATA line, `{"t":"inventory", …}` with the same fields, until either side resets. Lines are always whole snapshots, never deltas.
    - A line goes out only when something other than `reportedAt` changed, at most one every 250 ms (the page's throttle), and the latest state is always sent. slicc-agent builds a snapshot from the subscriptions that feed seven's transcript and monitor, and drops one whose content didn't change. It has no timers of its own.
    - The page repeats the current snapshot at least every 30 s, with the same `seq` and `changedAt` and a new `reportedAt`. Its timer runs only while a `watch` stream is open.
  - **Fields:**
    - `v`: 1. A change that breaks a reader gets a new `v`.
    - `instance.id`: seven's own id, 22 characters of base64url from 16 random bytes, minted once per seven profile by the page and kept in its storage. A new profile, or cleared storage, gets a new id (and new agent ids). It isn't derived from anything about the user, the device or the tray. `instance.name`, when present, is the `name` of seven's HELLO.
    - `seq`: goes up by one with every new snapshot slicc-agent builds, within one run of seven's agent. It isn't kept: after a reload it starts again at 1. All streams of one seven see the same `seq` for the same snapshot while the agent runs. A `seq` lower than the last one seen means the agent restarted, and a client treats the stream as new.
    - `changedAt`: when slicc-agent built this snapshot, in ms since the epoch, from the commit that changed it. `reportedAt`: when the page sent this line.
    - `counts`: the number of agents in each status. They add up to the number of `agents`.
    - `agents`: the cone, then its live scoops in the order seven lists them.
      - `id`: an opaque id, 22 characters of base64url, such as `"hR7cZ2mVq9LwX4eN1sKj0A"`. The page makes it from slicc-agent's registry id with HMAC-SHA-256 under a random key it minted for this seven with `instance.id`, keeps in its storage and never sends, and takes the first 16 bytes. So it's the same for an agent across streams, reloads and restarts of that seven, and two sevens never share one. It carries nothing else: not the scoop's folder, which comes from the name the scoop was given, nor whether it's the first cone. Clients compare ids and don't read them.
      - `kind`: `cone` or `scoop`. `parent`, for a scoop only: the opaque `id` of the agent that started it, the cone or another scoop.
      - `status`, one of:
        - `running`: it has a run, retries and approval waits included;
        - `queued`: no run yet, and at least one input waits for it;
        - `waiting`: no run and nothing queued, but work it waits for is pending: a scoop `wait` of the cone, a feed to the scoop, or a lick for it held in the agent's outbox (the same signals as `busy`);
        - `error`: none of those, and its last turn ended in an error;
        - `idle`: none of those.
      - `queued`: how many inputs (prompts, follow-ups and licks) wait for it, 0 or more.
      - `provider` and `model`: pi's `provider` and `modelId` for the agent's conversation, such as `anthropic` and `claude-sonnet-4-6`. `provider`, one of slicc-agent's provider ids, is always sent when the agent has a model. `model` may be absent: slicc-agent sends it only when pi's model catalog has that model for that provider, so a user-named deployment (Azure), a custom inference profile or an ARN is left out. Both are left out when the agent has no model. A change of model is a new snapshot.
      - `since`: when the agent entered its `status`, in ms since the epoch, as slicc-agent saw it since it started.
  - **Scope:** the cone the tab's agent shows, the same cone `watch` and `prompt` reach, and that cone's live scoops. Never other cones, frozen cones, other kernels' agents or guest seats. When the tab selects another cone, the next snapshot is that cone's.
  - **Who may:** the `agent` cap, as for the other ops, and slicc-agent serves the inventory only on its owner seat. View and control seats and guests get none. Asking for it changes nothing in the agent.
  - **Only the tab that runs the agent answers.** Any other page, a follower tab included, answers `EOPNOTSUPP: the agent is not running`.
  - **Never sent,** in any field: agents' names, folders and titles (they come from chats), prompts, transcript text, tool names, arguments and output, error messages, role text, paths, URLs and base URLs, account names, endpoint registrations, anything from the vault, settings or MCP servers. The fields above are all there is, and new ones are added only under these rules.
  - **Older sevens:** a page from before this op answers `EINVAL` (an unknown op), and a page whose slicc-agent has no inventory answers `EOPNOTSUPP`. The node treats both as `this seven doesn't report its agents`.
- **Events:** one JSON object per line, mapped by slicc-agent from pi-durable's agent events (`watchEvents`) and handed to the page by `remoteControl(connection)` from `@ai-ecoverse/slicc-agent/page`.
  - `{"t":"user","id","text","delivered"}`;
  - `{"t":"text","id","delta"}`: assistant text as it streams;
  - `{"t":"thinking","id","delta"}`: only when the stream was opened with `thinking`;
  - `{"t":"tool","id","call","name","summary"}` and `{"t":"tool-result","id","call","ok","summary"}`;
  - `{"t":"run","state":"start"|"end","reason"?}`. These have no `id`, so a resume doesn't replay a missed `run end`. Clients use them only for layout and notices, and the `end` line or the next `user` event is authoritative.
  - `{"t":"notice","id","text"}`: retries, compactions, rewinds;
  - `{"t":"error","id","message","detail"?}`;
  - `{"t":"busy","busy":true|false,"why"?:[…]}`: only on `watch` streams opened with `busy` (above).
  - **Ids** are opaque strings.
    - `user`, `tool-result`, and a `notice` or `error` that is a transcript entry: the entry id, such as `"42"`.
    - An assistant message's `text`, `thinking` and `tool` blocks: `"a<ts>.<block>"`, where `<ts>` is the message's own timestamp (set when its generation starts, and kept in the committed entry) and `<block>` its content index. An assistant message isn't an entry until it's committed, so this id is stable from the first delta to the committed entry. A second message with the same timestamp gets `"a<ts>~1.<block>"`, a third `~2`, and so on.
    - Other notices and errors: an id unique to that event, such as `"retry-<at>"`, `"compaction-<task>"` or `"task-<task>"`.
    - `run` lines have no id. A watch can't replay them, because runs aren't transcript entries.
  - **Guarantees** (the CLI's tracker relies on them):
    1. **An id is never reused for different content.** A regenerated or retried message is a new message with its own timestamp, and messages that share a timestamp get the `~n` suffix.
    2. **Deltas only append.** `delta` lines for one id arrive in order, and their concatenation equals the text `watch` replays for that id as `since`'s own entry, byte for byte.
    3. **Every event that isn't streamed has its own id.** A client drops a second non-streamed event with an id it has already shown, so a notice that changes gets a new id.
    4. **A `since` that names nothing the transcript still holds** (a compacted entry, or a message an abort or reload dropped before it was committed) answers `gap: true`. It never fails the OPEN.
  - **`delivered`** on `user`: `run`; `steer` when it follows a tool result in a run; `follow-up` when it was queued behind a run.
  - **A `prompt` stream** carries only its own `user` event, not other clients' user entries, even a steer into the same run. `watch` carries them all.
  - **`run end`** comes once the agent has settled the run's inputs, so its `reason` (`stop`, `aborted` or `error`) is the agent's. A `prompt` stream's `end` line follows it.
  - **Rules:** `summary` is at most 200 characters, with secrets redacted.
  - **Never sent:** credentials, or tool output beyond the summary. A tool's summary is its name and main argument (the command, path, pattern or URL). A result's summary is its line count, or the first line of an error.

#### 5.12 `udp` (cap `udp`), page → node
- **OPEN:** `{ "kind": "udp", "family": 4 | 6 }` opens one unconnected UDP socket on the node, bound to an ephemeral port on all addresses of that family. **OPENED:** `{ "localAddr": "ip:port" }`.
- **Records, both ways,** independent of DATA boundaries: `u8 type`, `u16` BE host length, host, `u16` BE port, `u32` BE length, payload.
  - **Type `1`, a datagram:**
    - page → node: the destination. The kernel always sends an IP literal of the socket's family; a hostname or the wrong family is `EPROTO`;
    - node → page: the source of a received datagram.
    - One record is one datagram, payload at most 65 507 bytes (IPv4) or 65 527 (IPv6).
  - **Type `2`, node → page only, unreachable:** host and port of a destination the node refused or that answered ICMP unreachable. The payload is the errno text (`EACCES: …` for policy, `ECONNREFUSED`, `ENETUNREACH`). The kernel may surface it on a connected socket and otherwise drop it.
- **Policy, per datagram:** the node's dial policy and protected ports apply to every destination ([§6](#6-policy-node)).
  - **A refused destination drops that datagram only,** with a type-2 record back. The socket stays open, because one resolver socket legitimately tries several servers.
  - **Broadcast, multicast and the unspecified address are always refused.**
- **Receiving: address-and-port-dependent filtering.** Like a NAT, the node passes on a received datagram only if its source `ip:port` is one the socket has sent to. Everything else is dropped silently.
- **Close:** END from either side closes the socket. An idle socket (nothing sent or received for 5 min) is closed by the node with END.
- **RESET codes:** `EPROTO` (a malformed or oversize record, or the wrong family) and `ENETUNREACH` (no usable interface of that family at OPEN).
- **Flow:** no credit back-pressure for datagrams. Either side drops a datagram that doesn't fit the stream window instead of waiting, as UDP would, and counts it. The count is in the dashboard and `SLICC_DEBUG` logs.
- **Privacy:** never log payloads; log only destination host and port at debug.
- **HELLO:** nodes that implement it add `"udp"` to `caps`, on `slicc`, `attach` and `follow --exit` (as `ws`).

**Node-side details:**
- **Socket:** `udp4` on `0.0.0.0` or `udp6` (IPv6 only) on `::`, so `localAddr` is `0.0.0.0:<port>` or `[::]:<port>`. "No usable interface" means no interface that is up has an address of that family, or the bind fails.
- **`EPROTO` also covers:** a record type other than `1` from the page, a host that is empty, longer than 64 bytes, carries a zone (`%eth0`) or is IPv4-mapped (`::ffff:a.b.c.d`) on an IPv6 socket, port `0`, END inside a record, and an OPEN whose `family` isn't the number `4` or `6`.
- **Always refused, with `EACCES`:** `255.255.255.255`, the directed broadcast address of every IPv4 interface of this machine (enumerated as for protected ports; if the interfaces can't be enumerated, every IPv4 destination is refused), `224.0.0.0/4`, `ff00::/8`, `0.0.0.0` and `::`. They're checked before and after [§6.1](#61-where-its-checked)'s normalization, so a NAT64 or 6to4 address can't embed them. `--allow` doesn't reopen them.
- **Type 2, other than policy:** a send the OS fails at once (`ENETUNREACH`, `EHOSTUNREACH`, `EMSGSIZE`, …). ICMP unreachable comes only on Linux (`IP_RECVERR`), for destinations the socket has sent to. macOS and Windows don't report it on an unconnected socket, so there the datagram is just lost.
- **Filtering:** the node remembers the 4096 destinations a socket sent to most recently. A destination counts from the moment its datagram goes out, so a reply that comes back at once isn't dropped.
- **Idle:** a datagram the page sends, or one the node passes on, counts as activity. Unsolicited datagrams don't.
- **Drops:** only node → page, when a record doesn't fit the page's window. The dashboard's `streams` line shows `udp … dropped`, and `SLICC_DEBUG` logs `udp <localAddr>: dropped N datagrams …` at most once a second, and the totals when the socket closes. The node reads page records all the time, so it never makes the page drop.
- **Logs** (`SLICC_DEBUG`): `udp <localAddr>: open`, the first datagram to each destination (`to <ip:port>`), refusals, and `closed, N sent, M received, K dropped`. Never a payload.

#### 5.13 `credentialed` (node cap): `http` streams that carry a value
Values live in the vault (`vault.sliccy.ai`). A request reaches a node with a value in it only through a **transit exit**: the vault's `forward` hands bios the injected head, and bios sends it over the link.

- **The cap:** a node that lists `credentialed` in `caps` handles every `http` stream opened with `"credentialed": true` as below. There's no new kind.
- **OPEN:** `{ "kind": "http", "credentialed": true }`. The bytes in both directions are [§5.3](#53-http-cap-http-page--node)'s.
- **The page** sets the flag on every `http` OPEN whose head carries an injected value, and sends such an OPEN only to a node that advertises the cap. A node without it ignores the field ([§4](#4-hello)), so to the vault that device has no transit exit.
- **The node:**
  - never logs the request's or the response's head or body, at any level, `SLICC_DEBUG` included;
  - names the request only by method, host and path, without the query, the fragment or userinfo. That holds for logs, the dashboard, `--json` events and RESET messages;
  - doesn't follow redirects (as for every `http` stream);
  - keeps nothing: no cache, no cookie jar, nothing on disk, and nothing of the request in memory once the stream has ended;
  - reports nothing about the request to telemetry. An error report carries its fixed source and the errno only.
- **RESETs:** [§5.3](#53-http-cap-http-page--node)'s codes. The text after `ERRNO:` never contains a header, a body byte, the query or the full URL. A TLS failure is `ECONNREFUSED: fetch failed: x509: …` and names only the host.
- **Refused:** a credentialed head whose URL isn't `https:` on port 443 is `RESET EACCES` before anything is dialled. The vault already refuses those, so this is defence in depth.
- **Not the node's job:** redacting the response. The node passes it on as it came, and the vault's and bios's rules apply on the page side.

#### 5.14 `jerk` (caps `jerk` and `jerk-seal`), page → node: J1
jerk ([ai-ecoverse/slicc-jerk](https://github.com/ai-ecoverse/slicc-jerk)) gets tokens on the host.

**The ground rule:** no token, grant, code or masked token crosses the link. The **one exception** is `seal`'s `lease` event, which only bios's link layer reads.

**Advertising:**

| node command | `jerk` (status, create) | `jerk-seal` (seal, unseal) |
|---|---|---|
| `slicc`, `slicc attach` (local) | yes, when `jerk` ≥ the minimum version is on the host's `PATH` or in slicc's data dir. `--no-jerk` turns both off. | yes, by default |
| `slicc <join> follow` (remote) | only with an explicit `--jerk` | only with an explicit `--jerk-seal`, which implies `--jerk`. A remote seven pulling this machine's tokens into its browser is a bigger grant than starting a sign-in. |
| `prompt`, `watch`, `wait`, `abort` | no | no |
| any node without host jerk | no. Host providers are missing in seven. | no |

HELLO adds `"jerk": { "version": "1.0.0" }` next to `caps`.

**OPEN:** `{ "kind": "jerk", "op": <op>, "provider"?: <id>, "scope"?: <string>, "lease_id"?: <string>, "duration"?: <seconds>, "params"?: { <k>: <string> } }`

| op | cap | opened by | fields | what the node runs, as the host user | limits |
|---|---|---|---|---|---|
| `list` | `jerk` | the kernel facade or bios | none | `jerk list --json` | |
| `status` | `jerk` | facade or bios | `provider` | `jerk <provider> get --json` without `--reveal`; the node keeps only the status fields | |
| `accounts` | `jerk` | facade or bios | `provider` | `jerk <provider> list --json` | |
| `create` | `jerk` | facade or bios | `provider`, `scope?`, `params?` | `jerk <provider> create --via-link --seven-origin <origin of the linked page> [--scope S] [-p k=v]` | only when the provider's create **opens a sign-in page a human completes**: `[oauth]` modes `loopback`, `intercept` and `device`, and CLI flows marked `create.link = true`. Never `--force`. Never a create that mints a token with no UI (`npm token create`, `lease_via = "create"`). |
| `seal` | `jerk-seal` | **bios's link layer only**; the facade refuses it | `provider`, `duration?` (default 3600) | `FNOX_LEASE_DURATION=<duration> jerk <provider> lease --via-link` | |
| `unseal` | `jerk-seal` | **bios's link layer only** | `provider`, `lease_id` | `FNOX_LEASE_ID=<lease_id> jerk <provider> drop --lease --via-link` | revokes upstream only what jerk minted |

- **`--via-link`** is a host-jerk flag. It forbids a terminal, `--reveal`, `--force` and CLI logout. Its stdout is status JSON, plus the single lease object for `lease`. Host jerk enforces it again itself, so a buggy node can't widen it.
- **There's no `drop` op.** A provider CLI logout is never possible over J1, and jerk-minted lease tokens are dropped through `unseal`.
- **`provider`** matches `^[a-z0-9][a-z0-9-]{0,63}$`. **`params`** must be well formed (the node-side details below), otherwise `EPROTO`. Whether a key is one the provider declares is checked by host jerk, and an undeclared key arrives as `result.exit` 2.
- **OPENED:** `{}` once the node has started host jerk.
- **Bytes node → page:** newline-delimited JSON events, then END.
  - `{ "event": "progress", "message": "sign-in opened in Google Chrome on laptop" }`, zero or more of them, for `create` only. A device-flow user code may appear here, since it is useless without the human's approval.
  - `{ "event": "lease", "credentials": { "<ENV>": "<token>", … }, "expires_at": "<RFC3339>", "lease_id": "…" }`, exactly once, for `seal` only, before the result.
    - bios's link layer takes it out of the stream and passes it straight to the vault's `seal`, and keeps no copy. It never forwards it, logs it, or puts it in an error.
    - Without `expires_at`, bios discards it and the result becomes exit 1.
    - The node never logs it either, and keeps no copy once it's sent.
  - Exactly one `{ "event": "result", "exit": <jerk exit code>, "provider"?, "user"?, "scopes"?, "expires_at"?, "env"?: [names], "message" }`. `list` and `accounts` add `"items": [ … ]` with the same status fields per row.
  - Bytes page → node: none. The page sends END right after OPEN.
- **Jerk outcomes are results, not RESETs.** Exits 1–5 (jerk's exit codes) arrive in `result.exit`, and in-kernel jerk exits with that code.

| RESET | when | in-kernel jerk exits |
|---|---|---|
| `EPROTO` | bad OPEN: unknown op, missing field, unknown param | 1 |
| `EACCES` | the op isn't allowed over J1 (the table above); a remote link lacks `--jerk` or `--jerk-seal`; `seal`/`unseal` came through the kernel facade; or a `seal` after the host's `seal-off` (`EACCES: the host turned off sealing on this slicc`) | 2 |
| `EBUSY` | a `create` is already in flight on this link (at most one at a time) | 1 |
| `ETIMEDOUT` | `create` ran past the provider `timeout` plus 30 s | 3 |
| `ECONNRESET` | host jerk died, or the link dropped | 1 |
| (cap not advertised) | `EOPNOTSUPP` per [§4](#4-hello)'s rule | 4 |

**Node-side details ([slicc-cli#74](https://github.com/ai-ecoverse/slicc-cli/pull/74)):**
- **`params`:** the node can't see a provider's declared params, so it refuses only malformed ones with `EPROTO`: keys must match `^[a-z][a-z0-9_]{0,31}$`, values must be printable, at most 16 params of at most 256 bytes each. Host jerk rejects unknown keys with exit 2, which arrives as `result.exit` 2.
- **`--seven-origin`** is passed only on links set up over CDP ([§1.1](#11-local-setup-over-cdp)), from the seven context's origin. Tray (`follow`) and stdio links run `create` without it.
- **Timeouts:** `create` gets 2 min 30 s (jerk's default provider timeout plus 30 s); other ops get 60 s. A `create` past its time is `RESET ETIMEDOUT`. Any other op past its time gives `result.exit` 1 with "jerk timed out", not a RESET.
- **Lease validation:** a lease without string `credentials` or an RFC 3339 `expires_at` is never sent; the result is exit 1.
- **"Died":** a signal, an exit code above 5, or a failed start gives `ECONNRESET`. Exits 0–5 are results.
- **Status fields are whitelisted:**
  - results keep `provider`, `user`, `scopes`, `expires_at`, `env`, `message` (and `items`);
  - list rows keep `provider`, `name`, `source`, `requires`, `cli`, `user`, `scopes`, `expires_at`, `env`, `id`, as strings, booleans or string lists.
  - Everything else is dropped, including any masked token.

**Host revoke and `seal-off` (node → page):** the host can revoke a lease seven holds and stop new ones, through the node's control socket ([`jerk`](#jerk-slicc-jerk-over-the-link)). Preventing future issuance and revoking what was issued are separate ops.
- **The lease list:** a node that offers `jerk-seal` remembers, in memory and for as long as the process runs, each lease it sent: provider, `lease_id` and `expires_at`, never the credentials.
- **Revoke:** the host's `unseal` runs `FNOX_LEASE_ID=<lease_id> jerk <provider> drop --lease --via-link` on the node, with no link request. Once jerk exits 0, the node tells seven.
- **The notice:** only after the node has sent its HELLO on that link, and to a page whose HELLO lists the page cap `jerk-revoke`, the node sends `OPEN { "kind": "jerk-revoked", "provider": "github", "lease_id": "jerk-github-…", "by": "host" }` on the control channel. It carries no credential, scope or rule text. The page answers OPENED `{}`, then END, and the node sends END.
  - **Delivery:** the node sends each notice once on every link that is up and on each later link of this process whose page lists the cap, until the lease's `expires_at`. A RESET, or no answer within 10 s, is logged (`jerk: revoke notice <provider>: …`) and the notice waits for the next link. Repeats are harmless.
  - **No notice** after seven's own `unseal`, since seven asked.
- **The page's obligation:** the page acts only on a notice from the node that created that lease. Otherwise any linked device with the cap could make seven forget another host's credential. When it seals a lease, the page keeps the per-process `node` id from the HELLO of the link the `lease` event came in on. It forgets the lease only when the HELLO `node` of the link the notice comes in on is that same id, and the `provider` and `lease_id` match. A notice from any other node, or for a `lease_id` it doesn't know, is answered OPENED `{}` and changes nothing.
- **`seal-off`:** every later `seal` on that process is `RESET EACCES: the host turned off sealing on this slicc`, and HELLO leaves out `jerk-seal` from the next link on. A link already up keeps the cap in its HELLO and gets the RESET. Live leases stay, and `unseal` (seven's and the host's) still works. `seal-on` undoes it.
- **A node without the list** (an older slicc, or a process that has since exited) never sends a notice. The host can still run jerk's drop itself, and seven then keeps the dead credential until it expires.

**The agent-vs-user rule:**
- The caller of J1 may be the agent. So J1 only starts things a human must finish (`create`), moves tokens into a store the agent can't read (`seal`), or undoes what jerk minted (`unseal`).
- For `create`, the **sign-in page is the confirmation**. A create that would succeed without a human on a page is refused (`EACCES`).
- `seal` can succeed with no human present, for example when gh is already signed in on the host. That's safe because a seal carries no rules: the token is injected only under rules in seven's root-owned fnox config, and adding one takes `sudo`, whose dialog is the human's approval.
- Ending a provider CLI's own login (`gh auth logout`, `wrangler logout`, `aio logout`) is never possible over J1. The user runs `jerk <provider> drop` on the host.
- A provider whose sign-in needs a terminal reports `exit 5` over J1, with "run `jerk github create` on laptop".

#### 5.15 `ws` (cap `ws`), page → node: WebSockets for kernel programs
From [slicc-bios#360](https://github.com/ai-ecoverse/slicc-bios/pull/360) and [slicc-kernel#215](https://github.com/ai-ecoverse/slicc-kernel/pull/215). bios never opens a browser WebSocket for a program, because the browser would attach the user's cookies. So a program's `ws:` or `wss:` connection always goes out through an exit.

- **OPEN:** `{ "kind": "ws", "url": "wss://host/path", "protocols": ["…"], "headers": [["name","value"], …] }`.
  - **`url`:** `ws:` or `wss:`. A URL on the link's own `<host>.slicc.internal` arrives already rewritten to `127.0.0.1`, as for `http`.
  - **`headers`:** the program's handshake headers, Latin-1, with [§5.3](#53-http-cap-http-page--node)'s header rules.
    - The node drops `Host`, `Connection`, `Upgrade`, `Sec-WebSocket-Key`, `-Version`, `-Extensions` and `-Protocol`, and sends the rest, `Origin` and `Authorization` included.
    - It offers no extensions (no permessage-deflate).
  - **Refused with `RESET EPROTO`:** URLs with credentials, other schemes, a bad OPEN.
- **Dial:** through the same policy-checked dialer and system TLS roots as `http`. **Protected ports ([§6](#6-policy-node)) apply**, so a `ws` stream can never reach a browser's debugging port.
  - **Redirects:** not followed.
  - **Success:** OPENED `{ "protocol": "<chosen or empty>" }`.
  - **Failure:** RESET with `EACCES` (policy), `ECONNREFUSED: HTTP <status>` (a non-101 answer, 3xx included), `ECONNREFUSED: …` (dial, DNS, timeout (30 s) or TLS; for TLS, `ECONNREFUSED: x509: …` naming only the host). bios maps every RESET to 502, so no finer codes are needed.
- **Bytes, both directions:** records of `u8 type`, then a `u32` BE length, then the payload. Records are independent of DATA frame boundaries.
  - **Types:** `1` text (UTF-8), `2` binary, `8` close (`u16` code, then a UTF-8 reason of at most 123 bytes; an empty payload means 1005).
  - **Whole messages only:** the node reassembles fragments, and the page sends whole messages.
  - **Size:** at most 64 MiB per message; over that, the receiver closes with 1009.
  - **Other types:** an unknown type, or a malformed close, makes the receiver close with 1002.
  - **Page-side violations** (1009, 1002): the node tells the page with a close record carrying that code, then END.
  - **Pings:** the node answers the far end's pings itself, and they're never relayed.
- **Closing:**
  - **Page first:** the page sends a close record, then END. The node sends the WebSocket close, relays the far end's close record, then END.
  - **Far end first:** the node echoes the far end's close code at once (RFC 6455), relays its close record, then END. A close record from the page after that is optional and ignored.
  - **No clean close:** END alone, which the page reports as 1006.
- **Flow control:** the stream window ([§3.3](#33-flow-control)), with nothing extra.
- **Privacy:** the node never logs handshake headers or message payloads at any level, and names a stream only by host and path (no query). A program may hold a real value through `fnox exec`.
- **No vault injection, v1:** `ws` streams are never `credentialed`. bios refuses (`EACCES`, "secrets in WebSocket handshakes aren't supported yet") an OPEN whose handshake contains a vault placeholder, rather than sending the placeholder.
- **HELLO:** nodes that implement it add `"ws"` to `caps`. Without it, bios answers the program 502 "…carries no WebSockets; update its slicc CLI".

#### 5.16 The link facade (page side, not on the wire): the in-kernel `slicc`

How a kernel program in seven reaches [§1.4](#14-a-seven-as-follower-leader-to-leader), and this seven's own tray. This isn't a link stream. It's seven's page serving its own kernel. The page holds the WebRTC; the program never sees the hub, the join state or the peer connection.

- **The program:** `@ai-ecoverse/wasi-slicc`, slicc-cli's client command layer built for `GOOS=wasip1` (published by slicc-cli, offered by bios's catalog).
  - **Commands:** `slicc <join-url> prompt|watch|wait|abort`, with the CLI's own flags, output and `--json` events, and `slicc tray` (below).
  - **Anything else** exits 2 with `slicc: <command> isn't available inside seven`. A prompt without a join URL is refused the same way.
  - **How it reaches the facade:** through its host module, at `SLICC_LINK_URL` (below).
- **Where it's served:** a page-served kernel facade with the caller's stamp on each connection, mirroring the CDP facade on `127.0.0.1:9222`. The mechanism is [slicc-kernel#526](https://github.com/ai-ecoverse/slicc-kernel/issues/526), shipped in [slicc-kernel 1.90.0](https://github.com/ai-ecoverse/slicc-kernel/tree/v1.90.0#page-facades) ([#556](https://github.com/ai-ecoverse/slicc-kernel/pull/556)):
  - **The kernel side:** `createKernel({ facades: { link: { port, hook } } })`. An attached client can supply the hook later with `client.serveFacade("link", hook)`.
  - **The stamp:** `hook(stamp, { name })` gets the connector's `{ pid, ppid, uid, gid, user?, groups, users, tag? }`, taken by the kernel at `connect()` from the process's credentials. A program can't forge it.
  - **The port:** reserved from boot, so a program's `bind()` to it fails with `EADDRINUSE`, and kernel-only, so the page's `kernel.dial`, a client's `dial` and `loopbackFetch` are refused with `ECONNREFUSED`.
  - **The limit:** bytes from the page wait in the kernel until the program reads them, up to 16 MiB per connection. Past that, the kernel closes the connection, and the hook's `close(reason)` gets `slicc-kernel: more than 16777216 bytes are waiting for the program to read`.
- **The connection:** a raw byte stream on the kernel's loopback, reached at `SLICC_LINK_URL` (`tcp://127.0.0.1:<port>`). The kernel hands the connection and the connector's stamp to the page's hook, as `SO_PEERCRED` would. The program's host module connects with the imports-module API's `ctx.net.connect` (slicc-kernel 1.92.0 or later).
- **Framing, one connection per [§5.11](#511-agent-page-cap-agent-node--page-remote-control) stream or `tray` request:** records of `u8 type`, a `u32` BE length, then the payload (as [§5.15](#515-ws-cap-ws-page--node-websockets-for-kernel-programs)), in both directions.
  - **`1` control (JSON):**
    - the program's first record is `{ "v": 1, "join": "<join-url>", "open": { "kind": "agent", "op": "prompt" | "watch" | "abort", … } }`. `open` is exactly [§5.11](#511-agent-page-cap-agent-node--page-remote-control)'s OPEN, `length` for long prompts included;
    - or, for this seven's own tray, `{ "v": 1, "open": { "kind": "tray", "op": "status" | "url" | "rotate" | "unlink" | "retry", "id"? } }`, without `join` (below);
    - the page answers with `{ "opened": { … } }` ([§5.11](#511-agent-page-cap-agent-node--page-remote-control)'s OPENED) or `{ "reset": "ERRNO: message" }`. After a `reset`, it closes the connection.
  - **`2` data:** the stream's DATA, prompt bytes up and NDJSON down.
  - **`3` end:** that side's END. The page closes the connection after both ends, or after a `reset`.
  - **After the first exchange, the only control record allowed is a `reset`,** at any point: it's that stream's RESET. Any other later control record gets `reset` `EPROTO`.
  - **Limits:** a control record is at most 64 KiB. A data record is at most 16 MiB, and senders keep them at 64 KiB or less. Any other type, or an oversized or malformed record, gets a `reset` with `EPROTO`.
  - **Ctrl-C inside seven** ends the program with status 130 (the kernel's default), `watch` included. Go on `wasip1` can't catch signals, so `--abort-on-interrupt` isn't offered there.
- **The `tray` kind:** this seven's own tray, the leader's view, with no join URL and no link stream behind it. The page serves it from what its Network port already has (`status().links`, `status().exit`, `rotateJoinUrl`, `unlink(id)`, `retryLinks`). One request per connection: the page answers with one control record, `{ "opened": <result> }` or `{ "reset": "ERRNO: message" }`, sends nothing else, and closes; slicc closes after the answer too.
  - **`status`:** `{ "tray": "idle" | "connecting" | "ready" | "reconnecting" | "failed", "error"?, "hub"?, "exit", "devices": [ … ] }`. `tray` is the tray's state (`idle` when it is off), `error` its last error, `hub` the hub's origin (never the join URL), and `exit` the Network port's current exit (`null`, `{ "kind": "link", "id" }` or `{ "kind": "tailnet", "node" }`). `devices` holds the Network port's devices, live and away, each `{ "id", "name", "kind"?, "host", "address", "mode", "offers", "policy"?, "exit", "state", "access"? }`; a remote device's `id` is its DTLS fingerprint, SHA-256 in lowercase hex without colons, the same id the device's own `slicc` shows. The answer never holds the join URL, `joinCommand`, `followUrl` or the webhook URL.
  - **`url`:** `{ "url": "<join-url>" }`, or `ENOTCONN` when the tray isn't `ready`. It is the only answer that carries the join URL.
  - **`rotate`:** `rotateJoinUrl`; `{}`. The new URL isn't in the answer.
  - **`unlink`:** `unlink(id)` for the device whose `id` equals `"id"` exactly; `{}`, or `ENOENT` when there's none. Matching a prefix is slicc's job.
  - **`retry`:** `retryLinks`; `{}`.
  - **Approval:** the page may ask the user to approve `rotate` and `unlink` first, as `sudo` does; a refusal is `EACCES: …`. slicc waits 5 minutes for those, and 1 minute for the others.
  - **Anything else** (another `op`, a `join` with `kind: "tray"`, a missing `id` for `unlink`) is `EINVAL`. A page without a tray answers every op with `EOPNOTSUPP`.
- **Who may:**
  - **only under kernel users `enforce`;**
  - **only uid 0 and members of `sudo`** (cones), from the stamp. Scoops are refused;
  - **otherwise** `{ "reset": "EACCES: …" }`.
- **Secrets:** a prompt ends at B's model provider.
  - **On the way out:** A's page scrubs every outgoing prompt byte with A's vault scrubber, streaming.
  - **While the vault is locked** (`scrub.ready: false`), the answer is `{ "reset": "EACCES: unlock the vault to continue" }`.
  - **On the way back:** events from B aren't scrubbed on A.
- **Errors:** [§5.11](#511-agent-page-cap-agent-node--page-remote-control)'s, plus:
  - `ENOTCONN`: the tray can't be reached;
  - `EACCES`: B refused (`UNLINKED`, or no *Allow remote control*), with B's message.
- **Not offered:** exits, kernel streams, CDP or anything else on B. Six's `exec` and `list` are parked.

#### 5.17 Reserved
`exec` is reserved (six's leader `exec`, parked in [slicc-cli#102](https://github.com/ai-ecoverse/slicc-cli/issues/102)).

#### 5.18 Native view-only screen sharing

Native screen support is an explicit bundled-app transport; ordinary CLI links remain disabled. The `screen` capability is never offered until a verified, properly signed sliccy.app connects and a local grant is active. Genuine signed-app, exec, descriptor-handoff and running-slice validation are release gates; a merge does not enable sharing without that verified app and its approved bundled-CLI pin. The native sliccy.app owns capture, consent, source policy, the indicator and stop. slicc owns the `screen` effective capability, link streams, node lifetime and authenticated local transport; bios owns seven's consumer. There is no input capability, click/key protocol, Accessibility request or input implementation in this feature. Existing `agent`, `cdp`, `kernel-in` and browser `capture.screen` permissions grant no native capture authority and provide no fallback.

**Verifier diagnostics.** `slicc - follow --native-screen --json --native-screen-proof` opts into content-free observations from the actual native verifier. It uses the same peer policy, deadlines and admission decisions as ordinary sharing, and creates no grant or additional trust role. No diagnostic events are emitted without this option; `--native-screen` and `--json` are both required.

The stdout event is `native-screen-verification`, schema 1, listed in `capabilities.events.names` but emitted only with the diagnostic option. At most one `check` for each accepted/refused outcome on each `safety`/`frames` lane is emitted, followed by one `complete`: at most five rows, with contiguous integer `seq` values starting at 1. Check rows carry `lane`, `outcome` (`accepted` or `refused`), nullable `tokenMatched`, `pidAgreed`, `uidAgreed`, and `lost`. The booleans come from the real kernel reads: `true` requires agreement in both required pre/post reads; `false` means an actual disagreement in either read; `null` means a required comparison did not execute or was unavailable. Short-circuit refusal can therefore record a token mismatch while leaving later comparisons null. No identity values, paths, hashes, recipients, application content or frame bytes are included, and these events are not audit records or wire messages.

Refusal observations remain buffered until native invalidation and socket closure have completed. The collector never waits for stdout and has fixed space for its five rows. Expected association closure does not itself set `lost`; incomplete/abandoned verifier observations do. A successful refusal may end with `complete` and `lost:false`. Consumers must treat missing, duplicate, gapped or lost reports, a missing final `complete`, and null comparisons required for their proof as inconclusive. CLI teardown joins the proof writer after native shutdown and before emitting `closed`, including startup refusal; native stop itself never waits for that writer. Output failure cannot change a verifier decision. An accepted check proves neither completed authentication nor permission to capture.

**Capture boundary.** A human selects eligible apps/windows or one display in sliccy.app. The fixed exclusions are the browser hosting the receiving seven (all its windows, profiles and relevant helpers), sliccy.app, and macOS authorization, TCC and password surfaces/services. System Settings is excluded by default; only the local human may allow it, and its password/permission surfaces remain excluded. Other browsers are eligible. App names, titles, seven's claimed browser, default-browser association and page Origin are not process identity. The app must establish the hosting-browser identity with trustworthy local process evidence and protect newly discovered hosts before any affected output; ambiguous local-host attribution blocks the affected source. A recipient proven to be remote does not imply that an unrelated local browser is a host. Source policy changes or recipient changes require local confirmation, never a remote choice.

The promise is to exclude protected **surfaces**, not to identify the provenance of every displayed pixel. An allowed app can display copied screenshots/text from a protected app. An app the user allows, such as Terminal, can automate protected apps if macOS lets it. This contract does not prevent that through other capabilities or existing local automation. It imposes no forced application allowlist or automation-proxy ban. Capture safety itself must still be provable through public macOS APIs: unresolved service-hosted sheets, system thumbnails/notifications, translucent composition, menus, child windows or transition races block the affected source; if the unsafe region cannot be bounded reliably, suppress all output. Passing a selected-window test does not qualify display capture.

The app's ScreenCaptureKit filter builder starts with an empty `exceptingWindows` list and asserts that it stays empty. In the display application-inclusion initializer, exceptions can add an excluded application's window; protected windows must never be placed there as purported extra exclusions. The included set is the user's selection minus protected/unknown surfaces, not a fixed product allowlist. Child-window capture is explicitly configured and tested with sheets. Window scope never silently becomes display scope. Cursor effects, audio, incidental JPEG metadata and remote whole-Mac/window inventories are absent. Source handles are opaque, session-local values, never OS window IDs, titles or paths. Every source or protection change ends the view session: first close the output gate and discard old captured/encoded/queued buffers. Advance the source/protection epochs, apply and verify the new filter, then require a fresh locally approved session before admitting fresh frames. Notifications alone do not prove invalidation precedes compositor visibility. An unprovable transition keeps that scope unavailable.

**Lifetime and stop.** Off by default; no headless setting or persisted permission enables it. Only local confirmation in the authenticated app creates a grant for the exact live app/CLI pair, link instance, recipient and source. One controller/view session per Mac. Grant admission and cross-child arbitration are app-wide. Resume is local and manual; reconnect, wake, unlock, restart or a recovered verifier never resumes sharing. A fresh session and fresh local consent are required after stop. A window close alone does not quit the menu-bar app; its UI says when sharing continues.

The native visible indicator and registered Control-Option-Command-Escape stop shortcut must be healthy before capture activates, including with the main window closed/full-screen Spaces. The menu-bar stop also remains available. Stop first atomically invalidates the app grant/output gate and discards buffers; it never waits for remote acknowledgements, an encoder, frame/control queues, MainActor work or audit I/O. slicc independently removes its effective cap before RESET/state/audit work. No post-stop parser completion, delayed state or HELLO restores it. Already transmitted pixels cannot be recalled.

Stop/revoke on source death, unresolved source/Space/display change, lock, sleep, session resignation, permission/indicator/shortcut/safety-monitor/audit failure, peer identity change, IPC EOF, app/CLI death, link replacement/down, or WebRTC disconnected/failed/closed. Native safety does not wait for WebRTC reconnection grace. Each side sends safety heartbeats every 500 ms; local monotonic expiry is 1.5 s after the last valid current-generation heartbeat. Frame traffic and partial bytes never renew the lease. On process scheduling after suspension, check expiry before any capture/forward/renewal; expired sessions remain disabled. A frozen process cannot execute stop: the 1.5 s lease is a liveness bound subject to scheduling, not the local-stop target. Measure native callback-to-invalidation maximum/p99 with a target <=50 ms under supported load, and healthy-transport RESET notification target <=250 ms, separately from lease failure latency.

**Negotiation and live capability.** Outer link framing stays v1. Native protocol is `screen.v=1`. An eligible Darwin bundled child configured for private native transport before linking may include this support-only HELLO metadata:

```json
{"screen":{"v":1,"scopes":["window","display"],"codec":"jpeg","limits":{"headerBytes":4096,"frameBytes":2097152,"longEdge":2560,"pixels":4000000,"defaultFps":5,"maxFps":10,"bytesPerSecond":8388608}}}
```

Scope metadata describes protocol support; the app must keep each source unavailable until its capture safety is proved. HELLO's static `caps` never contains `screen`; ordinary CLI runs and Linux/Windows/wasm omit the metadata too. No grant/session/epoch from HELLO is trusted. Each new link starts with effective screen caps `[]`. The node sends its initial `screen-state` only after sending its own HELLO and receiving the page HELLO. The page must advertise page cap `screen-state`; otherwise no grant and any `screen` OPEN is `EACCES`. An old page cannot receive pixels. A node incorrectly listing `screen` in HELLO caps is incompatible with this extension; bios does not open screen streams on that link. The extension authorizes `screen`, `screen-stop` and `screen-state` handling by the support metadata and negotiated page cap, without changing static gates for other kinds.

A full `screen-state` replacement has `caps` exactly `[]` or `["screen"]`; duplicate/other capabilities are `EPROTO`, clear viewing and never affect net/http/fs/cdp/credentialed or other link grants. `["screen"]` is the effective capability only while the authenticated native grant is active. On stop both brokers remove it immediately; delivery of the replacement is best effort. Support is not a grant. Limits use the minimum of node support, app grant, page implementation and OPEN request ceilings.

**JSON and identity rules.** All new records reject invalid UTF-8, duplicate keys at any nesting depth, trailing data, excessive nesting (>8) and every unknown field before use. v1 has no extension namespace; adding fields requires a negotiated protocol revision. u64 counters are JSON decimal strings matching `^(0|[1-9][0-9]{0,19})$`, additionally <=18446744073709551615; reject signs, exponents, fractions, leading zeros and overflow without wrapping. Go/Swift use checked unsigned arithmetic and JS compares BigInt, never Number. Small bounded sizes, fps and dimensions are JSON integers in their stated ranges. Binary lengths and existing outer stream IDs are unsigned big-endian integers; validate before allocation/addition. Reaching a counter limit revokes before increment and requires a fresh session/link as appropriate.

`link` is a fresh 128-bit random unpadded base64url ID per actual link instance, unrelated to hostname, persistent certificate or HELLO node ID. `session` and `source` are app-issued random 128-bit unpadded base64url IDs. `grantEpoch`, `sourceEpoch` and `protectionEpoch` are app-maintained u64 counters; together with `link`, `session` and `source` they form the required `binding` on active state, OPEN, OPENED and every frame. Both app and CLI compare it to their own live authority; these values are identifiers, not bearer tokens. Any protection/source revision invalidates pending old-binding records before accepting new ones. No old record may finish into output under a new binding.

`frameSeq` starts at "1" per session and strictly increases across all frames and stream reopenings, with gaps permitted for discarded frames. Stream/channel failure never resets it. A source or protection change ends the session and requires a fresh local grant with a new session ID; `frameSeq` restarts at "1" in that new session. `stateSeq` starts at "1" per link, increases across every replacement including disabled states, and outlives session/stream changes. A duplicate or lower accepted sequence is `ESTALE`; an old link/session cannot create new state. Retain the last stopped session on the same live link solely for idempotent stop; it confers no permission.

**Stream kinds.** OPEN and OPENED JSON <=4096 bytes for screen/stop and <=65536 for state. Native screen/stop OPENs complete within 3 s; state application has the 10 s bound below. Other kinds on a native-enabled link retain a separate 30 s OPEN deadline. Errors use fixed content-free reasons.

| Kind and direction | OPEN | OPENED, DATA and close |
|---|---|---|
| `screen`, page → node | `{kind:"screen",v:1,binding,maxFps,maxWidth,maxHeight}`; defaults 5 fps, width/height 2560, all requested values positive and within hard ceilings | Node validates the current grant, negotiates smaller output bounds, and answers `{v:1,binding,codec:"jpeg",maxWidth,maxHeight,maxFps}`. Each frame must fit maxWidth/maxHeight or the consumer resets `EPROTO`; source windows may resize within those bounds. Page sends END immediately and never DATA. Node sends frame records until RESET. One screen stream per session; a duplicate is `EBUSY`. |
| `screen-stop`, page → node | `{kind:"screen-stop",v:1,link,session}` on control channel; allowed with support metadata even when already disabled | CLI invalidates locally first and tells the app, including when disabled or grant admission is pending. `{v:1,stopped:true}` only after acknowledgement of native invalidation issued after this Stop began; then node END, page END; no DATA. Idempotent for the current/last stopped session on this link. `ESTALE` for another session/link. App unavailable/unconfirmed within 1.5 s: `ECONNRESET`, with CLI still revoked and app lease expiring independently. |
| `screen-state`, node → page | `{kind:"screen-state",v:1,stateSeq,caps,binding?,scope?,reason,limits?}` on control channel; `scope` is `window` or `display`; binding/scope/limits required when active; disabled form specified below | Full replacement, no DATA. Active limits have exactly the HELLO limits shape and each value <= its HELLO ceiling, else `EPROTO`; page uses the smaller limits. Page validates/applies before OPENED `{}`, then page END and node END. One update in flight, coalesce newest replacement, <=5 updates/s (burst 1); revocation bypasses coalescing locally. Completion >10 s: `ETIMEDOUT` and revoke because reliable state display is lost. No state ack gates native stop. |

Active state has exactly `{kind:"screen-state",v:1,stateSeq,caps:["screen"],binding,scope,reason:"active",limits}`. Disabled state has exactly `{kind:"screen-state",v:1,stateSeq,caps:[],link,lastSession,reason}`: `link` is the current live link ID and `lastSession` is null before the first grant, otherwise the last issued session on this link. Disabled state never includes binding/source/epochs/scope/limits; `lastSession` is for idempotent stop only. `stateSeq` belongs to the link and continues through every disabled/active transition. An initial screen-stop uses `{kind:"screen-stop",v:1,link,session:null}` and is idempotent only while lastSession is null; later stop uses the current/last session ID. Null never acts as a wildcard. Disabled-state link must equal every earlier accepted state's link ID on this actual link, else `EPROTO`; seven clears buffers and retains no epochs from disabled state. Seven sends Stop only after accepting at least one state, using that state's link and active session or lastSession (possibly null), never inventing either. If a fresh local grant races the click, `ESTALE` leaves that new grant active and seven shows its new active state with Stop available.

`reason` is a closed enum: `active`, `awaiting-consent`, `paused`, `stopped`, `source-changed`, `protection`, `permission`, `indicator`, `shortcut`, `audit`, `peer-untrusted`, `peer-lost`, `link-lost`, `lease-expired`, `stalled`, `resource-limit` or `incompatible`. `reason` is `active` if and only if caps is `["screen"]`; `active` with `[]` or any other reason with `["screen"]` is `EPROTO`. All other known reasons require `[]`. Seven maps each reason to its own fixed display text and never renders the raw enum value; unknown reason on active state is `EPROTO`, while unknown reason on disabled state displays stopped and remains ungranted. No caller-supplied reason text is rendered.

Stop resets active screen streams `EACCES` after local invalidation, then sends disabled state best effort. Stale binding is `ESTALE`, unavailable native peer/link `ECONNRESET`, malformed JSON/record `EPROTO`, oversized input `EMSGSIZE`, partial/stalled record `ETIMEDOUT`. Unsupported native kind is `EOPNOTSUPP`. Page RESET ends the current subscription only; it cannot grant, resume or choose a source. It may reopen under the same still-active grant; counters continue. Any abnormal native IPC loss ends the grant, not just the subscription.

Node sends a new state before emitting any new-binding frames. Different channels can reorder them: seven displays only a frame whose full binding matches the latest active accepted state, dropping an unmatched complete frame without treating it as new authority. If mismatch persists for 2 s it resets the subscription `ETIMEDOUT` and shows stalled. Seven clears pixels and frees buffers on whichever arrives first: local Stop click, `RESET EACCES`, disabled state or link close. Remote Stop UI clears without waiting for OPENED. It never sends enable/resume/source-choice operations.

**Frame records and backpressure.** Inner records span outer DATA boundaries:

| Bytes | Value |
|---|---|
| 4 | JSON-header length, u32 BE, 1..4096 |
| header length | UTF-8 JSON `{type:"frame",v:1,binding,frameSeq,capturedMonoMs,width,height,codec:"jpeg"}` |
| 4 | JPEG length, u32 BE, 1..2097152 |
| JPEG length | One independent JPEG, no EXIF/comment/title/path metadata |

`capturedMonoMs` is a decimal u64 string of the app's monotonic milliseconds, useful for local freshness, not remote clock authority. No input coordinates, observation/action handles or global geometry are needed. Width/height are positive, longest edge <=2560 and product <=4,000,000. Validate JPEG SOF dimensions before decode/allocation, require FFD8..FFD9 framing, and require decoded dimensions equal the header. Encoded pixels are orientation-normalized; seven preserves aspect ratio and never derives authority from dimensions. No audio or cursor is included.

Existing DATA remains 1..65536 bytes with 256 KiB directional credit. Credit each chunk after copying into a reserved bounded record buffer, not after a complete 2 MiB JPEG; keep draining every channel. An admitted record has a 2 s absolute deadline from its first length byte, not a slow-drip-reset idle timer. END inside a record is `EPROTO`. An incomplete timed-out record resets the stream. Slicc must also finish forwarding a begun record within 2 s or reset; no credit/no progress for 2 s resets subscription and shows stalled. Native capture gate rejects frames older than 500 ms at initial release; slicc rejects a frame held >500 ms before starting transmission. Already begun reliable records cannot be selectively recalled, only RESET.

Size each JPEG to finish in about 1 s at the measured available link throughput, leaving margin inside the 2 s absolute deadline; reduce dimensions/quality/fps for slow relay links rather than repeatedly sending 2 MiB records that cannot finish.

Independent ceilings at app and node: default <=5 fps, hard <=10 fps, token bucket burst 1 frame; JPEG bytes <=8 MiB/s (8,388,608 bytes/s), token bucket burst <=2 MiB. Count encoded JPEG bytes, excluding bounded headers. Full-sized frames cannot sustain 10 fps. Lower fps/quality or discard whole unsent frames to meet all ceilings. Seven may discard superseded complete frames rather than RESET merely for presentation rate; app/node enforce the sender ceilings.

App holds at most one frame encoding and one latest pending safe frame; slicc one streaming record and one latest unsent record; seven one partial encoded record and one decoded presentation. Each encoded slot is <=2,101,256 bytes including lengths/header. Each raw/decoded RGBA slot <=16,000,000 bytes. App and CLI bound their row padding/conversion/codec working memory to 64 MiB per stage in total; budget failure suppresses/stops. Seven bounds its own one encoded slot and one decoded bitmap; page JavaScript cannot cap the browser JPEG decoder's internal working memory, so this is not a claim to cap total browser memory. Drop superseded whole pending frames and stop pulling source buffers under pressure. Never truncate a started record except by RESET. Retained buffers of an old binding are discarded at every stage. No captured content goes on lifecycle stdout, debug logs, telemetry or audit.

**Association-wide admission.** Native screen has tight sublimits: one active image stream/channel, one outstanding state and one stop request; screen OPEN attempts <=10/s burst 10; state <=5/s burst 1; one bounded partial image record plus one latest unsent frame; all image/safety lengths/rates/deadlines above apply. Bulk screen DATA always uses its own channel. Separate SCTP channels still share resources and provide no scheduler isolation.

All peer-initiated kinds additionally have association-wide admission limits before expensive parser/allocation/logging work: live channels, streams, outstanding OPENs, OPEN/s, CREDIT/s and other control/s. HTTP/CDP allocation reservations below are independent of these count and rate limits. Quotas are set to at least 4x measured CI per-session peaks, with more headroom where measurements require it; no invented small limits may break existing exit/CDP/fs traffic. CREDIT has its own bounded CPU bucket, sized above measured valid credit traffic, including screen at 8 MiB/s; byte-accounting validation alone does not bound tiny-credit CPU cost. Frame-window/length validation remains mandatory. Per-peer buckets include all channels, not a separate allowance for every new channel.

Admission measurements used 138 association records across four CI runs. Ordinary link/remote/CDP and 64 simultaneous exit fetches plus a small pnpm install gave maxima: page/node channels 65/2, streams 64/1, pending OPENs 8/1, OPEN/s 66/4, CREDIT/s 1524 in the busier direction, other control/s 128. Mounts/fs-notice opened no links. These workload maxima are not guarantees of all possible workloads.

| Association resource | Agreed quota |
|---|---|
| Non-control channels by initiator | page 260, node 16; plus one shared control channel |
| Live streams by initiator | page 256, node 16 |
| Pending OPENs by initiator | page 32, node 8 |
| OPEN attempts per direction | page 512/s burst 512, node 32/s burst 32 |
| CREDIT per direction | 8192/s burst 8192, separate from other control; no minimum credit size (legitimate minimum measured 1 byte) |
| Other control per direction | 1024/s burst 1024; OPEN/OPENED/END/RESET, with OPEN also charged to its own bucket |
| Stream-window floor | (256+16) × 256 KiB = 68 MiB; not a total memory ceiling |
| CLI SCTP receive storage | 4 MiB; separate from application buffers |
| Existing HTTP header limit | 1 MiB per stream |
| Existing CDP buffers | 64 MiB per message and parked-command queue; separate from stream windows |
| Shared HTTP/CDP raw-buffer reservations | 512 MiB per CLI process across links; includes HTTP request-head buffers, CDP browser/message buffers, parked commands, guarded holds and outgoing framing |

Both initiator groups can coexist, so the window floor sums both quotas. A receiver must accept bytes inside credit already granted. The separate 512 MiB shared reservation covers raw HTTP request-head and CDP relay buffers before allocation or growth. HTTP reserves 1 MiB before OPENED, releasing after parsing. Each CDP browser reader initially reserves 4 KiB before dialing/OPENED and reserves its next capacity without waiting before every growth allocation. Growth copies while both reservations remain held, then releases the old one. Its capacity doubles up to 64 MiB + 1 byte, shrinking back to the 4 KiB step for the next message. Exhaustion resets only that relay with `ENOBUFS`, including during bootstrap/version reads; idle relays do not reserve their maximum possible message size. Page messages reserve their declared length before reading the body and returning CREDIT. Parked commands, browser messages retained by guard holds and outgoing length-prefixed copies reserve their retained capacity separately. Stage 1 debugger script copies, retained script IDs and held pause data also reserve before retention, while `SLICC_CDP_DEBUGGER_BYTES` keeps its existing per-guard ceiling of 64 MiB as an inner limit. Replacements, debugger disable, document invalidation and relay termination release those reservations. Debugger publication pins its retained input through forwarding and reserves the complete batch before allocating the outgoing frame buffer; any shared-budget failure resets the relay immediately without publishing unchecked content. The default budget is shared across every link in this CLI process; an embedded caller may supply a smaller budget, capped at the same maximum.

New HTTP/CDP streams that cannot reserve are reset with existing `ENOBUFS`. An existing CDP page parser waits only in its handler worker before consuming further bytes, so the peer eventually exhausts its unchanged 256 KiB window. Reset/link loss cancels that wait. Guard holds never wait: reservation exhaustion resets that relay, without forwarding unchecked content or pretending delivery. Parked-command and outgoing-frame exhaustion also resets the relay with `ENOBUFS`. Reservations follow held messages through release/forwarding and are returned on completion, reset, bootstrap/dial failure and other errors. No allocation wait runs in the link read loop or control-channel dispatcher, and granted credit is never reduced.

This is a raw-buffer bound, not a node-wide memory ceiling: transport windows/SCTP storage, HTTP body prebuffers, HTTP transport/TLS/decompression, decoded JSON and guard metadata/serialization working memory, Go runtime overhead and garbage awaiting collection are additional. Screen retains its own tight slots and does not acquire or consume this reservation; its 68 MiB stream-window floor above is unchanged and is not counted again in the HTTP/CDP budget. Safety/stop uses an independent local lane and invalidates authority without waiting for their queues, remote credit, audit I/O or acknowledgements. Regression coverage holds a nearly full 1 MiB HTTP header and 64 MiB CDP message open while revoking screen and preserving both streams, and concurrent HTTP/CDP tests admit 32 idle CDP relays across two links plus a large browser reply, exhaust a shared budget, check refusal/backpressure and verify zero reservations after completion/reset/error/link loss. These checks establish scheduling independence under synthetic buffer load, not a hard real-time guarantee under system-wide memory exhaustion.

Quota breach sheds screen first and refuses a new OPEN once with `EBUSY` if an OPEN is being admitted. It never resets an existing exit, CDP or other stream and never closes the association merely for quota exhaustion. Rate-limit admission/refusal work, coalesce one first-violation audit entry and drop further screen work rather than log every rejected frame. Existing outer-v1 malformed protocol rules remain distinct from quota policy. Refusals are fixed-string, constant-time EBUSY responses; audit only the first quota event until connection end. More than 32 refused OPENs/s sustained over a 2 s window (not one isolated burst) is the sole quota-based exception: close the link so every opener gets a reply or link close. Status line and JSON link-lost reason are exactly `seven opened streams faster than slicc allows; closed the link`. Below that sustained-abuse threshold, screen is shed and existing streams remain intact. Account/refuse excess OPENs without expensive JSON/signature/logging or retaining a refusal queue; the 32/s value is the sustained-abuse threshold, not a rule allowing unanswered OPENs. CREDIT arithmetic remains checked for every admitted frame; exceeding its rate bucket suppresses screen and defers new admissions, never discards valid existing credit or resets an existing stream. The bounded constant-time framing/accounting path keeps reading; no expensive JSON/signature/audit work is scheduled above admission budgets.

The node requires page HELLO <=64 KiB within 3 s of control-channel open; bios retains its existing 10 s wait for the node HELLO, which has the same size cap. Native OPENs complete within 3 s except the 10 s state-application deadline. Every partial record has the earlier absolute deadline; timeout cleanup releases all reservations. Keep draining channels while enforcing bounded admission; an unread SCTP stream can stall the association.

**App-to-slicc transport and live identity.** Existing same-uid public control-socket `status`/`stop`/`detach`/`revoke` behavior is preserved. No new grant/frame upgrade is admitted there. The transport uses app-owned private listeners and two child-created AF_UNIX stream connections after exec, one safety lane and one image lane. Inheriting a socketpair alone is not an identity proof: `LOCAL_PEERTOKEN` can reflect mutable last-owner attribution. Private bootstrap is inherited, rather than the connected data sockets.

App creates a fresh 0700 same-owner non-symlink directory and 0600 listeners. Launch uses only the bundled approved CLI, a scrubbed environment with no DYLD injection/Go debug preload overrides, and fixed safe descriptor actions. Only bootstrap read FD 3 and intended stdio are inherited; retained lanes are close-on-exec. Nothing secret goes in argv/environment/stdout; join secrets remain separate on stdin/in memory. The 3 s total launch window begins immediately before the app invokes posix_spawn; it includes spawn, bootstrap, both authenticated handshakes and pairing. Each accepted connection additionally has a 3 s deadline, never extending the total launch window. No launch-window timer is renewed by bytes.

Bootstrap and pre-auth records are `u32 BE JSON-length` then exactly that many UTF-8 JSON bytes, length 1..4096, with no body/line terminator. One bootstrap record is `{v:1,launch,safetyPath,framesPath,challenge,parentAuditToken}`. `launch` and `challenge` are independent 256-bit random unpadded base64url values (43 characters). Paths are absolute UTF-8, <=103 bytes each, in that launch's private directory; they confer no trust. `parentAuditToken` is the app's eight audit_token_t u32 words serialized in index order, each word big-endian, then unpadded base64url of the resulting 32 bytes (43 characters). It is an expected identity to compare with the independently kernel-obtained peer, never authentication by assertion. Close the pipe after one record, reject extra/truncated bootstrap data, never log any field. The app tracks/reaps the exact child without identity reuse; the child checks parent lifetime/incarnation before activating or forwarding.

Both lanes obtain kernel LOCAL_PEERCRED/LOCAL_PEERPID/LOCAL_PEERTOKEN and verify the exact expected peer incarnation and pinned live signing identity before accepting any pre-auth message as authoritative. Auth sequence is strictly:

1. Child creates/connects safety socket after exec and sends `{v:1,lane:"safety",launch,challenge,clientNonce}`. Child nonce is a fresh independent 256-bit random base64url value.
2. App independently authenticates that exact launched child, generates `connection` and `serverNonce` as independent 256-bit random base64url values, and replies `{v:1,lane:"safety",launch,challenge,clientNonce,serverNonce,connection}`. Child independently authenticates the expected app and validates all echoes before proceeding.
3. Child creates/connects frames socket after exec, with a fresh separate nonce, and sends `{v:1,lane:"frames",launch,challenge,clientNonce,connection}`. App verifies the same child incarnation, safety binding and connection, then replies `{v:1,lane:"frames",launch,challenge,clientNonce,serverNonce,connection}` with another fresh server nonce. Child independently verifies the app on this lane and all echoes. Neither lane accepts grant/image records before pairing.
4. Child sends first authenticated safety heartbeat. App's first heartbeat echoes it, proving the frames response was received; pairing/lease-ready completes only after the child receives that echo. Native grant admission waits for this ready state at both ends. App closes/unlinks listeners after pairing. Permit <=4 concurrent pre-auth accepts and 8 attempts total in the launch window; unauthenticated accepts consume no provider slot, but exhaustion fails the launch. No reconnect using old bootstrap material.

A PID followed by a disk lookup, process name, bundle string or JSON audit token is insufficient. Verify live audit-token guest identity at authentication, grant activation and before admitted records; monitor lifetime/exec and fail on missing/changed token, verifier error, peer exit or PID reuse. Native monitoring must close the observation race; periodic checking alone is insufficient. Do not accept a second genuinely signed copy in place of the expected child. Use recvmsg to reject SCM_RIGHTS and all other ancillary data, close received descriptors, reject truncated control data, and forbid descriptor handoff/duplication to other processes. These are trusted-program restrictions, not kernel proof that every subsequent writer is the same process. AF_UNIX mutable last-owner semantics and handoff/exec races remain explicit native/security proof obligations. Failure leaves screen unavailable, never a uid-only/path fallback; a different signed IPC architecture would need separate review.

After authentication both lanes use the frame-record envelope. Each safety record has JSON <=4096, body length zero, a 1 s absolute partial-record deadline and the common fields `{type,v:1,launch,connection}`. Each direction is bounded at 16 records/s burst 16 and 64 KiB/s burst 64 KiB. The following table lists the complete additional fields; no other fields are accepted. `B` below denotes the full binding already defined, `R` a fresh 128-bit random base64url request ID, and reason one of the closed enum values. All counters use canonical decimal u64 strings.

| type | Direction | Additional fields and legal state |
|---|---|---|
| `heartbeat` | Either | `{seq,echo}`. `seq` starts "1" and strictly increases per direction/connection; `echo` is "0" until one peer heartbeat was accepted, otherwise the highest accepted peer seq. Reject nonmonotonic seq, decreasing echo or echo beyond the largest locally sent seq. Only after both sockets authenticate; valid in paired-disabled, link-ready, grant-pending, active and revoked states. |
| `link` | CLI → app | `{link,recipient,browserEvidence}`. Only paired-disabled or revoked; never replaces a current active/pending link. Recipient is `{fingerprint,setup}` with fingerprint the verified page certificate SHA-256 as 64 lowercase hex characters and setup exactly `cdp`, `loopback` or `hub`; it denotes the actual peer of this exact link, not a remote claim. Evidence grammar below. The CLI snapshots the exact live link, opaque id and recipient fingerprint together; stale queued registrations are discarded. Grants wait for completion of that current registration, and replacement during an already-started native write closes the native association. Establishes link-ready, always ungranted. |
| `grant` | App → CLI | `{request:R,binding:B,scope,limits}`. Only link-ready after local consent/audit intent and ready lease; `B.link` must be current link. One outstanding grant. App gate remains closed. CLI verifies all local/live limits/identity and reserves the disabled session, entering grant-pending. |
| `granted` | CLI → app | `{request:R,binding:B}` exactly matching pending grant. CLI queues the new active screen-state before this reply and enters active. App validates matching request/binding, rechecks gate/identity/source/lease, then opens capture output and enters active; images never precede this. Late reply after invalidation is discarded and never reopens the gate. |
| `revoke` | Either | `{request:R,link,session,reason}`. `link` is current link or null before link-ready; `session` is current/last session or null before any session. Sender invalidates first in any paired state. Receiver validates current identifiers (null is never wildcard), invalidates before replying, discards all image/grant work, enters revoked. One pending revoke per direction; crossing revokes are both answered. |
| `revoked` | Either, replying to peer revoke | `{request:R,link,session}` exactly echoing that revoke. Confirms prior local invalidation only; sender must already be revoked. Duplicates with the same known request are idempotent; unknown IDs or mismatched identifiers are `EPROTO`. No ack restores a grant. |

Heartbeat lease is renewed only on a fresh, complete, valid current-connection heartbeat, never on grant/link/revoke/ack/image records or partial bytes. Initial readiness has a 1.5 s absolute deadline after child frames authentication, still within the total 3 s launch window. Thereafter each side revokes when 1.5 s elapses since its last valid heartbeat; require the peer's echoed sequence to make progress within 1.5 s whenever locally sent heartbeats are outstanding, so a one-way-stalled connection cannot remain healthy. Frames/control already queued before process suspension cannot revive an expired lease. Revoke confirmation deadline is 1.5 s, independent of frame queues; timeout closes both native lanes while remaining revoked. Until confirmation, no new grant is admitted; repeated revoke requests and continued heartbeats never extend this deadline. Failed grant admission sends revoke with its request-independent fixed reason, never images. On CLI link replacement/down, invalidate first, revoke the old link/session and only register a new link after old native revocation confirms; otherwise close/relaunch disabled.

`browserEvidence` is exactly `{location,provenance,hosts}`. `location` is `local`, `remote` or `unknown`; `provenance` is `cdp-owner`, `loopback-owner`, `native-verified` or `unresolved`. `hosts` is an array of at most 8 `{auditToken,bundleIdentifier}` entries; auditToken has the bootstrap token encoding and bundleIdentifier is bounded ASCII <=255 bytes. `local` requires >=1 host and non-unresolved provenance; `remote` requires native-verified evidence and no local hosts; `unknown` requires unresolved provenance and an empty list. No page-provided PID/browser name/Origin is verified host evidence; setup hub alone does not prove remote location. Native app independently resolves every claimed host to live process/signature/helpers, accumulates verified host exclusions and makes the source-safety decision. Uncertainty does not clear a browser. A host-evidence change while active revokes before a new link/evidence registration; no silent widening or stale signature snapshot. These private identities never appear in wire state/audit.

Image lane accepts only frame records with the same envelope and current launch/connection/binding. Its JSON header adds `launch` and `connection` to the wire frame fields; slicc validates/removes these private fields before forwarding. Its partial-record deadline is 2 s absolute; frame queues/rates match the wire. Lane EOF/error, lease expiry or malformed admitted record revokes both lanes/grant; safety never waits behind an image record. No frame or byte-progress event renews safety health.

**Signing and cgo.** Reuse the Developer ID Application, Team ID and notarization pipeline of ai-ecoverse/slicc through that repository's existing secret workflow, owned by app/release packaging. Approved identifiers are app `ai.sliccy.mac`, CLI `ai.sliccy.slicc`, and Team `S8LB56P782`. The CLI release already signs with that identifier; no identifier migration is required. Each side pins the Apple anchor, approved Team and other side's identifier in its designated requirement. Pin Apple Developer ID trust, the approved Team and role identifier in designated requirements, validate the running guest strictly and enforce hardened runtime/no `get-task-allow` or injection entitlements. Ad-hoc/current prototype bundles fail. Notarization/install assessment does not substitute for running-code verification.

slicc stays `CGO_ENABLED=0`. Approved Darwin-only implementation calls public Security.framework/CoreFoundation through purego pinned to an exact dependency version, using `SecCodeCopyGuestWithAttributes` with `kSecGuestAttributeAudit`, then `SecCodeCheckValidity` with pinned requirements, checked ABI/status/CF ownership. Prove the bridge first in a throwaway Darwin test and then in the PR. A load error, missing symbol, failed framework call or identity ambiguity is untrusted: no pixels or grants. This is approved implementation direction, not demonstrated support. Shelling out to `codesign` on a path cannot replace it. If infeasible, screen remains absent until a separately reviewed signed verifier alternative is approved; Linux/Windows/wasm never gain native framework dependencies.

The app trusts only its bundled CLI release, already pinned by release checksum. At app build, derive and compile the exact approved CodeDirectory hash allowlist for the selected native executable slice(s) of that checksum-verified signed CLI. At runtime, require the exact launched child's audit-token guest to pass the designated requirement and running-code validity, and its validated code identity to match that compiled cdhash pin. This exact-artifact pin is the app's approved CLI floor; no CLI version extraction or execution of --version is needed. Keep CLI packaging `-s -w`, `-trimpath`, `-buildvcs=false`, CGO_ENABLED=0 and rcodesign unchanged. Never infer a version from current Go build metadata: the pinned v1.55.0 fixture has main module `(devel)` and trimpath omits -ldflags.

slicc carries a compiled-in minimum app version, raised in releases fixing a security problem. Read `CFBundleShortVersionString` from the exact audit-token guest's secured Info.plist via SecCodeCopySigningInformation with signing-information flags; require canonical ASCII major.minor.patch, each component 0..2147483647 without leading zeros, suffixes or overflow, and compare checked numeric triples. Missing/ambiguous metadata is untrusted; the minimum signed app version is `0.1.0`. A separately executed program, IPC version claim, strings scan or unbound adjacent file is never a source.

SecCodeCopySigningInformation on a dynamic guest can contain disk-derived information, and kSecCodeInfoUnique identifies static code; calling it twice is not independent proof of a kernel-observed running hash. SecCodeCheckValidity must establish running-host/disk consistency for the exact audit-token guest, with selected native architecture, strict static/signature validation and before/after incarnation/exec checks. Demonstrate that replacement/mutation/PID reuse cannot substitute a different approved artifact or metadata. If exact live binding cannot be established, no sharing. Reject translated/unsupported slices unless their exact execution/identity binding has been proved.

Existing verified downloads/atomic install preserve update integrity; revocation uses npm deprecation for the CLI and app updates for the app. Deprecation does not invalidate an offline/running copy; installed app pins and CLI minimums enforce their own peer policy. No signed monotonic revision, persisted floor, new key-rotation system or symbol-table packaging change is introduced. Neither signatures nor hardened runtime isolate user-owned files/configuration or previously authorized automation.

**Offline capabilities.** Extend the side-effect-free `capabilities --json` schema/contract 1 with an additive `screen` entry. Existing command/events/agent/control entries keep their meaning; public `control.ops` gains no native grant operations. A binary with the reviewed Darwin implementation reports `screen:{v:1,available:true,transport:"private-child-unix-v1",peer:"audit-token-designated-requirement",lanes:["safety","frames"],kinds:["screen","screen-stop","screen-state"],pageCap:"screen-state",dynamicCaps:true,codecs:["jpeg"],scopes:[...],limits:{...}}`, with the exact limits defined above and named protocol/operation discovery rather than semver inference. `available` describes compiled support, never permission or current peer eligibility; the command probes no app, TCC, socket, file or network. Unsupported builds report `screen:{v:1,available:false}` and no native operations. The running child's status additionally reports `screen:{v:1,available,providerAuthenticated,granted,reason}`, no challenge/paths/recipient secrets; app checks both compiled support and authenticated actual-child state. `available:true` reports compiled transport support only; it does not qualify any capture source or bypass native peer trust.

**Audit and retention.** Local audit covers admitted consent/grant/policy/source changes, pause/stop, lifecycle loss, fixed broker refusals and audit-health failures. Admission occurs before expensive parsing/logging; on abuse record one fixed first-violation reason, terminate, and make no claim to log every packet/refusal under flood. Audit records have minute-rounded wall time, event category, fixed reason, scope kind and random local session correlation only. No per-frame records, exact timing/latency, coordinates, app/target names, dimensions/lengths, image hashes, content, window titles, URLs, page fingerprints, join/IPC secrets, key/text data or clipboard. Test-only latency measurements use synthetic metadata separately from production audit.

App journal: `~/Library/Application Support/ai.sliccy.mac/Audit/`; CLI broker journal: its private config `audit/`. Directories 0700, files 0600, no symlink-following. Both participate in one combined budget: delete on age >=30 days or before total would exceed 100 MiB, max 10 MiB/file. Reserve/coordinate quota before append; writer/rotation/quota failure leaves grant off or revokes it. App writes consent/grant intent before opening the output gate and records result; unresolved intent after crash means unknown, never restored. The native CLI provider and the app sweep both journals under the shared lock at startup and every five minutes while running; the app also sweeps after wake, including broker files while the CLI is stopped. Cleanup does not require sharing or a new event and writes no audit row. A busy quota lock defers maintenance to the next tick; writes still prune before append. When both processes are stopped or the Mac is asleep, no cleanup daemon runs; startup/wake removes expired metadata. Stop invalidation precedes audit writes. Local deletion/export is permitted, files are not tamper-evident, and same-user/root edits cannot be prevented. No link/agent audit-history API or automatic upload. Captured content is absent from audit/debug/telemetry/error attachments; crash-memory exposure remains possible. Screenshots deliberately retained between the user and their model provider follow ordinary conversation-content retention; consent discloses that exposure. This is separate from the content-free audit policy.

**Seven viewer.** The consumer shows the selected device and scope, current sharing state and Stop. This version has no agent frame tool, screenshot persistence or browser-capture fallback. Already received pixels cannot be recalled. Source availability and native identity acceptance remain subject to the app and verifier checks above.

#### 5.19 `cdp` (page cap `cdp-in`), node → page: a CDP endpoint on the node

This is how [`slicc … cdp`](#driving-sevens-browser-from-another-computer-slicc-join-url-cdp) drives seven's browser. It is the reverse of §5.8: the node opens the stream, so its ids are even and never collide with §5.8's page-opened `cdp`. seven's side is specified in slicc-bios's link section, and the two must stay in step.
- **HELLO:** seven adds `cdp-in` to the page caps of every CLI link, local and remote, when it can serve the kind. The cap means "this page understands the kind", not "this device may". Links from a seven following another seven (§1.4) don't get it. The page checks the device's grant on each OPEN, so turning it on or off needs no new link.
- **OPEN:** `{ "kind": "cdp" }` on a stream channel of its own. Unknown fields are ignored.
- **The page's checks, in this order:**
  1. seven's switch *Browser control from linked devices* is off: `RESET EACCES: browser control from linked devices is off in seven's settings`.
  2. The device has no `browser` grant: `RESET EACCES: browser control from this device is off; turn on Browser for it in seven's Linked devices`. A local CLI link (not paired) has the grant fixed on, as it has `control`. A paired link (`slicc open` and `pair`) follows its own access, as for `control`.
  3. No browser can be driven: `RESET ENOENT: <the reason>`, such as `no browser to drive: install slicc-extension, or link a computer with npx sliccy@next`.
- **Where the stream ends:** at `browserControl().hook({ caller: { kind: 'device', id, name } })` in seven's page, the same hook and guard as the kernel's CDP facade, never at a browser host or link exit directly. `caller` only names the device for seven's indicator; routing is automatic, as for a kernel program with no `runtime=`.
- **OPENED:** `{ "version": <the result of Browser.getVersion> }`, which the page sends with id `-2` on the guarded connection and doesn't pass to the stream. So it carries the guard's `slicc.route` hint (`extension`, `link` or `devtools`).
- **Bytes, both ways:** as §5.8, a `u32` BE length plus one UTF-8 CDP JSON message, at most 64 MiB (67 108 864 bytes).
  - A longer message is `RESET EMSGSIZE`, and an END in the middle of a message is `RESET EPROTO`.
  - The page passes each message to the connection's `send` as is, and each message from the connection to the stream as one message. Neither side rewrites ids or sessions.
- **Flow control:** the link's 256 KiB window per direction.
  - The page credits the node's bytes once `send` has taken the message.
  - Toward the node, the page queues the connection's messages while it has no credit, up to 64 MiB per stream, then `RESET ENOBUFS`. It keeps reading every channel (§2).
  - The node stops reading a client's socket while it has no credit.
- **Closing:**
  - END from the node: the page closes the connection, then sends END.
  - The connection closes on its own: `RESET ECONNRESET: <reason>`.
  - The owner turns the device's `browser` access off: `RESET EACCES: the owner took this access back`, on every stream of that device.
  - The switch goes off: the switch's text above, on every stream.
  - Stop on seven's indicator: `RESET ECONNRESET: stopped in seven`, on that device's streams.
  - Unlinking, or the link going down, ends the streams with the link.

### 6. Policy (node)

#### 6.1 Where it's checked
- **On the address being connected:** in the dialer's `Control` hook, for every address Happy Eyeballs tries. So a name can't rebind past it, and one check covers `tcp`, `http` and `resolve` filtering.
- **Normalized first:**
  - IPv4-mapped IPv6 (`::ffff:a.b.c.d`) is checked as IPv4.
  - NAT64 `64:ff9b::/96` and 6to4 (`2002::/16`) are checked by their embedded IPv4. The local-use NAT64 range `64:ff9b:1::/48` is denied outright, since RFC 8215 lets operators place the IPv4 address differently.
  - Teredo (`2001::/32`) is denied.
  - `0.0.0.0/8` and `::/128` are always denied, because `connect()` to them reaches loopback.

#### 6.2 Rules
- **Form:** `--allow` and `--deny` take `CIDR` or `CIDR:port` (`127.0.0.1/32:9222`, `[::1]/128:9222`).
- **Most specific wins:** the longest prefix wins, and at the same prefix a rule with a port wins over one without.
- **Ties:** at a full tie, a **user flag beats a default rule**, so `--allow 192.168.0.0/16` opens exactly the range it names. Among the user's own flags, deny wins.
- **Refusal:** `EACCES`.

#### 6.3 Remote default (`slicc <join-url> follow --exit`)

The node allows public unicast and denies:
- loopback: `127.0.0.0/8`, `::1/128`;
- private ranges: `10/8`, `172.16/12`, `192.168/16`;
- CGNAT and tailnets: `100.64/10`;
- link-local, including cloud metadata: `169.254/16`, `fe80::/10`;
- ULA: `fc00::/7`, which covers `fd7a:115c:a1e0::/48`;
- multicast: `224/4`, `ff00::/8`;
- reserved and documentation ranges: `240/4`, `255.255.255.255/32`, `192.0.0/24`, `192.0.2/24`, `198.18/15`, `198.51.100/24`, `203.0.113/24`, `2001:db8::/32`, `100::/64`;
- deprecated IPv4-compatible `::/96` and SIIT `::ffff:0:0:0/96`;
- this machine's debugging ports on every address of it ([§6.5](#65-debugging-ports-every-mode)), which no `--allow` reopens.

#### 6.4 Local default (`slicc`, the bridge to the browser it controls)

The remote default, but it **also allows loopback and the private ranges** (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`).
- **Why:** this is parity with slicc-node, whose `fetch-proxy` reaches the LAN today (a NAS, a dev server on another box).
- **Still denied:** link-local and metadata.
- **Except on loopback:** the node denies the debugging ports of [§6.5](#65-debugging-ports-every-mode) and its own listening ports (the kernel listener of [§5.9](#59-kernel-listener-on-the-node-local-only) included). Otherwise a kernel program could reach raw CDP on the host and get around the `cdp` capability.
  - It denies them on **every address of this machine** too (interfaces enumerated at check time, cached briefly), in case something listens on `0.0.0.0`.
  - Browsers the node launches bind their debugging port to loopback only, and so do the node's own listeners.
- **Opting out:** `--deny 127.0.0.0/8` and `--deny 192.168.0.0/16` restore the remote default.

#### 6.5 Debugging ports (every mode)

Both defaults, in every mode (local and remote), deny this machine's browser debugging ports on `127.0.0.0/8`, `::1` and **every address of this machine**, so `<host>.slicc.internal:<port>` (which the page sends as `127.0.0.1`) is refused too:
- **Which:** `9222`–`9229`; every port in a `DevToolsActivePort` file the node finds read-only; the port of every running process started with `--remote-debugging-port` (for port 0, the `DevToolsActivePort` of its `--user-data-dir`); the port of every running [`slicc … cdp`](#driving-sevens-browser-from-another-computer-slicc-join-url-cdp) of this user, from the `cdp` in its control socket's `status`; and the debugging port of each browser the node was given or launched. A `slicc … cdp` forwards nothing of its own, so it denies its own port to nothing.
- **When:** the files and the control sockets at startup, every 5 s, and on each `tcp` connect (`net`, `http`, `ws`, `ssh` forwarding) to an address of this machine on a port not yet denied. The process list at startup, then in the background (one scan at a time): about every 1 s while such connects keep coming (until 30 s without one; a slow scan is followed by a pause as long), every 30 s while idle. A connect uses the list only if it is under about 1 s old; otherwise it waits for a fresh scan, bounded by the scan's timeout, and is refused if that scan fails or times out (reason `a browser debugging port: couldn't list running browsers`). Connects to other hosts never wait. On Linux the list falls back to `/proc` when `ps` fails. On Windows it is read in-process, not through PowerShell, which can take more than its 5 s to start on a busy or freshly booted machine; until then every connect to this machine was refused. With no list at all at startup, the node says `can't list running browsers here (…); connects to this machine's own addresses are refused` once on stderr; a later failed scan is reported once, and again when it recovers. So a browser that starts later is refused before its first connection, once it has run for about a second.
- **Above every rule:** no `--allow` reopens one, `--allow 127.0.0.1:9222` included. A refusal is `EACCES`, reason `a browser debugging port`.
- **Opt-in:** only `--expose-debug-port <port>` (repeatable) leaves a port of the first three kinds open, and only one that is a debugging port at startup. Any other port is an error, and so is a browser the node was given or launched: `cdp` is the way to them.
- **HELLO:** `policy.deny` lists the loopback denies of the ports known at startup; `policy.summary` ends in `exposes debugging ports <ports>`, the ports actually exposed.

### 7. Reference peer for seven's CI

The node is the reference. Two setup modes exist for tests:
- **`slicc attach --cdp <url> --origin <test origin> [--allow …]`** is the production local path.
- **`--signal stdio`** writes the setup messages from [§1.1](#11-local-setup-over-cdp) as NDJSON on stdout and reads them from stdin. A test harness that already owns CDP relays them itself. slicc-shared-web's harness wants to be the only CDP client.

For tests, a node in either mode can add `--allow 127.0.0.1/32`, so the harness's own upstream servers count as public.

CI installs a pinned release: `go install github.com/ai-ecoverse/slicc-cli/cmd/slicc@v<version>`, or the release binary (see [Install](#install)).

§8 is retired.

## Development

Go 1.26 and Node ≥ 24 (for the lint tools). `npm run build` writes `bin/slicc`. `npm run lint` runs the slicc lint tools, `gofmt`, `go mod tidy -diff`, `go vet` (also with `GOOS=darwin` and `GOOS=windows`, since CI tests on Linux only) and golangci-lint; `npm run lint:sh` runs shellcheck on `install.sh` and the scripts. `npm test` runs the integration tests in `test/integration/`, which build the binary and drive it; run `npm ci` first, since the [Inside seven](#inside-seven) tests need the pinned `@ai-ecoverse/slicc-kernel`. Unit tests stay next to the code and out of git (`npm run test:unit`). `test/integration/testdata/native/` holds the fixtures of the contracts a native app relies on, each written by the integration test that checks it: run that test with `SLICC_FIXTURE_WRITE=1` to rewrite a fixture after a deliberate change, and commit the result. `go build` and `npm run build` link no version, so these binaries report `dev`. `npm run dist` cross-builds all six targets into `artifacts/release/` with `SHA256SUMS`, unsigned. `npm run npm:stage` then stages the npm packages into `dist/npm/`, and `npm run npm:check` packs them with `npm publish --dry-run`.

**CI.** `.github/workflows/ci.yml` is one caller of [slicc-shared-web's pipeline](https://github.com/ai-ecoverse/slicc-shared-web#pipeline), pinned at the release of the `@ai-ecoverse/slicc-shared-web` devDependency, with `secrets: inherit`. On pull requests and in the merge queue it runs `lint` (`npm run lint` with golangci-lint, pinned in `ci.yml` and installed into `$RUNNER_TEMP`, then `npm run lint:sh`), then `npm test` on four Linux shards of the pool, then on a pull request the Claude and Codex reviews with this repo's `review-prompt`. The checks to require are `ci / integration` and `ci / review`. A push to `main` runs lint and the release (see [Releases](#releases)); it runs no tests, since what reaches `main` passed the merge queue. macOS and Windows aren't tested on pull requests or in the merge queue: lint vets both, and platform code needs a local run there. `.github/workflows/windows.yml` runs `TestWindowsArgs` on `windows-latest` on each push to `main`; it isn't a required check, and a red run there needs a follow-up pull request. **Test shards.** `npm test` runs `scripts/test-shard.mjs ci/4 -race -count=1 -timeout 15m -v ./test/integration/...`. On the pipeline's shard `SLICC_TEST_SHARD` (0 to 3) it runs that shard's share of the top-level tests; without it (a local run) it runs them all. The split is greedy by measured time: `test/integration/shard-times.json` holds each top-level test's seconds on `linux`, each test goes to the shard with the least time so far, a test without a time counts as the median, and the last shard runs everything the others don't, so a new test still runs. `go test` gets the pipeline's own `NODE_OPTIONS` back (without its shard preload and JUnit reporter) and none of its `SLICC_TEST_*` variables. The script writes each top-level test's result to the shard's JUnit report (`SLICC_TEST_JUNIT`), which the pipeline's `integration` check counts: it fails when no test ran or one failed. It also fails a shard that selects no tests or whose `go test -v` runs none, and, when a shard hits its `-timeout`, names the test that was running and how long the finished ones took. `node scripts/shard-times.mjs --run <CI run id>` refreshes the times from a finished run's `test (<n>)` logs (`linux=<log file>` reads local `go test -v` output instead); commit the result when tests get much slower or faster. `TestEachCIShardFitsItsTimeout` fails when a shard adds up to more than three quarters of its `-timeout`, or when `timeout-minutes` leaves less than 5 minutes past it: then raise `shards` in `ci.yml` and the count in `npm test` together. The [guard vectors](https://github.com/ai-ecoverse/slicc-browsers#guard-vectors) run on every shard; `SLICC_GUARD_VECTORS=0` skips them locally. On macOS they leave out the C9 vectors (extension worlds; the `C9.` prefix that `guardSkippedOn` in `guardrunner_test.go` returns for `darwin`): there their unguarded control hung in Chrome itself ([#344](https://github.com/ai-ecoverse/slicc-cli/issues/344)). `TestCDPGuardVectorsSkipC9OnlyOnMacOS` keeps the skip to macOS, and `TestCDPHidesExtensionWorlds` covers the extension-world guard everywhere. **Node:** CI runs Node 26: on Node 24.21.0, V8 crashed the WASI kernel harness (`TestWasi*`) about once in a hundred runs on Windows and occasionally on Linux.

**Test names: `--hosts <file>`** (test-only, left out of `--help`). `slicc attach`, `slicc open`, `slicc pair` and `slicc <join-url> follow` take a hosts file, so tests get deterministic names without public DNS. Each line is `<ip> <name> [<name>…]`, and `#` starts a comment. Names match case-insensitively, with or without a trailing dot, and a name on several lines gets every address, in file order. Wherever the node resolves a name (`resolve`, `tcp` by name, `http`, and the policy's [browser endpoints](#network-policy)), a name in the file gets the file's addresses of the requested family, possibly none, and never the system resolver's. Other names go to the system resolver. The policy applies afterwards as usual: `resolve` drops refused addresses, and a dial to a refused address is `EACCES`. `tcp` and `http` try the addresses in file order. A file that can't be read or parsed exits 2. A name mapped to a loopback address only helps the node's own dials, `http` and `tcp` by name: a seven kernel that gets a loopback address from `resolve` dials its own loopback, because loopback never goes out through an exit. For end-to-end exit tests, map test names to a non-loopback address of the machine.

Front ends and capabilities follow a node's links with `node.Node.Subscribe(func(node.Event)) (unsubscribe func())`. Events: `connecting` (an offer was answered), `linked` (the peer's HELLO arrived, in `Peer`), `link-down` (`Reason` when the node closed the link: seven's `bye` reason, `replaced by a new offer`, or the reason of its own `bye`: `slicc is exiting`, or the `node.Bye` that cancelled `Serve`'s context as its cause, such as `slicc is relinking`; otherwise `Err` for a failure; a failed answer has no `Link`), and `stream-open` and `stream-close` for each page-opened stream, with its kind. Every event but a failed answer carries its `*link.Link`. A link's `link-down` comes before the next link's `connecting`, but its streams' `stream-close` can come after its `link-down`. Events are delivered synchronously on the node's and the link's goroutines, so a subscriber must return quickly and never block; one that needs to wait hands the event to its own goroutine.

**Test isolation: `SLICC_DISCOVERY`** (test-only). When it is set, browser discovery (`slicc browsers`, the default flow, and `browser.Discover` in the same process) never probes ports 9222–9229, never reads the process list, and finds only `--cdp` URLs, plus:
- `isolated`, or any value not listed here: nothing else;
- `installed`: installed browsers too;
- `home`: installed browsers, and the `DevToolsActivePort` files under the home and config dirs from the environment (`HOME`, `APPDATA`, `LOCALAPPDATA`, `XDG_CONFIG_HOME`), SLICC six's profiles included (`XDG_STATE_HOME`, `SLICC_USER_DATA_DIR`).

In the same process, probe ports, dirs and a process list passed in explicitly (`Options.ProbePorts`, `Options.Home`, `Options.ProfilesDir`, and `Options.Processes`, which returns the output of the process-list command) are still used. `Options.Transport` and `Options.Dial` replace discovery's HTTP transport and TCP dial; the corpus test fakes every server with them, and its process list is fake too. `test/integration`'s `TestMain` sets `SLICC_DISCOVERY=isolated` and unsets `SLICC_USER_DATA_DIR`, and points `HOME`, `APPDATA` and the XDG dirs, `XDG_STATE_HOME` included (and `LOCALAPPDATA`, and `USERPROFILE` except on Windows, where a Chrome started with a temporary one never writes `DevToolsActivePort`; the tests that have `slicc` launch a real Chrome give it the real `LOCALAPPDATA` on Windows, which they only do on CI) at a temporary directory for itself and every `slicc` it starts, so no test can reach the browsers of the machine it runs on. It also puts a stand-in `jerk` first on `PATH` that fails `--version` (on Windows one for every `PATHEXT` extension), so slicc never finds the machine's own slicc-jerk there, while every other tool keeps its own path. The tests that want a jerk put the fake jerk's dir before it. Tests opt in to more with a fake home or fake endpoints. The Chrome the harness starts itself gets the real environment.

**Debugging ports in tests: `SLICC_PROTECTED_PORTS`** (test-only). `<first>-<last>` (or one port) adds to 9222–9229 the range every mode denies (see [Network policy](#network-policy)); 9222–9229 stay denied. The debugging-port tests (`test/integration/debugports_test.go`) set it to two free ports where fakes listen, write fake `DevToolsActivePort` files for two more under `SLICC_DISCOVERY=home`, give a fake CDP endpoint as `--cdp-extra` or `--cdp`, and after link-up write another `DevToolsActivePort` file and start two fake browsers with `--remote-debugging-port` (one with a port, one with 0 and a `--user-data-dir`). They prove, in every mode, that a link `net` or `http` request to each of them is refused, an exact `--allow` included, that a browser that starts later is refused on the first connection after more than 1 s without one, and that `--expose-debug-port` opens only what it names, without ever touching 9222–9229 on the machine they run on. Tests of `browser.Guard` with a fake process table prove that a connect to this machine with a stale list waits for a gated, slow scan and is decided by it while connects to other hosts don't wait, that the list is rescanned about every second after a connect and connects then don't wait, that a scan that fails or times out refuses the connect, that failure and recovery are each reported once, that an idle node scans at most twice in 5 s, and that shutdown reports no failure; every connect in them has a deadline. **`SLICC_PROC_ROOT`** (test-only) replaces `/proc` for the Linux fallback. With a failing `ps` first on `PATH`, the tests prove that `slicc attach` reads a fake `/proc` on Linux (its browser's port refused, a browser added later refused on its first connection, other loopback ports open, no failure line) and that with no `/proc` either it prints the startup line once and refuses connects to this machine.

**SLICC six's profile in tests.** The `--profile six` tests build fake six profile trees in a fake home only: each OS's layout, a mixed-case name, a symlinked profile and root, `SingletonLock` (or `lockfile`) live, stale and absent, and debuggable endpoints that are fakes or the harness's own headless Chrome on a free port. They check the trees byte for byte after a launch, an attach and an exit. A fake browser (`testdata/fakechrome`) stands in for Chrome where a real one would write to the profile itself. The one real-Chrome launch into a fake six profile checks that nothing was seeded; on Windows it puts that profile at `SLICC_USER_DATA_DIR` in a temporary dir, since that Chrome keeps the real `LOCALAPPDATA` (see below).

**Port guard.** On CI (`CI` set), or with `SLICC_TEST_PORT_GUARD=1`, `TestMain` holds `127.0.0.1:9222` to `9229` (and `[::1]` where it can) for the whole run, and the run fails if anything connects to them, other than the one test that lends a port to its own fake browser. On CI a port it can't hold fails the run. Off CI it is off by default, so a developer's own Chrome on those ports is never touched; the guarded tests then skip.

**Protected-origin vectors.** `test/integration/testdata/link/protect.js` exports `protectVectors`, the attacks the relay must stop, in a shape other guards reuse: `{ id, setup, trigger, control: { origin: 'ordinary', expected }, expected }`. Each runs three times, through the relay, against the same server under three names: an ordinary origin (`ordinary.test`, the control, which must succeed), seven (an `--origin`) and the vault (`vault.sliccy.ai`). A run passes when its observations match `control.expected` or `expected`, and a vector whose control fails fails as a whole. The only run left out is the vault in the same-site iframe cases, since no unprotected page is same-site with a `sliccy.ai` host. The observations come from the test server's log of requests and of the beacons an injected init script sends, and from what the page's own CDP session saw. **Waivers on this head** (slicc-browsers 1.22.1; each waiver accepts only its documented failing leg): `C3.tab-attach` — guarded settle failure of `window.open` under main's empty-URL hold (runner folds that into `control: fail`, #260), not any other non-pass; stock Playwright/Puppeteer popup through the link guard stays green. Required green: `C3.popup`, `C3.tab-create`, `C3.tab-blank`, `C3.tab-protected`. The cases:
- C1, an init script, then `location.href`;
- C2, redirect chains through allowed hops (301, 302, 303, 307, 308, and a POST form through 307 and 308), and a chain that passes through the protected origin and back out;
- C3, an instrumented popup that navigates itself, a popup opened on a redirect chain, and popups opened on a redirect chain or straight on the origin that never reach the page: not through auto-attach with or without `waitForDebuggerOnStart`, `Target.attachToTarget` or `activateTarget`, and not after they navigate to another path of the origin and the opener turns auto-attach on again; `Target.getTargetInfo` shows them without their path;
- C4, static and script-inserted iframes, same-site and cross-site to the page;
- C5, the page's own `Fetch.enable` on every request at both stages: a document, `fetch()`, XHR, an image, an auth challenge, and fulfilling guessed request ids;
- C6, cookie reads, writes and clears, and storage commands that name the origin or its storage key.

Chrome 156 no longer has `Network.setRequestInterception`, so the deprecated `Network.requestIntercepted` path is tested against a fake browser instead.

**Real browsers in tests.** The Chrome the harness starts itself is `SLICC_TEST_CHROME` (test-only: the path of a Chrome executable), else the machine's Chrome or Chromium, with the real environment, a temporary profile and `--use-mock-keychain --password-store=basic`. The tests that have `slicc` itself launch a real Chrome run it under a temporary home, where its profiles are throwaway ones. Off CI they never launch the machine's own Chrome: they need `SLICC_TEST_CHROME` set to a Chrome for Testing inside its `.app`, which they link into the temporary home's `Applications` folder on macOS and launch by its id (`chrome-for-testing`), and they skip with a notice without it, or on other systems. On CI they launch the runner's Google Chrome. An older Chrome for Testing can fail the tests that read Chrome's own prefs.

On GitHub's Windows runners the system drive `C:` is slow: with Chrome profiles there, Chrome sometimes waited seconds before a new tab's first request (up to past the 15 s page-load limit) or before exiting (`did not exit`). With them on `D:`, the runner's temp drive, those stalls weren't seen and the suite ran about a quarter faster. So on a Windows runner, point `TEMP` and `TMP` at its temp drive: the tests' temporary homes and Chrome profiles live there. When a test page doesn't load, the failure says where it stopped: the tab's URL and `readyState`, the resources it fetched, and every request the page server saw.

The real-Chrome tests in `test/integration/` launch headless, so they run the executable directly. A windowed launch on macOS goes through `open -n -W -a`, which CI doesn't cover: on GitHub's macOS runners, Chrome started that way never writes `DevToolsActivePort`.

## Releases

PRs merge through the merge queue, which requires `ci / integration` and `ci / review`. semantic-release cuts a release from every push to `main` in the pipeline's `release` job (GitHub-hosted `ubuntu-latest`, since npm's trusted publishing takes OIDC tokens only from GitHub-hosted runners), in its own concurrency group that never cancels; `main` runs lint before it and no tests, since the merge queue tested the commit. The job gets the Apple secrets named in `ci.yml`'s `release-secrets` and nothing else. Nothing is released by hand: a failed release is fixed by the next push to `main`, which releases every commit since the last tag. `.releaserc.json` runs:
1. `scripts/go-major.mjs <version>` (verify, see below).
2. `scripts/release-build.sh <version>` (prepare): `scripts/package.sh <version>` builds `darwin`, `linux` and `windows` × `amd64`, `arm64` with `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false` and `-X main.version=<version>`, and `slicc-wasip1-wasm.wasm` (`GOOS=wasip1 GOARCH=wasm`) the same way, with the Go toolchain `go.mod` names (see [Reproducing a release](#reproducing-a-release)). It signs the darwin binaries with [rcodesign](https://github.com/indygreg/apple-platform-rs) (pinned by version and SHA-256, downloaded unless `RCODESIGN` names one), identifier `ai.sliccy.slicc`, hardened runtime, Developer ID with a timestamp, notarizes both in one zip with Apple's Notary API, and writes `SHA256SUMS`. The windows and linux binaries are not signed. In the release (`SLICC_SIGN=required`) a missing secret fails the build; elsewhere (`SLICC_SIGN` unset) it warns and leaves the darwin binaries unsigned, and `SLICC_SIGN=0` skips signing. The script then runs the runner's own binary with `--version`, which must answer `slicc <version>`, and `scripts/npm-stage.mjs` stages `@ai-ecoverse/slicc` and the six platform packages into `dist/npm/`, with `--wasi` also `@ai-ecoverse/wasi-slicc` (from `slicc-wasip1-wasm.wasm` and `npm/wasi-slicc/host/slicc-host.mjs`, with README.md's [Inside seven](#inside-seven) section as its README), and the same launcher as `sliccy` (two bins, `sliccy` and `slicc`, and a short preview README) into `dist/npm-next/` as `7.0.0-next.<version>`, with exact `optionalDependencies` on this release's platform packages. `npx sliccy@next` runs the bin named after the package, and both bins run the same launcher. `--bin <name>` (repeatable) overrides the bins of any staged launcher.
3. `@semantic-release/github` tags and creates the GitHub release with every binary, `install.sh` and `SHA256SUMS`.
4. `scripts/npm-publish.mjs` (publish) publishes the platform packages, then the launcher and `wasi-slicc`, through npm trusted publishing from `ci.yml` (OIDC, with provenance), then `sliccy` with `--tag next`. Versions already on npm are skipped. After any real publish error, it checks `npm view <package>@<version> version` again and accepts an exact published version, including when a retry reports a package-version conflict. If the version is still absent and publishing reports `TLOG_CREATE_ENTRY_ERROR` or a transparency-log HTTP 409, it retries with provenance, up to three total attempts with 5- and 10-second backoffs, then fails. Other errors fail after the registry check; dry-run errors fail immediately. Before publishing `sliccy`, it checks that every exact-pinned `optionalDependencies` version is on the registry, waits up to `SLICC_REGISTRY_WAIT_SECONDS` (default 900) for freshly published ones, and fails otherwise (exit code 3), so a `sliccy` version never points at missing platform packages.

After a release, the pipeline attests the build provenance of `artifacts/release/SHA256SUMS` (`attest` in `ci.yml`) and, from the pipeline release that ai-ecoverse/slicc-shared-web#470 lands in, attaches the attestation's bundle to the release as `SHA256SUMS.sigstore.json`, both of which `slicc update` checks (see [Attestations](#attestations)), and `after-release` runs `scripts/go-warm.sh <version>`: it asks `proxy.golang.org` for the module at `v<version>` (`go list -m` with a scratch module cache) and asks `pkg.go.dev` to fetch it. Both only warn.

**Go module versions.** In Go a pushed tag is the release: `proxy.golang.org` caches it, `sum.golang.org` records its hash for good, and `pkg.go.dev` indexes it.
- **Never move or delete a pushed tag.** The checksum database would then reject that version for every user. A bad version gets a `retract` directive in `go.mod`, and a new release.
- **Major versions.** A module at v2 or above needs `/vN` at the end of its module path, or `go install …@latest` never sees it. `scripts/go-major.mjs <version>` fails the release when the version's major doesn't fit the module path in `go.mod` (no suffix allows v0 and v1, `/vN` allows only vN). A `BREAKING CHANGE` commit therefore needs a deliberate module-path change first.

**Gate verdicts.** A gate's silence never reads as a pass. Each script that gates a release ends a pass with a `<name>: PASS …` line: `go-major: PASS 1.4.0 (major 1) fits module path …`, `release-build: PASS 1.4.0, slicc-linux-amd64 runs, 8 files in SHA256SUMS` and `npm-publish: PASS latest: published 8, 0 already published of 8 packages`. `.releaserc.json` runs each through `sh scripts/verdict.sh <name> <command…>`, which fails with the gate's exit code when it fails, and fails when it exits 0 without its `<name>: PASS` line. `npm-publish.mjs` also fails on a directory with no packages. The scripts find their main block with `realpath` on both `process.argv[1]` and the module's own path (`scripts/is-main.mjs`), so they also run through a symlinked checkout or script.

npm needs no token. `npm run trust` (fledgling) configures trusted publishing from `ci.yml` for the nine packages under `fledgling.include` in `package.json`, `@ai-ecoverse/wasi-slicc` and `sliccy` included.

`sliccy` only ever goes to the `next` dist-tag, so `npx sliccy` keeps running SLICC six and `npx sliccy@next` runs this binary. `npm-publish.mjs` refuses, even in a dry run, to publish `sliccy` with any tag but `next` or with a version that doesn't match `^7\.0\.0-next\.`. Nothing here changes `sliccy`'s other dist-tags.
