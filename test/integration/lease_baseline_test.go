package integration

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const leaseBaselinePrefix = "ci-proof lease-baseline "

func leaseBaselineSPKI(t *testing.T) string {
	t.Helper()
	probe := httptest.NewTLSServer(http.NotFoundHandler())
	defer probe.Close()
	d := sha256.Sum256(probe.Certificate().RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(d[:])
}

func leaseBaselineEcho(t *testing.T, out []byte) int {
	t.Helper()
	n := 0
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, leaseBaselinePrefix); i >= 0 {
			t.Log(line[i:])
			n++
		}
	}
	return n
}

func TestSignedLeaseBaselineBroker(t *testing.T) {
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
	})
	ctx, cancel := context.WithTimeout(t.Context(), 420*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-timeout", "400s", "-overlay", ov, "-run", "^TestLeaseBaseline", "./internal/lease")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "SLICC_LEASE_BASELINE_ENDPOINT="+owner.endpoint, "SLICC_LEASE_BASELINE_BROWSER_PID="+strconv.Itoa(owner.cmd.Process.Pid))
	out, err := cmd.CombinedOutput()
	rows := leaseBaselineEcho(t, out)
	if err != nil {
		t.Fatalf("lease baseline broker rows: %v\n%s", err, out)
	}
	for _, name := range []string{"TestLeaseBaselineRevokeGoto", "TestLeaseBaselineRevokeSnapshot", "TestLeaseBaselineExpiry", "TestLeaseBaselineClock", "TestLeaseBaselineLimits", "TestLeaseBaselineAdmissions"} {
		if !strings.Contains(string(out), "--- PASS: "+name+" ") {
			t.Fatalf("%s did not run\n%s", name, out)
		}
	}
	t.Logf("%sbroker-summary rows=%d fail=0", leaseBaselinePrefix, rows)
}
