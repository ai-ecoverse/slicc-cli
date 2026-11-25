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

type leaseGotoEdit struct{ file, from, to string }

func leaseGotoRun(t *testing.T, edits []leaseGotoEdit, run string) ([]byte, error) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{filepath.Join(root, "internal/lease/canonical_test.go"): filepath.Join(root, "test/integration/testdata/lease/canonical.go")}
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

func TestSignedLeaseGotoCanonicalEdges(t *testing.T) {
	if output, err := leaseGotoRun(t, nil, "^TestLeaseGotoCanonical"); err != nil {
		t.Fatalf("canonical goto edges: %v\n%s", err, output)
	}
}

func TestSignedLeaseGotoCanonicalMutations(t *testing.T) {
	use := "\t\tif url, ok = canonical(c.Allow, url, o); !ok {\n"
	ret := "\thref := u.Href(false)\n\treturn href, matches(rules, href, o)\n"
	for _, m := range []struct {
		name    string
		edits   []leaseGotoEdit
		run     string
		failing string
	}{
		{"raw-sent", []leaseGotoEdit{{"browser.go", use, "\t\tif _, ok = canonical(c.Allow, url, o); !ok {\n"}}, "^TestLeaseGotoCanonicalCreate$/^case$", "TestLeaseGotoCanonicalCreate/case"},
		{"raw-sent-assign", []leaseGotoEdit{{"browser.go", use, "\t\tif _, ok = canonical(c.Allow, url, o); !ok {\n"}}, "^TestLeaseGotoCanonicalAssign$/^idn-fullwidth$", "TestLeaseGotoCanonicalAssign/idn-fullwidth"},
		{"canonical-returns-raw", []leaseGotoEdit{{"contract.go", ret, "\thref := u.Href(false)\n\treturn raw, matches(rules, href, o)\n"}}, "^TestLeaseGotoCanonicalCreate$/^default-port$", "TestLeaseGotoCanonicalCreate/default-port"},
		{"canonical-not-rechecked", []leaseGotoEdit{{"contract.go", ret, "\thref := u.Href(false)\n\treturn href, true\n"}}, "^TestLeaseGotoCanonicalRefused$/^canonical-over-limit/assign$", "TestLeaseGotoCanonicalRefused/canonical-over-limit/assign"},
		{"raw-not-checked", []leaseGotoEdit{{"contract.go", "\tif !ok || !matches(rules, raw, o) {\n\t\treturn \"\", false\n", "\tif !ok {\n\t\treturn \"\", false\n"}}, "^TestLeaseGotoCanonicalDirect$/^refused$/^dot-segments-exact$", "TestLeaseGotoCanonicalDirect/refused/dot-segments-exact"},
		{"refusal-ignored", []leaseGotoEdit{{"browser.go", use, "\t\tif url, ok = canonical(c.Allow, url, o); !ok && false {\n"}}, "^TestLeaseGotoCanonicalRefused$/^canonical-over-limit/assign$", "TestLeaseGotoCanonicalRefused/canonical-over-limit/assign"},
		{"snapshot-canonicalised", []leaseGotoEdit{{"browser.go", "\tif op == \"goto\" {\n\t\tvar ok bool\n", "\tif op != \"\" {\n\t\tvar ok bool\n"}}, "^TestLeaseGotoCanonicalSnapshotUnchanged$", "TestLeaseGotoCanonicalSnapshotUnchanged"},
	} {
		t.Run(m.name, func(t *testing.T) {
			output, err := leaseGotoRun(t, m.edits, m.run)
			if err == nil {
				t.Fatalf("mutation %s must make %s red; got green\n%s", m.name, m.failing, output)
			}
			if !strings.Contains(string(output), "--- FAIL: "+m.failing+" ") {
				t.Fatalf("mutation %s did not fail %s\n%s", m.name, m.failing, output)
			}
		})
	}
}
