package integration

import (
	"path/filepath"
	"testing"
)

func diagC10Only(t *testing.T, variant string) {
	t.Helper()
	for _, id := range []string{"C10.cookie-plant", "C10.cookie-plant.credential"} {
		sum := runGuard(t, guardRun{variant: variant, slots: 2, only: id, rowTimeout: guardRowTimeout})
		if path, err := writeGuardReport(filepath.Join("..", "..", "artifacts", "guard"), "c10only-"+variant+"-"+id, sum); err != nil {
			t.Logf("the guard report wasn't written: %v", err)
		} else {
			t.Logf("guard report: %s", path)
		}
		for _, r := range sum.Results {
			t.Logf("DIAG C10-only %s %s %s: control %s, guarded %s, live %s, why %s", variant, r.ID, r.Target, r.Control, r.Guarded, r.Live, r.Why)
		}
	}
}

func TestDiagC10OnlyPrompt(t *testing.T) { diagC10Only(t, "prompt") }

func TestDiagC10OnlyLagging(t *testing.T) { diagC10Only(t, "lagging") }
