package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

type errorContentFrame struct {
	ID             string `json:"id"`
	URL            string `json:"url"`
	UnreachableURL string `json:"unreachableUrl"`
	SecurityOrigin string `json:"securityOrigin"`
}

type errorContentCommit struct {
	Frame errorContentFrame `json:"frame"`
	Type  string            `json:"type"`
}

func errorContentNavigate(t *testing.T, s *cdp.Session, method string, params any) errorContentCommit {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sub := s.Client.Subscribe()
	defer sub.Close()
	if err := s.Call(ctx, method, params, nil); err != nil {
		t.Fatal(err)
	}
	var commit errorContentCommit
	for {
		select {
		case event, ok := <-sub.Events():
			if !ok {
				t.Fatal("browser event stream closed")
			}
			if event.SessionID != s.ID {
				continue
			}
			switch event.Method {
			case "Page.frameNavigated":
				var next errorContentCommit
				if json.Unmarshal(event.Params, &next) == nil && next.Frame.ID != "" {
					commit = next
				}
				if commit.Type == "BackForwardCacheRestore" {
					return commit
				}
			case "Page.loadEventFired":
				if commit.Frame.ID != "" {
					return commit
				}
			case "Page.backForwardCacheNotUsed":
				var reasons struct {
					Explanations []struct {
						Reason string `json:"reason"`
					} `json:"notRestoredExplanations"`
				}
				_ = json.Unmarshal(event.Params, &reasons)
				for _, reason := range reasons.Explanations {
					t.Logf("BFCache not used: %s", reason.Reason)
				}
			}
		case <-ctx.Done():
			t.Fatal("error document navigation did not settle")
		}
	}
}

func errorContentHistoryEntry(t *testing.T, s *cdp.Session, url string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var history struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID  int    `json:"id"`
			URL string `json:"url"`
		} `json:"entries"`
	}
	if err := s.Call(ctx, "Page.getNavigationHistory", nil, &history); err != nil {
		t.Fatal(err)
	}
	if history.CurrentIndex < 0 || history.CurrentIndex >= len(history.Entries) || history.Entries[history.CurrentIndex].URL != url {
		t.Fatal("error history entry was not independently observed")
	}
	return history.Entries[history.CurrentIndex].ID
}

func errorContentCall(t *testing.T, env *protectEnv, opts map[string]any, out any) {
	t.Helper()
	env.evalJSON(t, "linkTest.errorContent("+jsArgs(opts)+")", out)
}

func errorContentRead(t *testing.T, env *protectEnv, lag *historyLag, s *cdp.Session, targetID, failedURL string, protected bool, commit errorContentCommit) {
	t.Helper()
	if commit.Frame.ID != targetID || commit.Frame.URL != "chrome-error://chromewebdata/" || commit.Frame.UnreachableURL != failedURL || commit.Frame.SecurityOrigin != "://" {
		t.Fatal("error document was not independently confirmed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var document struct {
		Root struct {
			BackendNodeID int `json:"backendNodeId"`
		} `json:"root"`
	}
	if err := s.Call(ctx, "DOM.getDocument", nil, &document); err != nil || document.Root.BackendNodeID == 0 {
		t.Fatalf("observer DOM document: %v", err)
	}
	lag.mu.Lock()
	start := len(lag.forwarded)
	lag.mu.Unlock()
	var result struct {
		Reads map[string]struct {
			cdpOutcome
			Usable bool `json:"usable"`
		} `json:"reads"`
		Listed         *protectTarget `json:"listed"`
		Info           protectTarget  `json:"info"`
		NavigationType string         `json:"navigationType"`
	}
	errorContentCall(t, env, map[string]any{"action": "read", "backendNodeId": document.Root.BackendNodeID}, &result)
	for _, name := range []string{"title", "loadTimeData", "reloadURL", "document", "html", "accessibility", "snapshot", "screenshot"} {
		answer, ok := result.Reads[name]
		if protected {
			if !ok || !answer.refused() {
				t.Fatalf("error document read %s was not refused: ok=%t code=%d", name, answer.OK, answer.Error.Code)
			}
		} else if !ok || !answer.OK || !answer.Usable {
			t.Fatalf("ordinary error document read %s did not work: %+v", name, answer)
		}
	}
	if protected {
		origin := failedURL[:strings.Index(failedURL, "/controller/")]
		if result.Listed == nil || result.Listed.URL != origin || result.Listed.Title != origin || result.Info.URL != origin || result.Info.Title != origin {
			t.Fatal("error target listings were not reduced to the origin")
		}
	} else if result.Listed == nil || result.Listed.URL != failedURL || result.Info.URL != failedURL {
		t.Fatal("ordinary error target listing changed")
	}
	if result.NavigationType != commit.Type {
		t.Fatalf("client and observer navigation evidence differs: %q and %q", result.NavigationType, commit.Type)
	}
	lag.mu.Lock()
	forwarded := append([]string(nil), lag.forwarded[start:]...)
	lag.mu.Unlock()
	counts := map[string]int{}
	for _, method := range forwarded {
		counts[method]++
	}
	for method, controlCount := range map[string]int{"Runtime.evaluate": 3, "DOM.getDocument": 1, "DOM.getOuterHTML": 1, "Accessibility.getFullAXTree": 1, "DOMSnapshot.captureSnapshot": 1, "Page.captureScreenshot": 1} {
		want := controlCount
		if protected {
			want = 0
		}
		if counts[method] != want {
			t.Fatalf("browser audit %s: forwarded=%d want=%d", method, counts[method], want)
		}
	}
	t.Logf("error document reads: protected=%t navigation=%s forwarded=%v", protected, commit.Type, counts)
}

func TestCDPErrorDocumentContentReads(t *testing.T) {
	for _, mode := range []string{"fresh", "bfcache-eligible"} {
		for _, preAttach := range []bool{false, true} {
			path := "blocked-hop"
			if preAttach {
				path = "pre-attach-history"
			}
			t.Run(mode+"/"+path, func(t *testing.T) {
				var flags []string
				if mode == "fresh" {
					flags = []string{"--disable-features=BackForwardCache"}
				}
				b := launchBrowserOn(t, 0, flags...)
				srv := pageServer(t)
				var protectedHits atomic.Int32
				plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/controller/fixture-id" {
						if strings.HasPrefix(r.Host, "127.0.0.1:") {
							protectedHits.Add(1)
						}
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							_ = conn.Close()
						}
						return
					}
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte("<!doctype html><title>ordinary fixture</title><p>ordinary"))
				}))
				t.Cleanup(plain.Close)
				allowed := strings.Replace(plain.URL, "127.0.0.1", "localhost", 1)
				failedURL := plain.URL + "/controller/fixture-id"
				proxy, lag := historyLagProxy(t, b.endpoint)
				close(lag.release)
				lag.armed.Store(true)
				env := &protectEnv{linkEnv: &linkEnv{t: t, b: b, origin: srv.URL}, plain: plain, allowed: allowed, seven: srv.URL}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				var target struct {
					TargetID string `json:"targetId"`
				}
				if err := b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &target); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { b.closeTarget(target.TargetID) })
				s, err := b.client.Attach(ctx, target.TargetID)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Call(ctx, "Page.enable", nil, nil); err != nil {
					t.Fatal(err)
				}
				errorContentNavigate(t, s, "Page.navigate", map[string]any{"url": allowed + "/start"})
				entryID := 0
				if preAttach {
					commit := errorContentNavigate(t, s, "Page.navigate", map[string]any{"url": failedURL})
					if commit.Frame.UnreachableURL != failedURL || protectedHits.Load() == 0 {
						t.Fatal("pre-attach error history was not created")
					}
					entryID = errorContentHistoryEntry(t, s, failedURL)
					errorContentNavigate(t, s, "Page.navigate", map[string]any{"url": allowed + "/after"})
					protectedHits.Store(0)
				}
				env.a = startAttach(t, "--cdp", b.endpoint, "--cdp-extra", proxy.URL, "--origin", srv.URL, "--origin", plain.URL, "--name", "it-node")
				env.p = b.openPage(t, srv.URL+"/index.html")
				env.waitUp("")
				var result any
				attach := func(id string) {
					errorContentCall(t, env, map[string]any{"action": "attach", "browser": "chrome-2", "targetId": id}, &result)
				}
				arm := func() { errorContentCall(t, env, map[string]any{"action": "arm"}, &result) }
				closeClient := func() { errorContentCall(t, env, map[string]any{"action": "close"}, &result) }
				attach(target.TargetID)
				if !preAttach {
					commit := errorContentNavigate(t, s, "Page.navigate", map[string]any{"url": failedURL})
					errorContentRead(t, env, lag, s, target.TargetID, failedURL, true, commit)
					entryID = errorContentHistoryEntry(t, s, failedURL)
				}
				for _, back := range []string{"history-entry", "history-back"} {
					t.Run(back, func(t *testing.T) {
						if !preAttach || back == "history-back" {
							errorContentNavigate(t, s, "Page.navigate", map[string]any{"url": allowed + "/after"})
						}
						arm()
						method, params := "Page.navigateToHistoryEntry", map[string]any{"entryId": entryID}
						if back == "history-back" {
							method, params = "Runtime.evaluate", map[string]any{"expression": "setTimeout(() => history.back(), 0); true"}
						}
						commit := errorContentNavigate(t, s, method, params)
						if mode == "fresh" && commit.Type == "BackForwardCacheRestore" {
							t.Fatal("fresh navigation unexpectedly used BFCache")
						}
						errorContentRead(t, env, lag, s, target.TargetID, failedURL, true, commit)
					})
				}
				closeClient()
				if protectedHits.Load() != 0 {
					t.Fatal("a guarded document reached the fixture server")
				}
				if !preAttach {
					t.Run("ordinary-error-control", func(t *testing.T) {
						var control struct {
							TargetID string `json:"targetId"`
						}
						cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer ccancel()
						if err := b.client.Call(cctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &control); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { b.closeTarget(control.TargetID) })
						observer, err := b.client.Attach(cctx, control.TargetID)
						if err != nil {
							t.Fatal(err)
						}
						if err := observer.Call(cctx, "Page.enable", nil, nil); err != nil {
							t.Fatal(err)
						}
						attach(control.TargetID)
						ordinaryURL := allowed + "/controller/fixture-id"
						commit := errorContentNavigate(t, observer, "Page.navigate", map[string]any{"url": ordinaryURL})
						errorContentRead(t, env, lag, observer, control.TargetID, ordinaryURL, false, commit)
						closeClient()
					})
				}
			})
		}
	}
}
