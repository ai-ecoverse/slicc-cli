package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
)

const exitTimedOut = 124

const waitUsage = `usage: slicc <join-url> wait [--allsettled <quiet>] [--timeout <limit>] [--json]
` + waitSynopsis + `
Wait until seven's agent has nothing left to do: no run or queued input on
the cone or on the scoops it started, and no result still to be delivered to
it. Nothing is sent to the agent. Once seven has settled, slicc says so on
stderr and exits.

flags:
  --allsettled <quiet>  also wait until nothing has come from seven for this
                        long, like 2m (default 0s)
  --timeout <limit>     give up after this long, like 30m (default: no limit)
  --json                print the raw NDJSON events on stdout, then
                        {"t":"settled"}
` + waitFlags + `
Exit status: 0 once seven has settled, 1 when seven doesn't report whether
it's busy or the link ends first, 2 for bad usage, 3 when seven doesn't allow
remote control from ` + waitExit

func runWait(ctx context.Context, f agentFlags, aio agentIO) int {
	tctx, cancel := ctx, context.CancelFunc(func() {})
	if f.timeout > 0 {
		tctx, cancel = context.WithTimeout(ctx, f.timeout)
	}
	defer cancel()
	timedOut := func() int {
		fmt.Fprintf(aio.stderr, "slicc wait: seven didn't settle within %s\n", f.timeout)
		return exitTimedOut
	}
	links, err := f.links(ctx, aio, func(string, ...any) {})
	if err != nil {
		return agentFail(aio, "wait", err)
	}
	defer links.close()
	sctx, scancel := context.WithTimeout(tctx, agentSetupTimeout)
	l, err := links.next(sctx)
	scancel()
	switch {
	case ctx.Err() != nil:
		return exitInterrupted
	case tctx.Err() != nil:
		return timedOut()
	case err != nil:
		return setupFailed(ctx, aio, "wait", err)
	case !l.Allowed():
		fmt.Fprintf(aio.stderr, "slicc wait: %s\n", noControlMessage)
		return exitNoControl
	}
	st, err := agentctl.Watch(tctx, l, agentctl.WatchOptions{Busy: true})
	switch {
	case ctx.Err() != nil:
		return exitInterrupted
	case tctx.Err() != nil:
		return timedOut()
	case errors.Is(err, agentctl.ErrNotAllowed):
		fmt.Fprintf(aio.stderr, "slicc wait: %s\n", refusedMessage(err))
		return exitNoControl
	case err != nil:
		return agentFail(aio, "wait", err)
	case st.Busy == nil:
		_ = st.Close()
		return agentFail(aio, "wait", agentctl.ErrNoBusy)
	}
	show := func(agentctl.Event) {}
	if f.json {
		show = func(e agentctl.Event) { _, _ = aio.stdout.Write(append(e.Line, '\n')) }
	}
	switch (settler{quiet: f.quiet, deadline: tctx.Done(), show: show}).run(ctx, st) {
	case settled:
		if f.json {
			_, _ = fmt.Fprint(aio.stdout, settledLine)
		}
		fmt.Fprint(aio.stderr, "slicc: seven is idle\n")
		return 0
	case settleInterrupted:
		return exitInterrupted
	case settleTimedOut:
		if ctx.Err() != nil {
			return exitInterrupted
		}
		return timedOut()
	}
	fmt.Fprint(aio.stderr, "slicc wait: lost the link to seven before it settled\n")
	return 1
}
