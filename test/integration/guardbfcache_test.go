package integration

import (
	"fmt"
	"testing"
)

func TestCDPSharedBFCacheObserver(t *testing.T) {
	sharedBFCache(t, "C8.protected-bfcache-restore")
}

func TestCDPSharedBFCacheHistory(t *testing.T) {
	sharedBFCache(t, "C8.protected-bfcache-restore.history")
}

func sharedBFCache(t *testing.T, id string) {
	t.Helper()
	sum := runGuard(t, guardRun{variant: "lagging", slots: 1, only: id, rowTimeout: guardRowTimeout})
	if len(sum.Results) == 0 || string(sum.Missing) != "[]" {
		t.Fatalf("shared BFCache evidence unavailable: %s missing=%s", sum.Line, sum.Missing)
	}
	for _, row := range sum.Results {
		if row.ID != id || row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" {
			t.Errorf("%s %s: control=%s guarded=%s live=%s why=%s errors=%v observed=%s", row.ID, row.Target, row.Control, row.Guarded, row.Live, row.Why, row.Errors, row.Observed)
		}
	}
	if !sum.Pass {
		t.Errorf("shared BFCache set did not pass: %s", sum.Line)
	}
	fmt.Println(sum.Line)
}
