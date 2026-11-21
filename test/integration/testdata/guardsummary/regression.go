package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var guardFixtureLegacyObservations = []string{"legacyHeadersPlanted", "legacyHeadersAnswered", "legacyHeadersRefused", "legacyRawPlanted", "legacyRawAnswered", "legacyRawRefused"}

func guardFixtureForgedWhy() string {
	parts := make([]string, len(guardFixtureLegacyObservations))
	for i, name := range guardFixtureLegacyObservations {
		parts[i] = "not-supported:" + name + " while the observation should run"
	}
	return strings.Join(parts, "; ")
}

func guardFixtureForged() []any {
	var out []any
	for _, row := range [][2]string{{"C10.cookie-plant", "seven"}, {"C10.cookie-plant", "vault"}, {"C10.cookie-plant.credential", "idp"}, {"C10.cookie-plant.credential", "idp-stg"}} {
		out = append(out, map[string]any{"id": row[0], "target": row[1], "variant": "prompt", "transport": "cdp", "why": guardFixtureForgedWhy()})
	}
	return out
}

func guardFixtureRow(id, target, status string) map[string]any {
	return map[string]any{"id": id, "target": target, "variant": "prompt", "transport": "cdp", "control": status, "guarded": status, "live": status}
}

func guardFixture(forged bool) map[string]any {
	results := []any{
		guardFixtureRow("C3.tab-attach", "seven", "pass"),
		guardFixtureRow("C9.page-drivable", "ordinary", "skip"),
	}
	for _, target := range []string{"seven", "vault"} {
		r := guardFixtureRow("C10.cookie-plant", target, "pass")
		r["notSupported"] = guardFixtureLegacyObservations
		results = append(results, r)
	}
	for _, target := range []string{"idp", "idp-stg"} {
		r := guardFixtureRow("C10.cookie-plant.credential", target, "pass")
		r["notSupported"] = guardFixtureLegacyObservations
		results = append(results, r)
	}
	for _, id := range guardNotRunExpectedIDs() {
		results = append(results, guardFixtureRow(id, "seven", "not-run"))
	}
	sum := map[string]any{
		"set":             "@ai-ecoverse/slicc-browsers@1.29.1",
		"pass":            true,
		"results":         results,
		"missing":         []any{},
		"forged":          []any{},
		"notRun":          guardNotRunExpectedIDs(),
		"observerSkipped": 0,
		"counts":          map[string]any{},
		"line":            "GUARD-PASS slicc-cli@e64c5f2 (vectors @ai-ecoverse/slicc-browsers@1.29.1)",
	}
	if forged {
		sum["pass"] = false
		sum["forged"] = guardFixtureForged()
		sum["line"] = "GUARD-FAIL slicc-cli@e64c5f2 (vectors @ai-ecoverse/slicc-browsers@1.29.1) forged 4"
	}
	return sum
}

func guardFixtureSummary(t *testing.T, raw map[string]any) *guardSummary {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var sum guardSummary
	if err := json.Unmarshal(b, &sum); err != nil {
		t.Fatal(err)
	}
	return &sum
}

func guardFixtureNotRunOnly(t *testing.T, edit func(map[string]any)) *guardSummary {
	t.Helper()
	raw := guardFixture(false)
	raw["pass"] = false
	raw["line"] = "GUARD-FAIL slicc-cli@e64c5f2 (vectors @ai-ecoverse/slicc-browsers@1.29.1)"
	if edit != nil {
		edit(raw)
	}
	return guardFixtureSummary(t, raw)
}

func TestGuardSetVerdictFailsOnForgedRows(t *testing.T) {
	ok, detail := guardSetVerdict(guardFixtureSummary(t, guardFixture(true)))
	if ok {
		t.Fatalf("a summary with 4 forged rows must fail the set, got ok detail=%q", detail)
	}
	for _, want := range []string{"forged", "C10.cookie-plant seven prompt", "C10.cookie-plant vault prompt", "C10.cookie-plant.credential idp prompt", "C10.cookie-plant.credential idp-stg prompt", "not-supported:legacyRawRefused while the observation should run"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q does not name %q", detail, want)
		}
	}
}

func TestGuardSetVerdictFailsOnForgedDespitePass(t *testing.T) {
	raw := guardFixture(true)
	raw["pass"] = true
	ok, detail := guardSetVerdict(guardFixtureSummary(t, raw))
	if ok || !strings.Contains(detail, "forged") {
		t.Fatalf("forged rows must fail the set even when pass is true, got ok=%v detail=%q", ok, detail)
	}
}

func TestGuardSetVerdictFailsWithoutForgedField(t *testing.T) {
	raw := guardFixture(false)
	delete(raw, "forged")
	ok, detail := guardSetVerdict(guardFixtureSummary(t, raw))
	if ok || !strings.Contains(detail, "forged") {
		t.Fatalf("a summary without the runner's forged list must fail the set, got ok=%v detail=%q", ok, detail)
	}
}

func TestGuardSetVerdictPassesCleanRun(t *testing.T) {
	if ok, detail := guardSetVerdict(guardFixtureSummary(t, guardFixture(false))); !ok {
		t.Fatalf("a passing summary with forged [] must pass: %s", detail)
	}
}

func TestGuardSetVerdictFailsOnPassFalseWithoutNotRunExplanation(t *testing.T) {
	raw := guardFixture(false)
	raw["pass"] = false
	raw["notRun"] = []string{}
	ok, detail := guardSetVerdict(guardFixtureSummary(t, raw))
	if ok {
		t.Fatalf("pass=false outside the expected not-run list must fail, got detail=%q", detail)
	}
}

func TestGuardNotRunToleranceRefusesForged(t *testing.T) {
	ok, detail := guardSetPassDespiteNotRun(guardFixtureSummary(t, guardFixture(true)))
	if ok || !strings.Contains(detail, "forged") {
		t.Fatalf("the not-run tolerance must not accept forged rows, got ok=%v detail=%q", ok, detail)
	}
}

func TestGuardNotRunToleranceRefusesMissing(t *testing.T) {
	sum := guardFixtureNotRunOnly(t, func(raw map[string]any) {
		raw["missing"] = []any{map[string]any{"id": "C3.tab-attach", "target": "vault", "variant": "prompt", "transport": "cdp"}}
	})
	ok, detail := guardSetPassDespiteNotRun(sum)
	if ok || !strings.Contains(detail, "missing") {
		t.Fatalf("the not-run tolerance must not accept missing rows, got ok=%v detail=%q", ok, detail)
	}
}

func TestGuardNotRunToleranceRefusesObserverSkipped(t *testing.T) {
	sum := guardFixtureNotRunOnly(t, func(raw map[string]any) { raw["observerSkipped"] = 1 })
	ok, detail := guardSetPassDespiteNotRun(sum)
	if ok || !strings.Contains(detail, "observer") {
		t.Fatalf("the not-run tolerance must not accept observer skips, got ok=%v detail=%q", ok, detail)
	}
}

func TestGuardNotRunToleranceRefusesEmptyResults(t *testing.T) {
	sum := guardFixtureNotRunOnly(t, func(raw map[string]any) { raw["results"] = []any{} })
	if ok, detail := guardSetPassDespiteNotRun(sum); ok {
		t.Fatalf("the not-run tolerance must not accept a run without rows, got detail=%q", detail)
	}
}

func TestGuardNotRunToleranceRefusesMixedLegs(t *testing.T) {
	sum := guardFixtureNotRunOnly(t, func(raw map[string]any) {
		r := guardFixtureRow("C3.tab-attach", "vault", "pass")
		r["guarded"] = "skip"
		raw["results"] = append(raw["results"].([]any), r)
	})
	ok, detail := guardSetPassDespiteNotRun(sum)
	if ok || !strings.Contains(detail, "C3.tab-attach") {
		t.Fatalf("the not-run tolerance must not accept a row with mixed legs, got ok=%v detail=%q", ok, detail)
	}
}

func TestGuardNotRunCheckToleratesExpected(t *testing.T) {
	if ok, detail := guardSetPassDespiteNotRun(guardFixtureNotRunOnly(t, nil)); !ok {
		t.Fatalf("expected not-run set must be tolerated%s", detail)
	}
}

func TestGuardNotRunCheckFailsOnInjectedFail(t *testing.T) {
	sum := guardFixtureNotRunOnly(t, func(raw map[string]any) {
		r := guardFixtureRow("C3.tab-attach", "vault", "pass")
		r["control"] = "fail"
		raw["results"] = append(raw["results"].([]any), r)
	})
	ok, detail := guardSetPassDespiteNotRun(sum)
	if ok || !strings.Contains(detail, "C3.tab-attach control=fail") {
		t.Fatalf("injected fail must fail the set, got ok=%v detail=%s", ok, detail)
	}
}

func TestGuardNotRunCheckFailsOnUnexpected(t *testing.T) {
	sum := guardFixtureNotRunOnly(t, func(raw map[string]any) {
		raw["notRun"] = append(guardNotRunExpectedIDs(), "Z9")
		raw["results"] = append(raw["results"].([]any), guardFixtureRow("Z9", "seven", "not-run"))
	})
	ok, detail := guardSetPassDespiteNotRun(sum)
	if ok || !strings.Contains(detail, "unexpected [Z9]") {
		t.Fatalf("unexpected not-run must fail, got ok=%v detail=%s", ok, detail)
	}
}

func TestGuardNotRunCheckFailsOnVanished(t *testing.T) {
	want := guardNotRunExpectedIDs()
	sum := guardFixtureNotRunOnly(t, func(raw map[string]any) { raw["notRun"] = want[:len(want)-1] })
	ok, detail := guardSetPassDespiteNotRun(sum)
	if ok || !strings.Contains(detail, "vanished [B7]") {
		t.Fatalf("vanished expected not-run must fail, got ok=%v detail=%s", ok, detail)
	}
}

func TestGuardReportCarriesForgedAndLine(t *testing.T) {
	raw := guardFixture(true)
	path, err := writeGuardReport(t.TempDir(), "prompt", guardFixtureSummary(t, raw))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "guard-report-prompt.json" {
		t.Errorf("report path %s", path)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	var forged []map[string]string
	if err := json.Unmarshal(got["forged"], &forged); err != nil || len(forged) != 4 {
		t.Fatalf("the report must carry the runner's 4 forged rows, got %s (%v)", got["forged"], err)
	}
	for _, f := range forged {
		if f["why"] != guardFixtureForgedWhy() || !strings.HasPrefix(f["id"], "C10.cookie-plant") {
			t.Errorf("forged row %v", f)
		}
	}
	var line string
	if err := json.Unmarshal(got["line"], &line); err != nil || line != raw["line"] {
		t.Fatalf("the report must carry the runner's line %q, got %s (%v)", raw["line"], got["line"], err)
	}
}

func TestGuardCapabilitiesDeclareLegacyInterceptionFalse(t *testing.T) {
	caps := guardCapabilities()
	legacy, declared := caps["legacyInterception"]
	if !declared || legacy {
		t.Errorf("legacyInterception must be declared false (present %v, value %v)", declared, legacy)
	}
	for _, name := range []string{"requestInterception", "fetchInterception"} {
		if !caps[name] {
			t.Errorf("%s must stay declared true", name)
		}
	}
}
