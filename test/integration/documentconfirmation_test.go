package integration

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCDPOrdinaryDocumentConfirmation(t *testing.T) {
	for _, mode := range []string{"error", "reload", "restore"} {
		t.Run(mode, func(t *testing.T) {
			reload := mode != "error"
			env, fake := setupFakeProtect(t, func(_, ordinary string) fakeScript {
				base := guardWaitScript("{}")
				frame := fmt.Sprintf(`{"id":"T1","url":"chrome-error://chromewebdata/","securityOrigin":"://","unreachableUrl":%q}`, ordinary+"/fixture")
				if reload {
					frame = fmt.Sprintf(`{"id":"T1","url":%q,"securityOrigin":%q}`, ordinary+"/fixture", ordinary)
				}
				return func(method string, params json.RawMessage) (string, []string) {
					switch method {
					case "Browser.getBrowserCommandLine":
						navType := "Navigation"
						if mode == "restore" {
							navType = "BackForwardCacheRestore"
						}
						events := []string{`{"method":"Page.frameNavigated","sessionId":"S2","params":{"type":"` + navType + `","frame":` + frame + `}}`}
						if mode == "reload" {
							events = append(events,
								fmt.Sprintf(`{"method":"Debugger.scriptParsed","sessionId":"S2","params":{"scriptId":"fixture","url":%q,"executionContextId":1}}`, ordinary+"/fixture.js"),
								fmt.Sprintf(`{"method":"CSS.styleSheetAdded","sessionId":"S2","params":{"header":{"styleSheetId":"fixture","sourceURL":%q}}}`, ordinary+"/fixture.css"),
								`{"method":"Debugger.paused","sessionId":"S2","params":{"callFrames":[{"callFrameId":"fixture","location":{"scriptId":"fixture"}}]}}`)
						}
						if reload {
							events = append(events, `{"method":"Page.loadEventFired","sessionId":"S2","params":{"timestamp":1}}`)
						}
						return "{}", events
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
			if mode == "restore" {
				fake.gate("Page.getFrameTree", "Browser.getWindowBounds")
			}
			var result struct {
				Title   cdpOutcome `json:"title"`
				DOM     cdpOutcome `json:"dom"`
				Shot    cdpOutcome `json:"shot"`
				MS      int        `json:"ms"`
				Loaded  bool       `json:"loaded"`
				Scripts int        `json:"scripts"`
				Styles  int        `json:"styles"`
				Pauses  int        `json:"pauses"`
			}
			env.evalJSON(t, fmt.Sprintf("linkTest.protect.ordinaryDocumentSequence('chrome-2', %t)", reload), &result)
			if !result.Title.OK || !result.DOM.OK || !result.Shot.OK || result.MS >= 5000 || result.Loaded != reload {
				t.Fatalf("ordinary document confirmation: %+v", result)
			}
			confirmed, resumed := false, false
			for _, raw := range fake.received() {
				var m struct {
					Method  string `json:"method"`
					Session string `json:"sessionId"`
				}
				_ = json.Unmarshal([]byte(raw), &m)
				confirmed = confirmed || (m.Method == "Page.getFrameTree" && m.Session == "S1")
				resumed = resumed || (m.Method == "Debugger.resume" && m.Session == "S2")
			}
			if mode == "reload" {
				if confirmed || !resumed || result.Scripts != 0 || result.Styles != 1 || result.Pauses != 0 {
					t.Fatalf("unleased debugger events were not suppressed and privately resumed: confirmed=%t resumed=%t result=%+v", confirmed, resumed, result)
				}
			} else if !confirmed {
				t.Fatal("document was not confirmed")
			}
		})
	}
}
