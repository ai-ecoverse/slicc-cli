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
