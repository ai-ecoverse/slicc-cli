package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

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
