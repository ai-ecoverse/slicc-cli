package integration

import (
	"os"
	"strconv"
	"testing"
)

func TestCDPC8CloseProtectedViewSourceAttachDiag(t *testing.T) {
	const id = "C8.close-protected.view-source"
	n := 50
	if raw := os.Getenv("SLICC_C8_VIEWSOURCE_DIAG_N"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			t.Fatalf("SLICC_C8_VIEWSOURCE_DIAG_N=%q", raw)
		}
		n = v
	}
	t.Setenv("SLICC_TEST_DUMP_ATTACH_STDERR", "1")
	t.Setenv("SLICC_DEBUG", "1")
	for i := 0; i < n; i++ {
		i := i
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			sum := runGuard(t, guardRun{
				variant:    "prompt",
				slots:      2,
				only:       id,
				rowTimeout: guardRowTimeout,
			})
			if len(sum.Results) == 0 {
				t.Fatalf("no rows for %s", id)
			}
			for _, r := range sum.Results {
				if r.ID != id {
					t.Errorf("unexpected row %s, want only %s", r.ID, id)
				}
				name := r.ID + " " + r.Target + " " + r.Variant
				t.Logf("DIAG %s trial=%d control=%s guarded=%s live=%s why=%q", name, i, r.Control, r.Guarded, r.Live, r.Why)
				for _, note := range r.Notes {
					t.Logf("DIAG %s note: %s", name, note)
				}
				if r.Observed != nil {
					t.Logf("DIAG %s observed=%s", name, r.Observed)
				}
				if r.Control != "pass" || r.Guarded != "pass" || r.Live != "pass" {
					t.Errorf("%s trial=%d: control %s, guarded %s, live %s (attach stderr always dumped via SLICC_TEST_DUMP_ATTACH_STDERR)", name, i, r.Control, r.Guarded, r.Live)
				}
			}
		})
	}
}
