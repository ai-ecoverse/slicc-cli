package cdp

import (
	"encoding/json"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func contextOriginGuard(t *testing.T, url string) *guard {
	t.Helper()
	g := newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
	page := map[string]any{"targetId": "P", "type": "page", "url": url, "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	defer g.mu.Unlock()
	if tg := g.targets["P"]; tg == nil || tg.protected {
		t.Fatalf("setup: page %q missing or already protected", url)
	}
	if s := g.sessions["S"]; s == nil || s.refused {
		t.Fatalf("setup: session on %q missing or refused", url)
	}
	return g
}

func contextOriginEvent(g *guard, origin, frame string, isDefault bool) browserVerdict {
	return g.fromBrowser(encode(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": "S",
		"params": map[string]any{"context": map[string]any{
			"id": 7, "origin": origin, "name": "", "uniqueId": "u7",
			"auxData": map[string]any{"isDefault": isDefault, "type": "default", "frameId": frame},
		}},
	}))
}

func contextOriginDetached(v browserVerdict) bool {
	for _, raw := range v.send {
		var call struct {
			Method string `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
			} `json:"params"`
		}
		if json.Unmarshal(raw, &call) == nil && call.Method == "Target.detachFromTarget" && call.Params.SessionID == "S" {
			return true
		}
	}
	return false
}

func TestDefaultContextOrigin(t *testing.T) {
	for _, c := range []struct {
		name      string
		url       string
		origin    string
		frame     string
		isDefault bool
		protected bool
		forward   bool
	}{
		{"lookalike/upper", "about:blank", "ABOUT://", "P", true, true, false},
		{"lookalike/host", "about:blank", "about://x", "P", true, true, false},
		{"lookalike/blank", "about:blank", "about:blank", "P", true, true, false},
		{"lookalike/blank-mixed", "about:blank", "About:Blank", "P", true, true, false},
		{"lookalike/srcdoc", "about:blank", "about:srcdoc", "P", true, true, false},
		{"lookalike/bare", "about:blank", "about:", "P", true, true, false},
		{"lookalike/ordinary-document", "https://example.test/", "about:blank", "P", true, true, false},
		{"lookalike/newtab", "about:blank", "about:newtab", "P", true, true, false},
		{"lookalike/new-tab-page", "about:blank", "about:new-tab-page", "P", true, true, false},
		{"lookalike/newtab-mixed", "https://example.test/", "About:NewTab", "P", true, true, false},
		{"kitesurf/fail-closed", "about:blank", "about://", "P", true, true, false},
		{"protected-origin", "about:blank", "https://vault.sliccy.ai", "P", true, true, false},
		{"opaque", "about:blank", "://", "P", true, false, true},
		{"ordinary", "about:blank", "https://example.test", "P", true, false, true},
		{"ordinary-document", "https://example.test/", "https://example.test", "P", true, false, true},
		{"null", "about:blank", "null", "P", true, false, true},
		{"missing", "about:blank", "", "P", true, false, true},
		{"non-default", "about:blank", "about:blank", "P", false, false, true},
		{"non-default-upper", "about:blank", "ABOUT://", "P", false, false, false},
		{"empty-frame", "about:blank", "about:blank", "", true, false, true},
		{"child-frame", "about:blank", "about:blank", "C", true, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := contextOriginGuard(t, c.url)
			v := contextOriginEvent(g, c.origin, c.frame, c.isDefault)
			g.mu.Lock()
			protected := g.targets["P"].protected
			refused := g.sessions["S"] != nil && g.sessions["S"].refused
			g.mu.Unlock()
			detached := contextOriginDetached(v)
			if protected != c.protected || refused != c.protected || detached != c.protected {
				t.Fatalf("origin %q frame %q default %v on %q: protected=%v refused=%v detached=%v, want %v", c.origin, c.frame, c.isDefault, c.url, protected, refused, detached, c.protected)
			}
			if c.forward != (v.forward != nil) {
				t.Fatalf("origin %q frame %q default %v: forwarded=%v, want %v", c.origin, c.frame, c.isDefault, v.forward != nil, c.forward)
			}
		})
	}
}

func TestDefaultContextOriginSticky(t *testing.T) {
	g := contextOriginGuard(t, "about:blank")
	if v := contextOriginEvent(g, "about:blank", "P", true); !contextOriginDetached(v) {
		t.Fatal("a lookalike default context origin did not detach the session")
	}
	v := contextOriginEvent(g, "https://example.test", "P", true)
	g.mu.Lock()
	protected := g.targets["P"].protected
	g.mu.Unlock()
	if !protected || v.forward != nil {
		t.Fatalf("an ordinary default context after a lookalike one: protected=%v forwarded=%v", protected, v.forward != nil)
	}
}
