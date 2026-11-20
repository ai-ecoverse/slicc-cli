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

func TestCDPExistingSessionProtectedNavigation(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/existing_session_test.go"): filepath.Join(root, "test/integration/testdata/existingsession/regression.go"),
	}
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", "^TestExistingSession", "./internal/caps/cdp")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("existing-session protected navigation cells: %v\n%s", err, output)
	}
}

func TestCDPExistingSessionChromeErrorOrderingMutation(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/existing_session_test.go"): filepath.Join(root, "test/integration/testdata/existingsession/regression.go"),
		filepath.Join(root, "internal/caps/cdp/documentidentity.go"):      filepath.Join(root, "test/integration/testdata/existingsession/mutation_documentidentity.go"),
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestExistingSessionChromeErrorNeverLearnSecretRefused$", "./internal/caps/cdp")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("mutation (ordering deleted) must make the never-learn secret cell red; got green\n%s", output)
	}
}
