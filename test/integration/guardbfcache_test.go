package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCDPSharedBFCacheObserver(t *testing.T) {
	sharedBFCache(t, "C8.protected-bfcache-restore")
}

func TestCDPSharedBFCacheHistory(t *testing.T) {
	sharedBFCache(t, "C8.protected-bfcache-restore.history")
}

func c8ExemptNote(notes []string) bool {
	for _, n := range notes {
		if strings.Contains(n, "commandsUnforwarded: exempt:frameOwnershipProbe (") {
			return true
		}
	}
	return false
}

func TestCDPC8ProofExemptNoteLookup(t *testing.T) {
	present := []string{"guarded commandsUnforwarded: exempt:frameOwnershipProbe (2 forwards)"}
	if !c8ExemptNote(present) {
		t.Fatal("c8-proof must treat the runner's guarded-prefixed note as exemptNote=true")
	}
	if c8ExemptNote(nil) || c8ExemptNote([]string{"guarded other"}) {
		t.Fatal("c8-proof must treat a missing exempt note as exemptNote=false")
	}
	row := guardRow{
		ID:       "C8.protected-bfcache-restore",
		Target:   "seven",
		Notes:    present,
		Control:  "pass",
		Guarded:  "pass",
		Live:     "pass",
		Observed: json.RawMessage(`{"guarded":{"commandsUnforwarded":true,"probeRanInProtected":true,"touched":true}}`),
	}
	var obs map[string]map[string]any
	if err := json.Unmarshal(row.Observed, &obs); err != nil {
		t.Fatal(err)
	}
	guarded := obs["guarded"]
	exempt := c8ExemptNote(row.Notes)
	line := "c8-proof id=" + row.ID + " target=" + row.Target +
		" commandsUnforwarded=" + stringifyAny(guarded["commandsUnforwarded"]) +
		" probeRanInProtected=" + stringifyAny(guarded["probeRanInProtected"]) +
		" touched=" + stringifyAny(guarded["touched"]) +
		" exemptNote=" + map[bool]string{true: "true", false: "false"}[exempt]
	if !strings.Contains(line, "exemptNote=true") {
		t.Fatalf("c8-proof line must carry exemptNote=true for the real note string: %s", line)
	}
	row.Notes = nil
	if c8ExemptNote(row.Notes) {
		t.Fatal("absent note must yield exemptNote=false")
	}
}

func stringifyAny(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<err>"
	}
	return string(b)
}

func sharedBFCache(t *testing.T, id string) {
	t.Helper()
	sum := runGuard(t, guardRun{variant: "lagging", slots: 1, only: id, rowTimeout: guardRowTimeout})
	if len(sum.Results) == 0 || string(sum.Missing) != "[]" {
		t.Fatalf("shared BFCache evidence unavailable: missing=%s", sum.Missing)
	}
	for _, row := range sum.Results {
		if row.ID != id || row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" {
			t.Errorf("%s %s: control=%s guarded=%s live=%s why=%s errors=%v observed=%s%s", row.ID, row.Target, row.Control, row.Guarded, row.Live, row.Why, row.Errors, row.Observed, evaluateAnswersLine(row))
			continue
		}
		var obs map[string]map[string]any
		_ = json.Unmarshal(row.Observed, &obs)
		guarded := obs["guarded"]
		commands := guarded["commandsUnforwarded"]
		probeRan := guarded["probeRanInProtected"]
		touched := guarded["touched"]
		exempt := c8ExemptNote(row.Notes)
		t.Logf("c8-proof id=%s target=%s commandsUnforwarded=%v probeRanInProtected=%v touched=%v exemptNote=%t", row.ID, row.Target, commands, probeRan, touched, exempt)
	}
	if !sum.Pass {
		t.Errorf("shared BFCache set did not pass: counts=%s", sum.Counts)
	}
	t.Logf("%s: counts=%s", id, sum.Counts)
}
