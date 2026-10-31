package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/test/integration/testdata/stage1"
)

func privateStage1Conversions(browser *stage1.Browser) int {
	count := 0
	for _, packet := range browser.Trace() {
		if packet.Method == "Debugger.pause" {
			count++
		}
	}
	return count
}

func privateStage1Resume(t *testing.T, browser *stage1.Browser) {
	t.Helper()
	select {
	case session := <-browser.Resumes:
		if session != "S" {
			t.Fatal("private resume used another session")
		}
	case <-time.After(time.Second):
		t.Fatal("known unnecessary instrumentation did not resume privately at once")
	}
}

func privateStage1Events(t *testing.T, client *stage1.Client) {
	t.Helper()
	stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil))
	for {
		select {
		case event := <-client.Events:
			if method, _ := event["method"].(string); strings.HasPrefix(method, "Debugger.") {
				t.Fatalf("private instrumentation published %s", method)
			}
		default:
			return
		}
	}
}

func Test675Stage1EvaluateInstrumentationNeverConvertsOrPublishes(t *testing.T) {
	client, browser := stage1Link(t)
	stage1Arm(t, client)
	before := privateStage1Conversions(browser)
	browser.Emit(t, "evaluate")
	privateStage1Resume(t, browser)
	privateStage1Events(t, client)
	if privateStage1Conversions(browser) != before {
		t.Fatal("evaluate instrumentation was converted")
	}
	if stage1Sent(browser, "Debugger.removeBreakpoint") != 0 {
		t.Fatal("instrumentation was disarmed")
	}
}

func Test675Stage1PublishedContextSkipsLaterScriptsButANewContextStillPublishes(t *testing.T) {
	client, browser := stage1Link(t)
	stage1Arm(t, client)
	browser.Emit(t, "instrumentation")
	client.Paused(t)
	stage1.Result(t, client.Call(t, "Debugger.resume", "S", nil))
	privateStage1Resume(t, browser)
	before := privateStage1Conversions(browser)
	browser.Emit(t, "instrumentation")
	privateStage1Resume(t, browser)
	privateStage1Events(t, client)
	if privateStage1Conversions(browser) != before {
		t.Fatal("already published context was converted again")
	}
	browser.Emit(t, "next-context")
	pause := client.Paused(t)
	params := pause["params"].(map[string]any)
	if params["reason"] != "other" {
		t.Fatal("new context did not get its normal publication")
	}
	if privateStage1Conversions(browser) != before+1 {
		t.Fatal("new context was incorrectly suppressed")
	}
}
