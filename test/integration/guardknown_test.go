package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestHistoryKnownsMatchOnlyRecordedIncompleteRows(t *testing.T) {
	data, err := os.ReadFile("testdata/history-knowns.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []guardRow
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, known := range guardKnowns {
		if known.issue == historyQuarantineIssue {
			count++
		}
	}
	if len(rows) != 3 || count != 7 {
		t.Fatalf("%d recorded shapes, %d named knowns", len(rows), count)
	}
	for _, row := range rows {
		ids := []string{row.ID}
		if row.ID != "C8.protected-document-restore" {
			ids = append(ids, row.ID+".query", row.ID+".fragment")
		}
		for _, id := range ids {
			known := guardKnowns[id]
			if !known.matches(row) {
				t.Errorf("%s rejects its recorded incomplete outcome", id)
			}
			for _, mutate := range []func(*guardRow){
				func(r *guardRow) { r.Control = "pass" },
				func(r *guardRow) { r.Guarded = "pass" },
				func(r *guardRow) { r.Live = "pass" },
				func(r *guardRow) { r.Why = json.RawMessage(`"not live"`) },
				func(r *guardRow) { r.Errors = []string{"some other failure"} },
				func(r *guardRow) { r.Errors = append(append([]string(nil), r.Errors...), "extra failure") },
				func(r *guardRow) {
					r.Observed = json.RawMessage(strings.Replace(string(r.Observed), `"live": true`, `"live": false`, 1))
				},
				func(r *guardRow) {
					r.Observed = json.RawMessage(strings.Replace(string(r.Observed), `"fullURL": false`, `"fullURL": true`, 1))
				},
				func(r *guardRow) {
					r.Observed = json.RawMessage(strings.Replace(string(r.Observed), `"fullURL": false`, `"fullURL": "https://vault.sliccy.ai/private"`, 1))
				},
				func(r *guardRow) {
					r.Observed = json.RawMessage(strings.Replace(string(r.Observed), `"fullURL": false`, `"marker": false, "fullURL": false`, 1))
				},
			} {
				changed := row
				mutate(&changed)
				if known.matches(changed) {
					t.Errorf("%s accepted a changed outcome: %+v", id, changed)
				}
			}
		}
	}
}
