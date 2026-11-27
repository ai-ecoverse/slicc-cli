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

func docIdentityReleaseRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func docIdentityReleaseOverlayRun(t *testing.T, replacements map[string]string, run string, failMustContain []string) {
	t.Helper()
	root := docIdentityReleaseRoot(t)
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
	out := string(output)
	if len(failMustContain) > 0 {
		if err == nil {
			t.Fatalf("mutant must fail the named row\n%s", out)
		}
		if strings.Contains(out, "[build failed]") || strings.Contains(out, "[setup failed]") {
			t.Fatalf("mutant failed to build or set up, not the named row\n%s", out)
		}
		for _, need := range failMustContain {
			if !strings.Contains(out, need) {
				t.Fatalf("mutant output missing %q\n%s", need, out)
			}
		}
		t.Logf("mutant red as required:\n%s", out)
		return
	}
	if err != nil {
		t.Fatalf("%s: %v\n%s", run, err, out)
	}
	t.Logf("production green:\n%s", out)
}

func TestCDPDocIdentityReleaseAtTargetInfoEdges(t *testing.T) {
	root := docIdentityReleaseRoot(t)
	docIdentityReleaseOverlayRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/docidentityrelease_edges_test.go"): filepath.Join(root, "test/integration/testdata/docidentityrelease/regression.go"),
	}, "^TestDocIdentityReleaseAtTargetInfo$", nil)
}

func TestCDPDocIdentityReleaseAtTargetInfoMutationWaitNavCommand(t *testing.T) {
	root := docIdentityReleaseRoot(t)
	docIdentityReleaseOverlayRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/docidentityrelease_edges_test.go"): filepath.Join(root, "test/integration/testdata/docidentityrelease/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentidentity.go"):              filepath.Join(root, "test/integration/testdata/docidentityrelease/mutation_waitnavcommand.go"),
	}, "^TestDocIdentityReleaseAtTargetInfo$/^reply-released-at-target-info-before-later-taint$", []string{
		"--- FAIL: TestDocIdentityReleaseAtTargetInfo/reply-released-at-target-info-before-later-taint",
		"Page.getFrameTree sent 1 times while the hold was open",
	})
}
