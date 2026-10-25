package integration

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCDPDocumentStateSequencing(t *testing.T) {
	env, fake := setupFakeProtect(t, func(seven, _ string) fakeScript {
		base := guardWaitScript("{}")
		frame := fmt.Sprintf(`{"id":"T1","loaderId":"restored","url":"chrome-error://chromewebdata/","securityOrigin":"://","unreachableUrl":%q}`, seven+"/controller/fixture-id")
		event := func(session string) string {
			return `{"method":"Page.frameNavigated","sessionId":"` + session + `","params":{"type":"BackForwardCacheRestore","frame":` + frame + `}}`
		}
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Browser.getBrowserCommandLine":
				return "{}", []string{event("S2")}
			case "Browser.getWindowBounds":
				return "{}", []string{event("S1")}
			case "Page.getFrameTree":
				return `{"frameTree":{"frame":` + frame + `}}`, nil
			case "Runtime.evaluate":
				return `{"result":{"type":"string","value":"fixture"}}`, nil
			case "Page.captureScreenshot":
				return `{"data":"Zml4dHVyZQ=="}`, nil
			}
			return base(method, params)
		}
	})
	fake.gate("Page.captureScreenshot", "Browser.getWindowBounds")
	var result struct {
		Before cdpOutcome `json:"before"`
		After  cdpOutcome `json:"after"`
		Shot   cdpOutcome `json:"shot"`
	}
	env.evalJSON(t, "linkTest.protect.documentStateSequence('chrome-2')", &result)
	if !result.Before.OK || !result.After.refused() || !result.Shot.refused() {
		t.Fatalf("document state sequence: %+v", result)
	}
	for _, raw := range fake.received() {
		var m struct {
			Method string `json:"method"`
			Params struct {
				Expression string `json:"expression"`
			} `json:"params"`
		}
		_ = json.Unmarshal([]byte(raw), &m)
		if m.Method == "Runtime.evaluate" && m.Params.Expression == "document.title" {
			t.Error("content command was forwarded during the transition")
		}
	}
}
