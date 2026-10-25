package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCDPIframeProtectionFollowsRootDocument(t *testing.T) {
	for _, mode := range []string{"known", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			env, fake := setupFakeProtect(t, func(seven, ordinary string) fakeScript {
				base := guardWaitScript("{}")
				loader := "old"
				frame := func() string {
					return fmt.Sprintf(`{"id":"T1","loaderId":%q,"url":%q,"securityOrigin":%q}`, loader, ordinary+"/fixture", ordinary)
				}
				commit := func(kind string) []string {
					var events []string
					for _, session := range []string{"S1", "S2"} {
						events = append(events, fmt.Sprintf(`{"method":"Page.frameNavigated","sessionId":%q,"params":{"type":%q,"frame":%s}}`, session, kind, frame()))
					}
					return events
				}
				return func(method string, params json.RawMessage) (string, []string) {
					switch method {
					case "Browser.getBrowserCommandLine":
						var events []string
						if mode == "known" {
							events = commit("Navigation")
						}
						events = append(events, fmt.Sprintf(`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":{"targetId":"CHILD","type":"iframe","url":%q,"parentFrameId":"T1"}}}`, seven+"/child"))
						return "{}", events
					case "Browser.getWindowBounds":
						loader = "new"
						return "{}", commit("Navigation")
					case "Browser.getWindowForTarget":
						loader = "old"
						return "{}", commit("BackForwardCacheRestore")
					case "Page.getFrameTree":
						return `{"frameTree":{"frame":` + frame() + `}}`, nil
					case "DOM.getDocument":
						return `{"root":{"nodeId":1}}`, nil
					case "Page.captureScreenshot":
						return `{"data":"Zml4dHVyZQ=="}`, nil
					}
					return base(method, params)
				}
			})
			var result struct {
				Before []cdpOutcome `json:"before"`
				Next   []cdpOutcome `json:"next"`
				Back   []cdpOutcome `json:"back"`
				MS     int          `json:"ms"`
			}
			env.evalJSON(t, fmt.Sprintf("linkTest.protect.frameGenerationSequence('chrome-2', %t)", mode == "known"), &result)
			refused := "slicc: refused: target is on seven's origin"
			check := func(label string, results []cdpOutcome, allowed bool) {
				t.Helper()
				if len(results) != 4 {
					t.Fatalf("%s: missing command outcomes: %+v", label, results)
				}
				for _, r := range results {
					if r.OK != allowed || (!allowed && !strings.Contains(r.Error.Message, refused)) {
						t.Errorf("%s allowed=%t: %+v", label, allowed, r)
					}
				}
			}
			check("original", result.Before, false)
			if mode == "known" {
				check("next", result.Next, true)
				check("restored", result.Back, false)
				if result.MS >= 5000 {
					t.Errorf("document transition exceeded bound: %d ms", result.MS)
				}
			}
			counts := map[string]int{}
			for _, raw := range fake.received() {
				var m struct {
					Method  string `json:"method"`
					Session string `json:"sessionId"`
				}
				_ = json.Unmarshal([]byte(raw), &m)
				if m.Session == "S2" {
					counts[m.Method]++
				}
			}
			want := 0
			if mode == "known" {
				want = 1
			}
			for _, method := range []string{"Page.getFrameTree", "DOM.getDocument", "Page.captureScreenshot", "Input.dispatchMouseEvent"} {
				if counts[method] != want {
					t.Errorf("%s reached browser %d times, want %d", method, counts[method], want)
				}
			}
		})
	}
}
