package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func assetName() string {
	return exeName(fmt.Sprintf("slicc-%s-%s", runtime.GOOS, runtime.GOARCH))
}

func buildVersion(t *testing.T, version, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, exeName("slicc"))
	build := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version="+version, "-o", out, "../../cmd/slicc")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if msg, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", version, err, msg)
	}
	return out
}

type result struct {
	stdout, stderr string
	code           int
}

func isolatedEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	home := t.TempDir()
	env := []string{}
	for _, kv := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if strings.HasPrefix(key, "SLICC_") || strings.HasPrefix(key, "OPTEL_") {
			continue
		}
		switch key {
		case "HOME", "XDG_CACHE_HOME", "LOCALAPPDATA", "USERPROFILE":
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+home,
		"USERPROFILE="+home,
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"LOCALAPPDATA="+filepath.Join(home, "cache"),
		"SLICC_UPDATE_API_BASE=http://127.0.0.1:1",
		"SLICC_TELEMETRY_URL=http://127.0.0.1:1/",
	)
	return append(env, extra...)
}

func runBin(t *testing.T, env []string, bin string, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
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

type fakeGitHub struct {
	*httptest.Server
	tag      string
	payload  []byte
	sums     string
	status   int
	requests atomic.Int32
}

func newFakeGitHub(t *testing.T, tag string, payload []byte, options ...func(*fakeGitHub)) *fakeGitHub {
	t.Helper()
	sum := sha256.Sum256(payload)
	f := &fakeGitHub{tag: tag, payload: payload, status: http.StatusOK}
	f.sums = fmt.Sprintf("%s  %s\n%s  slicc-other-arch\n", hex.EncodeToString(sum[:]), assetName(), strings.Repeat("0", 64))
	for _, option := range options {
		option(f)
	}
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.Start()
	t.Cleanup(f.Close)
	return f
}

func withStatus(status int) func(*fakeGitHub) {
	return func(f *fakeGitHub) { f.status = status }
}

func withSums(sums string) func(*fakeGitHub) {
	return func(f *fakeGitHub) { f.sums = sums }
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if f.status != http.StatusOK {
		http.Error(w, "unavailable", f.status)
		return
	}
	switch r.URL.Path {
	case "/repos/ai-ecoverse/slicc-cli/releases/latest":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": f.tag,
			"assets": []map[string]string{
				{"name": "slicc-other-arch", "browser_download_url": f.URL + "/dl/other"},
				{"name": assetName(), "browser_download_url": f.URL + "/dl/" + assetName()},
				{"name": "SHA256SUMS", "browser_download_url": f.URL + "/dl/SHA256SUMS"},
			},
		})
	case "/dl/" + assetName():
		_, _ = w.Write(f.payload)
	case "/dl/SHA256SUMS":
		_, _ = w.Write([]byte(f.sums))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGitHub) env(t *testing.T, extra ...string) []string {
	return isolatedEnv(t, append([]string{"SLICC_UPDATE_API_BASE=" + f.URL}, extra...)...)
}

func newRelease(t *testing.T, version string) []byte {
	t.Helper()
	data, err := os.ReadFile(buildVersion(t, version, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func versionOf(t *testing.T, bin string) string {
	t.Helper()
	return strings.TrimSpace(runBin(t, isolatedEnv(t), bin, "--version").stdout)
}

func TestUpdateCheckReportsANewerRelease(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", newRelease(t, "1.1.0"))
	bin := buildVersion(t, "1.0.0", t.TempDir())
	r := runBin(t, gh.env(t), bin, "update", "--check")
	if r.code != 0 || !strings.Contains(r.stdout, "slicc 1.1.0 is available (you have 1.0.0)") {
		t.Fatalf("%+v", r)
	}
	if got := versionOf(t, bin); got != "slicc 1.0.0" {
		t.Fatalf("--check changed the binary: %q", got)
	}
}

func TestUpdateReplacesTheBinary(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", newRelease(t, "1.1.0"))
	bin := buildVersion(t, "1.0.0", t.TempDir())
	r := runBin(t, gh.env(t), bin, "update")
	if r.code != 0 || !strings.Contains(r.stdout, "to slicc 1.1.0") {
		t.Fatalf("%+v", r)
	}
	if got := versionOf(t, bin); got != "slicc 1.1.0" {
		t.Fatalf("after update: %q", got)
	}
	if _, err := os.Stat(bin + ".old"); (err == nil) != (runtime.GOOS == "windows") {
		t.Fatalf("parked old binary: %v", err)
	}
	runBin(t, gh.env(t, "SLICC_NO_UPDATE_CHECK=1"), bin, "browsers", "--help")
	if _, err := os.Stat(bin + ".old"); err == nil {
		t.Fatal("the parked binary survived the next run")
	}
	r = runBin(t, gh.env(t), bin, "update")
	if r.code != 0 || !strings.Contains(r.stdout, "slicc 1.1.0 is up to date") {
		t.Fatalf("second update: %+v", r)
	}
}

func assertUntouched(t *testing.T, bin string) {
	t.Helper()
	if got := versionOf(t, bin); got != "slicc 1.0.0" {
		t.Fatalf("binary changed: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(bin))
	if len(entries) != 1 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestUpdateRejectsAChecksumMismatch(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", newRelease(t, "1.1.0"), withSums(strings.Repeat("ab", 32)+"  "+assetName()+"\n"))
	bin := buildVersion(t, "1.0.0", t.TempDir())
	r := runBin(t, gh.env(t), bin, "update")
	if r.code != 1 || !strings.Contains(r.stderr, "does not match its SHA-256") {
		t.Fatalf("%+v", r)
	}
	assertUntouched(t, bin)
}

func TestUpdateGatesTheNewBinaryOnItsVersion(t *testing.T) {
	for name, payload := range map[string][]byte{
		"wrong version": newRelease(t, "1.2.0"),
		"not a binary":  []byte("not a binary"),
	} {
		t.Run(name, func(t *testing.T) {
			gh := newFakeGitHub(t, "v1.1.0", payload)
			bin := buildVersion(t, "1.0.0", t.TempDir())
			r := runBin(t, gh.env(t), bin, "update")
			if r.code != 1 || !strings.Contains(r.stderr, "the downloaded binary") {
				t.Fatalf("%+v", r)
			}
			assertUntouched(t, bin)
		})
	}
}

func TestUpdateRefusesADevelopmentBuild(t *testing.T) {
	gh := newFakeGitHub(t, "v9.0.0", newRelease(t, "9.0.0"))
	r := runBin(t, gh.env(t), binary, "update")
	if r.code != 1 || !strings.Contains(r.stderr, "refusing to replace a development build (1.2.3-test)") {
		t.Fatalf("update: %+v", r)
	}
	r = runBin(t, gh.env(t), binary, "update", "--check")
	if r.code != 0 || !strings.Contains(r.stdout, "latest release: 9.0.0 (this is a development build") {
		t.Fatalf("update --check: %+v", r)
	}
}

func TestUpdateIsDisabledForNPMAndAppBundles(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", newRelease(t, "1.1.0"))
	npm := buildVersion(t, "1.0.0", t.TempDir())
	app := buildVersion(t, "1.0.0", filepath.Join(t.TempDir(), "Sliccstart.app", "Contents", "Helpers"))
	cases := []struct {
		bin, launcher, want string
	}{
		{npm, "npm", "installed with npm"},
		{app, "", "inside an app bundle"},
	}
	for _, c := range cases {
		for _, args := range [][]string{{"update"}, {"update", "--check"}} {
			r := runBin(t, gh.env(t, "SLICC_LAUNCHER="+c.launcher), c.bin, args...)
			if r.code != 1 || !strings.Contains(r.stderr, c.want) {
				t.Fatalf("%v: %+v", args, r)
			}
		}
		r := runBin(t, gh.env(t, "SLICC_LAUNCHER="+c.launcher), c.bin, "browsers", "--help")
		if strings.Contains(r.stderr, "is available") {
			t.Fatalf("notice shown: %+v", r)
		}
	}
	if n := gh.requests.Load(); n != 0 {
		t.Fatalf("%d requests to GitHub", n)
	}
}

func TestUpdateReportsAPIFailures(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", nil, withStatus(http.StatusForbidden))
	bin := buildVersion(t, "1.0.0", t.TempDir())
	r := runBin(t, gh.env(t), bin, "update")
	if r.code != 1 || !strings.Contains(r.stderr, "HTTP 403") {
		t.Fatalf("%+v", r)
	}
}

func TestUpdateRejectsUnknownArguments(t *testing.T) {
	r := runBin(t, isolatedEnv(t), binary, "update", "--force")
	if r.code != 2 || !strings.Contains(r.stderr, `unknown argument "--force"`) {
		t.Fatalf("%+v", r)
	}
}

func TestUpdateNoticeChecksOncePerDay(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", nil)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	env := gh.env(t)
	first := runBin(t, env, bin, "browsers", "--help")
	if strings.Contains(first.stderr, "is available") || gh.requests.Load() != 1 {
		t.Fatalf("first run: %d requests, %+v", gh.requests.Load(), first)
	}
	second := runBin(t, env, bin, "--plain", "--browser", "no-such-browser")
	if !strings.Contains(second.stderr, "slicc 1.1.0 is available (you have 1.0.0); run `slicc update`") {
		t.Fatalf("second run: %+v", second)
	}
	if n := gh.requests.Load(); n != 1 {
		t.Fatalf("checked GitHub %d times within a day", n)
	}
	quiet := runBin(t, append(env, "SLICC_NO_UPDATE_CHECK=1"), bin, "browsers", "--help")
	if strings.Contains(quiet.stderr, "is available") {
		t.Fatalf("SLICC_NO_UPDATE_CHECK: %+v", quiet)
	}
	version := runBin(t, env, bin, "--version")
	if version.stderr != "" {
		t.Fatalf("--version printed a notice: %+v", version)
	}
}
