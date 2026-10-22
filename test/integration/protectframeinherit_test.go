package integration

import (
	"testing"
)

func TestCDPPreAttachedInheritedFramesRemainProtected(t *testing.T) {
	for _, variant := range []string{"prompt", "lagging"} {
		for _, id := range []string{"C4.iframe-preattach.blank", "C4.iframe-preattach.srcdoc"} {
			t.Run(variant+"/"+id, func(t *testing.T) {
				sum := runGuard(t, guardRun{variant: variant, slots: 1, only: id, rowTimeout: guardRowTimeout})
				if !sum.Pass || len(sum.Results) != 1 || string(sum.Missing) != "[]" {
					t.Fatalf("inherited-frame coverage: %s missing=%s rows=%d", sum.Line, sum.Missing, len(sum.Results))
				}
				for _, row := range sum.Results {
					if row.ID != id || row.Target != "seven" || row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" {
						t.Fatalf("protected inherited frame: %+v observations=%s", row, row.Observed)
					}
				}
			})
		}
	}
}
