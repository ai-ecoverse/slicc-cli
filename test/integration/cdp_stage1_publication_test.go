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

func TestStage1DebuggerPublicationCrossesFlowControlWindows(t *testing.T) {
	for _, kind := range []string{"larger-than-window", "remaining-space"} {
		t.Run(kind, func(t *testing.T) {
			client, browser, server := stage1LinkStreams(t)
			stage1Arm(t, client)
			size := 300 << 10
			release := func() {}
			if kind == "remaining-space" {
				size = 64 << 10
				blocked, unblock := client.HoldNextRecord(t)
				release = unblock
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
			}
			browser.Script(t, "window-script", size)
			browser.Emit(t, "normal")
			if kind == "remaining-space" {
				select {
				case <-browser.Resumes:
					t.Fatal("remaining window space caused a private resume")
				case <-time.After(100 * time.Millisecond):
				}
			}
			release()
			stage1PublishedScripts(t, client, "window-script")
		})
	}
}

func TestStage1DebuggerPartialPublicationRevokesOrExpiresWithReset(t *testing.T) {
	for _, kind := range []string{"revoked", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			client, browser := stage1Link(t)
			stage1Arm(t, client)
			blocked, _ := client.HoldNextRecord(t)
			browser.Script(t, "partial-script", 300<<10)
			started := time.Now()
			browser.Emit(t, "normal")
			select {
			case size := <-blocked:
				if size < 300<<10 {
					t.Fatal("a record outside the verified script set was started", size)
				}
			case <-time.After(time.Second):
				t.Fatal("no verified script chunk reached the client")
			}
			if kind == "revoked" {
				browser.Emit(t, "foreign")
			}
			select {
			case <-browser.Resumes:
			case <-time.After(5500*time.Millisecond - time.Since(started)):
				t.Fatal("publication held the page beyond its original deadline")
			}
			select {
			case <-client.Stream.Done():
			case <-time.After(time.Second):
				t.Fatal("partial CDP record was not reset")
			}
			if !strings.Contains(client.Stream.Err().Error(), "ECANCELED") {
				t.Fatal("wrong stream reset", client.Stream.Err())
			}
			for {
				select {
				case event := <-client.Events:
					if event["method"] == "Debugger.paused" {
						t.Fatal("revoked batch published a pause")
					}
				default:
					return
				}
			}
		})
	}
}
