package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCDPNativeProtectedChildSettlement(t *testing.T) {
	var protectedHits atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, "seven.protected.test:") {
			protectedHits.Add(1)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<!doctype html><body>child fixture<script>globalThis.linkTest = {};</script>")
	}))
	t.Cleanup(fixture.Close)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	protected := "http://seven.protected.test:" + port
	ordinary := "http://page.ordinary.test:" + port
	b := launchBrowserOn(t, 0, "--site-per-process", "--host-resolver-rules=MAP *.test 127.0.0.1")
	control := b.openPage(t, ordinary+"/plain")
	var loaded bool
	control.evalWithin(`new Promise((resolve) => { const f = document.createElement('iframe'); f.onload = () => resolve(true); f.src = `+jsArgs(protected+"/child")+`; document.body.append(f); })`, &loaded, 10*time.Second)
	if !loaded || protectedHits.Load() == 0 {
		t.Fatal("raw cross-site child control did not load")
	}
	before := protectedHits.Load()
	srv := pageServer(t)
	top := b.openPage(t, ordinary+"/plain")
	gate := newGuardGate(t, b.endpoint, srv.URL)
	var mu sync.Mutex
	frames := map[string]bool{}
	waiting := map[string]bool{}
	var commands []gateCommand
	gate.observe = func(m gateCommand) {
		mu.Lock()
		defer mu.Unlock()
		commands = append(commands, m)
	}
	gate.transform = func(data []byte) []byte {
		var m struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(data, &m) != nil {
			return data
		}
		mu.Lock()
		defer mu.Unlock()
		if m.Method == "Fetch.requestPaused" {
			req, _ := m.Params["request"].(map[string]any)
			if req["url"] == protected+"/child" {
				frame, _ := m.Params["frameId"].(string)
				frames[frame] = true
			}
		}
		if m.Method == "Target.attachedToTarget" {
			info, _ := m.Params["targetInfo"].(map[string]any)
			frame, _ := info["targetId"].(string)
			if info["type"] == "iframe" && frames[frame] {
				sid, _ := m.Params["sessionId"].(string)
				paused, _ := m.Params["waitingForDebugger"].(bool)
				waiting[sid] = paused
				info["url"] = protected + "/child"
				var event map[string]any
				_ = json.Unmarshal(data, &event)
				event["params"] = m.Params
				data, _ = json.Marshal(event)
			}
		}
		return data
	}
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "held-child-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var result struct {
		Committed bool     `json:"committed"`
		Events    []string `json:"events"`
	}
	var started bool
	env.p.eval("window.heldChildNativeWork = linkTest.protect.heldChildNative("+jsArgs("chrome", top.target, protected+"/child")+"); true", &started)
	env.p.waitFor("window.heldChildNativeReady === true", 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var snapshot struct {
		FrameTree struct {
			ChildFrames []struct {
				Frame struct {
					URL            string `json:"url"`
					UnreachableURL string `json:"unreachableUrl"`
				} `json:"frame"`
			} `json:"childFrames"`
		} `json:"frameTree"`
	}
	err := top.session.Call(ctx, "Page.getFrameTree", nil, &snapshot)
	committed := err == nil && len(snapshot.FrameTree.ChildFrames) == 1 && snapshot.FrameTree.ChildFrames[0].Frame.URL == "chrome-error://chromewebdata/" && snapshot.FrameTree.ChildFrames[0].Frame.UnreachableURL == protected+"/child"
	env.p.eval("window.heldChildNativeContinue("+jsArgs(committed)+"); true", &started)
	env.p.evalWithin("window.heldChildNativeWork", &result, 10*time.Second)
	if err != nil {
		t.Errorf("native frame snapshot: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	resumed := map[string]bool{}
	detached := map[string]bool{}
	for _, call := range commands {
		if call.Method == "Runtime.runIfWaitingForDebugger" {
			resumed[call.Session] = true
		}
		if call.Method == "Target.detachFromTarget" {
			var p struct {
				Session string `json:"sessionId"`
			}
			_ = json.Unmarshal(call.Params, &p)
			if paused, ok := waiting[p.Session]; ok {
				detached[p.Session] = true
				if paused && !resumed[p.Session] {
					t.Error("native waiting child detached without first resuming its navigation throttle")
				}
			}
		}
	}
	paused := 0
	for sid, waits := range waiting {
		if waits {
			paused++
		}
		if !detached[sid] {
			t.Errorf("native child session %s was not hidden and detached within the bound", sid)
		}
	}
	if len(waiting) < 2 || paused == 0 {
		t.Errorf("native two-session fixture not established: %v", waiting)
	}
	for _, event := range result.Events {
		if strings.Contains(event, protected) {
			t.Errorf("native protected child information reached the client: %s", event)
		}
		for sid := range waiting {
			if strings.Contains(event, sid) {
				t.Errorf("native hidden child session reached the client: %s", event)
			}
		}
	}
	if !result.Committed {
		t.Error("native child navigation did not commit its blocked error frame within the bound")
	}
	if protectedHits.Load() != before {
		t.Error("guarded child reached the protected fixture server")
	}
	t.Logf("native error commit=%t hidden children=%d waiting=%d", result.Committed, len(detached), paused)
}

func TestCDPSettlesHeldProtectedChildrenBeforeDetaching(t *testing.T) {
	b := launchBrowser(t)
	for _, order := range []string{"known", "pause-first", "attach-first"} {
		t.Run(order, func(t *testing.T) {
			env, fake := setupFakeProtectEnvOn(t, b, nil, func(seven, _ string) fakeScript {
				base := guardWaitScript("{}")
				return func(method string, params json.RawMessage) (string, []string) {
					if method == "Browser.getBrowserCommandLine" {
						url := ""
						if order == "known" {
							url = seven + "/child"
						}
						var attaches []string
						for i, parent := range []string{"S2", "S3"} {
							attaches = append(attaches, fmt.Sprintf(`{"method":"Target.attachedToTarget","sessionId":%q,"params":{"sessionId":"H%d","targetInfo":{"targetId":"F1","type":"iframe","url":%q},"waitingForDebugger":%t}}`, parent, i+1, url, i == 1))
						}
						pause := fmt.Sprintf(`{"method":"Fetch.requestPaused","sessionId":"S2","params":{"requestId":"P1","frameId":"F1","resourceType":"Document","request":{"url":%q}}}`, seven+"/child")
						if order == "pause-first" {
							return "{}", append([]string{pause}, attaches...)
						}
						if order == "attach-first" {
							return "{}", append(attaches, pause)
						}
						return "{}", attaches
					}
					if method == "Target.detachFromTarget" {
						var p struct {
							SessionID string `json:"sessionId"`
						}
						_ = json.Unmarshal(params, &p)
						if p.SessionID == "H2" {
							return "{}", []string{`{"method":"Runtime.consoleAPICalled","sessionId":"S2","params":{"type":"debug","args":[{"type":"string","value":"settled-child"}]}}`}
						}
					}
					return base(method, params)
				}
			})
			var result struct {
				Completed bool     `json:"completed"`
				Events    []string `json:"events"`
			}
			env.evalJSON(t, "linkTest.protect.heldChild("+jsArgs("chrome-2", env.allowed+"/blank")+")", &result)
			if !result.Completed {
				t.Error("the hidden child was not detached within the bound")
			}
			for _, event := range result.Events {
				if strings.Contains(event, "H1") || strings.Contains(event, "H2") || strings.Contains(event, env.plain.URL+"/child") {
					t.Errorf("protected child reached the client: %s", event)
				}
			}
			resumed := false
			detached := map[string]bool{}
			for _, raw := range fake.received() {
				var call struct {
					Method    string `json:"method"`
					SessionID string `json:"sessionId"`
					Params    struct {
						SessionID string `json:"sessionId"`
					} `json:"params"`
				}
				_ = json.Unmarshal([]byte(raw), &call)
				if call.Method == "Runtime.runIfWaitingForDebugger" && call.SessionID == "H2" {
					resumed = true
				}
				if call.Method == "Target.detachFromTarget" && strings.HasPrefix(call.Params.SessionID, "H") {
					detached[call.Params.SessionID] = true
					if call.Params.SessionID == "H2" && !resumed {
						t.Error("Chrome's waiting child was detached before its navigation throttle was resumed")
					}
				}
			}
			if !resumed || !detached["H1"] || !detached["H2"] {
				t.Errorf("child settlement: resumed=%t detached=%v", resumed, detached)
			}
		})
	}
}
