package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

const defaultSignalOnlyTimeout = 2 * time.Minute

func looksLikeURL(arg string) bool {
	return strings.Contains(arg, "://")
}

func joinCommand(args []string, stdout, stderr io.Writer) int {
	if _, err := tray.ParseJoinURL(args[0]); err != nil {
		fmt.Fprintf(stderr, "slicc: %v\n", err)
		return 2
	}
	if len(args) < 2 || args[1] != "follow" {
		fmt.Fprint(stderr, "slicc: a join URL must be followed by a command, like follow\n")
		return 2
	}
	return follow(args[0], args[2:], stdout, stderr)
}

func follow(joinURL string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("follow", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	signalOnly := fs.Bool("signal-only", false, "")
	timeout := fs.Duration("timeout", defaultSignalOnlyTimeout, "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "slicc follow: %v\n", err)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprint(stderr, "slicc follow: unexpected arguments\n")
		return 2
	}
	if !*signalOnly {
		fmt.Fprint(stderr, "slicc follow: not available yet\n")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	return signalOnlyRun(ctx, joinURL, stdout, stderr)
}

func signalOnlyRun(ctx context.Context, joinURL string, stdout, stderr io.Writer) int {
	state, err := tray.OpenDefaultState()
	if err != nil {
		fmt.Fprint(stderr, "slicc follow: no config directory, the join URL is not remembered\n")
		state = nil
	}
	f, err := tray.NewFollower(tray.Config{
		JoinURL: joinURL,
		State:   state,
		Logf:    func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) },
	})
	if err != nil {
		fmt.Fprintf(stderr, "slicc follow: %v\n", err)
		return 2
	}
	fail := func(err error) int {
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("timed out waiting for an offer")
		}
		fmt.Fprintf(stderr, "slicc follow: %s\n", f.Redact(err.Error()))
		return 1
	}
	sess, err := f.Dial(ctx)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = sess.Close() }()
	if f.Redirected() {
		fmt.Fprint(stdout, "join url: superseded, the replacement is remembered\n")
	}
	fmt.Fprintf(stdout, "attached: bootstrap attempt %d\n", sess.Attempt())
	for {
		ev, err := sess.Recv(ctx)
		if err != nil {
			return fail(err)
		}
		switch ev.Kind {
		case tray.EventRestart:
			fmt.Fprintf(stdout, "restart: bootstrap attempt %d\n", ev.Attempt)
		case tray.EventOffer:
			fmt.Fprintf(stdout, "offer: %s\n", tray.SummarizeSDP(ev.SDP))
			printICEServers(stdout, sess.ICEServers())
			return 0
		}
	}
}

func printICEServers(w io.Writer, servers []tray.ICEServer) {
	fmt.Fprintf(w, "ice servers: %d\n", len(servers))
	for _, s := range servers {
		auth := ""
		if s.Username != "" || s.Credential != "" {
			auth = " (with credentials)"
		}
		fmt.Fprintf(w, "  %s%s\n", strings.Join(s.URLs, " "), auth)
	}
}
