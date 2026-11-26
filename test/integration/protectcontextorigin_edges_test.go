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

func protectContextOriginOverlayRun(t *testing.T, mutant, run, row string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/protect/contextorigin_edges_test.go"): filepath.Join(root, "test/integration/testdata/protectcontextorigin/regression.go"),
	}
	if mutant != "" {
		replacements[filepath.Join(root, "internal/protect/contextorigin.go")] = filepath.Join(root, "test/integration/testdata/protectcontextorigin", mutant)
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", run, "./internal/protect")
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

func TestProtectContextOriginEdges(t *testing.T) {
	protectContextOriginOverlayRun(t, "", "^TestContextOrigin(Vectors|Rows)$", "")
}

func TestProtectContextOriginMutants(t *testing.T) {
	for _, m := range []struct{ mutant, run, row string }{
		{"mutation_ruleoff.go", "^TestContextOriginRows$/^lookalike$/^blank$", "TestContextOriginRows/lookalike/blank"},
		{"mutation_casesensitive.go", "^TestContextOriginRows$/^lookalike$/^upper$", "TestContextOriginRows/lookalike/upper"},
		{"mutation_foldexempt.go", "^TestContextOriginRows$/^lookalike$/^mixed$", "TestContextOriginRows/lookalike/mixed"},
		{"mutation_kitesurf.go", "^TestContextOriginRows$/^kitesurf$/^none$", "TestContextOriginRows/kitesurf/none"},
		{"mutation_opaque.go", "^TestContextOriginRows$/^opaque$/^none$", "TestContextOriginRows/opaque/none"},
		{"mutation_ordinary.go", "^TestContextOriginRows$/^ordinary$/^none$", "TestContextOriginRows/ordinary/none"},
		{"mutation_missing.go", "^TestContextOriginRows$/^missing$/^none$", "TestContextOriginRows/missing/none"},
		{"mutation_anydocument.go", "^TestContextOriginRows$/^non-inheriting$/^ordinary-url$", "TestContextOriginRows/non-inheriting/ordinary-url"},
		{"mutation_trim.go", "^TestContextOriginRows$/^not-lookalike$/^leading-space$", "TestContextOriginRows/not-lookalike/leading-space"},
		{"mutation_nocolon.go", "^TestContextOriginRows$/^not-lookalike$/^no-colon$", "TestContextOriginRows/not-lookalike/no-colon"},
		{"mutation_errorpageorder.go", "^TestContextOriginRows$/^error-page$/^previous-wins$", "TestContextOriginRows/error-page/previous-wins"},
	} {
		t.Run(m.mutant, func(t *testing.T) {
			protectContextOriginOverlayRun(t, m.mutant, m.run, m.row)
		})
	}
}
