package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

func TestFlowKeepsPendingRelinkSettingsWhenCDPDrops(t *testing.T) {
	f := newCrashBrowser(t)
	relink := make(chan flow.Relink, 1)
	events := make(chan flow.Event, 256)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	cfg := flow.Config{
		Page: crashSevenURL, Name: "it-relink-drop", Version: "test",
		Compute: "ssh via bash -c", Relink: relink, Chosen: true,
		Emit: func(e flow.Event) { events <- e },
	}
	id := "cdp:" + strings.TrimPrefix(f.srv.URL, "http://")
	sel := []browser.Entry{{
		ID: id, Kind: browser.KindEndpoint, Name: "Crashy", Version: "1.0",
		Endpoint: &browser.Endpoint{CDP: f.srv.URL},
	}}
	go func() { done <- flow.Go(ctx, cfg, sel) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("flow.Go: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("flow.Go did not return")
		}
	})
	nextServing := func() flow.Event {
		t.Helper()
		timeout := time.After(30 * time.Second)
		for {
			select {
			case e := <-events:
				if e.Event == flow.Serving {
					return e
				}
				if e.Event == flow.Error {
					t.Fatalf("error while waiting for serving: %s", e.Message)
				}
			case <-timeout:
				t.Fatal("no serving event")
			}
		}
	}
	first := nextServing()
	if first.Compute != "ssh via bash -c" {
		t.Fatalf("first serving compute %q", first.Compute)
	}
	f.waitHello(t, 1)
	relink <- flow.Relink{Kernel: 7, Compute: "ssh via sh -c"}
	f.drop()
	second := nextServing()
	if second.Compute != "ssh via sh -c" {
		t.Fatalf("serving after the drop kept %q, want the pending relink", second.Compute)
	}
	f.waitHello(t, 2)
}
