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

func attachReplyOverlayRun(t *testing.T, mutant, run string, wantFail bool) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/attach_reply_test.go"): filepath.Join(root, "test/integration/testdata/attachreply/regression.go"),
	}
	if mutant != "" {
		replacements[filepath.Join(root, "internal/caps/cdp/held_reply_switches.go")] = filepath.Join(root, "test/integration/testdata/attachreply", mutant)
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

func TestCDPAttachReplyEdges(t *testing.T) {
	attachReplyOverlayRun(t, "", "^Test(AttachReply|HeldReply)", false)
}

func TestCDPAttachReplyMutants(t *testing.T) {
	for _, m := range []struct{ mutant, run string }{
		{"mutation_docconfirm.go", "^TestHeldReplyAnsweredOnTeardown$/^doc-confirm-attach-reply$/^ordinary$/^session-detached$"},
		{"mutation_docidentity.go", "^TestHeldReplyAnsweredOnTeardown$/^document-identity-reply$/^ordinary$/^session-detached$"},
		{"mutation_body.go", "^TestHeldReplyAnsweredOnTeardown$/^body-answer$/^ordinary$/^session-detached$"},
		{"mutation_paint.go", "^TestHeldReplyAnsweredOnTeardown$/^extension-paint$/^protected$/^session-detached$"},
		{"mutation_reply.go", "^TestAttachReplyPassedThenProtectedThenReply$/^vault-view-source$"},
		{"mutation_pass.go", "^TestAttachReplyHeldThenReplayedAnnounceThenOrdinaryConfirm$/^vault-view-source$"},
		{"mutation_announce.go", "^TestAttachReplyRefusedSessionReannounced$/^vault-view-source$"},
		{"mutation_flush.go", "^TestHeldReplyAnsweredOnTeardown$/^body-answer$/^ordinary$/^tab-cleanup$"},
		{"mutation_flush.go", "^TestHeldReplyOnEmptyTabAnsweredAtOnce$"},
		{"mutation_sticky.go", "^TestAttachReplyCutSessionReannouncedAfterClear$/^vault-view-source$"},
	} {
		t.Run(m.mutant+m.run, func(t *testing.T) {
			attachReplyOverlayRun(t, m.mutant, m.run, true)
		})
	}
}
