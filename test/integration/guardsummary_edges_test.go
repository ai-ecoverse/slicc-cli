package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func guardSummaryOverlayRun(t *testing.T, switches, run string, wantFail bool) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "test/integration/testdata/guardsummary")
	files, err := filepath.Glob(filepath.Join(pkg, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no guard summary rows in %s: %v", pkg, err)
	}
	replacements := map[string]string{}
	for _, f := range files {
		replacements[f] = ""
	}
	replacements[filepath.Join(pkg, "regression_test.go")] = filepath.Join(pkg, "regression.go")
	replacements[filepath.Join(pkg, "guardsummary_test.go")] = filepath.Join(root, "test/integration/guardsummary_test.go")
	replacements[filepath.Join(pkg, "guardsummary_switches_test.go")] = filepath.Join(root, "test/integration", switches)
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", run, "./test/integration/testdata/guardsummary")
	command.Dir = root
	output, err := command.CombinedOutput()
	if wantFail {
		name := strings.TrimSuffix(strings.TrimPrefix(run, "^"), "$")
		if err == nil || !strings.Contains(string(output), "--- FAIL: "+name+" ") {
			t.Fatalf("mutant %s must fail the named row %s\n%s", switches, name, output)
		}
		return
	}
	if err != nil || !strings.Contains(string(output), "--- PASS: ") {
		t.Fatalf("%s: %v\n%s", run, err, output)
	}
}

func TestCDPGuardSummaryEdges(t *testing.T) {
	guardSummaryOverlayRun(t, "guardsummary_switches_test.go", "^TestGuard", false)
}

func TestCDPGuardSummaryMutationVerdictAcceptsForged(t *testing.T) {
	guardSummaryOverlayRun(t, "testdata/guardsummary/mutation_verdict_accepts_forged.go", "^TestGuardSetVerdictFailsOnForgedDespitePass$", true)
}

func TestCDPGuardSummaryMutationNotRunAcceptsForged(t *testing.T) {
	guardSummaryOverlayRun(t, "testdata/guardsummary/mutation_notrun_accepts_forged.go", "^TestGuardNotRunToleranceRefusesForged$", true)
}

func TestCDPGuardSummaryMutationNotRunAcceptsUnfinished(t *testing.T) {
	guardSummaryOverlayRun(t, "testdata/guardsummary/mutation_notrun_accepts_unfinished.go", "^TestGuardNotRunToleranceRefusesMissing$", true)
}

func TestCDPGuardSummaryMutationNotRunAcceptsBadLegs(t *testing.T) {
	guardSummaryOverlayRun(t, "testdata/guardsummary/mutation_notrun_accepts_bad_legs.go", "^TestGuardNotRunCheckFailsOnInjectedFail$", true)
}

func TestCDPGuardSummaryMutationNotRunAcceptsUnexpected(t *testing.T) {
	guardSummaryOverlayRun(t, "testdata/guardsummary/mutation_notrun_accepts_unexpected.go", "^TestGuardNotRunCheckFailsOnUnexpected$", true)
}

func TestCDPGuardSummaryMutationNotRunAcceptsVanished(t *testing.T) {
	guardSummaryOverlayRun(t, "testdata/guardsummary/mutation_notrun_accepts_vanished.go", "^TestGuardNotRunCheckFailsOnVanished$", true)
}
