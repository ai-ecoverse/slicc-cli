package integration

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type preexistingGuardRow struct {
	ID       string         `json:"id"`
	Target   string         `json:"target"`
	Variant  string         `json:"variant"`
	Issue    string         `json:"issue"`
	Why      string         `json:"why"`
	Observed map[string]any `json:"observations"`
}

func preexistingGuardRows(t *testing.T) []preexistingGuardRow {
	t.Helper()
	data, err := os.ReadFile("testdata/preexisting-knowns.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []preexistingGuardRow
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func preexistingGuardIssue(r guardRow) string {
	if r.Target == "seven" && (r.ID == "C4.iframe-preattach.blank" || r.ID == "C4.iframe-preattach.srcdoc") {
		return "#402 (security)"
	}
	if r.ID == "C2.redirect-body" && r.Target == "ordinary" {
		return "#404"
	}
	return ""
}

func exactPreexistingGuardRow(r guardRow, rows []preexistingGuardRow) bool {
	if r.Control != "pass" || r.Guarded != "fail" || r.Live != "pass" || r.Reason != "" || len(r.Errors) != 0 {
		return false
	}
	var why string
	var observed map[string]any
	if json.Unmarshal(r.Why, &why) != nil || json.Unmarshal(r.Observed, &observed) != nil {
		return false
	}
	sorted := func(why string) []string { parts := strings.Split(why, "; "); slices.Sort(parts); return parts }
	for _, known := range rows {
		if r.ID == known.ID && r.Target == known.Target && r.Variant == known.Variant && preexistingGuardIssue(r) == known.Issue {
			return reflect.DeepEqual(observed, known.Observed) && slices.Equal(sorted(why), sorted(known.Why))
		}
	}
	return false
}

func TestGuardPreexistingRowsRequireExactBaselineEvidence(t *testing.T) {
	rows := preexistingGuardRows(t)
	if len(rows) != 6 {
		t.Fatalf("%d fixture rows, want3 in each variant", len(rows))
	}
	for _, known := range rows {
		why, _ := json.Marshal(known.Why)
		observed, _ := json.Marshal(known.Observed)
		r := guardRow{ID: known.ID, Target: known.Target, Variant: known.Variant, Control: "pass", Guarded: "fail", Live: "pass", Why: why, Observed: observed}
		if !exactPreexistingGuardRow(r, rows) {
			t.Fatal(r.ID, r.Variant)
		}
		mutations := []func(*guardRow){
			func(r *guardRow) { r.Control = "fail" }, func(r *guardRow) { r.Guarded = "pass" }, func(r *guardRow) { r.Live = "incomplete" },
			func(r *guardRow) { r.Target = "vault" }, func(r *guardRow) { r.Variant = "other" }, func(r *guardRow) { r.Errors = []string{"new error"} },
			func(r *guardRow) { r.Why = json.RawMessage(`"new failure"`) }, func(r *guardRow) { r.Observed = json.RawMessage(`{}`) },
		}
		for _, mutate := range mutations {
			changed := r
			mutate(&changed)
			if exactPreexistingGuardRow(changed, rows) {
				t.Fatal("accepted changed baseline outcome", changed)
			}
		}
		for leg, raw := range known.Observed {
			values := raw.(map[string]any)
			for key, value := range values {
				boolean, ok := value.(bool)
				if !ok {
					t.Fatal("nonboolean baseline evidence", key)
				}
				copy := map[string]any{}
				_ = json.Unmarshal(observed, &copy)
				copy[leg].(map[string]any)[key] = !boolean
				changed := r
				changed.Observed, _ = json.Marshal(copy)
				if exactPreexistingGuardRow(changed, rows) {
					t.Fatal("accepted changed read/override evidence", leg, key)
				}
			}
		}
	}
}
