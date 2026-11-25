package integration

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSignedLeaseLeadingDashIDIsData(t *testing.T) {
	for _, id := range []string{"-" + strings.Repeat("a", 23), "--" + strings.Repeat("b", 22), "_" + strings.Repeat("c", 23)} {
		for _, op := range []string{"grant", "deny", "revoke"} {
			args := []string{"lease", op, id}
			want := "unknown or stale ID"
			if op == "grant" {
				args = append(args, "--add", "https://receipt.example/one", "--keep-expiry")
				want = "no matching proven request"
			}
			if _, errOut, code := slicc(t, args...); code != 1 || !strings.Contains(errOut, want) {
				t.Errorf("%s ID %q: %d %s", op, id, code, errOut)
			}
		}
	}
	for _, args := range [][]string{{"grant", "--keep-expiry", "--add", "https://receipt.example/one"}, {"grant", "--unknown", "--add", "https://receipt.example/one", "--keep-expiry"}, {"revoke", "--no-input"}, {"deny", "--"}, {"revoke", "--bad/" + strings.Repeat("a", 18)}, {"deny", "not-an-id"}, {"revoke", "-" + strings.Repeat("a", 24)}, {"status", "-" + strings.Repeat("a", 23)}, {"revoke", "-" + strings.Repeat("a", 23), "--typo"}} {
		if _, errOut, code := slicc(t, append([]string{"lease"}, args...)...); code != 1 || !strings.Contains(errOut, "usage: slicc lease") {
			t.Errorf("invalid/ambiguous %v: %d %s", args, code, errOut)
		}
	}
}

func TestSignedLeaseRejectsMixedOrUnsafeFollow(t *testing.T) {
	for _, extra := range [][]string{{"--exit"}, {"--mount", "private"}, {"--cdp", "http://127.0.0.1:1"}, {"--native-screen"}, {"--runner", "not-a-real-program"}, {"--signal-only"}, {"--lease-browser", "https://untrusted.invalid"}, {"--lease-browser", "http://127.0.0.1:9/secret"}, {"--lease-browser", ""}, {"--name", "bad label"}} {
		args := append([]string{"-", "follow", "--lease-browser", "http://127.0.0.1:9", "--name", "fixture"}, extra...)
		cmd := exec.Command(binary, args...)
		cmd.Stdin = strings.NewReader("https://fixture.invalid/join/fixture.test\n")
		out, e := cmd.CombinedOutput()
		if e == nil || !strings.Contains(string(out), "lease-only follow requires") {
			t.Fatalf("%v: %s %v", extra, out, e)
		}
	}
}
