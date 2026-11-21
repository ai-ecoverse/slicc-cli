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

func rnOverlay(t *testing.T, scratch string, replacements map[string]string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	return overlay
}

func TestCDPRecoveryNavEdges(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	overlay := rnOverlay(t, t.TempDir(), map[string]string{
		filepath.Join(root, "internal/caps/cdp/recovery_nav_edges_test.go"): filepath.Join(root, "test/integration/testdata/recoverynav/regression.go"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestRecoveryNav", "./internal/caps/cdp")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("recovery-nav edges: %v\n%s", err, output)
	}
}

type rnEdit struct {
	file string
	from string
	to   string
}

type rnMutation struct {
	name    string
	edits   []rnEdit
	run     string
	failing string
}

func rnMutations() []rnMutation {
	health := "\tnt := g.errorPageNavLocked(c.session)\n\treturn nt != nil && nt.done && !nt.failed\n"
	errorPageRefusal := "\t\treturn \"\"\n\t}\n\treturn refusedErrorPage\n}\n"
	errorPageAdmit := "\t\treturn \"\"\n\t}\n\treturn \"\"\n}\n"
	return []rnMutation{
		{
			name:    "exemption-removed",
			edits:   []rnEdit{{"guard_page.go", " && !g.errorPageRecoveryLocked(c) {", " {"}},
			run:     "^TestRecoveryNavOrderMatrix$/^navigate-away$/^fail$/^after-confirmError$/^relay$",
			failing: "TestRecoveryNavOrderMatrix/navigate-away/fail/after-confirmError/relay",
		},
		{
			name:    "exemption-widened-to-error-page-methods",
			edits:   []rnEdit{{"navguard.go", "\tif !errorPageRecovery[c.method] {\n", "\tif !errorPageMethods[c.method] {\n"}},
			run:     "^TestRecoveryNavReadsKeepConfirmGate$/^frame-tree$/^fail$/^after-confirmError$",
			failing: "TestRecoveryNavReadsKeepConfirmGate/frame-tree/fail/after-confirmError",
		},
		{
			name:    "exemption-widened-to-navigation-controls",
			edits:   []rnEdit{{"navguard.go", "\tif !errorPageRecovery[c.method] {\n", "\tif !navigationControls[c.method] {\n"}},
			run:     "^TestRecoveryNavReadsKeepConfirmGate$/^navigation-history$/^settle$/^after-clientCommit$",
			failing: "TestRecoveryNavReadsKeepConfirmGate/navigation-history/settle/after-clientCommit",
		},
		{
			name:    "exemption-ignores-guard-loss",
			edits:   []rnEdit{{"navguard.go", health, "\tnt := g.errorPageNavLocked(c.session)\n\treturn nt != nil\n"}},
			run:     "^TestRecoveryNavGuardLostStaysRefused$/^reload$/^confirm-failed$",
			failing: "TestRecoveryNavGuardLostStaysRefused/reload/confirm-failed",
		},
		{
			name:    "exemption-on-any-page",
			edits:   []rnEdit{{"navguard.go", health, "\ts, ok := g.sessions[c.session]\n\tif !ok {\n\t\treturn false\n\t}\n\tnt, ok := g.navTargets[s.target]\n\treturn ok && nt.done && !nt.failed\n"}},
			run:     "^TestRecoveryNavOrdinaryTwin$/^ordinary-page/navigate-away$",
			failing: "TestRecoveryNavOrdinaryTwin/ordinary-page/navigate-away",
		},
		{
			name:    "error-page-methods-widened-to-navigation-controls",
			edits:   []rnEdit{{"navguard.go", "\t\treturn refusedErrorPage\n\t}\n\tif errorPageMethods[c.method] || continueOK[c.method] {\n", "\t\treturn refusedErrorPage\n\t}\n\tif errorPageMethods[c.method] || navigationControls[c.method] || continueOK[c.method] {\n"}},
			run:     "^TestRecoveryNavContentStaysRefused$/^resource-tree$/^settle$/^after-confirm$",
			failing: "TestRecoveryNavContentStaysRefused/resource-tree/settle/after-confirm",
		},
		{
			name:    "lookup-drops-session-check",
			edits:   []rnEdit{{"navguard.go", "\ts, ok := g.sessions[sessionID]\n\tif !ok {\n\t\treturn nil\n\t}\n\tnt, ok := g.navTargets[s.target]\n\tif !ok || nt.errorURL == \"\"", "\ts := g.sessions[sessionID]\n\tnt, ok := g.navTargets[s.target]\n\tif !ok || nt.errorURL == \"\""}},
			run:     "^TestRecoveryNavUnknownLookup$/^unknown-session$/^navigate-away$",
			failing: "TestRecoveryNavUnknownLookup/unknown-session/navigate-away",
		},
		{
			name:    "lookup-drops-target-check",
			edits:   []rnEdit{{"navguard.go", "\tif !ok || nt.errorURL == \"\" || !g.o.Loaded(nt.errorURL) {\n", "\tif nt.errorURL == \"\" || !g.o.Loaded(nt.errorURL) || !ok {\n"}},
			run:     "^TestRecoveryNavUnknownLookup$/^target-without-nav-guard$/^reload$",
			failing: "TestRecoveryNavUnknownLookup/target-without-nav-guard/reload",
		},
		{
			name:    "error-page-refusal-removed",
			edits:   []rnEdit{{"navguard.go", errorPageRefusal, errorPageAdmit}},
			run:     "^TestRecoveryNavContentStaysRefused$/^evaluate$/^settle$/^after-confirm$",
			failing: "TestRecoveryNavContentStaysRefused/evaluate/settle/after-confirm",
		},
		{
			name:    "content-refusal-removed",
			edits:   []rnEdit{{"navguard.go", errorPageRefusal, errorPageAdmit}, {"conformance.go", "\t\treturn \"\"\n\t}\n\treturn refusedHistory\n}\n", "\t\treturn \"\"\n\t}\n\treturn \"\"\n}\n"}},
			run:     "^TestRecoveryNavContentStaysRefused$/^screenshot$/^fail$/^before-started$",
			failing: "TestRecoveryNavContentStaysRefused/screenshot/fail/before-started",
		},
	}
}

func TestCDPRecoveryNavMutations(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	regression := filepath.Join(root, "test/integration/testdata/recoverynav/regression.go")
	for _, m := range rnMutations() {
		t.Run(m.name, func(t *testing.T) {
			scratch := t.TempDir()
			replacements := map[string]string{filepath.Join(root, "internal/caps/cdp/recovery_nav_edges_test.go"): regression}
			for _, e := range m.edits {
				src := filepath.Join(root, "internal/caps/cdp", e.file)
				raw, err := os.ReadFile(src)
				if err != nil {
					t.Fatal(err)
				}
				text := string(raw)
				if strings.Count(text, e.from) != 1 {
					t.Fatalf("mutation %s: anchor must occur once in %s:\n%s", m.name, e.file, e.from)
				}
				mutant := filepath.Join(scratch, "mutation_"+e.file)
				if err := os.WriteFile(mutant, []byte(strings.Replace(text, e.from, e.to, 1)), 0600); err != nil {
					t.Fatal(err)
				}
				replacements[src] = mutant
			}
			overlay := rnOverlay(t, scratch, replacements)
			ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", m.run, "./internal/caps/cdp")
			command.Dir = root
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("mutation %s must make %s red; got green\n%s", m.name, m.failing, output)
			}
			if !strings.Contains(string(output), "--- FAIL: "+m.failing+" ") {
				t.Fatalf("mutation %s did not fail %s\n%s", m.name, m.failing, output)
			}
		})
	}
}
