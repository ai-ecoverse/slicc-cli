package integration

import "testing"

func TestCDPKnownProtectedRedirectOverridesRefusedAcrossFreshTargets(t *testing.T) {
	for _, variant := range []string{"prompt", "lagging"} {
		t.Run(variant, func(t *testing.T) {
			sum := runGuard(t, guardRun{variant: variant, slots: 1, only: "C2.redirect-body", rowTimeout: guardRowTimeout})
			if !sum.Pass || len(sum.Results) != 1 || string(sum.Missing) != "[]" {
				t.Fatalf("cross-target redirect override: %s missing=%s rows=%d", sum.Line, sum.Missing, len(sum.Results))
			}
			for _, row := range sum.Results {
				if row.ID != "C2.redirect-body" || row.Target != "ordinary" || row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" {
					t.Fatalf("known protected redirect override: %+v observations=%s", row, row.Observed)
				}
			}
		})
	}
}
