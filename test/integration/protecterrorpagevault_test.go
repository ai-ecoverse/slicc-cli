package integration

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestCDPProtectedErrorPageVaultVectorsPrompt(t *testing.T) {
	checkVaultErrorPages(t, "prompt")
}

func TestCDPProtectedErrorPageVaultVectorsLagging(t *testing.T) {
	checkVaultErrorPages(t, "lagging")
}

func checkVaultErrorPages(t *testing.T, variant string) {
	t.Helper()
	sum := runGuard(t, guardRun{variant: variant, slots: guardSlots(), rowTimeout: guardRowTimeout, errorPageVault: true})
	ids := []string{"C8.protected-error-page", "C8.protected-error-page.query", "C8.protected-error-page.fragment", "C8.protected-document-restore", "C8.protected-error-page-sticky", "C8.protected-error-page-sticky.query", "C8.protected-error-page-sticky.fragment"}
	if len(sum.Results) != len(ids) || string(sum.Missing) != "[]" || sum.ObserverSkipped != 0 {
		t.Fatalf("vault error-page coverage: %+v", sum)
	}
	seen := map[string]bool{}
	for _, row := range sum.Results {
		if !slices.Contains(ids, row.ID) || seen[row.ID] || row.Target != "vault" || row.Variant != variant {
			t.Fatalf("unexpected vault error-page row: %+v", row)
		}
		seen[row.ID] = true
		if row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" || len(row.Errors) != 0 {
			t.Errorf("%s vault %s: control %s, guarded %s, live %s, why %s, errors %v, observed %s", row.ID, variant, row.Control, row.Guarded, row.Live, row.Why, row.Errors, row.Observed)
		}
		var observed struct {
			Guarded struct {
				EventsRedacted bool `json:"eventsRedacted"`
			} `json:"guarded"`
		}
		if err := json.Unmarshal(row.Observed, &observed); err != nil || !observed.Guarded.EventsRedacted {
			t.Errorf("%s vault %s discovery redaction: %s", row.ID, variant, row.Observed)
		}
		t.Logf("%s vault %s: control %s guarded %s live %s", row.ID, variant, row.Control, row.Guarded, row.Live)
	}
}
