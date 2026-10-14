package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
)

const promptUsage = `usage: slicc <join-url> prompt [flags] <text>|@file|-
` + promptSynopsis + `
Send a prompt to seven's agent and stream its answer. The assistant's text
goes to stdout as it streams; tool calls, results, notices and errors go to
stderr, styled on a terminal and plain otherwise. @file reads the prompt from
a file, and - (or @-) from stdin. The words after the flags are joined with
spaces.

` + promptHow + `
flags:
  --steer               if seven is busy, steer the running turn with it
                        (default: queue it as a follow-up)
  --reject              if seven is busy, don't send it; exit 1
  --thinking            also show the agent's thinking, on stderr
  --json                print the raw NDJSON events on stdout instead
  --plain               no styling, even on a terminal
` + promptFlags + `
Exit status: 0 when the run ends normally, 1 on an error, an aborted run or a
lost link, 2 for bad usage, 3 when seven doesn't allow remote control from
` + promptExit

func runPrompt(ctx context.Context, f agentFlags, aio agentIO) int {
	text, err := readTextArg(f.args, aio.stdin)
	if err == nil {
		err = agentctl.CheckPrompt(text)
	}
	if err != nil {
		fmt.Fprintf(aio.stderr, "slicc prompt: %v\n", err)
		return 2
	}
	r := f.renderer(aio, false, false)
	links, err := f.links(ctx, aio, func(string, ...any) {})
	if err != nil {
		return agentFail(aio, "prompt", err)
	}
	defer links.close()
	sctx, cancel := context.WithTimeout(ctx, agentSetupTimeout)
	l, err := links.next(sctx)
	cancel()
	if ctx.Err() != nil {
		return exitInterrupted
	}
	if err != nil {
		return setupFailed(ctx, aio, "prompt", err)
	}
	if !l.Allowed() {
		fmt.Fprintf(aio.stderr, "slicc prompt: %s\n", noControlMessage)
		return exitNoControl
	}
	opts := agentctl.PromptOptions{Text: text, WhenBusy: agentctl.FollowUp, Thinking: f.thinking}
	switch {
	case f.steer:
		opts.WhenBusy = agentctl.Steer
	case f.reject:
		opts.WhenBusy = agentctl.Reject
	}
	st, err := agentctl.Prompt(ctx, l, opts)
	if ctx.Err() != nil {
		return exitInterrupted
	}
	if err != nil {
		return promptOpenFailed(aio, err)
	}
	return streamPrompt(ctx, f, aio, r, l, st)
}

func promptOpenFailed(aio agentIO, err error) int {
	switch {
	case errors.Is(err, agentctl.ErrNotAllowed):
		fmt.Fprintf(aio.stderr, "slicc prompt: %s\n", refusedMessage(err))
		return exitNoControl
	case errors.Is(err, agentctl.ErrBusy):
		fmt.Fprint(aio.stderr, "slicc prompt: seven is busy, so the prompt wasn't sent (leave out --reject to queue it)\n")
		return 1
	}
	return agentFail(aio, "prompt", err)
}

type promptResult struct {
	code int
	msg  string
}

func streamPrompt(ctx context.Context, f agentFlags, aio agentIO, r *agentctl.Renderer, l agentctl.Link, st *agentctl.Stream) int {
	done := make(chan promptResult, 1)
	go func() { done <- readPrompt(f, aio, r, st) }()
	select {
	case res := <-done:
		if !f.json {
			r.Finish()
		}
		if res.msg != "" {
			fmt.Fprintf(aio.stderr, "slicc prompt: %s\n", res.msg)
		}
		return res.code
	case <-ctx.Done():
		_ = st.Close()
		if !f.json {
			r.Finish()
		}
		if f.abortOnInterrupt {
			actx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := agentctl.Abort(actx, l)
			cancel()
			if err != nil {
				fmt.Fprintf(aio.stderr, "slicc prompt: could not abort the run: %v\n", err)
			} else {
				fmt.Fprint(aio.stderr, "slicc prompt: aborted the run\n")
			}
		}
		return exitInterrupted
	}
}

func readPrompt(f agentFlags, aio agentIO, r *agentctl.Renderer, st *agentctl.Stream) promptResult {
	for {
		e, err := st.Next()
		if err != nil {
			return promptStreamError(err)
		}
		if f.json {
			_, _ = aio.stdout.Write(append(e.Line, '\n'))
		}
		if e.T == agentctl.EventEnd {
			return promptEnd(e)
		}
		if !f.json {
			r.Render(e)
		}
	}
}

func promptEnd(e agentctl.Event) promptResult {
	switch e.Reason {
	case agentctl.EndStop:
		return promptResult{code: 0}
	case agentctl.EndAborted:
		return promptResult{code: 1, msg: "the run was aborted"}
	}
	msg := "the run failed"
	if m := agentctl.OneLineFull(e.Message); m != "" {
		msg += ": " + m
	}
	return promptResult{code: 1, msg: msg}
}

func promptStreamError(err error) promptResult {
	switch {
	case errors.Is(err, agentctl.ErrBusy):
		return promptResult{code: 1, msg: "seven is busy, so the prompt wasn't sent (leave out --reject to queue it)"}
	case errors.Is(err, io.EOF):
		return promptResult{code: 1, msg: "seven ended the stream before the run ended"}
	}
	return promptResult{code: 1, msg: "lost the link to seven; the run goes on there"}
}
