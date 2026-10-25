package integration

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCDPOriginFrameSequencing(t *testing.T) {
	env, fake := setupFakeProtect(t, func(_, ordinary string) fakeScript {
		base := guardWaitScript("{}")
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Browser.getBrowserCommandLine":
				return "{}", []string{
					fmt.Sprintf(`{"method":"Target.attachedToTarget","params":{"sessionId":"FRAME","targetInfo":{"targetId":"T2","type":"iframe","url":%q,"parentFrameId":"T1"},"waitingForDebugger":false}}`, ordinary+"/frame"),
					fmt.Sprintf(`{"method":"Target.attachedToTarget","params":{"sessionId":"POPUP","targetInfo":{"targetId":"T3","type":"page","url":%q,"openerId":"T1","openerFrameId":"T2"},"waitingForDebugger":false}}`, ordinary+"/popup"),
				}
			case "Target.attachToTarget":
				var p struct {
					Target string `json:"targetId"`
				}
				_ = json.Unmarshal(params, &p)
				if p.Target == "T2" {
					return "!fixture guard failure", nil
				}
				if p.Target == "T3" {
					return `{"sessionId":"GUARD_POPUP"}`, nil
				}
			case "Browser.getWindowBounds":
				return "{}", []string{`{"method":"Page.frameNavigated","sessionId":"POPUP","params":{"type":"Navigation","frame":{"id":"T3","url":"about:blank","securityOrigin":"://"}}}`}
			case "Page.getFrameTree":
				return `{"frameTree":{"frame":{"id":"T3","url":"about:blank","securityOrigin":"://"}}}`, nil
			case "Runtime.evaluate":
				return `{"result":{"type":"string","value":"fixture"}}`, nil
			}
			return base(method, params)
		}
	})
	var result cdpOutcome
	env.evalJSON(t, "linkTest.protect.originFrameSequence('chrome-2')", &result)
	if !result.refused() {
		t.Fatalf("origin frame sequencing: %+v", result)
	}
	for _, raw := range fake.received() {
		var m struct {
			Method  string `json:"method"`
			Session string `json:"sessionId"`
		}
		_ = json.Unmarshal([]byte(raw), &m)
		if m.Method == "Runtime.evaluate" && m.Session == "POPUP" {
			t.Fatal("content was forwarded without originating frame confirmation")
		}
	}
}
