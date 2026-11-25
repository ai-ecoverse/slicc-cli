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

func leaseWriteOverlayRun(t *testing.T, mutant bool, run string, wantFail bool) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "internal/caps/cdp/relay.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	head := "func (rl *relay) forward("
	if strings.Count(src, head) != 1 {
		t.Fatal("forward insertion point changed")
	}
	start := strings.Index(src, head)
	end := start + strings.Index(src[start:], "\n}\n")
	body := src[start:end]
	if mutant {
		needle := "\t\tif !live || v.(context.Context).Err() != nil {\n\t\t\treturn nil\n\t\t}\n"
		if strings.Count(body, needle) != 1 {
			t.Fatal("forward liveness check changed")
		}
		body = strings.Replace(body, needle, needle+"\t\treturn rl.conn.Write(v.(context.Context), websocket.MessageText, out)\n", 1)
	}
	if !strings.Contains(body, "rl.conn.Write(") {
		t.Fatal("forward write changed")
	}
	body = strings.ReplaceAll(body, "rl.conn.Write(", "leaseRecord(rl).Write(")
	scratch := t.TempDir()
	copied := filepath.Join(scratch, "relay.go")
	if err := os.WriteFile(copied, []byte(src[:start]+body+src[end:]), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{
		path: copied,
		filepath.Join(root, "internal/caps/cdp/lease_write_test.go"): filepath.Join(root, "test/integration/testdata/leasewrite/regression.go"),
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
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-overlay", overlayPath, "-run", run, "./internal/caps/cdp")
	command.Dir = root
	output, err := command.CombinedOutput()
	if wantFail {
		if err == nil || !strings.Contains(string(output), "--- FAIL: Test") {
			t.Fatalf("restored call context must fail %s: %v\n%s", run, err, output)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s: %v\n%s", run, err, output)
	}
}

func TestCDPLeaseWriteContext(t *testing.T) {
	leaseWriteOverlayRun(t, false, "^TestLeaseWrite", false)
}

func TestCDPLeaseWriteContextMutant(t *testing.T) {
	for _, run := range []string{
		"^TestLeaseWriteCancelDuringLockWait$/^toBrowser$",
		"^TestLeaseWriteCancelDuringLockWait$/^release$",
		"^TestLeaseWriteCancelDuringFrameWrite$/^toBrowser$",
		"^TestLeaseWriteCancelDuringFrameWrite$/^release$",
		"^TestLeaseWriteLiveCallOnce$",
	} {
		t.Run(run, func(t *testing.T) {
			leaseWriteOverlayRun(t, true, run, true)
		})
	}
}
