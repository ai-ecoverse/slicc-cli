package integration

import (
	"strings"
	"testing"
	"time"
)

func Test675Stage1CrossOriginPlacementWaitsAndPublishesOnlyAfterOrdinaryChildPlaced(t *testing.T) {
	client, browser := stage1Link(t)
	stage1Arm(t, client)
	browser.SetChild("")
	browser.Emit(t, "normal")
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	waiting := true
	for waiting {
		select {
		case <-browser.Resumes:
			t.Fatal("unplaced ordinary child was resumed immediately instead of held for placement")
		case event := <-client.Events:
			if strings.HasPrefix(event["method"].(string), "Debugger.") {
				t.Fatal("unplaced child allowed publication")
			}
		case <-deadline.C:
			waiting = false
		}
	}
	browser.SetChild("https://child.test/doc")
	pause := client.Paused(t)
	if pause["params"].(map[string]any)["reason"] != "other" {
		t.Fatal("wrong pause reason")
	}
}

func Test675Stage1CrossOriginPlacementFailureAlwaysPrivatelyResumesWithinDeadline(t *testing.T) {
	for _, kind := range []string{"unplaced", "protected"} {
		t.Run(kind, func(t *testing.T) {
			client, browser := stage1Link(t)
			stage1Arm(t, client)
			if kind == "protected" {
				browser.SetChild("https://vault.sliccy.ai/doc")
			} else {
				browser.SetChild("")
			}
			started := time.Now()
			browser.Emit(t, "normal")
			select {
			case session := <-browser.Resumes:
				if session != "S" {
					t.Fatal("private rescue used another owner")
				}
			case <-time.After(5500 * time.Millisecond):
				t.Fatal("unplaced or protected child left the page paused")
			}
			if time.Since(started) >= 5500*time.Millisecond {
				t.Fatal("placement rescue exceeded deadline")
			}
			for {
				select {
				case event := <-client.Events:
					if strings.HasPrefix(event["method"].(string), "Debugger.") {
						t.Fatal("failed placement published Debugger data")
					}
				default:
					return
				}
			}
		})
	}
}
