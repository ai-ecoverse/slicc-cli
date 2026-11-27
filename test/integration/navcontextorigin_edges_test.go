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

func navContextOriginOverlayRun(t *testing.T, file, mutant, run, row string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/navcontextorigin_edges_test.go"): filepath.Join(root, "test/integration/testdata/navcontextorigin/regression.go"),
	}
	if mutant != "" {
		replacements[filepath.Join(root, "internal/caps/cdp", file)] = filepath.Join(root, "test/integration/testdata/navcontextorigin", mutant)
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

func TestCDPNavContextOriginEdges(t *testing.T) {
	navContextOriginOverlayRun(t, "", "", "^TestNavContextOrigin(Judge|Vectors|Order|Frames|TargetsApart|Lifecycle|Evidence|MarkClear|Client|Ledger|URLProtected|SevenToOrdinary|BlankMarkThenOrdinary)$", "")
}

func TestCDPNavContextOriginMutants(t *testing.T) {
	for _, m := range []struct{ file, mutant, test, row string }{
		{"contextmark.go", "mutation_oldjudge.go", "Judge", "protected/kitesurf"},
		{"contextmark.go", "mutation_oldjudge.go", "Vectors", "idx71"},
		{"contextmark.go", "mutation_oldjudge.go", "Vectors", "idx76"},
		{"contextmark.go", "mutation_oldjudge.go", "Vectors", "idx77"},
		{"contextmark.go", "mutation_foldjudge.go", "Judge", "protected/upper"},
		{"contextmark.go", "mutation_opaqueclears.go", "MarkClear", "kitesurf-holds"},
		{"contextmark.go", "mutation_navoff.go", "Order", "before-client/protected"},
		{"contextmark.go", "mutation_navoff.go", "Vectors", "idx80"},
		{"contextmark.go", "mutation_noredecide.go", "Order", "admitted-then/lookalike"},
		{"contextmark.go", "mutation_noredecide.go", "Order", "admitted-then/protected"},
		{"contextmark.go", "mutation_childframe.go", "Frames", "child-frame"},
		{"contextmark.go", "mutation_isolated.go", "Frames", "isolated-world"},
		{"contextmark.go", "mutation_crosstarget.go", "TargetsApart", "other-target-frame"},
		{"contextmark.go", "mutation_crosstarget.go", "TargetsApart", "reused-session"},
		{"contextmark.go", "mutation_keepondestroy.go", "Lifecycle", "destroyed-clears"},
		{"contextmark.go", "mutation_anydestroy.go", "Lifecycle", "other-destroyed-keeps"},
		{"contextmark.go", "mutation_keeponcleared.go", "Lifecycle", "cleared-clears"},
		{"contextmark.go", "mutation_keeponcommit.go", "Lifecycle", "new-loader-clears"},
		{"contextmark.go", "mutation_sameloaderclears.go", "Lifecycle", "same-loader-keeps"},
		{"contextmark.go", "mutation_childcommitclears.go", "Lifecycle", "child-commit-keeps"},
		{"contextmark.go", "mutation_extracommand.go", "Ledger", "ordinary-run"},
		{"contextmark.go", "mutation_marksall.go", "URLProtected", "seven-url-gets-no-nav-mark"},
		{"contextmark.go", "mutation_marksall.go", "URLProtected", "vault-url-gets-no-nav-mark"},
		{"contextmark.go", "mutation_marksall.go", "SevenToOrdinary", "context-before-commit-releases"},
		{"contextmark.go", "mutation_marksall.go", "SevenToOrdinary", "no-context-after-commit-releases"},
		{"contextmark.go", "mutation_inheritingonly.go", "URLProtected", "ordinary-url-lookalike-marks"},
		{"contextmark.go", "mutation_clearsbeforecommit.go", "BlankMarkThenOrdinary", "context-before-commit-stays-marked"},
		{"contextmark.go", "mutation_commitclears.go", "BlankMarkThenOrdinary", "no-context-after-commit-stays-marked"},
		{"navguard.go", "mutation_noevidence.go", "Evidence", "context-origin"},
		{"navguard.go", "mutation_noevidence.go", "Evidence", "reason"},
		{"navguard.go", "mutation_errorevidence.go", "Evidence", "with-error-page"},
	} {
		t.Run(m.mutant+"/"+m.test+"/"+m.row, func(t *testing.T) {
			name := "TestNavContextOrigin" + m.test
			navContextOriginOverlayRun(t, m.file, m.mutant, "^"+name+"$/^"+strings.ReplaceAll(m.row, "/", "$/^")+"$", name+"/"+m.row)
		})
	}
}

func navContextOriginReadmeRow(readme string) string {
	bullet := func(head string) string {
		for line := range strings.SplitSeq(readme, "\n") {
			if strings.HasPrefix(line, head) {
				return line
			}
		}
		return ""
	}
	targets, unparseable := bullet("- **Targets.** "), bullet("- **URLs slicc can't parse** ")
	for _, want := range []string{
		"exactly `://` and exactly `about://` are opaque reports: they protect nothing by themselves, so the URL, opener and previous rules above decide",
		"a blank page whose default context reports `about://` is protected only when its opener is protected or unknown or it was protected before",
		"Any other value that starts with `about:` in any case protects the page, such as `about:blank`, `about:newtab`, `ABOUT://` or `about:///`",
		"and so does a protected origin or one slicc can't parse, such as a bare `about`",
		"The node also reads the main frame's default context on its own guard session",
		"whether or not the page enables `Runtime` on its session and whether the context arrives before or after the page attached",
	} {
		if !strings.Contains(targets, want) {
			return "the Targets bullet lacks: " + want
		}
	}
	for _, gone := range []string{"is protected too: a browser that reports `about://` for its blank pages has them refused", "but the node still judges it as the `chrome://` URL it names"} {
		if strings.Contains(targets, gone) {
			return "the Targets bullet still says: " + gone
		}
	}
	if !strings.Contains(unparseable, "`about://` is opaque only as the main frame's default-context `origin` that *Targets* judges") || strings.Contains(unparseable, "`about://` is not treated as opaque here") {
		return "the unparseable-URL bullet does not state where `about://` is opaque"
	}
	return ""
}

func TestCDPNavContextOriginReadme(t *testing.T) {
	for _, c := range []struct {
		name, file string
		ok         bool
	}{
		{"current", "../../README.md", true},
		{"old-fail-closed", "testdata/navcontextorigin/readme_old.txt", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, err := os.ReadFile(c.file)
			if err != nil {
				t.Fatal(err)
			}
			if got := navContextOriginReadmeRow(string(raw)); (got == "") != c.ok {
				t.Fatalf("%s: row answer %q, want ok=%v", c.file, got, c.ok)
			}
		})
	}
}
