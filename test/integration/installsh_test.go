//go:build !windows

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func stageInstallRelease(t *testing.T, dir, version string) {
	t.Helper()
	payload := newRelease(t, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, assetName()), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	sums := fmt.Sprintf("%s  %s\n%s  slicc-other-arch\n", hex.EncodeToString(sum[:]), assetName(), strings.Repeat("0", 64))
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
}

func serveDir(t *testing.T, root string) string {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir(root)))
	t.Cleanup(srv.Close)
	return srv.URL
}

func installScript(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func runInstaller(t *testing.T, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command("sh", append([]string{installScript(t)}, args...)...)
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{stdout.String(), stderr.String(), code}
}

func TestInstallScriptChecks(t *testing.T) {
	root := t.TempDir()
	stageInstallRelease(t, filepath.Join(root, "release"), "1.2.3")
	cmd := exec.Command("sh", "testdata/install/check.sh", root, serveDir(t, root), "1.2.3")
	cmd.Env = isolatedEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check.sh: %v\n%s", err, out)
	}
}

func TestInstallScriptUpgradesDowngradesAndSliccUpdatesInPlace(t *testing.T) {
	root := t.TempDir()
	stageInstallRelease(t, filepath.Join(root, "latest", "download"), "1.1.0")
	stageInstallRelease(t, filepath.Join(root, "download", "v1.0.0"), "1.0.0")
	env := isolatedEnv(t, "SLICC_INSTALL_BASE_URL="+serveDir(t, root))
	home := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			home = strings.TrimPrefix(kv, "HOME=")
		}
	}
	bin := filepath.Join(home, ".local", "bin", "slicc")
	steps := []struct {
		args []string
		want string
	}{
		{[]string{"--version", "1.0.0"}, "slicc 1.0.0"},
		{nil, "slicc 1.1.0"},
		{[]string{"--version", "v1.0.0"}, "slicc 1.0.0"},
	}
	for _, step := range steps {
		r := runInstaller(t, env, step.args...)
		if r.code != 0 || !strings.Contains(r.stdout, "installed "+step.want+" at ") {
			t.Fatalf("install %v: %+v", step.args, r)
		}
		if got := versionOf(t, bin); got != step.want {
			t.Fatalf("install %v: %q", step.args, got)
		}
	}
	gh := newFakeGitHub(t, "v1.1.0", newRelease(t, "1.1.0"))
	r := runBin(t, gh.env(t), bin, "update")
	if r.code != 0 || !strings.Contains(r.stdout, "to slicc 1.1.0") {
		t.Fatalf("update: %+v", r)
	}
	if got := versionOf(t, bin); got != "slicc 1.1.0" {
		t.Fatalf("after update: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(bin))
	if len(entries) != 1 {
		t.Fatalf("left files behind in %s: %v", filepath.Dir(bin), entries)
	}
}

func TestInstallScriptHelpNamesTheShortURLFirst(t *testing.T) {
	r := runInstaller(t, isolatedEnv(t), "--help")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	short := strings.Index(r.stdout, "curl -fsSL https://get.sliccy.ai/slicc | sh\n")
	options := strings.Index(r.stdout, "curl -fsSL https://get.sliccy.ai/slicc | sh -s -- [options]\n")
	long := strings.Index(r.stdout, "curl -fsSL https://github.com/ai-ecoverse/slicc-cli/releases/latest/download/install.sh | sh\n")
	if short < 0 || options < short || long < options {
		t.Fatalf("help:\n%s", r.stdout)
	}
}
