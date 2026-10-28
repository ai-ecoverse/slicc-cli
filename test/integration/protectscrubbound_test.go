package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCDPBoundsPageControlledTitleAndConsoleScrubbing(t *testing.T) {
	env := setupProtect(t)
	for _, tc := range []struct {
		name string
		text string
	}{
		{"schemes", strings.Repeat("a://", 6400)},
		{"colons", strings.Repeat("é:", 6400) + "/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var result struct {
				Title   string  `json:"title"`
				Console string  `json:"console"`
				Elapsed float64 `json:"elapsed"`
			}
			env.evalJSONWithin(t, "linkTest.protect.scrubBoundedPage("+jsArgs(env.allowed+"/blank", tc.text+" "+env.seven+"/scrub-secret-fixture?token=scrub-secret-fixture")+")", &result, 20*time.Second)
			if result.Elapsed <= 0 || result.Elapsed >= 5000 {
				t.Fatalf("page-controlled event delivery took %.0f ms", result.Elapsed)
			}
			for name, text := range map[string]string{"title": result.Title, "console": result.Console} {
				if strings.Contains(text, "scrub-secret-fixture") {
					t.Errorf("%s leaked protected URL details", name)
				}
			}
			t.Logf("title and console delivered in %.0f ms", result.Elapsed)
		})
	}
}

func TestCDPBoundsFakeDiscoveryAndConsoleScrubbing(t *testing.T) {
	env, _ := setupFakeProtect(t, func(seven, allowed string) fakeScript {
		return func(method string, _ json.RawMessage) (string, []string) {
			if method != "Target.setDiscoverTargets" {
				return "{}", nil
			}
			var events []string
			for i, text := range []string{strings.Repeat("a://", 6400), strings.Repeat("é:", 6400) + "/x"} {
				text += " " + seven + "/scrub-secret-fixture?token=scrub-secret-fixture"
				events = append(events,
					fmt.Sprintf(`{"method":"Target.targetInfoChanged","params":{"targetInfo":{"targetId":"BOUND%d","type":"page","url":%q,"title":%q}}}`, i, allowed+"/blank", text),
					fmt.Sprintf(`{"method":"Console.messageAdded","params":{"message":{"text":%q}}}`, text),
				)
			}
			return "{}", append(events, `{"method":"Console.messageAdded","params":{"message":{"text":"scrub-bound-done"}}}`)
		}
	})
	var result struct {
		Titles  []string `json:"titles"`
		Console []string `json:"console"`
	}
	env.evalJSONWithin(t, "linkTest.protect.scrubBoundedDiscovery('chrome-2')", &result, 20*time.Second)
	if len(result.Titles) != 2 || len(result.Console) != 2 {
		t.Fatalf("missing bounded discovery or console events: %d, %d", len(result.Titles), len(result.Console))
	}
	for _, text := range append(result.Titles, result.Console...) {
		if strings.Contains(text, "scrub-secret-fixture") {
			t.Error("protected URL details leaked")
		}
	}
}
