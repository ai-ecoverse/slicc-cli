package integration

import (
	"strings"
	"testing"
	"time"

	capcdp "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/test/integration/testdata/stage1"
)

func stage1Link(t *testing.T) (*stage1.Client, *stage1.Browser) {
	t.Helper()
	browser := stage1.NewBrowser(t)
	registry := capcdp.NewRegistry(nil)
	entry, err := registry.Add(t.Context(), browser.Server.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := stage1.Open(t, registry.Handler(), entry.ID)
	client := stage1.NewClient(t, stream)
	stage1.Result(t, client.Call(t, "Target.createTarget", "", map[string]any{"url": "https://ordinary.test/doc"}))
	attached := stage1.Result(t, client.Call(t, "Target.attachToTarget", "", map[string]any{"targetId": "T", "flatten": true}))
	if attached["sessionId"] != "S" {
		t.Fatal("fixture attached with wrong native session")
	}
	stage1.Result(t, client.Call(t, "Page.enable", "S", nil))
	return client, browser
}

func stage1Arm(t *testing.T, client *stage1.Client) {
	t.Helper()
	stage1.Refused(t, client.Call(t, "Debugger.pause", "S", nil))
	stage1.Result(t, client.Call(t, "Debugger.enable", "S", nil))
	stage1.Result(t, client.Call(t, "Debugger.setInstrumentationBreakpoint", "S", map[string]any{"instrumentation": "beforeScriptExecution"}))
	stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil))
	for {
		select {
		case event := <-client.Events:
			if method, _ := event["method"].(string); strings.HasPrefix(method, "Debugger.") {
				t.Fatalf("Debugger event before the publication gate: %s", method)
			}
		default:
			return
		}
	}
}

func stage1Sent(browser *stage1.Browser, method string) int {
	count := 0
	for _, packet := range browser.Trace() {
		if packet.Method == method && packet.ID < 1879048192 {
			count++
		}
	}
	return count
}

func Test675Stage1CurrentLiveIDsAndStaleCallFramesOnTheRealLinkGuard(t *testing.T) {
	client, browser := stage1Link(t)
	stage1Arm(t, client)
	stage1.Refused(t, client.Call(t, "Debugger.getScriptSource", "S", map[string]any{"scriptId": "script"}))
	if stage1Sent(browser, "Debugger.getScriptSource") != 0 {
		t.Fatal("an unpublished script reached Chrome")
	}
	browser.Emit(t, "instrumentation")
	pause := client.Paused(t)
	params, _ := pause["params"].(map[string]any)
	if params["reason"] == "instrumentation" {
		t.Fatal("the instrumentation pause was published")
	}
	source := stage1.Result(t, client.Call(t, "Debugger.getScriptSource", "S", map[string]any{"scriptId": "script"}))
	if source["scriptSource"] != "globalThis.stage1Effect = 1" {
		t.Fatal("wrong source after the current live check")
	}
	stage1.Refused(t, client.Call(t, "Debugger.getScriptSource", "S", map[string]any{"scriptId": "unplaced"}))
	frame := `{"injectedScriptId":7,"id":0}`
	stage1.Result(t, client.Call(t, "Debugger.evaluateOnCallFrame", "S", map[string]any{"callFrameId": frame, "expression": "21*2", "returnByValue": true}))
	before := stage1Sent(browser, "Debugger.evaluateOnCallFrame")
	browser.Emit(t, "commit")
	stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil))
	stage1.Refused(t, client.Call(t, "Debugger.evaluateOnCallFrame", "S", map[string]any{"callFrameId": frame, "expression": "globalThis.staleEffect=1"}))
	stage1.Refused(t, client.Call(t, "Debugger.getScriptSource", "S", map[string]any{"scriptId": "script"}))
	if stage1Sent(browser, "Debugger.evaluateOnCallFrame") != before {
		t.Fatal("a stale call-frame id reached Chrome")
	}
}

func Test675Stage1ProtectedAndStalledPausesResumePrivatelyWithinTheBound(t *testing.T) {
	for _, kind := range []string{"protected", "stalled"} {
		t.Run(kind, func(t *testing.T) {
			client, browser := stage1Link(t)
			stage1Arm(t, client)
			if kind == "protected" {
				browser.Emit(t, "protected")
			} else {
				browser.StallReads()
			}
			start := time.Now()
			browser.Emit(t, "normal")
			select {
			case session := <-browser.Resumes:
				if session != "S" {
					t.Fatal("rescue used another session")
				}
			case <-time.After(5500 * time.Millisecond):
				t.Fatal("a protected or failed-proof pause stalled the page past the bound")
			}
			if elapsed := time.Since(start); elapsed > 5500*time.Millisecond {
				t.Fatal("pause rescue exceeded its bound")
			}
			stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil))
			for {
				select {
				case event := <-client.Events:
					if method, _ := event["method"].(string); strings.HasPrefix(method, "Debugger.") {
						t.Fatalf("private rescue published %s", method)
					}
				default:
					return
				}
			}
		})
	}
}

func Test675Stage1OtherExtensionRefusalIsLinkOnly(t *testing.T) {
	client, browser := stage1Link(t)
	browser.Emit(t, "foreign")
	stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil))
	stage1.Refused(t, client.Call(t, "Debugger.enable", "S", nil))
	if stage1Sent(browser, "Debugger.enable") != 0 {
		t.Fatal("link sent Debugger.enable beside a foreign world")
	}
	for _, method := range []string{"Debugger.setBreakpoint", "Debugger.setBreakpointByUrl", "DOMDebugger.setEventListenerBreakpoint", "EventBreakpoints.setInstrumentationBreakpoint"} {
		stage1.Refused(t, client.Call(t, method, "S", nil))
	}
}
