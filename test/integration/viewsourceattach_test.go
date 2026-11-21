package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func viewSourceAttachRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func viewSourceAttachOverlayRun(t *testing.T, replacements map[string]string, run string, env []string, wantFail bool) {
	t.Helper()
	root := viewSourceAttachRoot(t)
	scratch := t.TempDir()
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", run, "./internal/caps/cdp")
	command.Dir = root
	if len(env) > 0 {
		command.Env = append(os.Environ(), env...)
	}
	output, err := command.CombinedOutput()
	if wantFail {
		if err == nil {
			t.Fatalf("mutant %s must fail the named row\n%s", run, output)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s: %v\n%s", run, err, output)
	}
}

func viewSourceAttachRegression(root string) map[string]string {
	return map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
	}
}

func TestCDPViewSourceAttachRace(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestViewSourceAttach", nil, false)
}

func TestCDPViewSourceAttachMutationConfirmDisabled(t *testing.T) {
	root := viewSourceAttachRoot(t)
	replacements := viewSourceAttachRegression(root)
	replacements[filepath.Join(root, "internal/caps/cdp/documentconfirm.go")] = filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_documentconfirm.go")
	replacements[filepath.Join(root, "internal/caps/cdp/documentidentity.go")] = filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_documentidentity.go")
	viewSourceAttachOverlayRun(t, replacements, "^TestViewSourceAttachMutationProbe$", []string{"SLICC_DOC_CONFIRM_MUTATION=1"}, false)
}

func TestCDPDocConfirmObserveNeverChangesVerdict(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmObserveNeverChangesVerdict$", nil, false)
}

func TestCDPDocConfirmObserveMustNotClaimNavguardSession(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmObserveMustNotClaimNavguardSession$", nil, false)
}

func TestCDPDocConfirmObserveMustNotEmitProofDetach(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmObserveMustNotEmitProofDetach$", nil, false)
}

func TestCDPDocConfirmBlankProtectedLatch(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmBlankProtectedLatch$", nil, false)
}

func TestCDPDocConfirmBlankProtectedLatchEnforceOff(t *testing.T) {
	root := viewSourceAttachRoot(t)
	replacements := viewSourceAttachRegression(root)
	replacements[filepath.Join(root, "internal/caps/cdp/documentconfirm_observe_switches.go")] = filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_enforce_off.go")
	viewSourceAttachOverlayRun(t, replacements, "^TestDocConfirmBlankProtectedLatch$", nil, true)
}

func TestCDPDocConfirmNewTabPageNeverRefuses(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmNewTabPageNeverRefuses$", nil, false)
}

func TestCDPDocConfirmEmptyHoldBoundFallthrough(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmEmptyHoldBoundFallthrough$", nil, false)
}

func TestCDPDocConfirmAnnounceClassMapper(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmAnnounceClassMapper$", nil, false)
}

func TestCDPDocConfirmExplicitPathObserve(t *testing.T) {
	root := viewSourceAttachRoot(t)
	replacements := viewSourceAttachRegression(root)
	replacements[filepath.Join(root, "internal/caps/cdp/documentconfirm_observe_switches.go")] = filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_observe_diag_on.go")
	viewSourceAttachOverlayRun(t, replacements, "^TestDocConfirmExplicitPathObserve$", []string{"SLICC_DOC_CONFIRM_DIAG=1"}, false)
}

func TestCDPDocConfirmObserveTabAttachPopupHoldResume(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmObserveTabAttachPopupHoldResume$", nil, false)
}

func TestCDPDocConfirmObserveStatePrunedOnDestroy(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmObserveStatePrunedOnDestroy$", nil, false)
}

func TestCDPDocConfirmObserveStatePruneMutant(t *testing.T) {
	root := viewSourceAttachRoot(t)
	replacements := viewSourceAttachRegression(root)
	replacements[filepath.Join(root, "internal/caps/cdp/documentconfirm_observe_prune.go")] = filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_observe_noprune.go")
	viewSourceAttachOverlayRun(t, replacements, "^TestDocConfirmObserveStatePruneMutant$", []string{"SLICC_DOC_CONFIRM_OBSERVE_NOPRUNE=1"}, false)
}

func TestCDPDocConfirmLatchIndependentOfDiag(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmLatchIndependentOfDiag$", nil, false)
}

func TestCDPDocConfirmLatchGatedOnDiagMutant(t *testing.T) {
	root := viewSourceAttachRoot(t)
	replacements := viewSourceAttachRegression(root)
	replacements[filepath.Join(root, "internal/caps/cdp/documentconfirm_observe_switches.go")] = filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_observe_latch_on_diag.go")
	viewSourceAttachOverlayRun(t, replacements, "^TestDocConfirmLatchGatedOnDiagMutant$", []string{"SLICC_DOC_CONFIRM_LATCH_ON_DIAG=1"}, false)
}

func TestCDPDocConfirmLaterProtectedCommitUsesExistingPath(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmLaterProtectedCommitUsesExistingPath$", nil, false)
}

func TestCDPDocConfirmNewtabPathSubstringStillLatches(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmNewtabPathSubstringStillLatches$", nil, false)
}

func TestCDPDocConfirmBlankProtectedCommitBeforeAnnounce(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmBlankProtectedCommitBeforeAnnounce$", nil, false)
}

func TestCDPDocConfirmOrdinaryAnnounceProtectedTwin(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmOrdinaryAnnounceProtectedTwin$", nil, false)
}

func TestCDPDocConfirmUnparseableOriginFailClosedLatch(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmUnparseableOriginFailClosedLatch$", nil, false)
}

func TestCDPDocConfirmExpireHoldWithProtectedEvidenceLatches(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmExpireHoldWithProtectedEvidenceLatches$", nil, false)
}

func TestCDPDocConfirmNoteAfterSeveralCommitsUsesFinishSnapshot(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmNoteAfterSeveralCommitsUsesFinishSnapshot$", nil, false)
}

func TestCDPDocConfirmObserveNoUseAfterPrune(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmObserveNoUseAfterPrune$", nil, false)
}

func TestCDPDocConfirmObserveLiveTargetStillTracks(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmObserveLiveTargetStillTracks$", nil, false)
}

func TestCDPDocConfirmObserveNoteThenDestroyClears(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachOverlayRun(t, viewSourceAttachRegression(root), "^TestDocConfirmObserveNoteThenDestroyClears$", nil, false)
}

func TestCDPDocConfirmObserveUseAfterPruneMutant(t *testing.T) {
	root := viewSourceAttachRoot(t)
	replacements := viewSourceAttachRegression(root)
	replacements[filepath.Join(root, "internal/caps/cdp/documentconfirm_observe_live.go")] = filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_observe_live_gone.go")
	viewSourceAttachOverlayRun(t, replacements, "^TestDocConfirmObserveUseAfterPruneMutant$", []string{"SLICC_DOC_CONFIRM_OBSERVE_LIVE_GONE=1"}, false)
}
