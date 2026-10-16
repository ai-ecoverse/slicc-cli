//go:build !wasip1

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

const launchUsage = `usage: slicc launch [--browser <id>] [--profile <name>] [--url <url>] [--headless] [--no-seven-flags] [--kill] [--json]

Launch a browser with CDP in a separate SLICC profile, print its CDP URL and
keep running until interrupted. On exit, close the browser it started.

  --browser <id>     an installed browser or Electron app id from
                     "slicc browsers" (default: the best installed browser)
  --profile <name>   profile directory name (default: the browser id)
  --profile six[:<port>]
                     SLICC six's own Chrome profile, used in place: attach
                     to six's Chrome when it runs with CDP, else launch the
                     browser six used (or --browser) in it
  --url <url>        page to open at start
  --headless         run without a window
  --no-seven-flags   leave out the flags that keep a background seven tab
                     running (see README.md)
  --kill             an Electron app that is already running is quit and
                     started again with remote debugging
  --json             print NDJSON events instead of plain text
`

type launchFlags struct {
	browserID string
	profile   string
	url       string
	headless  bool
	noSeven   bool
	kill      bool
	json      bool
	six       *int
}

func parseLaunch(args []string, stdout, stderr io.Writer) (launchFlags, int, bool) {
	var f launchFlags
	fs := newFlagSet("launch", stderr, launchUsage)
	fs.StringVar(&f.browserID, "browser", "", "")
	fs.StringVar(&f.profile, "profile", "", "")
	fs.StringVar(&f.url, "url", "", "")
	fs.BoolVar(&f.headless, "headless", false, "")
	fs.BoolVar(&f.noSeven, "no-seven-flags", false, "")
	fs.BoolVar(&f.kill, "kill", false, "")
	fs.BoolVar(&f.json, "json", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, launchUsage)
			return f, 0, false
		}
		return f, 2, false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "slicc launch: unexpected argument %q\n\n%s", fs.Arg(0), launchUsage)
		return f, 2, false
	}
	port, six, err := browser.ParseSixProfile(f.profile)
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "slicc launch: %v\n", err)
		return f, 2, false
	case six:
		f.six, f.profile = &port, ""
	case f.profile != "" && !browser.ValidProfileName(f.profile):
		fmt.Fprintf(stderr, "slicc launch: invalid profile name %q\n", f.profile)
		return f, 2, false
	}
	return f, 0, true
}

type launchEvent struct {
	Event        string `json:"event"`
	ID           string `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	Version      string `json:"version,omitempty"`
	Engine       string `json:"engine,omitempty"`
	PID          int    `json:"pid,omitempty"`
	Profile      string `json:"profile,omitempty"`
	ProfileDir   string `json:"profileDir,omitempty"`
	CDP          string `json:"cdp,omitempty"`
	WebSocketURL string `json:"webSocketDebuggerUrl,omitempty"`
	Message      string `json:"message,omitempty"`
	Elapsed      int    `json:"elapsed,omitempty"`
}

type launchOut struct {
	json   bool
	stdout io.Writer
	stderr io.Writer
}

func (o launchOut) event(e launchEvent) {
	if o.json {
		b, _ := json.Marshal(e)
		fmt.Fprintf(o.stdout, "%s\n", b)
	}
}

func (o launchOut) slow(in browser.Installed) func(time.Duration) {
	return func(elapsed time.Duration) {
		secs := int(elapsed / time.Second)
		o.event(launchEvent{Event: "browser-slow", ID: in.ID, Name: in.Name, Elapsed: secs})
		if !o.json {
			fmt.Fprintf(o.stderr, "slicc: %s\n", flow.SlowLine(in.Name, secs))
		}
	}
}

func (o launchOut) fail(err error) int {
	if o.json {
		o.event(launchEvent{Event: "error", Message: err.Error()})
	}
	fmt.Fprintf(o.stderr, "slicc launch: %v\n", err)
	return 1
}

func pickInstalled(ctx context.Context, id string) (browser.Installed, error) {
	installed := browser.DiscoverInstalled(ctx, browser.Options{})
	if id != "" {
		if in, ok := browser.FindInstalled(installed, id); ok {
			return in, nil
		}
		return browser.Installed{}, fmt.Errorf("no installed browser %q; see slicc browsers", id)
	}
	if in, ok := browser.BestInstalled(browser.Rank(nil, installed)); ok {
		return in, nil
	}
	return browser.Installed{}, errors.New("no Chromium browser found")
}

func runLaunch(args []string, stdout, stderr io.Writer) int {
	f, code, ok := parseLaunch(args, stdout, stderr)
	if !ok {
		return code
	}
	out := launchOut{json: f.json, stdout: stdout, stderr: stderr}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	ctx := context.Background()
	if f.six != nil {
		return runLaunchSix(ctx, f, out, sigs)
	}
	in, err := pickInstalled(ctx, f.browserID)
	if err != nil {
		return out.fail(err)
	}
	lctx, launched := interruptible(ctx, sigs)
	b, err := browser.Launch(lctx, in, browser.LaunchOptions{Profile: f.profile, URL: f.url, Headless: f.headless, NoSevenFlags: f.noSeven, Kill: f.kill, Slow: out.slow(in)})
	pending := launched()
	if err != nil {
		return out.launchFailed(err, pending, in)
	}
	return serveLaunched(ctx, f, out, pending, in, b)
}

func interruptible(ctx context.Context, sigs <-chan os.Signal) (context.Context, func() <-chan os.Signal) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	finished := make(chan struct{})
	got := make(chan os.Signal, 1)
	go func() {
		defer close(finished)
		select {
		case s := <-sigs:
			got <- s
			cancel()
		case <-done:
		}
	}()
	return ctx, func() <-chan os.Signal {
		close(done)
		<-finished
		cancel()
		if len(got) > 0 {
			return got
		}
		return sigs
	}
}

func (o launchOut) launchFailed(err error, sigs <-chan os.Signal, in browser.Installed) int {
	if len(sigs) == 0 {
		return o.fail(err)
	}
	o.event(launchEvent{Event: "closed"})
	if !o.json {
		fmt.Fprintf(o.stderr, "slicc: interrupted while %s was starting; closed it\n", in.Name)
	}
	return 0
}

func serveLaunched(ctx context.Context, f launchFlags, out launchOut, sigs <-chan os.Signal, in browser.Installed, b *browser.Browser) int {
	stdout, stderr := out.stdout, out.stderr
	out.event(launchEvent{
		Event: "launched", ID: in.ID, Name: in.Name, Version: in.Version, Engine: string(in.Engine), PID: b.PID,
		Profile: b.Profile, ProfileDir: b.ProfileDir, CDP: b.CDP, WebSocketURL: b.WebSocketURL,
	})
	if !f.json {
		fmt.Fprintln(stdout, b.CDP)
		fmt.Fprintf(stderr, "slicc: launched %s; Ctrl-C closes it\n", describeLaunch(in, b))
	}
	select {
	case <-b.Done():
		out.event(launchEvent{Event: "exited"})
		if !f.json {
			fmt.Fprintf(stderr, "slicc: %s exited\n", in.Name)
		}
		return 0
	case <-sigs:
	}
	closeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := b.Close(closeCtx); err != nil {
		return out.fail(err)
	}
	out.event(launchEvent{Event: "closed"})
	if !f.json {
		fmt.Fprintf(stderr, "slicc: closed %s\n", in.Name)
	}
	return 0
}

func describeLaunch(in browser.Installed, b *browser.Browser) string {
	s := in.Name
	if in.Version != "" {
		s += " " + in.Version
	}
	if b.ProfileDir != "" {
		s += " (profile " + b.ProfileDir + ")"
	}
	return s
}
