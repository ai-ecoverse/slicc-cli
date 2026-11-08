package integration

import (
	"encoding/json"
	"testing"
)

func TestCDPTabIframeLeakIsCrossSite(t *testing.T) {
	for _, variant := range []string{"prompt", "lagging"} {
		t.Run(variant, func(t *testing.T) {
			sum := runGuard(t, guardRun{variant: variant, slots: 1, only: "C3.tab-iframe-leak", rowTimeout: guardRowTimeout})
			if len(sum.Results) == 0 {
				t.Fatal("C3.tab-iframe-leak produced no rows")
			}
			for _, row := range sum.Results {
				logTabIframeLeakEvidence(t, row)
				if row.ID != "C3.tab-iframe-leak" {
					t.Fatalf("unexpected row: %+v", row)
				}
				if row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" || len(row.Errors) != 0 {
					t.Fatalf("C3.tab-iframe-leak must pass both legs without a waiver: control=%s guarded=%s live=%s why=%s errors=%v observed=%s",
						row.Control, row.Guarded, row.Live, row.Why, row.Errors, row.Observed)
				}
				var observed map[string]map[string]any
				if err := json.Unmarshal(row.Observed, &observed); err != nil {
					t.Fatal(err)
				}
				controlAttached, _ := observed["control"]["iframeAttached"].(bool)
				guardedAttached, _ := observed["guarded"]["iframeAttached"].(bool)
				if !controlAttached || guardedAttached {
					t.Fatalf("want control iframeAttached=true and guarded iframeAttached=false: %s", row.Observed)
				}
			}
		})
	}
}
