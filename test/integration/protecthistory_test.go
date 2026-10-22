package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/coder/websocket"
)

type historyLag struct {
	armed     atomic.Bool
	blocked   chan struct{}
	release   chan struct{}
	probe     chan struct{}
	once      sync.Once
	mu        sync.Mutex
	forwarded []string
}

func historyLagProxy(t *testing.T, endpoint string) (*httptest.Server, *historyLag) {
	t.Helper()
	lag := &historyLag{blocked: make(chan struct{}), release: make(chan struct{}), probe: make(chan struct{}, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		var version map[string]any
		resp, err := http.Get(endpoint + "/json/version")
		if err != nil {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if json.NewDecoder(resp.Body).Decode(&version) != nil {
			http.Error(w, "invalid version", http.StatusBadGateway)
			return
		}
		version["webSocketDebuggerUrl"] = "ws://" + r.Host + "/browser"
		_ = json.NewEncoder(w).Encode(version)
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[]")) })
	mux.HandleFunc("/browser", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		resp, err := http.Get(endpoint + "/json/version")
		if err != nil {
			return
		}
		var version struct {
			URL string `json:"webSocketDebuggerUrl"`
		}
		err = json.NewDecoder(resp.Body).Decode(&version)
		_ = resp.Body.Close()
		if err != nil {
			return
		}
		up, _, err := websocket.Dial(ctx, version.URL, nil)
		if err != nil {
			return
		}
		defer func() { _ = up.CloseNow() }()
		down, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = down.CloseNow() }()
		go func() {
			defer cancel()
			for {
				kind, data, err := down.Read(ctx)
				if err != nil {
					return
				}
				if lag.armed.Load() {
					var m struct {
						ID     int64  `json:"id"`
						Method string `json:"method"`
					}
					if json.Unmarshal(data, &m) == nil {
						if m.ID < 1<<30 {
							lag.mu.Lock()
							lag.forwarded = append(lag.forwarded, m.Method)
							lag.mu.Unlock()
						}
						select {
						case lag.probe <- struct{}{}:
						default:
						}
					}
				}
				if up.Write(ctx, kind, data) != nil {
					return
				}
			}
		}()
		for {
			kind, data, err := up.Read(ctx)
			if err != nil {
				return
			}
			if lag.armed.Load() {
				lag.once.Do(func() { close(lag.blocked) })
				select {
				case <-lag.release:
				case <-ctx.Done():
					return
				}
			}
			if down.Write(ctx, kind, data) != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, lag
}

func TestCDPAllowsFreshTabsWithEmptyStartupEvidence(t *testing.T) {
	var histories atomic.Int32
	env, fake := setupFakeProtect(t, func(_, _ string) fakeScript {
		base := guardWaitScript("{}")
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Page.getNavigationHistory":
				switch histories.Add(1) {
				case 1:
					return `{"currentIndex":-1,"entries":[]}`, nil
				case 2:
					return `{}`, nil
				}
				return `{"currentIndex":0,"entries":[{"id":1,"url":"about:blank"}]}`, nil
			case "Target.getTargetInfo":
				if histories.Load() >= 3 {
					return `{"targetInfo":{"targetId":"T1","type":"page","url":"about:blank"}}`, nil
				}
				return `{"targetInfo":{"targetId":"T1","type":"page","url":""}}`, nil
			case "Page.getFrameTree":
				return `{"frameTree":{"frame":{"id":"T1","url":"about:blank","securityOrigin":"://"}}}`, nil
			case "Runtime.evaluate":
				return `{"result":{"type":"boolean","value":true}}`, nil
			}
			return base(method, params)
		}
	})
	var result cdpOutcome
	env.evalJSON(t, "linkTest.protect.initialHistory('chrome-2')", &result)
	if !result.OK {
		t.Fatalf("a fresh tab was quarantined before its initial document: %+v", result)
	}
	frames := 0
	for _, raw := range fake.received() {
		var m struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(raw), &m)
		if m.Method == "Page.getFrameTree" {
			frames++
		}
	}
	if histories.Load() != 3 || frames != 1 {
		t.Fatalf("fresh evidence: %d histories, %d frame trees", histories.Load(), frames)
	}
}

func TestCDPHistoryQuarantineWorkflow(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	plain := plainServer(t)
	var callbacks atomic.Int32
	allowed := strings.Replace(plain.URL, "127.0.0.1", "localhost", 1)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbacks.Add(1)
		http.Redirect(w, r, allowed+"/?n=finished", http.StatusFound)
	}))
	t.Cleanup(callback.Close)
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, callback.URL+"/vault/callback", http.StatusFound)
	}))
	t.Cleanup(idp.Close)
	e := &protectEnv{linkEnv: &linkEnv{t: t, b: b, origin: srv.URL}, plain: plain, allowed: allowed, seven: srv.URL}
	e.a = startAttach(t, "--cdp", b.endpoint, "--origin", srv.URL, "--origin", plain.URL, "--origin", callback.URL, "--name", "it-node")
	e.p = b.openPage(t, srv.URL+"/index.html")
	e.waitUp("")
	var result struct {
		Navigate   cdpOutcome `json:"navigate"`
		Locked     cdpOutcome `json:"locked"`
		Reset      cdpOutcome `json:"historyReset"`
		AfterReset cdpOutcome `json:"afterReset"`
		NewTab     cdpOutcome `json:"newTab"`
	}
	authorize := strings.Replace(idp.URL, "127.0.0.1", "localhost", 1) + "/authorize"
	e.evalJSON(t, "linkTest.protect.historyWorkflow("+jsArgs("chrome", allowed+"/?n=start", authorize, allowed+"/?n=finished")+")", &result)
	if !result.Navigate.OK || !result.Reset.OK || !result.Locked.refused() || !result.AfterReset.refused() || !result.NewTab.OK {
		t.Fatalf("workflow recovery: %+v", result)
	}
	if callbacks.Load() != 0 {
		t.Fatalf("protected callback was reached %d times", callbacks.Load())
	}
	t.Log("the protected callback was blocked before its redirect; CDP navigation resumed, but content stayed locked even after history reset; a new tab admitted content")
}

func TestCDPHistoryConformanceUnderInboundLag(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	plain := plainServer(t)
	proxy, lag := historyLagProxy(t, b.endpoint)
	var released sync.Once
	release := func() { released.Do(func() { close(lag.release) }) }
	defer release()
	e := &protectEnv{linkEnv: &linkEnv{t: t, b: b, origin: srv.URL}, plain: plain, allowed: strings.Replace(plain.URL, "127.0.0.1", "localhost", 1), seven: srv.URL}
	e.a = startAttach(t, "--cdp", b.endpoint, "--cdp-extra", proxy.URL, "--origin", srv.URL, "--origin", plain.URL, "--name", "it-node")
	e.p = b.openPage(t, srv.URL+"/index.html")
	e.waitUp("")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, direct := e.directTab(t, e.seven+"/frame.html?n=history-conformance")
	var value any
	if err := direct.Evaluate(ctx, "window.sevenSecret = 'history-marker'; true", cdp.EvaluateOptions{ReturnByValue: true}, &value); err != nil {
		t.Fatal(err)
	}
	var history struct {
		Entries []struct {
			ID int `json:"id"`
		} `json:"entries"`
	}
	if err := direct.Call(ctx, "Page.getNavigationHistory", nil, &history); err != nil || len(history.Entries) == 0 {
		t.Fatalf("history: %v %+v", err, history)
	}
	entry := history.Entries[len(history.Entries)-1].ID
	if err := direct.Call(ctx, "Page.navigate", map[string]any{"url": e.allowed + "/?n=history-conformance"}, nil); err != nil {
		t.Fatal(err)
	}
	waitHref(t, direct, e.allowed)
	if err := direct.Detach(ctx); err != nil {
		t.Fatal(err)
	}
	e.p.eval("window.historyConformancePromise = linkTest.protect.historyConformance("+jsArgs("chrome-2", id)+"); true", &value)
	e.p.waitFor("window.historyConformanceReady === true", 10*time.Second)
	direct, err := b.client.Attach(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = direct.Detach(ctx) }()
	lag.armed.Store(true)
	if err := direct.Call(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": entry}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lag.blocked:
	case <-ctx.Done():
		t.Fatal("lag gate did not engage")
	}
	waitHref(t, direct, e.seven)
	var marker string
	if err := direct.Evaluate(ctx, "window.sevenSecret", cdp.EvaluateOptions{ReturnByValue: true}, &marker); err != nil || marker != "history-marker" {
		t.Fatalf("protected document was not restored: %q %v", marker, err)
	}
	e.p.eval("window.historyConformanceFire(); true", &value)
	settled, stopSettled := context.WithCancel(ctx)
	defer stopSettled()
	outcomes := make(chan error, 1)
	go func() {
		var result any
		outcomes <- e.p.session.Evaluate(settled, "window.historyConformanceOutcomes", cdp.EvaluateOptions{AwaitPromise: true, ReturnByValue: true}, &result)
	}()
	select {
	case <-lag.probe:
		stopSettled()
	case err := <-outcomes:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("neither refusal nor forwarding was observed")
	}
	var ran int
	if err := direct.Evaluate(ctx, "window.__guardRan || 0", cdp.EvaluateOptions{ReturnByValue: true}, &ran); err != nil {
		t.Fatal(err)
	}
	lag.mu.Lock()
	forwarded := append([]string(nil), lag.forwarded...)
	lag.mu.Unlock()
	if ran != 0 || len(forwarded) != 0 {
		t.Errorf("document-reaching calls escaped while inbound was held: marker=%d methods=%v", ran, forwarded)
	}
	release()
	e.p.waitFor("window.historyConformanceResult !== undefined", 20*time.Second)
	var results map[string]cdpOutcome
	e.p.eval("window.historyConformanceResult", &results)
	for method, result := range results {
		if !result.refused() {
			t.Errorf("%s: %+v", method, result)
		}
	}
	if len(results) != 7 {
		t.Errorf("got %d command outcomes", len(results))
	}
}
