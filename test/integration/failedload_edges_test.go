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

func TestCDPFailedLoadAnnounceEdges(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/failed_load_edges_test.go"): filepath.Join(root, "test/integration/testdata/failedload/regression.go"),
	}
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestFailedLoad(CreatedBlanksUntilProof|AttachedHoldsUntilProof|EmptyThenRelayChangedHoldsTitle|EmptyThenNonRelayChangedBlanks|ProtectedProofHidesWithoutClientAttach|OrdinaryProofReleasesRealTitle|UnreadableProofStaysHeld|DetachWhilePendingLeavesNoClientAttach|GetTargetsBlanksPending|AttachEventBeforeConfirmReply|ConfirmReplyBeforeAttachEvent)$", "./internal/caps/cdp")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("failed-load announce edges: %v\n%s", err, output)
	}
}
