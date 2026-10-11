package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	ossignal "os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

const (
	agentSetupTimeout = time.Minute
	exitNoControl     = 3
	exitInterrupted   = 130
	noControlMessage  = "this seven doesn't allow remote control from this device; turn it on in seven's Linked devices"
)

type agentFlags struct {
	verb             string
	joinURL          string
	cdp              string
	signal           string
	origins          []string
	plain            bool
	json             bool
	thinking         bool
	steer            bool
	reject           bool
	abortOnInterrupt bool
	args             []string
}

type agentIO struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func agentUsage(verb string) string {
	switch verb {
	case "prompt":
		return promptUsage
	case "watch":
		return watchUsage
	default:
		return abortUsage
	}
}

func parseAgent(verb, joinURL string, args []string, aio agentIO) (agentFlags, int) {
	f := agentFlags{verb: verb, joinURL: joinURL, signal: "cdp"}
	usage := agentUsage(verb)
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if joinURL == "" {
		fs.StringVar(&f.cdp, "cdp", "", "")
		fs.Var((*multi)(&f.origins), "origin", "")
		fs.StringVar(&f.signal, "signal", "cdp", "")
	}
	if verb != "abort" {
		fs.BoolVar(&f.plain, "plain", false, "")
		fs.BoolVar(&f.json, "json", false, "")
		fs.BoolVar(&f.thinking, "thinking", false, "")
	}
	if verb == "prompt" {
		fs.BoolVar(&f.steer, "steer", false, "")
		fs.BoolVar(&f.reject, "reject", false, "")
		fs.BoolVar(&f.abortOnInterrupt, "abort-on-interrupt", false, "")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(aio.stdout, usage)
			return f, 0
		}
		fmt.Fprintf(aio.stderr, "slicc %s: %v\n\n%s", verb, err, usage)
		return f, 2
	}
	f.args = fs.Args()
	if code := f.checkSignal(aio); code >= 0 {
		return f, code
	}
	switch {
	case verb != "prompt" && len(f.args) > 0:
		fmt.Fprintf(aio.stderr, "slicc %s: unexpected arguments\n\n%s", verb, usage)
		return f, 2
	case verb == "prompt" && len(f.args) == 0:
		fmt.Fprintf(aio.stderr, "slicc prompt: say what to send: <text>, @file or -\n\n%s", usage)
		return f, 2
	case f.steer && f.reject:
		fmt.Fprintf(aio.stderr, "slicc prompt: --steer and --reject don't go together\n")
		return f, 2
	case joinURL == "" && f.cdp == "" && f.signal != "stdio":
		fmt.Fprintf(aio.stderr, "slicc %s: give a join URL (slicc <join-url> %s) or --cdp <url>\n\n%s", verb, verb, usage)
		return f, 2
	}
	return f, -1
}

func (f *agentFlags) checkSignal(aio agentIO) int {
	switch {
	case f.signal != "cdp" && f.signal != "stdio":
		fmt.Fprintf(aio.stderr, "slicc %s: --signal must be cdp or stdio\n", f.verb)
		return 2
	case f.signal != "stdio":
		return -1
	case len(f.args) == 1 && (f.args[0] == "-" || f.args[0] == "@-"):
		fmt.Fprintf(aio.stderr, "slicc %s: with --signal stdio, stdin carries the setup, so the prompt can't come from it\n", f.verb)
		return 2
	}
	f.json = true
	return -1
}

func readTextArg(args []string, stdin io.Reader) (string, error) {
	if len(args) == 1 {
		switch a := args[0]; {
		case a == "-" || a == "@-":
			b, err := io.ReadAll(io.LimitReader(stdin, agentctl.MaxPrompt+1))
			if err != nil {
				return "", fmt.Errorf("reading stdin: %w", err)
			}
			return strings.TrimRight(string(b), "\r\n"), nil
		case strings.HasPrefix(a, "@"):
			b, err := readLimited(a[1:])
			if err != nil {
				return "", err
			}
			return strings.TrimRight(string(b), "\r\n"), nil
		}
	}
	return strings.Join(args, " "), nil
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, agentctl.MaxPrompt+1))
}

func detectStyle(w io.Writer, plain bool) agentctl.Style {
	if plain || !isTerminal(w) || os.Getenv("TERM") == "dumb" {
		return agentctl.Style{}
	}
	color := os.Getenv("NO_COLOR") == "" && enableVT(w)
	return agentctl.Style{TTY: true, Color: color}
}

func (f agentFlags) renderer(aio agentIO, users, runs bool) *agentctl.Renderer {
	return agentctl.NewRenderer(agentctl.RenderOptions{
		Out: aio.stdout, Err: aio.stderr,
		OutStyle: detectStyle(aio.stdout, f.plain), ErrStyle: detectStyle(aio.stderr, f.plain),
		Thinking: f.thinking, Users: users, Runs: runs,
	})
}

func agentLogf(stderr io.Writer) func(string, ...any) {
	if os.Getenv("SLICC_DEBUG") == "" {
		return func(string, ...any) {}
	}
	return func(format string, a ...any) { fmt.Fprintf(stderr, "slicc debug: "+format+"\n", a...) }
}

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

type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func runAgent(verb, joinURL string, args []string, aio agentIO) int {
	f, code := parseAgent(verb, joinURL, args, aio)
	if code >= 0 {
		return code
	}
	if f.signal == "stdio" {
		aio.stdout = &lineWriter{w: aio.stdout}
	}
	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch verb {
	case "prompt":
		return runPrompt(ctx, f, aio)
	case "watch":
		return runWatch(ctx, f, aio)
	default:
		return runAbort(ctx, f, aio)
	}
}

func agentFail(aio agentIO, verb string, err error) int {
	fmt.Fprintf(aio.stderr, "slicc %s: %v\n", verb, err)
	return 1
}

func setupFailed(ctx context.Context, aio agentIO, verb string, err error) int {
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		err = fmt.Errorf("no seven answered within %s", agentSetupTimeout)
	}
	return agentFail(aio, verb, err)
}
