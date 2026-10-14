//go:build !wasip1

package main

import (
	"context"

	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

const (
	interruptAborts = true

	promptSynopsis = `       slicc prompt --cdp <url> [--origin <origin>]... [flags] <text>|@file|-
       slicc prompt --signal stdio [flags] <text>|@file
`
	promptHow = `With a join URL, slicc links to that seven through the tray hub as this
device; seven must allow remote control from it (Linked devices). With --cdp,
slicc links to the seven tab in that CDP browser, as slicc attach does. The
link offers nothing to seven.
`
	promptFlags = `  --abort-on-interrupt  abort the run on Ctrl-C (by default it goes on)
  --cdp <url>           link over CDP instead of a join URL
  --origin <origin>     with --cdp, also accept seven on this origin
  --signal stdio        carry the local setup messages as NDJSON on stdin
                        and stdout instead of CDP (for a test harness);
                        stdout also carries the events, as with --json
`
	promptExit = `this device, 130 on Ctrl-C.
`

	watchSynopsis = `       slicc watch --cdp <url> [--origin <origin>]... [--plain] [--json] [--thinking]
       slicc watch --signal stdio [--thinking]
`
	watchFlags = `  --cdp <url>        link over CDP instead of a join URL
  --origin <origin>  with --cdp, also accept seven on this origin
  --signal stdio     carry the local setup messages as NDJSON on stdin
                     and stdout instead of CDP (for a test harness);
                     stdout also carries the events, as with --json
`
	watchExit = `0 on Ctrl-C, 1 when the link can't be set up for good, 2 for bad
usage, 3 when seven doesn't allow remote control from this device.
`

	abortSynopsis = `       slicc abort --cdp <url> [--origin <origin>]...
       slicc abort --signal stdio
`
	abortFlags = `
flags:
  --cdp <url>        link over CDP instead of a join URL
  --origin <origin>  with --cdp, also accept seven on this origin
  --signal stdio     carry the local setup messages as NDJSON on stdin
                     and stdout instead of CDP (for a test harness);
                     --cdp is then not needed
`
)

func (f agentFlags) links(ctx context.Context, aio agentIO, status func(string, ...any)) (agentLinks, error) {
	logf := agentLogf(aio.stderr)
	if f.joinURL != "" {
		return newRemoteLinks(f.joinURL, logf, status)
	}
	origins, err := signal.NewOrigins(f.origins)
	if err != nil {
		return nil, err
	}
	if f.signal == "stdio" {
		return newStdioLinks(aio.stdin, aio.stdout, logf)
	}
	return newLocalLinks(ctx, f.cdp, origins, logf)
}
