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

func lhOverlay(t *testing.T, scratch string, replacements map[string]string) string {
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

func TestCDPLeaseHopEdges(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	overlay := lhOverlay(t, t.TempDir(), map[string]string{
		filepath.Join(root, "internal/caps/cdp/lease_hop_edges_test.go"): filepath.Join(root, "test/integration/testdata/leasehops/regression.go"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", overlay, "-run", "^TestLeaseHop", "./internal/caps/cdp")
	command.Dir = root
	if cache := os.Getenv("GOCACHE"); cache != "" {
		command.Env = append(os.Environ(), "GOCACHE="+cache)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lease-hop edges: %v\n%s", err, output)
	}
}
