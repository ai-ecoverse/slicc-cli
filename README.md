# slicc-cli

`slicc` is SLICC's command line for seven, the SLICC on `*.sliccy.ai`. It is one binary for macOS, Windows and Linux. It launches and links browsers, and shares a machine's network, compute and browsers with a running seven over a WebRTC link. `npx sliccy` and Sliccstart.app run this binary. It replaces slicc-node and slicc-swift.

**Status:** being built. `slicc` (the default flow), `slicc attach` (with the `cdp` capability), `slicc <join-url> follow`, `slicc browsers`, `slicc launch`, `update`, `--version` and `--help` work so far, plus a hidden diagnostic, `slicc <join-url> follow --signal-only [--timeout 2m]`. It attaches through the tray hub, prints a summary of the leader's offer (its size, media sections, candidate count and DTLS fingerprint) and the ICE server URLs (credentials are never printed), and exits without answering. The link protocol is drafted alongside seven's link exit and moves into this README once both sides pass a shared test.

## Commands (planned)

| command | what it does |
| --- | --- |
| `slicc` | pick browsers in a small TUI; Enter launches or attaches to the best match, opens seven and links it (works now; see [The default](#the-default-slicc)) |
| `slicc attach --cdp <url>` | link a seven that is open in an already-running CDP browser (works now; see [Local setup](#local-setup-slicc-attach)) |
| `slicc launch` | launch a browser with CDP in a separate SLICC profile and print its CDP URL |
| `slicc <join-url> follow [--exit] [--runner …] [--cdp <url>]` | follow a seven's tray from another machine and offer its network, compute (over SSH) or browsers (`--exit`, `--runner` and `--cdp` work now; see [Remote setup](#remote-setup-slicc-join-url-follow)) |
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
- `enter`, with the subcommand as `source`, from a fixed list (`attach`, `browsers`, `follow`, `launch`, `prompt`, `update`, `watch`, `default` for no command (no arguments, or only flags), `join` for a bare join URL, otherwise `unknown`). No other argument is ever sent.
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

## The default: `slicc`

```
slicc [--browser <id>]... [--cdp <url>]... [--page <url>] [--origin <origin>]... [--profile <name>] [--headless]
      [--name <name>] [--kernel-port <n>] [--no-kernel] [--allow <rule>]... [--deny <rule>]... [--yes] [--plain] [--json]
```

`slicc` with no command (`npx sliccy`) finds browsers, launches or attaches to the best one, opens seven in it and links it. It serves the link until it is told to stop: `q` or Ctrl-C in the dashboard, or `SIGINT` or `SIGTERM`. Then it says `bye` to seven, closes the browsers this run launched (as `slicc launch` does), leaves the browsers it attached to running, and exits 0. If the browser that hosts seven goes away, `slicc` closes the rest and exits 0 too. Setup failures (no browser, a launch or attach that fails) exit 1, and bad flags exit 2.

**Discovery** is `slicc browsers`' discovery and ranking (see [Browsers](#browsers)), with each `--cdp` URL added and the `--page` origin counted as a seven origin.

**Going with a browser.** The first browser hosts seven:
- a running endpoint with a seven tab open: slicc attaches to it;
- a running endpoint without one: slicc opens `--page` in a new tab (`Target.createTarget`) and attaches;
- an installed browser: slicc launches it as `slicc launch --url <page>` does, in its SLICC profile (`--profile`, or the one picked with `p`; default: the browser id), with the seven flags and prefs, and `--headless=new` with `--headless`, then attaches.

An Electron app can't host seven. Each further browser is an extra: an installed one is launched without a start page, and an endpoint is used as it is. Then the link is set up as for `slicc attach` (see [Local setup](#local-setup-slicc-attach)): over CDP with the browser that hosts seven, the first page tab on an allowed origin.

**What the link offers:** `net` (tcp, resolve), `http`, and `cdp` with the browser that hosts seven (`seven: true`) and every extra, under the local policy with all of their debugging ports protected; and kernel services on `--kernel-port` (default 80; `--no-kernel` turns them off) when seven offers `kernel-in` (see [Kernel services](#kernel-services)). seven gets full control of every listed browser.

**Flags.**
- `--browser <id>`: an id from `slicc browsers` (an installed id such as `chrome`, or `cdp:<host>:<port>`), or a name, case-insensitively. Repeatable: the first hosts seven, the others are extras. Without it, the best match that can host seven.
- `--cdp <url>`: also consider this CDP endpoint, as `slicc browsers --cdp`.
- `--page <url>`: the seven page, default `https://seven.sliccy.ai/`. It must be `http` or `https`, and its origin is accepted as a seven origin.
- `--origin`, `--name`, `--allow`, `--deny`, `--kernel-port`, `--no-kernel`: as for `slicc attach`.
- `--profile <name>`: the SLICC profile for a launched browser that hosts seven.
- `--headless`: launch browsers without a window.
- `--yes`, `--plain`: no picker. `--json`: NDJSON events, below.
- Any other flag, or an argument, is an error.

**The picker** comes up when stdin and stdout are both terminals and none of `--yes`, `--browser`, `--plain` or `--json` is given. It lists the ranking best first, one row each, with its state: `CDP :<port>` and `seven open` for a running endpoint, `installed · profile <name>` (or `new SLICC profile <name>` when the profile doesn't exist yet) for a browser slicc would launch, and `installed · Electron` for an Electron app. The first row is marked `best match`. A list longer than the terminal scrolls with the cursor.

| key | what it does |
| --- | --- |
| ↑ ↓ (`k`, `j`) | move |
| space | select or unselect the row; the first selected hosts seven, and the rest are `extra cdp` browsers, in the order selected |
| Enter | go: with the selected rows, or with the highlighted row when nothing is selected |
| `p` | choose the highlighted browser's SLICC profile: its default, an existing profile in the profiles dir, or `new profile…` with a name (letters, digits, `.`, `_`, `-`) |
| Esc | back, in the profile chooser |
| `q`, Ctrl-C | quit; in the dashboard, close down as described above |

**The dashboard** follows: the linked browsers (launched or attached, their CDP port, hosts seven or extra), the seven tab, the link state (`waiting for seven`, `connecting`, `linked` with its uptime, or `reconnecting` after a link is lost, while slicc waits for seven to offer again), the capabilities offered, the policy summary, the kernel services URL, stream counts per kind (open and total, never contents), the last status lines, and the update notice, which then isn't printed on stderr. `NO_COLOR` turns colors off.

**Plain mode** (no terminal, `--yes`, `--browser` or `--plain`): no picker and no dashboard, just the default, with status lines on stderr (`slicc: launched …`, `slicc: attached to …`, `slicc: linked with "<name>" in <tab>`, `slicc: link lost: …`, `slicc: closed <ids>`). It never writes ANSI escapes or cursor codes. `SLICC_DEBUG=1` adds diagnostics.

**`--json`** prints NDJSON events on stdout and nothing else there, for Sliccstart (`slicc --json --browser <id>`). Errors are also printed on stderr. Every event has `"schema": 1` and `event`. Fields marked `?` are left out when empty; new fields and events may be added, and existing ones keep their meaning.
- `discovered`: `entries`, the ranked list in the shape of `slicc browsers --json` (always a list).
- `launched`: `browser` (the id), `name`, `version?`, `role` (`seven` or `extra`), `pid`, `profile?`, `profileDir?`, `cdp`, `webSocketDebuggerUrl`.
- `attached`: `browser`, `name`, `version?`, `role`, `launched?` (`true` when this run launched it), `cdp`, `webSocketDebuggerUrl?`. For `seven` slicc is connected to it over CDP; an `extra` is listed for `cdp`.
- `linked`: `peer?` (the name in seven's HELLO), `tab?` (the seven tab's URL), `caps`, `policy` (the summary).
- `link-lost`: `reason?` when slicc or seven closed the link (`slicc is exiting`, seven's `bye` reason, `replaced by a new offer`), or `message?` for a failure. seven sets the link up again after a reload, and a new `linked` follows.
- `closed`: `browsers`, the ids slicc closed (always a list). It is the last event of every run that got past choosing its browsers.
- `error`: `message`, and `browser?` when closing that browser failed. A setup error ends the run with exit 1.

```
{"schema":1,"event":"discovered","entries":[…]}
{"schema":1,"event":"launched","browser":"chrome","name":"Google Chrome","version":"154.0.8037.98","role":"seven","pid":4242,"profile":"chrome","profileDir":"…/slicc/profiles/chrome","cdp":"http://127.0.0.1:53211","webSocketDebuggerUrl":"ws://127.0.0.1:53211/devtools/browser/…"}
{"schema":1,"event":"attached","browser":"chrome","name":"Google Chrome","version":"154.0.8037.98","role":"seven","launched":true,"cdp":"http://127.0.0.1:53211","webSocketDebuggerUrl":"ws://127.0.0.1:53211/devtools/browser/…"}
{"schema":1,"event":"linked","tab":"https://seven.sliccy.ai/","peer":"seven","caps":["net","http","cdp"],"policy":"every public address, this machine, and the local network"}
{"schema":1,"event":"link-lost","reason":"slicc is exiting"}
{"schema":1,"event":"closed","browsers":["chrome"]}
```

## Browsers

### `slicc browsers [--json] [--cdp <url>]... [--no-bundle-scan]`

Lists running CDP endpoints and installed browsers, best match first.

**Running endpoints** come from three places:
- `DevToolsActivePort` files in the default user-data dirs of the browsers below, in each SLICC profile under the profiles dir (see `slicc launch`), and on Linux in the flatpak (`~/.var/app/<id>/config/…`) and snap (`~/snap/chromium/common/chromium`) dirs. The file's port must answer `/json/version` with the websocket path on the file's second line, otherwise the file is stale and ignored.
- `GET /json/version` on `127.0.0.1:9222` to `9229`.
- Each `--cdp` URL: `http(s)://host:port`, `ws(s)://host:port/devtools/browser/<id>`, `host:port` or a bare port. One that doesn't answer is reported on stderr and left out; the exit status stays 0.

Endpoints are merged by host and port. A `DevToolsActivePort` file wins over `--cdp`, which wins over the port probe. For each endpoint, `/json/version` gives the name, version and `webSocketDebuggerUrl`, and `/json/list` gives the seven tabs: `page` targets whose origin is `https://<label>.sliccy.ai`, other than `www`. A `DevToolsActivePort` whose port is open but serves no `/json/version` (Chrome's opt-in remote debugging of a running browser) is listed as `wsOnly`, with the websocket URL from the file and no tab check.

**Installed browsers**, by id: `chrome`, `edge`, `brave`, `opera`, `vivaldi`, `arc`, `atlas`, `dia`, `chrome-beta`, `chrome-dev`, `chrome-canary`, `brave-beta`, `brave-nightly`, `edge-beta`, `edge-dev`, `edge-canary`, `vivaldi-snapshot`, `chrome-for-testing`, `chromium`. Electron apps get `electron:<slug of the name>`, such as `electron:slack`. When a browser is found twice, the first find keeps the id.
- **macOS:** `/Applications` and `~/Applications`, plus `mdfind` by bundle id. Name, version and executable come from each bundle's `Info.plist`. An app is an Electron app when it has `Contents/Frameworks/Electron Framework.framework` or `MSWebView2.framework`. `~/Applications/<Name> Debug.app` (a Sliccstart debug build) replaces `/Applications/<Name>.app`. Claude and 1Password block remote debugging and are listed with `debugSupport: "disabled"`. With `--no-bundle-scan`, nothing inside an app bundle is read except for debug builds, which avoids macOS App Management prompts: browsers are matched by bundle id or app name, Electron apps by a list of known bundle ids, and versions come from Spotlight (`mdls`). Those Electron apps have `debugSupport: "unknown"`.
- **Windows:** `StartMenuInternet` and `App Paths` in the registry (current user, then machine), then the known paths under `%ProgramFiles%`, `%ProgramFiles(x86)%` and `%LOCALAPPDATA%`. The version is the highest version-named directory next to the executable. Electron apps are directories under `%LOCALAPPDATA%\Programs`, `%ProgramFiles%` and `%ProgramFiles(x86)%` with `resources\app.asar` (or `resources\app`) and Chromium's `.pak`/`.bin` files, and Squirrel installs (`%LOCALAPPDATA%\<app>\Update.exe` with `app-<version>` dirs).
- **Linux:** snaps (`/snap/bin/<name>`), names on `PATH` (such as `google-chrome-stable`, `chromium`, `microsoft-edge-stable`, `brave-browser`), flatpaks (`~/.local/share/flatpak/app/<id>`, `/var/lib/flatpak/app/<id>`) and `.desktop` files in the XDG, flatpak and snap application dirs. Versions come from `/var/lib/dpkg/status`, a snap's `meta/snap.yaml`, or else `<browser> --version`. A `.desktop` program whose directory has `resources/app.asar` (or `resources/app`) and Chromium's data files is an Electron app.

**Ranking.** Tiers, in order: 1 an endpoint with seven open, 2 any other running endpoint, 3 Chrome stable, 4 other Chromium browsers, 5 Electron apps. Endpoints sort by host, then port. Browsers sort in the id order above, Electron apps by name, then path. Versions below `MinChromiumMajor` (116) are left out: for endpoints by the `Chrome/<major>` in the user agent, and for installed Chrome, Edge and Chromium by their version, which is the Chromium version. Other browsers' versions are not Chromium versions and are not filtered.

**`--json`** prints one document. Fields marked `?` are left out when empty; new fields may be added, existing ones keep their meaning.

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
        "sevenTabs": [{ "id": "…", "url": "https://lars.sliccy.ai/", "title": "seven" }]
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

- Entry: `id` (an installed id, or `cdp:<host>:<port>`), `kind` (`endpoint` or `installed`), `tier` (1–5), `name`, `version?`, and either `endpoint` or `installed`.
- `endpoint`: `cdp` (the HTTP base URL), `webSocketDebuggerUrl`, `name`, `version?`, `product?` and `userAgent?` (from `/json/version`), `source` (`devtools-active-port`, `flag` or `probe`), `browser?` (the installed id whose user-data dir held the file), `userDataDir?`, `profile?` (the SLICC profile name), `wsOnly?`, `sevenOpen`, `sevenTabs` (always a list).
- `installed`: `id`, `name`, `engine` (`chromium` or `electron`), `channel?`, `version?`, `path` (the `.app`, executable or flatpak dir), `executable` (for a flatpak, the `flatpak` binary), `bundleId?`, `via?` (`snap` or `flatpak`), `package?` (the snap or flatpak id), `userDataDir?` (the browser's default), `debugSupport` (`supported`, `disabled` or `unknown`), `debugBuild?`.

Without `--json`, the same list is a table: id, name, version, state and where.

### `slicc launch [--browser <id>] [--profile <name>] [--url <url>] [--headless] [--no-seven-flags] [--json]`

Launches a browser with CDP and keeps running until interrupted (Ctrl-C, `SIGINT` or `SIGTERM`; on Windows Ctrl-C or Ctrl-Break). Then it closes the browser it started and exits 0. If the browser exits by itself, `slicc launch` exits 0 too.

- `--browser` takes an installed id from `slicc browsers` (or its name, case-insensitively). The default is the best installed Chromium browser in the ranking above.
- Chromium browsers always get a separate SLICC profile, `<config dir>/slicc/profiles/<name>`, because Chrome 136 and later ignore CDP flags on the default profile. The config dir is `~/Library/Application Support` on macOS, `%APPDATA%` on Windows and `$XDG_CONFIG_HOME` (or `~/.config`) on Linux. A snap's profiles live in `~/snap/<snap>/common/slicc/profiles`, the only place it can write. `--profile` is the name (letters, digits, `.`, `_`, `-`; default: the browser id). A profile another browser process still holds is refused.
- Before the launch, a stale `DevToolsActivePort`, Chrome's `Singleton*` files and the session-restore snapshot are removed, and the last exit is marked clean, so only the start page opens.
- Before each launch, the seven prefs are merged into the profile's `Default/Preferences`, which is created if missing: `tab_freezing_enabled: false`, `performance_tuning.tab_freezing.enabled: false`, Memory Saver off (`performance_tuning.high_efficiency_mode.state: 0`), and `[*.]sliccy.ai` plus each extra allowed origin's host as tab-discard exceptions. The exceptions go into the list `performance_tuning.tab_discarding.exceptions` (six's form) and into the map `performance_tuning.tab_discarding.exceptions_with_time`, which Chrome 154 reads and shows under "Always keep these sites active". Existing keys, exceptions and timestamps are kept, and a file that already has everything isn't rewritten. A Preferences file that isn't a JSON object is left alone. `--no-seven-flags` skips these prefs too.
- Chromium flags: `--remote-debugging-port=0 --user-data-dir=<profile> --no-first-run --no-default-browser-check`, the seven flags below, `--headless=new` with `--headless`, then `--url`.
- The seven flags keep a seven tab's kernel running while the tab is in the background: `--disable-background-timer-throttling`, `--disable-backgrounding-occluded-windows`, `--disable-renderer-backgrounding`, `--disable-background-tracing`, and `--disable-features=LocalNetworkAccessChecks,LocalNetworkAccessChecksWebSockets,IntensiveWakeUpThrottling,HighEfficiencyModeAvailable,InfiniteTabsFreezing,InfiniteTabsFreezingOnMemoryPressure,CPUMeasurementInFreezingPolicy,MemoryMeasurementInFreezingPolicy,AllowDevtoolsConnectedDiscard`. Chromium ignores feature names it doesn't know. `--no-seven-flags` (`LaunchOptions.NoSevenFlags`) leaves them all out, along with the seven prefs above. Chromium keeps only one `--disable-features` value, so every `--disable-features` from the seven flags and from `LaunchOptions.ExtraArgs` is merged into one switch, in order and without duplicates. The CDP port is read from the profile's `DevToolsActivePort` once `/json/version` answers with the same websocket path (30 s limit). On macOS a browser with a window starts through `open -n -W -a <app> --args …`, so camera and microphone permissions belong to the browser, not the terminal; `--headless` runs the executable directly. A flatpak starts with `flatpak run --filesystem=<profile> <id>`.
- Electron apps keep their own data and get `--remote-debugging-port=<free port>`. An app that is already running is refused, since a second instance would hand off to it; quit it first. Apps with `debugSupport: "disabled"` are refused.
- Shutdown: `Browser.close` over CDP; if the browser is still running 5 s later, a signal (`SIGTERM` to the browser's process group and main process; `taskkill /T` on Windows), then after 3 s a kill (`SIGKILL`; `taskkill /F /T`). The browser runs in its own process group (`CREATE_NEW_PROCESS_GROUP` on Windows), so a terminal Ctrl-C reaches only `slicc`.

Plain output: the CDP HTTP URL on stdout, status lines on stderr. With `--json`, NDJSON events on stdout:
- `{"event":"launched","id","name","version","engine","pid","profile","profileDir","cdp","webSocketDebuggerUrl"}` (`version`, `profile` and `profileDir` are left out when empty);
- `{"event":"closed"}` after `slicc launch` closed the browser;
- `{"event":"exited"}` when the browser exited by itself;
- `{"event":"error","message"}`, then exit 1.

## Local setup: `slicc attach`

```
slicc attach --cdp <url> [--cdp-extra <url>]... [--origin <origin>]... [--signal cdp|stdio] [--name <name>] [--allow <rule>]... [--deny <rule>]...
             [--runner <argv…> [--]] [--shell <argv…> [--]] [--eval <argv…> [--]] [--eval-quiet <dur>] [--sftp | --no-sftp] [--kernel-port <n>] [--no-kernel]
```

`attach` links a seven that is open in a running CDP browser and serves the link until it is interrupted (`SIGINT`, `SIGTERM`), or until stdin closes with `--signal stdio`. Status lines go to stderr, and `SLICC_DEBUG=1` adds diagnostics, pion's included. pion never writes to stderr itself.

**Flags.**
- `--cdp`: the browser's endpoint. `http://host:port` is looked up through `GET /json/version`, and `ws://…` is used as is. slicc talks to the browser WebSocket only and attaches to the tab with `Target.attachToTarget { flatten: true }`. It names one browser; a second `--cdp` is an error. This browser is also listed for the [`cdp`](#cdp-browser-control) capability, with `seven: true`.
- `--cdp-extra`: another browser seven may drive over the link (repeatable), in any form `slicc browsers --cdp` takes. Each must answer when `attach` starts.
- `--origin`: also accept seven on this origin (`scheme://host[:port]`), repeatable. `https://<label>.sliccy.ai` is always accepted, except `www`.
- `--signal`: `cdp` (default) or `stdio`.
- `--name`: the link's `name` and `host` in HELLO. The default name is `slicc on <host>`, with this machine's short host name, lower-cased.
- `--allow`, `--deny`: policy rules on top of the local default (see [Network policy](#network-policy)). The `--cdp` and `--cdp-extra` ports and endpoint addresses are denied.
- `--runner <argv…>`: offer compute over SSH (cap `ssh`, see [SSH server](#ssh-server)). Each command runs as `<argv…> <command>`. The argv is every word after `--runner` up to a lone `--` or the end of the command line, so `--runner bash -c` or `--runner docker exec -i box sh -c -- --name box`. An argv can't contain a lone `--` itself. `--runner=…` is refused. Without `--runner` the link has no `ssh` capability, and the flags below are refused.
- `--shell <argv…>`: the interactive form for `ssh` without a command, in the same argv syntax. The default is derived from the runner.
- `--eval <argv…>` and `--eval-quiet <duration>`: the REPL behind the `slicc-eval` subsystem (for example `--eval python3 -i`), and the silence that ends one response (default `500ms`).
- `--sftp`, `--no-sftp`: serve the `sftp` subsystem or not. The default is on only for a plain local shell as the runner.
- `--kernel-port`: the local port for [kernel services](#kernel-services) (1–65535, default `80`).
- `--no-kernel`: no kernel services; nothing listens.

**Setup over CDP.**
1. slicc picks the first page tab whose URL has an allowed origin. Until there is one it says `waiting for a seven tab` and watches for new tabs.
2. It attaches, grants the tab's origin `local-network-access`, `local-network` and `loopback-network` with `Browser.setPermission` (best effort), calls `Runtime.addBinding { name: "sliccLinkSend" }`, then `Runtime.enable`.
3. For each new default execution context of the main frame whose origin is allowed, slicc mints a nonce (16 random bytes, base64url). It then calls `globalThis.sliccLinkReceive({ "t": "hello", "v": 1, "nonce" })` in that context. The call is retried every 100 ms to 1 s until `sliccLinkReceive` is a function.
4. seven answers through `sliccLinkSend(JSON.stringify(message))`:
   - `{ "t": "offer", "nonce", "sdp" }`;
   - `{ "t": "candidate", "nonce", "candidate" }`, where `candidate` is `RTCIceCandidate.toJSON()`, or `null` at the end of gathering;
   - `{ "t": "bye", "nonce", "reason" }`.
5. slicc sends `answer`, its own `candidate`s and, when it exits, `bye` in the same shapes through `globalThis.sliccLinkReceive?.(message)`, in the same context.

**Rules for incoming setup messages.**
- A binding call counts only when it comes from that main-frame context and carries its nonce. Calls from iframes, workers, other contexts and other origins, and stale nonces, are ignored.
- Each offer starts a new link and replaces the current one.
- A navigation or reload ends the link, and the next allowed context starts the setup again with a new nonce.
- A closed tab ends the link, and slicc waits for another seven tab.

**Setup over stdio** (`--signal stdio`), for a test harness that owns CDP itself:
- The same messages go as NDJSON: slicc writes `hello`, `answer`, `candidate` and `bye` lines to stdout, and reads `offer`, `candidate` and `bye` lines from stdin. Messages with another nonce are ignored.
- The harness writes `{"t":"hello"}` to stdin to get a fresh `hello` with a new nonce, for example after it reloaded the page.
- `--cdp` is optional here. When given, it names the debugging port to deny and the `seven: true` browser for `cdp`.

**ICE.**
- No STUN or TURN, so only host candidates are used, loopback included.
- Chrome's `<uuid>.local` candidates are resolved with mDNS in query-only mode.
- The link is declared failed after 15 s without connectivity.

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

HELLO from `attach`: `{ "v": 1, "role": "node", "name", "host", "version", "mode": "local", "caps": ["net", "http", "cdp"], "routes": { "prefixes": [], "exit": true }, "policy": { "allow", "deny", "summary" }, "browsers": [ … ] }`. `cdp` and `browsers` are there when at least one browser is listed (see [`cdp`](#cdp-browser-control)). With `--runner`, `caps` adds `"ssh"` and HELLO adds `"sshHostKey": "ssh-ed25519 AAAA…"`, the host key in `authorized_keys` form, so the page can write `<host>.slicc.internal <key>` into the kernel's `known_hosts`.

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

**`http`** (cap `http`), page → node. slicc-node's raw fetch (its README, *Request* and *Response*) carried on a stream:
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

**`kernel`** (page cap `kernel-in`), node → page:
- OPEN `{ "kind": "kernel", "port" }`, sent only to a page whose HELLO lists `kernel-in`. The page dials that kernel port and answers OPENED or RESET (`ECONNREFUSED` when nothing listens). See [Kernel services](#kernel-services).
- Then raw bytes both ways: the node's first DATA is everything it has read from the browser's connection so far, request head included. END half-closes, as for `tcp`.

### `cdp` (browser control)

> **Warning:** a seven with the `cdp` capability has full control of every listed browser: its tabs, cookies, stored passwords and logged-in sessions, files it can download, and anything a page in it can reach. List only browsers you would let that seven use as you. For `follow` that is a seven on another machine.

**HELLO** lists the browsers: `"browsers": [{ "id": "chrome", "name": "Chrome (headless)", "version": "154.0.8037.98", "seven": true }]`. Every entry has all four fields.
- `id` is the browser kind, then `-2`, `-3`… for a second or third of the same kind. The kind comes from `/json/version`: `electron` for an Electron user agent, else by product `chrome` (also `HeadlessChrome`, and browsers such as Brave that report `Chrome`), `edge` (`Edg`), `opera` (`OPR`), else the product lower-cased, else `chromium`.
- An id keeps its meaning for the life of the node. A removed id is never given to another browser, and the same endpoint (CDP host and port) added again gets its old id back.
- `name` and `version` come from the endpoint's `/json/version`, as in `slicc browsers`.
- `seven: true` marks the browser that hosts this seven tab (`attach --cdp`).

**OPEN** `{ "kind": "cdp", "browser": "<id>" }`, page → node. For each stream the node reads the browser's `/json/version` again and opens a fresh WebSocket to its `webSocketDebuggerUrl`, so each stream is its own CDP client with its own sessions. A `wsOnly` browser is dialled at its websocket URL directly. Dial and handshake together get 10 s.
- **OPENED:** `{ "product", "protocolVersion" }`, the `Browser` and `Protocol-Version` fields of `/json/version` (from `Browser.getVersion` for a `wsOnly` browser).
- **RESET:** `ENOENT` for an id that isn't listed, `ECONNREFUSED` when the browser doesn't answer, `EPROTO` for an OPEN without `browser`.

**Bytes**, both directions: a sequence of messages, each a `u32` BE length plus one UTF-8 CDP JSON message of at most 64 MiB (67 108 864 bytes). The node sends each page message as one WebSocket text message, and each text message from the browser as one stream message. A length over 64 MiB from the page, or a browser message over 64 MiB, is `RESET EMSGSIZE`, and a page END in the middle of a message is `RESET EPROTO`. Browser messages that start with `{"method":"Network.webSocketFrameReceived"` or `{"method":"Network.webSocketFrameSent"` are dropped: seven runs inside a browser it may debug, so those events would carry the CDP traffic itself and grow without bound (parity with slicc-node's `/cdp`).

**Closing.**
- The browser closes the socket with a normal close (1000): the node sends END, then discards whatever the page still sends until its END.
- The browser socket fails or closes any other way: `RESET ECONNRESET`.
- END from the page: the node closes the socket with a normal close, then sends END.
- RESET from the page, or the link going down: the socket is closed at once.

**List changes.** The node checks every listed browser's debugging port every 2 s. One that fails twice in a row is removed: its open streams get `RESET ECONNRESET: browser quit`, and a later OPEN for it gets `ENOENT`. Once its port answers again it is listed again under the same id. A browser removed through the API (`Registry.Remove`, for the TUI) has its streams reset with `ECONNRESET: browser removed`.

**`browsers` updates** (page cap `browsers`), node → page. When the list changes while a link is up and the page's HELLO has `browsers` in `caps`, the node opens `OPEN { "kind": "browsers", "browsers": [ … ] }` on the control channel with the full current list in HELLO's shape. It replaces the previous list; there are no deltas. The page answers OPENED `{}` and END, and the node sends END. Updates are sent one at a time per link, a change that comes while one is in flight is sent after it, with the list as it is then, and an update the page doesn't finish within 10 s is reset with `ETIMEDOUT`. A change between the HELLO and the link coming up is sent as an update right away.

`test/integration/testdata/link/link.js` is a page-side implementation of the link (framing, credit, both channel layouts, the violation rules, and `resolve` and raw-fetch helpers) that the integration tests run in Chrome against `slicc attach`. `cdp.js` next to it is a CDP client over `cdp` streams, and `page.js` answers `browsers` updates. Loaded with `?kernel`, the test page lists `kernel-in` and answers `kernel` OPENs with `kernel.js`, a small HTTP and WebSocket echo server in the page.

### Kernel services

seven's kernel runs servers (vite, `python -m http.server`), and kernel port `N` is `http://N.kernel.localhost/`. seven's service worker carries subresources itself, but not top-level navigations or WebSockets. `*.kernel.localhost` resolves to loopback, so `attach` runs a listener that carries those connections into the page as `kernel` streams.

**Listener.**
- It binds `127.0.0.1:<kernel port>` (default `80`, which macOS lets any user bind) when the first page whose HELLO lists `kernel-in` links up, and keeps it until `attach` exits. With `--kernel-port 8080` the URLs carry the port: `http://5173.kernel.localhost:8080/`. `--no-kernel` turns it off, and remote nodes never listen.
- If the port can't be bound (something else uses it, or Linux refuses an unprivileged bind on `80`), `attach` prints `kernel services are off: cannot listen on 127.0.0.1:<port> (<reason>); try --kernel-port` and runs on without it. It tries once.
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

**Security.** The `Host` check defends against DNS rebinding. Kernel services are reachable like any dev server on localhost: by any local process, and by any web page Chrome lets reach loopback. Use `--no-kernel` to keep them inside the page.

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

**Local default** (the bridge to the browser `slicc` controls). The remote default, with loopback and the private ranges (`127.0.0.0/8`, `::1/128`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7`) allowed instead of denied, so this machine and the LAN are reachable. `--deny 127.0.0.0/8` and `--deny 192.168.0.0/16` take loopback and the LAN back out.

**Protected ports**, in both defaults. The debugging port of every browser the node serves over `cdp` (`attach --cdp` and `--cdp-extra`, `follow --cdp`) and any port the node itself listens on are denied, so a `tcp` stream can't get around the `cdp` capability:
- on loopback, each as `127.0.0.0/8:port`, `127.0.0.1/32:port` and `[::1]/128:port`, so `--allow 127.0.0.1/32` doesn't reopen them;
- on every address of this machine, as `<address>/32:port` or `<address>/128:port`. The interfaces are enumerated at check time and cached for 5 seconds, so a new interface counts. If they can't be enumerated, those ports are denied on every address.

**Browser endpoints.** Each listed `cdp` browser's own endpoints are denied too, so a browser on another machine (`--cdp http://10.0.0.5:9222 --allow 10.0.0.0/8`) is reachable only through `cdp`:
- the host and port of its CDP URL, and of the `webSocketDebuggerUrl` it reports;
- a host that is a name is resolved when the browser is added (the `--hosts` file first, then the system resolver), and every address it resolves to is denied on that port;
- while the browser is listed, its names are resolved again on every 2 s check, and new addresses are added to the old ones, so nothing is dropped while it's listed. IP literals aren't looked up again;
- when the browser is listed again after it stopped answering, its endpoints are resolved again and replace the old ones. If resolving fails, the old ones stay.

They are listed in HELLO's `policy.deny` as `<address>/32:port` or `[<address>]/128:port`.

Only a user allow for exactly the same address and port reopens one of these.

**`resolve` filtering.** An address is kept when the policy allows it on at least one port. When every address is dropped, the answer is an empty list.

**HELLO.** `policy.summary` names the default and lists the `--allow` and `--deny` rules on top of it. `policy.allow` and `policy.deny` list the `--allow` and `--deny` rules plus the protected ports' loopback denies and the browser endpoints, not the default ranges or the per-interface denies.

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

```
slicc <join-url> follow [--exit] [--allow <rule>]... [--deny <rule>]... [--name <name>] [--cdp <url>]...
             [--runner <argv…> [--]] [--shell <argv…> [--]] [--eval <argv…> [--]] [--eval-quiet <dur>] [--sftp | --no-sftp]
```

`follow` links this machine to a seven on another machine through the tray hub (see [Tray hub signaling](#tray-hub-signaling-remote-setup)). It serves the link until interrupted (`SIGINT`, `SIGTERM`) and reconnects when the link drops. Status lines go to stderr, and `SLICC_DEBUG=1` adds diagnostics. The join URL is never printed.

**Flags.**
- `--exit`: offer this machine's network to seven, as `tcp` and `resolve` streams (cap `net`) and `http` streams (cap `http`) under the remote policy, so public addresses only by default. Without `--exit`, all three are refused with `EOPNOTSUPP`.
- `--allow`, `--deny`: policy rules on top of the remote default (see [Network policy](#network-policy)).
- `--name`: the link's `name` and `host` in HELLO, as for `attach`.
- `--runner`, `--shell`, `--eval`, `--eval-quiet`, `--sftp`, `--no-sftp`: compute over SSH (cap `ssh`), exactly as for `attach`. `direct-tcpip` forwarding goes through the remote policy, so it reaches public addresses only unless `--allow` says otherwise. `--runner` works with or without `--exit`.
- `--cdp <url>`: let seven drive this browser over the link (repeatable, in any form `slicc browsers --cdp` takes; each must answer when `follow` starts). See [`cdp`](#cdp-browser-control), and its warning: the seven on the other machine gets full control of every browser listed. No browser is `seven: true`. Without `--cdp`, `follow` doesn't offer `cdp`, and a `cdp` OPEN is refused with `EOPNOTSUPP`. Each browser's debugging port and its own endpoint addresses are [denied](#network-policy) to `tcp`, `http` and `ssh` forwarding.

HELLO from `follow`: `{ "v": 1, "role": "node", "name", "host", "version", "mode": "remote", "caps", "routes": { "prefixes": [], "exit" }, "policy" }`. `caps` holds `net` and `http` with `--exit`, `ssh` with `--runner` (plus `sshHostKey`), `cdp` (and HELLO `browsers`) with `--cdp`, and always `join`.

**Identity.** The device is its DTLS certificate. `follow` keeps one ECDSA P-256 certificate per hub origin (`scheme://host:port`), valid for 20 years, in `<config>/slicc/tray-certs.json` (mode `0600`). Every link to that hub uses it, so the fingerprint stays the same across reconnects, restarts and updates. The first run on a hub mints it and says `new device, fingerprint sha-256 …`; later runs say `device, fingerprint …`. Every dial attaches with a fresh `controllerId`, which is only a routing handle.

**`join`** (cap `join`), page → node. OPEN `{ "kind": "join", "url" }` carries a rotated join URL (see Rotation below). The node answers OPENED `{}` then END, or `RESET EACCES` when the URL is not a join URL on the hub it follows.

**`unlink`** (cap `join`), page → node. OPEN `{ "kind": "unlink" }`. The node answers OPENED `{}` then END, deletes the join state and the certificate for that hub, closes the link, says `unlinked by <name>` with the name from seven's HELLO, and exits 0. A non-retryable `bootstrap.failed` with code `UNLINKED` does the same, with the name from the last link to that hub. seven sends it after the node's answer and then drops the peer connection. The next `follow` mints a new certificate, so seven sees a new device.

**Reconnects.** A link that drops, a link not up within 45 s of the offer, a failed bootstrap and any other signaling error lead to a new dial. Dials are spaced 1 s apart at first, doubling up to 30 s, each wait randomized by ±25 %, and back to 1 s after a link was up. A bootstrap the hub restarts (a retryable `bootstrap.failed`) starts a new peer connection without a new dial. The node keeps polling the bootstrap after its answer until seven's HELLO arrives, then stops. A link that closes before HELLO waits up to 3 s for a failure code from the hub, and without one it is an ordinary reconnect.

**Stops** with exit 1 and says why on `INVALID_JOIN_CAPABILITY`, `TRAY_EXPIRED` or `TRAY_SUPERSEDED_MISSING_JOIN_URL` from the hub, and on `UNTRUSTED`, `UNSUPPORTED_RUNTIME` or `NO_FINGERPRINT` (an internal error: the answer had no SHA-256 fingerprint) from seven. The device's fingerprint is always SHA-256.

## Tray hub signaling (remote setup)

A node on another machine sets up its link through the tray hub, the `slicc-tray-hub` worker on `www.sliccy.ai`. seven is the tray leader and the node is a follower. The wire contract is six's `packages/shared-ts/src/tray-signaling.ts`. The node only signals over it and never speaks six's tray-sync messages.

**Join URL.** `https://<host>/join/<token>`. Plain `http` is accepted only for loopback hosts. The join URL is the secret: the node never prints it, its token, or the token's secret part, in output, logs or errors.

**Attach.** `POST <join-url>` with `{ "controllerId", "runtime": "slicc-link/1" }`. The runtime tag lets seven tell link nodes from six's followers; the hub passes it on in `follower.join_requested`, and the node sends it again on `retry`. `wait` is retried after `retryAfterMs` (clamped to 50 ms–30 s) with the same controller id. `signal` starts a bootstrap and carries the TURN and STUN `iceServers` for the peer connection. `fail` (`INVALID_JOIN_CAPABILITY`, `TRAY_EXPIRED`) is terminal.

**Supersede.** The node does not follow HTTP redirects itself. A superseded tray's replacement is read from the `Link` header (`rel="successor-version"`), then a 3xx `Location` (with `json` dropped from its query), then the body's `result.joinUrl` when `code` is `TRAY_SUPERSEDED`. The 308 `redirect` and the older 409 `fail` shapes are both accepted. A replacement must itself be a valid join URL. Each hop uses a fresh controller id, and a dial follows at most five hops before it fails with `TRAY_SUPERSEDED_CHAIN_EXHAUSTED`. A supersede seen during a bootstrap ends it with `TRAY_SUPERSEDED`, and the next dial starts at the replacement.

**Bootstrap.** The node polls `{ "action": "poll", "controllerId", "bootstrapId", "cursor" }` once a second, and takes the leader's `bootstrap.offer` and `bootstrap.ice_candidate` events in order. It posts `answer`, then its own `ice-candidate`s. Candidates gathered before the answer is posted are held and sent right after it. A `bootstrap.failed` that is `retryable`, with `retriesRemaining` above 0, is retried with `{ "action": "retry" }` after its `retryAfterMs`. That mints a new bootstrap, so the peer connection starts over. Any other failure ends the bootstrap. When no offer arrives within 30 s, the bootstrap ends with `OFFER_TIMEOUT`.

**Rotation.** When seven creates a new join URL it doesn't supersede the old tray, so a follower learns the new URL from seven over its link. The node accepts it only on the hub origin it currently follows (same scheme, host and port), and only as a valid join URL. It stores the URL as current for the join URL it was given, keeps the running link, and dials the new URL on its next reconnect. It never prints the URL.

**Requests.** Each HTTP attempt has a 30 s timeout. A logical request gets 8 s and up to four attempts. Network errors, HTTP 5xx and non-JSON bodies are retried with a backoff of 1 s, 2 s, then 4 s. JSON refusals below 500 are not retried.

**Join URL state.** The node remembers the current join URL for each join URL it was given, so it reconnects after a network change or restart without walking the supersede chain again. The state lives in `<config>/slicc/tray-join.json`, where `<config>` is `~/Library/Application Support` on macOS, `$XDG_CONFIG_HOME` or `~/.config` on Linux, and `%AppData%` on Windows. The directory is mode `0700` and the file `0600`, written atomically. Entries are keyed by the SHA-256 of the given join URL, and at most 16 are kept.

## Development

Go 1.26 and Node ≥ 24 (for the lint tools). `npm run build` writes `bin/slicc`. `npm run lint` runs the slicc lint tools, `gofmt`, `go mod tidy -diff`, `go vet` and golangci-lint. `npm test` runs the integration tests in `test/integration/`, which build the binary and drive it. Unit tests stay next to the code and out of git (`npm run test:unit`). `npm run dist` cross-builds all six targets into `artifacts/release/` with `SHA256SUMS`, unsigned. `npm run npm:stage` then stages the npm packages into `dist/npm/`, and `npm run npm:check` packs them with `npm publish --dry-run`.

**Test names: `--hosts <file>`** (test-only, left out of `--help`). `slicc attach` and `slicc <join-url> follow` take a hosts file, so tests get deterministic names without public DNS. Each line is `<ip> <name> [<name>…]`, and `#` starts a comment. Names match case-insensitively, with or without a trailing dot, and a name on several lines gets every address, in file order. Wherever the node resolves a name (`resolve`, `tcp` by name, `http`, and the policy's [browser endpoints](#network-policy)), a name in the file gets the file's addresses of the requested family, possibly none, and never the system resolver's. Other names go to the system resolver. The policy applies afterwards as usual: `resolve` drops refused addresses, and a dial to a refused address is `EACCES`. `tcp` and `http` try the addresses in file order. A file that can't be read or parsed exits 2. A name mapped to a loopback address only helps the node's own dials, `http` and `tcp` by name: a seven kernel that gets a loopback address from `resolve` dials its own loopback, because loopback never goes out through an exit. For end-to-end exit tests, map test names to a non-loopback address of the machine.

Front ends and capabilities follow a node's links with `node.Node.Subscribe(func(node.Event)) (unsubscribe func())`. Events: `connecting` (an offer was answered), `linked` (the peer's HELLO arrived, in `Peer`), `link-down` (`Reason` when the node closed the link: seven's `bye` reason, `replaced by a new offer` or `slicc is exiting`; otherwise `Err` for a failure; a failed answer has no `Link`), and `stream-open` and `stream-close` for each page-opened stream, with its kind. Every event but a failed answer carries its `*link.Link`. A link's `link-down` comes before the next link's `connecting`, but its streams' `stream-close` can come after its `link-down`. Events are delivered synchronously on the node's and the link's goroutines, so a subscriber must return quickly and never block; one that needs to wait hands the event to its own goroutine.

The real-Chrome tests in `test/integration/` launch headless, so they run the executable directly. A windowed launch on macOS goes through `open -n -W -a`, which CI doesn't cover: on GitHub's macOS runners, Chrome started that way never writes `DevToolsActivePort`.

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

npm needs no token. `npm run trust` (fledgling) configures trusted publishing for the seven packages listed under `fledgling.include` in `package.json`. Publishing to npm is off until the repository variable `SLICC_NPM_PUBLISH` is `true`; until then a release only warns.
