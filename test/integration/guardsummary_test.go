package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type guardRow struct {
	ID          string          `json:"id"`
	Target      string          `json:"target"`
	Variant     string          `json:"variant"`
	Transport   string          `json:"transport"`
	Via         string          `json:"via"`
	Control     string          `json:"control"`
	Guarded     string          `json:"guarded"`
	Live        string          `json:"live"`
	Reason      string          `json:"reason"`
	Why         json.RawMessage `json:"why"`
	Notes       []string        `json:"notes"`
	Errors      []string        `json:"errors"`
	Observed    json.RawMessage `json:"observations"`
	Diagnostics json.RawMessage `json:"diagnostics,omitempty"`
}

type guardSummary struct {
	Set             string          `json:"set"`
	Pass            bool            `json:"pass"`
	Results         []guardRow      `json:"results"`
	Missing         json.RawMessage `json:"missing"`
	Forged          json.RawMessage `json:"forged"`
	Line            string          `json:"line"`
	NotRun          []string        `json:"notRun"`
	ObserverSkipped int             `json:"observerSkipped"`
	Counts          json.RawMessage `json:"counts"`
}

const guardNotRunReason = "the route does not run it"

type guardNotRunExpect struct {
	ID     string
	Reason string
}

var guardNotRunExpected = []guardNotRunExpect{
	{ID: "E1", Reason: guardNotRunReason},
	{ID: "E2", Reason: guardNotRunReason},
	{ID: "E3", Reason: guardNotRunReason},
	{ID: "E5", Reason: guardNotRunReason},
	{ID: "B1", Reason: guardNotRunReason},
	{ID: "B2", Reason: guardNotRunReason},
	{ID: "B3", Reason: guardNotRunReason},
	{ID: "B4", Reason: guardNotRunReason},
	{ID: "B5", Reason: guardNotRunReason},
	{ID: "B6", Reason: guardNotRunReason},
	{ID: "B7", Reason: guardNotRunReason},
}

func guardNotRunExpectedIDs() []string {
	out := make([]string, len(guardNotRunExpected))
	for i, e := range guardNotRunExpected {
		out[i] = e.ID
	}
	return out
}

func guardNotRunDiff(got, want []string) (unexpected, vanished []string) {
	wantSet := map[string]bool{}
	gotSet := map[string]bool{}
	for _, id := range want {
		wantSet[id] = true
	}
	for _, id := range got {
		gotSet[id] = true
		if !wantSet[id] {
			unexpected = append(unexpected, id)
		}
	}
	for _, id := range want {
		if !gotSet[id] {
			vanished = append(vanished, id)
		}
	}
	slices.Sort(unexpected)
	slices.Sort(vanished)
	return unexpected, vanished
}

func guardRowStatuses(r guardRow) []string {
	return []string{r.Control, r.Guarded, r.Live}
}

type guardForgery struct {
	ID        string `json:"id"`
	Target    string `json:"target"`
	Variant   string `json:"variant"`
	Transport string `json:"transport"`
	Why       string `json:"why"`
}

func guardForgedNone(sum *guardSummary) (ok bool, detail string) {
	var rows []guardForgery
	if len(sum.Forged) == 0 || json.Unmarshal(sum.Forged, &rows) != nil || rows == nil {
		return false, fmt.Sprintf("; the runner's forged list is missing or unreadable: %q", sum.Forged)
	}
	if len(rows) == 0 {
		return true, ""
	}
	named := make([]string, len(rows))
	for i, r := range rows {
		named[i] = fmt.Sprintf("%s %s %s %s: %s", r.ID, r.Target, r.Variant, r.Transport, r.Why)
	}
	return false, fmt.Sprintf("; forged %d: %s", len(rows), strings.Join(named, " | "))
}

func guardRowLegsBad(r guardRow) []string {
	var bad []string
	for i, status := range guardRowStatuses(r) {
		switch status {
		case "pass", "skip", "not-run":
		default:
			bad = append(bad, fmt.Sprintf("%s %s=%s", r.ID, []string{"control", "guarded", "live"}[i], status))
		}
	}
	if len(bad) == 0 && (r.Control != r.Guarded || r.Control != r.Live) {
		bad = append(bad, fmt.Sprintf("%s %s legs %s/%s/%s", r.ID, r.Target, r.Control, r.Guarded, r.Live))
	}
	return bad
}

func guardSetPassDespiteNotRun(sum *guardSummary) (ok bool, detail string) {
	if sum == nil {
		return false, " missing summary"
	}
	if guardNotRunRefusesForged() {
		if ok, detail := guardForgedNone(sum); !ok {
			return false, detail
		}
	}
	if guardNotRunRefusesUnfinished() {
		switch {
		case len(sum.Results) == 0:
			return false, "; the run has no rows"
		case string(sum.Missing) != "[]":
			return false, fmt.Sprintf("; rows missing from the run: %s", sum.Missing)
		case sum.ObserverSkipped != 0:
			return false, fmt.Sprintf("; %d rows skipped for want of an unguarded observer", sum.ObserverSkipped)
		}
	}
	var bad []string
	if guardNotRunRefusesBadLegs() {
		for _, r := range sum.Results {
			bad = append(bad, guardRowLegsBad(r)...)
		}
	}
	if len(bad) > 0 {
		slices.Sort(bad)
		bad = slices.Compact(bad)
		return false, fmt.Sprintf("; rows not only pass/skip/not-run on all three legs: %s", strings.Join(bad, ", "))
	}
	got := append([]string{}, sum.NotRun...)
	slices.Sort(got)
	got = slices.Compact(got)
	want := guardNotRunExpectedIDs()
	unexpected, vanished := guardNotRunDiff(got, want)
	if !guardNotRunRefusesUnexpected() {
		unexpected = nil
	}
	if !guardNotRunRefusesVanished() {
		vanished = nil
	}
	if len(unexpected) > 0 || len(vanished) > 0 {
		return false, fmt.Sprintf("; not-run unexpected %v vanished %v (got %v want %v)", unexpected, vanished, got, want)
	}
	return true, fmt.Sprintf("; tolerated not-run %v (%s)", want, guardNotRunReason)
}

func writeGuardReport(dir, variant string, sum *guardSummary) (string, error) {
	b, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "guard-report-"+variant+".json")
	return path, os.WriteFile(path, append(b, '\n'), 0o644)
}

const guardLegacyInterceptionUndeclared = "legacyInterception is declared false: the deprecated Network.setRequestInterception and Network.continueInterceptedRequest are not a supported interception path on this route; intercept requests through Fetch instead (the extension declares the same after slicc-extension#424). The C10 legacy observations stay unmeasured here until slicc-cli#869"

func guardCapabilities() map[string]bool {
	return map[string]bool{"autoAttach": true, "iframeAutoAttach": true, "errorFrameClass": true, "browserSession": true, "browserContexts": true, "chromeUrlsAttach": true, "clientExit": true, "discovery": true, "extensionWorld": true, "extensionFrame": true, "inboundDelay": true, "forwardAudit": true, "stockSetup": true, "closeProtected": true, "secureOrigin": true, "signInHold": false, "explicitAttach": false, "requestInterception": true, "fetchInterception": true, "legacyInterception": false}
}

func guardSetVerdict(sum *guardSummary) (ok bool, detail string) {
	if sum == nil {
		return false, " missing summary"
	}
	if guardVerdictRefusesForged() {
		if ok, detail := guardForgedNone(sum); !ok {
			return false, detail
		}
	}
	if sum.Pass {
		return true, ""
	}
	if ok, detail := guardSetPassDespiteNotRun(sum); ok {
		return true, "; Pass=false only from expected not-run vectors" + detail
	}
	return false, detail
}
