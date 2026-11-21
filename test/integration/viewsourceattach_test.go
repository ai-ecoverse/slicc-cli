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

func TestCDPViewSourceAttachRace(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
	}
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", "^TestViewSourceAttach", "./internal/caps/cdp")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("view-source attach race: %v\n%s", err, output)
	}
}

func TestCDPViewSourceAttachMutationConfirmDisabled(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/view_source_attach_test.go"): filepath.Join(root, "test/integration/testdata/viewsourceattach/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentconfirm.go"):         filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_documentconfirm.go"),
		filepath.Join(root, "internal/caps/cdp/documentidentity.go"):        filepath.Join(root, "test/integration/testdata/viewsourceattach/mutation_documentidentity.go"),
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", "^TestViewSourceAttachMutationProbe$", "./internal/caps/cdp")
	command.Dir = root
	command.Env = append(os.Environ(), "SLICC_DOC_CONFIRM_MUTATION=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("view-source attach mutation (confirm stubbed): %v\n%s", err, output)
	}
}
