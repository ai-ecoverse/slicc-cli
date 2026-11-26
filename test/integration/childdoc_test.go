package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type childDocEdit struct {
	file, from, to string
}

type childDocMutation struct {
	name, switches, run, failing string
	legacy                       bool
	hook                         bool
	edits                        []childDocEdit
}

func childDocHookEdits() []childDocEdit {
	return []childDocEdit{
		{
			file: "guard_browser.go",
			from: "\t\tif method != \"\" {\n\t\t\tout, drop, st = applyChildDocOnSnapshot(data, method, snap, m.ID)\n\t\t}\n",
			to:   "\t\tif method != \"\" {\n\t\t\tchildDocBeforeWalkHook()\n\t\t\tout, drop, st = applyChildDocOnSnapshot(data, method, snap, m.ID)\n\t\t}\n",
		},
		{
			file: "guard_browser.go",
			from: "\tg.mu.Unlock()\n\tout, drop, st = applyChildDocOnSnapshot(data, m.Method, snap, nil)\n\treturn \"\", out, drop, st, true\n",
			to:   "\tg.mu.Unlock()\n\tchildDocBeforeWalkHook()\n\tout, drop, st = applyChildDocOnSnapshot(data, m.Method, snap, nil)\n\treturn \"\", out, drop, st, true\n",
		},
	}
}

func childDocOverlay(t *testing.T, mutation childDocMutation) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "test/integration/testdata/childdoc")
	scratch := t.TempDir()
	switches := filepath.Join(fixture, "switches_ok.go")
	if mutation.switches != "" {
		switches = filepath.Join(fixture, mutation.switches)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/childdoc_edges_test.go"): filepath.Join(fixture, "regression.go"),
		filepath.Join(root, "internal/caps/cdp/childdoc_switches.go"):   switches,
	}
	if mutation.legacy {
		replacements[filepath.Join(root, "internal/caps/cdp/childdoc.go")] = filepath.Join(fixture, "childdoc_legacy.go")
	}
	edits := append([]childDocEdit{}, mutation.edits...)
	if mutation.hook {
		edits = append(childDocHookEdits(), edits...)
	}
	byFile := map[string][]childDocEdit{}
	for _, e := range edits {
		byFile[e.file] = append(byFile[e.file], e)
	}
	for file, fileEdits := range byFile {
		src := filepath.Join(root, "internal/caps/cdp", file)
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, e := range fileEdits {
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

func childDocRun(t *testing.T, overlay, run string, race bool) (output []byte, err error) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 900*time.Second)
	defer cancel()
	args := []string{"test", "-count=1", "-timeout", "840s", "-overlay", overlay, "-run", run, "./internal/caps/cdp"}
	if race {
		args = append([]string{"test", "-race", "-v"}, args[1:]...)
	}
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = root
	command.Env = os.Environ()
	if race {
		command.Env = append(command.Env, "CGO_ENABLED=1")
	}
	return command.CombinedOutput()
}

var childDocWalkRaceOK = regexp.MustCompile(`(?m)^ok\s+github\.com/ai-ecoverse/slicc-cli/internal/caps/cdp\s`)

func childDocWalkRaceCheck(step string, output []byte, err error) error {
	out := string(output)
	switch {
	case strings.Contains(out, "cgo: C compiler"), strings.Contains(out, "-race requires cgo"), strings.Contains(out, "C compiler \"gcc\" not found"), strings.Contains(out, "loadinternal: cannot find runtime/cgo"):
		return fmt.Errorf("%s: cgo/gcc required for -race\n%s", step, out)
	case strings.Contains(out, "no tests to run"), strings.Contains(out, "[no tests to run]"):
		return fmt.Errorf("%s: subprocess ran no tests\n%s", step, out)
	case err != nil:
		return fmt.Errorf("%s: %w\n%s", step, err, out)
	case strings.Count(out, "ci-proof childdoc-walk-race") < 1:
		return fmt.Errorf("%s: missing ci-proof walk-race marker (subprocess did not run the row)\n%s", step, out)
	case !childDocWalkRaceOK.Match(output):
		return fmt.Errorf("%s: missing go test ok line\n%s", step, out)
	}
	return nil
}

func TestCDPChildDocEdges(t *testing.T) {
	overlay := childDocOverlay(t, childDocMutation{hook: true})
	output, err := childDocRun(t, overlay, "^TestChildDoc", false)
	if err != nil {
		t.Fatalf("childdoc edges: %v\n%s", err, output)
	}
}

func childDocMutations() []childDocMutation {
	return []childDocMutation{
		{name: "strip-disabled", switches: "mutation_strip_disabled.go", run: "^TestChildDocStripProtectedMarker$", failing: "TestChildDocStripProtectedMarker"},
		{name: "shadow-skip", switches: "mutation_shadow_skip.go", run: "^TestChildDocShadowRootProtected$", failing: "TestChildDocShadowRootProtected"},
		{
			name: "tight-limits", run: "^TestChildDocBigOrdinaryTree$", failing: "TestChildDocBigOrdinaryTree",
			edits: []childDocEdit{{
				file: "childdoc.go",
				from: "\tfor len(stack) > 0 {\n\t\tjob := stack[len(stack)-1]\n\t\tstack = stack[:len(stack)-1]\n\t\tswitch t := job.v.(type) {\n\t\tcase map[string]any:\n\t\t\tc, r, more := stripChildDocMapStep(t, job.parentOrdinary, job.depth, job.carrier, st)\n",
				to:   "\tnodes := 0\n\tfor len(stack) > 0 {\n\t\tjob := stack[len(stack)-1]\n\t\tstack = stack[:len(stack)-1]\n\t\tif job.depth > 24 || nodes > 4096 {\n\t\t\treturn changed, true\n\t\t}\n\t\tswitch t := job.v.(type) {\n\t\tcase map[string]any:\n\t\t\tnodes++\n\t\t\tif nodes > 4096 {\n\t\t\t\treturn changed, true\n\t\t\t}\n\t\t\tc, r, more := stripChildDocMapStep(t, job.parentOrdinary, job.depth, job.carrier, st)\n",
			}},
		},
		{name: "unknown-shape-ok", switches: "mutation_unknown_shape_ok.go", run: "^TestChildDocUnknownShapeRefused$", failing: "TestChildDocUnknownShapeRefused"},
		{name: "snapshot-pass", switches: "mutation_snapshot_pass.go", run: "^TestChildDocCaptureSnapshotProtectedRefused$", failing: "TestChildDocCaptureSnapshotProtectedRefused"},
		{name: "get-snapshot-pass", switches: "mutation_get_snapshot_pass.go", run: "^TestChildDocGetSnapshotProtectedRefused$", failing: "TestChildDocGetSnapshotProtectedRefused"},
		{name: "imported-skip", switches: "mutation_imported_skip.go", run: "^TestChildDocImportedDocumentProtected$", failing: "TestChildDocImportedDocumentProtected"},
		{name: "events-skip", switches: "mutation_events_skip.go", run: "^TestChildDocSetChildNodesEvent$", failing: "TestChildDocSetChildNodesEvent"},
		{name: "child-inserted-skip", switches: "mutation_child_inserted_skip.go", run: "^TestChildDocChildNodeInsertedEvent$", failing: "TestChildDocChildNodeInsertedEvent"},
		{
			name: "classify-loaded-only", run: "^TestChildDocStripEmptyURL$", failing: "TestChildDocStripEmptyURL",
			edits: []childDocEdit{{
				file: "childdoc.go",
				from: "func childDocOrdinary(o *protect.Origins, documentURL, baseURL string, documentPresent, basePresent, parentOrdinary bool) bool {\n\treturn childDocOrdinaryBoth(o, documentURL, baseURL, documentPresent, basePresent, parentOrdinary)\n}",
				to:   "func childDocOrdinary(o *protect.Origins, documentURL, baseURL string, documentPresent, basePresent, parentOrdinary bool) bool {\n\tu := \"\"\n\tif documentPresent {\n\t\tu = documentURL\n\t}\n\tif u == \"\" && basePresent {\n\t\tu = baseURL\n\t}\n\tif u == \"\" {\n\t\treturn true\n\t}\n\treturn !o.Loaded(u)\n}",
			}},
		},
		{
			name: "both-fields-short", run: "^TestChildDocBothFieldsProtected$", failing: "TestChildDocBothFieldsProtected",
			edits: []childDocEdit{{
				file: "childdoc.go",
				from: "func childDocOrdinary(o *protect.Origins, documentURL, baseURL string, documentPresent, basePresent, parentOrdinary bool) bool {\n\treturn childDocOrdinaryBoth(o, documentURL, baseURL, documentPresent, basePresent, parentOrdinary)\n}",
				to:   "func childDocOrdinary(o *protect.Origins, documentURL, baseURL string, documentPresent, basePresent, parentOrdinary bool) bool {\n\tfor _, u := range []string{documentURL, baseURL} {\n\t\tif u != \"\" && protect.Inheriting(u) {\n\t\t\treturn parentOrdinary\n\t\t}\n\t}\n\tu := documentURL\n\tif u == \"\" {\n\t\tu = baseURL\n\t}\n\tif u == \"\" {\n\t\treturn false\n\t}\n\tif o.Loaded(documentURL) || o.Loaded(baseURL) || o.Loaded(u) {\n\t\treturn false\n\t}\n\tif protect.Unparseable(documentURL) || protect.Unparseable(baseURL) || protect.Unparseable(u) {\n\t\treturn false\n\t}\n\treturn true\n}",
			}},
		},
		{name: "distributed-skip", switches: "mutation_distributed_skip.go", run: "^TestChildDocDistributedNodesProtected$", failing: "TestChildDocDistributedNodesProtected"},
		{name: "template-pseudo-skip", switches: "mutation_template_pseudo_skip.go", run: "^TestChildDocTemplateContentProtected$", failing: "TestChildDocTemplateContentProtected"},
		{name: "string-index-lax", switches: "mutation_string_index_lax.go", run: "^TestChildDocCaptureSnapshotBadIndexRefused$", failing: "TestChildDocCaptureSnapshotBadIndexRefused"},
		{
			name: "exempt-by-key-anywhere", run: "^TestChildDocDeeperRootKeyDocumentStripped$", failing: "TestChildDocDeeperRootKeyDocumentStripped",
			edits: []childDocEdit{{
				file: "childdoc.go",
				from: "func childDocPathExempt(carrier childDocCarrier, key string) bool {\n\tswitch carrier {\n\tcase childDocCarrierResult:\n\t\treturn key == \"root\" || key == \"node\"\n\tcase childDocCarrierParams:\n\t\treturn key == \"node\"\n\tdefault:\n\t\treturn false\n\t}\n}",
				to:   "func childDocPathExempt(carrier childDocCarrier, key string) bool {\n\treturn key == \"root\" || key == \"node\"\n}",
			}},
		},
		{
			name: "ignore-non-wire-shapes", run: "^TestChildDocCaptureSnapshotIntURLsWithoutStringsRefused$", failing: "TestChildDocCaptureSnapshotIntURLsWithoutStringsRefused",
			edits: []childDocEdit{{
				file: "childdoc.go",
				from: "\t\thasStrings = true\n\t}\n\tfor _, item := range docs {\n",
				to:   "\t\thasStrings = true\n\t}\n\tif !hasStrings {\n\t\treturn false\n\t}\n\tfor _, item := range docs {\n",
			}},
		},
		{
			name: "capture-absent-url-ok", run: "^TestChildDocCaptureSnapshotAbsentURLFieldsRefused$", failing: "TestChildDocCaptureSnapshotAbsentURLFieldsRefused",
			edits: []childDocEdit{{
				file: "childdoc.go",
				from: "\t\tif docRefuse {\n\t\t\treturn childDocStringIndexStrict()\n\t\t}\n\t\tbaseURL, basePresent, baseRefuse := childDocResolveCaptureURL(stringsTable, hasStrings, doc[\"baseURL\"])\n\t\tif baseRefuse {\n\t\t\treturn childDocStringIndexStrict()\n\t\t}\n\t\tif !docRefuse && !baseRefuse && childDocExtensionURLs(o, docURL, docPresent, baseURL, basePresent) {\n",
				to:   "\t\tif docRefuse {\n\t\t\treturn childDocStringIndexStrict()\n\t\t}\n\t\tbaseURL, basePresent, baseRefuse := childDocResolveCaptureURL(stringsTable, hasStrings, doc[\"baseURL\"])\n\t\tif baseRefuse {\n\t\t\treturn childDocStringIndexStrict()\n\t\t}\n\t\tif !docPresent && !basePresent {\n\t\t\tcontinue\n\t\t}\n\t\tif !docRefuse && !baseRefuse && childDocExtensionURLs(o, docURL, docPresent, baseURL, basePresent) {\n",
			}},
		},
		{
			name: "capture-url-type-lax", run: "^TestChildDocCaptureSnapshotNonStringNonIntURLRefused$", failing: "TestChildDocCaptureSnapshotNonStringNonIntURLRefused",
			edits: []childDocEdit{{
				file: "childdoc.go",
				from: "\tn, ok := childDocIndex(raw)\n\tif !ok {\n\t\treturn \"\", false, true\n\t}\n",
				to:   "\tn, ok := childDocIndex(raw)\n\tif !ok {\n\t\treturn \"\", false, false\n\t}\n",
			}},
		},
		{name: "walk-all-methods", switches: "mutation_walk_all_methods.go", run: "^TestChildDocRuntimeEvaluateChildrenStringByteIdentical$", failing: "TestChildDocRuntimeEvaluateChildrenStringByteIdentical"},
		{name: "gate-by-event-only", switches: "mutation_gate_by_event_only.go", run: "^TestChildDocStripProtectedMarker$", failing: "TestChildDocStripProtectedMarker"},
		{name: "no-pending-methods", switches: "mutation_no_pending_methods.go", run: "^TestChildDocBusyRefusalSameKeyStillStrips$", failing: "TestChildDocBusyRefusalSameKeyStillStrips"},
		{name: "strip-after-hold", switches: "mutation_strip_after_hold.go", run: "^TestChildDocIdentityHoldDataAlreadyStripped$", failing: "TestChildDocIdentityHoldDataAlreadyStripped"},
		{name: "walk-at-writepage-only", switches: "mutation_walk_at_writepage_only.go", legacy: true, run: "^TestChildDocBusyRefusalSameKeyStillStrips$", failing: "TestChildDocBusyRefusalSameKeyStillStrips"},
		{
			name: "walk-under-lock", hook: true, run: "^TestChildDocMuFreeDuringWalk$", failing: "TestChildDocMuFreeDuringWalk",
			edits: []childDocEdit{{
				file: "guard_browser.go",
				from: "\t\tg.mu.Lock()\n\t\tmethod, snap, _ := g.peekChildDocReplyLocked(m)\n\t\tg.mu.Unlock()\n\t\tif method != \"\" {\n\t\t\tchildDocBeforeWalkHook()\n\t\t\tout, drop, st = applyChildDocOnSnapshot(data, method, snap, m.ID)\n\t\t}\n",
				to:   "\t\tg.mu.Lock()\n\t\tmethod, snap, _ := g.peekChildDocReplyLocked(m)\n\t\tif method != \"\" {\n\t\t\tchildDocBeforeWalkHook()\n\t\t\tout, drop, st = applyChildDocOnSnapshot(data, method, snap, m.ID)\n\t\t}\n\t\tg.mu.Unlock()\n",
			}},
		},
		{
			name: "event-walk-under-lock", hook: true, run: "^TestChildDocMuFreeDuringEventWalk$", failing: "TestChildDocMuFreeDuringEventWalk",
			edits: []childDocEdit{{
				file: "guard_browser.go",
				from: "\tg.mu.Lock()\n\tsnap := g.snapshotChildDocLocked(m.SessionID)\n\tg.mu.Unlock()\n\tchildDocBeforeWalkHook()\n\tout, drop, st = applyChildDocOnSnapshot(data, m.Method, snap, nil)\n\treturn \"\", out, drop, st, true\n",
				to:   "\tg.mu.Lock()\n\tsnap := g.snapshotChildDocLocked(m.SessionID)\n\tchildDocBeforeWalkHook()\n\tout, drop, st = applyChildDocOnSnapshot(data, m.Method, snap, nil)\n\tg.mu.Unlock()\n\treturn \"\", out, drop, st, true\n",
			}},
		},
		{
			name: "decode-fail-open", run: "^TestChildDocDecodeFailureWalkReplyRefused$", failing: "TestChildDocDecodeFailureWalkReplyRefused",
			edits: []childDocEdit{{
				file: "childdoc.go",
				from: "\tm, ok := decodeObject(data)\n\tif !ok {\n\t\tif id != nil {\n\t\t\treturn refusal(*id, snap.session, refusedTarget), false, nil\n\t\t}\n\t\treturn nil, true, nil\n\t}\n",
				to:   "\tm, ok := decodeObject(data)\n\tif !ok {\n\t\treturn data, false, nil\n\t}\n",
			}},
		},
	}
}

func TestCDPChildDocMutations(t *testing.T) {
	for _, row := range childDocMutations() {
		t.Run(row.name, func(t *testing.T) {
			overlay := childDocOverlay(t, row)
			output, err := childDocRun(t, overlay, row.run, false)
			if err == nil {
				t.Fatalf("mutant %s must fail %s\n%s", row.name, row.failing, output)
			}
			if !strings.Contains(string(output), "--- FAIL: "+row.failing+" ") {
				t.Fatalf("mutant %s did not fail %s\n%s", row.name, row.failing, output)
			}
		})
	}
}

func TestCDPChildDocWalkRace(t *testing.T) {
	overlay := childDocOverlay(t, childDocMutation{hook: true})
	output, err := childDocRun(t, overlay, "^TestChildDocWalkRace$", true)
	if check := childDocWalkRaceCheck("walk-race", output, err); check != nil {
		t.Fatal(check)
	}
}

func TestCDPChildDocWalkRaceMustRun(t *testing.T) {
	overlay := childDocOverlay(t, childDocMutation{name: "walk-race-must-run", hook: true})
	output, err := childDocRun(t, overlay, "^TestChildDocWalkRaceNeverMatches$", true)
	if childDocWalkRaceCheck("walk-race-must-run", output, err) == nil {
		t.Fatalf("must-run mutant: wrapper accepted a subprocess that did not run the row\n%s", output)
	}
}

func TestCDPChildDocWalkRaceMutant(t *testing.T) {
	overlay := childDocOverlay(t, childDocMutation{
		name: "walk-reads-guard",
		hook: true,
		run:  "^TestChildDocWalkRace$",
		edits: []childDocEdit{
			{
				file: "guard_browser.go",
				from: "\t\tif method != \"\" {\n\t\t\tchildDocBeforeWalkHook()\n\t\t\tout, drop, st = applyChildDocOnSnapshot(data, method, snap, m.ID)\n\t\t}\n",
				to:   "\t\tif method != \"\" {\n\t\t\tchildDocBeforeWalkHook()\n\t\t\t_ = g.sessionTargetLocked(snap.session)\n\t\t\tout, drop, st = applyChildDocOnSnapshot(data, method, snap, m.ID)\n\t\t}\n",
			},
			{
				file: "guard_browser.go",
				from: "\tchildDocBeforeWalkHook()\n\tout, drop, st = applyChildDocOnSnapshot(data, m.Method, snap, nil)\n\treturn \"\", out, drop, st, true\n",
				to:   "\tchildDocBeforeWalkHook()\n\t_ = g.sessionTargetLocked(snap.session)\n\tout, drop, st = applyChildDocOnSnapshot(data, m.Method, snap, nil)\n\treturn \"\", out, drop, st, true\n",
			},
		},
	})
	output, err := childDocRun(t, overlay, "^TestChildDocWalkRace$", true)
	if err == nil {
		t.Fatalf("walk-reads-guard mutant must fail under -race\n%s", output)
	}
	out := string(output)
	if !strings.Contains(out, "DATA RACE") {
		t.Fatalf("walk-reads-guard mutant must report DATA RACE\n%s", out)
	}
}
