package integration

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCDPInheritedParentSequencing(t *testing.T) {
	env, fake := setupFakeProtect(t, func(_, ordinary string) fakeScript {
		base := guardWaitScript("{}")
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Browser.getBrowserCommandLine":
				return "{}", []string{fmt.Sprintf(`{"method":"Target.attachedToTarget","params":{"sessionId":"CHILD","targetInfo":{"targetId":"T2","type":"iframe","url":%q,"parentFrameId":"T1"},"waitingForDebugger":false}}`, ordinary+"/fixture")}
			case "Target.attachToTarget":
				var p struct {
					Target string `json:"targetId"`
				}
				_ = json.Unmarshal(params, &p)
				if p.Target == "T2" {
					return `{"sessionId":"GUARD_CHILD"}`, nil
				}
			case "Browser.getWindowBounds":
				return "{}", []string{`{"method":"Page.frameNavigated","sessionId":"CHILD","params":{"type":"Navigation","frame":{"id":"T2","parentId":"T1","url":"about:blank","securityOrigin":"://"}}}`}
			case "Page.getFrameTree":
				return `{"frameTree":{"frame":{"id":"T2","parentId":"T1","url":"about:blank","securityOrigin":"://"}}}`, nil
			case "Runtime.evaluate":
				return `{"result":{"type":"string","value":"fixture"}}`, nil
			}
			return base(method, params)
		}
	})
	fake.gate("Page.getNavigationHistory", "Browser.getWindowForTarget")
	var result cdpOutcome
	env.evalJSON(t, "linkTest.protect.inheritedParentSequence('chrome-2')", &result)
	if !result.refused() {
		t.Fatalf("inherited parent sequencing: %+v", result)
	}
	for _, raw := range fake.received() {
		var m struct {
			Method  string `json:"method"`
			Session string `json:"sessionId"`
		}
		_ = json.Unmarshal([]byte(raw), &m)
		if m.Method == "Runtime.evaluate" && m.Session == "CHILD" {
			t.Fatal("content was forwarded before parent confirmation")
		}
	}
}
