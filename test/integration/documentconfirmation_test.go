package integration

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCDPOrdinaryDocumentConfirmation(t *testing.T) {
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprint(reload), func(t *testing.T) {
			env, fake := setupFakeProtect(t, func(_, ordinary string) fakeScript {
				base := guardWaitScript("{}")
				frame := fmt.Sprintf(`{"id":"T1","url":"chrome-error://chromewebdata/","securityOrigin":"://","unreachableUrl":%q}`, ordinary+"/fixture")
				if reload {
					frame = fmt.Sprintf(`{"id":"T1","url":%q,"securityOrigin":%q}`, ordinary+"/fixture", ordinary)
				}
				return func(method string, params json.RawMessage) (string, []string) {
					switch method {
					case "Browser.getBrowserCommandLine":
						events := []string{`{"method":"Page.frameNavigated","sessionId":"S2","params":{"frame":` + frame + `}}`}
						if reload {
							events = append(events,
								`{"method":"Debugger.scriptParsed","sessionId":"S2","params":{"scriptId":"fixture","executionContextId":1}}`,
								`{"method":"Debugger.paused","sessionId":"S2","params":{"callFrames":[{"callFrameId":"fixture","location":{"scriptId":"fixture"}}]}}`,
								`{"method":"Page.loadEventFired","sessionId":"S2","params":{"timestamp":1}}`)
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
			if reload {
				fake.gate("Page.getFrameTree", "Browser.getWindowBounds")
			}
			var result struct {
				Title  cdpOutcome `json:"title"`
				DOM    cdpOutcome `json:"dom"`
				Shot   cdpOutcome `json:"shot"`
				MS     int        `json:"ms"`
				Loaded bool       `json:"loaded"`
			}
			env.evalJSON(t, fmt.Sprintf("linkTest.protect.ordinaryDocumentSequence('chrome-2', %t)", reload), &result)
			if !result.Title.OK || !result.DOM.OK || !result.Shot.OK || result.MS >= 5000 || result.Loaded != reload {
				t.Fatalf("ordinary document confirmation: %+v", result)
			}
			resumed := false
			for _, raw := range fake.received() {
				var m struct {
					Method  string `json:"method"`
					Session string `json:"sessionId"`
				}
				_ = json.Unmarshal([]byte(raw), &m)
				resumed = resumed || (m.Method == "Debugger.resume" && m.Session == "S2")
			}
			if reload && !resumed {
				t.Fatal("paused document was not resumed during confirmation")
			}
		})
	}
}
