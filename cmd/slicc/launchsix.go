//go:build !wasip1

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

var errNoSixBrowser = errors.New("no Chrome to open SLICC six's profile with; install Chrome or name one with --browser")

func pickSixBrowser(ctx context.Context, id, dir string) (browser.Installed, error) {
	installed := browser.DiscoverInstalled(ctx, browser.Options{})
	if id == "" {
		if in, ok := browser.SixBrowser(dir, installed); ok {
			return in, nil
		}
		return browser.Installed{}, errNoSixBrowser
	}
	in, ok := browser.FindInstalled(installed, id)
	if !ok {
		return browser.Installed{}, fmt.Errorf("no installed browser %q; see slicc browsers", id)
	}
	if in.Engine != browser.EngineChromium {
		return browser.Installed{}, fmt.Errorf("%s can't open SLICC six's Chrome profile", in.Name)
	}
	return in, nil
}

func runLaunchSix(ctx context.Context, f launchFlags, out launchOut, sigs <-chan os.Signal) int {
	six := browser.SixProfileFor(browser.Options{}, *f.six)
	if err := browser.SixExists(six.Dir); err != nil {
		return out.fail(err)
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	ep, err := browser.ProbeSix(pctx, browser.Options{}, six.Dir)
	cancel()
	if err != nil {
		return out.fail(err)
	}
	if ep != nil {
		return attachSix(ctx, f, out, sigs, six, *ep)
	}
	in, err := pickSixBrowser(ctx, f.browserID, six.Dir)
	if err != nil {
		return out.fail(err)
	}
	b, err := browser.LaunchSix(ctx, in, six, browser.LaunchOptions{URL: f.url, Headless: f.headless, NoSevenFlags: f.noSeven, Slow: out.slow(in)})
	if err != nil {
		return out.fail(err)
	}
	return serveLaunched(ctx, f, out, sigs, in, b)
}

func attachSix(ctx context.Context, f launchFlags, out launchOut, sigs <-chan os.Signal, six browser.Six, ep browser.Endpoint) int {
	if f.url != "" {
		if err := openTab(ctx, ep, f.url); err != nil {
			return out.fail(err)
		}
	}
	out.event(launchEvent{
		Event: "attached", ID: browser.EndpointID(ep), Name: ep.Name, Version: ep.Version, Engine: string(browser.EngineChromium),
		Profile: six.Label, ProfileDir: six.Dir, CDP: ep.CDP, WebSocketURL: ep.WebSocketURL,
	})
	if !f.json {
		fmt.Fprintln(out.stdout, ep.CDP)
		fmt.Fprintf(out.stderr, "slicc: attached to SLICC six's Chrome at %s, which slicc didn't launch and leaves running; Ctrl-C detaches\n", ep.CDP)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-sigs:
			out.event(launchEvent{Event: "detached"})
			if !f.json {
				fmt.Fprintln(out.stderr, "slicc: detached; six's Chrome keeps running")
			}
			return 0
		case <-tick.C:
			if !browser.SixHeld(six.Dir) {
				out.event(launchEvent{Event: "exited"})
				if !f.json {
					fmt.Fprintln(out.stderr, "slicc: six's Chrome exited")
				}
				return 0
			}
		}
	}
}

func openTab(ctx context.Context, ep browser.Endpoint, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := cdp.Dial(ctx, ep.WebSocketURL)
	if err != nil {
		return fmt.Errorf("attach to SLICC six's Chrome: %w", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Call(ctx, "", "Target.createTarget", map[string]any{"url": url}, nil); err != nil {
		return fmt.Errorf("open %s: %w", url, err)
	}
	return nil
}
