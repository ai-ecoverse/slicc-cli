package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

type frameLatchEdit struct {
	file, from, to string
}

type frameLatchMutation struct {
	name, run, failing string
	all                []string
	edits              []frameLatchEdit
}

var frameLatchRows = []string{
	"framelatch-mark-clears",
	"framelatch-mark-survives-probe",
	"framelatch-mark-survives-same-document",
	"framelatch-mark-probe-refuses",
	"framelatch-older-survives-commit",
	"framelatch-older-survives-probe",
	"framelatch-mark-keeps-older",
	"framelatch-older-takes-over",
	"framelatch-mark-leaves-older-value",
	"framelatch-probe-refuses",
}

func frameLatchOverlay(t *testing.T, mutation frameLatchMutation) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/frame_latch_edges_test.go"): filepath.Join(root, "test/integration/testdata/framelatch/regression.go"),
	}
	byFile := map[string][]frameLatchEdit{}
	for _, e := range mutation.edits {
		byFile[e.file] = append(byFile[e.file], e)
	}
	for file, edits := range byFile {
		src := filepath.Join(root, "internal/caps/cdp", file)
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, e := range edits {
			if n := strings.Count(text, e.from); n != 1 {
				t.Fatalf("mutation %s: anchor must occur once in %s (got %d):\n%s", mutation.name, e.file, n, e.from)
			}
			text = strings.Replace(text, e.from, e.to, 1)
		}
		mutant := filepath.Join(scratch, "mutation_"+file)
		if err := os.WriteFile(mutant, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[src] = mutant
	}
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

func frameLatchRun(t *testing.T, overlay, run string) ([]byte, error) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", run, "./internal/caps/cdp")
	command.Dir = root
	return command.CombinedOutput()
}

func TestCDPFrameLatchEdges(t *testing.T) {
	output, err := frameLatchRun(t, frameLatchOverlay(t, frameLatchMutation{}), "^TestFrameLatch")
	if err != nil {
		t.Fatalf("frame latch edges: %v\n%s", err, output)
	}
	for _, row := range frameLatchRows {
		if !strings.Contains(string(output), "row "+row+" PASS") {
			t.Fatalf("row %s did not report PASS\n%s", row, output)
		}
		t.Logf("row %s PASS", row)
	}
}

func frameLatchMutations() []frameLatchMutation {
	clearOnProbe := frameLatchEdit{
		file: "extensionframe.go",
		from: "\t\tg.rootDocumentLocked(page, tree.FrameTree.Frame.LoaderID)\n",
		to:   "\t\tg.commitRootDocumentLocked(page, tree.FrameTree.Frame.LoaderID)\n",
	}
	clearAll := frameLatchEdit{
		file: "childdoc.go",
		from: "\t\tif wc != nil && wc.markLatch {\n\t\t\twc.protectedFrame = \"\"\n",
		to:   "\t\tif wc != nil {\n\t\t\twc.protectedFrame = \"\"\n",
	}
	return []frameLatchMutation{
		{
			name: "clear-on-probe", run: "^TestFrameLatchMarkSurvivesShownProbe$", failing: "TestFrameLatchMarkSurvivesShownProbe",
			all:   []string{"TestFrameLatchMarkKeepsOlderOwner", "TestFrameLatchMarkShownProbeRefuses", "TestFrameLatchMarkSurvivesSameDocumentCommit", "TestFrameLatchMarkSurvivesShownProbe"},
			edits: []frameLatchEdit{clearOnProbe},
		},
		{
			name: "clear-all-latches", run: "^TestFrameLatchOlderSurvivesTopLevelCommit$", failing: "TestFrameLatchOlderSurvivesTopLevelCommit",
			all:   []string{"TestFrameLatchMarkKeepsOlderOwner", "TestFrameLatchMarkLeavesOlderValue", "TestFrameLatchOlderSurvivesTopLevelCommit", "TestFrameLatchOlderTakesOverMarked"},
			edits: []frameLatchEdit{clearAll},
		},
		{
			name: "never-clear-on-commit", run: "^TestFrameLatchMarkClearsOnTopLevelCommit$", failing: "TestFrameLatchMarkClearsOnTopLevelCommit",
			all: []string{"TestFrameLatchMarkClearsOnTopLevelCommit", "TestFrameLatchMarkKeepsOlderOwner"},
			edits: []frameLatchEdit{{
				file: "frameparents.go",
				from: "\t\t\t\tg.commitRootDocumentLocked(page, p.Frame.LoaderID)\n",
				to:   "\t\t\t\tg.rootDocumentLocked(page, p.Frame.LoaderID)\n",
			}},
		},
		{
			name: "clear-on-same-document", run: "^TestFrameLatchMarkSurvivesSameDocumentCommit$", failing: "TestFrameLatchMarkSurvivesSameDocumentCommit",
			all: []string{"TestFrameLatchMarkSurvivesSameDocumentCommit"},
			edits: []frameLatchEdit{{
				file: "frameownership.go",
				from: "\tsame := page.document != \"\" && page.document == loader\n",
				to:   "\tsame := false\n",
			}},
		},
		{
			name: "released-behaviour", run: "^TestFrameLatchShownProbeRefusesChild$", failing: "TestFrameLatchShownProbeRefusesChild",
			edits: []frameLatchEdit{clearOnProbe, clearAll},
		},
		{
			name: "mark-owns-older", run: "^TestFrameLatchMarkKeepsOlderOwner$", failing: "TestFrameLatchMarkKeepsOlderOwner",
			edits: []frameLatchEdit{{
				file: "childdoc.go",
				from: "\t\t\tif wc != nil && wc.frame == frameID && wc.protectedFrame == \"\" {\n",
				to:   "\t\t\tif wc != nil && wc.frame == frameID {\n",
			}},
		},
		{
			name: "mark-overwrites-older", run: "^TestFrameLatchMarkLeavesOlderValue$", failing: "TestFrameLatchMarkLeavesOlderValue",
			edits: []frameLatchEdit{{
				file: "childdoc.go",
				from: "\t\t\tif wc != nil && wc.frame == frameID && wc.protectedFrame == \"\" {\n\t\t\t\twc.protectedFrame = frameID\n\t\t\t\twc.markLatch = true\n",
				to:   "\t\t\tif wc != nil && wc.frame == frameID {\n\t\t\t\twc.markLatch = wc.markLatch || wc.protectedFrame == \"\"\n\t\t\t\twc.protectedFrame = frameID\n",
			}},
		},
		{
			name: "mark-owns-nothing", run: "^TestFrameLatchMarkClearsOnTopLevelCommit$", failing: "TestFrameLatchMarkClearsOnTopLevelCommit",
			edits: []frameLatchEdit{{
				file: "childdoc.go",
				from: "\t\t\t\twc.protectedFrame = frameID\n\t\t\t\twc.markLatch = true\n",
				to:   "\t\t\t\twc.protectedFrame = frameID\n",
			}},
		},
		{
			name: "hidden-context-keeps-mark-owner", run: "^TestFrameLatchOlderTakesOverMarked$/^context-created$", failing: "TestFrameLatchOlderTakesOverMarked/context-created",
			edits: []frameLatchEdit{{
				file: "worlds.go",
				from: "\t\t\twc.protectedFrame = frame\n\t\t\twc.markLatch = false\n",
				to:   "\t\t\twc.protectedFrame = frame\n",
			}},
		},
	}
}

var frameLatchFailLine = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+) `)

func frameLatchTopFailures(output []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range frameLatchFailLine.FindAllStringSubmatch(string(output), -1) {
		top, _, _ := strings.Cut(m[1], "/")
		if !seen[top] {
			seen[top] = true
			out = append(out, top)
		}
	}
	slices.Sort(out)
	return out
}

func TestCDPFrameLatchMutations(t *testing.T) {
	for _, row := range frameLatchMutations() {
		t.Run(row.name, func(t *testing.T) {
			overlay := frameLatchOverlay(t, row)
			output, err := frameLatchRun(t, overlay, row.run)
			if err == nil {
				t.Fatalf("mutant %s must fail %s\n%s", row.name, row.failing, output)
			}
			if !strings.Contains(string(output), "--- FAIL: "+row.failing+" ") {
				t.Fatalf("mutant %s did not fail %s\n%s", row.name, row.failing, output)
			}
			top, _, _ := strings.Cut(row.failing, "/")
			for _, m := range frameLatchFailLine.FindAllStringSubmatch(string(output), -1) {
				if m[1] != row.failing && m[1] != top && !strings.HasPrefix(m[1], row.failing+"/") {
					t.Fatalf("mutant %s failed another row %s\n%s", row.name, m[1], output)
				}
			}
			if row.all != nil {
				full, _ := frameLatchRun(t, overlay, "^TestFrameLatch")
				if got := frameLatchTopFailures(full); !slices.Equal(got, row.all) {
					t.Fatalf("mutant %s over every row failed %v, want %v\n%s", row.name, got, row.all, full)
				}
				t.Logf("mutant %s over every row fails %v", row.name, row.all)
			}
			t.Logf("mutant %s fails %s", row.name, row.failing)
		})
	}
}
