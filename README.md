# slicc-cli

`slicc` is SLICC's command line for seven, the SLICC on `*.sliccy.ai`. It is one binary for macOS, Windows and Linux. It launches and links browsers, and shares a machine's network, compute and browsers with a running seven over a WebRTC link. `npx sliccy` and Sliccstart.app run this binary. It replaces slicc-node and slicc-swift.

**Status:** being built. `slicc` (the default flow), `slicc attach` (with the `cdp` capability), `slicc <join-url> follow`, remote control with `prompt`, `watch` and `abort`, host folders with `--mount`, `slicc browsers`, `slicc launch`, `update`, `install`, `--version` and `--help` work so far, plus a hidden diagnostic, `slicc <join-url> follow --signal-only [--timeout 2m]`. It attaches through the tray hub, prints a summary of the leader's offer (its size, media sections, candidate count and DTLS fingerprint) and the ICE server URLs (credentials are never printed), and exits without answering. The link protocol is drafted alongside seven's link exit and moves into this README once both sides pass a shared test.

## Commands (planned)

| command | what it does |
| --- | --- |
| `slicc` | pick browsers in a small TUI; Enter launches or attaches to the best match, opens seven and links it (works now; see [The default](#the-default-slicc)) |
| `slicc attach --cdp <url>` | link a seven that is open in an already-running CDP browser (works now; see [Local setup](#local-setup-slicc-attach)) |
| `slicc launch` | launch a browser with CDP in a separate SLICC profile and print its CDP URL |
| `slicc <join-url> follow [--exit] [--runner …] [--cdp <url>] [--mount <path>]` | follow a seven's tray from another machine and offer its network, compute (over SSH), browsers or folders (`--exit`, `--runner`, `--cdp` and `--mount` work now; see [Remote setup](#remote-setup-slicc-join-url-follow)) |
| `slicc <join-url> prompt <text>`, `watch`, `abort` | send a prompt to seven's agent and stream its answer, follow the agent as it works, or abort its run; `--cdp <url>` instead of a join URL links locally (works now; see [Remote control](#remote-control-slicc-prompt-watch-and-abort)) |
| `slicc browsers --json` | list running CDP endpoints and installed browsers, ranked |
| `slicc update [--check]` | replace this binary with the latest release (works now; see [Updates](#updates)) |
| `slicc install jerk\|fnox` | install slicc-jerk or fnox into slicc's data folder (works now; see [Companion tools](#companion-tools)) |

## Install

- **npm:** `npm install -g @ai-ecoverse/slicc`, or `npx @ai-ecoverse/slicc`. The package is a small Node launcher; npm picks one of the platform packages `@ai-ecoverse/slicc-<os>-<arch>` (`darwin`, `linux`, `windows` × `amd64`, `arm64`) through `optionalDependencies` and their `os` and `cpu` fields. The launcher runs that binary with the same arguments, stdio and environment plus `SLICC_LAUNCHER=npm`, forwards `SIGTERM`, `SIGHUP`, `SIGQUIT` and `SIGUSR2` to it, and `SIGINT` too when stdin is not a terminal (a terminal's Ctrl-C already reaches the binary; on Windows the console delivers Ctrl-C and Ctrl-Break to both). It exits with the binary's exit code, or dies of the same signal. An install with `--omit=optional` has no binary, and the launcher says so. The companion tools come the same way (see [Companion tools](#companion-tools)).
- **GitHub releases:** every release of `ai-ecoverse/slicc-cli` carries `slicc-<os>-<arch>` (`.exe` on Windows) for all six targets, plus `SHA256SUMS`. See [Verifying a download](#verifying-a-download).
- **Go:** `go install github.com/ai-ecoverse/slicc-cli/cmd/slicc@latest`. See [Install with Go](#install-with-go).

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
- **New:** `SLICC_TELEMETRY_URL`, `SLICC_LAUNCHER` (set by npm), `SLICC_DISCOVERY` (tests).

**Files.**
- **Not read:** `~/.slicc/secrets.env`, `~/.slicc/session-id`, `~/.slicc/logs/` and `~/.slicc/cloud-sessions.json`.
- **Moved into seven:** `~/.slicc/crontab` and `~/.slicc/webhooks/` now live inside seven's kernel, not on the host.
- **Update cache:** six's Go follower caches its update check in `<user cache dir>/slicc/update-check.json`, and this binary uses `update-check-seven.json` next to it. Both files have the same shape, so with one shared file, each binary would show the other's version and hold back the other's check.
- **Kept by this binary:** `<config>/slicc/` holds `ssh_host_ed25519_key`, `tray-certs.json`, `tray-join.json` and `seven-profiles/`.
- **Profiles:** six's Chrome profiles are `~/Library/Application Support/Slicc/profiles/browser-coding-agent-chrome[-<port>]` on macOS, `$XDG_STATE_HOME/slicc/profiles` on Linux and `%LOCALAPPDATA%\Slicc\profiles` on Windows (`chrome-launch.ts:119-148`). seven's live in `<config>/slicc/seven-profiles`, so a SLICC `--profile <name>` never opens six's logins. Only `--profile six` does, in place; see [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile). Discovery lists six's Chrome marked `six: true`, last, and never chooses it.

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
- **Live and debuggable**, when the profile's `DevToolsActivePort` answers `/json/version` with its websocket path (usually six's 9222): slicc attaches to the running Chrome. `slicc` opens a seven tab in it (or uses one that is open) and links it, while six's node-server stays attached alongside. `slicc launch` prints its CDP URL, opens `--url` in a new tab when given, and waits.
- **Live and not debuggable:** slicc stops with exit 1: `six's Chrome is using this profile; close it, or use --browser/--profile to pick another`.
- **Free** (no lock, or a stale one): slicc launches the browser six used. That is the installed Chromium browser whose version is the profile's `Last Version` (or, without one, the version in `Local State`'s `user_experience_metrics.stability.stats_version`), the first in `slicc browsers`' id order when several match; otherwise Chrome stable. `--browser <id>` overrides it (for `slicc`, the first `--browser`; the others are extras as usual).

**Nothing is written to six's profile.** No seven prefs, no clean-exit marking, no removal of `DevToolsActivePort`, `Singleton*`, the session snapshot or any other file, no extensions added or removed, and no migration. The launch passes only runtime flags, which don't persist: `--remote-debugging-port=0 --user-data-dir=<six's profile> --no-first-run --no-default-browser-check`, the seven flags (left out with `slicc launch --no-seven-flags`), `--headless=new` with `--headless`, and the start page. A `DevToolsActivePort` that was there before the launch is ignored until Chrome writes a new one.

**What does persist** is only what Chrome itself keeps: browsing data from the session, and the seven tab in session restore, so six may reopen that tab the next time it starts Chrome.

**Going back:** quit (`q`, Ctrl-C, or `slicc launch`'s Ctrl-C), then start six as usual. A Chrome slicc launched is closed as for any launch; one slicc attached to keeps running. Nothing needs undoing: slicc keeps no record that points into six's profile.

**Output.** `slicc --profile six` skips the picker and prints plain lines (or NDJSON with `--json`). When it attaches, the `foreign-browser` notice and event follow `attached`, with `profile` `six` (or `six:<port>`): `slicc: using Chrome (profile six) at http://127.0.0.1:9222, which slicc didn't launch; opened a new seven tab. …`. A launch reports `launched` with `browser` `six`, `profile` `six` and `profileDir`. `slicc launch --profile six` prints `attached` and `detached` events when it attaches (see [`slicc launch`](#slicc-launch---browser-id---profile-name---url-url---headless---no-seven-flags---json)). Errors about six's profile go to telemetry as fixed messages, never with a path.

**In the picker** a `Chrome · SLICC six profile` row (`… six profile <port>` for a port variant) appears when that profile exists, with `SLICC six's own profile, used in place`, below every other row. It is never the best match and never preselected: Enter on it needs the cursor moved there, or the row selected with space. Picking it does the same as `--profile six`. A six endpoint in that profile is shown only through its row.

## Verifying a download

Put the binary and the release's `SHA256SUMS` in one folder, then check:
- **Checksum on macOS and Linux:** `shasum -a 256 -c SHA256SUMS --ignore-missing`. On Linux `sha256sum -c SHA256SUMS --ignore-missing` does the same.
- **Checksum on Windows (PowerShell):** `(Get-FileHash slicc-windows-amd64.exe -Algorithm SHA256).Hash.ToLower()` must equal the hash on that file's line in `SHA256SUMS` (`Select-String slicc-windows-amd64.exe SHA256SUMS`).
- **Provenance:** `gh attestation verify <file> --repo ai-ecoverse/slicc-cli` checks the GitHub build-provenance attestation (Sigstore) that the release workflow made for that exact file. `slicc update` checks the same attestation itself (see [Attestations](#attestations)).

Signing:
- **macOS:** the binaries are signed with a Developer ID, with the hardened runtime, and notarized. A bare binary can't carry a stapled ticket (only apps, disk images and installer packages can), so Gatekeeper checks the notarization online the first time it runs. Inside Sliccstart.app the binary is stapled with the app.
- **Windows:** the binaries are unsigned for now. Authenticode signing through Azure Trusted Signing comes before general availability. Until then a browser download gets a SmartScreen warning, and AppLocker, WDAC or Intune policies can block the binary.

## Companion tools

slicc works with two tools that run on this machine: slicc-jerk (`jerk`), which gets tokens for seven's vault, and fnox. slicc finds them on `PATH` or in `bin` of its data folder: `~/Library/Application Support/slicc` on macOS, `$XDG_DATA_HOME/slicc` (default `~/.local/share/slicc`) on Linux, `%LOCALAPPDATA%\slicc` on Windows. That folder is never shared over `fs` (see [Host folders](#host-folders)). When its `bin` exists, slicc appends it to its own `PATH` at start, so its children (the SSH runner, and jerk calling fnox) find the tools too; a copy already on `PATH` wins.

**With npm.** `@ai-ecoverse/slicc` and `sliccy` list `@ai-ecoverse/slicc-jerk` and `@ai-ecoverse/fnox` as `optionalDependencies` (`^` the latest release), but only those that are on the registry when the release is staged (`npm-stage.mjs --companions`); the others are left out with a notice. The publish check that waits for the platform packages doesn't wait for them. The launcher puts the `node_modules/.bin` of each installed companion package first on `PATH` for the binary, so `jerk` and `fnox` resolve for slicc and its children.

**`slicc install jerk|fnox`** installs one into the data folder's `bin`:
- `jerk` from the latest release of `ai-ecoverse/slicc-jerk` (0.3.0 or later; an older latest release is refused with `… predates the release layout slicc installs from`), `fnox` from the latest release of `ai-ecoverse/fnox-build`;
- **the release contract** (the same as slicc-cli's own): a plain binary `<name>-<os>-<arch>` (`.exe` on Windows; `darwin`, `linux`, `windows` × `amd64`, `arm64`), and `SHA256SUMS` with a line for it, with a build provenance attestation by the repo's `release.yml` over the binary or over `SHA256SUMS`;
- slicc checks the download against `SHA256SUMS` and verifies its attestation (see [Attestations](#attestations)), then installs it only once its `--version` names the release's version, through a rename next to the old one (as `slicc update` does);
- it says `installed <name> <version> at <path> (SHA256SUMS and the build provenance attestation by <identity>)`, or `<name> <version> is already installed at <path>`;
- `--checksum-only` skips the attestation, with a warning, and says `SHA256SUMS only; the attestation wasn't checked`;
- the GitHub API base follows `SLICC_UPDATE_API_BASE`, as for `update`.

Only a user runs `slicc install`. Nothing on a link starts an install.

## Attestations

`slicc update` and `slicc install` verify the GitHub build provenance attestation of every binary they download, after its `SHA256SUMS` check, and refuse it when that fails. A release without an attestation fails too. `--checksum-only` is the only way around it, and it warns.

**Where the attestation comes from.** `GET /repos/<repo>/attestations/sha256:<digest>` on the GitHub API, first for the binary's digest. When the binary has none, slicc looks up the digest of the release's `SHA256SUMS` instead: an attestation over `SHA256SUMS` covers the binary through its line there, which slicc has already checked. slicc accepts the binary when one of the bundles verifies.

**What is verified** (`internal/attest`, standard library only), for a Sigstore bundle v0.3 with one DSSE envelope, one Fulcio certificate and one Rekor (v1) entry:
1. the envelope is an in-toto statement whose predicate type is `https://slsa.dev/provenance/v1`, with a subject whose SHA-256 is the file's;
2. its single DSSE signature verifies with the certificate's key (ECDSA P-256 or P-384) over the DSSE pre-authentication encoding;
3. the certificate chains, as a code-signing certificate, to a Fulcio root in the trusted root that is valid at the entry's integrated time, and the certificate itself was valid then;
4. one of the certificate's embedded SCTs (`1.3.6.1.4.1.11129.2.4.2`) verifies with a certificate transparency log key in the trusted root, valid at the SCT's time, over the precertificate (the certificate body without its SCT list) and its issuer's key hash (RFC 6962);
5. the certificate's identity: issuer (Fulcio extension `1.3.6.1.4.1.57264.1.8`) `https://token.actions.githubusercontent.com`, and exactly one subject alternative name, the URI `https://github.com/<repo>/.github/workflows/release.yml@refs/heads/main`, pinned per repo: `ai-ecoverse/slicc-cli` for `update`, `ai-ecoverse/slicc-jerk` and `ai-ecoverse/fnox-build` for `install`;
6. the Rekor entry is a `dsse` 0.0.1 entry whose payload hash, signature and verifier are the envelope's and the certificate's;
7. its signed entry timestamp verifies with the key of a Rekor log in the trusted root, valid at the integrated time;
8. its inclusion proof leads from the entry to the proof's root hash (RFC 6962), and the checkpoint carries that tree size and root hash and is signed by the same log.

Errors name the step that failed (`attestation identity: …`). A bundle from a Rekor v2 log, or any other shape, fails closed.

**Unsupported: Rekor v2.** Bundles from Rekor v2 logs (Ed25519 log keys, `dsse` 0.0.2 entries, RFC 3161 timestamps instead of signed entry timestamps) aren't supported. They fail closed, so `update` and `install` refuse such a release until the verifier learns them; `--checksum-only` is the way around it meanwhile.

**The trusted root.** `internal/attest/trusted_root.json` is Sigstore's public-good trusted root (Fulcio CAs, Rekor and CT log keys) (the `trusted_root.json` target of `https://tuf-repo-cdn.sigstore.dev`), embedded in the binary. `SLICC_SIGSTORE_TRUSTED_ROOT` replaces it, for tests.

**Rotating the trusted root.** The *Sigstore* workflow runs daily. Its `trusted-root` job runs `node scripts/sigstore-root.mjs`, which follows the TUF repository's `timestamp.json`, snapshot and targets to the current `trusted_root.json` and compares its SHA-256 with the embedded file; when they differ, it opens an issue. To rotate:
1. run `node scripts/sigstore-root.mjs --write`, which downloads the new file and checks it against the targets hash;
2. review the diff (new Fulcio certificates or Rekor keys, `validFor` ranges) against Sigstore's announcement; the script doesn't check TUF signatures, so this review is the trust decision;
3. open a `fix:` PR, so a release ships it. Binaries released before it keep the old root, and fail closed on attestations that need the new one until they update with `--checksum-only` or by hand.

**Parity.** The workflow's `parity` job runs `TestAttestParityWithGH`: for the latest release, `gh attestation verify --repo ai-ecoverse/slicc-cli --signer-workflow …/release.yml` and slicc's verifier must both accept the binary, and both refuse it after one byte is appended.

## Updates

`slicc update [--check] [--checksum-only]` replaces the running binary (symlinks resolved) with the latest GitHub release of `ai-ecoverse/slicc-cli`. It downloads the asset for this OS and CPU, checks it against the release's `SHA256SUMS`, and verifies its build provenance attestation (see [Attestations](#attestations)); `--checksum-only` skips the attestation, with a warning. The new binary has to answer `--version` with the release's version before anything is replaced. The swap is a rename next to the old binary; on Windows the running binary is first moved aside to `<name>.old`, which the next run deletes. `slicc update --check` only reports.

**The version.** A release build has its version linked in (`-X main.version=<version>`). Without one, `slicc` reads its Go build info: a binary built by `go install …@<version>` (or `go run`) from a downloaded module, with a checksum in the build info, reports that module version without the leading `v`. Anything else built from a checkout reports `dev`.

`update` refuses, with exit code 1:
- a go-installed binary (no linked version, and a downloaded module with a checksum in its build info): it prints ``update it with `go install github.com/ai-ecoverse/slicc-cli/cmd/slicc@latest` ``;
- a development build (any version that isn't plain `X.Y.Z`); `--check` still prints the latest release;
- an npm install (`SLICC_LAUNCHER=npm`, or a path through `node_modules`), which updates through npm. The message names the commands: ``npm install -g @ai-ecoverse/slicc@latest`` and ``npx @ai-ecoverse/slicc@latest``, or `sliccy@next` for both when the binary came with `sliccy` (a `node_modules/sliccy` on its path, or next to its `node_modules`). Under npx (an `_npx` dir on the path) it names only the npx command;
- a binary inside an app bundle (`*.app/Contents/…`), which the app updates.

These checks come first, so `--check` refuses the same way and nothing asks GitHub.

Release builds outside npm and app bundles print a notice on stderr when a newer release exists: ``slicc X is available (you have Y); run `slicc update` ``. A go-installed release says ``run `go install github.com/ai-ecoverse/slicc-cli/cmd/slicc@latest` `` instead. The notice comes from a cache (`<user cache dir>/slicc/update-check-seven.json`; six's Go follower uses `update-check.json`, see [Coming from SLICC six](#coming-from-slicc-six)), and a background check refreshes it at most once every 24 hours, so no run waits on the check for more than 2 seconds. `--version`, `update`, and any command line with `--help`, `-h` or `-help` print no notice.

## Telemetry

Release builds (a plain `X.Y.Z` version, linked in or, for `go install`, from the module version) send operational telemetry through [go-optel](https://github.com/ai-ecoverse/go-optel) with the app id `slicc-cli`, sampled once per process (1 in 100 by default):
- `enter`, with the subcommand as `source`, from a fixed list (`abort`, `attach`, `browsers`, `follow`, `launch`, `prompt`, `update`, `watch`, `default` for no command (no arguments, or only flags), `join` for a bare join URL, otherwise `unknown`). No other argument is ever sent.
- `error`, for operational failures, from a fixed set of sources (`update`, `signal`, `link`, `browser`, `ssh`, `cdp`). Messages pass through go-optel's sanitizer, which strips URLs and paths.

`--version`, and any command line with `--help`, `-h` or `-help` (such as `slicc update --help`), send nothing, and development builds never configure a client.

## Environment

| variable | effect |
| --- | --- |
| `SLICC_NO_TELEMETRY=1` | send no telemetry |
| `SLICC_NO_UPDATE_CHECK=1` | no update notice and no background check |
| `SLICC_UPDATE_API_BASE` | GitHub API base for `update`, `install` and the notice (default `https://api.github.com`), for mirrors and tests |
| `SLICC_SIGSTORE_TRUSTED_ROOT` | a Sigstore `trusted_root.json` to verify attestations against instead of the embedded one, for tests (see [Attestations](#attestations)) |
| `SLICC_TELEMETRY_URL` | telemetry collector base (default `https://rum.hlx.page/`) |
| `OPTEL_RATE`, `OPTEL_DEBUG` | go-optel's sampling rate and beacon logging |
| `SLICC_LAUNCHER` | set to `npm` by the npm launcher; removed from the environment that `slicc` passes on |
| `SLICC_DISCOVERY` | test-only: limits browser discovery, see [Development](#development) |
| `SLICC_USER_DATA_DIR`, `XDG_STATE_HOME` | read, as SLICC six reads them, only to find six's Chrome profile (see [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile)) |

## The default: `slicc`

```
slicc [--browser <id>]... [--cdp <url>]... [--page <url>] [--origin <origin>]... [--profile <name> | --profile six[:<port>]] [--headless]
      [--name <name>] [--kernel-port <n>] [--no-kernel] [--allow <rule>]... [--deny <rule>]... [--mount <path>[:<name>][:ro]]...
      [--yes] [--plain] [--json]
      [--runner <argv…> [--]] [--shell <argv…> [--]] [--eval <argv…> [--]] [--eval-quiet <dur>] [--sftp | --no-sftp]
```

`slicc` with no command (`npx sliccy`) finds browsers, launches or attaches to the best one, opens seven in it and links it. It serves the link until it is told to stop: `q` or Ctrl-C in the dashboard, or `SIGINT` or `SIGTERM`. Then it says `bye` to seven, closes the browsers this run launched (as `slicc launch` does), leaves the browsers it attached to running, and exits 0. If the browser that hosts seven goes away, `slicc` closes the rest and exits 0 too. Setup failures (no browser, a launch or attach that fails) exit 1, and bad flags exit 2.

**Discovery** is `slicc browsers`' discovery and ranking (see [Browsers](#browsers)), with each `--cdp` URL added and the `--page` origin counted as a seven origin.

**Going with a browser.** The first browser hosts seven:
- a running endpoint with a seven tab open: slicc attaches to it;
- a running endpoint without one: slicc opens `--page` in a new tab (`Target.createTarget`) and attaches;
- an installed browser: slicc launches it as `slicc launch --url <page>` does, in its SLICC profile (`--profile`, or the one picked with `p`; default: the browser id), with the seven flags and prefs, and `--headless=new` with `--headless`, then attaches.

Without `--browser`, the first row of the ranking that can host seven is chosen, an endpoint slicc didn't launch included, but never one in SLICC six's profile. When that happens without the picker, slicc says so on stderr, for an endpoint that isn't in a SLICC profile and that no `--cdp` named: `slicc: using Google Chrome (profile default) at http://127.0.0.1:9222, which slicc didn't launch; opened a new seven tab. Use --browser or --cdp to choose another.` It says `found a seven tab` when one was open already. The profile is `default` for a browser's default user-data dir, the base name of the user-data dir otherwise, and left out (with its parentheses) when unknown. With `--json` the line comes as the `foreign-browser` event too. Picking a row in the picker, `--browser`, or a `--cdp` for that endpoint means the user chose it, and there is no notice. An Electron app can't host seven. Each further browser is an extra: an installed one is launched without a start page, and an endpoint is used as it is. Then the link is set up as for `slicc attach` (see [Local setup](#local-setup-slicc-attach)): over CDP with the browser that hosts seven, the first page tab on an allowed origin.

**What the link offers:** `net` (tcp, resolve), `http`, and `cdp` with the browser that hosts seven (`seven: true`) and every extra, under the local policy with all of their debugging ports protected; and kernel services on `--kernel-port` (default 80; `--no-kernel` turns them off) when seven offers `kernel-in` (see [Kernel services](#kernel-services)); and `ssh` only with `--runner` (see [SSH server](#ssh-server)); and `fs` with each `--mount` folder (see [Host folders](#host-folders)). seven gets full control of every listed browser, the one it runs in too. The `cdp` list (HELLO `browsers`) has only the browser that hosts seven and the extras picked with `--browser` or in the picker; the other browsers discovery found are never offered.

The cdp list follows the browsers: an extra that stops answering is taken off the list (`browser-gone`), and listed again with the same id when it answers on the same endpoint again (`browser-back`).

**Flags.**
- `--browser <id>`: an id from `slicc browsers` (an installed id such as `chrome`, or `cdp:<host>:<port>`), or a name, case-insensitively. Repeatable: the first hosts seven, the others are extras. Without it, the best match that can host seven. An endpoint in SLICC six's profile is refused (`<id> is SLICC six's Chrome; only --profile six uses it`).
- `--cdp <url>`: also consider this CDP endpoint, as `slicc browsers --cdp`.
- `--page <url>`: the seven page, default `https://seven.sliccy.ai/`. It must be `http` or `https`, and its origin is accepted as a seven origin.
- `--origin`, `--name`, `--allow`, `--deny`, `--kernel-port`, `--no-kernel`, `--no-jerk`, `--mount`, and the compute flags `--runner`, `--shell`, `--eval`, `--eval-quiet`, `--sftp`, `--no-sftp`: as for `slicc attach`. Without `--runner` there is no `ssh`.
- `--profile <name>`: the SLICC profile for a launched browser that hosts seven. `--profile six` and `--profile six:<port>` use SLICC six's own Chrome profile instead, without the picker (see [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile)).
- `--headless`: launch browsers without a window.
- `--yes`, `--plain`: no picker. `--json`: NDJSON events, below.
- A flag of SLICC six's points back to six and exits 2 (see [Coming from SLICC six](#coming-from-slicc-six)). Any other flag, or an argument, is an error.

**The picker** comes up when stdin and stdout are both terminals and none of `--yes`, `--browser`, `--plain` or `--json` is given. It lists the ranking best first, one row each, with its state: `CDP :<port>` and `seven open` for a running endpoint, `installed · profile <name>` (or `new SLICC profile <name>` when the profile doesn't exist yet) for a browser slicc would launch, and `installed · Electron` for an Electron app. The first row is marked `best match`. A list longer than the terminal scrolls with the cursor.

| key | what it does |
| --- | --- |
| ↑ ↓ (`k`, `j`) | move |
| space | select or unselect the row; the first selected hosts seven, and the rest are `extra cdp` browsers, in the order selected |
| Enter | go: with the selected rows, or with the highlighted row when nothing is selected |
| `p` | choose the highlighted browser's SLICC profile: its default, an existing profile in the profiles dir, or `new profile…` with a name (letters, digits, `.`, `_`, `-`) |
| Esc | back, in the profile chooser |
| `q`, Ctrl-C | quit; in the dashboard, close down as described above |

**The dashboard** follows: the linked browsers (launched or attached, their CDP port, hosts seven or extra, and `gone` while one doesn't answer), the seven tab, the link state (`waiting for seven`, `connecting`, `linked` with its uptime, or `reconnecting` after a link is lost, while slicc waits for seven to offer again), the capabilities offered, the policy summary, `compute  ssh via <runner>` with `--runner`, the kernel services URL, stream counts per kind (open and total, never contents), the last status lines, and the update notice, which then isn't printed on stderr. `NO_COLOR` turns colors off.

**Plain mode** (no terminal, `--yes`, `--browser` or `--plain`): no picker and no dashboard, just the default, with status lines on stderr (`slicc: launched …`, `slicc: attached to …`, `slicc: offering …; policy: …; compute: ssh via <runner>`, `slicc: linked with "<name>" in <tab>`, `slicc: link lost: …`, `slicc: <name> quit …`, `slicc: <name> is back …`, `slicc: closed <ids>`). It never writes ANSI escapes or cursor codes. `SLICC_DEBUG=1` adds diagnostics.

**`--json`** prints NDJSON events on stdout and nothing else there, for Sliccstart (`slicc --json --browser <id>`). Errors are also printed on stderr. Every event has `"schema": 1` and `event`. Fields marked `?` are left out when empty; new fields and events may be added, and existing ones keep their meaning.
- `discovered`: `entries`, the ranked list in the shape of `slicc browsers --json` (always a list).
- `launched`: `browser` (the id), `name`, `version?`, `role` (`seven` or `extra`), `pid`, `profile?`, `profileDir?`, `cdp`, `webSocketDebuggerUrl`.
- `attached`: `browser`, `name`, `version?`, `role`, `launched?` (`true` when this run launched it), `cdp`, `webSocketDebuggerUrl?`. For `seven` slicc is connected to it over CDP; an `extra` is listed for `cdp`.
- `foreign-browser`: `browser`, `name`, `profile?`, `cdp`, `openedTab` (`true` when slicc opened the seven tab, `false` when one was open). It follows `attached` when the browser hosting seven is an endpoint slicc didn't launch and the user didn't choose it (see above), and always when it is SLICC six's Chrome, with `profile` `six` or `six:<port>`. The same line goes to stderr.
- `linked`: `peer?` (the name in seven's HELLO), `tab?` (the seven tab's URL), `caps`, `policy` (the summary).
- `link-lost`: `reason?` when slicc or seven closed the link (`slicc is exiting`, seven's `bye` reason, `replaced by a new offer`), or `message?` for a failure. seven sets the link up again after a reload, and a new `linked` follows.
- `browser-gone`, `browser-back`: `browser`, `name`, `role`. A browser stopped answering and left the cdp list, or answers again and is back on it.
- `closed`: `browsers`, the ids slicc closed (always a list). It is the last event of every run that got past choosing its browsers.
- `error`: `message`, and `browser?` when closing that browser failed. A setup error ends the run with exit 1.

```
{"schema":1,"event":"discovered","entries":[…]}
{"schema":1,"event":"launched","browser":"chrome","name":"Google Chrome","version":"154.0.8037.98","role":"seven","pid":4242,"profile":"chrome","profileDir":"…/slicc/seven-profiles/chrome","cdp":"http://127.0.0.1:53211","webSocketDebuggerUrl":"ws://127.0.0.1:53211/devtools/browser/…"}
{"schema":1,"event":"attached","browser":"chrome","name":"Google Chrome","version":"154.0.8037.98","role":"seven","launched":true,"cdp":"http://127.0.0.1:53211","webSocketDebuggerUrl":"ws://127.0.0.1:53211/devtools/browser/…"}
{"schema":1,"event":"linked","tab":"https://seven.sliccy.ai/","peer":"seven","caps":["net","http","cdp"],"policy":"every public address, this machine, and the local network"}
{"schema":1,"event":"link-lost","reason":"slicc is exiting"}
{"schema":1,"event":"closed","browsers":["chrome"]}
```

## Browsers

### `slicc browsers [--json] [--cdp <url>]... [--no-bundle-scan]`

Lists running CDP endpoints and installed browsers, best match first. The browser tables (ids, names, bundle ids, install and user-data paths, Electron apps), the ranking tiers, the seven flags and prefs and the process-scan rules come from [slicc-browsers](https://github.com/ai-ecoverse/slicc-browsers)' `catalog.json`, which slicc-cdp's `playwright-cli` reads too. Its conformance corpus runs in `test/integration/corpus_test.go`.

**Running endpoints** come from four places:
- `DevToolsActivePort` files in the default user-data dirs of the browsers below, in each SLICC profile under the profiles dir (see `slicc launch`), in SLICC six's profiles (see [Using SLICC six's Chrome profile](#using-slicc-sixs-chrome-profile)), and on Linux in the flatpak (`~/.var/app/<id>/config/…`) and snap (`~/snap/chromium/common/chromium`) dirs. The file's port must answer `/json/version` with the websocket path on the file's second line, otherwise the file is stale and ignored. A file without a second line starting with `/` is ignored too.
- `GET /json/version` on `127.0.0.1:9222` to `9229`.
- The process list (`ps -ax -o pid= -o command=`; on Windows `Get-CimInstance Win32_Process` with `ProcessId` and `CommandLine`, as JSON): each process started with `--remote-debugging-port=<n>` or `--remote-debugging-port <n>`. Pid 0, `slicc` itself, and commands run by a wrapper (`node`, `npx`, `tsx`, `npm`, `open`, `bash`, `zsh`, `sh`, `csh`, `fish`, `dash`, `timeout`, `env`, `sudo`, `caffeinate`, `cmd`, `powershell`, `pwsh`) are skipped. Port 0 means the port is in the `DevToolsActivePort` of the process's `--user-data-dir` (its value runs to the next whitespace followed by `--` or to the end, so paths with spaces work, and a quoted value runs to the matching quote), checked as above; without one, or with one under a `slicc/profiles` folder that isn't one of six's profiles, the process is skipped. Any other port is asked for `/json/version` on `127.0.0.1`. The exact rules are in [slicc-browsers](https://github.com/ai-ecoverse/slicc-browsers#the-process-source).
- Each `--cdp` URL: `http(s)://host:port`, `ws(s)://host:port/devtools/browser/<id>`, `host:port` or a bare port. One that doesn't answer is reported on stderr and left out; the exit status stays 0.

Endpoints are merged by host and port. A `DevToolsActivePort` file wins over `--cdp`, which wins over the process list, which wins over the port probe. For each endpoint, `/json/version` gives the name, version and `webSocketDebuggerUrl`, and `/json/list` gives the seven tabs: `page` targets whose origin is `https://<label>.sliccy.ai`, other than `www` (`:443` is the same origin, any other port isn't). An endpoint's `cdp` has its host in lower case, with `localhost` and `::1` written as `127.0.0.1`. A `DevToolsActivePort` whose port is open but serves no `/json/version` (Chrome's opt-in remote debugging of a running browser) is listed as `wsOnly`, with the websocket URL from the file and no tab check.

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

- Entry: `id` (an installed id, or `cdp:<host>:<port>`), `kind` (`endpoint` or `installed`), `tier` (1–6), `name`, `version?`, and either `endpoint` or `installed`.
- `endpoint`: `cdp` (the HTTP base URL), `webSocketDebuggerUrl`, `name`, `version?`, `product?` and `userAgent?` (from `/json/version`), `source` (`devtools-active-port`, `flag`, `process` or `probe`), `browser?` (the installed id whose user-data dir held the file), `userDataDir?` (the dir that held the file, or a process's `--user-data-dir`), `profile?` (the SLICC profile name), `wsOnly?`, `six?` (`true` for SLICC six's Chrome), `sevenOpen`, `sevenTabs` (always a list).
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
- Chromium flags: `--remote-debugging-port=0 --user-data-dir=<profile> --no-first-run --no-default-browser-check`, the seven flags below, `--headless=new` with `--headless`, then `--url`.
- The seven flags keep a seven tab's kernel running while the tab is in the background: `--disable-background-timer-throttling`, `--disable-backgrounding-occluded-windows`, `--disable-renderer-backgrounding`, `--disable-background-tracing`, and `--disable-features=LocalNetworkAccessChecks,LocalNetworkAccessChecksWebSockets,IntensiveWakeUpThrottling,HighEfficiencyModeAvailable,InfiniteTabsFreezing,InfiniteTabsFreezingOnMemoryPressure,CPUMeasurementInFreezingPolicy,MemoryMeasurementInFreezingPolicy,AllowDevtoolsConnectedDiscard`. Chromium ignores feature names it doesn't know. `--no-seven-flags` (`LaunchOptions.NoSevenFlags`) leaves them all out, along with the seven prefs above. Chromium keeps only one `--disable-features` value, so every `--disable-features` from the seven flags and from `LaunchOptions.ExtraArgs` is merged into one switch, in order and without duplicates. The CDP port is read from the profile's `DevToolsActivePort` once `/json/version` answers with the same websocket path (30 s limit). On macOS a browser with a window starts through `open -n -W -a <app> --args …`, so camera and microphone permissions belong to the browser, not the terminal; `--headless` runs the executable directly. A flatpak starts with `flatpak run --filesystem=<profile> <id>`.
- Electron apps keep their own data and get `--remote-debugging-port=<port>`, the first free port from 9333 up, so they never take 9222–9229. An app that is already running is refused, since a second instance would hand off to it; quit it first. Apps with `debugSupport: "disabled"` are refused.
- Shutdown: `Browser.close` over CDP; if the browser is still running 5 s later, a signal (`SIGTERM` to the browser's process group and main process; `taskkill /T` on Windows), then after 3 s a kill (`SIGKILL`; `taskkill /F /T`). The browser runs in its own process group (`CREATE_NEW_PROCESS_GROUP` on Windows), so a terminal Ctrl-C reaches only `slicc`.

Plain output: the CDP HTTP URL on stdout, status lines on stderr. With `--json`, NDJSON events on stdout:
- `{"event":"launched","id","name","version","engine","pid","profile","profileDir","cdp","webSocketDebuggerUrl"}` (`version`, `profile` and `profileDir` are left out when empty);
- `{"event":"closed"}` after `slicc launch` closed the browser;
- `{"event":"exited"}` when the browser exited by itself;
- with `--profile six`, when six's Chrome was already running: `{"event":"attached","id","name","version","engine","profile","profileDir","cdp","webSocketDebuggerUrl"}` instead of `launched`, then `{"event":"detached"}` on Ctrl-C (six's Chrome keeps running) or `exited` when it quits;
- `{"event":"error","message"}`, then exit 1.

## Local setup: `slicc attach`

```
slicc attach --cdp <url> [--cdp-extra <url>]... [--origin <origin>]... [--signal cdp|stdio] [--name <name>] [--allow <rule>]... [--deny <rule>]...
             [--runner <argv…> [--]] [--shell <argv…> [--]] [--eval <argv…> [--]] [--eval-quiet <dur>] [--sftp | --no-sftp] [--kernel-port <n>] [--no-kernel]
             [--mount <path>[:<name>][:ro]]...
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
- `--mount <path>[:<name>][:ro]`: share this folder with seven's kernel (cap `fs`, repeatable); see [Host folders](#host-folders).
- `--kernel-port`: the local port for [kernel services](#kernel-services) (1–65535, default `80`).
- `--no-kernel`: no kernel services; nothing listens.
- `--no-jerk`: don't offer this machine's slicc-jerk (caps `jerk` and `jerk-seal`, see [`jerk`](#jerk-slicc-jerk-over-the-link)).

**Setup over CDP.**
1. slicc picks the first page tab whose URL has an allowed origin. Until there is one it says `waiting for a seven tab` and watches for new tabs.
2. It attaches, grants the tab's origin `local-network-access`, `local-network` and `loopback-network` with `Browser.setPermission` (best effort), calls `Runtime.addBinding { name: "sliccLinkSend" }`, then `Runtime.enable`.
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
- A closed tab ends the link, and slicc waits for another seven tab.
- A tab that closes while slicc attaches to it doesn't stop slicc: each setup call has 10 s, and when the tab is gone slicc looks for another seven tab.

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

HELLO from `attach`: `{ "v": 1, "role": "node", "name", "host", "version", "mode": "local", "node", "caps": ["net", "http", "credentialed", "cdp"], "routes": { "prefixes": [], "exit": true }, "policy": { "allow", "deny", "summary" }, "browsers": [ … ] }`. `cdp` and `browsers` are there when at least one browser is listed (see [`cdp`](#cdp-browser-control)). With `--runner`, `caps` adds `"ssh"` and HELLO adds `"sshHostKey": "ssh-ed25519 AAAA…"`, the host key in `authorized_keys` form, so the page can write `<host>.slicc.internal <key>` into the kernel's `known_hosts`. With `--mount`, `caps` adds `"fs"` and HELLO adds `"mounts"` (see [Host folders](#host-folders)). With host jerk, `caps` adds `"jerk"` and `"jerk-seal"` and HELLO adds `"jerk": { "version" }` (see [`jerk`](#jerk-slicc-jerk-over-the-link)).

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

**`credentialed`** (node cap, no kind of its own), on every node that serves `http`. An `http` OPEN with `"credentialed": true` carries a credential the vault injected for a transit exit (link spec §5.13). For such a stream the node:
- refuses a URL that isn't `https:` on port 443 with `RESET EACCES: credentialed requests go only to https on port 443`, before anything is dialled;
- logs the request only as its method, scheme and host, as for every `http` stream, and never its path, query, headers or body, `SLICC_DEBUG` included;
- scrubs RESET messages: they carry the error's reason, never the URL's path or query, a header or a body byte;
- follows no redirects and keeps nothing: no cache, no cookies, nothing on disk.

Any value of `credentialed` other than `false` or `null` counts as true, so a malformed flag never sends a credential out as an ordinary request.

**`kernel`** (page cap `kernel-in`), node → page:
- OPEN `{ "kind": "kernel", "port" }`, sent only to a page whose HELLO lists `kernel-in`. The page dials that kernel port and answers OPENED or RESET (`ECONNREFUSED` when nothing listens). See [Kernel services](#kernel-services).
- Then raw bytes both ways: the node's first DATA is everything it has read from the browser's connection so far, request head included. END half-closes, as for `tcp`.

### `jerk`: slicc-jerk over the link

Link spec §5.14 (J1). slicc-jerk (`jerk`, [Companion tools](#companion-tools)) gets tokens on this machine. Over the link seven can ask it for status, start sign-ins a human completes, and seal a lease into its vault. No token crosses the link except in `seal`'s `lease` event, which only seven's link layer reads and hands to the vault.

**Offered** when host jerk is found: the first `jerk` on `PATH`, else `jerk` in the data folder's `bin`, whose `jerk --version` names 0.3.0 or later.
- `slicc` and `slicc attach`: caps `jerk` and `jerk-seal`, unless `--no-jerk`.
- `follow`: only with `--jerk` (cap `jerk`) or `--jerk-seal` (both).
- `prompt`, `watch` and `abort`: never.
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
- `EPROTO`: an unknown op, a missing or malformed `provider` (`^[a-z0-9][a-z0-9-]{0,63}$`) or `lease_id`, a `duration` out of range, a `scope` that isn't printable words, or a `param` whose key isn't `^[a-z][a-z0-9_]{0,31}$` or whose value isn't printable (at most 16, 256 bytes each). Host jerk checks the keys against the provider's own `params` and exits 2 for unknown ones.
- `EACCES`: `drop` (there's no such op; `unseal` drops what jerk minted), and `seal` or `unseal` without `jerk-seal`.
- `EBUSY`: a second `create` on a link while one runs.
- `ETIMEDOUT`: a `create` still running after 2 min 30 s, which is then killed. Other ops get 60 s and then answer exit 1, `jerk timed out`.
- `ECONNRESET`: jerk couldn't start, died of a signal, or exited with a code above 5.

A page RESET or a closed link kills the jerk it started.

### `cdp` (browser control)

> **Warning:** a seven with the `cdp` capability has full control of every listed browser: its tabs, cookies, stored passwords and logged-in sessions, files it can download, and anything a page in it can reach. That includes the browser seven runs in (`seven: true`), whether slicc launched it or not: with `slicc` or `slicc attach` on your everyday browser, seven can drive all of that browser, without a further prompt. List only browsers you would let that seven use as you. For `follow` that is a seven on another machine. The one exception is seven's own origin, which the node keeps out of reach (see below).

Only browsers the user named are listed: the one seven runs in, `--cdp` and `--cdp-extra` for `attach`, the extras picked with `--browser` or in the picker for `slicc`, and `--cdp` for `follow`. A browser discovery found but nobody picked is not.

**HELLO** lists the browsers: `"browsers": [{ "id": "chrome", "name": "Chrome (headless)", "version": "154.0.8037.98", "seven": true }]`. Every entry has all four fields.
- `id` is the browser kind, then `-2`, `-3`… for a second or third of the same kind. The kind comes from `/json/version`: `electron` for an Electron user agent, else by product `chrome` (also `HeadlessChrome`, and browsers such as Brave that report `Chrome`), `edge` (`Edg`), `opera` (`OPR`), else the product lower-cased, else `chromium`.
- An id keeps its meaning for the life of the node. A removed id is never given to another browser, and the same endpoint (CDP host and port) added again gets its old id back.
- `name` and `version` come from the endpoint's `/json/version`, as in `slicc browsers`.
- `seven: true` marks the browser that hosts this seven tab (`attach --cdp`).

**OPEN** `{ "kind": "cdp", "browser": "<id>" }`, page → node. For each stream the node reads the browser's `/json/version` again and opens a fresh WebSocket to its `webSocketDebuggerUrl`, so each stream is its own CDP client with its own sessions. A `wsOnly` browser is dialled at its websocket URL directly. Dial and handshake together get 10 s.
- **OPENED:** `{ "product", "protocolVersion" }`, the `Browser` and `Protocol-Version` fields of `/json/version` (from `Browser.getVersion` for a `wsOnly` browser).
- **RESET:** `ENOENT` for an id that isn't listed, `ECONNREFUSED` when the browser doesn't answer, `EPROTO` for an OPEN without `browser`.

**Bytes**, both directions: a sequence of messages, each a `u32` BE length plus one UTF-8 CDP JSON message of at most 64 MiB (67 108 864 bytes). The node sends each page message as one WebSocket text message, and each text message from the browser as one stream message, after the filter below. A length over 64 MiB from the page, or a browser message over 64 MiB, is `RESET EMSGSIZE`, and a page END in the middle of a message is `RESET EPROTO`. Browser messages that start with `{"method":"Network.webSocketFrameReceived"` or `{"method":"Network.webSocketFrameSent"` are dropped: seven runs inside a browser it may debug, so those events would carry the CDP traffic itself and grow without bound (parity with slicc-node's `/cdp`).

**Closing.**
- The browser closes the socket with a normal close (1000): the node sends END, then discards whatever the page still sends until its END.
- The browser socket fails or closes any other way: `RESET ECONNRESET`.
- END from the page: the node closes the socket with a normal close, then sends END.
- RESET from the page, or the link going down: the socket is closed at once.

**Seven's origin is off limits.** The node relays CDP through a filter, so seven's browser control never navigates to, opens or attaches to a target on seven's own origin, and never reads or writes its cookies or storage. That holds for every listed browser, not only the one seven runs in. What is protected comes from [slicc-browsers](https://github.com/ai-ecoverse/slicc-browsers#protected-origins)' `protectedOrigins`: every `sliccy.ai` host (the apex and `www` included, any port, `https` and `http`), `vault.sliccy.ai` by name (the vault is never a CDP target for seven's tools), `blob:`, `filesystem:` and `view-source:` URLs by the URL inside them, `devtools:` URLs, and the extra origins: `--origin` for `attach`, and `--origin` and the `--page` origin for `slicc`. `follow` has none. `*.kernel.localhost` is not protected. URLs are parsed the way the browser parses them (WHATWG), and the node forwards its own re-encoding of each command, so the browser runs exactly what was checked: a key given twice counts once, as its last value, and a message that isn't a JSON object with an integer `id` and a string `method` is refused (or dropped, without an `id`).
- **Targets.** Before the stream opens, the node attaches its own session to the browser target and calls `Target.setDiscoverTargets` and `Target.getTargets` on it with ids of its own. Neither the session nor its messages ever reach the page. From then on it knows every target's URL and opener. A target on an inheriting URL (`about:blank`, `about:srcdoc`, none) is protected when it was protected before or when its opener is protected or unknown, so a blank popup that seven opens counts as seven. On a page session, the main frame's `securityOrigin` (`Page.frameNavigated`) and the default context's `origin` (`Runtime.executionContextCreated`) count too.
- **Refused**, with `{"id":…,"sessionId"?:…,"error":{"code":-32000,"message":"slicc: refused: target is on seven's origin"}}`, never forwarded: `Target.createTarget`, `Page.navigate` and anything else whose `url` is protected; `Page.navigateToHistoryEntry` to an entry whose URL is protected (the node looks it up with `Page.getNavigationHistory`); `Target.attachToTarget`, `activateTarget`, `closeTarget`, `autoAttachRelated`, `Browser.getWindowForTarget` and any other command whose `targetId` or `frameId` is a protected target; and every command on a session whose target is protected, or became so. `Target.getTargetInfo` and `Target.detachFromTarget` stay allowed.
- **Origins and storage.** A command whose `origin`, `securityOrigin`, `ownerOrigin`, `issuerOrigin`, `topLevelOrigin`, `topLevelSite`, `embeddedOrigin`, `requestingOrigin`, `scopeURL` or `manifestId` is protected, or whose `storageKey` or `cacheId` names seven's storage, is refused the same way. This covers `Storage.*`, `IndexedDB.*`, `CacheStorage.*`, `DOMStorage.*`, `FileSystem.*`, `ServiceWorker.*`, `PWA.*`, `Browser.setPermission` and `Browser.grantPermissions`. Free-form values (`expression`, `arguments`, `value`, `headers`, `postData` and the like) aren't searched.
- **Cookies.** `Network.getCookies`, `Network.getAllCookies`, `Storage.getCookies` and `Page.getCookies` answer without the cookies whose domain is protected (`sliccy.ai` and every subdomain, and each extra origin's host and its parents: cookies ignore ports), and without partitioned cookies whose `partitionKey.topLevelSite` is such a site. `Network.setCookie`, `setCookies`, `deleteCookies`, `Storage.setCookies` and `Page.deleteCookie` for such a domain, a URL on such a host, or a partition key under such a site, are refused with `slicc: refused: cookies on seven's origin`. So are `Network.clearBrowserCookies` and `Storage.clearCookies`, except in a browser context this stream created with `Target.createBrowserContext`.
- **Requests a page makes to seven.** For a request whose URL is protected, `Network.*` and `Fetch.*` events lose their `Cookie` and `Set-Cookie` headers, their raw header texts and their cookie lists, and `requestWillBeSentExtraInfo` drops seven's cookies from `associatedCookies` for any request. For a WebSocket, WebTransport or EventSource on a protected origin (`ws:` and `wss:` count as `http:` and `https:`), `webSocketWillSendHandshakeRequest`, `webSocketHandshakeResponseReceived`, `webSocketFrameError`, `eventSourceMessageReceived` and `dataReceived` are dropped. Reading such a request's body (`Network.getResponseBody`, `Fetch.getResponseBody`, `takeResponseBody…AsStream`, `Audits.getEncodedResponse` and the like) is refused. Continuing, failing or fulfilling it is allowed, except for a document request, which the node fails itself (see the next items).
- **Browser-wide commands**, refused with their own reason after `slicc: refused: `: `Target.exposeDevToolsProtocol` (it would give a page the whole browser) and `Target.sendMessageToTarget` (attach with `flatten: true` instead), on any target; `Fetch.enable` on the browser itself (a page session is fine); `Tracing.start`; `Extensions.loadUnpacked`; and `Browser.setPermission`, `Browser.grantPermissions` without an `origin` and `Browser.resetPermissions`, except in a browser context this stream created.
- **Auto-attach.** With `Target.setAutoAttach`, a session to a protected target is detached by the node at once (`Target.detachFromTarget` on its parent session), and the page never sees its `attachedToTarget` or `detachedFromTarget`.
- **Navigation by other means.** A script's `location = …`, a link, `window.open` or a server redirect isn't a command the node can refuse, so the node guards the network instead.
  - A target the stream attached to (`Target.attachToTarget`, or auto-attach that reached the page) or created (`Target.createTarget`) is *touched*, for as long as the stream is open, even after the page detached. That includes every tab the browser opens while the stream auto-attaches to new tabs.
  - On each touched page, iframe, background page or webview target the node attaches a session of its own, which the page never sees. On it, the node enables `Fetch` for document requests to protected hosts (`resourceType: Document`, request stage), and `Network` with `Network.setBypassServiceWorker`, so a service worker can't hand out a document without a request.
  - The node fails each such request into a protected origin with `Fetch.failRequest` (`BlockedByClient`), and the target shows Chrome's error page instead. Every other request continues untouched. So a touched target never loads a protected document, and nothing the stream installed on it (`Page.addScriptToEvaluateOnNewDocument`, `Fetch`, `Network`, `Page.setBypassCSP`, emulation, overrides) ever runs there.
  - Seven's own tab and its vault frame are never touched, because attaching to them is refused.
  - Page commands on a touched target's session wait until the node's own session there is ready. That takes at most 10 s; after that, or when the node's session can't be set up or is lost, they are refused with `slicc: refused: slicc can't keep seven's origin out of this target`.
  - In touched targets, service workers are bypassed for every request, not only for protected ones.
  - The guard covers every frame that shares the touched target's process. A cross-site iframe's navigation pauses in its parent's target, before the iframe becomes a target of its own, with the frame's id as `frameId` (the id the iframe target gets later). A popup's request pauses in the popup's target, with its `targetId` as `frameId`. A redirect into a protected origin pauses again at the request stage, with `redirectedRequestId`, even when the first URL didn't match.
  - The page's own `Fetch.enable` on a page session still works. Chrome chains the interceptors of a target's sessions, the most recently enabled first, so the page's interceptor could see a protected document's request before the node's does, or after the node failed it, at the response stage. The node fails such a `Fetch.requestPaused` (`resourceType: Document`, protected URL, either stage) itself, and the page never sees it.
  - When a target commits a protected origin all the same (see the limits below), the node sees it arrive (`Target.targetInfoChanged`) only after the browser committed it. A request the node failed also leaves the target on the protected URL, showing the error page. Either way the node detaches every page session on the target, drops their events and results, and refuses further commands on them; the page gets `Target.detachedFromTarget`.
- **Auto-attach of targets without a URL.** A cross-site iframe can attach before its URL is known (`url: ""`, which stays empty while it waits for the debugger), and so can a `window.open` popup. So when an auto-attached session (one that doesn't answer the page's own `Target.attachToTarget`) is to a page or iframe target with an empty URL, the node holds it:
  - The page doesn't get its `attachedToTarget` yet, its events are dropped, and page commands on it are refused.
  - If the page asked for `waitForDebuggerOnStart`, the node runs `Runtime.runIfWaitingForDebugger` on it itself.
  - Once the target has a URL (`Target.targetInfoChanged`), a protected target's session is detached without the page seeing it. Otherwise the page gets `attachedToTarget` with the current `targetInfo` and `waitingForDebugger: false`. The target has already started, so a script the page adds for new documents in such a frame or popup misses its first one.
  - A target that has no URL after 10 s is treated as protected. A session that the node does forward makes the target touched (see above).
- **Listing.** `Target.getTargets`, `Target.getTargetInfo`, `Target.targetCreated` and `Target.targetInfoChanged` still show protected targets, so seven knows its tab exists, but their `url` and `title` are only the origin (`https://seven.sliccy.ai`, or `about:blank`). Service worker registrations and versions on a protected origin are left out of `ServiceWorker` events, and `Storage`, `DOMStorage` and `BackgroundService` events that name a protected origin are dropped.
- **Limits.**
  - The filter relies on Chrome's site isolation. A seven frame inside a page of another site is a target of its own (`iframe`), which the node protects. A page the stream never touched can still frame seven, though, and once the stream touches that page, a screenshot of it shows the frame. A same-site frame shares its parent's process and isn't a target. Every `sliccy.ai` host is protected, so this only matters for an extra origin, such as one port of `127.0.0.1` framed by a page on another.
  - A document that arrives without a request isn't failed: a page restored from the back/forward cache, and a `blob:` or `filesystem:` URL of a protected origin (which only that origin can make). The node detaches after the commit, as above, and commands that were already on their way may still have run in the new page.
  - If the node loses its own session (`ECONNRESET: slicc lost track of the browser's targets`), it refuses every command and resets the stream unless the browser closes within 2 s.
  - The node's own setup connection (see [Local setup](#local-setup-slicc-attach)) is not a `cdp` stream and isn't filtered.

**List changes.** The node checks every listed browser's debugging port every 2 s. One that fails twice in a row is removed: its open streams get `RESET ECONNRESET: browser quit`, and a later OPEN for it gets `ENOENT`. Once its port answers again it is listed again under the same id. A browser removed through the API (`Registry.Remove`, for the TUI) has its streams reset with `ECONNRESET: browser removed`.

**`browsers` updates** (page cap `browsers`), node → page. When the list changes while a link is up and the page's HELLO has `browsers` in `caps`, the node opens `OPEN { "kind": "browsers", "browsers": [ … ] }` on the control channel with the full current list in HELLO's shape. It replaces the previous list; there are no deltas. The page answers OPENED `{}` and END, and the node sends END. Updates are sent one at a time per link, a change that comes while one is in flight is sent after it, with the list as it is then, and an update the page doesn't finish within 10 s is reset with `ETIMEDOUT`. A change between the HELLO and the link coming up is sent as an update right away.

`test/integration/testdata/link/link.js` is a page-side implementation of the link (framing, credit, both channel layouts, the violation rules, and `resolve` and raw-fetch helpers) that the integration tests run in Chrome against `slicc attach`. `cdp.js` next to it is a CDP client over `cdp` streams, `protect.js` probes the filter that keeps seven's origin out of reach, `fs.js` an op-shaped client for `fs` streams (`call`, `list`, `read`, `write`, `watch`), and `page.js` answers `browsers` and `mounts` updates. `page.js` keeps one link per `node` id from the hello. For tray links (no hello `node`) it replaces an older link with the same fingerprint only when HELLO's `node` matches, as seven does. `agent.js` is a fake agent behind the page's `agent` cap: the tests queue scripted runs, emit transcript events, hold and release a run, reset watch streams, and turn the cap off. Loaded with `?kernel`, the test page lists `kernel-in` and answers `kernel` OPENs with `kernel.js`, a small HTTP and WebSocket echo server in the page.

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

## Host folders

`--mount <path>[:<name>][:ro]` shares a folder of this machine with seven's kernel, which mounts it with its `hostfs` driver. It is repeatable and works on `slicc`, `slicc attach` and `slicc <join-url> follow`. This is slicc-node's hostfs (its README, *Host folders*), with the same operations and the same rules, carried on `fs` link streams instead of HTTP. There are no tokens: the link is the trust boundary, as for `ssh`, and seven decides which kernel mounts it creates. With `follow`, the folders go to the seven on the other machine.

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
- host fnox's files: `FNOX_CONFIG_DIR` and `FNOX_STATE_DIR` with fnox's own defaults (`$XDG_CONFIG_HOME/fnox` and `$XDG_STATE_HOME/fnox`, else `~/.config/fnox` and `~/.local/state/fnox`, or `~\AppData\Local\fnox` on Windows), `FNOX_AGE_KEY_FILE`, and the daemon's socket folders.

The paths are read from slicc's own environment at start. A `--mount` of one of them, or of a folder inside one, is skipped with `--mount <spec>: slicc never shares this folder, which holds its own keys or a credential tool's files, skipping`. In a folder that contains one, every operation on a private path or below it is `EACCES` with `slicc never shares this path`. That holds when the path is reached through a symlink, through a Windows 8.3 short name, or, on macOS and Windows, typed with a different case. A `rename`, `rmdir`, `unlink` or `symlink` of a folder that holds a private path (`.config`, for example) is refused too, so a private folder can't be moved out of the fence or replaced. `list` still shows a private folder's name. `watch` drops a change inside a private folder before it is batched, so it adds no line, no parent folder and no `all`.

**HELLO.** `caps` lists `fs`, and `"mounts"` lists the folders:

```json
"mounts": [{ "name": "project", "readonly": false,
             "capabilities": { "maxIo": 16777216, "symlinks": true, "chmod": true,
                               "caseInsensitive": true, "normalization": "nfd-insensitive", "ranges": true } }]
```

- `capabilities` is slicc-node's grant answer, so seven hands it to the driver unchanged.
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

**Operations**, with slicc-node's arguments and answers:

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

**Mount updates** (page cap `mounts`). When folders are added or removed while a link is up (`fs.Server.Add` and `Remove`, for the TUI), the node opens `OPEN { "kind": "mounts", "mounts": [ … ] }` on the control channel, with the full list in HELLO's shape. It replaces the previous list, and there are no deltas. The page answers OPENED `{}` and END, and the node sends END. Updates go one at a time per link, and one the page doesn't finish within 10 s is reset with `ETIMEDOUT`. A removed folder's handles close (`EBADF` if the name comes back), its operations are `ENOENT`, and a watch stream covering it ends with `RESET ENOENT: no such folder`. `--mount` alone never changes the set.

**Errors.** The code is the POSIX name, `EIO` when there is none. The message is libuv's lower-case text for it (`no such file or directory`), or slicc-node's own for the cases it names (`path escapes the folder`, `read-only folder`, `file changed since it was opened`, `the folder root cannot be moved or removed`). It never contains a host path. Windows errors are mapped the way libuv maps them, for example `ERROR_ACCESS_DENIED` to `EPERM` and `ERROR_SHARING_VIOLATION` to `EBUSY`.

**Windows.**
- Paths go through `\\?\` and `GetFinalPathNameByHandle`, so device names (`CON`, `NUL`) and 8.3 short names are plain names inside the folder.
- Files are opened with `FILE_SHARE_DELETE` and without following reparse points.
- Symlinks and junctions both show as `kind: "symlink"`, `readlink` works on both, and `unlink` removes the link itself.
- `ino` is the file index, and `mode` is `0666`, or `0444` for a read-only file, plus `0111` for a directory. `setattr` `mode` sets or clears the read-only attribute.
- `unlink` clears the read-only attribute first, as `DeleteFile` would refuse otherwise.
- `rename` gives the POSIX answers above, replacing an empty directory itself.

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
             [--mount <path>[:<name>][:ro]]... [--jerk] [--jerk-seal]
```

`follow` links this machine to a seven on another machine through the tray hub (see [Tray hub signaling](#tray-hub-signaling-remote-setup)). It serves the link until interrupted (`SIGINT`, `SIGTERM`) and reconnects when the link drops. Status lines go to stderr, and `SLICC_DEBUG=1` adds diagnostics. The join URL is never printed.

**Flags.**
- `--exit`: offer this machine's network to seven, as `tcp` and `resolve` streams (cap `net`) and `http` streams (cap `http`) under the remote policy, so public addresses only by default. Without `--exit`, all three are refused with `EOPNOTSUPP`.
- `--allow`, `--deny`: policy rules on top of the remote default (see [Network policy](#network-policy)).
- `--name`: the link's `name` and `host` in HELLO, as for `attach`.
- `--runner`, `--shell`, `--eval`, `--eval-quiet`, `--sftp`, `--no-sftp`: compute over SSH (cap `ssh`), exactly as for `attach`. `direct-tcpip` forwarding goes through the remote policy, so it reaches public addresses only unless `--allow` says otherwise. `--runner` works with or without `--exit`.
- `--cdp <url>`: let seven drive this browser over the link (repeatable, in any form `slicc browsers --cdp` takes; each must answer when `follow` starts). See [`cdp`](#cdp-browser-control), and its warning: the seven on the other machine gets full control of every browser listed. No browser is `seven: true`. Without `--cdp`, `follow` doesn't offer `cdp`, and a `cdp` OPEN is refused with `EOPNOTSUPP`. Each browser's debugging port and its own endpoint addresses are [denied](#network-policy) to `tcp`, `http` and `ssh` forwarding.
- `--mount <path>[:<name>][:ro]`: share this folder with the seven on the other machine (cap `fs`, repeatable); see [Host folders](#host-folders). It can read the folder, and change it unless `:ro`.
- `--jerk`: offer this machine's slicc-jerk for status and sign-ins (cap `jerk`). `--jerk-seal` also lets the seven on the other machine seal this machine's tokens into its vault (cap `jerk-seal`) and implies `--jerk`. Without host jerk, `follow` says `jerk: no slicc-jerk 0.3.0 or later on PATH or in slicc's data folder; not offering it` and runs on without it. See [`jerk`](#jerk-slicc-jerk-over-the-link).

HELLO from `follow`: `{ "v": 1, "role": "node", "name", "host", "version", "mode": "remote", "node", "caps", "routes": { "prefixes": [], "exit" }, "policy" }`. `caps` holds `net` and `http` with `--exit`, `ssh` with `--runner` (plus `sshHostKey`), `cdp` (and HELLO `browsers`) with `--cdp`, `fs` (and HELLO `mounts`) with `--mount`, `jerk` (and HELLO `jerk`) with `--jerk`, `jerk-seal` with `--jerk-seal`, and always `join`.

**Identity.** The device is its DTLS certificate. `follow` keeps one ECDSA P-256 certificate per hub origin (`scheme://host:port`), valid for 20 years, in `<config>/slicc/tray-certs.json` (mode `0600`). Every link to that hub uses it, so the fingerprint stays the same across reconnects, restarts and updates. The first run on a hub mints it and says `new device, fingerprint sha-256 …`; later runs say `device, fingerprint …`. Every dial attaches with a fresh `controllerId`, which is only a routing handle.

**`join`** (cap `join`), page → node. OPEN `{ "kind": "join", "url" }` carries a rotated join URL (see Rotation below). The node answers OPENED `{}` then END, or `RESET EACCES` when the URL is not a join URL on the hub it follows.

**`unlink`** (cap `join`), page → node. OPEN `{ "kind": "unlink" }`. The node answers OPENED `{}` then END, deletes the join state and the certificate for that hub, closes the link, says `unlinked by <name>` with the name from seven's HELLO, and exits 0. A non-retryable `bootstrap.failed` with code `UNLINKED` does the same, with the name from the last link to that hub. seven sends it after the node's answer and then drops the peer connection. The next `follow` mints a new certificate, so seven sees a new device.

**Reconnects.** A link that drops, a link not up within 45 s of the offer, a failed bootstrap and any other signaling error lead to a new dial. Dials are spaced 1 s apart at first, doubling up to 30 s, each wait randomized by ±25 %, and back to 1 s after a link was up. A bootstrap the hub restarts (a retryable `bootstrap.failed`) starts a new peer connection without a new dial. The node keeps polling the bootstrap after its answer until seven's HELLO arrives, then stops. A link that closes before HELLO waits up to 3 s for a failure code from the hub, and without one it is an ordinary reconnect.

**Stops** with exit 1 and says why on `INVALID_JOIN_CAPABILITY`, `TRAY_EXPIRED` or `TRAY_SUPERSEDED_MISSING_JOIN_URL` from the hub, and on `UNTRUSTED`, `UNSUPPORTED_RUNTIME` or `NO_FINGERPRINT` (an internal error: the answer had no SHA-256 fingerprint) from seven. The device's fingerprint is always SHA-256.

## Remote control: `slicc prompt`, `watch` and `abort`

```
slicc <join-url> prompt [--steer | --reject] [--thinking] [--json] [--plain] [--abort-on-interrupt] <text>|@file|-
slicc <join-url> watch [--thinking] [--json] [--plain]
slicc <join-url> abort
slicc prompt --cdp <url> [--origin <origin>]... [flags] <text>|@file|-
slicc watch --cdp <url> [--origin <origin>]... [flags]
slicc abort --cdp <url> [--origin <origin>]...
slicc prompt|watch|abort --signal stdio [flags] …
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

**`watch`** follows the agent until Ctrl-C or `SIGTERM`, then exits 0.
- **Output:** `user` events go to stdout as `> <text>`, on one line. Assistant text goes to stdout as for `prompt`, and a line ends at each run's end. Everything else goes to stderr, as for `prompt`, plus `run ended: <reason>` for a run that didn't end with `stop`. `--json` prints the events instead.
- **Status lines** on stderr: `slicc: watching <name>`, `slicc: lost the link to <name>; reconnecting`, and for the tray `<reason>; trying again in <wait>`.
- **Reconnects:** when the stream ends or is reset, slicc opens a new `watch` on the same link. When the link goes, it sets up a new one, through the tray again or by waiting for seven over CDP. Waits start at 1 s, double up to 30 s with ±25 % jitter, and go back to 1 s once a stream opens.
- **Resuming:** each new stream asks for `since`, the id of the last event slicc saw, so nothing still in the transcript is lost. Nothing is shown twice either. For `text` and `thinking`, slicc counts the bytes it has shown per entry and skips that many from the entry's replay. Any other entry is skipped when its id was already seen. `run` events have no id and aren't replayed. When seven answers `gap: true`, slicc prints `some events were missed`.
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

**`watch`:** `OPEN { "kind": "agent", "op": "watch", "since"?: "<entry id>", "thinking"?: true }` on its own channel.
- **Answer:** OPENED `{ "head": "<entry id>", "gap"?: true }`, then NDJSON events until either side resets.
- **Without `since`:** the events start at the next one.
- **With `since`:** the page first replays `since`'s own entry when it's `text` or `thinking` (its whole text so far, as one `delta`), then every entry after it. `gap: true` means the transcript no longer holds `since`, and the events start at the next one.

**`abort`:** `OPEN { "kind": "agent", "op": "abort" }`, answered OPENED `{ "aborted": true | false }` (`false` when nothing was running), then END.

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
| `{"t":"end","reason","message"?}` | the last line of a `prompt` stream |

- **Ids:** `id` is the transcript entry id, and `delta` lines for one `id` arrive in order.
- **Limits:** `summary` is at most 200 characters. Credentials and tool output beyond the summary are never sent.
- **Unknown events:** the node ignores events and fields it doesn't know.

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

Go 1.26 and Node ≥ 24 (for the lint tools). `npm run build` writes `bin/slicc`. `npm run lint` runs the slicc lint tools, `gofmt`, `go mod tidy -diff`, `go vet` and golangci-lint. `npm test` runs the integration tests in `test/integration/`, which build the binary and drive it. Unit tests stay next to the code and out of git (`npm run test:unit`). `go build` and `npm run build` link no version, so these binaries report `dev`. `npm run dist` cross-builds all six targets into `artifacts/release/` with `SHA256SUMS`, unsigned. `npm run npm:stage` then stages the npm packages into `dist/npm/`, and `npm run npm:check` packs them with `npm publish --dry-run`.

**Test names: `--hosts <file>`** (test-only, left out of `--help`). `slicc attach` and `slicc <join-url> follow` take a hosts file, so tests get deterministic names without public DNS. Each line is `<ip> <name> [<name>…]`, and `#` starts a comment. Names match case-insensitively, with or without a trailing dot, and a name on several lines gets every address, in file order. Wherever the node resolves a name (`resolve`, `tcp` by name, `http`, and the policy's [browser endpoints](#network-policy)), a name in the file gets the file's addresses of the requested family, possibly none, and never the system resolver's. Other names go to the system resolver. The policy applies afterwards as usual: `resolve` drops refused addresses, and a dial to a refused address is `EACCES`. `tcp` and `http` try the addresses in file order. A file that can't be read or parsed exits 2. A name mapped to a loopback address only helps the node's own dials, `http` and `tcp` by name: a seven kernel that gets a loopback address from `resolve` dials its own loopback, because loopback never goes out through an exit. For end-to-end exit tests, map test names to a non-loopback address of the machine.

Front ends and capabilities follow a node's links with `node.Node.Subscribe(func(node.Event)) (unsubscribe func())`. Events: `connecting` (an offer was answered), `linked` (the peer's HELLO arrived, in `Peer`), `link-down` (`Reason` when the node closed the link: seven's `bye` reason, `replaced by a new offer` or `slicc is exiting`; otherwise `Err` for a failure; a failed answer has no `Link`), and `stream-open` and `stream-close` for each page-opened stream, with its kind. Every event but a failed answer carries its `*link.Link`. A link's `link-down` comes before the next link's `connecting`, but its streams' `stream-close` can come after its `link-down`. Events are delivered synchronously on the node's and the link's goroutines, so a subscriber must return quickly and never block; one that needs to wait hands the event to its own goroutine.

**Test isolation: `SLICC_DISCOVERY`** (test-only). When it is set, browser discovery (`slicc browsers`, the default flow, and `browser.Discover` in the same process) never probes ports 9222–9229, never reads the process list, and finds only `--cdp` URLs, plus:
- `isolated`, or any value not listed here: nothing else;
- `installed`: installed browsers too;
- `home`: installed browsers, and the `DevToolsActivePort` files under the home and config dirs from the environment (`HOME`, `APPDATA`, `LOCALAPPDATA`, `XDG_CONFIG_HOME`), SLICC six's profiles included (`XDG_STATE_HOME`, `SLICC_USER_DATA_DIR`).

In the same process, probe ports, dirs and a process list passed in explicitly (`Options.ProbePorts`, `Options.Home`, `Options.ProfilesDir`, and `Options.Processes`, which returns the output of the process-list command) are still used. `Options.Transport` and `Options.Dial` replace discovery's HTTP transport and TCP dial; the corpus test fakes every server with them, and its process list is fake too. `test/integration`'s `TestMain` sets `SLICC_DISCOVERY=isolated` and unsets `SLICC_USER_DATA_DIR`, and points `HOME`, `APPDATA` and the XDG dirs, `XDG_STATE_HOME` included (and `USERPROFILE` and `LOCALAPPDATA` except on Windows, where a Chrome started with temporary ones never writes `DevToolsActivePort`) at a temporary directory for itself and every `slicc` it starts, so no test can reach the browsers of the machine it runs on. Tests opt in to more with a fake home or fake endpoints. The Chrome the harness starts itself gets the real environment.

**SLICC six's profile in tests.** The `--profile six` tests build fake six profile trees in a fake home only: each OS's layout, a mixed-case name, a symlinked profile and root, `SingletonLock` (or `lockfile`) live, stale and absent, and debuggable endpoints that are fakes or the harness's own headless Chrome on a free port. They check the trees byte for byte after a launch, an attach and an exit. A fake browser (`testdata/fakechrome`) stands in for Chrome where a real one would write to the profile itself. The one real-Chrome launch into a fake six profile checks that nothing was seeded; on Windows it puts that profile at `SLICC_USER_DATA_DIR` in a temporary dir, since that Chrome keeps the real `LOCALAPPDATA` (see below).

**Port guard.** On CI (`CI` set), or with `SLICC_TEST_PORT_GUARD=1`, `TestMain` holds `127.0.0.1:9222` to `9229` (and `[::1]` where it can) for the whole run, and the run fails if anything connects to them, other than the one test that lends a port to its own fake browser. On CI a port it can't hold fails the run. Off CI it is off by default, so a developer's own Chrome on those ports is never touched; the guarded tests then skip.

On macOS, a headless Chrome started with `HOME` pointed at a temporary dir may never load a page (seen on a developer Mac, not on CI). So on macOS the test that has `slicc` launch Chrome for seven runs it with the real `HOME` and a unique `--profile`, which it deletes afterwards.

On GitHub's Windows runners the system drive `C:` is slow: with Chrome profiles there, Chrome sometimes waited seconds before a new tab's first request (up to past the 15 s page-load limit) or before exiting (`did not exit`). With them on `D:`, the runner's temp drive, those stalls weren't seen and the suite ran about a quarter faster. So CI points `TEMP` and `TMP` at the runner's temp dir, and the tests' temporary homes and Chrome profiles live there. When a test page doesn't load, the failure says where it stopped: the tab's URL and `readyState`, the resources it fetched, and every request the page server saw.

The real-Chrome tests in `test/integration/` launch headless, so they run the executable directly. A windowed launch on macOS goes through `open -n -W -a`, which CI doesn't cover: on GitHub's macOS runners, Chrome started that way never writes `DevToolsActivePort`.

## Releases

semantic-release cuts releases from `main` in `.github/workflows/release.yml`, on a macOS runner:
1. `scripts/release-plan.mjs` asks semantic-release (dry run) for the next version, and stops when there is none.
2. `scripts/package.sh <version>` builds `darwin`, `linux` and `windows` × `amd64`, `arm64` with `CGO_ENABLED=0`, `-trimpath` and `-X main.version=<version>`. It signs and notarizes the darwin binaries and writes `SHA256SUMS`. It would sign the windows binaries with jsign against Azure Trusted Signing, but that configuration isn't complete yet, so releases ship them unsigned for now. Without a group's signing secrets (forks, pull requests, Windows today) it warns and leaves those binaries unsigned.
3. `scripts/npm-stage.mjs` stages `@ai-ecoverse/slicc` and the six platform packages into `dist/npm/`. It also stages the same launcher as `sliccy` (with two bins, `sliccy` and `slicc`, and a short preview README) into `dist/npm-next/`, as version `7.0.0-next.<version>`, with exact `optionalDependencies` on this release's platform packages. `npx sliccy@next` runs the bin named after the package, `sliccy`, and both bins run the same launcher. `--bin <name>` (repeatable) overrides the bins of any staged launcher.
4. `actions/attest-build-provenance` attests every binary.
5. semantic-release checks the version again (`dist/VERSION`, and `scripts/go-major.mjs`, see below), tags, creates the GitHub release with every binary and `SHA256SUMS`, then `scripts/npm-publish.mjs` publishes the platform packages and the launcher through npm trusted publishing (OIDC, with provenance), and then `sliccy` with `--tag next`. Versions already on npm are skipped. Before publishing `sliccy`, the script checks that every exact-pinned `optionalDependencies` version is on the registry, waits up to `SLICC_REGISTRY_WAIT_SECONDS` (default 900) for freshly published ones to appear, and fails otherwise, so a `sliccy` version never points at missing platform packages.
6. `scripts/go-warm.sh <version>`, once the tag is on `origin`, asks `proxy.golang.org` for the module at `v<version>` (`go list -m` with a scratch module cache) and asks `pkg.go.dev` to fetch it. The proxy fetch is retried briefly; the `pkg.go.dev` fetch is one attempt of at most 10 s, since the proxy fetch already gets the version indexed. Both only warn, and the step can't fail the release.

**Go module versions.** In Go a pushed tag is the release: `proxy.golang.org` caches it, `sum.golang.org` records its hash for good, and `pkg.go.dev` indexes it.
- **Never move or delete a pushed tag.** The checksum database would then reject that version for every user. A bad version gets a `retract` directive in `go.mod`, and a new release.
- **Major versions.** A module at v2 or above needs `/vN` at the end of its module path, or `go install …@latest` never sees it. `scripts/go-major.mjs <version>` fails the release when the version's major doesn't fit the module path in `go.mod` (no suffix allows v0 and v1, `/vN` allows only vN). `release-plan.mjs` runs the same check, so it fails before the build. A `BREAKING CHANGE` commit therefore needs a deliberate module-path change first.

**Recovering `sliccy@next`.** When a release tagged and published its platform packages but `sliccy` didn't go out (a registry that lagged past the wait, say), `gh workflow run release.yml --ref main -f sliccy_next_only=true` publishes it without a new release. On `main` only, that run skips the release job and runs `scripts/sliccy-next.mjs`. The script takes the latest `vX.Y.Z` tag reachable from `HEAD`, stages `sliccy@7.0.0-next.<version>` with the launcher (`npm/slicc.js`) from that tag and exact `optionalDependencies` on that version's platform packages, and publishes it through `npm-publish.mjs` with `--tag next`. The same guards apply: the `SLICCY_NEXT_PUBLISH` gate, the tag and version checks, the registry check, and a version that is already published is skipped. Only the latest release can be recovered, so `next` never moves backwards.

On pull requests the same workflow runs as a dry run: an unsigned build of all six targets, the npm packages and `sliccy` (with `--tag next`) packed with `--dry-run`, `scripts/sliccy-next.mjs --dry-run` (the recovery path against the latest real release, registry check included), and each binary run with `--version` on its own OS and CPU.

The workflow reads these repository secrets. Each group is optional, and a missing group leaves its binaries unsigned:
- macOS signing: `APPLE_CERTIFICATE_BASE64`, `APPLE_DEVELOPER_CERTIFICATE_PASSWORD`;
- notarization: `APPLE_API_KEY_ID`, `APPLE_API_KEY_ISSUER_ID`, `APPLE_API_KEY_P8_BASE64`;
- Windows signing: `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_TRUSTED_SIGNING_ENDPOINT`, `AZURE_TRUSTED_SIGNING_ACCOUNT`, `AZURE_TRUSTED_SIGNING_PROFILE`, and `AZURE_CLIENT_SECRET` unless the app registration trusts this workflow through a federated credential (OIDC).

npm needs no token. `npm run trust` (fledgling) configures trusted publishing for the seven packages listed under `fledgling.include` in `package.json`. Publishing to npm is off until the repository variable `SLICC_NPM_PUBLISH` is `true`, and `sliccy` stays off until `SLICCY_NEXT_PUBLISH` is `true`; until then a release skips them with a notice.

`sliccy` only ever goes to the `next` dist-tag, so `npx sliccy` keeps running SLICC six and `npx sliccy@next` runs this binary. `npm-publish.mjs` refuses, even in a dry run, to publish `sliccy` with any tag but `next` or with a version that doesn't match `^7\.0\.0-next\.`. Nothing here changes `sliccy`'s other dist-tags.
