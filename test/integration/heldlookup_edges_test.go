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

func heldLookupOverlayRun(t *testing.T, mutant, run string, wantFail bool) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/held_lookup_reply_test.go"): filepath.Join(root, "test/integration/testdata/heldlookup/regression.go"),
	}
	if mutant != "" {
		replacements[filepath.Join(root, "internal/caps/cdp/held_lookup_switches.go")] = filepath.Join(root, "test/integration/testdata/heldlookup", mutant)
	}
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
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

func TestCDPHeldLookupEdges(t *testing.T) {
	heldLookupOverlayRun(t, "", "^TestHeldLookup", false)
}

func TestCDPHeldLookupMutants(t *testing.T) {
	for _, m := range []struct{ mutant, run string }{
		{"mutation_park.go", "^TestHeldLookupReplyParkedUntilRelease$/^reply-without-url$"},
		{"mutation_raw.go", "^TestHeldLookupReplyProtectedAtSettleNoLeak$"},
		{"mutation_protected.go", "^TestHeldLookupReplyProtectedAtSettle$/^own-url-vault$"},
		{"mutation_deadline.go", "^TestHeldLookupReplyDeadline$"},
		{"mutation_slot.go", "^TestHeldLookupReplySlotsReleasedOnSettle$"},
	} {
		t.Run(m.mutant, func(t *testing.T) {
			heldLookupOverlayRun(t, m.mutant, m.run, true)
		})
	}
}
