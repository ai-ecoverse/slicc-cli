package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type cleanupEdit struct{ file, from, to string }

type cleanupMutation struct {
	name  string
	named []string
	edits []cleanupEdit
}

func leaseCleanupMutations() []cleanupMutation {
	browser := func(from, to string) cleanupEdit { return cleanupEdit{"internal/lease/browser.go", from, to} }
	broker := func(from, to string) cleanupEdit { return cleanupEdit{"internal/lease/broker.go", from, to} }
	guard := func(from, to string) cleanupEdit { return cleanupEdit{"internal/caps/cdp/targetguard.go", from, to} }
	closeScope := "\t\t\tbs.closeTab(t, true)\n"
	promptClose := "if prompt {\n" + closeScope
	deferredClose := "\t\t\tif knownBad {\n\t" + closeScope + "\t\t\t}\n"
	closeCall := "\t\t_ = bs.c.Call(ctx, \"\", \"Target.closeTarget\", map[string]any{\"targetId\": target}, nil)"
	createClose := "\t\t\tbs.closeTab(t, false)"
	clearTarget := "\tt.target = \"\"\n\tdelete(bs.tabs, t.session)"
	detachCondition := "if t := bs.tabs[p.SessionID]; t != nil && !t.lost && !t.closing {"
	return []cleanupMutation{
		{"ScopeCloseRemoved", []string{"R1CreateRedirectForeign", "R1CreateRedirectErrorPage", "R2AssignRedirectForeign", "R2AssignRedirectErrorPage"}, []cleanupEdit{browser(promptClose, "if prompt {\n"), browser(deferredClose, "")}},
		{"CloseWrongTarget", []string{"R5RecordedTargetOnly"}, []cleanupEdit{browser(closeCall, "\t\tif targets, err := bs.c.Targets(ctx); err == nil && len(targets) != 0 { target = targets[0].TargetID }\n"+closeCall)}},
		{"CreateFailureLeaks", []string{"R3CreateFailure-Target.attachToTarget", "R3CreateFailure-Page.enable", "R3CreateFailure-Page.navigate"}, []cleanupEdit{browser(createClose, "\t\t\tbs.mu.Lock(); t.lost = true; bs.mu.Unlock()")}},
		{"DuplicateCloseRepeated", []string{"R7ExpiryDoesNotRepeatClose"}, []cleanupEdit{browser(clearTarget, "\tdelete(bs.tabs, t.session)")}},
		{"CloseOnTimeout", []string{"R6UnknownTimeoutKeepsTab"}, []cleanupEdit{browser(deferredClose, closeScope)}},
		{"CloseAfterReplyOnly", []string{"R8ConcurrentActionBusy"}, []cleanupEdit{browser(promptClose, "if prompt {\n\t\t\ttime.AfterFunc(time.Second, func() { bs.closeTab(t, true) })\n")}},
		{"KeepTabUsable", []string{"R1CreateRedirectForeign"}, []cleanupEdit{browser("t.lost, t.cut = true, cut", "t.lost, t.cut = !cut, false")}},
		{"TargetFromEvent", []string{"R5RecordedTargetOnly"}, []cleanupEdit{browser("if json.Unmarshal(e.Params, &p) != nil || p.Frame.ID != t.target {\n\t\t\treturn\n\t\t}", "if json.Unmarshal(e.Params, &p) != nil { return }; t.target = p.Frame.ID")}},
		{"RetryCloseOnRefusal", []string{"R4CloseFailure-error", "R9KeptVault/cleanup"}, []cleanupEdit{browser(closeCall, "\t\tif bs.c.Call(ctx, \"\", \"Target.closeTarget\", map[string]any{\"targetId\": target}, nil) != nil {\n"+closeCall+"\n\t\t}")}},
		{"GuardBypassClose", []string{"R9KeptSevenOS/cleanup", "R9KeptVault/cleanup"}, []cleanupEdit{guard("if g.keptCloseLocked(id) {", "if false && g.keptCloseLocked(id) {")}},
		{"DetachTriggerRemoved", []string{"R9OrdinaryProtected/cleanup", "R10UserCloseDetachFirst"}, []cleanupEdit{browser(detachCondition, "if t := bs.tabs[p.SessionID]; t != nil && false {")}},
		{"DetachRetriggersOnOwnClose", []string{"R13OwnDiscard"}, []cleanupEdit{browser(clearTarget, ""), browser(detachCondition, "if t := bs.tabs[p.SessionID]; t != nil && ((!t.lost && !t.closing) || t.closing) {")}},
		{"TriggerOnCrash", []string{"R11CrashAlone"}, []cleanupEdit{browser("if e.Method == \"Target.detachedFromTarget\" && e.SessionID == \"\" {", "if e.Method == \"Inspector.targetCrashed\" { if t := bs.tabs[e.SessionID]; t != nil { bs.closeTabLocked(t, true) }; return }; if e.Method == \"Target.detachedFromTarget\" && e.SessionID == \"\" {")}},
		{"CloseNotRecordedTarget", []string{"R10UserCloseDestroyFirst"}, []cleanupEdit{browser("target := t.target\n\tif target == \"\" {", "target := t.session\n\tif target == \"\" {")}},
		{"HolderCodeChanges", []string{"R9KeptSevenOS/cleanup", "R10UserCloseDestroyFirst"}, []cleanupEdit{browser("if cut {\n\t\treturn nil, stamp{}, denied", "if cut {\n\t\treturn nil, stamp{}, retired")}},
		{"SynchronousCloseInCallback", []string{"R14CallbackDoesNotBlock"}, []cleanupEdit{browser("bs.cleanup.Go(func() {\n\t\tctx, cancel", "(func() {\n\t\tctx, cancel"), browser(closeCall+"\n\t})", closeCall+"\n\t})()")}},
		{"CleanupAfterShutdown", []string{"R16ShutdownRejectsLateCleanup"}, []cleanupEdit{browser("\tif bs.stopped {\n\t\treturn\n\t}\n", "")}},
		{"EarlyReplyOnErrorPage", []string{"R1CreateRedirectErrorPage", "R2AssignRedirectErrorPage"}, []cleanupEdit{browser(promptClose, "if prompt || now.f.UnreachableURL != \"\" {\n"+closeScope)}},
		{"CloseAtFirstBadObservation", []string{"R20ErrorPageRecoversToAllowed", "R21SameURLOpaqueReloadRecovers"}, []cleanupEdit{browser("return nil, stamp{}, denied\n\t\t}\n\t\tselect {\n\t\tcase <-ctx.Done():\n"+deferredClose, "return nil, stamp{}, denied\n\t\t}\n\t\tif bad { bs.closeTab(t,true); <-ctx.Done(); return nil, stamp{}, denied }\n\t\tselect {\n\t\tcase <-ctx.Done():\n"+deferredClose)}},
		{"KnownBadNotClearedOnAllowedRead", []string{"R23RestoredOriginalAllowedDocumentKeepsTab"}, []cleanupEdit{browser("\t\tif allowed {\n\t\t\tknownBad = false\n\t\t}\n", "")}},
		{"DeferredCleanupDropped", []string{"R1CreateRedirectErrorPage", "R2AssignRedirectErrorPage", "R18SameURLNewOpaqueDocument"}, []cleanupEdit{browser(deferredClose, "")}},
		{"LoaderChangeIgnored", []string{"R18SameURLNewOpaqueDocument"}, []cleanupEdit{browser("return prompt || st.f.LoaderID != previous.f.LoaderID, prompt", "return prompt, prompt")}},
		{"UnboundedCleanupWait", []string{"R19ShutdownAbortsStalledCloseWrite"}, []cleanupEdit{broker("select {\n\tcase <-done:\n\tcase <-time.After(3 * time.Second):\n\t\tb.bs.stopCleanup()\n\t}", "<-done")}},
		{"ChildDetachTriggersParentClose", []string{"R15bChildDetachDoesNotCloseParent"}, []cleanupEdit{browser(detachCondition, "if t := bs.tabs[p.SessionID]; t == nil { for _, parent := range bs.tabs { bs.closeTabLocked(parent, true); break } } else if !t.lost && !t.closing {")}},
	}
}

var leaseCleanupTraceEdits = []cleanupEdit{
	{"internal/lease/browser.go", "knownBad = knownBad || bad", "knownBad = knownBad || bad\n\t\tleaseCleanupNavigationObserved(t, bad, prompt, allowed)"},
	{"internal/caps/cdp/local.go", "_, e := t.c.Write(pageFrame(b))", "leaseCleanupRecord(\"send\", b)\n\t_, e := t.c.Write(pageFrame(b))"},
	{"internal/caps/cdp/local.go", "return websocket.MessageText, b, e", "if e == nil { leaseCleanupRecord(\"receive\", b) }; return websocket.MessageText, b, e"},
}

func leaseCleanupSources(t *testing.T, root string, edits []cleanupEdit) map[string]string {
	t.Helper()
	sources := map[string]string{}
	for _, e := range edits {
		src, ok := sources[e.file]
		if !ok {
			b, err := os.ReadFile(filepath.Join(root, e.file))
			if err != nil {
				t.Fatal(err)
			}
			src = string(b)
		}
		if n := strings.Count(src, e.from); n != 1 {
			t.Fatalf("stale anchor in %s: got %d matches, want one\n%s", e.file, n, e.from)
		}
		sources[e.file] = strings.Replace(src, e.from, e.to, 1)
	}
	return sources
}

func leaseCleanupRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func leaseCleanupCache(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func leaseCleanupRun(t *testing.T, root, cache, run string, env []string, edits []cleanupEdit) ([]byte, error) {
	t.Helper()
	scratch := t.TempDir()
	data := filepath.Join(root, "test", "integration", "testdata")
	replacements := map[string]string{
		filepath.Join(root, "internal/lease/cleanup_rows_test.go"):    filepath.Join(data, "leasecleanup/rows.go"),
		filepath.Join(root, "internal/lease/cleanup_guard_test.go"):   filepath.Join(data, "leasecleanup/guard.go"),
		filepath.Join(root, "internal/lease/cleanup_live_test.go"):    filepath.Join(data, "leasecleanup/live.go"),
		filepath.Join(root, "internal/caps/cdp/cleanup_transport.go"): filepath.Join(data, "leasecleanup/transport.go"),
		filepath.Join(root, "internal/lease/canonical_test.go"):       filepath.Join(data, "lease/canonical.go"),
	}
	for file, src := range leaseCleanupSources(t, root, append(slices.Clone(leaseCleanupTraceEdits), edits...)) {
		parts := strings.Split(file, "/")
		dst := filepath.Join(scratch, strings.Join(parts, "_"))
		if err := os.WriteFile(dst, []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[filepath.Join(root, file)] = dst
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-overlay", hrOverlay(t, scratch, replacements), "-run", run, "./internal/lease")
	cmd.Dir = root
	cmd.Env = append(cmd.Environ(), "GOCACHE="+cache)
	cmd.Env = append(cmd.Env, env...)
	return cmd.CombinedOutput()
}

type cleanupLeaf struct {
	outcome string
	phase   bool
}

func leaseCleanupLeaves(t *testing.T, output []byte, err error) map[string]cleanupLeaf {
	t.Helper()
	observed := map[string]cleanupLeaf{}
	parents := map[string]bool{}
	var preceding string
	s := bufio.NewScanner(bytes.NewReader(output))
	s.Buffer(make([]byte, 4096), 2<<20)
	for s.Scan() {
		var event struct{ Action, Test, Output string }
		if json.Unmarshal(s.Bytes(), &event) != nil {
			t.Fatalf("invalid child output: %s\n%s", s.Bytes(), output)
		}
		if strings.Contains(event.Output, "panic:") || strings.Contains(event.Output, "DATA RACE") {
			t.Fatalf("unclean child process\n%s", output)
		}
		if event.Test == "" {
			continue
		}
		if parent, _, ok := strings.Cut(event.Test, "/"); !ok || parent == "" {
			continue
		}
		if pos := strings.LastIndexByte(event.Test, '/'); pos >= 0 {
			parents[event.Test[:pos]] = true
		}
		row := observed[event.Test]
		if event.Action == "output" && strings.Contains(event.Output, "cleanup phase reached") {
			row.phase = true
		}
		switch event.Action {
		case "pass", "fail", "skip":
			if row.outcome != "" {
				t.Fatalf("duplicate outcome for %s", event.Test)
			}
			row.outcome = event.Action
			preceding = event.Test
		}
		observed[event.Test] = row
	}
	if scanErr := s.Err(); scanErr != nil {
		t.Fatal(scanErr)
	}
	var reds int
	for name, row := range observed {
		if parents[name] {
			delete(observed, name)
			continue
		}
		switch row.outcome {
		case "pass":
		case "fail":
			if !row.phase {
				t.Fatalf("leaf failed before cleanup phase: %s\n%s", name, output)
			}
			reds++
		default:
			t.Fatalf("invalid leaf %s: %+v\n%s", name, row, output)
		}
	}
	var exit *exec.ExitError
	if len(observed) == 0 || (reds == 0 && err != nil) || (reds != 0 && (!errors.As(err, &exit) || exit.ExitCode() != 1)) {
		t.Fatalf("unclean child result %v, leaves=%d, failed=%d, last=%s\n%s", err, len(observed), reds, preceding, output)
	}
	return observed
}

const cleanupUnitRun = "^TestLeaseCleanup(Rows|RealGuard)$"

const leaseCleanupRequiredLeaves = `
TestLeaseCleanupRows/R1CreateRedirectForeign
TestLeaseCleanupRows/R1CreateRedirectErrorPage
TestLeaseCleanupRows/R2AssignRedirectForeign
TestLeaseCleanupRows/R2AssignRedirectErrorPage
TestLeaseCleanupRows/R3CreateFailure-Target.attachToTarget
TestLeaseCleanupRows/R3CreateFailure-Page.enable
TestLeaseCleanupRows/R3CreateFailure-Runtime.enable
TestLeaseCleanupRows/R3CreateFailure-Page.navigate
TestLeaseCleanupRows/R4CloseFailure-error
TestLeaseCleanupRows/R4CloseFailure-gone
TestLeaseCleanupRows/R4CloseFailure-drop
TestLeaseCleanupRows/R5RecordedTargetOnly
TestLeaseCleanupRows/R6UnknownTimeoutKeepsTab
TestLeaseCleanupRows/R7ExpiryDoesNotRepeatClose
TestLeaseCleanupRows/R8ConcurrentActionBusy
TestLeaseCleanupRealGuard/R9OrdinaryProtected/baseline
TestLeaseCleanupRealGuard/R9OrdinaryProtected/cleanup
TestLeaseCleanupRealGuard/R9KeptSevenOS/baseline
TestLeaseCleanupRealGuard/R9KeptSevenOS/cleanup
TestLeaseCleanupRealGuard/R9KeptVault/baseline
TestLeaseCleanupRealGuard/R9KeptVault/cleanup
TestLeaseCleanupRows/R10UserCloseDetachFirst
TestLeaseCleanupRows/R10UserCloseDestroyFirst
TestLeaseCleanupRows/R10UserCloseCloseBeforeDestroy
TestLeaseCleanupRows/R11CrashAlone
TestLeaseCleanupRows/R12AllowedNavigation
TestLeaseCleanupRows/R13OwnDiscard
TestLeaseCleanupRows/R14CallbackDoesNotBlock
TestLeaseCleanupRows/R15bChildDetachDoesNotCloseParent
TestLeaseCleanupRows/R16ShutdownRejectsLateCleanup
TestLeaseCleanupRows/R17CutNavigationKeepsTimeout
TestLeaseCleanupRows/R18SameURLNewOpaqueDocument
TestLeaseCleanupRows/R19ShutdownAbortsStalledCloseWrite
TestLeaseCleanupRows/R20ErrorPageRecoversToAllowed
TestLeaseCleanupRows/R21SameURLOpaqueReloadRecovers
TestLeaseCleanupRows/R22ErrorPageThenForeignRefusesPromptly
TestLeaseCleanupRows/R23RestoredOriginalAllowedDocumentKeepsTab
`

func leaseCleanupInventory(t *testing.T, want []string, got map[string]cleanupLeaf) {
	t.Helper()
	expected := make(map[string]bool, len(want))
	for _, name := range want {
		if expected[name] {
			t.Errorf("duplicate required leaf %q", name)
		}
		expected[name] = true
		if _, ok := got[name]; !ok {
			t.Errorf("missing required leaf %q", name)
		}
	}
	for name := range got {
		if !expected[name] {
			t.Errorf("unexpected leaf %q", name)
		}
	}
}

func TestLeaseCleanupRowsAndMutations(t *testing.T) {
	root, cache := leaseCleanupRoot(t), leaseCleanupCache(t)
	mutations := leaseCleanupMutations()
	t.Run("AnchorPrecheck", func(t *testing.T) {
		leaseCleanupSources(t, root, leaseCleanupTraceEdits)
		for _, m := range mutations {
			t.Run(m.name, func(t *testing.T) { leaseCleanupSources(t, root, m.edits) })
		}
	})
	if t.Failed() {
		return
	}
	out, err := leaseCleanupRun(t, root, cache, cleanupUnitRun, nil, nil)
	base := leaseCleanupLeaves(t, out, err)
	if err != nil {
		t.Fatalf("baseline %v\n%s", err, out)
	}
	if !t.Run("InventoryPrecheck", func(t *testing.T) { leaseCleanupInventory(t, strings.Fields(leaseCleanupRequiredLeaves), base) }) {
		return
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			out, err := leaseCleanupRun(t, root, cache, cleanupUnitRun, nil, m.edits)
			got := leaseCleanupLeaves(t, out, err)
			if len(got) != len(base) {
				t.Fatalf("leaves: got %d want %d\n%s", len(got), len(base), out)
			}
			var red, named, incidental []string
			for path := range base {
				row, ok := got[path]
				if !ok {
					t.Fatalf("missing leaf %s\n%s", path, out)
				}
				if row.outcome != "fail" {
					continue
				}
				_, name, _ := strings.Cut(path, "/")
				red = append(red, name)
				if slices.Contains(m.named, name) {
					named = append(named, name)
				} else {
					incidental = append(incidental, name)
				}
			}
			for _, names := range [][]string{red, named, incidental} {
				slices.Sort(names)
			}
			t.Logf("red=%v named=%v incidental=%v", red, named, incidental)
			for _, name := range m.named {
				if !slices.Contains(named, name) {
					t.Errorf("named row %s survived\n%s", name, out)
				}
			}
		})
	}
}

func TestLeaseCleanupRedirectToProtectedHostViaErrorPageNotDetach(t *testing.T) {
	runLeaseCleanupChrome(t, "redirect")
}

func TestLeaseCleanupProtectedChildFrameDoesNotCloseTheParent(t *testing.T) {
	runLeaseCleanupChrome(t, "child")
}
