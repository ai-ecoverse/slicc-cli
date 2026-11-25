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

func hrOverlay(t *testing.T, scratch string, replacements map[string]string) string {
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

func hrOverlayRun(t *testing.T, mutant, run string, wantFail bool) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/history_restore_edges_test.go"): filepath.Join(root, "test/integration/testdata/historyrestore/regression.go"),
	}
	if mutant != "" {
		replacements[filepath.Join(root, "internal/caps/cdp/history_restore_switches.go")] = filepath.Join(root, "test/integration/testdata/historyrestore", mutant)
	}
	overlay := hrOverlay(t, t.TempDir(), replacements)
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", run, "./internal/caps/cdp")
	command.Dir = root
	output, err := command.CombinedOutput()
	if wantFail {
		if err == nil {
			t.Fatalf("mutant %s must fail %s\n%s", mutant, run, output)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s: %v\n%s", run, err, output)
	}
}

func TestCDPHistoryRestoreEdges(t *testing.T) {
	hrOverlayRun(t, "", "^TestHistoryRestore", false)
}

func TestCDPHistoryRestoreMutants(t *testing.T) {
	for _, m := range []struct{ mutant, run string }{
		{"mutation_confirm_at_nav.go", "^TestHistoryRestoreChildren$/^ordinary/iframe/guard-commit-first$"},
		{"mutation_old_detach.go", "^TestHistoryRestoreChildren$/^ordinary/iframe/guard-commit-first$"},
		{"mutation_hold_removed.go", "^TestHistoryRestoreChildren$/^ordinary/iframe/attach-first$"},
		{"mutation_workers_excluded.go", "^TestHistoryRestoreChildren$/^ordinary/third-worker/attach-first$"},
		{"mutation_explicit_hold.go", "^TestHistoryRestoreExplicitAttach$/^before-restore-events$"},
		{"mutation_identity.go", "^TestHistoryRestoreIdentityChangeHides$"},
		{"mutation_identity.go", "^TestHistoryRestoreReadEmptyIdentityHides$"},
		{"mutation_deadline_forwards.go", "^TestHistoryRestoreDeadlineHides$"},
		{"mutation_before_confirm.go", "^TestHistoryRestoreLockedNeverRestores$"},
		{"mutation_every_tab_holds.go", "^TestHistoryRestoreOrdinaryTwin$"},
		{"mutation_empty_snap.go", "^TestHistoryRestoreEmptySnapHides$"},
		{"mutation_identity_from_attach.go", "^TestHistoryRestoreEmptyAttachSnapExplicitDelivers$"},
		{"mutation_identity_stamp_once.go", "^TestHistoryRestoreSecondReadRestampsIdentity$"},
		{"mutation_first_protected.go", "^TestHistoryRestoreFirstProtectedWithChildren$"},
		{"mutation_unresolved.go", "^TestHistoryRestoreUnresolvedOwnerLockedStream$"},
		{"mutation_unresolved_to_locked.go", "^TestHistoryRestoreUnresolvedOwnerResolvesLocked$"},
		{"mutation_unresolved_to_ordinary.go", "^TestHistoryRestoreUnresolvedOwnerResolvesOrdinary$"},
		{"mutation_unresolved_await.go", "^TestHistoryRestoreUnresolvedOwnerAwaitDecide$"},
		{"mutation_unresolved_deadline.go", "^TestHistoryRestoreUnresolvedOwnerDeadline$"},
		{"mutation_pin_concern.go", "^TestHistoryRestoreUnresolvedOwner(ResolvesLocked|IndependentOfConcernPages)$"},
		{"mutation_ordinary_bypass.go", "^TestHistoryRestoreUnresolvedOrdinaryEmptyURLHolds$"},
		{"mutation_detach_reply.go", "^TestHistoryRestoreExplicitDetachAnswersOnce$"},
		{"mutation_accept_any_confirm.go", "^TestHistoryRestoreConfirmInFlightChildNeedsSecondRead$"},
		{"mutation_reuse_done_confirm.go", "^TestHistoryRestoreUnresolvedStaleDoneConfirmNeedsNewRead$"},
		{"mutation_reuse_open_confirm.go", "^TestHistoryRestoreConfirmInFlightChildNeedsSecondRead$"},
		{"mutation_stale_deliver.go", "^TestHistoryRestoreStaleForwardHides$"},
		{"mutation_reissue_min_zero.go", "^TestHistoryRestoreMinZeroHoldNoReissue$"},
		{"mutation_stream_undecided.go", "^TestHistoryRestoreUndecidedOnlyStreamByteIdentical$"},
		{"mutation_own_owner_undecided.go", "^TestHistoryRestoreOwnOwnerUndecidedNoLockedStreamNoHistoryHold$"},
		{"mutation_child_late_forward.go", "^TestHistoryRestoreInFlightChildCommandsAtProtectCommit$"},
		{"mutation_new_child_window.go", "^TestHistoryRestoreNewChildAtPendingProtectCommit$"},
	} {
		t.Run(m.mutant, func(t *testing.T) {
			hrOverlayRun(t, m.mutant, m.run, true)
		})
	}
}
