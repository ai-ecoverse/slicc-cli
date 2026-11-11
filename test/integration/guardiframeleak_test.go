package integration

import (
	"encoding/json"
	"testing"
)

func TestCDPTabIframeLeakIsCrossSite(t *testing.T) {
	for _, variant := range []string{"prompt", "lagging"} {
		t.Run(variant, func(t *testing.T) {
			sum := runGuard(t, guardRun{variant: variant, slots: 1, only: "C3.tab-iframe-leak", rowTimeout: guardRowTimeout})
			if len(sum.Results) == 0 {
				t.Fatal("C3.tab-iframe-leak produced no rows")
			}
			seen := map[string]bool{}
			for _, row := range sum.Results {
				logTabIframeLeakEvidence(t, row)
				if row.ID != "C3.tab-iframe-leak" {
					t.Fatalf("unexpected row: %+v", row)
				}
				seen[row.Target] = true
				if row.Control == "incomplete" || row.Guarded == "incomplete" || row.Live == "incomplete" {
					t.Fatalf("incomplete when the observer misses the framed target fails the run (never pass, never waive): control=%s guarded=%s live=%s why=%s errors=%v diagnostics=%s",
						row.Control, row.Guarded, row.Live, row.Why, row.Errors, row.Diagnostics)
				}
				if row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" || len(row.Errors) != 0 {
					t.Fatalf("C3.tab-iframe-leak must pass both legs without a waiver: control=%s guarded=%s live=%s why=%s errors=%v observed=%s diagnostics=%s",
						row.Control, row.Guarded, row.Live, row.Why, row.Errors, row.Observed, row.Diagnostics)
				}
				if tabIframeLeakObserverMissesFrame(row.Diagnostics) {
					t.Fatalf("%s %s: observer must see the framed target: diagnostics=%s", row.Target, row.Variant, row.Diagnostics)
				}
				if err := tabIframeLeakGuardedHidesFrame(row.Observed, row.Diagnostics); err != nil {
					t.Fatalf("%s %s: %v observed=%s diagnostics=%s", row.Target, row.Variant, err, row.Observed, row.Diagnostics)
				}
			}
			for _, target := range []string{"seven", "vault"} {
				if !seen[target] {
					t.Fatalf("missing %s row", target)
				}
			}
		})
	}
}

func TestTabIframeLeakFailsWhenGuardedLeaks(t *testing.T) {
	cleanObs := json.RawMessage(`{"control":{"admitted":true,"iframeExists":true,"iframeAttached":true},"guarded":{"admitted":true,"iframeExists":true,"iframeAttached":false}}`)
	cleanDiag := json.RawMessage(`{"iframeLeak":{"iframeAttached":false,"frameScrubbed":true,"attachRefused":true,"frames":[{"targetId":"F1"}],"attached":[],"listings":[{"targetId":"F1","url":"http://seven.oopif.test:1","title":"http://seven.oopif.test:1","origin":"http://seven.oopif.test:1"}],"sessions":[],"attachAttempts":[{"targetId":"F1","refused":true,"message":"slicc: refused: target is on seven's origin"}]}}`)
	absentListing := json.RawMessage(`{"iframeLeak":{"iframeAttached":false,"frameScrubbed":true,"attachRefused":true,"frames":[{"targetId":"F1"}],"attached":[],"listings":[],"sessions":[],"attachAttempts":[{"targetId":"F1","refused":true,"message":"slicc: refused: target is on seven's origin"}]}}`)
	if err := tabIframeLeakGuardedHidesFrame(cleanObs, cleanDiag); err != nil {
		t.Fatal(err)
	}
	if err := tabIframeLeakGuardedHidesFrame(cleanObs, absentListing); err != nil {
		t.Fatalf("absent listing must pass clause (b): %v", err)
	}
	cases := map[string]json.RawMessage{
		"attach-other-type": json.RawMessage(`{"iframeLeak":{"iframeAttached":true,"frameScrubbed":true,"attachRefused":true,"frames":[{"targetId":"F1"}],"attached":[{"targetId":"F1","type":"page","url":"http://seven.oopif.test:1"}],"listings":[{"targetId":"F1","url":"http://seven.oopif.test:1","title":"http://seven.oopif.test:1","origin":"http://seven.oopif.test:1"}],"sessions":[{"targetId":"F1","sessionId":"S1"}],"attachAttempts":[{"targetId":"F1","refused":true,"message":"slicc: refused: target is on seven's origin"}]}}`),
		"listing-path":      json.RawMessage(`{"iframeLeak":{"iframeAttached":false,"frameScrubbed":false,"attachRefused":true,"frames":[{"targetId":"F1"}],"attached":[],"listings":[{"targetId":"F1","url":"http://seven.oopif.test:1/doc?n=frame-x","title":"http://seven.oopif.test:1","origin":"http://seven.oopif.test:1"}],"sessions":[],"attachAttempts":[{"targetId":"F1","refused":true,"message":"slicc: refused: target is on seven's origin"}]}}`),
		"listing-title":     json.RawMessage(`{"iframeLeak":{"iframeAttached":false,"frameScrubbed":false,"attachRefused":true,"frames":[{"targetId":"F1"}],"attached":[],"listings":[{"targetId":"F1","url":"http://seven.oopif.test:1","title":"secret title","origin":"http://seven.oopif.test:1"}],"sessions":[],"attachAttempts":[{"targetId":"F1","refused":true,"message":"slicc: refused: target is on seven's origin"}]}}`),
		"attach-accepted":   json.RawMessage(`{"iframeLeak":{"iframeAttached":false,"frameScrubbed":true,"attachRefused":false,"frames":[{"targetId":"F1"}],"attached":[],"listings":[],"sessions":[],"attachAttempts":[{"targetId":"F1","refused":false,"message":"","sessionId":"S1"}]}}`),
		"observer-miss":     json.RawMessage(`{"iframeLeak":{"iframeAttached":false,"frameScrubbed":true,"attachRefused":false,"frames":[],"attached":[],"listings":[],"sessions":[],"attachAttempts":[]}}`),
	}
	for name, diag := range cases {
		if err := tabIframeLeakGuardedHidesFrame(cleanObs, diag); err == nil {
			t.Fatalf("%s leak/incomplete accepted: %s", name, diag)
		}
	}
	miss := guardRow{
		ID: "C3.tab-iframe-leak", Control: "incomplete", Guarded: "incomplete", Live: "incomplete",
		Why: json.RawMessage(`"incomplete: unguarded observer never saw the framed target"`),
	}
	if _, ok := waivedGuardRow(miss); ok {
		t.Fatal("observer-miss incomplete must never be waived", miss)
	}
	rec := &recordTB{TB: t}
	checkGuardSummary(rec, &guardSummary{
		Set:     guardNPMPackage + "@" + guardSetVersion(t),
		Pass:    false,
		Missing: json.RawMessage(`[]`),
		Results: []guardRow{miss},
	})
	if len(rec.errs) == 0 {
		t.Fatal("checkGuardSummary must fail the run when the observer miss is incomplete")
	}
}
