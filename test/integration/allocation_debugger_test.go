package integration

import (
	"context"
	be "encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	capCDP "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/test/integration/testdata/stage1"
)

func sharedDebuggerLink(t *testing.T) (*stage1.Client, *stage1.Browser, *link.Stream, *link.AllocationBudget) {
	t.Helper()
	browser := stage1.NewBrowser(t)
	registry := capCDP.NewRegistry(nil)
	entry, err := registry.Add(t.Context(), browser.Server.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	budget := link.NewAllocationBudget(4 << 20)
	incoming := make(chan *link.Stream, 1)
	handler := registry.Handler()
	pair := publicationLinks(t, t.Context(), link.Options{AllocationBudget: budget, Hello: link.Hello{Caps: []string{"cdp"}}, Handlers: map[string]link.Handler{"cdp": func(s *link.Stream) { incoming <- s; handler(s) }}}, link.Options{})
	stream, err := pair.page.Open(t.Context(), map[string]any{"kind": "cdp", "browser": entry.ID}, link.OpenOptions{Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	peer := <-incoming
	client := stage1.NewClient(t, stream)
	stage1.Result(t, client.Call(t, "Target.createTarget", "", map[string]any{"url": "https://ordinary.test/doc"}))
	stage1.Result(t, client.Call(t, "Target.attachToTarget", "", map[string]any{"targetId": "T", "flatten": true}))
	stage1.Result(t, client.Call(t, "Page.enable", "S", nil))
	stage1Arm(t, client)
	return client, browser, peer, budget
}

func stableDebuggerUsage(t *testing.T, budget *link.AllocationBudget) int {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	previous := -1
	for {
		select {
		case <-t.Context().Done():
			t.Fatal("usage did not settle")
		case <-ticker.C:
			used := budget.Used()
			if used == previous {
				return used
			}
			previous = used
		}
	}
}

func TestSharedAllocationStage1HoldsFailClosed(t *testing.T) {
	for _, kind := range []string{"script", "script-id", "pause"} {
		t.Run(kind, func(t *testing.T) {
			client, browser, peer, budget := sharedDebuggerLink(t)
			baseline := stableDebuggerUsage(t, budget)
			if baseline <= 4096 {
				t.Fatal("debugger retention did not enter shared accounting")
			}
			available := 128
			if kind == "script" {
				available = (192 << 10) + 64 - 4096
			}
			if kind == "script-id" {
				available = (384 << 10) + 64 - 4096
			}
			filler := peer.ReserveAllocation((4<<20)-baseline-available, false)
			if filler == nil {
				t.Fatal("could not fill shared budget")
			}
			defer filler.Release()
			switch kind {
			case "script":
				browser.Script(t, "shared-script", 64<<10)
			case "script-id":
				browser.Script(t, strings.Repeat("i", 129<<10), 0)
			case "pause":
				browser.Emit(t, "normal")
			}
			select {
			case <-client.Done:
			case <-time.After(3 * time.Second):
				t.Fatal("exhausted debugger hold waited instead of resetting")
			}
			var reset *link.ResetError
			if !errors.As(client.Stream.Err(), &reset) || reset.Code != "ENOBUFS" {
				t.Fatalf("hold exhaustion: %v", client.Stream.Err())
			}
			for {
				select {
				case event := <-client.Events:
					if strings.HasPrefix(event["method"].(string), "Debugger.") {
						t.Fatal("unchecked debugger hold reached the page")
					}
				default:
					filler.Release()
					ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
					defer cancel()
					allocationZero(t, ctx, budget)
					return
				}
			}
		})
	}
}

func TestSharedAllocationStage1RetentionReleases(t *testing.T) {
	for _, ending := range []string{"disable", "navigation", "reset", "completion", "error"} {
		t.Run(ending, func(t *testing.T) {
			client, browser, _, budget := sharedDebuggerLink(t)
			browser.Script(t, "script", 4096)
			stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil))
			browser.Script(t, "script", 1024)
			stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil))
			browser.Emit(t, "normal")
			client.Paused(t)
			switch ending {
			case "disable":
				stage1.Result(t, client.Call(t, "Debugger.disable", "S", nil))
			case "navigation":
				browser.Emit(t, "commit")
				stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil))
			case "reset":
				_ = client.Stream.Reset("ECONNRESET")
			case "completion":
				_ = client.Stream.CloseWrite()
			case "error":
				var size [4]byte
				be.BigEndian.PutUint32(size[:], capCDP.MaxMessage+1)
				_, _ = client.Stream.Write(size[:])
			}
			if ending == "disable" || ending == "navigation" {
				if used := stableDebuggerUsage(t, budget); used != 4096 {
					t.Fatalf("retention survived %s: %d", ending, used)
				}
				_ = client.Stream.Reset("ECONNRESET")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			allocationZero(t, ctx, budget)
		})
	}
}
