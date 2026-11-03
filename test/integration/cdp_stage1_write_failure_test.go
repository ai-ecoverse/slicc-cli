package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func Test675Stage1PrivateDebuggerWriteFailureResetsPageStream(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "internal/link/link.go")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	needle := "func (ch *channel) send(b []byte) error {\n"
	if strings.Count(string(data), needle) != 1 {
		t.Fatal("missing memory transport insertion point")
	}
	memory := strings.Replace(string(data), needle, needle+"\tif ch.dc == nil { return nil }\n", 1)
	scratch := t.TempDir()
	memoryPath := filepath.Join(scratch, "link.go")
	if err := os.WriteFile(memoryPath, []byte(memory), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "test/integration/testdata/debuggerwrite")
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{
		source: memoryPath,
		filepath.Join(root, "internal/link/debugger_write_fixture.go"):          filepath.Join(fixture, "link.go"),
		filepath.Join(root, "internal/caps/cdp/debugger_write_failure_test.go"): filepath.Join(fixture, "regression.go"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlayPath, "-run", "^TestPrivateDebuggerWriteFailureResetsPageStream$", "./internal/caps/cdp")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("private debugger write regression: %v\n%s", err, output)
	}
}
