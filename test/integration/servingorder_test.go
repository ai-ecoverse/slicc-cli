package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

func TestFlowWatchesBrowsersOnlyAfterServing(t *testing.T) {
	const page = "https://test.sliccy.ai/"
	seven := newFakeCDPBrowser(t, func(method string, _ json.RawMessage) (string, []string) {
		if method == "Target.getTargets" {
			return fmt.Sprintf(`{"targetInfos":[{"targetId":"seven","type":"page","url":%q}]}`, page), nil
		}
		return "{}", nil
	})
	id := "cdp:" + strings.TrimPrefix(seven.srv.URL, "http://")
	var serving, early atomic.Bool
	watched := make(chan struct{}, 1)
	events := make(chan flow.Event, 256)
	cfg := flow.Config{
		Page: page, Name: "it-serving-order", Version: "test",
		Register: func(context.Context, *node.Node, *policy.Policy, []flow.Browser) (flow.Caps, error) {
			return flow.Caps{Live: func() map[string]bool {
				if serving.Load() {
					early.Store(true)
				}
				select {
				case watched <- struct{}{}:
				default:
				}
				return map[string]bool{}
			}}, nil
		},
		Emit: func(e flow.Event) {
			if e.Event == flow.Serving {
				serving.Store(true)
				select {
				case <-watched:
				case <-time.After(3 * time.Second):
				}
				serving.Store(false)
			}
			events <- e
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	sel := []browser.Entry{{ID: id, Kind: browser.KindEndpoint, Name: "Chrome", Endpoint: &browser.Endpoint{CDP: seven.srv.URL}}}
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
	var order []flow.Kind
	timeout := time.After(30 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Event == flow.Error {
				t.Fatalf("error: %s", e.Message)
			}
			if e.Event != flow.Serving && e.Event != flow.BrowserGone {
				continue
			}
			order = append(order, e.Event)
		case <-timeout:
			t.Fatalf("no browser-gone, saw %v", order)
		}
		if len(order) > 0 && order[len(order)-1] == flow.BrowserGone {
			break
		}
	}
	if early.Load() {
		t.Fatal("the browser watcher ran before serving was out")
	}
	if len(order) != 2 || order[0] != flow.Serving {
		t.Fatalf("events %v, want serving then browser-gone", order)
	}
}
