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

func defaultContextOriginOverlayRun(t *testing.T, mutant, run, row string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/defaultcontextorigin_edges_test.go"): filepath.Join(root, "test/integration/testdata/defaultcontextorigin/regression.go"),
	}
	if mutant != "" {
		replacements[filepath.Join(root, "internal/caps/cdp/contextmark.go")] = filepath.Join(root, "test/integration/testdata/defaultcontextorigin", mutant)
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

func TestCDPDefaultContextOriginEdges(t *testing.T) {
	defaultContextOriginOverlayRun(t, "", "^TestDefaultContextOrigin(Sticky)?$", "")
}

func TestCDPDefaultContextOriginMutants(t *testing.T) {
	for _, m := range []struct{ mutant, run, row string }{
		{"mutation_ruleoff.go", "^TestDefaultContextOrigin$/^lookalike$/^blank$", "TestDefaultContextOrigin/lookalike/blank"},
		{"mutation_nondefault.go", "^TestDefaultContextOrigin$/^non-default$", "TestDefaultContextOrigin/non-default"},
		{"mutation_anyframe.go", "^TestDefaultContextOrigin$/^empty-frame$", "TestDefaultContextOrigin/empty-frame"},
		{"mutation_opaque.go", "^TestDefaultContextOrigin$/^opaque$", "TestDefaultContextOrigin/opaque"},
		{"mutation_ordinary.go", "^TestDefaultContextOrigin$/^ordinary$", "TestDefaultContextOrigin/ordinary"},
		{"mutation_missing.go", "^TestDefaultContextOrigin$/^missing$", "TestDefaultContextOrigin/missing"},
		{"mutation_inheritingonly.go", "^TestDefaultContextOrigin$/^lookalike$/^ordinary-document$", "TestDefaultContextOrigin/lookalike/ordinary-document"},
		{"mutation_inheritingorigin.go", "^TestDefaultContextOrigin$/^lookalike$/^newtab$", "TestDefaultContextOrigin/lookalike/newtab"},
		{"mutation_kitesurf.go", "^TestDefaultContextOrigin$/^kitesurf$/^fail-closed$", "TestDefaultContextOrigin/kitesurf/fail-closed"},
	} {
		t.Run(m.mutant, func(t *testing.T) {
			defaultContextOriginOverlayRun(t, m.mutant, m.run, m.row)
		})
	}
}
