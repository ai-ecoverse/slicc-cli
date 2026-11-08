package integration

import (
	"strings"
	"testing"
	"time"
)

func TestCDPProtectNavigateProtectedReturnsThenErrorPage(t *testing.T) {
	const id = "E7.emulation-carry"
	sum := runGuard(t, guardRun{variant: "prompt", slots: 2, only: id, rowTimeout: 45 * time.Second})
	if len(sum.Results) == 0 {
		t.Fatalf("no rows for %s", id)
	}
	for _, r := range sum.Results {
		if r.ID != id {
			t.Errorf("unexpected row %s", r.ID)
			continue
		}
		if r.Control != "pass" || r.Guarded != "pass" || r.Live != "pass" {
			t.Errorf("%s %s: control %s, guarded %s, live %s\n  why %s\n  errors %v\n  observed %s", r.ID, r.Target, r.Control, r.Guarded, r.Live, r.Why, r.Errors, r.Observed)
		}
		obs := string(r.Observed)
		if strings.Contains(obs, `"loaded":true`) && strings.Contains(obs, `"guarded"`) {
			if strings.Contains(obs, `"guarded":{`) && strings.Contains(obs[strings.Index(obs, `"guarded":{`):], `"loaded":true`) {
				t.Errorf("%s %s: guarded loaded a protected document: %s", r.ID, r.Target, obs)
			}
		}
	}
}
