package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCDPKeepsLargeHTMLWithDistinctOrdinaryURLs(t *testing.T) {
	env := setupProtect(t)
	for _, protected := range []bool{false, true} {
		t.Run(fmt.Sprintf("protected=%t", protected), func(t *testing.T) {
			var html strings.Builder
			html.Grow(2 * 1024 * 1024)
			html.WriteString("<main>")
			secret := "https://vault.sliccy.ai/scrub-secret-fixture?token=fixture#fragment"
			for i := range 5000 {
				u := fmt.Sprintf("https://catalog.example.org/product/%d", i)
				if protected && i == 2500 {
					u = secret
				}
				fmt.Fprintf(&html, `<a href="%s">Product %d</a>`, u, i)
			}
			html.WriteString(strings.Repeat("x", 2*1024*1024-html.Len()-len("</main>")))
			html.WriteString("</main>")
			var result struct {
				Control string `json:"control"`
				Guarded string `json:"guarded"`
			}
			env.evalJSON(t, "linkTest.protect.ordinaryScrubHTML("+jsArgs(env.allowed+"/blank", html.String())+")", &result)
			want := result.Control
			if protected {
				if !strings.Contains(want, secret) {
					t.Fatal("the protected fixture was absent from the page's HTML")
				}
				want = strings.ReplaceAll(want, secret, "https://vault.sliccy.ai")
			}
			if len(result.Control) < 2*1024*1024 || result.Guarded != want {
				t.Fatalf("large DOM.getOuterHTML changed unexpectedly: control=%d bytes, guarded=%d bytes, want=%d bytes", len(result.Control), len(result.Guarded), len(want))
			}
		})
	}
}

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
				if text != "" {
					t.Errorf("%s did not exercise the capped adversarial string", name)
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
			for i, text := range []string{
				strings.Repeat("a://", 6400),
				strings.Repeat("é:", 6400) + "/x",
				strings.Repeat("a://", 100) + "https://VAULT.SLICCY.AI/scrub-secret-fixture",
				strings.Repeat("a://", 100) + "https%253A%252F%252Fvault.sliccy.ai%252Fscrub-secret-fixture",
				strings.Repeat("é:", 100) + "vault.sliccy.ai/scrub-secret-fixture",
				"https://vault.sliccy.ai/" + strings.Repeat("x", 100*1024) + "scrub-secret-fixture",
			} {
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
	if len(result.Titles) != 6 || len(result.Console) != 6 {
		t.Fatalf("missing bounded discovery or console events: %d, %d", len(result.Titles), len(result.Console))
	}
	for _, text := range append(result.Titles, result.Console...) {
		if text != "" {
			t.Error("a capped metadata string was not blanked")
		}
	}
}
