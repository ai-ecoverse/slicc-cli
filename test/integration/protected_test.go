package integration

import (
	"encoding/json"
	"strings"
	"testing"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

var openers = map[string]protect.Opener{
	"":                              protect.NoOpener,
	sliccbrowsers.OpenerProtected:   protect.OpenerProtected,
	sliccbrowsers.OpenerUnprotected: protect.OpenerUnprotected,
	sliccbrowsers.OpenerUnknown:     protect.OpenerUnknown,
}

func TestProtectedVectors(t *testing.T) {
	v, err := sliccbrowsers.Protected()
	if err != nil {
		t.Fatal(err)
	}
	var typed struct {
		Targets []struct {
			Type string `json:"type"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(sliccbrowsers.ProtectedJSON(), &typed); err != nil || len(typed.Targets) != len(v.Targets) {
		t.Fatalf("protected.json targets: %v", err)
	}
	o, err := protect.New(v.Origins)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range v.URLs {
		if got := o.Loaded(c.URL); got != c.Protected {
			t.Errorf("Loaded(%q) = %v, want %v", c.URL, got, c.Protected)
		}
	}
	for i, c := range v.Targets {
		op, ok := openers[c.Opener]
		if !ok {
			t.Fatalf("target vector %+v: unknown opener", c)
		}
		typ := typed.Targets[i].Type
		ev := protect.Evidence{DocumentURL: c.DocumentURL, UnreachableURL: c.UnreachableURL, Navigating: c.Navigating}
		if got := o.TargetWith(c.URL, typ, c.Previous, op, ev); got != c.Protected {
			t.Errorf("Target(%+v, type %q) = %v", c, typ, got)
		}
	}
	for _, c := range v.CookieDomains {
		if got := o.CookieDomain(c.Domain); got != c.Protected {
			t.Errorf("CookieDomain(%q) = %v, want %v", c.Domain, got, c.Protected)
		}
	}
	for _, c := range v.StorageKeys {
		if got := o.StorageKey(c.Key); got != c.Protected {
			t.Errorf("StorageKey(%q) = %v, want %v", c.Key, got, c.Protected)
		}
	}
}

func TestProtectRefusalAndRedaction(t *testing.T) {
	if !protect.MustNew(nil).URL("https://vault.sliccy.ai/") || !protect.MustNew(nil).CookieDomain("vault.sliccy.ai") {
		t.Fatal("the vault isn't protected")
	}
	if protect.Refusal() != "slicc: refused: target is on seven's origin" {
		t.Fatalf("refusal %q", protect.Refusal())
	}
	for in, want := range map[string]string{
		"https://seven.sliccy.ai/secret/path?q=1#x":                                "https://seven.sliccy.ai",
		"blob:https://seven.sliccy.ai/0b9e":                                        "https://seven.sliccy.ai",
		"https://SEVEN.sliccy.ai:443/":                                             "https://seven.sliccy.ai",
		"http://127.0.0.1:5710/index.html":                                         "http://127.0.0.1:5710",
		"devtools://devtools/bundled/inspector.html?ws=x":                          "devtools://devtools",
		"chrome-extension://pejdijmoenmkgeppbflobdenhhabjlaj/completion_list.html": "about:blank",
		"about:blank#frag":                                                         "about:blank",
		"":                                                                         "",
	} {
		if got := protect.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
	o := protect.MustNew(nil)
	if !o.ExtensionFrame("chrome-extension://pejdijmoenmkgeppbflobdenhhabjlaj/completion_list.html") {
		t.Fatal("a foreign extension frame isn't recognised")
	}
	own := "chrome-extension://abcdefghijklmnopabcdefghijklmnop"
	if protect.MustNew([]string{own}).ExtensionFrame(own + "/frame.html") {
		t.Fatal("the program's own extension frame still counts as a foreign one")
	}
	if !protect.ExtensionShownMethod("Page.captureScreenshot") || !protect.ExtensionPresentMethod("Page.startScreencast") {
		t.Fatal("extension frame method lists don't match the catalog")
	}
	if !strings.Contains(protect.ShownRefusal(), "chrome-extension://") || !strings.Contains(protect.PresentRefusal(), "chrome-extension://") {
		t.Fatalf("extension frame refusals %q %q", protect.ShownRefusal(), protect.PresentRefusal())
	}
	if _, err := protect.New([]string{"not an origin"}); err == nil {
		t.Fatal("New accepts a non-origin")
	}
}
