package integration

import (
	"encoding/json"
	"testing"
)

func TestCDPKnownProtectedRedirectOverridesRefusedAcrossFreshTargets(t *testing.T) {
	for _, variant := range []string{"prompt", "lagging"} {
		t.Run(variant, func(t *testing.T) {
			sum := runGuard(t, guardRun{variant: variant, slots: 1, only: "C2.redirect-body", rowTimeout: guardRowTimeout})
			if !sum.Pass || len(sum.Results) != 1 || string(sum.Missing) != "[]" || sum.ObserverSkipped != 0 {
				t.Fatalf("cross-target redirect override: counts=%s missing=%s rows=%d observerSkipped=%d", sum.Counts, sum.Missing, len(sum.Results), sum.ObserverSkipped)
			}
			for _, row := range sum.Results {
				if row.ID != "C2.redirect-body" || row.Target != "ordinary" || row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" || len(row.Errors) != 0 {
					t.Fatalf("known protected redirect override: %+v observations=%s", row, row.Observed)
				}
				var observed map[string]map[string]bool
				if err := json.Unmarshal(row.Observed, &observed); err != nil {
					t.Fatal(err)
				}
				for side, checks := range map[string]map[string]bool{"control": {"overridesWorked": true, "overridesRefused": false}, "guarded": {"overridesWorked": false, "overridesRefused": true}} {
					for key, want := range checks {
						got, present := observed[side][key]
						if !present || got != want {
							t.Errorf("%s %s: ordinary override control and cross-target refusal not proven: %s", side, key, row.Observed)
						}
					}
				}
				for _, side := range []string{"control", "guarded"} {
					for _, key := range []string{"live", "sessionSurvives", "rawRecorded", "requestsTerminated"} {
						if !observed[side][key] {
							t.Errorf("%s %s not proven: %s", side, key, row.Observed)
						}
					}
				}
				for _, key := range []string{"secretHidden", "protectedBodyHidden", "cookieHidden", "hopsRedacted", "networkBodyRefused"} {
					if !observed["guarded"][key] {
						t.Errorf("protected read evidence missing for %s: %s", key, row.Observed)
					}
				}
			}
		})
	}
}
