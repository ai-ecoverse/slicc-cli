package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCDPExtraSelfNavigationErrorTargetRecovery(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	var protectedHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, "127.0.0.1:") && r.URL.Path == "/protected" {
			protectedHits.Add(1)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>ordinary fixture</title>"))
	}))
	t.Cleanup(plain.Close)
	proxy, _ := historyLagProxy(t, b.endpoint)
	env := &protectEnv{linkEnv: &linkEnv{t: t, b: b, origin: srv.URL}, plain: plain, allowed: strings.Replace(plain.URL, "127.0.0.1", "localhost", 1), seven: srv.URL}
	env.a = startAttach(t, "--cdp", b.endpoint, "--cdp-extra", proxy.URL, "--origin", srv.URL, "--origin", plain.URL, "--name", "it-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var result struct {
		Trials []struct {
			Frame struct {
				cdpOutcome
				Result struct {
					FrameTree struct {
						Frame struct {
							URL string `json:"url"`
						} `json:"frame"`
					} `json:"frameTree"`
				} `json:"result"`
			} `json:"frame"`
			Evaluate   cdpOutcome `json:"evaluate"`
			DOM        cdpOutcome `json:"dom"`
			Screenshot cdpOutcome `json:"screenshot"`
			Reload     cdpOutcome `json:"reload"`
			Navigate   cdpOutcome `json:"navigate"`
			Reset      cdpOutcome `json:"reset"`
			AfterReset cdpOutcome `json:"afterReset"`
			Close      cdpOutcome `json:"close"`
		} `json:"trials"`
	}
	env.evalJSON(t, "linkTest.protect.selfErrorRecovery("+jsArgs("chrome-2", env.allowed+"/blank", plain.URL+"/protected", 3)+")", &result)
	if len(result.Trials) != 3 {
		t.Fatalf("recovery trials: %d", len(result.Trials))
	}
	for i, trial := range result.Trials {
		if !trial.Frame.OK || trial.Frame.Result.FrameTree.Frame.URL != "chrome-error://chromewebdata/" || !trial.Reload.OK || !trial.Navigate.OK || !trial.Reset.OK || !trial.Close.OK {
			t.Errorf("trial %d recovery: %+v", i, trial)
		}
		if !trial.Evaluate.refused() || !trial.DOM.refused() || !trial.Screenshot.refused() || !trial.AfterReset.refused() {
			t.Errorf("trial %d content quarantine: %+v", i, trial)
		}
	}
	if protectedHits.Load() != 0 {
		t.Fatalf("protected document requests reached the server: %d", protectedHits.Load())
	}
}

func TestCDPProtectedErrorTargetUsesFreshBrowserState(t *testing.T) {
	b := launchBrowser(t)
	for _, tc := range []struct {
		name    string
		order   string
		state   string
		held    bool
		changes bool
		hint    bool
		loading bool
		unsure  bool
	}{
		{name: "target before error commit", order: "target-first", state: "error"},
		{name: "error commit before target", order: "frame-first", state: "error"},
		{name: "missing error commit event", order: "target-only", state: "error"},
		{name: "ordinary snapshot despite protected target hint", order: "target-only", state: "ordinary"},
		{name: "stale error event and real protected document", order: "frame-first", state: "protected"},
		{name: "error URL with protected security origin", order: "frame-first", state: "protected-origin"},
		{name: "unknown browser state", order: "target-only", state: "unknown"},
		{name: "delayed error snapshot", order: "target-only", state: "error", held: true},
		{name: "snapshot invalidated by a protected commit", order: "target-only", state: "error", held: true, changes: true},
		{name: "ordinary snapshot invalidated by a new target hint", order: "target-only", state: "ordinary", held: true, changes: true, hint: true},
		{name: "loading flags without error commit or stop events", order: "target-only", state: "error", loading: true},
		{name: "unsure loading flags with held error snapshot and missing events", order: "target-only", state: "error", held: true, loading: true, unsure: true},
		{name: "loading flags with ordinary snapshot", order: "target-only", state: "ordinary", loading: true, unsure: true},
		{name: "loading flags with held protected snapshot", order: "target-only", state: "protected", held: true, loading: true, unsure: true},
		{name: "loading flags with unknown snapshot", order: "target-only", state: "unknown", loading: true, unsure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int32
			var triggered, changed atomic.Bool
			env, fake := setupFakeProtectEnvOn(t, b, guardBounds("2s", "8s"), func(seven, allowed string) fakeScript {
				base := guardWaitScript("{}")
				errorFrame := fmt.Sprintf(`{"id":"T1","url":"chrome-error://chromewebdata/","securityOrigin":"://","unreachableUrl":%q}`, seven+"/protected")
				protectedFrame := fmt.Sprintf(`{"id":"T1","url":%q,"securityOrigin":%q}`, seven+"/protected", seven)
				ordinaryFrame := fmt.Sprintf(`{"id":"T1","url":%q,"securityOrigin":%q}`, allowed+"/blank", allowed)
				event := func(frame string) string {
					return `{"method":"Page.frameNavigated","sessionId":"S1","params":{"frame":` + frame + `}}`
				}
				targetEvent := fmt.Sprintf(`{"method":"Target.targetInfoChanged","sessionId":"RELAY","params":{"targetInfo":{"targetId":"T1","type":"page","url":%q}}}`, seven+"/protected")
				return func(method string, params json.RawMessage) (string, []string) {
					switch method {
					case "Browser.getBrowserCommandLine":
						if triggered.Swap(true) {
							return "{}", nil
						}
						var events []string
						if tc.loading {
							events = append(events, `{"method":"Page.frameStartedNavigating","sessionId":"S1","params":{"frameId":"T1","loaderId":"missing-stop","navigationType":"differentDocument"}}`)
						}
						if tc.unsure {
							events = append(events, `{"method":"Page.frameStartedNavigating","sessionId":"S1","params":{"frameId":"T1","navigationType":"differentDocument"}}`)
						}
						switch tc.order {
						case "target-first":
							events = append(events, targetEvent, event(errorFrame))
						case "frame-first":
							events = append(events, event(errorFrame), targetEvent)
						default:
							events = append(events, targetEvent)
						}
						return "{}", events
					case "Browser.getWindowBounds":
						if tc.changes {
							changed.Store(true)
							if tc.hint {
								return "{}", []string{strings.ReplaceAll(targetEvent, "/protected", "/different")}
							}
							return "{}", []string{event(protectedFrame)}
						}
					case "Page.getFrameTree":
						reads.Add(1)
						frame := errorFrame
						switch tc.state {
						case "protected":
							frame = protectedFrame
						case "protected-origin":
							frame = fmt.Sprintf(`{"id":"T1","url":"chrome-error://chromewebdata/","securityOrigin":%q,"unreachableUrl":%q}`, seven, seven+"/protected")
						case "ordinary":
							frame = ordinaryFrame
						case "unknown":
							return "{}", nil
						}
						if changed.Load() {
							frame = protectedFrame
						}
						return `{"frameTree":{"frame":` + frame + `}}`, nil
					case "Runtime.evaluate":
						return `{"result":{"type":"number","value":1}}`, nil
					case "Page.navigate":
						return "{}", []string{event(ordinaryFrame)}
					case "Target.detachFromTarget":
						return "{}", []string{`{"method":"Target.detachedFromTarget","params":{"sessionId":"S2"}}`}
					}
					return base(method, params)
				}
			})
			fake.gate("Runtime.callFunctionOn", "Browser.getBrowserCommandLine")
			if tc.held {
				fake.gate("Page.getFrameTree", "Browser.getWindowBounds")
			}
			var value any
			env.p.eval("window.targetRaceWork = linkTest.protect.errorTargetRace("+jsArgs("chrome-2", env.allowed+"/blank", env.plain.URL+"/protected", tc.held)+"); true", &value)
			env.p.waitFor("window.targetRaceReady === true", 10*time.Second)
			if tc.held {
				var settled bool
				env.p.eval("window.targetRaceSettled !== undefined", &settled)
				if settled {
					t.Error("recovery was decided before the fresh browser response")
				}
				for _, raw := range fake.received() {
					if strings.Contains(raw, `"method":"Page.reload"`) {
						t.Error("reload reached the browser without authoritative state")
					}
				}
				env.p.eval("window.targetRaceContinue(); true", &value)
			}
			var result struct {
				Answers  map[string]cdpOutcome `json:"answers"`
				Detached bool                  `json:"detached"`
			}
			env.evalJSON(t, "window.targetRaceWork", &result)
			if !result.Answers["before"].OK {
				t.Fatal("ordinary content was not admitted before the transition")
			}
			usable := tc.state == "error" || tc.state == "ordinary"
			if tc.changes {
				usable = false
			}
			for name, answer := range result.Answers {
				if name == "before" {
					continue
				}
				control := name == "reload" || name == "navigate" || name == "reset" || name == "pageClose" || name == "close"
				if usable && control {
					if !answer.OK {
						t.Errorf("error target recovery %s: %+v", name, answer)
					}
				} else if !answer.refused() {
					t.Errorf("quarantined %s: %+v", name, answer)
				}
			}
			if usable && result.Detached {
				t.Error("a protected error target was permanently detached")
			}
			if reads.Load() == 0 || (tc.changes && reads.Load() < 2) {
				t.Errorf("fresh frame snapshots: %d", reads.Load())
			}
			if (tc.state == "protected" || tc.state == "protected-origin" || tc.changes) && !result.Detached {
				t.Error("a genuine protected document did not detach the client session")
			}
		})
	}
}
