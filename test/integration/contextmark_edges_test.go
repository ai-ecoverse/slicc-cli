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

func contextMarkOverlayRun(t *testing.T, mutant, run, row string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/contextmark_edges_test.go"): filepath.Join(root, "test/integration/testdata/contextmark/regression.go"),
	}
	if mutant != "" {
		replacements[filepath.Join(root, "internal/caps/cdp/contextmark.go")] = filepath.Join(root, "test/integration/testdata/contextmark", mutant)
	}
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", run, "./internal/caps/cdp")
	command.Dir = root
	output, err := command.CombinedOutput()
	if row != "" {
		if err == nil || !strings.Contains(string(output), "--- FAIL: "+row+" ") {
			t.Fatalf("mutant %s must fail %s\n%s", mutant, row, output)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s: %v\n%s", run, err, output)
	}
}

func TestCDPDefaultContextMarkEdges(t *testing.T) {
	contextMarkOverlayRun(t, "", "^TestDefaultContextMark(Holds|PagesApart|InheritedBlank|Reason|EmptyLoader|TargetReuse|URLLagHeldByHistory|SetupLatch)$", "")
}

func TestCDPDefaultContextMarkMutants(t *testing.T) {
	for _, m := range []struct{ mutant, test, row string }{
		{"mutation_nomark.go", "Holds", "title-only"},
		{"mutation_anysession.go", "Holds", "commit-on-client-session"},
		{"mutation_anyframe.go", "Holds", "commit-of-child-frame"},
		{"mutation_emptyloader.go", "Holds", "commit-empty-loader"},
		{"mutation_sameloader.go", "Holds", "commit-same-loader"},
		{"mutation_nocommit.go", "Holds", "ordinary-context-before-commit"},
		{"mutation_noreset.go", "Holds", "late-ordinary-context-after-protected"},
		{"mutation_nondefault.go", "Holds", "commit-then-isolated-context"},
		{"mutation_otherframe.go", "Holds", "commit-then-child-default-context"},
		{"mutation_opaque.go", "Holds", "commit-then-opaque-context"},
		{"mutation_null.go", "Holds", "commit-then-null-context"},
		{"mutation_missing.go", "Holds", "commit-then-missing-origin"},
		{"mutation_noredecide.go", "Holds", "info-before-context-then-clears"},
		{"mutation_confirmcommits.go", "Holds", "confirm-fill-is-not-a-commit"},
		{"mutation_nobaseline.go", "EmptyLoader", "late-old-commit-is-baseline"},
		{"mutation_notreeread.go", "EmptyLoader", "tree-baseline-then-commit-clears"},
		{"mutation_firstcommitclears.go", "EmptyLoader", "new-document-before-baseline-holds-then-next-clears"},
		{"mutation_noforget.go", "TargetReuse", ""},
	} {
		t.Run(m.mutant, func(t *testing.T) {
			name := "TestDefaultContextMark" + m.test
			run, row := "^"+name+"$", name
			if m.row != "" {
				run, row = run+"/^"+m.row+"$", name+"/"+m.row
			}
			contextMarkOverlayRun(t, m.mutant, run, row)
		})
	}
}
