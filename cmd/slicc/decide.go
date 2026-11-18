//go:build !wasip1

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
)

const decideUsage = `usage: slicc <join-url> decide <id> once|always|deny|stop [--reason <text>] [--json]
       slicc decide --cdp <url> [--origin <origin>]... <id> once|always|deny|stop [flags]
       slicc decide --signal stdio <id> once|always|deny|stop [flags]

Answer one of seven's approvals, by the id slicc approvals shows: allow it
once, always, deny it, or deny it and stop the turn that asked.

flags:
  --reason <text>    tell seven why
  --json             print one {"schema":1,"event":"decided",...} line, or an
                     error line when seven refuses
` + watchFlags + `
Exit status: 0 once seven took the answer, 1 when seven refuses it (its reason
goes to stderr) or the link can't be set up, 2 for bad usage, 3 when seven
doesn't allow this device to answer, 130 on Ctrl-C.
`

var decidedMessage = map[string]string{
	"once":   "allowed it once",
	"always": "allowed it always",
	"deny":   "denied it",
	"stop":   "denied it and stopped the turn",
}

func checkDecide(f agentFlags, aio agentIO) int {
	switch {
	case len(f.args) != 2 || f.args[0] == "":
		fmt.Fprintf(aio.stderr, "slicc decide: say which approval and how to answer: <id> once|always|deny|stop\n\n%s", decideUsage)
		return 2
	case !slices.Contains(agentctl.Answers, f.args[1]):
		fmt.Fprintf(aio.stderr, "slicc decide: answer once, always, deny or stop, not %q\n", agentctl.OneLineFull(f.args[1]))
		return 2
	}
	return -1
}

func decideLine(aio agentIO, v any) {
	b, _ := json.Marshal(v)
	_, _ = aio.stdout.Write(append(b, '\n'))
}

type decideError struct {
	Schema    int    `json:"schema"`
	Event     string `json:"event"`
	Message   string `json:"message"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

func runDecide(ctx context.Context, f agentFlags, aio agentIO) int {
	id, answer := f.args[0], f.args[1]
	links, err := f.links(ctx, aio, func(string, ...any) {})
	if err != nil {
		return agentFail(aio, "decide", err)
	}
	defer links.close()
	sctx, cancel := context.WithTimeout(ctx, agentSetupTimeout)
	defer cancel()
	l, err := links.next(sctx)
	if ctx.Err() != nil {
		return exitInterrupted
	}
	if err != nil {
		return setupFailed(ctx, aio, "decide", err)
	}
	err = agentctl.Decide(sctx, l, id, answer, f.reason)
	var refusal *agentctl.DecideRefusal
	switch {
	case ctx.Err() != nil:
		return exitInterrupted
	case errors.Is(err, agentctl.ErrNotAllowed):
		msg := refusedMessage(err)
		if f.json {
			decideLine(aio, decideError{eventSchema, "error", msg, "EACCES", false})
		}
		fmt.Fprintf(aio.stderr, "slicc decide: %s\n", msg)
		return exitNoControl
	case errors.As(err, &refusal):
		msg := agentctl.OneLineFull(refusal.Message)
		if f.json {
			decideLine(aio, decideError{eventSchema, "error", msg, refusal.Code, false})
		}
		fmt.Fprintln(aio.stderr, msg)
		return 1
	case err != nil:
		return setupFailed(ctx, aio, "decide", err)
	}
	if f.json {
		decideLine(aio, struct {
			Schema int    `json:"schema"`
			Event  string `json:"event"`
			ID     string `json:"id"`
			Answer string `json:"answer"`
		}{eventSchema, agentctl.DecidedEvent, id, answer})
		return 0
	}
	fmt.Fprintf(aio.stderr, "slicc: %s\n", decidedMessage[answer])
	return 0
}
