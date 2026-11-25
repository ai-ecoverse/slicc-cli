package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type leaseEdit struct{ file, from, to string }

type leaseMutation struct {
	name    string
	edits   []leaseEdit
	run     string
	failing string
}

func leaseOverlayRun(t *testing.T, regression, target string, edits []leaseEdit, run string) ([]byte, error) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{filepath.Join(root, "internal/lease", target): filepath.Join(root, "test/integration/testdata/lease", regression)}
	for _, e := range edits {
		src := filepath.Join(root, "internal/lease", e.file)
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(raw), e.from) != 1 {
			t.Fatalf("anchor must occur once in %s:\n%s", e.file, e.from)
		}
		mutant := filepath.Join(scratch, "mutation_"+e.file)
		if err := os.WriteFile(mutant, []byte(strings.Replace(string(raw), e.from, e.to, 1)), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[src] = mutant
	}
	overlay := hrOverlay(t, scratch, replacements)
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", run, "./internal/lease")
	command.Dir = root
	return command.CombinedOutput()
}

func leaseMutationsRun(t *testing.T, regression, target string, mutations []leaseMutation) {
	t.Helper()
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			output, err := leaseOverlayRun(t, regression, target, m.edits, m.run)
			if err == nil {
				t.Fatalf("mutation %s must make %s red; got green\n%s", m.name, m.failing, output)
			}
			if !strings.Contains(string(output), "--- FAIL: "+m.failing+" ") {
				t.Fatalf("mutation %s did not fail %s\n%s", m.name, m.failing, output)
			}
		})
	}
}

func TestSignedLeaseExpansionEdges(t *testing.T) {
	if output, err := leaseOverlayRun(t, "expansion.go", "expansion_test.go", nil, "^TestLeaseExpansion"); err != nil {
		t.Fatalf("expansion edges: %v\n%s", err, output)
	}
}

func TestSignedLeaseExpansionMutations(t *testing.T) {
	check := "\t\t\tif r.Kind != \"exact\" || slices.Contains(a.Allow, r) {\n"
	parent := "\tif o.Parent != \"\" {\n\t\ta := b.active[o.Parent]\n"
	leaseMutationsRun(t, "expansion.go", "expansion_test.go", []leaseMutation{
		{"kind-check-removed", []leaseEdit{{"broker.go", check, "\t\t\tif slices.Contains(a.Allow, r) {\n"}}, "^TestLeaseExpansionRefusesNonExact$/^origin$", "TestLeaseExpansionRefusesNonExact/origin"},
		{"origin-only", []leaseEdit{{"broker.go", check, "\t\t\tif r.Kind == \"origin\" || slices.Contains(a.Allow, r) {\n"}}, "^TestLeaseExpansionRefusesNonExact$/^path-prefix$", "TestLeaseExpansionRefusesNonExact/path-prefix"},
		{"first-rule-only", []leaseEdit{{"broker.go", check, "\t\t\tif o.Allow[0].Kind != \"exact\" || slices.Contains(a.Allow, r) {\n"}}, "^TestLeaseExpansionRefusesNonExact$/^exact-then-origin$", "TestLeaseExpansionRefusesNonExact/exact-then-origin"},
		{"exact-refused", []leaseEdit{{"broker.go", check, "\t\t\tif len(r.Kind) > 0 || slices.Contains(a.Allow, r) {\n"}}, "^TestLeaseExpansionExactAccepted$/^one$", "TestLeaseExpansionExactAccepted/one"},
		{"top-level-restricted", []leaseEdit{{"broker.go", parent, "\tfor _, r := range o.Allow {\n\t\tif r.Kind != \"exact\" {\n\t\t\treturn nil, invalid\n\t\t}\n\t}\n" + parent}}, "^TestLeaseExpansionTopLevelUnchanged$/^origin$", "TestLeaseExpansionTopLevelUnchanged/origin"},
	})
}
