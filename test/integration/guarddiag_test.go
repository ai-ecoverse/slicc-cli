package integration

import (
	"strings"
	"testing"
)

const diagViewSourceID = "C8.close-protected.view-source"

func diagViewSource(t *testing.T, run guardRun) {
	t.Helper()
	sum := runGuard(t, run)
	rows, failed := 0, 0
	for _, r := range sum.Results {
		if r.ID != diagViewSourceID && !strings.HasPrefix(r.ID, diagViewSourceID+".rep") {
			continue
		}
		rows++
		if r.Control == "pass" && r.Guarded == "pass" && r.Live == "pass" {
			continue
		}
		failed++
		t.Errorf("DIAG %s %s: control %s, guarded %s, live %s, why %s, errors %v, notes %v", r.ID, r.Target, r.Control, r.Guarded, r.Live, r.Why, r.Errors, r.Notes)
	}
	t.Logf("DIAG view-source %s slots=%d: %d rows, %d failed", run.variant, run.slots, rows, failed)
	if rows == 0 {
		t.Fatal("DIAG no view-source rows ran")
	}
	t.Error("DIAG forcing the guard stderr dump so the attach decision lines are visible")
}

func TestDiagViewSourceAlone(t *testing.T) {
	diagViewSource(t, guardRun{variant: "lagging", slots: 1, only: diagViewSourceID, repeatID: diagViewSourceID, repeatN: 40, rowTimeout: guardRowTimeout})
}

func TestDiagViewSourceUnderLoad(t *testing.T) {
	diagViewSource(t, guardRun{variant: "lagging", slots: guardSlots(), repeatID: diagViewSourceID, repeatN: 100, rowTimeout: guardRowTimeout})
}
