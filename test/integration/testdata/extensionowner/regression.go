package cdp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	extensionOwnerForeignURL = "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob/frame.html"
	extensionOwnerPoison     = `{"id":7,"sessionId":"S","result":{"backendNodeId":2,"nodeId":2,"contentDocument":{"nodeId":99},"sliccExtensionFrame":{"version":1,"kind":"extensionFrame","url":"chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob/frame.html","attacker":"poisoned"}}}`
)

func extensionOwnerGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New([]string{"chrome-extension://abcdefghijklmnopabcdefghijklmnop"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/", ctx: "default", document: "L1"}
	g.targets["FRAME"] = &target{id: "FRAME", typ: "iframe", url: extensionOwnerForeignURL, ctx: "default", parentFrame: "PAGE"}
	g.sessions["S"] = &session{id: "S", target: "PAGE", typ: "page"}
	nt := &navTarget{target: "PAGE", session: "S", done: true}
	g.navTargets["PAGE"] = nt
	g.noteExtensionFrameURLLocked("PAGE", "FRAME", extensionOwnerForeignURL)
	return g
}

func admitExtensionOwner(t *testing.T, g *guard, id int64) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"id": id, "method": "DOM.getFrameOwner", "sessionId": "S", "params": map[string]any{"frameId": "FRAME"}})
	c, n, ok := parseCommand(raw)
	if !ok || n != id {
		t.Fatalf("parse command: ok=%t id=%d", ok, n)
	}
	v := g.fromCommand(c, n, ok)
	if v.reply != nil || v.forward == nil {
		t.Fatalf("DOM.getFrameOwner was not admitted: reply=%s forward=%s", v.reply, v.forward)
	}
}

func ownerPoisonLeak(raw []byte) bool {
	s := string(raw)
	return strings.Contains(s, "contentDocument") || strings.Contains(s, `"attacker"`) || strings.Contains(s, "chrome-extension://")
}

func TestExtensionOwnerReplyWhitelistedUnderLock(t *testing.T) {
	g := extensionOwnerGuard(t)
	admitExtensionOwner(t, g, 7)
	v := g.fromBrowser([]byte(extensionOwnerPoison))
	if v.forward == nil {
		t.Fatal("getFrameOwner reply was dropped")
	}
	if ownerPoisonLeak(v.forward) {
		t.Fatalf("poison survived responseLocked: %s", v.forward)
	}
	out := g.scrub(append([]byte(nil), v.forward...))
	if ownerPoisonLeak(out) || !strings.Contains(string(out), `"sliccExtensionFrame"`) {
		t.Fatalf("scrub lost the guard marker: %s", out)
	}
}

func TestExtensionOwnerConcurrentSameIDBothWhitelisted(t *testing.T) {
	g := extensionOwnerGuard(t)
	admitExtensionOwner(t, g, 7)
	first := g.fromBrowser([]byte(extensionOwnerPoison))
	if first.forward == nil || ownerPoisonLeak(first.forward) {
		t.Fatalf("first reply: %s", first.forward)
	}
	admitExtensionOwner(t, g, 7)
	second := g.fromBrowser([]byte(strings.Replace(extensionOwnerPoison, `"backendNodeId":2`, `"backendNodeId":3`, 1)))
	if second.forward == nil || ownerPoisonLeak(second.forward) {
		t.Fatalf("second same-id reply: %s", second.forward)
	}
	for i, raw := range [][]byte{first.forward, second.forward} {
		out := g.scrub(append([]byte(nil), raw...))
		if ownerPoisonLeak(out) || !strings.Contains(string(out), `"sliccExtensionFrame"`) {
			t.Fatalf("reply %d after scrub: %s", i+1, out)
		}
	}
}

func TestExtensionOwnerUntrackedBeforeReplyRefuses(t *testing.T) {
	g := extensionOwnerGuard(t)
	admitExtensionOwner(t, g, 7)
	_ = g.fromBrowser([]byte(`{"method":"Page.frameNavigated","sessionId":"S","params":{"frame":{"id":"FRAME","parentId":"PAGE","url":"https://ordinary.test/embed","securityOrigin":"https://ordinary.test"}}}`))
	_ = g.fromBrowser([]byte(`{"method":"Target.targetInfoChanged","params":{"targetInfo":{"targetId":"FRAME","type":"iframe","url":"https://ordinary.test/embed","browserContextId":"default","parentFrameId":"PAGE"}}}`))
	v := g.fromBrowser([]byte(extensionOwnerPoison))
	if v.forward == nil {
		t.Fatal("expected a refusal forward")
	}
	if !strings.Contains(string(v.forward), `"error"`) || ownerPoisonLeak(v.forward) {
		t.Fatalf("untracked frame reply: %s", v.forward)
	}
}

func TestExtensionOwnerEmptyDetachKeepsWhitelist(t *testing.T) {
	g := extensionOwnerGuard(t)
	admitExtensionOwner(t, g, 7)
	_ = g.fromBrowser([]byte(`{"method":"Target.detachedFromTarget","params":{"sessionId":""}}`))
	if _, ok := g.extensionOwnerCalls[callKey{"S", 7}]; !ok {
		t.Fatal("empty session detach cleared in-flight owner calls")
	}
	v := g.fromBrowser([]byte(extensionOwnerPoison))
	if v.forward == nil || ownerPoisonLeak(v.forward) {
		t.Fatalf("empty detach left a raw owner reply: %s", v.forward)
	}
}
