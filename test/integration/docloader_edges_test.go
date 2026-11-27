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

func docLoaderOverlayRun(t *testing.T, file, mutant, run, row string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/docloader_edges_test.go"): filepath.Join(root, "test/integration/testdata/docloader/regression.go"),
	}
	if mutant != "" {
		replacements[filepath.Join(root, "internal/caps/cdp", file)] = filepath.Join(root, "test/integration/testdata/docloader", mutant)
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

func TestCDPDocLoaderEdges(t *testing.T) {
	docLoaderOverlayRun(t, "", "", "^TestDocLoader(Methods|Sources|Precedence|Holds|Ledger)$", "")
}

func docLoaderMethodNames() []string {
	return []string{
		"Runtime.evaluate", "DOM.getDocument", "DOM.getOuterHTML", "DOMSnapshot.captureSnapshot",
		"Page.captureScreenshot", "Page.printToPDF", "Page.getResourceContent",
		"Accessibility.getFullAXTree", "Accessibility.getPartialAXTree", "Accessibility.queryAXTree",
	}
}

func TestCDPDocLoaderMutants(t *testing.T) {
	type mutantRow struct{ file, mutant, test, row string }
	rows := []mutantRow{}
	for _, method := range docLoaderMethodNames() {
		rows = append(rows,
			mutantRow{"document_identity_switches.go", "mutation_compareoff.go", "Methods", method + "/replaced"},
			mutantRow{"documentloader.go", "mutation_sameloaderdrops.go", "Methods", method + "/same-loader"},
		)
	}
	rows = append(rows, []mutantRow{
		{"documentidentity.go", "mutation_cut.go", "Methods", "Runtime.evaluate/replaced"},
		{"documentidentity.go", "mutation_cut.go", "Methods", "Page.captureScreenshot/replaced"},
		{"documentidentity.go", "mutation_cut.go", "Sources", "tracker-replaced"},
		{"documentloader.go", "mutation_sameloaderdrops.go", "Sources", "same-document"},
		{"document_identity_switches.go", "mutation_compareoff.go", "Sources", "tracker-replaced"},
		{"documentloader.go", "mutation_unknowndiffers.go", "Sources", "admission-unknown"},
		{"documentloader.go", "mutation_unknowndiffers.go", "Sources", "current-unknown"},
		{"documentidentity.go", "mutation_waitnavtree.go", "Sources", "href-info-before-taint"},
		{"documentidentity.go", "mutation_loaderfirst.go", "Precedence", "chrome-error-after-hold"},
		{"document_identity_switches.go", "mutation_compareoff.go", "Holds", "different-admission-loaders"},
		{"documentidentity.go", "mutation_perhold.go", "Holds", "different-admission-loaders"},
		{"documentidentity.go", "mutation_perhold.go", "Ledger", "several-holds"},
		{"documentidentity.go", "mutation_perhold.go", "Ledger", "shared-ask"},
		{"documentidentity.go", "mutation_pinnedheld.go", "Ledger", "no-hold"},
	}...)
	for _, m := range rows {
		t.Run(m.mutant+"/"+m.test+"/"+m.row, func(t *testing.T) {
			name := "TestDocLoader" + m.test
			docLoaderOverlayRun(t, m.file, m.mutant, "^"+name+"$/^"+strings.ReplaceAll(m.row, "/", "$/^")+"$", name+"/"+m.row)
		})
	}
}
