package integration

import (
	"os/exec"
	"strings"
	"testing"
)

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
