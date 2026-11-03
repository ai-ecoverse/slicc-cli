package integration

import (
	encodingbinary "encoding/binary"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/test/integration/testdata/stage1"
)

func stage1PublishedScripts(t *testing.T, client *stage1.Client, expected ...string) {
	t.Helper()
	seen := map[string]bool{}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-client.Events:
			switch event["method"] {
			case "Debugger.scriptParsed", "Debugger.scriptFailedToParse":
				seen[event["params"].(map[string]any)["scriptId"].(string)] = true
			case "Debugger.paused":
				for _, id := range expected {
					if !seen[id] {
						t.Errorf("pause preceded retained script %q: %v", id, seen)
					}
				}
				if len(seen) != len(expected) {
					t.Errorf("published scripts outside verified record set: %v", seen)
				}
				return
			}
		case <-client.Done:
			t.Fatal("stream ended before verified pause")
		case <-timer.C:
			t.Fatal("verified pause was not published")
		}
	}
}

func TestStage1PersistentDebuggerRepliesSurviveOnlyDocumentChanges(t *testing.T) {
	for _, method := range []string{"Debugger.enable", "Debugger.setInstrumentationBreakpoint"} {
		for _, revoked := range []bool{false, true} {
			name := method
			if revoked {
				name += "-revoked"
			}
			t.Run(name, func(t *testing.T) {
				client, browser := stage1Link(t)
				if method != "Debugger.enable" {
					stage1.Result(t, client.Call(t, "Debugger.enable", "S", nil))
				}
				browser.CommitOnReply(method, revoked)
				reply := client.Call(t, method, "S", map[string]any{"instrumentation": "beforeScriptExecution"})
				if revoked {
					stage1.Refused(t, reply)
					return
				}
				stage1.Result(t, reply)
				if method == "Debugger.enable" {
					stage1.Result(t, client.Call(t, "Debugger.setInstrumentationBreakpoint", "S", map[string]any{"instrumentation": "beforeScriptExecution"}))
				}
				stage1.Result(t, client.Call(t, "Debugger.removeBreakpoint", "S", map[string]any{"breakpointId": "B"}))
				browser.Script(t, "after-commit", 1024)
				browser.Emit(t, "normal")
				stage1PublishedScripts(t, client, "after-commit")
			})
		}
	}
}

func TestStage1PublishesValidatedAsyncAndParentStackRecords(t *testing.T) {
	for _, kind := range []string{"async", "parent-stack", "unplaced-async"} {
		t.Run(kind, func(t *testing.T) {
			client, browser := stage1Link(t)
			stage1Arm(t, client)
			browser.Emit(t, kind)
			if kind == "unplaced-async" {
				select {
				case <-browser.Resumes:
				case <-time.After(time.Second):
					t.Fatal("unplaced async script did not resume privately")
				}
				privateStage1Events(t, client)
				return
			}
			stage1PublishedScripts(t, client, "script", "async-script")
		})
	}
}

func stage1OccupyWindow(t *testing.T, client *stage1.Client, server *link.Stream) func() {
	t.Helper()
	blocked, release := client.HoldNextRecord(t)
	data, _ := json.Marshal(map[string]any{"method": "fixture.padding", "params": map[string]any{"data": strings.Repeat("x", link.Window-8192)}})
	framed := make([]byte, 4+len(data))
	encodingbinary.BigEndian.PutUint32(framed, uint32(len(data)))
	copy(framed[4:], data)
	if _, err := server.Write(framed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("client did not hold the occupied window")
	}
	return release
}

func TestStage1DebuggerPublicationWaitsForAvailableWindow(t *testing.T) {
	client, browser, server := stage1LinkStreams(t)
	stage1Arm(t, client)
	release := stage1OccupyWindow(t, client, server)
	read := browser.NextFrameRead()
	browser.Script(t, "window-script", 64<<10)
	browser.Emit(t, "normal")
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("pause proof was not read")
	}
	select {
	case <-browser.Resumes:
		t.Fatal("remaining window space caused a private resume")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	stage1PublishedScripts(t, client, "window-script")
	select {
	case <-read:
	default:
		t.Fatal("publication reused its pre-wait frame-tree proof")
	}
}

func TestStage1DebuggerBatchLargerThanWindowResumesPrivately(t *testing.T) {
	client, browser := stage1Link(t)
	stage1Arm(t, client)
	browser.Script(t, "large-script", 300<<10)
	browser.Emit(t, "normal")
	select {
	case <-browser.Resumes:
	case <-time.After(time.Second):
		t.Fatal("oversized batch did not resume privately")
	}
	privateStage1Events(t, client)
}

func TestStage1DebuggerCreditWaitProcessesQueuedRevocationBeforePublication(t *testing.T) {
	for _, row := range []struct{ name, event string }{{"revocation-during-wait", "small-forwardable"}, {"barrier-behind-stalled-write", "forwardable"}} {
		t.Run(row.name, func(t *testing.T) {
			client, browser, server := stage1LinkStreams(t)
			stage1Arm(t, client)
			release := stage1OccupyWindow(t, client, server)
			read := browser.NextFrameRead()
			browser.Script(t, "waiting-script", 64<<10)
			browser.Emit(t, "normal")
			select {
			case <-read:
			case <-time.After(time.Second):
				t.Fatal("pause proof was not read")
			}
			select {
			case <-browser.Resumes:
				t.Fatal("publisher did not wait for credit")
			case <-time.After(100 * time.Millisecond):
			}
			browser.Emit(t, row.event)
			select {
			case <-browser.Resumes:
				t.Fatal("publisher resumed before revocation")
			case <-time.After(100 * time.Millisecond):
			}
			browser.Emit(t, "foreign")
			revoked := time.Now()
			release()
			select {
			case <-browser.Resumes:
			case <-time.After(time.Second):
				t.Fatal("queued revocation did not resume promptly after credit returned")
			}
			if time.Since(revoked) >= time.Second {
				t.Fatal("revocation waited for the pause deadline")
			}
			privateStage1Events(t, client)
			stage1.Refused(t, client.Call(t, "Debugger.enable", "S", nil))
			select {
			case <-client.Stream.Done():
				t.Fatal("revocation unnecessarily reset the stream")
			default:
			}
		})
	}
}

func TestStage1DebuggerStalledCreditKeepsTheOriginalPauseDeadline(t *testing.T) {
	client, browser, server := stage1LinkStreams(t)
	stage1Arm(t, client)
	release := stage1OccupyWindow(t, client, server)
	browser.Script(t, "waiting-script", 64<<10)
	started := time.Now()
	browser.Emit(t, "normal")
	select {
	case <-browser.Resumes:
	case <-time.After(5500 * time.Millisecond):
		t.Fatal("credit wait held the page beyond its original deadline")
	}
	if time.Since(started) < 4*time.Second {
		t.Fatal("credit wait resumed before the original deadline")
	}
	if time.Since(started) > 5500*time.Millisecond {
		t.Fatal("credit wait extended the pause")
	}
	release()
	privateStage1Events(t, client)
	select {
	case <-client.Stream.Done():
		t.Fatal("credit expiry unnecessarily reset the stream")
	default:
	}
}
