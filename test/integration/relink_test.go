package integration

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

func TestFlowRelinksWithNewCapsInTheSameTab(t *testing.T) {
	srv := pageServer(t)
	pageURL := srv.URL + "/"
	b, p := launchWithPage(t, pageURL)
	events := make(chan flow.Event, 256)
	relink := make(chan flow.Relink, 1)
	var mu sync.Mutex
	registered := 0
	cfg := flow.Config{
		Page: pageURL, Name: "it-relink", Version: "test", Relink: relink,
		Register: func(_ context.Context, n *node.Node, _ *policy.Policy, _ []flow.Browser) (flow.Caps, error) {
			tcp.Register(n)
			mu.Lock()
			registered++
			mu.Unlock()
			return flow.Caps{}, nil
		},
		Emit: func(e flow.Event) { events <- e },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	sel := []browser.Entry{{ID: "it", Kind: browser.KindEndpoint, Name: "Chrome", Endpoint: &browser.Endpoint{CDP: b.endpoint}}}
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
	next := func(want flow.Kind) flow.Event {
		t.Helper()
		timeout := time.After(30 * time.Second)
		for {
			select {
			case e := <-events:
				if e.Event == want {
					return e
				}
				if e.Event == flow.Error {
					t.Fatalf("error while waiting for %s: %s", want, e.Message)
				}
			case <-timeout:
				t.Fatalf("no %s event", want)
			}
		}
	}
	next(flow.Linked)
	echoOverLink(t, p)
	var before struct {
		Node string `json:"node"`
	}
	p.eval("linkTest.hello()", &before)

	relink <- flow.Relink{Compute: "ssh via sh -c"}
	if lost := next(flow.LinkLost); lost.Reason != flow.RelinkReason {
		t.Fatalf("link-lost reason %q", lost.Reason)
	}
	if s := next(flow.Serving); s.Compute != "ssh via sh -c" {
		t.Fatalf("serving compute %q", s.Compute)
	}
	up := next(flow.Linked)
	if up.Tab != pageURL || !slices.Contains(up.Caps, "net") {
		t.Fatalf("linked again %+v", up)
	}
	echoOverLink(t, p)
	var after struct {
		Node string `json:"node"`
	}
	p.eval("linkTest.hello()", &after)
	if after.Node == "" || after.Node != before.Node {
		t.Fatalf("node id changed across the relink: %q → %q", before.Node, after.Node)
	}
	mu.Lock()
	n := registered
	mu.Unlock()
	if n != 2 {
		t.Fatalf("Register ran %d times", n)
	}
	if !b.answers() {
		t.Fatal("the browser stopped answering after the relink")
	}
}
