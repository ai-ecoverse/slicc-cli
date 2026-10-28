package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrubHan(size int) string {
	var b strings.Builder
	for i := range size / 3 {
		b.WriteRune(rune(0x4e00 + i%22000))
	}
	return b.String()
}

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

func TestCDPKeepsMultilingualAndEncodedOrdinaryHTML(t *testing.T) {
	env := setupProtect(t)
	var links, ips strings.Builder
	for i := range 80 {
		fmt.Fprintf(&links, `<a href="https://example.com/caf%%C3%%A9%d/menu">café</a>`, i)
		fmt.Fprintf(&ips, `<a href="http://192.168.1.%d/menu">IP</a>`, i+1)
	}
	for _, tc := range []struct {
		name string
		html string
		from string
		to   string
	}{
		{"euro pricing", strings.Repeat("<li>9,99 €/Monat</li>", 50), "", ""},
		{"Chinese text", strings.Repeat("<p>首页/新闻</p>", 50), "", ""},
		{"percent paths", links.String(), "", ""},
		{"ordinary IPs", ips.String(), "", ""},
		{"IPv6 prefixes", `<a href="https://docs.example.com/">docs</a>` + strings.Repeat("<li>2001:db8:1::/48</li>", 12), "", ""},
		{"colon timestamps", `<a href="https://docs.example.com/">docs</a>` + strings.Repeat("<td>12:34:56/</td>", 40), "", ""},
		{"schedule ranges", strings.Repeat("<li>09:30–17:45/</li>", 40), "", ""},
		{"long ordinary Chinese paragraph", "<p>" + scrubHan(100*1024) + "/x</p>", "", ""},
		{"fullwidth protected host", strings.Repeat("<li>9,99 €/Monat</li>", 50) + `<a href="https://ｓｅｖｅｎ．ｓｌｉｃｃｙ．ａｉ/doc/secret-fixture">fixture</a>`, "https://ｓｅｖｅｎ．ｓｌｉｃｃｙ．ａｉ/doc/secret-fixture", "https://seven.sliccy.ai"},
		{"percent protected host", links.String() + `<a href="https://%73even.sliccy.ai/doc/secret-fixture">fixture</a>`, "https://%73even.sliccy.ai/doc/secret-fixture", "https://seven.sliccy.ai"},
		{"fullwidth bare host", `<p>ｓｅｖｅｎ．ｓｌｉｃｃｙ．ａｉ/doc/secret-fixture</p>`, "ｓｅｖｅｎ．ｓｌｉｃｃｙ．ａｉ/doc/secret-fixture", "ｓｅｖｅｎ．ｓｌｉｃｃｙ．ａｉ"},
		{"percent bare host", `<p>%73even.sliccy.ai/doc/secret-fixture</p>`, "%73even.sliccy.ai/doc/secret-fixture", "%73even.sliccy.ai"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var result struct {
				Control string `json:"control"`
				Guarded string `json:"guarded"`
			}
			env.evalJSON(t, "linkTest.protect.ordinaryScrubHTML("+jsArgs(env.allowed+"/blank", tc.html)+")", &result)
			want := result.Control
			if tc.from != "" {
				if !strings.Contains(want, tc.from) {
					t.Fatal("the protected fixture was absent from the page's HTML")
				}
				want = strings.ReplaceAll(want, tc.from, tc.to)
			}
			if result.Guarded != want {
				t.Fatalf("DOM.getOuterHTML changed unexpectedly: control=%d bytes, guarded=%d bytes, want=%d bytes", len(result.Control), len(result.Guarded), len(want))
			}
		})
	}
}

func TestCDPBoundsPageControlledTitleAndConsoleScrubbing(t *testing.T) {
	env := setupProtect(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/scrub.js" {
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte(`document.addEventListener('scrub-bound', (event) => { document.title = event.detail.title; console.log(event.detail.text); });`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>scrub fixture</title><script src="/scrub.js"></script><body>scrub fixture`))
	}))
	t.Cleanup(fixture.Close)
	ordinary := strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1)
	for _, tc := range []struct {
		name string
		text string
	}{
		{"schemes", strings.Repeat("a://", 6400)},
		{"colons", strings.Repeat("é:", 6400) + "/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			title := tc.text
			if tc.name == "colons" {
				title = strings.Repeat("é:", 64) + "/x " + title
			}
			suffix := " " + env.seven + "/scrub-secret-fixture?token=scrub-secret-fixture"
			var result struct {
				Title   string  `json:"title"`
				Console string  `json:"console"`
				Elapsed float64 `json:"elapsed"`
			}
			env.evalJSONWithin(t, "linkTest.protect.scrubBoundedPage("+jsArgs(ordinary+"/", title+suffix, title+suffix)+")", &result, 20*time.Second)
			if result.Elapsed <= 0 || result.Elapsed >= 5000 {
				t.Fatalf("page-controlled event delivery took %.0f ms", result.Elapsed)
			}
			for name, text := range map[string]string{"title": result.Title, "console": result.Console} {
				if strings.Contains(text, "scrub-secret-fixture") {
					t.Errorf("%s leaked protected URL details", name)
				}
				if text != "" {
					t.Errorf("%s did not exercise the capped adversarial string (%d bytes, prefix %q)", name, len(text), text[:min(len(text), 80)])
				}
			}
			t.Logf("title and console delivered in %.0f ms", result.Elapsed)
		})
	}
}

func TestCDPBoundsDistinctUnicodeTitleAndConsoleMetadata(t *testing.T) {
	host := scrubHan(100 * 1024)
	ordinary := "https://" + host + "/x"
	bare := host + "/x"
	protected := "https://" + host + ".vault.sliccy.ai/secret-fixture"
	padded := "https://va" + strings.Repeat("\u200b", 2500) + "ult.sliccy.ai/secret-fixture"
	texts := []string{ordinary, bare, protected, padded}
	wants := []string{ordinary, bare, "", ""}
	env, _ := setupFakeProtect(t, func(seven, allowed string) fakeScript {
		return func(method string, _ json.RawMessage) (string, []string) {
			if method != "Target.setDiscoverTargets" {
				return "{}", nil
			}
			var events []string
			for i, text := range texts {
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
	for name, values := range map[string][]string{"title": result.Titles, "console": result.Console} {
		if len(values) != len(wants) {
			t.Fatalf("missing Unicode %s metadata events: %d", name, len(values))
		}
		for i, want := range wants {
			if values[i] != want {
				t.Errorf("Unicode %s %d: got %d bytes, want %d bytes", name, i, len(values[i]), len(want))
			}
		}
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
				strings.Repeat("é:", 64) + "/x " + strings.Repeat("é:", 6400) + "/x",
				strings.Repeat("a://", 100) + "https://VAULT.SLICCY.AI/scrub-secret-fixture",
				strings.Repeat("a://", 100) + "https%253A%252F%252Fvault.sliccy.ai%252Fscrub-secret-fixture",
				strings.Repeat("é:", 100) + "vault.sliccy.ai/scrub-secret-fixture",
				"https://vault.sliccy.ai/" + strings.Repeat("x", 100*1024) + "scrub-secret-fixture",
				strings.Repeat("seven.sliccy.ai:1:2/scrub-secret-fixture ", 40),
				strings.Repeat("ｓｅｖｅｎ．ｓｌｉｃｃｙ．ａｉ:1:2/scrub-secret-fixture ", 40),
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
	if len(result.Titles) != 8 || len(result.Console) != 8 {
		t.Fatalf("missing bounded discovery or console events: %d, %d", len(result.Titles), len(result.Console))
	}
	for _, text := range append(result.Titles, result.Console...) {
		if text != "" {
			t.Error("a capped metadata string was not blanked")
		}
	}
	if !strings.Contains(env.a.stderr.String(), "cdp: scrub blanked kind=metadata length=") {
		t.Error("SLICC_DEBUG did not report the fail-closed blank")
	}
}
