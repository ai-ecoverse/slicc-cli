package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRedirectKnownsAreRecordedIncompletesNotProtectionVerdicts(t *testing.T) {
	data, err := os.ReadFile("testdata/redirect-incompletes.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []guardRow
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	count, total := 0, 0
	for id, known := range guardKnowns {
		if known.issue == historyQuarantineIssue {
			total++
			if strings.HasPrefix(id, "C2.") {
				count++
			}
		}
	}
	if len(rows) != 3 || count != 3 || total != 10 {
		t.Fatalf("%d rows, %d redirect knowns, %d total knowns", len(rows), count, total)
	}
	for _, row := range rows {
		known := guardKnowns[row.ID]
		if !known.matches(row) {
			t.Errorf("%s rejected its recorded incomplete", row.ID)
		}
		for _, mutate := range []func(*guardRow){
			func(r *guardRow) {
				if r.Control == "pass" {
					r.Control = "fail"
				} else {
					r.Control = "pass"
				}
			},
			func(r *guardRow) { r.Guarded = "pass" },
			func(r *guardRow) {
				if r.Live == "pass" {
					r.Live = "fail"
				} else {
					r.Live = "pass"
				}
			},
			func(r *guardRow) { r.Why = json.RawMessage(`"another failed check"`) },
			func(r *guardRow) { r.Errors = append(append([]string(nil), r.Errors...), "extra failure") },
			func(r *guardRow) {
				r.Observed = json.RawMessage(`{"control":{},"guarded":{"marker":"https://vault.sliccy.ai/private"}}`)
			},
			func(r *guardRow) { r.Observed = json.RawMessage(`{"control":{},"guarded":{"extra":true}}`) },
		} {
			changed := row
			mutate(&changed)
			if known.matches(changed) {
				t.Errorf("%s accepted a changed outcome: %+v", row.ID, changed)
			}
		}
		var observations map[string]map[string]bool
		if err := json.Unmarshal(row.Observed, &observations); err != nil {
			t.Fatal(err)
		}
		for field, value := range observations["guarded"] {
			observations["guarded"][field] = !value
		}
		changed := row
		changed.Observed, _ = json.Marshal(observations)
		if !known.matches(changed) {
			t.Errorf("%s pins unmeasured protection values", row.ID)
		}
	}
}
