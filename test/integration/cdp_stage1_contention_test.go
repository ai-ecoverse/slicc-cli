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

func TestStage1DebuggerContentionAfterBarrierRequiresAnotherBarrier(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{}
	scratch := t.TempDir()

	path := filepath.Join(root, "internal/caps/cdp/relay.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	needle := "func (rl *relay) writePage(data []byte) error {\n\trl.pageMu.Lock()\n\tdefer rl.pageMu.Unlock()"
	if strings.Count(string(data), needle) != 1 {
		t.Fatal("ordinary-write-unlocked pause hook insertion point changed")
	}
	instrumented := strings.Replace(string(data), needle, "func (rl *relay) writePage(data []byte) error {\n\trl.pageMu.Lock()\n\tdefer func(){rl.pageMu.Unlock(); debuggerContentionAfterOrdinaryWrite(data)}()", 1)
	copied := filepath.Join(scratch, "relay.go")
	if err := os.WriteFile(copied, []byte(instrumented), 0600); err != nil {
		t.Fatal(err)
	}
	replacements[path] = copied

	replacements[filepath.Join(root, "internal/caps/cdp/debugger_contention_test.go")] = filepath.Join(root, "test/integration/testdata/debuggercontention/regression.go")
	data, err = json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlay, "-run", "^TestDebuggerContentionAfterBarrier$", "./internal/caps/cdp")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("post-barrier contention regression: %v\n%s", err, output)
	}
}
