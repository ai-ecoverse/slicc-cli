package integration

import (
	"encoding/json"
	"testing"
)

func TestCDPPreAttachedInheritedFramesRemainProtected(t *testing.T) {
	for _, variant := range []string{"prompt", "lagging"} {
		for _, id := range []string{"C4.iframe-preattach.blank", "C4.iframe-preattach.srcdoc"} {
			t.Run(variant+"/"+id, func(t *testing.T) {
				sum := runGuard(t, guardRun{variant: variant, slots: 1, only: id, rowTimeout: guardRowTimeout})
				if !sum.Pass || len(sum.Results) != 1 || string(sum.Missing) != "[]" || sum.ObserverSkipped != 0 {
					t.Fatalf("inherited-frame coverage: counts=%s missing=%s rows=%d observerSkipped=%d", sum.Counts, sum.Missing, len(sum.Results), sum.ObserverSkipped)
				}
				for _, row := range sum.Results {
					if row.ID != id || row.Target != "seven" || row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" || len(row.Errors) != 0 {
						t.Fatalf("protected inherited frame: %+v observations=%s", row, row.Observed)
					}
					var observed map[string]map[string]bool
					if err := json.Unmarshal(row.Observed, &observed); err != nil {
						t.Fatal(err)
					}
					for _, key := range []string{"frameTreeShown", "axFullShown", "axPartialShown", "documentShown", "nodeShown", "snapshotShown"} {
						control, controlSeen := observed["control"][key]
						guarded, guardedSeen := observed["guarded"][key]
						if !controlSeen || !control || !guardedSeen || guarded {
							t.Errorf("%s: independent positive control and guarded refusal not proven: %s", key, row.Observed)
						}
					}
					if !observed["control"]["live"] || !observed["guarded"]["live"] {
						t.Errorf("ordinary top-level page did not remain live: %s", row.Observed)
					}
				}
			})
		}
	}
}
