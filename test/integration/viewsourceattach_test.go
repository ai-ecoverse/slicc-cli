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

func viewSourceAttachRun(t *testing.T, replacements map[string]string, run, envKey string) {
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
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", run, "./internal/caps/cdp")
	command.Dir = root
	command.Env = append(os.Environ(), envKey+"=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", run, err, output)
	}
}

func TestCDPViewSourceAttachRace(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
	}, "^TestViewSourceAttach", "SLICC_VIEWSOURCE_STOCK")
}

func TestCDPViewSourceAttachMutationConfirmDisabled(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm.go"):         filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_documentconfirm.go"),
	}, "^TestViewSourceAttachMutationProbe$", "SLICC_DOC_CONFIRM_MUTATION")
}

func TestCDPViewSourceAttachMutationDropProof(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"):  filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm_switches.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_switches.go"),
	}, "^TestViewSourceAttachMutationDropProof$", "SLICC_DOC_CONFIRM_DROP_PROOF")
}

func TestCDPViewSourceAttachMutationLatchOnFailure(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"):  filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm_switches.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_latch_on_failure.go"),
	}, "^TestViewSourceAttachMutationLatchOnFailure$", "SLICC_DOC_CONFIRM_LATCH_ON_FAILURE")
}

func TestCDPViewSourceAttachMutationExemptBlankness(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"):  filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm_switches.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_exempt_blankness.go"),
	}, "^TestViewSourceAttachMutationExemptBlankness$", "SLICC_DOC_CONFIRM_EXEMPT_BLANKNESS")
}

func TestCDPViewSourceAttachMutationDropProvenance(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"):  filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm_switches.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_drop_provenance.go"),
	}, "^TestViewSourceAttachMutationDropProvenance$", "SLICC_DOC_CONFIRM_DROP_PROVENANCE")
}

func TestCDPViewSourceAttachMutationDropSettleEmpty(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"):  filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm_switches.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_drop_settle_empty.go"),
	}, "^TestViewSourceAttachMutationDropSettleEmpty$", "SLICC_DOC_CONFIRM_DROP_SETTLE_EMPTY")
}

func TestCDPViewSourceAttachMutationDropSettleFailed(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"):  filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm_switches.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_drop_settle_failed.go"),
	}, "^TestViewSourceAttachMutationDropSettleFailed$", "SLICC_DOC_CONFIRM_DROP_SETTLE_FAILED")
}

func TestCDPViewSourceAttachMutationForeignOrdinary(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"):  filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm_switches.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_foreign_ordinary.go"),
	}, "^TestViewSourceAttachMutation(ForeignOrdinary|LighterResidualDocumentsHole)$", "SLICC_DOC_CONFIRM_FOREIGN_ORDINARY")
}

func TestCDPViewSourceAttachResidualLighterIsRed(t *testing.T) {
	root := viewSourceAttachRoot(t)
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"):  filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm_switches.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_foreign_ordinary.go"),
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", "^TestViewSourceAttachResidualOrdinaryAnnounceThenProtectedBeforeCommand$", "./internal/caps/cdp")
	command.Dir = root
	command.Env = append(os.Environ(), "SLICC_DOC_CONFIRM_FOREIGN_ORDINARY=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("residual row must be red with foreignOrdinaryProof=false and #863 absent:\n%s", output)
	}
	t.Logf("residual lighter red as required:\n%s", output)
}

func TestCDPViewSourceAttachMutationDropDK(t *testing.T) {
	root := viewSourceAttachRoot(t)
	viewSourceAttachRun(t, map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"):  filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm_switches.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_drop_dk.go"),
	}, "^TestViewSourceAttachMutationDropDK$", "SLICC_DOC_CONFIRM_DROP_DK")
}
