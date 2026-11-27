package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSignedLeaseBaselineR12(t *testing.T) {
	leaseHopRequireChrome(t)
	owner := launchBrowserOn(t, 0,
		"--ignore-certificate-errors-spki-list="+leaseBaselineSPKI(t),
		"--host-resolver-rules=MAP seven.sliccy.ai 127.0.0.1",
		"--no-proxy-server",
	)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ov := hrOverlay(t, t.TempDir(), map[string]string{
		filepath.Join(root, "internal/lease/baseline_test.go"): filepath.Join(root, "test/integration/testdata/lease/baseline.go"),
		filepath.Join(root, "internal/lease/r12_test.go"):      filepath.Join(root, "test/integration/testdata/lease/r12.go"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 420*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-timeout", "400s", "-overlay", ov, "-run", "^TestLeaseBaselineR12", "./internal/lease")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "SLICC_LEASE_BASELINE_ENDPOINT="+owner.endpoint, "SLICC_LEASE_BASELINE_BROWSER_PID="+strconv.Itoa(owner.cmd.Process.Pid))
	out, err := cmd.CombinedOutput()
	rows := leaseBaselineEcho(t, out)
	if err != nil {
		t.Fatalf("lease baseline R12 rows: %v\n%s", err, out)
	}
	for _, name := range []string{
		"TestLeaseBaselineR12RedirectCreate",
		"TestLeaseBaselineR12RedirectAssign",
		"TestLeaseBaselineR12PageChange",
		"TestLeaseBaselineR12SnapshotIdentity",
		"TestLeaseBaselineR12CaptureRace",
		"TestLeaseBaselineR12ScopeMatch",
		"TestLeaseBaselineR12ContractFacts",
	} {
		if !strings.Contains(string(out), "--- PASS: "+name+" ") {
			t.Fatalf("%s did not run\n%s", name, out)
		}
	}
	t.Logf("%sR12-summary rows=%d fail=0", leaseBaselinePrefix, rows)
}
