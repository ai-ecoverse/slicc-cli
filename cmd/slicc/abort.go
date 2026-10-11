package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
)

const abortUsage = `usage: slicc <join-url> abort
       slicc abort --cdp <url> [--origin <origin>]...
       slicc abort --signal stdio

Abort the run seven's agent is working on, if any.

flags:
  --cdp <url>        link over CDP instead of a join URL
  --origin <origin>  with --cdp, also accept seven on this origin
  --signal stdio     carry the local setup messages as NDJSON on stdin
                     and stdout instead of CDP (for a test harness);
                     --cdp is then not needed

Exit status: 0 when the run was aborted or nothing was running, 1 on an
error, 2 for bad usage, 3 when seven doesn't allow remote control from this
device, 130 on Ctrl-C.
`

func runAbort(ctx context.Context, f agentFlags, aio agentIO) int {
	links, err := f.links(ctx, aio, func(string, ...any) {})
	if err != nil {
		return agentFail(aio, "abort", err)
	}
	defer links.close()
	sctx, cancel := context.WithTimeout(ctx, agentSetupTimeout)
	defer cancel()
	l, err := links.next(sctx)
	if ctx.Err() != nil {
		return exitInterrupted
	}
	if err != nil {
		return setupFailed(ctx, aio, "abort", err)
	}
	aborted, err := agentctl.Abort(sctx, l)
	switch {
	case ctx.Err() != nil:
		return exitInterrupted
	case errors.Is(err, agentctl.ErrNotAllowed):
		fmt.Fprintf(aio.stderr, "slicc abort: %s\n", noControlMessage)
		return exitNoControl
	case err != nil:
		return setupFailed(ctx, aio, "abort", err)
	case aborted:
		fmt.Fprint(aio.stderr, "slicc: aborted the run\n")
	default:
		fmt.Fprint(aio.stderr, "slicc: nothing was running\n")
	}
	return 0
}
