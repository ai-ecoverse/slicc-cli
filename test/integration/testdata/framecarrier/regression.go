package cdp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	carrierText     = "carrier-free-text-4b1d"
	carrierShared   = "carrier-shared-name"
	carrierPage     = "https://ordinary.test/page"
	carrierOrigin   = "https://ordinary.test"
	carrierSeven    = "view-source:https://seven.sliccy.ai/os/"
	carrierVault    = "blob:https://vault.sliccy.ai/0b9e0f6c"
	carrierFS       = "filesystem:https://seven.sliccy.ai/temporary/notes.txt"
	carrierExtWrap  = "view-source:chrome-extension://nkbihfbeogaeaoehlefnkodbefgpgknn/home.html"
	carrierFailed   = "https://seven.sliccy.ai/missing"
	carrierErrorDoc = "chrome-error://chromewebdata/"
)

var carrierWrappers = []string{carrierSeven, carrierVault, carrierFS}

func carrierGuard(t *testing.T) *guard {
	t.Helper()
	g := newGuard(protect.MustNew(nil), t.Logf)
	g.relaySession = "RELAY"
	page := map[string]any{"targetId": "P", "type": "page", "url": carrierPage, "title": "ordinary", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S", "waitingForDebugger": false, "targetInfo": page}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	g.navTargets["P"] = &navTarget{target: "P", session: "S", ready: ready, done: true}
	g.mu.Unlock()
	return g
}

func carrierArmErrorPage(g *guard) {
	g.mu.Lock()
	defer g.mu.Unlock()
	nt := g.navTargets["P"]
	nt.documentURL = carrierErrorDoc
	nt.errorURL = carrierFailed
}

func carrierFrame(id, parent, url, origin, name string) map[string]any {
	f := map[string]any{"id": id, "loaderId": "L-" + id, "url": url, "securityOrigin": origin, "mimeType": "text/html", "name": name, "domainAndRegistry": "carrier-domain-" + name}
	if parent != "" {
		f["parentId"] = parent
	}
	return f
}

func carrierNode(f map[string]any, kids ...any) map[string]any {
	return map[string]any{"frame": f, "childFrames": kids, "resources": []any{}}
}

func carrierEvent(method string, params map[string]any) []byte {
	return encode(map[string]any{"method": method, "sessionId": "S", "params": params})
}

func carrierOut(g *guard, v browserVerdict) []string {
	var out []string
	if v.forward != nil {
		out = append(out, string(g.scrub(v.forward)))
	}
	for _, b := range v.page {
		out = append(out, string(g.scrub(b)))
	}
	return out
}

func carrierAsk(t *testing.T, g *guard, id int64, method string) {
	t.Helper()
	v := g.fromCommand(&command{id: id, method: method, session: "S", params: map[string]any{}}, id, true)
	if v.forward == nil || v.reply != nil || v.wait != nil {
		t.Fatalf("%s not forwarded: reply=%s wait=%v", method, v.reply, v.wait != nil)
	}
}

func carrierReply(id int64, result map[string]any) []byte {
	return encode(map[string]any{"id": id, "sessionId": "S", "result": result})
}

func carrierTreeReply(t *testing.T, g *guard, id int64, method string, tree map[string]any) []string {
	t.Helper()
	carrierAsk(t, g, id, method)
	return carrierOut(g, g.fromBrowser(carrierReply(id, map[string]any{"frameTree": tree})))
}

func carrierHas(out []string, s string) bool {
	for _, o := range out {
		if strings.Contains(o, s) {
			return true
		}
	}
	return false
}

func carrierRefused(out []string) bool {
	return len(out) == 1 && strings.Contains(out[0], "slicc: refused: ") && !strings.Contains(out[0], `"result"`)
}

func carrierLatched(g *guard) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessionRefusedLocked("S")
}

func carrierNames(t *testing.T, out []string) map[string]string {
	t.Helper()
	names := map[string]string{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if f, ok := x["frame"].(map[string]any); ok {
				id, _ := f["id"].(string)
				name, _ := f["name"].(string)
				names[id] = name
			}
			for _, val := range x {
				walk(val)
			}
		case []any:
			for _, val := range x {
				walk(val)
			}
		}
	}
	for _, o := range out {
		var v any
		if err := json.Unmarshal([]byte(o), &v); err != nil {
			t.Fatalf("client bytes are not JSON: %s", o)
		}
		walk(v)
	}
	return names
}

func carrierStack(url string) map[string]any {
	return map[string]any{"description": carrierText, "callFrames": []any{map[string]any{"functionName": carrierText, "scriptId": "1", "url": url, "lineNumber": 1, "columnNumber": 1}}}
}

func TestFrameCarrierWrapperMainTreePrelearn(t *testing.T) {
	for _, method := range []string{"Page.getFrameTree", "Page.getResourceTree"} {
		for _, u := range carrierWrappers {
			for _, origin := range []string{"://", protect.Redact(u)} {
				g := carrierGuard(t)
				out := carrierTreeReply(t, g, 7, method, carrierNode(carrierFrame("P", "", u, origin, carrierText)))
				if !carrierRefused(out) || carrierHas(out, carrierText) {
					t.Fatalf("%s main %s origin %s: want refusal, got %v", method, u, origin, out)
				}
				if !carrierLatched(g) {
					t.Fatalf("%s main %s origin %s: session not latched", method, u, origin)
				}
			}
		}
	}
}

func TestFrameCarrierWrapperMainTreePostlearn(t *testing.T) {
	for _, method := range []string{"Page.getFrameTree", "Page.getResourceTree"} {
		g := carrierGuard(t)
		g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("P", "", carrierSeven, "://", carrierText), "type": "Navigation"}))
		if !carrierLatched(g) {
			t.Fatal("main wrapper navigation did not latch")
		}
		v := g.fromCommand(&command{id: 9, method: method, session: "S", params: map[string]any{}}, 9, true)
		if v.forward != nil || !strings.Contains(string(v.reply), "slicc: refused: ") {
			t.Fatalf("%s after latch: forward=%s reply=%s", method, v.forward, v.reply)
		}
	}
}

func TestFrameCarrierWrapperChildTreePrelearn(t *testing.T) {
	for _, method := range []string{"Page.getFrameTree", "Page.getResourceTree"} {
		for _, u := range carrierWrappers {
			g := carrierGuard(t)
			tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared),
				carrierNode(carrierFrame("C", "P", u, "://", carrierText)),
				carrierNode(carrierFrame("O", "P", "https://other.test/x", "https://other.test", carrierShared)))
			out := carrierTreeReply(t, g, 7, method, tree)
			if carrierRefused(out) || carrierLatched(g) {
				t.Fatalf("%s child %s under ordinary parent must not refuse: %v", method, u, out)
			}
			if carrierHas(out, carrierText) {
				t.Fatalf("%s child %s carrier delivered: %v", method, u, out)
			}
			names := carrierNames(t, out)
			if names["P"] != carrierShared || names["O"] != carrierShared || names["C"] != "" {
				t.Fatalf("%s child %s names: %v", method, u, names)
			}
			if carrierHas(out, "carrier-domain-"+carrierText) || !carrierHas(out, "carrier-domain-"+carrierShared) {
				t.Fatalf("%s child %s other frame strings: %v", method, u, out)
			}
		}
	}
}

func TestFrameCarrierWrapperChildTreePostlearn(t *testing.T) {
	for _, method := range []string{"Page.getFrameTree", "Page.getResourceTree"} {
		g := carrierGuard(t)
		g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("C", "P", carrierSeven, "://", carrierText), "type": "Navigation"}))
		v := g.fromCommand(&command{id: 8, method: method, session: "S", params: map[string]any{}}, 8, true)
		if v.forward == nil {
			if !strings.Contains(string(v.reply), "slicc: refused: ") {
				t.Fatalf("%s post-learn: neither forwarded nor refused: %s", method, v.reply)
			}
			continue
		}
		tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared), carrierNode(carrierFrame("C", "P", carrierSeven, "://", carrierText)))
		out := carrierOut(g, g.fromBrowser(carrierReply(8, map[string]any{"frameTree": tree})))
		if carrierHas(out, carrierText) {
			t.Fatalf("%s post-learn child carrier delivered: %v", method, out)
		}
	}
}

func TestFrameCarrierBlankChildInheritsFromTree(t *testing.T) {
	g := carrierGuard(t)
	tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared),
		carrierNode(carrierFrame("C", "P", carrierSeven, "://", carrierText),
			carrierNode(carrierFrame("B", "C", "about:blank", "://", carrierText+"-blank"))))
	out := carrierTreeReply(t, g, 7, "Page.getFrameTree", tree)
	if carrierHas(out, carrierText) {
		t.Fatalf("blank child under a protected tree parent delivered its carrier: %v", out)
	}
}

func TestFrameCarrierBlankChildOriginOnly(t *testing.T) {
	g := carrierGuard(t)
	tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared),
		carrierNode(carrierFrame("B", "P", "about:blank", "https://seven.sliccy.ai", carrierText)))
	out := carrierTreeReply(t, g, 7, "Page.getFrameTree", tree)
	if carrierHas(out, carrierText) {
		t.Fatalf("blank child with a protected origin delivered its carrier: %v", out)
	}
	g = carrierGuard(t)
	v := g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("B", "P", "about:blank", "https://vault.sliccy.ai", carrierText), "type": "Navigation"}))
	if out := carrierOut(g, v); carrierHas(out, carrierText) {
		t.Fatalf("blank child event with a protected origin delivered its carrier: %v", out)
	}
}

func TestFrameCarrierBlankChildUnknownParentFailsClosed(t *testing.T) {
	g := carrierGuard(t)
	tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared),
		carrierNode(carrierFrame("C", "P", carrierVault, "://", "carrier-evidence")),
		carrierNode(carrierFrame("B", "UNKNOWN", "about:blank", "://", carrierText)))
	out := carrierTreeReply(t, g, 7, "Page.getFrameTree", tree)
	if carrierHas(out, carrierText) {
		t.Fatalf("blank child with an unknown parent in a reply with protected evidence delivered its carrier: %v", out)
	}
	g = carrierGuard(t)
	g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("C", "P", carrierVault, "://", "carrier-evidence"), "type": "Navigation"}))
	v := g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("B", "UNKNOWN", "about:blank", "://", carrierText), "type": "Navigation"}))
	if out := carrierOut(g, v); carrierHas(out, carrierText) {
		t.Fatalf("blank child event with an unknown parent on a page with protected evidence delivered its carrier: %v", out)
	}
}

func TestFrameCarrierBlankChildUnknownParentOrdinaryKeeps(t *testing.T) {
	g := carrierGuard(t)
	tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared),
		carrierNode(carrierFrame("B", "UNKNOWN", "about:blank", "://", carrierShared)))
	out := carrierTreeReply(t, g, 7, "Page.getFrameTree", tree)
	if names := carrierNames(t, out); names["B"] != carrierShared {
		t.Fatalf("ordinary reply: blank child with an unknown parent lost its name: %v", out)
	}
}

func TestFrameCarrierErrorPageUnmarkedName(t *testing.T) {
	for _, unreachable := range []string{carrierFailed, ""} {
		g := carrierGuard(t)
		carrierArmErrorPage(g)
		f := carrierFrame("P", "", carrierErrorDoc, "://", carrierText)
		if unreachable != "" {
			f["unreachableUrl"] = unreachable
		}
		out := carrierTreeReply(t, g, 7, "Page.getFrameTree", carrierNode(f))
		if carrierRefused(out) || carrierLatched(g) || carrierHas(out, carrierText) {
			t.Fatalf("error page tree (unreachable=%q): want forwarded and blanked, got %v", unreachable, out)
		}
		v := g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": f, "type": "Navigation"}))
		if out := carrierOut(g, v); carrierLatched(g) || len(out) == 0 || carrierHas(out, carrierText) {
			t.Fatalf("error page frameNavigated (unreachable=%q): want forwarded and blanked, got %v", unreachable, out)
		}
	}
}

func TestFrameCarrierErrorPageHistoryAndRecovery(t *testing.T) {
	g := carrierGuard(t)
	carrierArmErrorPage(g)
	carrierAsk(t, g, 7, "Page.getNavigationHistory")
	entries := []any{
		map[string]any{"id": 1, "url": carrierPage, "userTypedURL": carrierPage, "title": carrierShared, "transitionType": "typed"},
		map[string]any{"id": 2, "url": carrierFailed, "userTypedURL": carrierFailed, "title": carrierText, "transitionType": "typed"},
		map[string]any{"id": 3, "url": carrierErrorDoc, "userTypedURL": carrierErrorDoc, "title": carrierText + "-error", "transitionType": "reload"},
	}
	out := carrierOut(g, g.fromBrowser(carrierReply(7, map[string]any{"currentIndex": 2, "entries": entries})))
	if carrierRefused(out) || carrierHas(out, carrierText) || !carrierHas(out, carrierShared) {
		t.Fatalf("error page history: want forwarded, protected titles blanked, ordinary kept: %v", out)
	}
	for i, c := range []*command{
		{id: 20, method: "Page.navigate", session: "S", params: map[string]any{"url": "https://safe.test/"}},
		{id: 21, method: "Page.reload", session: "S", params: map[string]any{}},
		{id: 22, method: "Page.navigateToHistoryEntry", session: "S", params: map[string]any{"entryId": 1}},
		{id: 23, method: "Page.getFrameTree", session: "S", params: map[string]any{}},
	} {
		v := g.fromCommand(c, c.id, true)
		if strings.Contains(string(v.reply), "slicc: refused: ") {
			t.Fatalf("recovery command %d (%s) refused on the error page: %s", i, c.method, v.reply)
		}
	}
}

func TestFrameCarrierErrorPageTargetInfo(t *testing.T) {
	g := carrierGuard(t)
	carrierArmErrorPage(g)
	info := map[string]any{"targetId": "P", "type": "page", "url": carrierErrorDoc, "title": carrierText, "attached": true}
	out := carrierOut(g, g.fromBrowser(encode(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": info}})))
	if carrierHas(out, carrierText) {
		t.Fatalf("error page targetInfo title delivered: %v", out)
	}
}

func TestFrameCarrierHistoryPrelearn(t *testing.T) {
	for _, u := range carrierWrappers {
		g := carrierGuard(t)
		carrierAsk(t, g, 7, "Page.getNavigationHistory")
		entries := []any{
			map[string]any{"id": 1, "url": carrierPage, "userTypedURL": carrierPage, "title": carrierShared, "transitionType": "typed"},
			map[string]any{"id": 2, "url": u, "userTypedURL": u, "title": carrierText, "transitionType": "typed"},
		}
		out := carrierOut(g, g.fromBrowser(carrierReply(7, map[string]any{"currentIndex": 1, "entries": entries})))
		if carrierRefused(out) || carrierLatched(g) {
			t.Fatalf("history with a protected current entry must never be refused: %v", out)
		}
		if carrierHas(out, carrierText) || !carrierHas(out, carrierShared) {
			t.Fatalf("history %s: want protected title blanked, ordinary kept: %v", u, out)
		}
	}
}

func TestFrameCarrierHistoryPostlearnChild(t *testing.T) {
	g := carrierGuard(t)
	g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("C", "P", carrierSeven, "://", carrierText), "type": "Navigation"}))
	carrierAsk(t, g, 7, "Page.getNavigationHistory")
	entries := []any{
		map[string]any{"id": 1, "url": carrierSeven, "userTypedURL": carrierSeven, "title": carrierText, "transitionType": "typed"},
		map[string]any{"id": 2, "url": carrierPage, "userTypedURL": carrierPage, "title": carrierShared, "transitionType": "typed"},
	}
	out := carrierOut(g, g.fromBrowser(carrierReply(7, map[string]any{"currentIndex": 1, "entries": entries})))
	if carrierRefused(out) || carrierHas(out, carrierText) || !carrierHas(out, carrierShared) {
		t.Fatalf("post-learn history: %v", out)
	}
}

func TestFrameCarrierChildFrameNavigated(t *testing.T) {
	for _, u := range carrierWrappers {
		g := carrierGuard(t)
		for range 2 {
			v := g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("C", "P", u, "://", carrierText), "type": "Navigation"}))
			out := carrierOut(g, v)
			if len(out) == 0 || carrierHas(out, carrierText) {
				t.Fatalf("child frameNavigated %s: want forwarded and blanked, got %v", u, out)
			}
		}
		if carrierLatched(g) {
			t.Fatalf("child frameNavigated %s latched the ordinary page", u)
		}
	}
}

func TestFrameCarrierChildDocumentOpened(t *testing.T) {
	g := carrierGuard(t)
	v := g.fromBrowser(carrierEvent("Page.documentOpened", map[string]any{"frame": carrierFrame("C", "P", carrierVault, "://", carrierText)}))
	if out := carrierOut(g, v); len(out) == 0 || carrierHas(out, carrierText) {
		t.Fatalf("child documentOpened: %v", out)
	}
}

func TestFrameCarrierMainEventLatch(t *testing.T) {
	for _, method := range []string{"Page.frameNavigated", "Page.documentOpened"} {
		for _, u := range carrierWrappers {
			g := carrierGuard(t)
			v := g.fromBrowser(carrierEvent(method, map[string]any{"frame": carrierFrame("P", "", u, "://", carrierText), "type": "Navigation"}))
			if out := carrierOut(g, v); len(out) != 0 {
				t.Fatalf("%s main %s: want dropped, got %v", method, u, out)
			}
			if !carrierLatched(g) {
				t.Fatalf("%s main %s: not latched", method, u)
			}
		}
	}
}

func TestFrameCarrierFrameAttachedStack(t *testing.T) {
	g := carrierGuard(t)
	v := g.fromBrowser(carrierEvent("Page.frameAttached", map[string]any{"frameId": "G", "parentFrameId": "C", "stack": carrierStack(carrierSeven)}))
	out := carrierOut(g, v)
	if len(out) == 0 || carrierHas(out, carrierText) || !carrierHas(out, `"frameId":"G"`) {
		t.Fatalf("frameAttached with a protected stack url: %v", out)
	}
	parent := map[string]any{"callFrames": []any{}, "parent": carrierStack(carrierVault)}
	v = g.fromBrowser(carrierEvent("Page.frameAttached", map[string]any{"frameId": "H", "parentFrameId": "C", "stack": parent}))
	if out := carrierOut(g, v); carrierHas(out, carrierText) {
		t.Fatalf("frameAttached with a protected async parent stack url: %v", out)
	}
}

func TestFrameCarrierOrderFrameNavigatedTargetInfo(t *testing.T) {
	iframe := map[string]any{"targetId": "C", "type": "iframe", "url": carrierSeven, "title": carrierText, "attached": false, "parentFrameId": "P"}
	nav := carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("C", "P", carrierSeven, "://", carrierText), "type": "Navigation"})
	info := encode(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": iframe}})
	for _, order := range [][][]byte{{nav, info}, {info, nav}} {
		g := carrierGuard(t)
		for _, msg := range order {
			if out := carrierOut(g, g.fromBrowser(msg)); carrierHas(out, carrierText) {
				t.Fatalf("child order: carrier delivered: %v", out)
			}
		}
	}
	page := map[string]any{"targetId": "P", "type": "page", "url": carrierSeven, "title": carrierText, "attached": true}
	mainNav := carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("P", "", carrierSeven, "://", carrierText), "type": "Navigation"})
	mainInfo := encode(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": page}})
	for _, order := range [][][]byte{{mainNav, mainInfo}, {mainInfo, mainNav}} {
		g := carrierGuard(t)
		for _, msg := range order {
			if out := carrierOut(g, g.fromBrowser(msg)); carrierHas(out, carrierText) {
				t.Fatalf("main order: carrier delivered: %v", out)
			}
		}
		if !carrierLatched(g) {
			t.Fatal("main order: not latched")
		}
	}
}

func TestFrameCarrierOrderFrameAttachedFrameNavigated(t *testing.T) {
	nav := carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("C", "P", carrierSeven, "://", carrierText), "type": "Navigation"})
	attach := carrierEvent("Page.frameAttached", map[string]any{"frameId": "G", "parentFrameId": "C", "stack": carrierStack(carrierSeven)})
	for _, order := range [][][]byte{{nav, attach}, {attach, nav}} {
		g := carrierGuard(t)
		for _, msg := range order {
			if out := carrierOut(g, g.fromBrowser(msg)); carrierHas(out, carrierText) {
				t.Fatalf("attach/navigate order: carrier delivered: %v", out)
			}
		}
	}
}

func TestFrameCarrierOrderTreeFrameNavigated(t *testing.T) {
	tree := func() map[string]any {
		return carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared), carrierNode(carrierFrame("C", "P", carrierVault, "://", carrierText)))
	}
	nav := carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("C", "P", carrierVault, "://", carrierText), "type": "Navigation"})
	g := carrierGuard(t)
	if out := carrierTreeReply(t, g, 7, "Page.getFrameTree", tree()); carrierHas(out, carrierText) {
		t.Fatalf("tree first: %v", out)
	}
	if out := carrierOut(g, g.fromBrowser(nav)); carrierHas(out, carrierText) {
		t.Fatalf("event after tree: %v", out)
	}
	g = carrierGuard(t)
	if out := carrierOut(g, g.fromBrowser(nav)); carrierHas(out, carrierText) {
		t.Fatalf("event first: %v", out)
	}
	v := g.fromCommand(&command{id: 8, method: "Page.getFrameTree", session: "S", params: map[string]any{}}, 8, true)
	if v.forward != nil {
		if out := carrierOut(g, g.fromBrowser(carrierReply(8, map[string]any{"frameTree": tree()}))); carrierHas(out, carrierText) {
			t.Fatalf("tree after event: %v", out)
		}
	}
}

func TestFrameCarrierSameValue(t *testing.T) {
	g := carrierGuard(t)
	v := g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("C", "P", carrierSeven, "://", carrierSeven+carrierText), "type": "Navigation"}))
	if out := carrierOut(g, v); carrierHas(out, carrierText) || carrierNames(t, out)["C"] != "" {
		t.Fatalf("name equal to the url: %v", out)
	}
	g = carrierGuard(t)
	v = g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("C", "P", carrierSeven, "://", carrierSeven), "type": "Navigation"}))
	if out := carrierOut(g, v); carrierNames(t, out)["C"] != "" {
		t.Fatalf("name identical to the url string: %v", out)
	}
	g = carrierGuard(t)
	tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared),
		carrierNode(carrierFrame("C", "P", carrierSeven, "://", carrierShared)),
		carrierNode(carrierFrame("O", "P", "https://other.test/", "https://other.test", carrierShared)))
	names := carrierNames(t, carrierTreeReply(t, g, 7, "Page.getFrameTree", tree))
	if names["P"] != carrierShared || names["O"] != carrierShared || names["C"] != "" {
		t.Fatalf("same name on protected and ordinary frames: %v", names)
	}
	g = carrierGuard(t)
	for _, title := range []string{carrierText, carrierText, carrierText + "-changed"} {
		info := map[string]any{"targetId": "T2", "type": "page", "url": "https://seven.sliccy.ai/os/", "title": title, "attached": false}
		if out := carrierOut(g, g.fromBrowser(encode(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": info}}))); carrierHas(out, carrierText) {
			t.Fatalf("same-url targetInfoChanged: %v", out)
		}
	}
}

func TestFrameCarrierWindowOpenAndDownload(t *testing.T) {
	g := carrierGuard(t)
	v := g.fromBrowser(carrierEvent("Page.windowOpen", map[string]any{"url": carrierSeven, "windowName": carrierText, "windowFeatures": []any{carrierText}, "userGesture": true}))
	if out := carrierOut(g, v); carrierHas(out, carrierText) {
		t.Fatalf("windowOpen: %v", out)
	}
	v = g.fromBrowser(carrierEvent("Page.downloadWillBegin", map[string]any{"frameId": "P", "guid": "g", "url": carrierVault, "suggestedFilename": carrierText}))
	if out := carrierOut(g, v); carrierHas(out, carrierText) {
		t.Fatalf("downloadWillBegin: %v", out)
	}
}

func TestFrameCarrierScrubOrder(t *testing.T) {
	g := carrierGuard(t)
	tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared), carrierNode(carrierFrame("C", "P", carrierExtWrap, "://", carrierText)))
	if out := carrierTreeReply(t, g, 7, "Page.getFrameTree", tree); carrierHas(out, carrierText) {
		t.Fatalf("classification must use the value before the url scrub: %v", out)
	}
}

func TestFrameCarrierStockCost(t *testing.T) {
	g := carrierGuard(t)
	tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared),
		carrierNode(carrierFrame("A", "P", "https://other.test/", "https://other.test", carrierShared+"-a"),
			carrierNode(carrierFrame("B", "A", "about:blank", "://", carrierShared+"-b"))))
	carrierAsk(t, g, 7, "Page.getFrameTree")
	reply := carrierReply(7, map[string]any{"frameTree": tree})
	v := g.fromBrowser(reply)
	if string(v.forward) != string(reply) || string(g.scrub(v.forward)) != string(reply) {
		t.Fatalf("ordinary tree reply changed:\n%s\n%s", reply, g.scrub(v.forward))
	}
	for _, s := range v.send {
		if strings.Contains(string(s), "getFrameTree") || strings.Contains(string(s), "getTargetInfo") || strings.Contains(string(s), "getNavigationHistory") {
			t.Fatalf("ordinary tree reply caused an extra read: %s", s)
		}
	}
	carrierAsk(t, g, 8, "Page.getNavigationHistory")
	hist := carrierReply(8, map[string]any{"currentIndex": 0, "entries": []any{map[string]any{"id": 1, "url": carrierPage, "userTypedURL": carrierPage, "title": carrierShared, "transitionType": "typed"}}})
	v = g.fromBrowser(hist)
	if string(g.scrub(v.forward)) != string(hist) || len(v.send) != 0 {
		t.Fatalf("ordinary history changed: %s send=%q", g.scrub(v.forward), v.send)
	}
	for _, msg := range [][]byte{
		carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("P", "", carrierPage+"?2", carrierOrigin, carrierShared), "type": "Navigation"}),
		carrierEvent("Page.documentOpened", map[string]any{"frame": carrierFrame("P", "", carrierPage+"?3", carrierOrigin, carrierShared)}),
		carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("A", "P", "https://other.test/2", "https://other.test", carrierShared), "type": "Navigation"}),
		carrierEvent("Page.frameAttached", map[string]any{"frameId": "Z", "parentFrameId": "P", "stack": map[string]any{"callFrames": []any{map[string]any{"functionName": carrierShared, "scriptId": "1", "url": carrierPage, "lineNumber": 1, "columnNumber": 1}}}}),
	} {
		v := g.fromBrowser(msg)
		if string(v.forward) != string(msg) || string(g.scrub(v.forward)) != string(msg) {
			t.Fatalf("ordinary event changed:\n%s\n%s", msg, g.scrub(v.forward))
		}
		if len(v.send) != 0 {
			t.Fatalf("ordinary event caused sends: %s -> %q", msg, v.send)
		}
	}
	if carrierLatched(g) {
		t.Fatal("ordinary traffic latched the session")
	}
}

const (
	carrierExt       = "chrome-extension://nkbihfbeogaeaoehlefnkodbefgpgknn/home.html"
	carrierExtOrigin = "chrome-extension://nkbihfbeogaeaoehlefnkodbefgpgknn"
	carrierOther     = "https://other.test/frame"
	carrierOtherOrig = "https://other.test"
)

type carrierParentCase struct {
	name   string
	url    string
	origin string
}

var carrierProtectedParents = []carrierParentCase{
	{"seven", "https://seven.sliccy.ai/os/", "https://seven.sliccy.ai"},
	{"wrapper", carrierSeven, "://"},
	{"vault-origin-only", "about:blank", "https://vault.sliccy.ai"},
}

func carrierFrameField(t *testing.T, out []string, id, key string) (string, bool) {
	t.Helper()
	var found string
	ok := false
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if f, isFrame := x["frame"].(map[string]any); isFrame {
				if fid, _ := f["id"].(string); fid == id {
					found, ok = f[key].(string)
				}
			}
			for _, val := range x {
				walk(val)
			}
		case []any:
			for _, val := range x {
				walk(val)
			}
		}
	}
	for _, o := range out {
		var v any
		if err := json.Unmarshal([]byte(o), &v); err != nil {
			t.Fatalf("client bytes are not JSON: %s", o)
		}
		walk(v)
	}
	return found, ok
}

func carrierAncestorTree(parentURL, parentOrigin, childName, grandName string) map[string]any {
	return carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared),
		carrierNode(carrierFrame("X", "P", parentURL, parentOrigin, "carrier-parent-name"),
			carrierNode(carrierFrame("C", "X", carrierOther, carrierOtherOrig, childName),
				carrierNode(carrierFrame("G", "C", carrierOther+"/g", carrierOtherOrig, grandName)))),
		carrierNode(carrierFrame("O", "P", carrierOther+"/o", carrierOtherOrig, carrierShared)))
}

func TestFrameCarrierAncestorNameTree(t *testing.T) {
	for _, method := range []string{"Page.getFrameTree", "Page.getResourceTree"} {
		for _, pc := range carrierProtectedParents {
			g := carrierGuard(t)
			out := carrierTreeReply(t, g, 7, method, carrierAncestorTree(pc.url, pc.origin, carrierText, carrierText+"-grand"))
			if carrierRefused(out) || carrierLatched(g) {
				t.Fatalf("%s %s: protected child under an ordinary page must not refuse: %v", method, pc.name, out)
			}
			names := carrierNames(t, out)
			if names["X"] != "" || names["C"] != "" || names["G"] != "" {
				t.Fatalf("%s %s: name of an ordinary frame under a protected ancestor delivered: %v", method, pc.name, names)
			}
			if names["P"] != carrierShared || names["O"] != carrierShared {
				t.Fatalf("%s %s: ordinary frames outside the protected subtree lost their names: %v", method, pc.name, names)
			}
			if d, _ := carrierFrameField(t, out, "C", "domainAndRegistry"); d != "carrier-domain-"+carrierText {
				t.Fatalf("%s %s: the ancestor rule must blank the name only, child domainAndRegistry=%q", method, pc.name, d)
			}
			if d, _ := carrierFrameField(t, out, "X", "domainAndRegistry"); d != "" {
				t.Fatalf("%s %s: the protected parent keeps text: %q", method, pc.name, d)
			}
		}
	}
}

func TestFrameCarrierAncestorNameOrdinaryTwin(t *testing.T) {
	g := carrierGuard(t)
	out := carrierTreeReply(t, g, 7, "Page.getFrameTree", carrierAncestorTree("https://third.test/x", "https://third.test", carrierShared+"-c", carrierShared+"-g"))
	names := carrierNames(t, out)
	if names["X"] != "carrier-parent-name" || names["C"] != carrierShared+"-c" || names["G"] != carrierShared+"-g" {
		t.Fatalf("ordinary ancestors: names must be kept: %v", names)
	}
}

func TestFrameCarrierAncestorNameSameValue(t *testing.T) {
	g := carrierGuard(t)
	out := carrierTreeReply(t, g, 7, "Page.getFrameTree", carrierAncestorTree(carrierVault, "://", "carrier-parent-name", carrierOther))
	names := carrierNames(t, out)
	if names["X"] != "" || names["C"] != "" || names["G"] != "" {
		t.Fatalf("same name as the protected parent / name equal to a url: %v", names)
	}
}

func TestFrameCarrierAncestorNameEvents(t *testing.T) {
	for _, pc := range carrierProtectedParents {
		for _, method := range []string{"Page.frameNavigated", "Page.documentOpened"} {
			g := carrierGuard(t)
			g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("X", "P", pc.url, pc.origin, "carrier-parent-name"), "type": "Navigation"}))
			out := carrierOut(g, g.fromBrowser(carrierEvent(method, map[string]any{"frame": carrierFrame("C", "X", carrierOther, carrierOtherOrig, carrierText), "type": "Navigation"})))
			if len(out) == 0 || carrierHas(out, `"name":"`+carrierText) {
				t.Fatalf("%s %s: child name under a known protected parent delivered: %v", method, pc.name, out)
			}
			if d, _ := carrierFrameField(t, out, "C", "domainAndRegistry"); d != "carrier-domain-"+carrierText {
				t.Fatalf("%s %s: event ancestor rule must blank the name only: %v", method, pc.name, out)
			}
			out = carrierOut(g, g.fromBrowser(carrierEvent(method, map[string]any{"frame": carrierFrame("G", "C", carrierOther+"/g", carrierOtherOrig, carrierText+"-grand"), "type": "Navigation"})))
			if carrierHas(out, `"name":"`+carrierText+"-grand") {
				t.Fatalf("%s %s: grandchild name under a protected ancestor delivered: %v", method, pc.name, out)
			}
		}
	}
}

func TestFrameCarrierAncestorNameEventOrders(t *testing.T) {
	parent := carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("X", "P", carrierVault, "://", "carrier-parent-name"), "type": "Navigation"})
	child := carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("C", "X", carrierOther, carrierOtherOrig, carrierText), "type": "Navigation"})
	g := carrierGuard(t)
	g.fromBrowser(parent)
	if out := carrierOut(g, g.fromBrowser(child)); carrierHas(out, `"name":"`+carrierText) {
		t.Fatalf("parent first: %v", out)
	}
	g = carrierGuard(t)
	g.fromBrowser(carrierEvent("Page.frameAttached", map[string]any{"frameId": "X", "parentFrameId": "P"}))
	g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("X", "P", "https://third.test/", "https://third.test", "x"), "type": "Navigation"}))
	if out := carrierOut(g, g.fromBrowser(child)); !carrierHas(out, `"name":"`+carrierText) {
		t.Fatalf("child first under an ordinary parent must keep its name (ordinary twin): %v", out)
	}
	g.fromBrowser(parent)
	if out := carrierOut(g, g.fromBrowser(child)); carrierHas(out, `"name":"`+carrierText) {
		t.Fatalf("child again after the parent turned protected: %v", out)
	}
	if out := carrierTreeReply(t, g, 9, "Page.getFrameTree", carrierAncestorTree(carrierVault, "://", carrierText, carrierText)); carrierHas(out, `"name":"`+carrierText) {
		t.Fatalf("tree after the events: %v", out)
	}
}

func TestFrameCarrierAncestorNameTargetInfo(t *testing.T) {
	iframe := map[string]any{"targetId": "C", "type": "iframe", "url": carrierOther, "title": carrierText, "attached": false, "parentFrameId": "X"}
	parent := carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("X", "P", carrierVault, "://", "carrier-parent-name"), "type": "Navigation"})
	info := encode(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": iframe}})
	for _, order := range [][][]byte{{parent, info}, {info, parent}} {
		g := carrierGuard(t)
		for _, msg := range order {
			if out := carrierOut(g, g.fromBrowser(msg)); carrierHas(out, carrierText) {
				t.Fatalf("iframe target under a protected parent delivered its title: %v", out)
			}
		}
	}
}

func carrierExtensionGuard(t *testing.T) *guard {
	t.Helper()
	g := carrierGuard(t)
	g.fromBrowser(carrierEvent("Page.frameAttached", map[string]any{"frameId": "E", "parentFrameId": "P"}))
	g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("E", "P", carrierExt, carrierExtOrigin, "carrier-ext-name"), "type": "Navigation"}))
	return g
}

func carrierExtensionTree() map[string]any {
	return carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared),
		carrierNode(carrierFrame("E", "P", carrierExt, carrierExtOrigin, "carrier-ext-name"),
			carrierNode(carrierFrame("EB", "E", "about:blank", "://", carrierShared+"-eb")),
			carrierNode(carrierFrame("EC", "E", carrierOther, carrierOtherOrig, carrierShared+"-ec"))),
		carrierNode(carrierFrame("O", "P", carrierOther+"/o", carrierOtherOrig, carrierShared+"-o")),
		carrierNode(carrierFrame("U", "UNKNOWN", "about:blank", "://", carrierShared+"-u")))
}

func TestFrameCarrierExtensionFrameTree(t *testing.T) {
	for _, method := range []string{"Page.getFrameTree", "Page.getResourceTree"} {
		for _, first := range []string{"event", "tree"} {
			var g *guard
			if first == "event" {
				g = carrierExtensionGuard(t)
			} else {
				g = carrierGuard(t)
			}
			out := carrierTreeReply(t, g, 7, method, carrierExtensionTree())
			if carrierRefused(out) || carrierLatched(g) {
				t.Fatalf("%s (%s first): a tree with an extension frame must not refuse or latch: %v", method, first, out)
			}
			names := carrierNames(t, out)
			for id, want := range map[string]string{"P": carrierShared, "E": "carrier-ext-name", "EB": carrierShared + "-eb", "EC": carrierShared + "-ec", "O": carrierShared + "-o", "U": carrierShared + "-u"} {
				if names[id] != want {
					t.Fatalf("%s (%s first): frame %s name=%q want %q (all %v)", method, first, id, names[id], want, names)
				}
			}
			if first == "event" && carrierHas(out, "nkbihfbeogaeaoehlefnkodbefgpgknn") {
				t.Fatalf("%s: tracked extension frame skipped the extension-frame scrub: %v", method, out)
			}
			if first == "tree" {
				g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("E", "P", carrierExt, carrierExtOrigin, "carrier-ext-name"), "type": "Navigation"}))
				if carrierLatched(g) {
					t.Fatalf("%s: extension frame event after the tree latched the page", method)
				}
			}
		}
	}
}

func TestFrameCarrierExtensionFrameEvents(t *testing.T) {
	g := carrierExtensionGuard(t)
	for _, msg := range [][]byte{
		carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("EC", "E", carrierOther, carrierOtherOrig, carrierShared+"-ec"), "type": "Navigation"}),
		carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("EB", "E", "about:blank", "://", carrierShared+"-eb"), "type": "Navigation"}),
		carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("U", "UNKNOWN", "about:blank", "://", carrierShared+"-u"), "type": "Navigation"}),
		carrierEvent("Page.documentOpened", map[string]any{"frame": carrierFrame("EC", "E", carrierOther, carrierOtherOrig, carrierShared+"-ec")}),
	} {
		v := g.fromBrowser(msg)
		if string(v.forward) != string(msg) || string(g.scrub(v.forward)) != string(msg) {
			t.Fatalf("event under an extension frame changed:\n%s\n%s", msg, g.scrub(v.forward))
		}
	}
	if carrierLatched(g) {
		t.Fatal("extension frame events latched the page")
	}
	v := g.fromBrowser(carrierEvent("Page.frameAttached", map[string]any{"frameId": "EZ", "parentFrameId": "E", "stack": carrierStack(carrierExt)}))
	if out := carrierOut(g, v); !carrierHas(out, `"stack"`) {
		t.Fatalf("frameAttached with an extension-frame stack must keep its stack shape: %v", out)
	}
}

func TestFrameCarrierDocumentOpenedDefaultParity(t *testing.T) {
	g := carrierGuard(t)
	for _, msg := range [][]byte{
		carrierEvent("Page.documentOpened", map[string]any{"frame": carrierFrame("P", "", carrierPage+"?d", carrierOrigin, carrierShared)}),
		carrierEvent("Page.documentOpened", map[string]any{"frame": carrierFrame("C", "P", carrierOther, carrierOtherOrig, carrierShared)}),
		carrierEvent("Page.documentOpened", map[string]any{"frame": carrierFrame("Q", "UNKNOWN", carrierOther, carrierOtherOrig, carrierShared)}),
		carrierEvent("Page.documentOpened", map[string]any{"frame": carrierFrame("P", "", carrierPage+"?d", carrierOrigin, carrierShared)}),
	} {
		v := g.fromBrowser(msg)
		if string(v.forward) != string(msg) || len(v.send) != 0 || len(v.page) != 0 || string(g.scrub(v.forward)) != string(msg) {
			t.Fatalf("ordinary documentOpened must forward as the default path did: forward=%s send=%q page=%q", v.forward, v.send, v.page)
		}
	}
	other := encode(map[string]any{"method": "Page.documentOpened", "sessionId": "NOPE", "params": map[string]any{"frame": carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared)}})
	if v := g.fromBrowser(other); string(v.forward) != string(other) || len(v.send) != 0 {
		t.Fatalf("documentOpened on an unknown session must forward as before: %s send=%q", v.forward, v.send)
	}
	carrierArmErrorPage(g)
	f := carrierFrame("P", "", carrierErrorDoc, "://", carrierText)
	f["unreachableUrl"] = carrierFailed
	if out := carrierOut(g, g.fromBrowser(carrierEvent("Page.documentOpened", map[string]any{"frame": f}))); carrierLatched(g) || len(out) == 0 || carrierHas(out, carrierText) {
		t.Fatalf("error page documentOpened: want forwarded, not latched, blanked: %v", out)
	}
	g = carrierGuard(t)
	g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("P", "", carrierSeven, "://", carrierText), "type": "Navigation"}))
	if out := carrierOut(g, g.fromBrowser(carrierEvent("Page.documentOpened", map[string]any{"frame": carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared)}))); len(out) != 0 {
		t.Fatalf("documentOpened on a refused session must stay dropped: %v", out)
	}
}

func carrierExtensionTabInfos() []map[string]any {
	var out []map[string]any
	for i, typ := range []string{"page", "tab"} {
		for j, u := range []string{carrierExt, "chrome-extension://aeblfdkhhhdcdjpifhhbdiojplfjncoa/popup.html#" + carrierText} {
			out = append(out, map[string]any{"targetId": "XT" + string(rune('a'+i*2+j)), "type": typ, "url": u, "title": carrierText, "attached": false})
		}
	}
	return out
}

func carrierTitleOriginOnly(t *testing.T, out []string, where string) {
	t.Helper()
	if carrierHas(out, carrierText) {
		t.Fatalf("%s: extension tab title or url carried free text: %v", where, out)
	}
	for _, o := range out {
		var v any
		_ = json.Unmarshal([]byte(o), &v)
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				if title, ok := x["title"].(string); ok {
					if u, _ := x["url"].(string); title != "" && title != u {
						t.Fatalf("%s: title %q is not the shown origin %q", where, title, u)
					}
				}
				for _, val := range x {
					walk(val)
				}
			case []any:
				for _, val := range x {
					walk(val)
				}
			}
		}
		walk(v)
	}
}

func TestFrameCarrierExtensionTabTitle(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		for _, info := range carrierExtensionTabInfos() {
			where := func(s string) string {
				return s + " " + info["type"].(string) + " " + info["url"].(string) + map[bool]string{true: " tracked", false: ""}[tracked]
			}
			newGuardFor := func() *guard {
				if tracked {
					return carrierExtensionGuard(t)
				}
				return carrierGuard(t)
			}
			for _, method := range []string{"Target.targetCreated", "Target.targetInfoChanged"} {
				g := newGuardFor()
				if method == "Target.targetInfoChanged" {
					g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}}))
				}
				out := carrierOut(g, g.fromBrowser(encode(map[string]any{"method": method, "params": map[string]any{"targetInfo": info}})))
				carrierTitleOriginOnly(t, out, where(method))
			}
			g := newGuardFor()
			if v := g.fromCommand(&command{id: 31, method: "Target.getTargets", params: map[string]any{}}, 31, true); v.forward == nil {
				t.Fatalf("%s: not forwarded: %s", where("Target.getTargets"), v.reply)
			}
			reply := encode(map[string]any{"id": 31, "result": map[string]any{"targetInfos": []any{info}}})
			carrierTitleOriginOnly(t, carrierOut(g, g.fromBrowser(reply)), where("Target.getTargets"))
			g = newGuardFor()
			g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}}))
			v := g.fromCommand(&command{id: 32, method: "Target.getTargetInfo", params: map[string]any{"targetId": info["targetId"]}}, 32, true)
			if v.forward != nil {
				reply := encode(map[string]any{"id": 32, "result": map[string]any{"targetInfo": info}})
				carrierTitleOriginOnly(t, carrierOut(g, g.fromBrowser(reply)), where("Target.getTargetInfo"))
			} else if strings.Contains(string(v.reply), carrierText) {
				t.Fatalf("%s: local reply carried free text: %s", where("Target.getTargetInfo"), v.reply)
			}
		}
	}
}

func TestFrameCarrierExtensionBelowProtected(t *testing.T) {
	g := carrierGuard(t)
	tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared),
		carrierNode(carrierFrame("X", "P", carrierVault, "://", "carrier-parent-name"),
			carrierNode(carrierFrame("E", "X", carrierExt, carrierExtOrigin, carrierText+"-e"),
				carrierNode(carrierFrame("O", "E", carrierOther, carrierOtherOrig, carrierText+"-o")))),
		carrierNode(carrierFrame("Q", "P", carrierOther+"/q", carrierOtherOrig, carrierShared+"-q")))
	out := carrierTreeReply(t, g, 7, "Page.getFrameTree", tree)
	names := carrierNames(t, out)
	if carrierRefused(out) || names["E"] != "" || names["O"] != "" || names["Q"] != carrierShared+"-q" || names["P"] != carrierShared {
		t.Fatalf("extension subtree below a protected frame: %v", names)
	}
	for _, order := range []string{"protected-first", "ordinary-first"} {
		g = carrierGuard(t)
		first := carrierVault
		if order == "ordinary-first" {
			first = "https://third.test/x"
		}
		g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("X", "P", first, protect.Redact(first), "x"), "type": "Navigation"}))
		g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("E", "X", carrierExt, carrierExtOrigin, carrierText+"-e"), "type": "Navigation"}))
		if order == "ordinary-first" {
			g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("X", "P", carrierVault, "://", "x"), "type": "Navigation"}))
		}
		out = carrierOut(g, g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("O", "E", carrierOther, carrierOtherOrig, carrierText+"-o"), "type": "Navigation"})))
		if carrierHas(out, `"name":"`+carrierText) || carrierLatched(g) {
			t.Fatalf("%s: ordinary frame below an extension frame below a protected frame: %v", order, out)
		}
	}
}

func carrierChainTree(top map[string]any, n int, name string) map[string]any {
	var node map[string]any
	for i := n; i >= 1; i-- {
		f := carrierFrame(fmt.Sprintf("D%d", i), fmt.Sprintf("D%d", i-1), fmt.Sprintf("%s/%d", carrierOther, i), carrierOtherOrig, fmt.Sprintf("%s-%d", name, i))
		if node == nil {
			node = carrierNode(f)
		} else {
			node = carrierNode(f, node)
		}
	}
	top["frame"].(map[string]any)["id"] = "D0"
	top["childFrames"] = []any{node}
	return carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared), top)
}

func TestFrameCarrierDepthTree(t *testing.T) {
	for _, n := range []int{maxFrameAncestors - 1, maxFrameAncestors + 1, maxFrameAncestors + 6} {
		g := carrierGuard(t)
		out := carrierTreeReply(t, g, 7, "Page.getFrameTree", carrierChainTree(carrierNode(carrierFrame("D0", "P", carrierVault, "://", "carrier-parent-name")), n, carrierText))
		if carrierHas(out, `"name":"`+carrierText) {
			t.Fatalf("depth %d under a protected frame: a name was delivered", n)
		}
		g = carrierGuard(t)
		out = carrierTreeReply(t, g, 8, "Page.getFrameTree", carrierChainTree(carrierNode(carrierFrame("D0", "P", "https://third.test/", "https://third.test", "carrier-parent-name")), n, carrierShared))
		if names := carrierNames(t, out); names[fmt.Sprintf("D%d", n)] != fmt.Sprintf("%s-%d", carrierShared, n) || names["D1"] != carrierShared+"-1" {
			t.Fatalf("depth %d ordinary chain: names must be kept", n)
		}
	}
}

func carrierChainEvents(g *guard, top string, n int, name string) []string {
	g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("D0", "P", top, protect.Redact(top), "carrier-parent-name"), "type": "Navigation"}))
	var out []string
	for i := 1; i <= n; i++ {
		out = carrierOut(g, g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame(fmt.Sprintf("D%d", i), fmt.Sprintf("D%d", i-1), fmt.Sprintf("%s/%d", carrierOther, i), carrierOtherOrig, fmt.Sprintf("%s-%d", name, i)), "type": "Navigation"})))
	}
	return out
}

func TestFrameCarrierDepthEvents(t *testing.T) {
	for _, n := range []int{maxFrameAncestors + 2, maxFrameAncestors + 8} {
		g := carrierGuard(t)
		if out := carrierChainEvents(g, carrierVault, n, carrierText); carrierHas(out, `"name":"`+carrierText) {
			t.Fatalf("event at depth %d under a protected frame delivered its name: %v", n, out)
		}
		g = carrierGuard(t)
		carrierChainEvents(g, "https://third.test/", n, carrierText)
		g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("D0", "P", carrierVault, "://", "carrier-parent-name"), "type": "Navigation"}))
		out := carrierOut(g, g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame(fmt.Sprintf("D%d", n), fmt.Sprintf("D%d", n-1), carrierOther, carrierOtherOrig, carrierText+"-again"), "type": "Navigation"})))
		if carrierHas(out, `"name":"`+carrierText+"-again") {
			t.Fatalf("event at depth %d after the top turned protected delivered its name: %v", n, out)
		}
		g = carrierGuard(t)
		if out := carrierChainEvents(g, "https://third.test/", n, carrierShared); !carrierHas(out, fmt.Sprintf(`"name":"%s-%d"`, carrierShared, n)) {
			t.Fatalf("ordinary chain at depth %d must keep its names: %v", n, out)
		}
	}
}

func TestFrameCarrierAncestryCycle(t *testing.T) {
	for _, protectedElsewhere := range []bool{true, false} {
		g := carrierGuard(t)
		if protectedElsewhere {
			g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("X", "P", carrierVault, "://", "x"), "type": "Navigation"}))
		}
		g.mu.Lock()
		g.frameParents["CA"] = "CB"
		g.frameParents["CB"] = "CA"
		g.mu.Unlock()
		out := carrierOut(g, g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("CC", "CA", carrierOther, carrierOtherOrig, carrierText), "type": "Navigation"})))
		delivered := carrierHas(out, `"name":"`+carrierText)
		if protectedElsewhere == delivered {
			t.Fatalf("cycle (protected frame on the page=%t): name delivered=%t: %v", protectedElsewhere, delivered, out)
		}
	}
}

func carrierSecondPage(g *guard) {
	page := map[string]any{"targetId": "PB", "type": "page", "url": "https://second.test/", "title": "second", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": page}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "SB", "waitingForDebugger": false, "targetInfo": page}}))
}

func carrierDeepProtectedChain(g *guard, session, root, prefix string, n int) {
	parent := root
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("%s%d", prefix, i)
		u, origin := fmt.Sprintf("%s/%s/%d", carrierOther, prefix, i), carrierOtherOrig
		if i == n {
			u, origin = carrierVault, "://"
		}
		g.fromBrowser(encode(map[string]any{"method": "Page.frameNavigated", "sessionId": session, "params": map[string]any{"frame": carrierFrame(id, parent, u, origin, "chain"), "type": "Navigation"}}))
		parent = id
	}
}

func carrierUnknownParentBlank(g *guard) []string {
	return carrierOut(g, g.fromBrowser(carrierEvent("Page.frameNavigated", map[string]any{"frame": carrierFrame("UB", "UNKNOWN-PARENT", "about:blank", "://", carrierShared+"-ub"), "type": "Navigation"})))
}

func TestFrameCarrierEvidenceScopedToPage(t *testing.T) {
	for _, n := range []int{3, maxFrameAncestors + 6} {
		g := carrierGuard(t)
		carrierSecondPage(g)
		carrierDeepProtectedChain(g, "SB", "PB", "B", n)
		if out := carrierUnknownParentBlank(g); !carrierHas(out, `"name":"`+carrierShared+"-ub") {
			t.Fatalf("depth %d: a protected chain rooted at another page blanked a name here: %v", n, out)
		}
		tree := carrierNode(carrierFrame("P", "", carrierPage, carrierOrigin, carrierShared), carrierNode(carrierFrame("UT", "UNKNOWN-PARENT", "about:blank", "://", carrierShared+"-ut")))
		if names := carrierNames(t, carrierTreeReply(t, g, 7, "Page.getFrameTree", tree)); names["UT"] != carrierShared+"-ut" {
			t.Fatalf("depth %d: tree answer here over-blanked by another page's chain: %v", n, names)
		}
		g = carrierGuard(t)
		carrierSecondPage(g)
		carrierDeepProtectedChain(g, "S", "P", "A", n)
		if out := carrierUnknownParentBlank(g); carrierHas(out, `"name":"`+carrierShared+"-ub") {
			t.Fatalf("depth %d: a protected chain rooted at this page must count: %v", n, out)
		}
	}
}

func TestFrameCarrierEvidenceUnresolvedChain(t *testing.T) {
	for _, shape := range []string{"cycle", "dangling"} {
		g := carrierGuard(t)
		carrierSecondPage(g)
		g.mu.Lock()
		g.childFrames["CX"] = carrierVault
		if shape == "cycle" {
			g.frameParents["CX"] = "CY"
			g.frameParents["CY"] = "CX"
		} else {
			g.frameParents["CX"] = "NOWHERE"
		}
		g.mu.Unlock()
		out := carrierUnknownParentBlank(g)
		kept := carrierHas(out, `"name":"`+carrierShared+"-ub")
		if shape == "cycle" && kept {
			t.Fatalf("a protected frame on an unresolved (cyclic) chain must count as evidence: %v", out)
		}
		if shape == "dangling" && !kept {
			t.Fatalf("a protected frame whose chain ends at an unknown frame is not counted: %v", out)
		}
	}
}

func carrierQuarantine(g *guard) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.navTargets["P"].historyLocked = true
}

func carrierRefusedVerdict(v pageVerdict) bool {
	return v.forward == nil && strings.Contains(string(v.reply), "slicc: refused: ")
}

func carrierQuarantineTree(main map[string]any) map[string]any {
	return carrierNode(main, carrierNode(carrierFrame("C", "P", "https://seven.sliccy.ai/os/child?q=carrier-query#carrier-fragment", "https://seven.sliccy.ai", carrierText)))
}

func TestFrameCarrierQuarantineOrdinaryTree(t *testing.T) {
	for _, method := range []string{"Page.getFrameTree", "Page.getResourceTree"} {
		g := carrierGuard(t)
		carrierQuarantine(g)
		tree := carrierQuarantineTree(carrierFrame("P", "", "about:blank", "://", carrierShared))
		tree["resources"] = []any{map[string]any{"url": "https://seven.sliccy.ai/os/app.js?q=carrier-query#carrier-fragment", "type": "Script", "mimeType": "text/javascript"}}
		out := carrierTreeReply(t, g, 7, method, tree)
		if carrierRefused(out) || carrierLatched(g) {
			t.Fatalf("%s on a quarantined tab with an ordinary document must stay readable: %v", method, out)
		}
		if carrierHas(out, carrierText) || carrierHas(out, "carrier-query") || carrierHas(out, "carrier-fragment") || carrierHas(out, "/os/") {
			t.Fatalf("%s: protected child text or url survived: %v", method, out)
		}
		if u, _ := carrierFrameField(t, out, "C", "url"); u != "https://seven.sliccy.ai" {
			t.Fatalf("%s: child url not cut to its origin: %q", method, u)
		}
		if carrierNames(t, out)["P"] != carrierShared {
			t.Fatalf("%s: ordinary main frame lost its name: %v", method, out)
		}
		if v := g.fromCommand(&command{id: 30, method: "Runtime.evaluate", session: "S", params: map[string]any{"expression": "1"}}, 30, true); !carrierRefusedVerdict(v) {
			t.Fatalf("%s: content command on a quarantined tab must stay refused: %s", method, v.reply)
		}
	}
}

func TestFrameCarrierQuarantineControlsStayAllowed(t *testing.T) {
	g := carrierGuard(t)
	carrierQuarantine(g)
	for _, c := range []*command{
		{id: 40, method: "Page.navigate", session: "S", params: map[string]any{"url": "https://safe.test/"}},
		{id: 41, method: "Page.reload", session: "S", params: map[string]any{}},
		{id: 42, method: "Page.navigateToHistoryEntry", session: "S", params: map[string]any{"entryId": 2}},
		{id: 43, method: "Page.getNavigationHistory", session: "S", params: map[string]any{}},
		{id: 44, method: "Page.resetNavigationHistory", session: "S", params: map[string]any{}},
	} {
		if v := g.fromCommand(c, c.id, true); carrierRefusedVerdict(v) {
			t.Fatalf("%s on a quarantined tab with an ordinary document refused: %s", c.method, v.reply)
		}
	}
}

func TestFrameCarrierQuarantineProtectedMainRefused(t *testing.T) {
	for _, method := range []string{"Page.getFrameTree", "Page.getResourceTree"} {
		g := carrierGuard(t)
		carrierQuarantine(g)
		out := carrierTreeReply(t, g, 7, method, carrierQuarantineTree(carrierFrame("P", "", "https://seven.sliccy.ai/os/path?q=carrier-query", "https://seven.sliccy.ai", carrierText)))
		if !carrierRefused(out) || carrierHas(out, carrierText) || !carrierLatched(g) {
			t.Fatalf("%s: protected main frame on a quarantined tab: want refused and latched, got %v", method, out)
		}
		if v := g.fromCommand(&command{id: 31, method: "Page.getNavigationHistory", session: "S", params: map[string]any{}}, 31, true); !carrierRefusedVerdict(v) {
			t.Fatalf("%s: the session must be refused after the latch: %s", method, v.reply)
		}
		v := g.fromCommand(&command{id: 32, method: "Target.closeTarget", params: map[string]any{"targetId": "P"}}, 32, true)
		if carrierRefusedVerdict(v) {
			t.Fatalf("%s: Target.closeTarget refused after the latch: %s", method, v.reply)
		}
	}
}

func TestFrameCarrierQuarantineRestoreRace(t *testing.T) {
	seven := carrierFrame("P", "", "https://seven.sliccy.ai/os/restored", "https://seven.sliccy.ai", carrierText)
	for _, order := range []string{"reply-first", "navigated-first"} {
		for _, method := range []string{"Page.getFrameTree", "Page.getResourceTree"} {
			g := carrierGuard(t)
			carrierQuarantine(g)
			if v := g.fromCommand(&command{id: 50, method: "Page.navigateToHistoryEntry", session: "S", params: map[string]any{"entryId": 1}}, 50, true); carrierRefusedVerdict(v) {
				t.Fatalf("%s: navigateToHistoryEntry refused on a quarantined tab: %s", order, v.reply)
			}
			carrierAsk(t, g, 51, method)
			nav := carrierEvent("Page.frameNavigated", map[string]any{"frame": seven, "type": "BackForwardCacheRestore"})
			reply := carrierReply(51, map[string]any{"frameTree": carrierNode(seven)})
			var out []string
			if order == "reply-first" {
				out = carrierOut(g, g.fromBrowser(reply))
				if navOut := carrierOut(g, g.fromBrowser(nav)); len(navOut) != 0 {
					t.Fatalf("%s %s: restore commit after the latch delivered: %v", order, method, navOut)
				}
			} else {
				if navOut := carrierOut(g, g.fromBrowser(nav)); len(navOut) != 0 {
					t.Fatalf("%s %s: restore commit delivered: %v", order, method, navOut)
				}
				out = carrierOut(g, g.fromBrowser(reply))
			}
			if !carrierRefused(out) || carrierHas(out, carrierText) || carrierHas(out, "/os/") {
				t.Fatalf("%s %s: tree reply racing the restore: want refused, got %v", order, method, out)
			}
			if !carrierLatched(g) {
				t.Fatalf("%s %s: restore race did not latch", order, method)
			}
		}
	}
}
