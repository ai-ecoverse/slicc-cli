package integration

import (
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/test/integration/testdata/stage1"
)

func TestStage1DebuggerFloodReleasesItsBudgetAndTheNextDocumentPublishes(t *testing.T) {
	client, browser := stage1Link(t)
	stage1Arm(t, client)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range 96 {
		browser.Script(t, "flood-"+strconv.Itoa(i), 1000000)
		stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil, 15*time.Second))
		if i%8 == 7 {
			runtime.GC()
			var current runtime.MemStats
			runtime.ReadMemStats(&current)
			if current.HeapAlloc > before.HeapAlloc+(64<<20) {
				t.Fatal("retained debugger data exceeded the relay's 64 MiB bound")
			}
		}
	}
	stage1.Refused(t, client.Call(t, "Debugger.enable", "S", nil))
	stage1.Refused(t, client.Call(t, "Debugger.setInstrumentationBreakpoint", "S", map[string]any{"instrumentation": "beforeScriptExecution"}))
	browser.Emit(t, "normal")
	select {
	case session := <-browser.Resumes:
		if session != "S" {
			t.Fatal("budget rescue used another session")
		}
	case <-time.After(time.Second):
		t.Fatal("a pause in an unretained script did not resume privately")
	}
	privateStage1Events(t, client)
	browser.Emit(t, "commit")
	stage1.Result(t, client.Call(t, "Browser.getVersion", "", nil))
	stage1Arm(t, client)
	browser.Emit(t, "next-context")
	pause := client.Paused(t)
	params := pause["params"].(map[string]any)
	if params["reason"] != "other" {
		t.Fatal("the next ordinary document did not publish its normal pause")
	}
}
