package protect

import (
	"testing"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
)

func TestContextOriginVectors(t *testing.T) {
	v, err := sliccbrowsers.Protected()
	if err != nil {
		t.Fatal(err)
	}
	o, err := New(v.Origins)
	if err != nil {
		t.Fatal(err)
	}
	openers := map[string]Opener{"": NoOpener, sliccbrowsers.OpenerProtected: OpenerProtected, sliccbrowsers.OpenerUnprotected: OpenerUnprotected, sliccbrowsers.OpenerUnknown: OpenerUnknown}
	n := 0
	for _, c := range v.Targets {
		if c.ContextOrigin == "" {
			continue
		}
		n++
		ev := Evidence{DocumentURL: c.DocumentURL, UnreachableURL: c.UnreachableURL, Navigating: c.Navigating, ContextOrigin: c.ContextOrigin}
		if got := o.TargetWith(c.URL, "", c.Previous, openers[c.Opener], ev); got != c.Protected {
			t.Errorf("vector %+v: got %v", c, got)
		}
	}
	if n != 25 {
		t.Fatalf("%d contextOrigin vectors, want 25", n)
	}
}

func TestContextOriginRows(t *testing.T) {
	o := MustNew([]string{"http://127.0.0.1:5710"})
	for _, c := range []struct {
		name     string
		url      string
		previous bool
		opener   Opener
		ev       Evidence
		want     bool
	}{
		{"opaque/none", "about:blank", false, NoOpener, Evidence{ContextOrigin: "://"}, false},
		{"opaque/unprotected", "about:blank", false, OpenerUnprotected, Evidence{ContextOrigin: "://"}, false},
		{"opaque/protected-opener", "about:blank", false, OpenerProtected, Evidence{ContextOrigin: "://"}, true},
		{"opaque/unknown-opener", "about:blank", false, OpenerUnknown, Evidence{ContextOrigin: "://"}, true},
		{"opaque/previous", "about:blank", true, NoOpener, Evidence{ContextOrigin: "://"}, true},
		{"opaque/srcdoc", "about:srcdoc", false, OpenerUnprotected, Evidence{ContextOrigin: "://"}, false},
		{"opaque/empty-url", "", false, NoOpener, Evidence{ContextOrigin: "://"}, false},
		{"kitesurf/none", "about:blank", false, NoOpener, Evidence{ContextOrigin: "about://"}, false},
		{"kitesurf/unprotected", "about:blank", false, OpenerUnprotected, Evidence{ContextOrigin: "about://"}, false},
		{"kitesurf/protected-opener", "about:blank", false, OpenerProtected, Evidence{ContextOrigin: "about://"}, true},
		{"kitesurf/unknown-opener", "about:blank", false, OpenerUnknown, Evidence{ContextOrigin: "about://"}, true},
		{"kitesurf/previous", "about:blank", true, NoOpener, Evidence{ContextOrigin: "about://"}, true},
		{"kitesurf/fragment", "about:blank#top", false, NoOpener, Evidence{ContextOrigin: "about://"}, false},
		{"ordinary/none", "about:blank", false, NoOpener, Evidence{ContextOrigin: "https://example.test"}, false},
		{"ordinary/unprotected", "about:blank", false, OpenerUnprotected, Evidence{ContextOrigin: "https://example.test"}, false},
		{"ordinary/protected-opener", "about:blank", false, OpenerProtected, Evidence{ContextOrigin: "https://example.test"}, true},
		{"ordinary/unknown-opener", "about:blank", false, OpenerUnknown, Evidence{ContextOrigin: "https://example.test"}, true},
		{"ordinary/previous", "about:blank", true, NoOpener, Evidence{ContextOrigin: "https://example.test"}, true},
		{"ordinary/null", "about:blank", false, OpenerUnprotected, Evidence{ContextOrigin: "null"}, false},
		{"ordinary/seven-origin", "about:blank", false, OpenerUnprotected, Evidence{ContextOrigin: "https://seven.sliccy.ai"}, false},
		{"ordinary/srcdoc", "about:srcdoc", false, NoOpener, Evidence{ContextOrigin: "https://example.test"}, false},
		{"lookalike/upper", "about:blank", false, NoOpener, Evidence{ContextOrigin: "ABOUT://"}, true},
		{"lookalike/mixed", "about:blank", false, OpenerUnprotected, Evidence{ContextOrigin: "About://"}, true},
		{"lookalike/host", "about:blank", false, NoOpener, Evidence{ContextOrigin: "about://x"}, true},
		{"lookalike/blank", "about:blank", false, NoOpener, Evidence{ContextOrigin: "about:blank"}, true},
		{"lookalike/blank-upper", "about:blank", false, OpenerUnprotected, Evidence{ContextOrigin: "About:Blank"}, true},
		{"lookalike/bare", "about:blank", false, NoOpener, Evidence{ContextOrigin: "about:"}, true},
		{"lookalike/srcdoc-origin", "about:blank", false, NoOpener, Evidence{ContextOrigin: "about:srcdoc"}, true},
		{"lookalike/kitesurf-slash", "about:blank", false, NoOpener, Evidence{ContextOrigin: "about:///"}, true},
		{"lookalike/srcdoc-doc", "about:srcdoc", false, OpenerUnprotected, Evidence{ContextOrigin: "about:blank"}, true},
		{"lookalike/empty-url", "", false, NoOpener, Evidence{ContextOrigin: "about://x"}, true},
		{"lookalike/upper-doc", "ABOUT:BLANK", false, NoOpener, Evidence{ContextOrigin: "about:blank"}, true},
		{"lookalike/query-doc", "about:blank?x=1", false, NoOpener, Evidence{ContextOrigin: "ABOUT://"}, true},
		{"lookalike/trailing-space", "about:blank", false, NoOpener, Evidence{ContextOrigin: "about:blank "}, true},
		{"not-lookalike/leading-space", "about:blank", false, NoOpener, Evidence{ContextOrigin: " about:blank"}, false},
		{"not-lookalike/leading-tab", "about:blank", false, OpenerUnprotected, Evidence{ContextOrigin: "\tABOUT://x"}, false},
		{"not-lookalike/no-colon", "about:blank", false, NoOpener, Evidence{ContextOrigin: "aboutx://h"}, false},
		{"not-lookalike/bare-word", "about:blank", false, NoOpener, Evidence{ContextOrigin: "about"}, false},
		{"not-lookalike/slash", "about:blank", false, NoOpener, Evidence{ContextOrigin: "about//x"}, false},
		{"missing/none", "about:blank", false, NoOpener, Evidence{}, false},
		{"missing/unprotected", "about:blank", false, OpenerUnprotected, Evidence{}, false},
		{"missing/protected-opener", "about:blank", false, OpenerProtected, Evidence{}, true},
		{"missing/unknown-opener", "about:blank", false, OpenerUnknown, Evidence{}, true},
		{"missing/previous", "about:blank", true, NoOpener, Evidence{}, true},
		{"non-inheriting/ordinary-url", "https://example.test/", false, NoOpener, Evidence{ContextOrigin: "about:blank"}, false},
		{"non-inheriting/ordinary-url-upper", "https://example.test/", false, OpenerProtected, Evidence{ContextOrigin: "ABOUT://"}, false},
		{"non-inheriting/data", "data:text/html,x", false, NoOpener, Evidence{ContextOrigin: "about://x"}, false},
		{"non-inheriting/newtab", "about:newtab", false, NoOpener, Evidence{ContextOrigin: "about:blank"}, false},
		{"non-inheriting/protected-url", "https://seven.sliccy.ai/", false, NoOpener, Evidence{ContextOrigin: "https://example.test"}, true},
		{"non-inheriting/settings", "about:settings", false, NoOpener, Evidence{ContextOrigin: "://"}, true},
		{"error-page/previous-wins", "about:blank", false, NoOpener, Evidence{DocumentURL: "chrome-error://chromewebdata/", UnreachableURL: "about:blank", ContextOrigin: "about:blank"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := o.TargetWith(c.url, "", c.previous, c.opener, c.ev); got != c.want {
				t.Fatalf("TargetWith(%q, previous=%v, opener=%v, %+v) = %v, want %v", c.url, c.previous, c.opener, c.ev, got, c.want)
			}
		})
	}
	if !o.TargetWith("about:blank", "browser_ui", false, NoOpener, Evidence{ContextOrigin: "://"}) {
		t.Fatal("a hidden target type is no longer protected with an opaque context origin")
	}
}
