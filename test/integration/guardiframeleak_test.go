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
				if row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" || len(row.Errors) != 0 {
					t.Fatalf("C3.tab-iframe-leak must pass both legs without a waiver: control=%s guarded=%s live=%s why=%s errors=%v observed=%s",
						row.Control, row.Guarded, row.Live, row.Why, row.Errors, row.Observed)
				}
				if err := tabIframeLeakGuardedHidesFrame(row.Observed); err != nil {
					t.Fatalf("%s %s: %v observed=%s", row.Target, row.Variant, err, row.Observed)
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
	clean := json.RawMessage(`{"control":{"admitted":true,"iframeExists":true,"iframeAttached":true,"frameListed":true},"guarded":{"admitted":true,"iframeExists":true,"iframeAttached":false,"frameListed":false}}`)
	if err := tabIframeLeakGuardedHidesFrame(clean); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"attach", "list", "both"} {
		leaked := map[string]map[string]any{
			"control": {"admitted": true, "iframeExists": true, "iframeAttached": true, "frameListed": true},
			"guarded": {"admitted": true, "iframeExists": true, "iframeAttached": false, "frameListed": false},
		}
		switch name {
		case "attach":
			leaked["guarded"]["iframeAttached"] = true
		case "list":
			leaked["guarded"]["frameListed"] = true
		case "both":
			leaked["guarded"]["iframeAttached"] = true
			leaked["guarded"]["frameListed"] = true
		}
		raw, err := json.Marshal(leaked)
		if err != nil {
			t.Fatal(err)
		}
		if err := tabIframeLeakGuardedHidesFrame(raw); err == nil {
			t.Fatalf("%s leak accepted: %s", name, raw)
		}
	}
}
