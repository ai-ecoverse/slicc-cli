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

## Development

Go 1.26 and Node ≥ 24 (for the lint tools). `npm run build` writes `bin/slicc`. `npm run lint` runs the slicc lint tools, `gofmt`, `go mod tidy -diff`, `go vet` and golangci-lint. `npm test` runs the integration tests in `test/integration/`, which build the binary and drive it. Unit tests stay next to the code and out of git (`npm run test:unit`). Releases are cut by semantic-release on `main`.
