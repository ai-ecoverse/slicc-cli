package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCDPDocumentSetupFailureSettlesImmediately(t *testing.T) {
	for _, failure := range []string{"attach", "setup", "lost", "limit"} {
		t.Run(failure, func(t *testing.T) {
			bounds, wait := guardBounds("10s", "60s"), 2000
			if failure == "limit" {
				bounds, wait = guardBounds("60s", "2s"), 8000
			}
			env, fake := setupFakeProtectEnv(t, bounds, func(_, _ string) fakeScript {
				return func(method string, _ json.RawMessage) (string, []string) {
					switch method {
					case "Browser.getBrowserCommandLine":
						return "{}", []string{`{"method":"Target.attachedToTarget","params":{"sessionId":"POPUP","targetInfo":{"targetId":"T3","type":"iframe","url":"chrome-error://chromewebdata/"},"waitingForDebugger":false}}`}
					case "Target.attachToTarget":
						if failure == "attach" {
							return "!fixture attach failure", nil
						}
						return `{"sessionId":"GUARD_POPUP"}`, nil
					case "Network.enable":
						if failure == "setup" {
							return "!fixture setup failure", nil
						}
						return "{}", []string{`{"method":"Target.detachedFromTarget","sessionId":"RELAY","params":{"sessionId":"GUARD_POPUP","targetId":"T3"}}`}
					case "Page.getFrameTree":
						return "{}", nil
					}
					return "{}", nil
				}
			})
			fake.gate("Page.getFrameTree", "Browser.neverCalled")
			if failure == "limit" {
				fake.gate("Network.enable", "Browser.neverCalled")
			}
			var result struct {
				cdpOutcome
				MS int `json:"ms"`
			}
			env.evalJSON(t, fmt.Sprintf("linkTest.protect.documentLifecycle('chrome-2', false, true, %d)", wait), &result)
			if failure == "limit" {
				if want := "slicc: refused: slicc can't keep seven's origin out of this target: its guard wasn't ready in 2s"; !result.refused() || result.Error.Message != want {
					t.Fatalf("guard bound during a pending confirmation: %+v, want %q", result, want)
				}
				return
			}
			if !result.refused() || result.MS > 1500 {
				t.Fatalf("setup failure did not refuse promptly: %+v", result)
			}
		})
	}
}

func TestCDPInheritedOpenerLifecycle(t *testing.T) {
	for _, state := range []string{"ordinary", "ordinary-gone", "protected", "protected-child", "protected-gone", "unknown-frame", "untouched", "gone", "opener-frame-gone"} {
		t.Run(state, func(t *testing.T) {
			env, fake := setupFakeProtect(t, func(seven, ordinary string) fakeScript {
				base := guardWaitScript("{}")
				return func(method string, params json.RawMessage) (string, []string) {
					switch method {
					case "Page.getNavigationHistory":
						url := ordinary
						if state == "protected" {
							url = seven
						}
						return fmt.Sprintf(`{"currentIndex":0,"entries":[{"id":1,"url":%q,"userTypedURL":%q}]}`, url, url), nil
					case "Target.getTargetInfo":
						var p struct {
							ID string `json:"targetId"`
						}
						_ = json.Unmarshal(params, &p)
						return fmt.Sprintf(`{"targetInfo":{"targetId":%q,"type":"page","url":%q}}`, p.ID, ordinary), nil
					case "Browser.getBrowserCommandLine":
						openerFrame := "F1"
						var events []string
						if state == "untouched" || state == "gone" {
							openerFrame = "T1"
							events = append(events, fmt.Sprintf(`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":{"targetId":"T1","type":"page","url":%q}}}`, ordinary))
						}
						if state == "gone" {
							events = append(events, `{"method":"Target.targetDestroyed","sessionId":"RELAY","params":{"targetId":"T1"}}`)
						}
						if state == "ordinary" || state == "ordinary-gone" || state == "protected" || state == "protected-child" || state == "protected-gone" || state == "opener-frame-gone" {
							events = append(events, `{"method":"Page.frameAttached","sessionId":"S1","params":{"frameId":"F1","parentFrameId":"T1"}}`)
						}
						if state == "ordinary" || state == "ordinary-gone" || state == "protected-child" || state == "protected-gone" || state == "opener-frame-gone" {
							origin := ordinary
							if state == "protected-child" || state == "protected-gone" {
								origin = seven
							}
							events = append(events, fmt.Sprintf(`{"method":"Runtime.executionContextCreated","sessionId":"S1","params":{"context":{"id":42,"uniqueId":"opener-child","origin":%q,"name":"","auxData":{"isDefault":true,"frameId":"F1"}}}}`, origin))
						}
						return "{}", append(events, fmt.Sprintf(`{"method":"Target.attachedToTarget","params":{"sessionId":"POPUP","targetInfo":{"targetId":"T3","type":"page","url":%q,"openerId":"T1","openerFrameId":%q},"waitingForDebugger":false}}`, ordinary+"/popup", openerFrame))
					case "Target.attachToTarget":
						var p struct {
							ID string `json:"targetId"`
						}
						_ = json.Unmarshal(params, &p)
						if p.ID == "T3" {
							return `{"sessionId":"GUARD_POPUP"}`, nil
						}
					case "Browser.getWindowBounds":
						var events []string
						if state == "opener-frame-gone" {
							events = append(events, `{"method":"Target.targetDestroyed","sessionId":"RELAY","params":{"targetId":"T1"}}`)
						}
						events = append(events, `{"method":"Page.frameNavigated","sessionId":"POPUP","params":{"frame":{"id":"T3","url":"about:blank","securityOrigin":"://"}}}`)
						if state == "ordinary-gone" || state == "protected-gone" {
							events = append(events, `{"method":"Runtime.executionContextDestroyed","sessionId":"S1","params":{"executionContextId":42,"executionContextUniqueId":"opener-child"}}`)
						}
						return "{}", events
					case "Page.getFrameTree":
						return `{"frameTree":{"frame":{"id":"T3","url":"about:blank","securityOrigin":"://"}}}`, nil
					case "Runtime.evaluate":
						return `{"result":{"type":"string","value":"fixture"}}`, nil
					}
					return base(method, params)
				}
			})
			var result cdpOutcome
			touch := state != "untouched" && state != "gone"
			env.evalJSON(t, fmt.Sprintf("linkTest.protect.documentLifecycle('chrome-2', %t, false)", touch), &result)
			if state == "ordinary" {
				if !result.OK {
					t.Fatalf("ordinary child popup refused: %+v", result)
				}
				return
			}
			if !result.refused() {
				t.Fatalf("untrusted opener allowed: %+v", result)
			}
			if (state == "untouched" && !strings.Contains(result.Error.Message, "unknown opener")) || (state == "gone" && !strings.Contains(result.Error.Message, "gone opener")) {
				t.Fatalf("opener diagnostic: %+v", result)
			}
			if want := "slicc: refused: slicc can't keep seven's origin out of this target: inherited document has a gone opener or parent"; state == "opener-frame-gone" && result.Error.Message != want {
				t.Fatalf("gone owner of an in-process opener frame: %+v, want %q", result, want)
			}
			for _, raw := range fake.received() {
				var m struct {
					Method  string `json:"method"`
					Session string `json:"sessionId"`
				}
				_ = json.Unmarshal([]byte(raw), &m)
				if m.Method == "Runtime.evaluate" && m.Session == "POPUP" {
					t.Fatal("untrusted popup content command forwarded")
				}
			}
		})
	}
}
