package cdp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	markSeven    = "https://seven.sliccy.ai"
	markOrdinary = "https://example.test"
)

func markGuard(t *testing.T) *guard {
	t.Helper()
	return newGuard(protect.MustNew([]string{"http://127.0.0.1:7001"}), t.Logf)
}

func markPage(t *testing.T, g *guard, id, url, opener string) {
	t.Helper()
	info := map[string]any{"targetId": id, "type": "page", "url": url, "title": "t0", "attached": true}
	if opener != "" {
		info["openerId"] = opener
	}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}}))
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S" + id, "waitingForDebugger": false, "targetInfo": info}}))
	g.mu.Lock()
	defer g.mu.Unlock()
	ready := make(chan struct{})
	close(ready)
	nt := &navTarget{target: id, session: "NAV" + id, ready: ready, done: true, loaderID: "L0" + id, documentURL: url}
	g.navTargets[id] = nt
	g.navSessions["NAV"+id] = nt
}

func markContext(g *guard, session, frame, origin, unique string, isDefault bool) browserVerdict {
	return g.fromBrowser(encode(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": session,
		"params": map[string]any{"context": map[string]any{
			"id": len(unique) + 10, "origin": origin, "name": "", "uniqueId": unique,
			"auxData": map[string]any{"isDefault": isDefault, "type": "default", "frameId": frame},
		}},
	}))
}

func markCommit(g *guard, session, frame, parent, loader, url string) {
	f := map[string]any{"id": frame, "loaderId": loader, "url": url, "securityOrigin": markOrdinary, "mimeType": "text/html"}
	if parent != "" {
		f["parentId"] = parent
	}
	g.fromBrowser(encode(map[string]any{"method": "Page.frameNavigated", "sessionId": session, "params": map[string]any{"frame": f, "type": "Navigation"}}))
}

func markInfo(g *guard, id, url, title string) {
	g.fromBrowser(encode(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": map[string]any{"targetId": id, "type": "page", "url": url, "title": title, "attached": true}}}))
}

func markProtected(g *guard, id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.targets[id].protected
}

func markFreshSession(t *testing.T, g *guard, id, sid string) (admitted, evaluated bool) {
	t.Helper()
	g.mu.Lock()
	url := g.targets[id].url
	g.mu.Unlock()
	info := map[string]any{"targetId": id, "type": "page", "url": url, "title": "t2", "attached": true}
	v := g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": sid, "waitingForDebugger": false, "targetInfo": info}}))
	detached := false
	for _, m := range v.send {
		if strings.Contains(string(m), `"Target.detachFromTarget"`) && strings.Contains(string(m), `"`+sid+`"`) {
			detached = true
		}
	}
	g.mu.Lock()
	s := g.sessions[sid]
	admitted = s != nil && !s.refused && !detached && v.forward != nil
	g.mu.Unlock()
	pv := g.fromCommand(&command{id: 900, method: "Runtime.evaluate", session: sid, params: map[string]any{"expression": "1"}}, 900, true)
	var reply struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(pv.reply, &reply)
	if pv.forward == nil && !strings.HasPrefix(reply.Error.Message, "slicc: refused: ") {
		t.Fatalf("Runtime.evaluate on %s neither forwarded nor refused: wait=%v reply=%s", sid, pv.wait != nil, pv.reply)
	}
	return admitted, pv.forward != nil
}

var markSessions int

func markExpect(t *testing.T, g *guard, id string, want bool) {
	t.Helper()
	markSessions++
	if got := markProtected(g, id); got != want {
		t.Fatalf("page %s protected=%v, want %v", id, got, want)
	}
	admitted, evaluated := markFreshSession(t, g, id, "F"+id+itoa(int64(markSessions)))
	if admitted == want || evaluated == want {
		t.Fatalf("page %s (protected=%v): fresh session admitted=%v evaluate forwarded=%v", id, want, admitted, evaluated)
	}
}

func markByContext(t *testing.T, g *guard, id, origin string) {
	t.Helper()
	v := markContext(g, "S"+id, id, origin, "mark-"+id, true)
	if v.forward != nil || !markProtected(g, id) {
		t.Fatalf("default context %q on %s: forwarded=%v protected=%v", origin, id, v.forward != nil, markProtected(g, id))
	}
}

func markNav(g *guard, id string, fn func(*navTarget)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn(g.navTargets[id])
}

const markURL = "https://example.test/"

func TestDefaultContextMarkHolds(t *testing.T) {
	for _, c := range []struct {
		name  string
		steps func(g *guard)
		want  bool
	}{
		{"title-only", func(g *guard) { markInfo(g, "P", markURL, "t1") }, true},
		{"ordinary-url-without-commit", func(g *guard) { markInfo(g, "P", "https://example.test/other", "t1") }, true},
		{"ordinary-url-repeated-without-commit", func(g *guard) {
			markInfo(g, "P", "https://example.test/a", "t1")
			markInfo(g, "P", "https://example.test/b", "t2")
			markInfo(g, "P", markURL, "t3")
		}, true},
		{"commit-then-ordinary-context-clears", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, false},
		{"info-before-context-then-clears", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markInfo(g, "P", "https://example.test/other", "t1")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
		}, false},
		{"commit-then-protected-context", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://seven.sliccy.ai/os/")
			markContext(g, "NAVP", "P", markSeven, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"late-ordinary-context-after-protected", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://seven.sliccy.ai/os/")
			markContext(g, "NAVP", "P", markSeven, "u1", true)
			markContext(g, "NAVP", "P", markOrdinary, "u2", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"ordinary-context-before-commit", func(g *guard) {
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-same-loader", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L0P", markURL)
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", markURL, "t1")
		}, true},
		{"commit-empty-loader", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "", "https://example.test/other")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-empty-loader-after-commit", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markCommit(g, "NAVP", "P", "", "", "https://example.test/other")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-on-client-session", func(g *guard) {
			g.mu.Lock()
			nt := g.navTargets["P"]
			f := map[string]any{"id": "P", "loaderId": "L1P", "url": "https://example.test/other", "securityOrigin": markOrdinary}
			g.navEventLocked(nt, &message{Method: "Page.frameNavigated", SessionID: "SP", Params: encode(map[string]any{"frame": f, "type": "Navigation"})})
			g.mu.Unlock()
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-of-child-frame", func(g *guard) {
			markNav(g, "P", func(nt *navTarget) { nt.iframe = true })
			markCommit(g, "NAVP", "C", "P", "L1C", "https://example.test/child")
			markNav(g, "P", func(nt *navTarget) { nt.iframe = false })
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-then-isolated-context", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", markOrdinary, "u1", false)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-then-child-default-context", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "C", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-then-lookalike-context", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", "about:blank", "u1", true)
			markContext(g, "NAVP", "P", markOrdinary, "u2", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-then-opaque-context", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", "://", "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-then-null-context", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", "null", "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-then-missing-origin", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", "", "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-then-unparseable-context", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", "http://[::1", "u1", true)
			markContext(g, "NAVP", "P", markOrdinary, "u2", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"commit-then-context-on-client-session", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "SP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"no-nav-session", func(g *guard) {
			g.mu.Lock()
			delete(g.navSessions, "NAVP")
			g.mu.Unlock()
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
			g.mu.Lock()
			g.navSessions["NAVP"] = g.navTargets["P"]
			g.mu.Unlock()
		}, true},
		{"same-document-navigation", func(g *guard) {
			g.fromBrowser(encode(map[string]any{"method": "Page.navigatedWithinDocument", "sessionId": "NAVP", "params": map[string]any{"frameId": "P", "url": markURL + "#x", "navigationType": "fragment"}}))
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", markURL+"#x", "t1")
		}, true},
		{"bfcache-restore-without-context", func(g *guard) {
			g.fromBrowser(encode(map[string]any{"method": "Page.frameNavigated", "sessionId": "NAVP", "params": map[string]any{"frame": map[string]any{"id": "P", "loaderId": "L1P", "url": "https://example.test/other", "securityOrigin": markOrdinary}, "type": "BackForwardCacheRestore"}}))
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
		{"bfcache-restore-with-ordinary-context-clears", func(g *guard) {
			g.fromBrowser(encode(map[string]any{"method": "Page.frameNavigated", "sessionId": "NAVP", "params": map[string]any{"frame": map[string]any{"id": "P", "loaderId": "L1P", "url": "https://example.test/other", "securityOrigin": markOrdinary}, "type": "BackForwardCacheRestore"}}))
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, false},
		{"confirm-fill-is-not-a-commit", func(g *guard) {
			g.mu.Lock()
			nt := g.navTargets["P"]
			ready := make(chan struct{})
			frame := confirmFrame{ID: "P", URL: markURL, LoaderID: "L9P", SecurityOrigin: markOrdinary}
			g.confirmFrameDocumentLocked(nt, &navTarget{target: "P", ready: ready}, g.targets["P"], frame, encode(map[string]any{"id": "P", "url": markURL, "loaderId": "L9P", "securityOrigin": markOrdinary}), 0)
			g.mu.Unlock()
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", markURL, "t1")
		}, true},
		{"cleared-then-marked-again", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
			markFreshSession(t, g, "P", "G1")
			markContext(g, "G1", "P", markSeven, "u2", true)
			markInfo(g, "P", "https://example.test/other", "t2")
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := markGuard(t)
			markPage(t, g, "P", markURL, "")
			markByContext(t, g, "P", markSeven)
			c.steps(g)
			markExpect(t, g, "P", c.want)
		})
	}
}

func TestDefaultContextMarkPagesApart(t *testing.T) {
	g := markGuard(t)
	markPage(t, g, "P", markURL, "")
	markPage(t, g, "Q", "https://example.test/q", "")
	markByContext(t, g, "P", markSeven)
	markCommit(g, "NAVQ", "Q", "", "L1Q", "https://example.test/q2")
	markContext(g, "NAVQ", "Q", markOrdinary, "uq", true)
	markInfo(g, "Q", "https://example.test/q2", "tq")
	markInfo(g, "P", markURL, "t1")
	markExpect(t, g, "P", true)
	markExpect(t, g, "Q", false)
	markByContext(t, g, "Q", markSeven)
	markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
	markContext(g, "NAVP", "P", markOrdinary, "u1", true)
	markInfo(g, "P", "https://example.test/other", "t2")
	markInfo(g, "Q", "https://example.test/q2", "tq2")
	markExpect(t, g, "P", false)
	markExpect(t, g, "Q", true)
}

func TestDefaultContextMarkInheritedBlank(t *testing.T) {
	for _, c := range []struct {
		name   string
		opener string
	}{
		{"protected-opener", "O"},
		{"unknown-opener", "GONE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := markGuard(t)
			if c.opener == "O" {
				o := map[string]any{"targetId": "O", "type": "page", "url": "https://seven.sliccy.ai/os/", "attached": false}
				g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": o}}))
			}
			markPage(t, g, "P", "about:blank", c.opener)
			if !markProtected(g, "P") {
				t.Fatal("setup: a blank popup of a protected or unknown opener is not protected")
			}
			markContext(g, "SP", "P", markSeven, "mark-P", true)
			markInfo(g, "P", "about:blank", "t1")
			markExpect(t, g, "P", true)
			markCommit(g, "NAVP", "P", "", "L1P", "about:blank")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "about:blank", "t2")
			markExpect(t, g, "P", true)
		})
	}
}

func TestDefaultContextMarkReason(t *testing.T) {
	var logs []string
	g := newGuard(protect.MustNew(nil), func(format string, args ...any) {
		if strings.Contains(format, "reason=") {
			logs = append(logs, strings.Join(strings.Fields(strings.ReplaceAll(format, "%s", "")), " "))
			for _, a := range args {
				if s, ok := a.(string); ok {
					logs = append(logs, s)
				}
			}
		}
	})
	markPage(t, g, "P", markURL, "")
	logs = nil
	markByContext(t, g, "P", markSeven)
	joined := strings.Join(logs, " ")
	if !strings.Contains(joined, "default-context-origin") || strings.Contains(joined, "sticky") {
		t.Fatalf("first mark logged %q", joined)
	}
}

func markTreeCall(t *testing.T, v browserVerdict, session string) int64 {
	t.Helper()
	for _, raw := range v.send {
		var c struct {
			ID        int64  `json:"id"`
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(raw, &c) == nil && c.Method == "Page.getFrameTree" && c.SessionID == session {
			return c.ID
		}
	}
	return 0
}

func markTreeReply(g *guard, id int64, loader string) {
	g.fromBrowser(encode(map[string]any{"id": id, "sessionId": "NAVP", "result": map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "P", "loaderId": loader, "url": markURL, "securityOrigin": markOrdinary}}}}))
}

func TestDefaultContextMarkEmptyLoader(t *testing.T) {
	for _, c := range []struct {
		name  string
		steps func(g *guard, tree int64)
		want  bool
	}{
		{"late-old-commit-is-baseline", func(g *guard, tree int64) {
			markCommit(g, "NAVP", "P", "", "LOLD", markURL)
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", markURL, "t1")
		}, true},
		{"tree-baseline-then-commit-clears", func(g *guard, tree int64) {
			markTreeReply(g, tree, "LOLD")
			markCommit(g, "NAVP", "P", "", "LNEW", "https://example.test/other")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, false},
		{"tree-baseline-then-old-commit-holds", func(g *guard, tree int64) {
			markTreeReply(g, tree, "LOLD")
			markCommit(g, "NAVP", "P", "", "LOLD", markURL)
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", markURL, "t1")
		}, true},
		{"new-document-before-baseline-holds-then-next-clears", func(g *guard, tree int64) {
			markCommit(g, "NAVP", "P", "", "LNEW", "https://example.test/other")
			markTreeReply(g, tree, "LNEW")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
			markExpect(t, g, "P", true)
			markCommit(g, "NAVP", "P", "", "LNEXT", "https://example.test/next")
			markContext(g, "NAVP", "P", markOrdinary, "u2", true)
			markInfo(g, "P", "https://example.test/next", "t2")
		}, false},
		{"confirm-tree-first-baseline-then-commit-clears", func(g *guard, tree int64) {
			g.mu.Lock()
			nt := g.navTargets["P"]
			frame := confirmFrame{ID: "P", URL: markURL, LoaderID: "LOLD", SecurityOrigin: markOrdinary}
			g.confirmFrameDocumentLocked(nt, &navTarget{target: "P", ready: make(chan struct{})}, g.targets["P"], frame, encode(map[string]any{"id": "P", "url": markURL, "loaderId": "LOLD", "securityOrigin": markOrdinary}), 0)
			g.mu.Unlock()
			markCommit(g, "NAVP", "P", "", "LNEW", "https://example.test/other")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, false},
		{"commit-baseline-then-commit-clears", func(g *guard, tree int64) {
			markCommit(g, "NAVP", "P", "", "LOLD", markURL)
			markCommit(g, "NAVP", "P", "", "LNEW", "https://example.test/other")
			markTreeReply(g, tree, "LNEW")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := markGuard(t)
			markPage(t, g, "P", markURL, "")
			markNav(g, "P", func(nt *navTarget) { nt.loaderID = "" })
			v := markContext(g, "SP", "P", markSeven, "mark-P", true)
			if v.forward != nil || !markProtected(g, "P") {
				t.Fatal("setup: the default context did not mark the page")
			}
			tree := markTreeCall(t, v, "NAVP")
			if tree == 0 {
				t.Fatalf("a mark with no recorded loader did not read the frame tree on the guard's session: %q", v.send)
			}
			c.steps(g, tree)
			markExpect(t, g, "P", c.want)
		})
	}
}

func TestDefaultContextMarkTargetReuse(t *testing.T) {
	g := markGuard(t)
	markPage(t, g, "P", markURL, "")
	markByContext(t, g, "P", markSeven)
	g.fromBrowser(encode(map[string]any{"method": "Target.targetDestroyed", "params": map[string]any{"targetId": "P"}}))
	info := map[string]any{"targetId": "P", "type": "page", "url": "https://example.test/again", "title": "t0", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}}))
	g.mu.Lock()
	ready := make(chan struct{})
	close(ready)
	nt := &navTarget{target: "P", session: "NAVP2", ready: ready, done: true, loaderID: "LAGAIN"}
	g.navTargets["P"] = nt
	g.navSessions["NAVP2"] = nt
	g.mu.Unlock()
	markExpect(t, g, "P", false)
}

func TestDefaultContextMarkURLLagHeldByHistory(t *testing.T) {
	g := markGuard(t)
	markPage(t, g, "P", "https://seven.sliccy.ai/os/", "")
	markCommit(g, "NAVP", "P", "", "L1P", "https://seven.sliccy.ai/os/")
	g.fromBrowser(encode(map[string]any{"method": "Page.frameStartedNavigating", "sessionId": "NAVP", "params": map[string]any{"frameId": "P", "url": "https://example.test/slow", "loaderId": "L2P", "navigationType": "differentDocument"}}))
	markInfo(g, "P", "https://example.test/slow", "seven title")
	g.mu.Lock()
	marked := g.targets["P"].contextMark != nil
	g.mu.Unlock()
	info := map[string]any{"targetId": "P", "type": "page", "url": "https://example.test/slow", "title": "t2", "attached": true}
	g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "X", "waitingForDebugger": false, "targetInfo": info}}))
	pv := g.fromCommand(&command{id: 901, method: "Runtime.evaluate", session: "X", params: map[string]any{"expression": "1"}}, 901, true)
	if marked || pv.forward != nil || !strings.Contains(string(pv.reply), "history may restore a protected document") {
		t.Fatalf("URL lag: context mark %v, evaluate forwarded=%v reply=%s", marked, pv.forward != nil, pv.reply)
	}
}

func markAttach(t *testing.T, g *guard, id, sid string) bool {
	t.Helper()
	g.mu.Lock()
	url := g.targets[id].url
	g.mu.Unlock()
	info := map[string]any{"targetId": id, "type": "page", "url": url, "title": "t3", "attached": true}
	v := g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": sid, "waitingForDebugger": false, "targetInfo": info}}))
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[sid]
	return s != nil && !s.refused && v.forward != nil
}

func markCommand(g *guard, id int64, sid, method string, params map[string]any) (bool, string) {
	pv := g.fromCommand(&command{id: id, method: method, session: sid, params: params}, id, true)
	var reply struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(pv.reply, &reply)
	return pv.forward != nil, reply.Error.Message
}

func markLatch(g *guard, id string, locked, seen bool) {
	markNav(g, id, func(nt *navTarget) {
		nt.historyLocked = locked
		nt.setupProtectedSeen = seen
	})
}

func TestDefaultContextMarkSetupLatch(t *testing.T) {
	setup := map[string]any{"bypass": true}
	t.Run("marked-and-history-locked", func(t *testing.T) {
		g := markGuard(t)
		markPage(t, g, "P", markURL, "")
		markLatch(g, "P", true, false)
		markByContext(t, g, "P", markSeven)
		if markAttach(t, g, "P", "A1") {
			t.Fatal("a fresh session on a marked, history-locked page was admitted")
		}
		if ok, msg := markCommand(g, 801, "A1", "Network.setBypassServiceWorker", setup); ok || !strings.HasPrefix(msg, "slicc: refused: ") {
			t.Fatalf("setup command on a marked, history-locked page: forwarded=%v reply=%q", ok, msg)
		}
	})
	t.Run("latch-cleared-mark-stays", func(t *testing.T) {
		g := markGuard(t)
		markPage(t, g, "P", markURL, "")
		markLatch(g, "P", true, true)
		markByContext(t, g, "P", markSeven)
		markLatch(g, "P", true, false)
		markInfo(g, "P", markURL, "t1")
		if !markProtected(g, "P") || markAttach(t, g, "P", "A2") {
			t.Fatal("clearing the setup latch unprotected a marked page")
		}
		if ok, msg := markCommand(g, 802, "A2", "Network.setBypassServiceWorker", setup); ok || !strings.HasPrefix(msg, "slicc: refused: ") {
			t.Fatalf("setup command after the latch cleared on a marked page: forwarded=%v reply=%q", ok, msg)
		}
	})
	t.Run("mark-cleared-latch-stays", func(t *testing.T) {
		g := markGuard(t)
		markPage(t, g, "P", markURL, "")
		markByContext(t, g, "P", markSeven)
		markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
		markContext(g, "NAVP", "P", markOrdinary, "u1", true)
		markInfo(g, "P", "https://example.test/other", "t1")
		if markProtected(g, "P") {
			t.Fatal("setup: the mark did not clear")
		}
		markLatch(g, "P", true, true)
		if !markAttach(t, g, "P", "A3") {
			t.Fatal("a fresh session on the cleared page was not admitted")
		}
		if ok, msg := markCommand(g, 803, "A3", "Network.setBypassServiceWorker", setup); ok || msg != "slicc: refused: "+refusedHistory {
			t.Fatalf("setup command with the latch set after the mark cleared: forwarded=%v reply=%q", ok, msg)
		}
		if ok, msg := markCommand(g, 804, "A3", "Runtime.evaluate", map[string]any{"expression": "1"}); ok || msg != "slicc: refused: "+refusedHistory {
			t.Fatalf("evaluate on a history-locked page after the mark cleared: forwarded=%v reply=%q", ok, msg)
		}
		markLatch(g, "P", true, false)
		if ok, msg := markCommand(g, 805, "A3", "Network.setBypassServiceWorker", setup); !ok {
			t.Fatalf("setup command with the latch clear after the mark cleared: refused %q", msg)
		}
	})
}

func TestDefaultContextMarkLookalike(t *testing.T) {
	for _, c := range []struct {
		name  string
		steps func(g *guard)
		want  bool
	}{
		{"title-only", func(g *guard) { markInfo(g, "P", markURL, "t1") }, true},
		{"commit-then-ordinary-context-clears", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", markOrdinary, "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, false},
		{"commit-then-lookalike-context-holds", func(g *guard) {
			markCommit(g, "NAVP", "P", "", "L1P", "https://example.test/other")
			markContext(g, "NAVP", "P", "ABOUT://", "u1", true)
			markInfo(g, "P", "https://example.test/other", "t1")
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := markGuard(t)
			markPage(t, g, "P", markURL, "")
			markByContext(t, g, "P", "about:blank")
			c.steps(g)
			markExpect(t, g, "P", c.want)
		})
	}
}
