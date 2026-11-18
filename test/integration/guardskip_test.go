package integration

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
)

const (
	guardSkipUnexplained   = ""
	guardSkipUnsupported   = "unsupported"
	guardSkipNotApplicable = "not-applicable"
)

func guardSkipClass(r guardRow) string {
	if r.Control != "skip" || r.Guarded != "skip" || r.Live != "skip" {
		return guardSkipUnexplained
	}
	if r.Reason == "not-applicable" || strings.HasPrefix(r.Reason, "not-applicable: ") {
		return guardSkipNotApplicable
	}
	switch r.Reason {
	case "not-supported:siteIsolationOff":
		if r.ID == "C4.iframe-preattach.blank.shared" || r.ID == "C4.iframe-preattach.srcdoc.shared" {
			return guardSkipUnsupported
		}
	case "not-supported:syntheticAutoAttach":
		if strings.HasSuffix(r.ID, ".synthetic") {
			return guardSkipUnsupported
		}
	}
	return guardSkipUnexplained
}

func documentedGuardSkip(r guardRow) bool {
	return guardSkipClass(r) != guardSkipUnexplained
}

type guardTally struct {
	passed      map[string]int
	unsupported map[string]int
	waivedFail  map[string]bool
}

func tallyGuardRows(results []guardRow) guardTally {
	g := guardTally{passed: map[string]int{}, unsupported: map[string]int{}, waivedFail: map[string]bool{}}
	for _, r := range results {
		switch {
		case r.Control == "pass" && r.Guarded == "pass" && r.Live == "pass":
			g.passed[r.ID]++
		case r.Control == "skip" && guardSkipClass(r) == guardSkipUnsupported:
			g.unsupported[r.ID]++
		default:
			if _, ok := waivedGuardRow(r); ok {
				g.waivedFail[r.ID] = true
			}
		}
	}
	return g
}

func (g guardTally) proven(id string) bool {
	return g.passed[id] > 0 || g.unsupported[id] > 0 || g.waivedFail[id]
}

func (g guardTally) any() bool {
	return len(g.passed) > 0 || len(g.unsupported) > 0 || len(g.waivedFail) > 0
}

func TestGuardSkipsRequireExplicitConsistentReasons(t *testing.T) {
	base := guardRow{ID: "C4.iframe-preattach.blank.shared", Control: "skip", Guarded: "skip", Live: "skip", Reason: "not-supported:siteIsolationOff"}
	if !documentedGuardSkip(base) {
		t.Fatal(base)
	}
	for _, reason := range []string{"", "not-applicable-fake", "not-supported:unknown", "unknown"} {
		r := base
		r.Reason = reason
		if documentedGuardSkip(r) {
			t.Fatal("accepted unexplained skip", r)
		}
	}
	for _, status := range []string{"pass", "fail", "incomplete", "not-run"} {
		for _, field := range []string{"control", "guarded", "live"} {
			r := base
			switch field {
			case "control":
				r.Control = status
			case "guarded":
				r.Guarded = status
			case "live":
				r.Live = status
			}
			if documentedGuardSkip(r) {
				t.Fatal("accepted inconsistent skip", r)
			}
		}
	}
	r := base
	r.ID = "C1.href"
	if documentedGuardSkip(r) {
		t.Fatal("accepted unapproved unsupported vector", r)
	}
	r.Reason = "not-applicable"
	if !documentedGuardSkip(r) {
		t.Fatal(r)
	}
}

var signInHoldRowIDs = []string{
	"C7.sign-in-hold.held-popup",
	"C7.sign-in-hold.callback",
	"C7.sign-in-hold.callback-encoded",
	"C7.sign-in-hold.callback-origin",
	"C7.sign-in-hold.ordinary-popup",
	"C7.sign-in-hold.ordinary-authorize",
}

func skippedGuardRow(id, reason string) guardRow {
	return guardRow{ID: id, Target: "seven", Variant: "prompt", Control: "skip", Guarded: "skip", Live: "skip", Reason: reason}
}

func TestGuardSkipSignInHoldOnlyOnTheSignInHoldRows(t *testing.T) {
	var set struct {
		Vectors []struct {
			ID       string   `json:"id"`
			Kind     string   `json:"kind"`
			Requires []string `json:"requires"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(sliccbrowsers.GuardVectorsJSON(), &set); err != nil {
		t.Fatal(err)
	}
	var gated []string
	for _, v := range set.Vectors {
		if v.Kind == "cdp" && slices.Contains(v.Requires, "signInHold") {
			gated = append(gated, v.ID)
		}
	}
	if want := slices.Sorted(slices.Values(signInHoldRowIDs)); !slices.Equal(slices.Sorted(slices.Values(gated)), want) {
		t.Errorf("the set gates %v on signInHold, the test knows %v", gated, want)
	}
	for _, id := range signInHoldRowIDs {
		r := skippedGuardRow(id, "not-supported:signInHold")
		if got := guardSkipClass(r); got != guardSkipUnsupported {
			t.Errorf("%s skipped not-supported:signInHold: class %q, want %q", id, got, guardSkipUnsupported)
		}
		if g := tallyGuardRows([]guardRow{r, skippedGuardRow(id, "not-supported:signInHold")}); !g.proven(id) || !g.any() {
			t.Errorf("%s: all rows skipped not-supported:signInHold, yet the vector counts as unproven: %+v", id, g)
		}
		for _, reason := range []string{"", "skip", "not-applicable", "not-applicable: elsewhere", "not-supported:browserSession", "not-supported:siteIsolationOff", "not-supported:syntheticAutoAttach", "not-supported:iframeAutoAttach", "not-supported:signinhold", "not-supported:signInHold ", "not-supported:unguarded-observer"} {
			r := skippedGuardRow(id, reason)
			if got := guardSkipClass(r); got != guardSkipUnexplained {
				t.Errorf("%s skipped %q: class %q, want unexplained", id, reason, got)
			}
			if g := tallyGuardRows([]guardRow{r}); g.proven(id) {
				t.Errorf("%s skipped %q counts as proven", id, reason)
			}
		}
		for _, field := range []string{"control", "guarded", "live"} {
			r := skippedGuardRow(id, "not-supported:signInHold")
			switch field {
			case "control":
				r.Control = "pass"
			case "guarded":
				r.Guarded = "fail"
			case "live":
				r.Live = "incomplete"
			}
			if got := guardSkipClass(r); got != guardSkipUnexplained {
				t.Errorf("%s with %s not skipped: class %q, want unexplained", id, field, got)
			}
		}
	}
	for _, id := range []string{"C3.protected-parent-iframes.page", "C3.protected-parent-srcdoc.page", "C3.ordinary-parent-iframes.page", "C3.tab-attach", "C1.href", "C7.sign-in-hold", "C7.sign-in-hold.other", "C7.sign-in-hold.held-popup.synthetic", "c7.sign-in-hold.held-popup", "C4.iframe-preattach.blank.shared"} {
		r := skippedGuardRow(id, "not-supported:signInHold")
		if got := guardSkipClass(r); got != guardSkipUnexplained {
			t.Errorf("%s skipped not-supported:signInHold: class %q, want unexplained", id, got)
		}
		if g := tallyGuardRows([]guardRow{r}); g.proven(id) || g.any() {
			t.Errorf("%s skipped not-supported:signInHold counts as proven: %+v", id, g)
		}
	}
	for _, c := range []struct {
		id, reason, want string
	}{
		{"C4.iframe-preattach.blank.shared", "not-supported:siteIsolationOff", guardSkipUnsupported},
		{"C4.iframe-preattach.srcdoc.shared", "not-supported:siteIsolationOff", guardSkipUnsupported},
		{"C4.iframe-preattach.blank.isolated", "not-supported:siteIsolationOff", guardSkipUnexplained},
		{"C3.tab-attach.synthetic", "not-supported:syntheticAutoAttach", guardSkipUnsupported},
		{"C3.tab-attach", "not-supported:syntheticAutoAttach", guardSkipUnexplained},
		{"C3.protected-parent-iframes.page", "not-supported:iframeAutoAttach", guardSkipUnexplained},
		{"C3.tab-attach", "not-applicable", guardSkipNotApplicable},
		{"C9.page-drivable", guardC9MacReason, guardSkipNotApplicable},
	} {
		r := skippedGuardRow(c.id, c.reason)
		if got := guardSkipClass(r); got != c.want {
			t.Errorf("%s skipped %q: class %q, want %q", c.id, c.reason, got, c.want)
		}
		if g := tallyGuardRows([]guardRow{r}); g.proven(c.id) != (c.want == guardSkipUnsupported) {
			t.Errorf("%s skipped %q: proven %v", c.id, c.reason, g.proven(c.id))
		}
	}
	mixed := []guardRow{skippedGuardRow("C7.sign-in-hold.callback", "not-supported:signInHold"), skippedGuardRow("C3.protected-parent-iframes.page", "not-supported:signInHold"), {ID: "C1.href", Control: "pass", Guarded: "pass", Live: "pass"}}
	g := tallyGuardRows(mixed)
	if !g.proven("C7.sign-in-hold.callback") || g.proven("C3.protected-parent-iframes.page") || !g.proven("C1.href") {
		t.Errorf("mixed run tally %+v", g)
	}
}
