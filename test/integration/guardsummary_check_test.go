package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCDPGuardCheckFailsOnForgedRows(t *testing.T) {
	forged := `[{"id":"C10.cookie-plant","target":"seven","variant":"prompt","transport":"cdp","why":"not-supported:legacyRawRefused while the observation should run"}]`
	for _, pass := range []bool{false, true} {
		rec := &recordTB{TB: t}
		checkGuardSummary(rec, &guardSummary{
			Set:     guardNPMPackage + "@" + guardSetVersion(t),
			Pass:    pass,
			Missing: json.RawMessage(`[]`),
			Forged:  json.RawMessage(forged),
			NotRun:  guardNotRunExpectedIDs(),
			Results: []guardRow{{ID: "C10.cookie-plant", Target: "seven", Variant: "prompt", Control: "pass", Guarded: "pass", Live: "pass"}},
		})
		if !strings.Contains(strings.Join(rec.errs, "\n"), "forged 1: C10.cookie-plant seven prompt cdp") {
			t.Errorf("pass=%v: checkGuardSummary must fail on the forged row, got %q", pass, rec.errs)
		}
	}
}
