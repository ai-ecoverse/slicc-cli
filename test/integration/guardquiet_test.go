package integration

import (
	"path/filepath"
	"testing"
)

func diagQuietRows(t *testing.T, variant string) {
	t.Helper()
	ids := []string{"C3.popup-self-nav", "C3.popup-on-redirect-302", "C3.popup-on-redirect-chain", "C4.static-cross-site", "C4.script-cross-site", "C4.static-same-site", "C4.script-same-site", "C10.cookie-plant", "C10.cookie-plant.credential"}
	for _, id := range ids {
		sum := runGuard(t, guardRun{variant: variant, slots: 2, only: id, rowTimeout: guardRowTimeout})
		if path, err := writeGuardReport(filepath.Join("..", "..", "artifacts", "guard"), "quiet-"+variant+"-"+id, sum); err != nil {
			t.Logf("the guard report wasn't written: %v", err)
		} else {
			t.Logf("guard report: %s", path)
		}
		for _, r := range sum.Results {
			t.Logf("DIAG quiet %s %s %s: control %s, guarded %s, live %s, why %s", variant, r.ID, r.Target, r.Control, r.Guarded, r.Live, r.Why)
		}
	}
}

func TestDiagQuietRowsPrompt(t *testing.T) { diagQuietRows(t, "prompt") }

func TestDiagQuietRowsLagging(t *testing.T) { diagQuietRows(t, "lagging") }
