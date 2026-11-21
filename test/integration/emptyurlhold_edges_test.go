package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func emptyURLHoldRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func emptyURLHoldOverlayRun(t *testing.T, replacements map[string]string, run string, wantFail bool) {
	t.Helper()
	root := emptyURLHoldRoot(t)
	scratch := t.TempDir()
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", run, "./internal/caps/cdp")
	command.Dir = root
	output, err := command.CombinedOutput()
	if wantFail {
		if err == nil {
			t.Fatalf("mutant %s must fail the named row\n%s", run, output)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s: %v\n%s", run, err, output)
	}
}

func TestCDPEmptyURLHoldEdges(t *testing.T) {
	root := emptyURLHoldRoot(t)
	emptyURLHoldOverlayRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/empty_url_hold_edges_test.go"): filepath.Join(root, "test/integration/testdata/emptyurlhold/regression.go"),
	}, "^TestEmptyURLHold", false)
}

func TestCDPEmptyURLHoldMutationDropWaitingFalse(t *testing.T) {
	root := emptyURLHoldRoot(t)
	emptyURLHoldOverlayRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/empty_url_hold_edges_test.go"): filepath.Join(root, "test/integration/testdata/emptyurlhold/regression.go"),
		filepath.Join(root, "internal/caps/cdp/hold_resume_switches.go"):      filepath.Join(root, "test/integration/testdata/emptyurlhold/mutation_drop_waiting_false.go"),
	}, "^TestEmptyURLHoldTabPopupPageChildResumedAtHold/waiting=false$", true)
}

func TestCDPEmptyURLHoldMutationResumeUnheld(t *testing.T) {
	root := emptyURLHoldRoot(t)
	emptyURLHoldOverlayRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/empty_url_hold_edges_test.go"): filepath.Join(root, "test/integration/testdata/emptyurlhold/regression.go"),
		filepath.Join(root, "internal/caps/cdp/hold_resume_switches.go"):      filepath.Join(root, "test/integration/testdata/emptyurlhold/mutation_resume_unheld.go"),
	}, "^TestEmptyURLHoldResumeUnheldChild$", true)
}

func TestCDPEmptyURLHoldMutationResumeTwice(t *testing.T) {
	root := emptyURLHoldRoot(t)
	emptyURLHoldOverlayRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/empty_url_hold_edges_test.go"): filepath.Join(root, "test/integration/testdata/emptyurlhold/regression.go"),
		filepath.Join(root, "internal/caps/cdp/hold_resume_switches.go"):      filepath.Join(root, "test/integration/testdata/emptyurlhold/mutation_resume_twice.go"),
	}, "^TestEmptyURLHoldResumeTwice$", true)
}

func TestCDPEmptyURLHoldMutationResumeRefused(t *testing.T) {
	root := emptyURLHoldRoot(t)
	emptyURLHoldOverlayRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/empty_url_hold_edges_test.go"): filepath.Join(root, "test/integration/testdata/emptyurlhold/regression.go"),
		filepath.Join(root, "internal/caps/cdp/hold_resume_switches.go"):      filepath.Join(root, "test/integration/testdata/emptyurlhold/mutation_resume_refused.go"),
	}, "^TestEmptyURLHoldResumeRefusedChild$", true)
}

func TestCDPEmptyURLHoldMutationProtectedFate(t *testing.T) {
	root := emptyURLHoldRoot(t)
	emptyURLHoldOverlayRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/empty_url_hold_edges_test.go"): filepath.Join(root, "test/integration/testdata/emptyurlhold/regression.go"),
		filepath.Join(root, "internal/caps/cdp/hold_resume_switches.go"):      filepath.Join(root, "test/integration/testdata/emptyurlhold/mutation_protected_fate.go"),
	}, "^TestEmptyURLHoldResumeProtectedFateUnchanged$", true)
}
