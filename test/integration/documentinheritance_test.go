package integration

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCDPInheritedDocumentSequencing(t *testing.T) {
	env, fake := setupFakeProtect(t, func(_, ordinary string) fakeScript {
		base := guardWaitScript("{}")
		blank := `{"id":"T1","url":"about:blank","securityOrigin":"://"}`
		plain := fmt.Sprintf(`{"id":"T1","url":%q,"securityOrigin":%q}`, ordinary+"/fixture", ordinary)
		frame := blank
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Browser.getBrowserCommandLine":
				return "{}", []string{`{"method":"Page.frameNavigated","sessionId":"S2","params":{"type":"Navigation","frame":` + blank + `}}`}
			case "Browser.getWindowBounds":
				frame = plain
				return "{}", []string{
					`{"method":"Page.frameNavigated","sessionId":"S2","params":{"type":"Navigation","frame":` + plain + `}}`,
					fmt.Sprintf(`{"method":"Debugger.scriptParsed","sessionId":"S2","params":{"scriptId":"fixture","url":%q,"executionContextId":1}}`, ordinary+"/fixture.js"),
					fmt.Sprintf(`{"method":"CSS.styleSheetAdded","sessionId":"S2","params":{"header":{"styleSheetId":"fixture","sourceURL":%q}}}`, ordinary+"/fixture.css"),
					`{"method":"Console.messageAdded","sessionId":"S2","params":{"message":{"source":"console-api","level":"log","text":"fixture"}}}`,
				}
			case "Page.getFrameTree":
				return `{"frameTree":{"frame":` + frame + `}}`, nil
			case "Runtime.evaluate":
				return `{"result":{"type":"string","value":"fixture"}}`, nil
			case "DOM.getDocument":
				return `{"root":{"nodeId":1}}`, nil
			case "Page.captureScreenshot":
				return `{"data":"Zml4dHVyZQ=="}`, nil
			}
			return base(method, params)
		}
	})
	var result struct {
		Blank   cdpOutcome `json:"blank"`
		Title   cdpOutcome `json:"title"`
		DOM     cdpOutcome `json:"dom"`
		Shot    cdpOutcome `json:"shot"`
		MS      int        `json:"ms"`
		Scripts int        `json:"scripts"`
		Styles  int        `json:"styles"`
	}
	env.evalJSON(t, "linkTest.protect.inheritedDocumentSequence('chrome-2')", &result)
	if !result.Blank.OK || !result.Title.OK || !result.DOM.OK || !result.Shot.OK || result.MS >= 5000 || result.Scripts != 1 || result.Styles != 1 {
		t.Fatalf("inherited document sequence: %+v", result)
	}
	confirmed := 0
	for _, raw := range fake.received() {
		var m struct {
			Method  string `json:"method"`
			Session string `json:"sessionId"`
		}
		_ = json.Unmarshal([]byte(raw), &m)
		if m.Method == "Page.getFrameTree" && m.Session == "S1" {
			confirmed++
		}
	}
	if confirmed != 1 {
		t.Fatalf("frame confirmation count: %d, want one for the inherited commit only", confirmed)
	}
}
