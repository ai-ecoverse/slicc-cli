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

func TestCDPRecoveryNavOrderEdges(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/recovery_nav_edges_test.go"): filepath.Join(root, "test/integration/testdata/recoverynav/regression.go"),
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestRecoveryNav(OrderMatrix|ConfirmFailedStaysUnreadableAndRecoverable)$", "./internal/caps/cdp")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("recovery-nav order edges: %v\n%s", err, output)
	}
}

func TestCDPRecoveryNavMutationWidenNavigationControls(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/recovery_nav_edges_test.go"): filepath.Join(root, "test/integration/testdata/recoverynav/regression.go"),
		filepath.Join(root, "internal/caps/cdp/navguard.go"):                filepath.Join(root, "test/integration/testdata/recoverynav/mutation_navguard.go"),
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestRecoveryNavMutationWidenNavigationControls$", "./internal/caps/cdp")
	command.Dir = root
	command.Env = append(os.Environ(), "SLICC_RECOVERY_NAV_MUTATION=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("recovery-nav mutation (navigationControls widen): %v\n%s", err, output)
	}
	contract := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestRecoveryNavOrderMatrix$/resource-tree-on-error-page$", "./internal/caps/cdp")
	contract.Dir = root
	if out, err := contract.CombinedOutput(); err == nil {
		t.Fatalf("widening error-page admission to navigationControls must break resource-tree-on-error-page\n%s", out)
	}
}
