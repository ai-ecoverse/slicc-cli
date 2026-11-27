package lease

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLeaseBaselineR12RedirectCreate(t *testing.T) {
	r := blSetup(t)
	for _, kind := range []string{"exact", "path-prefix", "origin"} {
		for _, target := range []string{"same", "inprefix", "other", "protected", "error"} {
			b := r.broker()
			n := kind + "-" + target
			start := r.site.url("/r/" + target + "/" + n)
			rule := Rule{Kind: kind, URL: start}
			switch kind {
			case "path-prefix":
				rule.URL = r.site.url("/r")
			case "origin":
				rule.URL = r.site.main.URL
			}
			if !ValidRules([]Rule{rule}, b.origins) {
				t.Fatalf("rule %v", rule)
			}
			a, _ := blGrant(t, b, 60, []Rule{rule})
			ph := r.site.protectedHits()
			t0 := time.Now()
			g := r.do(b, a, "goto", blGoto(start))
			nav := r.proxy.find(t0, true, "Page.navigate", nil)
			id := blFrameID(b, a)
			s := r.do(b, a, "snapshot", "{}")
			blLine(t, "R12-redirect-create-broker", "kind", kind, "target", target, "dispatched", map[bool]string{true: "Page.navigate-requested-url", false: "none"}[!nav.at.IsZero()],
				"goto_err", blErr(g.err), "goto_ms", blSince(g.start, g.end), "goto_result_keys", g.keys(), "site_hops", r.site.hops(n), "protected_hits", r.site.protectedHits()-ph,
				"tab_final", r.tabURL(a.tab.target), "frame_id_present", map[bool]string{true: "yes", false: "no"}[id != ""], "snapshot_err", blErr(s.err), "snapshot_text", s.text(), "snapshot_result_keys", s.keys())
			_ = b.Revoke(a.ID)
		}
	}
}

func TestLeaseBaselineR12RedirectAssign(t *testing.T) {
	r := blSetup(t)
	for _, target := range []string{"same", "other", "protected", "error"} {
		b := r.broker()
		n := "assign-" + target
		a, _ := blGrant(t, b, 60, r.origin())
		if f := r.do(b, a, "goto", blGoto(r.site.url("/fast?n="+n))); f.err != nil {
			t.Fatalf("setup goto: %v", f.err)
		}
		ph := r.site.protectedHits()
		t0 := time.Now()
		g := r.do(b, a, "goto", blGoto(r.site.url("/r/"+target+"/"+n)))
		ev := r.proxy.find(t0, true, "Runtime.evaluate", func(m blMsg) bool { return m.unique })
		s := r.do(b, a, "snapshot", "{}")
		blLine(t, "R12-redirect-assign-broker", "kind", "origin", "target", target, "dispatched", map[bool]string{true: "Runtime.evaluate-location.assign", false: "none"}[!ev.at.IsZero()],
			"goto_err", blErr(g.err), "goto_ms", blSince(g.start, g.end), "site_hops", r.site.hops(n), "protected_hits", r.site.protectedHits()-ph,
			"tab_final", r.tabURL(a.tab.target), "snapshot_err", blErr(s.err), "snapshot_text", s.text(), "snapshot_result_keys", s.keys())
		_ = b.Revoke(a.ID)
	}
}

func TestLeaseBaselineR12PageChange(t *testing.T) {
	r := blSetup(t)
	for _, kind := range []string{"exact", "origin"} {
		for _, page := range []string{"assign", "meta", "push", "write", "frames"} {
			b := r.broker()
			n := kind + "-" + page
			start := r.site.url("/page/" + page + "?n=" + n)
			rule := Rule{Kind: "origin", URL: r.site.main.URL}
			if kind == "exact" {
				rule = Rule{Kind: "exact", URL: start}
			}
			a, _ := blGrant(t, b, 60, []Rule{rule})
			ph := r.site.protectedHits()
			g := r.do(b, a, "goto", blGoto(start))
			gen0 := blGen(b, a)
			committed := r.tabURL(a.tab.target)
			loader0 := blLoaderID(b, a)
			time.Sleep(1800 * time.Millisecond)
			gen1 := blGen(b, a)
			tabSnap := r.tabURL(a.tab.target)
			loader1 := blLoaderID(b, a)
			s := r.do(b, a, "snapshot", "{}")
			loaderChanged := "unknown"
			if loader0 != "" && loader1 != "" {
				loaderChanged = map[bool]string{true: "yes", false: "no"}[loader0 != loader1]
			}
			blLine(t, "R12-page-change-broker", "kind", kind, "change", page, "goto_err", blErr(g.err), "committed", committed, "tab_at_snapshot", tabSnap,
				"document_url_equal_goto_committed", map[bool]string{true: "yes", false: "no"}[tabSnap == committed],
				"loader_id_changed", loaderChanged,
				"broker_generation_changed", gen1 != gen0, "snapshot_err", blErr(s.err), "snapshot_text", s.text(), "snapshot_result_keys", s.keys(),
				"site_hops", r.site.hops(n), "protected_hits", r.site.protectedHits()-ph)
			_ = b.Revoke(a.ID)
		}
	}
}

func TestLeaseBaselineR12SnapshotIdentity(t *testing.T) {
	r := blSetup(t)
	b := r.broker()
	a, _ := blGrant(t, b, 60, r.origin())
	start := r.site.url("/final/ok?n=identity")
	g := r.do(b, a, "goto", blGoto(start))
	committed := r.tabURL(a.tab.target)
	loader := blLoaderID(b, a)
	frameID := blFrameID(b, a)
	s := r.do(b, a, "snapshot", "{}")
	after := r.tabURL(a.tab.target)
	blLine(t, "R12-snapshot-identity-broker", "goto_err", blErr(g.err), "goto_committed_url", committed, "tab_url_at_snapshot", after,
		"urls_equal", map[bool]string{true: "yes", false: "no"}[committed == after && committed != "none"],
		"loader_id_present", map[bool]string{true: "yes", false: "no"}[loader != ""], "frame_id_present", map[bool]string{true: "yes", false: "no"}[frameID != ""],
		"snapshot_err", blErr(s.err), "snapshot_result_keys", s.keys(), "snapshot_has_url_key", strings.Contains(s.keys(), "url"),
		"snapshot_has_loader_key", strings.Contains(s.keys(), "loader"), "snapshot_has_frame_key", strings.Contains(s.keys(), "frame"),
		"snapshot_text", s.text())
	_ = b.Revoke(a.ID)
}

func TestLeaseBaselineR12CaptureRace(t *testing.T) {
	r := blSetup(t)
	for _, race := range []string{"nav", "push", "text"} {
		b := r.broker()
		n := "race-" + race
		a, _ := blGrant(t, b, 60, r.origin())
		start := r.site.url("/race/" + race + "?n=" + n)
		if race == "nav" {
			start += "&k=0"
		}
		g := r.do(b, a, "goto", blGoto(start))
		delivered, refused, staleAtSend := 0, 0, 0
		errs := map[string]int{}
		texts := map[string]bool{}
		for i := 0; i < 25; i++ {
			s := r.do(b, a, "snapshot", "{}")
			if len(s.sends) == 0 {
				refused++
				errs[blErr(s.err)]++
				continue
			}
			delivered++
			txt := s.text()
			texts[txt] = true
			if race == "nav" {
				k, _ := strconv.Atoi(strings.TrimPrefix(txt, "Race_doc_"))
				if r.site.count("race-doc", n, s.sends[0]) > k+1 {
					staleAtSend++
				}
			}
		}
		var ek []string
		for k, v := range errs {
			ek = append(ek, fmt.Sprintf("%s:%d", k, v))
		}
		sort.Strings(ek)
		blLine(t, "R12-capture-race-broker", "change", race, "goto_err", blErr(g.err), "snapshots", 25, "delivered", delivered, "refused", refused,
			"refusals", "["+strings.Join(ek, ",")+"]", "distinct_texts", len(texts), "delivered_while_next_document_requested", staleAtSend)
		_ = b.Revoke(a.ID)
	}
}

func TestLeaseBaselineR12ScopeMatch(t *testing.T) {
	r := blSetup(t)
	o := r.broker().origins
	host := r.site.main.URL
	rules := []Rule{{"exact", host + "/a/b?x=1"}, {"path-prefix", host + "/a"}, {"origin", host}}
	cands := []string{host + "/a/b?x=1", host + "/a/b?x=2", host + "/a/b", host + "/a/b?x=1#f", host + "/a", host + "/a/", host + "/ab", host + "/a/b/../b?x=1", host + "/a%2fb?x=1", strings.ToUpper(host[:8]) + host[8:] + "/a/b?x=1"}
	for _, rule := range rules {
		var m []string
		for i, c := range cands {
			m = append(m, fmt.Sprintf("c%d:%t", i, matches([]Rule{rule}, c, o)))
		}
		blLine(t, "R12-scope-match-broker", "kind", rule.Kind, "rule_valid", rule.Valid(o), "results", strings.Join(m, ","))
	}
	blLine(t, "R12-scope-match-candidates", "c0", "rule", "c1", "other-query", "c2", "no-query", "c3", "plus-fragment", "c4", "prefix-base", "c5", "prefix-slash", "c6", "sibling", "c7", "dot-segment", "c8", "encoded-slash", "c9", "upper-scheme")
}

func TestLeaseBaselineR12FramesGate(t *testing.T) {
	r := blSetup(t)
	fixtures := []struct {
		name, path string
		support    bool
		kinds      []string
		needChild  bool
		domProof   bool
	}{
		{"same", "/page/frame/same", false, []string{"exact", "origin"}, true, false},
		{"cross", "/page/frame/cross", false, []string{"exact", "origin"}, true, false},
		{"blank", "/page/frame/blank", false, []string{"exact", "origin"}, true, true},
		{"srcdoc", "/page/frame/srcdoc", false, []string{"exact", "origin"}, true, true},
		{"sandbox", "/page/frame/sandbox", false, []string{"exact", "origin"}, true, true},
		{"data", "/page/frame/data", false, []string{"exact", "origin"}, true, true},
		{"protected", "/page/frame/protected", false, []string{"exact", "origin"}, true, false},
		{"control-cdn-api", "/page/stable", false, []string{"exact"}, false, false},
		{"stable-no-support", "/page/stable", false, []string{"exact", "origin"}, false, false},
		{"stable-one-support", "/page/stable", true, []string{"exact", "origin"}, true, false},
		{"mixed", "/page/frames", false, []string{"exact", "origin"}, true, false},
	}
	for _, fx := range fixtures {
		for _, kind := range fx.kinds {
			b := r.broker()
			n := fx.name + "-" + kind
			q := "?n=" + n
			if fx.support {
				q = "?support=1&n=" + n
			}
			start := r.site.url(fx.path + q)
			rule := Rule{Kind: "origin", URL: r.site.main.URL}
			if kind == "exact" {
				rule = Rule{Kind: "exact", URL: start}
			}
			a, _ := blGrant(t, b, 60, []Rule{rule})
			ph0 := r.site.protectedHits()
			g := r.do(b, a, "goto", blGoto(start))
			time.Sleep(900 * time.Millisecond)
			hops := r.site.hops(n)
			childNet := strings.Contains(hops, "/child/") || strings.Contains(hops, "/support-frame/") || strings.Contains(hops, "/widget/") || strings.Contains(hops, "protected/child")
			dom := blFrameDOMProof(b, a)
			childOK := childNet || (fx.domProof && dom.frames >= 1 && dom.hasIframe)
			if fx.needChild && !childOK && fx.name != "protected" {
				t.Fatalf("%s/%s: child not present before snapshot hops=%q dom=%+v", fx.name, kind, hops, dom)
			}
			blGateSet("unset")
			blStampDiffSet("unset")
			s := r.do(b, a, "snapshot", "{}")
			txt := s.text()
			tab := r.tabURL(a.tab.target)
			stampDiff := blStampDiffGet()
			wantStamp := fx.name == "protected" || fx.name == "mixed" || fx.name == "same"
			if wantStamp && stampDiff == "unset" {
				t.Fatalf("%s/%s: missing stamp_diff", fx.name, kind)
			}
			gate := blGateGet()
			blLine(t, "R12-frames-gate-broker", "fixture", fx.name, "kind", kind, "support_frame", fx.support,
				"goto_err", blErr(g.err), "tab", tab,
				"document_url_stable", map[bool]string{true: "yes", false: "no"}[tab != "none" && !strings.Contains(tab, "/final/")],
				"snapshot_err", blErr(s.err), "text_delivered", map[bool]string{true: "yes", false: "no"}[s.err == nil && txt != "none"],
				"rendered_cdn", strings.Contains(txt, "cdn_ok"), "rendered_api", strings.Contains(txt, "api_ok"),
				"snapshot_text", txt, "failing_gate", gate,
				"child_requested", map[bool]string{true: "yes", false: "no"}[childOK],
				"child_dom_frames", dom.frames, "child_dom_has_iframe", dom.hasIframe,
				"child_dom_src", dom.src, "child_dom_content_doc", dom.contentDoc,
				"stamp_diff", stampDiff,
				"site_hops", hops, "protected_hits", r.site.protectedHits()-ph0)
			if fx.name == "protected" || fx.name == "mixed" || fx.name == "same" {
				blEmitStampDiag(t, b, a, fx.name, kind, gate, stampDiff)
			}
			_ = b.Revoke(a.ID)
		}
	}
}

func blEmitStampDiag(t *testing.T, b *Broker, a *active, fixture, kind, gate, stampDiff string) {
	t.Helper()
	step := blStampStepGet()
	raw := blStampRawFrameGet()
	firstLoader, firstFrame := "none", "none"
	if i := strings.Index(stampDiff, "loader="); i >= 0 {
		rest := stampDiff[i+len("loader="):]
		if j := strings.Index(rest, "|"); j >= 0 {
			firstLoader = rest[:j]
		}
	}
	if i := strings.Index(stampDiff, "frame="); i >= 0 {
		rest := stampDiff[i+len("frame="):]
		if j := strings.Index(rest, "|"); j >= 0 {
			firstFrame = rest[:j]
		}
	}
	pageLoader, pageFrame, pageErr := blPageFrameTree(b, a)
	blLine(t, "R12-stamp-diag", "fixture", fixture, "kind", kind, "failing_gate", gate,
		"second_stamp_step", step, "second_stamp_raw_frame", raw,
		"first_stamp_loader", firstLoader, "first_stamp_frame", firstFrame,
		"page_tree_loader", pageLoader, "page_tree_frame", pageFrame, "page_tree_err", pageErr,
		"first_vs_page_loader_differ", map[bool]string{true: "yes", false: "no"}[firstLoader != "none" && pageLoader != "none" && firstLoader != pageLoader],
		"first_vs_page_frame_differ", map[bool]string{true: "yes", false: "no"}[firstFrame != "none" && pageFrame != "none" && firstFrame != pageFrame],
		"observation", "open_not_a_gate")
}

func blPageFrameTree(b *Broker, a *active) (loader, frameID, errName string) {
	if a == nil || a.tab == nil || a.tab.session == "" || b.bs == nil || b.bs.c == nil {
		return "none", "none", "no-session"
	}
	var p struct {
		FrameTree struct {
			Frame struct {
				ID, LoaderID, ParentID, URL, UnreachableURL string
			}
		}
	}
	if e := b.bs.c.Call(context.Background(), a.tab.session, "Page.getFrameTree", nil, &p); e != nil {
		return "none", "none", blStampRedact(e.Error())
	}
	f := p.FrameTree.Frame
	if f.LoaderID == "" {
		loader = "empty"
	} else {
		loader = f.LoaderID
	}
	if f.ID == "" {
		frameID = "empty"
	} else {
		frameID = f.ID
	}
	return loader, frameID, "nil"
}

type blDOMProof struct {
	frames     int
	hasIframe  bool
	src        string
	contentDoc string
}

func blFrameDOMProof(b *Broker, a *active) blDOMProof {
	out := blDOMProof{src: "none", contentDoc: "none"}
	if a == nil || a.tab == nil || a.tab.session == "" || b.bs == nil || b.bs.c == nil {
		return out
	}
	var r struct {
		Result struct {
			Value struct {
				Frames     int
				HasIframe  bool
				Src        string
				ContentDoc string
			}
		}
	}
	expr := `(()=>{const f=document.querySelector('iframe');let cd='none';if(f){try{cd=f.contentDocument?'same-origin-doc':'null-doc'}catch(e){cd='opaque'}}return {frames:window.frames.length,hasIframe:!!f,src:(f&&(f.getAttribute('src')||f.src||''))||'',contentDoc:cd}})()`
	if b.bs.c.Call(context.Background(), a.tab.session, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "timeout": 2000}, &r) != nil {
		return out
	}
	out.frames = r.Result.Value.Frames
	out.hasIframe = r.Result.Value.HasIframe
	if r.Result.Value.Src != "" {
		out.src = blWord(r.Result.Value.Src, 48)
	}
	if r.Result.Value.ContentDoc != "" {
		out.contentDoc = r.Result.Value.ContentDoc
	}
	return out
}

func TestLeaseBaselineR12ContractFacts(t *testing.T) {
	r := blSetup(t)
	b := r.broker()
	o := b.origins
	kinds := []string{"exact", "path-prefix", "origin"}
	var kindOK []string
	for _, k := range kinds {
		rule := Rule{Kind: k, URL: r.site.main.URL}
		if k == "exact" {
			rule.URL = r.site.url("/final/ok")
		}
		if k == "path-prefix" {
			rule.URL = r.site.url("/final")
		}
		kindOK = append(kindOK, k+":"+strconv.FormatBool(rule.Valid(o)))
	}
	a, _ := blGrant(t, b, 60, r.origin())
	start := r.site.url("/final/ok?n=contract")
	g := r.do(b, a, "goto", blGoto(start))
	s := r.do(b, a, "snapshot", "{}")
	u := r.do(b, a, "unknown-op", "{}")
	c := r.do(b, a, "click", "{}")
	blLine(t, "R12-contract-ops-broker", "known_ops", "[goto,snapshot]", "unknown_op_err", blErr(u.err), "click_op_err", blErr(c.err),
		"rule_kinds_valid", strings.Join(kindOK, ","),
		"goto_err", blErr(g.err), "goto_result_keys", g.keys(), "snapshot_err", blErr(s.err), "snapshot_result_keys", s.keys(),
		"snapshot_text_present", map[bool]string{true: "yes", false: "no"}[s.text() != "none" && s.err == nil])
	_ = b.Revoke(a.ID)
}

func blLoaderID(b *Broker, a *active) string {
	if a == nil || a.tab == nil || a.tab.session == "" || b.bs == nil || b.bs.c == nil {
		return ""
	}
	var p struct {
		FrameTree struct {
			Frame struct {
				LoaderID string
			}
		}
	}
	if b.bs.c.Call(context.Background(), a.tab.session, "Page.getFrameTree", nil, &p) != nil {
		return ""
	}
	return p.FrameTree.Frame.LoaderID
}

func blFrameID(b *Broker, a *active) string {
	if a == nil || a.tab == nil {
		return ""
	}
	return a.tab.target
}
