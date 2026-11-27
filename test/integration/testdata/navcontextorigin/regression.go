package cdp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	ncoSeven    = "https://seven.sliccy.ai"
	ncoOrdinary = "https://example.test"
)

func ncoGuard(t *testing.T, origins ...string) *guard {
	t.Helper()
	if len(origins) == 0 {
		origins = []string{"http://127.0.0.1:7001", ncoSeven}
	}
	o, err := protect.New(origins)
	if err != nil {
		t.Fatal(err)
	}
	return newGuard(o, t.Logf)
}

func ncoInfo(id, url, opener string) map[string]any {
	info := map[string]any{"targetId": id, "type": "page", "url": url, "title": "t0", "attached": true}
	if opener != "" {
		info["openerId"] = opener
	}
	return info
}

func ncoCreate(g *guard, id, url, opener string) {
	g.fromBrowser(encode(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": ncoInfo(id, url, opener)}}))
}

func ncoChange(g *guard, id, url, opener string) {
	g.fromBrowser(encode(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": ncoInfo(id, url, opener)}}))
}

func ncoNav(g *guard, id, loader string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ready := make(chan struct{})
	close(ready)
	nt := &navTarget{target: id, session: "NAV" + id, ready: ready, done: true, loaderID: loader}
	g.navTargets[id] = nt
	g.navSessions["NAV"+id] = nt
}

func ncoDetaches(v browserVerdict, sid string) bool {
	for _, raw := range v.send {
		var call struct {
			Method string `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
			} `json:"params"`
		}
		if json.Unmarshal(raw, &call) == nil && call.Method == "Target.detachFromTarget" && call.Params.SessionID == sid {
			return true
		}
	}
	return false
}

func ncoAttach(g *guard, id, sid string) bool {
	g.mu.Lock()
	t := g.targets[id]
	url, opener := t.url, t.opener
	g.mu.Unlock()
	info := ncoInfo(id, url, opener)
	v := g.fromBrowser(encode(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": sid, "waitingForDebugger": false, "targetInfo": info}}))
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[sid]
	return s != nil && !s.refused && v.forward != nil && !ncoDetaches(v, sid)
}

var ncoIDs int64 = 5000

func ncoEvaluate(t *testing.T, g *guard, sid string) bool {
	t.Helper()
	ncoIDs++
	pv := g.fromCommand(&command{id: ncoIDs, method: "Runtime.evaluate", session: sid, params: map[string]any{"expression": "1"}}, ncoIDs, true)
	var reply struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(pv.reply, &reply)
	if pv.forward == nil && !strings.HasPrefix(reply.Error.Message, "slicc: refused: ") {
		t.Fatalf("Runtime.evaluate on %s neither forwarded nor refused: wait=%v reply=%s", sid, pv.wait != nil, pv.reply)
	}
	return pv.forward != nil
}

func ncoContext(g *guard, session, frame, origin string, id int64, isDefault bool) browserVerdict {
	return g.fromBrowser(encode(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": session,
		"params": map[string]any{"context": map[string]any{
			"id": id, "origin": origin, "name": "", "uniqueId": "u" + itoa(id),
			"auxData": map[string]any{"isDefault": isDefault, "type": "default", "frameId": frame},
		}},
	}))
}

func ncoCommit(g *guard, session, frame, parent, loader, url string) {
	f := map[string]any{"id": frame, "loaderId": loader, "url": url, "securityOrigin": ncoOrdinary, "mimeType": "text/html"}
	if parent != "" {
		f["parentId"] = parent
	}
	g.fromBrowser(encode(map[string]any{"method": "Page.frameNavigated", "sessionId": session, "params": map[string]any{"frame": f, "type": "Navigation"}}))
}

type ncoView struct {
	protected bool
	marked    bool
	origin    string
	refused   bool
}

func ncoState(g *guard, id, sid string) ncoView {
	g.mu.Lock()
	defer g.mu.Unlock()
	var v ncoView
	if t := g.targets[id]; t != nil {
		v.protected, v.marked = t.protected, t.contextMark != nil
	}
	if nt := g.navTargets[id]; nt != nil {
		v.origin = nt.defaultOrigin
	}
	if sid != "" {
		s := g.sessions[sid]
		v.refused = g.refused[sid] || g.hidden[sid] || s != nil && s.refused
	}
	return v
}

var ncoFresh int

func ncoExpect(t *testing.T, g *guard, id string, want bool) {
	t.Helper()
	ncoFresh++
	sid := fmt.Sprintf("F%s%d", id, ncoFresh)
	admitted := ncoAttach(g, id, sid)
	evaluated := ncoEvaluate(t, g, sid)
	if admitted == want || evaluated == want {
		t.Fatalf("page %s (protected=%v): fresh session admitted=%v evaluate forwarded=%v", id, want, admitted, evaluated)
	}
}

func TestNavContextOriginJudge(t *testing.T) {
	o := protect.MustNew([]string{"http://127.0.0.1:7001", ncoSeven})
	for _, c := range []struct {
		name      string
		origin    string
		protected bool
		ordinary  bool
	}{
		{"kitesurf", "about://", false, false},
		{"opaque", "://", false, false},
		{"upper", "ABOUT://", true, false},
		{"mixed", "About://", true, false},
		{"kitesurf-slash", "about:///", true, false},
		{"kitesurf-space", "about:// ", true, false},
		{"lookalike", "about:x", true, false},
		{"blank", "about:blank", true, false},
		{"bare-about", "about", true, false},
		{"unparseable", "http://[::1", true, false},
		{"protected", ncoSeven, true, false},
		{"null", "null", false, false},
		{"missing", "", false, false},
		{"ordinary", ncoOrdinary, false, true},
	} {
		t.Run("protected/"+c.name, func(t *testing.T) {
			if got := contextOriginProtected(o, c.origin); got != c.protected {
				t.Fatalf("contextOriginProtected(%q) = %v, want %v", c.origin, got, c.protected)
			}
		})
		t.Run("ordinary/"+c.name, func(t *testing.T) {
			if got := ordinaryContextOrigin(o, c.origin); got != c.ordinary {
				t.Fatalf("ordinaryContextOrigin(%q) = %v, want %v", c.origin, got, c.ordinary)
			}
		})
	}
}

type ncoVector struct {
	idx       int
	url       string
	origin    string
	opener    string
	previous  bool
	protected bool
}

func ncoVectors(t *testing.T) ([]string, []ncoVector) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "node_modules", "@ai-ecoverse", "slicc-browsers", "protected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var npm, mod struct {
		Origins []string                     `json:"origins"`
		Targets []map[string]json.RawMessage `json:"targets"`
	}
	if err := json.Unmarshal(raw, &npm); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(sliccbrowsers.ProtectedJSON(), &mod); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(npm, mod) {
		t.Fatal("the npm and Go pins of slicc-browsers carry different protected vectors")
	}
	var out []ncoVector
	for i, row := range npm.Targets {
		if _, ok := row["contextOrigin"]; !ok {
			continue
		}
		var c struct {
			URL           string `json:"url"`
			ContextOrigin string `json:"contextOrigin"`
			Opener        string `json:"opener"`
			Previous      bool   `json:"previous"`
			Protected     bool   `json:"protected"`
			DocumentURL   string `json:"documentUrl"`
		}
		b, _ := json.Marshal(row)
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		for k := range row {
			switch k {
			case "url", "contextOrigin", "opener", "previous", "protected":
			default:
				t.Fatalf("vector %d has a field the guard row does not drive: %s", i, k)
			}
		}
		out = append(out, ncoVector{i, c.URL, c.ContextOrigin, c.Opener, c.Previous, c.Protected})
	}
	return npm.Origins, out
}

func TestNavContextOriginVectors(t *testing.T) {
	origins, vectors := ncoVectors(t)
	nonEmpty := 0
	for _, v := range vectors {
		if v.origin != "" {
			nonEmpty++
		}
	}
	if len(vectors) != 26 || nonEmpty != 25 {
		t.Fatalf("%d contextOrigin vectors (%d non-empty), want 26 (25 non-empty)", len(vectors), nonEmpty)
	}
	protectedURL := strings.TrimSuffix(origins[len(origins)-1], "/") + "/"
	for _, v := range vectors {
		t.Run(fmt.Sprintf("idx%d", v.idx), func(t *testing.T) {
			g := ncoGuard(t, origins...)
			opener := ""
			switch v.opener {
			case "":
			case sliccbrowsers.OpenerProtected:
				ncoCreate(g, "O", protectedURL, "")
				opener = "O"
			case sliccbrowsers.OpenerUnprotected:
				ncoCreate(g, "O", ncoOrdinary+"/", "")
				opener = "O"
			case sliccbrowsers.OpenerUnknown:
				opener = "GONE"
			default:
				t.Fatalf("vector %d: opener %q", v.idx, v.opener)
			}
			if v.previous {
				ncoCreate(g, "P", protectedURL, opener)
				ncoChange(g, "P", v.url, opener)
			} else {
				ncoCreate(g, "P", v.url, opener)
			}
			ncoNav(g, "P", "L0P")
			before := ncoState(g, "P", "").protected
			admitted := ncoAttach(g, "P", "SP")
			if admitted == before {
				t.Fatalf("vector %d: client admitted=%v on a page protected=%v before its default context", v.idx, admitted, before)
			}
			ev := ncoContext(g, "NAVP", "P", v.origin, 11, true)
			ncoChange(g, "P", v.url, opener)
			got := ncoState(g, "P", "SP")
			if got.origin != v.origin {
				t.Fatalf("vector %d: recorded default-context origin %q, want %q", v.idx, got.origin, v.origin)
			}
			if got.protected != v.protected || got.refused != v.protected {
				t.Fatalf("vector %+v: protected=%v client refused=%v, want %v", v, got.protected, got.refused, v.protected)
			}
			if !before && v.protected && !ncoDetaches(ev, "SP") {
				t.Fatalf("vector %d: the default context protected the page but did not cut its client session", v.idx)
			}
			if ev.forward != nil {
				t.Fatalf("vector %d: the guard's own session event reached the client", v.idx)
			}
			if !v.protected && !ncoEvaluate(t, g, "SP") {
				t.Fatalf("vector %d: an unprotected page refused its admitted client", v.idx)
			}
			ncoExpect(t, g, "P", v.protected)
		})
	}
}

func TestNavContextOriginOrder(t *testing.T) {
	for _, c := range []struct {
		name   string
		origin string
		want   bool
	}{
		{"protected", ncoSeven, true},
		{"lookalike", "about:blank", true},
		{"lookalike-mixed", "AbOuT:blank", true},
		{"upper", "ABOUT://", true},
		{"unparseable", "http://[::1", true},
		{"bare-about", "about", true},
		{"kitesurf", "about://", false},
		{"opaque", "://", false},
		{"null", "null", false},
		{"ordinary", ncoOrdinary, false},
	} {
		t.Run("admitted-then/"+c.name, func(t *testing.T) {
			g := ncoGuard(t)
			ncoCreate(g, "P", "about:blank", "")
			ncoNav(g, "P", "L0P")
			if !ncoAttach(g, "P", "SP") || !ncoEvaluate(t, g, "SP") {
				t.Fatal("setup: the client was not admitted on a blank page with no default context yet")
			}
			v := ncoContext(g, "NAVP", "P", c.origin, 11, true)
			got := ncoState(g, "P", "SP")
			if got.protected != c.want || got.marked != c.want || got.refused != c.want || ncoDetaches(v, "SP") != c.want {
				t.Fatalf("origin %q after admission: %+v detached=%v, want %v", c.origin, got, ncoDetaches(v, "SP"), c.want)
			}
			if ncoEvaluate(t, g, "SP") == c.want {
				t.Fatalf("origin %q after admission: evaluate forwarded=%v", c.origin, !c.want)
			}
			ncoExpect(t, g, "P", c.want)
		})
		t.Run("before-client/"+c.name, func(t *testing.T) {
			g := ncoGuard(t)
			ncoCreate(g, "P", "about:blank", "")
			ncoNav(g, "P", "L0P")
			ncoContext(g, "NAVP", "P", c.origin, 11, true)
			got := ncoState(g, "P", "")
			if got.protected != c.want || got.marked != c.want {
				t.Fatalf("origin %q before any client: %+v, want %v", c.origin, got, c.want)
			}
			if admitted := ncoAttach(g, "P", "SP"); admitted == c.want {
				t.Fatalf("origin %q before any client: client admitted=%v", c.origin, admitted)
			}
			if ncoEvaluate(t, g, "SP") == c.want {
				t.Fatalf("origin %q before any client: evaluate forwarded=%v", c.origin, !c.want)
			}
		})
	}
}

func TestNavContextOriginFrames(t *testing.T) {
	for _, c := range []struct {
		name      string
		frame     string
		isDefault bool
		want      bool
	}{
		{"top-frame", "P", true, true},
		{"child-frame", "C", true, false},
		{"isolated-world", "P", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := ncoGuard(t)
			ncoCreate(g, "P", "about:blank", "")
			ncoNav(g, "P", "L0P")
			ncoAttach(g, "P", "SP")
			ncoContext(g, "NAVP", c.frame, "about:blank", 11, c.isDefault)
			got := ncoState(g, "P", "SP")
			g.mu.Lock()
			ev := g.evidenceLocked(g.targets["P"])
			g.mu.Unlock()
			wantOrigin := ""
			if c.want {
				wantOrigin = "about:blank"
			}
			if got.origin != wantOrigin || ev.ContextOrigin != wantOrigin || got.protected != c.want || got.refused != c.want {
				t.Fatalf("frame %q default %v: %+v evidence %q, want origin %q protected %v", c.frame, c.isDefault, got, ev.ContextOrigin, wantOrigin, c.want)
			}
		})
	}
}

func TestNavContextOriginTargetsApart(t *testing.T) {
	t.Run("other-target-frame", func(t *testing.T) {
		g := ncoGuard(t)
		ncoCreate(g, "P", "about:blank", "")
		ncoCreate(g, "Q", "about:blank", "")
		ncoNav(g, "P", "L0P")
		ncoNav(g, "Q", "L0Q")
		ncoContext(g, "NAVQ", "P", "about:blank", 11, true)
		p, q := ncoState(g, "P", ""), ncoState(g, "Q", "")
		if p != (ncoView{}) || q != (ncoView{}) {
			t.Fatalf("a context for P on Q's session: P %+v Q %+v", p, q)
		}
	})
	t.Run("reused-session", func(t *testing.T) {
		g := ncoGuard(t)
		ncoCreate(g, "P", "about:blank", "")
		ncoCreate(g, "Q", "about:blank", "")
		ncoNav(g, "P", "L0P")
		ncoNav(g, "Q", "L0Q")
		ncoContext(g, "NAVP", "P", "://", 11, true)
		g.mu.Lock()
		g.navDetachedLocked("NAVP")
		nq := g.navTargets["Q"]
		delete(g.navSessions, "NAVQ")
		nq.session = "NAVP"
		g.navSessions["NAVP"] = nq
		g.mu.Unlock()
		ncoContext(g, "NAVP", "Q", "about:blank", 12, true)
		ncoContext(g, "NAVP", "P", "about:blank", 13, true)
		p, q := ncoState(g, "P", ""), ncoState(g, "Q", "")
		if p != (ncoView{origin: "://"}) {
			t.Fatalf("P after its session id was reused by Q: %+v", p)
		}
		if q != (ncoView{protected: true, marked: true, origin: "about:blank"}) {
			t.Fatalf("Q on the reused session: %+v", q)
		}
	})
}

func TestNavContextOriginLifecycle(t *testing.T) {
	destroyed := func(g *guard, session string, params map[string]any) {
		g.fromBrowser(encode(map[string]any{"method": "Runtime.executionContextDestroyed", "sessionId": session, "params": params}))
	}
	cleared := func(g *guard, session string) {
		g.fromBrowser(encode(map[string]any{"method": "Runtime.executionContextsCleared", "sessionId": session, "params": map[string]any{}}))
	}
	for _, c := range []struct {
		name  string
		steps func(g *guard)
		want  string
	}{
		{"kept", func(*guard) {}, "://"},
		{"destroyed-clears", func(g *guard) {
			destroyed(g, "NAVP", map[string]any{"executionContextId": 11, "executionContextUniqueId": "u11"})
		}, ""},
		{"destroyed-by-id-clears", func(g *guard) { destroyed(g, "NAVP", map[string]any{"executionContextId": 11}) }, ""},
		{"destroyed-by-unique-clears", func(g *guard) {
			destroyed(g, "NAVP", map[string]any{"executionContextId": 0, "executionContextUniqueId": "u11"})
		}, ""},
		{"other-destroyed-keeps", func(g *guard) {
			destroyed(g, "NAVP", map[string]any{"executionContextId": 12, "executionContextUniqueId": "u12"})
		}, "://"},
		{"cleared-clears", func(g *guard) { cleared(g, "NAVP") }, ""},
		{"new-loader-clears", func(g *guard) { ncoCommit(g, "NAVP", "P", "", "L1P", ncoOrdinary+"/other") }, ""},
		{"empty-loader-clears", func(g *guard) { ncoCommit(g, "NAVP", "P", "", "", ncoOrdinary+"/other") }, ""},
		{"same-loader-keeps", func(g *guard) { ncoCommit(g, "NAVP", "P", "", "L0P", "about:blank") }, "://"},
		{"child-commit-keeps", func(g *guard) { ncoCommit(g, "NAVP", "C", "P", "L1C", ncoOrdinary+"/child") }, "://"},
		{"client-commit-keeps", func(g *guard) { ncoCommit(g, "SP", "P", "", "L1P", ncoOrdinary+"/other") }, "://"},
		{"client-cleared-keeps", func(g *guard) { cleared(g, "SP") }, "://"},
		{"client-context-keeps", func(g *guard) { ncoContext(g, "SP", "P", ncoOrdinary, 31, true) }, "://"},
		{"recreated-replaces", func(g *guard) { ncoContext(g, "NAVP", "P", ncoOrdinary, 12, true) }, ncoOrdinary},
		{"old-destroyed-after-replace-keeps", func(g *guard) {
			ncoContext(g, "NAVP", "P", ncoOrdinary, 12, true)
			destroyed(g, "NAVP", map[string]any{"executionContextId": 11, "executionContextUniqueId": "u11"})
		}, ncoOrdinary},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := ncoGuard(t)
			ncoCreate(g, "P", "about:blank", "")
			ncoNav(g, "P", "L0P")
			ncoAttach(g, "P", "SP")
			ncoContext(g, "NAVP", "P", "://", 11, true)
			if got := ncoState(g, "P", "SP").origin; got != "://" {
				t.Fatalf("setup: recorded %q", got)
			}
			c.steps(g)
			if got := ncoState(g, "P", "SP").origin; got != c.want {
				t.Fatalf("recorded default-context origin %q, want %q", got, c.want)
			}
		})
	}
}

func TestNavContextOriginEvidence(t *testing.T) {
	evidence := func(g *guard) protect.Evidence {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.evidenceLocked(g.targets["P"])
	}
	t.Run("context-origin", func(t *testing.T) {
		g := ncoGuard(t)
		ncoCreate(g, "P", "about:blank", "")
		ncoNav(g, "P", "L0P")
		ncoContext(g, "NAVP", "P", "://", 11, true)
		if ev := evidence(g); ev != (protect.Evidence{ContextOrigin: "://"}) {
			t.Fatalf("evidence %+v", ev)
		}
	})
	t.Run("not-ready", func(t *testing.T) {
		g := ncoGuard(t)
		ncoCreate(g, "P", "about:blank", "")
		ncoNav(g, "P", "L0P")
		ncoContext(g, "NAVP", "P", "://", 11, true)
		g.mu.Lock()
		nt := g.navTargets["P"]
		nt.done, nt.errorURL, nt.documentURL = false, ncoSeven+"/", "chrome-error://chromewebdata/"
		g.mu.Unlock()
		if ev := evidence(g); ev != (protect.Evidence{ContextOrigin: "://"}) {
			t.Fatalf("evidence %+v", ev)
		}
	})
	t.Run("with-error-page", func(t *testing.T) {
		g := ncoGuard(t)
		ncoCreate(g, "P", "about:blank", "")
		ncoNav(g, "P", "L0P")
		ncoContext(g, "NAVP", "P", "://", 11, true)
		g.mu.Lock()
		nt := g.navTargets["P"]
		nt.errorURL, nt.documentURL, nt.unsure = ncoSeven+"/", "chrome-error://chromewebdata/", true
		g.mu.Unlock()
		want := protect.Evidence{DocumentURL: "chrome-error://chromewebdata/", UnreachableURL: ncoSeven + "/", Navigating: true, ContextOrigin: "://"}
		if ev := evidence(g); ev != want {
			t.Fatalf("evidence %+v, want %+v", ev, want)
		}
	})
	for _, c := range []struct {
		name   string
		url    string
		want   bool
		reason string
	}{
		{"reason", "about:blank", true, "inheriting-context-origin"},
		{"reason-ordinary-url", ncoOrdinary + "/", false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := ncoGuard(t)
			ncoCreate(g, "P", c.url, "")
			ncoNav(g, "P", "L0P")
			ncoContext(g, "NAVP", "P", "about:blank", 11, true)
			g.mu.Lock()
			defer g.mu.Unlock()
			tg := g.targets["P"]
			if _, reason := g.decideReasonLocked(tg, false); reason != "default-context-origin" {
				t.Fatalf("marked page reason %q", reason)
			}
			forgetContextMark(tg)
			if got, reason := g.decideReasonLocked(tg, false); got != c.want || reason != c.reason {
				t.Fatalf("decision without the mark: %v %q, want %v %q", got, reason, c.want, c.reason)
			}
		})
	}
}

func TestNavContextOriginMarkClear(t *testing.T) {
	for _, c := range []struct {
		name    string
		origins []string
		want    bool
	}{
		{"kitesurf-holds", []string{"about://"}, true},
		{"opaque-holds", []string{"://"}, true},
		{"kitesurf-then-ordinary-clears", []string{"about://", ncoOrdinary}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := ncoGuard(t)
			ncoCreate(g, "P", ncoOrdinary+"/", "")
			ncoNav(g, "P", "L0P")
			ncoAttach(g, "P", "SP")
			if v := ncoContext(g, "SP", "P", ncoSeven, 21, true); v.forward != nil || !ncoState(g, "P", "SP").marked {
				t.Fatal("setup: the client's default context did not mark the page")
			}
			ncoCommit(g, "NAVP", "P", "", "L1P", ncoOrdinary+"/other")
			for i, origin := range c.origins {
				ncoContext(g, "NAVP", "P", origin, int64(30+i), true)
			}
			ncoChange(g, "P", ncoOrdinary+"/other", "")
			if got := ncoState(g, "P", "").marked; got != c.want {
				t.Fatalf("mark after a commit and %q: %v, want %v", c.origins, got, c.want)
			}
			ncoExpect(t, g, "P", c.want)
		})
	}
}

func TestNavContextOriginClient(t *testing.T) {
	for _, c := range []struct {
		name      string
		origin    string
		forwarded bool
		frame     bool
	}{
		{"kitesurf", "about://", false, true},
		{"opaque", "://", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := ncoGuard(t)
			ncoCreate(g, "P", "about:blank", "")
			ncoNav(g, "P", "L0P")
			ncoAttach(g, "P", "SP")
			v := ncoContext(g, "SP", "P", c.origin, 21, true)
			got := ncoState(g, "P", "SP")
			g.mu.Lock()
			_, frame := g.childFrames["P"]
			g.mu.Unlock()
			if got.protected || got.marked || got.refused || ncoDetaches(v, "SP") {
				t.Fatalf("client default context %q: %+v", c.origin, got)
			}
			if (v.forward != nil) != c.forwarded || frame != c.frame {
				t.Fatalf("client default context %q: forwarded=%v frame record=%v, want %v %v", c.origin, v.forward != nil, frame, c.forwarded, c.frame)
			}
			if !ncoEvaluate(t, g, "SP") {
				t.Fatalf("client default context %q: evaluate refused", c.origin)
			}
			ncoExpect(t, g, "P", false)
		})
	}
}

type ncoWire struct {
	t      *testing.T
	g      *guard
	ledger []string
	queue  [][]byte
	urls   map[string]string
}

func ncoCanon(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (w *ncoWire) sent(kind string, raw []byte) {
	var m struct {
		ID        *int64          `json:"id"`
		Method    string          `json:"method"`
		SessionID string          `json:"sessionId"`
		Params    json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		w.t.Fatalf("undecodable %s: %s", kind, raw)
	}
	w.ledger = append(w.ledger, kind+" "+m.SessionID+" "+m.Method+" "+ncoCanon(m.Params))
	if m.ID == nil {
		return
	}
	var p struct {
		TargetID string `json:"targetId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	target := strings.TrimPrefix(m.SessionID, "NAV")
	url := w.urls[target]
	var result any = map[string]any{}
	switch m.Method {
	case "Target.attachToTarget":
		result = map[string]any{"sessionId": "NAV" + p.TargetID}
	case "Page.getNavigationHistory":
		result = map[string]any{"currentIndex": 0, "entries": []any{map[string]any{"id": 1, "url": url, "userTypedURL": url, "title": "", "transitionType": "typed"}}}
	case "Target.getTargetInfo":
		result = map[string]any{"targetInfo": map[string]any{"targetId": p.TargetID, "type": "page", "url": w.urls[p.TargetID], "title": "", "attached": true, "browserContextId": "CTX"}}
	case "Page.getFrameTree":
		result = map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": target, "loaderId": "L0" + target, "url": url, "securityOrigin": "://", "mimeType": "text/html"}}}
	}
	reply := map[string]any{"id": *m.ID, "result": result}
	if m.SessionID != "" {
		reply["sessionId"] = m.SessionID
	}
	w.queue = append(w.queue, encode(reply))
}

func (w *ncoWire) take(v browserVerdict) {
	for _, raw := range v.send {
		w.sent("send", raw)
	}
}

func (w *ncoWire) drain() {
	for n := 0; len(w.queue) > 0; n++ {
		if n > 1000 {
			w.t.Fatal("the fake browser did not settle")
		}
		raw := w.queue[0]
		w.queue = w.queue[1:]
		w.take(w.g.fromBrowser(raw))
	}
}

func (w *ncoWire) browser(msg map[string]any) {
	w.take(w.g.fromBrowser(encode(msg)))
	w.drain()
}

func (w *ncoWire) command(session, method string) {
	ncoIDs++
	pv := w.g.fromCommand(&command{id: ncoIDs, method: method, session: session, params: map[string]any{}}, ncoIDs, true)
	for _, raw := range pv.send {
		w.sent("send", raw)
	}
	switch {
	case pv.forward != nil:
		w.sent("forward", pv.forward)
	case pv.reply != nil:
		w.ledger = append(w.ledger, "reply "+session+" "+method)
	default:
		w.ledger = append(w.ledger, "held "+session+" "+method)
	}
	w.drain()
}

func (w *ncoWire) context(session, frame, origin string, id int64) {
	w.browser(map[string]any{"method": "Runtime.executionContextCreated", "sessionId": session, "params": map[string]any{"context": map[string]any{
		"id": id, "origin": origin, "name": "", "uniqueId": "u" + itoa(id), "auxData": map[string]any{"isDefault": true, "type": "default", "frameId": frame},
	}}})
}

func (w *ncoWire) run(seven bool) []string {
	w.g.relaySession = "RELAY"
	w.urls = map[string]string{"P": "about:blank"}
	info := map[string]any{"targetId": "P", "type": "page", "url": "about:blank", "title": "", "attached": false, "browserContextId": "CTX"}
	w.browser(map[string]any{"method": "Target.targetCreated", "sessionId": "RELAY", "params": map[string]any{"targetInfo": info}})
	w.browser(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}})
	attached := map[string]any{"targetId": "P", "type": "page", "url": "about:blank", "title": "", "attached": true, "browserContextId": "CTX"}
	w.browser(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "SP", "waitingForDebugger": false, "targetInfo": attached}})
	w.context("NAVP", "P", "://", 1)
	w.command("SP", "Runtime.enable")
	w.context("SP", "P", "://", 1)
	if seven {
		w.context("SP", "P", ncoSeven, 2)
		w.context("NAVP", "P", ncoSeven, 2)
		return w.ledger
	}
	w.command("SP", "Page.enable")
	w.browser(map[string]any{"method": "Page.frameNavigated", "sessionId": "NAVP", "params": map[string]any{"type": "Navigation", "frame": map[string]any{"id": "P", "loaderId": "L1P", "url": ncoOrdinary + "/", "securityOrigin": ncoOrdinary, "mimeType": "text/html"}}})
	w.context("NAVP", "P", ncoOrdinary, 3)
	w.context("SP", "P", ncoOrdinary, 3)
	w.urls["P"] = ncoOrdinary + "/"
	changed := map[string]any{"targetId": "P", "type": "page", "url": ncoOrdinary + "/", "title": "x", "attached": true, "browserContextId": "CTX"}
	w.browser(map[string]any{"method": "Target.targetInfoChanged", "sessionId": "RELAY", "params": map[string]any{"targetInfo": changed}})
	w.browser(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{"targetInfo": changed}})
	w.command("SP", "Runtime.evaluate")
	return w.ledger
}

var ncoLedgers = map[string][]string{
	"ordinary-run": {
		`send RELAY Target.attachToTarget {"flatten":true,"targetId":"P"}`,
		`send NAVP Fetch.enable {"patterns":[{"requestStage":"Request","urlPattern":"*://*sliccy.ai/*"},{"requestStage":"Request","urlPattern":"*://*sliccy.ai./*"},{"requestStage":"Request","urlPattern":"*://*sliccy.ai:*/*"},{"requestStage":"Request","urlPattern":"*://*sliccy.ai.:*/*"},{"requestStage":"Request","urlPattern":"http://*127.0.0.1:7001/*"},{"requestStage":"Request","urlPattern":"https://*seven.sliccy.ai/*"},{"requestStage":"Request","urlPattern":"https://*seven.sliccy.ai./*"}]}`,
		`send NAVP Network.enable {"maxPostDataSize":0,"maxResourceBufferSize":0,"maxTotalBufferSize":0}`,
		`send NAVP Network.setBypassServiceWorker {"bypass":true}`,
		`send NAVP Runtime.enable {}`,
		`send NAVP Page.enable {}`,
		`send NAVP Page.getNavigationHistory {}`,
		`send RELAY Target.getTargetInfo {"targetId":"P"}`,
		`forward SP Runtime.enable {}`,
		`forward SP Page.enable {}`,
		`forward SP Runtime.evaluate {}`,
		`send RELAY Target.getTargetInfo {"targetId":"P"}`,
	},
	"client-marked-then-nav": {
		`send RELAY Target.attachToTarget {"flatten":true,"targetId":"P"}`,
		`send NAVP Fetch.enable {"patterns":[{"requestStage":"Request","urlPattern":"*://*sliccy.ai/*"},{"requestStage":"Request","urlPattern":"*://*sliccy.ai./*"},{"requestStage":"Request","urlPattern":"*://*sliccy.ai:*/*"},{"requestStage":"Request","urlPattern":"*://*sliccy.ai.:*/*"},{"requestStage":"Request","urlPattern":"http://*127.0.0.1:7001/*"},{"requestStage":"Request","urlPattern":"https://*seven.sliccy.ai/*"},{"requestStage":"Request","urlPattern":"https://*seven.sliccy.ai./*"}]}`,
		`send NAVP Network.enable {"maxPostDataSize":0,"maxResourceBufferSize":0,"maxTotalBufferSize":0}`,
		`send NAVP Network.setBypassServiceWorker {"bypass":true}`,
		`send NAVP Runtime.enable {}`,
		`send NAVP Page.enable {}`,
		`send NAVP Page.getNavigationHistory {}`,
		`send RELAY Target.getTargetInfo {"targetId":"P"}`,
		`forward SP Runtime.enable {}`,
		`send  Target.detachFromTarget {"sessionId":"SP"}`,
		`send NAVP Page.getFrameTree {}`,
	},
}

func TestNavContextOriginLedger(t *testing.T) {
	for _, c := range []struct {
		name  string
		seven bool
	}{
		{"ordinary-run", false},
		{"client-marked-then-nav", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := (&ncoWire{t: t, g: ncoGuard(t)}).run(c.seven)
			again := (&ncoWire{t: t, g: ncoGuard(t)}).run(c.seven)
			if !reflect.DeepEqual(got, again) {
				t.Fatalf("the ledger is not deterministic:\n%s\n--\n%s", strings.Join(got, "\n"), strings.Join(again, "\n"))
			}
			want := ncoLedgers[c.name]
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("the commands the guard sent changed:\ngot\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

func TestNavContextOriginURLProtected(t *testing.T) {
	for _, c := range []struct {
		name   string
		url    string
		origin string
		marked bool
	}{
		{"seven-url-gets-no-nav-mark", ncoSeven + "/", ncoSeven, false},
		{"vault-url-gets-no-nav-mark", "https://vault.sliccy.ai/", "https://vault.sliccy.ai", false},
		{"settings-url-gets-no-nav-mark", "about:settings", "about://settings", false},
		{"unparseable-url-gets-no-nav-mark", "http://[::1", "http://[::1", false},
		{"blank-url-protected-origin-marks", "about:blank", ncoSeven, true},
		{"blank-url-lookalike-marks", "about:blank", "about:blank", true},
		{"ordinary-url-lookalike-marks", ncoOrdinary + "/", "about:blank", true},
		{"ordinary-url-protected-origin-marks", ncoOrdinary + "/", ncoSeven, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := ncoGuard(t)
			ncoCreate(g, "P", c.url, "")
			ncoNav(g, "P", "L0P")
			v := ncoContext(g, "NAVP", "P", c.origin, 11, true)
			got := ncoState(g, "P", "")
			if got.marked != c.marked || !got.protected || got.origin != c.origin || v.forward != nil {
				t.Fatalf("url %q origin %q: %+v forwarded=%v, want marked=%v protected", c.url, c.origin, got, v.forward != nil, c.marked)
			}
			ncoExpect(t, g, "P", true)
		})
	}
}

func ncoNavigate(g *guard, order string) {
	commit := func() { ncoCommit(g, "NAVP", "P", "", "L1P", ncoOrdinary+"/") }
	context := func() { ncoContext(g, "NAVP", "P", ncoOrdinary, 12, true) }
	switch order {
	case "commit-then-context":
		commit()
		context()
	case "context-before-commit":
		context()
		commit()
	case "no-context-after-commit":
		commit()
	}
	ncoChange(g, "P", ncoOrdinary+"/", "")
}

func TestNavContextOriginSevenToOrdinary(t *testing.T) {
	for _, c := range []struct{ name, order string }{
		{"commit-then-context-releases", "commit-then-context"},
		{"context-before-commit-releases", "context-before-commit"},
		{"no-context-after-commit-releases", "no-context-after-commit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := ncoGuard(t)
			ncoCreate(g, "P", ncoSeven+"/", "")
			ncoNav(g, "P", "L0P")
			ncoContext(g, "NAVP", "P", ncoSeven, 11, true)
			if got := ncoState(g, "P", ""); !got.protected || got.marked {
				t.Fatalf("setup: a seven page %+v, want protected by its URL and not marked", got)
			}
			if ncoAttach(g, "P", "SP") {
				t.Fatal("setup: a client attached to a seven page")
			}
			ncoNavigate(g, c.order)
			if got := ncoState(g, "P", ""); got.protected || got.marked {
				t.Fatalf("%s: the page that left seven for an ordinary page is %+v, want released", c.order, got)
			}
			ncoExpect(t, g, "P", false)
		})
	}
}

func TestNavContextOriginBlankMarkThenOrdinary(t *testing.T) {
	for _, c := range []struct {
		name, order string
		marked      bool
	}{
		{"commit-then-context-releases", "commit-then-context", false},
		{"context-before-commit-stays-marked", "context-before-commit", true},
		{"no-context-after-commit-stays-marked", "no-context-after-commit", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := ncoGuard(t)
			ncoCreate(g, "P", "about:blank", "")
			ncoNav(g, "P", "L0P")
			ncoContext(g, "NAVP", "P", ncoSeven, 11, true)
			if got := ncoState(g, "P", ""); !got.protected || !got.marked {
				t.Fatalf("setup: a blank page whose default context reports seven's origin is %+v, want marked", got)
			}
			ncoNavigate(g, c.order)
			if got := ncoState(g, "P", ""); got.protected != c.marked || got.marked != c.marked {
				t.Fatalf("%s: %+v, want marked and protected %v", c.order, got, c.marked)
			}
			ncoExpect(t, g, "P", c.marked)
		})
	}
}
