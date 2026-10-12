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
	"sync"
	"sync/atomic"
	"testing"
)

func buildCompanion(t *testing.T, name, version string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), exeName(name))
	build := exec.Command("go", "build", "-ldflags", "-X main.name="+name+" -X main.version="+version, "-o", out, "./testdata/fakecompanion")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if msg, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type fakeReleases struct {
	*httptest.Server
	repo, asset, tag string
	mu               sync.Mutex
	payload          []byte
	sums             string
	asked            atomic.Int32
}

func (f *fakeReleases) set(payload []byte, sums string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if payload != nil {
		f.payload = payload
	}
	f.sums = sums
}

func newFakeReleases(t *testing.T, repo, name, tag string, payload []byte) *fakeReleases {
	t.Helper()
	f := &fakeReleases{repo: repo, asset: name + "-" + runtime.GOOS + "-" + runtime.GOARCH, tag: tag, payload: payload}
	if runtime.GOOS == "windows" {
		f.asset += ".exe"
	}
	sum := sha256.Sum256(payload)
	f.sums = fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), f.asset)
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.asked.Add(1)
		f.mu.Lock()
		payload, sums := f.payload, f.sums
		f.mu.Unlock()
		switch r.URL.Path {
		case "/repos/" + f.repo + "/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": f.tag, "assets": []map[string]string{
				{"name": f.asset, "browser_download_url": f.URL + "/dl/asset"},
				{"name": "SHA256SUMS", "browser_download_url": f.URL + "/dl/sums"},
			}})
		case "/dl/asset":
			_, _ = w.Write(payload)
		case "/dl/sums":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	f.Start()
	t.Cleanup(f.Close)
	return f
}

func installEnv(t *testing.T, f *fakeReleases) ([]string, string) {
	t.Helper()
	env := isolatedEnv(t, "SLICC_UPDATE_API_BASE="+f.URL)
	home := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	data := filepath.Join(home, ".local", "share", "slicc")
	switch runtime.GOOS {
	case "darwin":
		data = filepath.Join(home, "Library", "Application Support", "slicc")
	case "windows":
		data = filepath.Join(home, "cache", "slicc")
	default:
		env = append(env, "XDG_DATA_HOME=")
	}
	return env, filepath.Join(data, "bin", exeName(strings.SplitN(f.asset, "-", 2)[0]))
}

func TestInstallCompanions(t *testing.T) {
	for _, c := range []struct{ name, repo string }{{"jerk", "ai-ecoverse/slicc-jerk"}, {"fnox", "ai-ecoverse/fnox-build"}} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeReleases(t, c.repo, c.name, "v1.4.2", buildCompanion(t, c.name, "1.4.2"))
			env, dest := installEnv(t, f)
			r := runBin(t, env, binary, "install", c.name)
			if r.code != 0 || !strings.Contains(r.stdout, "installed "+c.name+" 1.4.2 at "+dest) || !strings.Contains(r.stdout, "the attestation isn't checked yet") {
				t.Fatalf("%+v", r)
			}
			if out, err := exec.Command(dest, "--version").Output(); err != nil || strings.TrimSpace(string(out)) != c.name+" 1.4.2" {
				t.Fatalf("%s: %q %v", dest, out, err)
			}
			r = runBin(t, env, binary, "install", c.name)
			if r.code != 0 || !strings.Contains(r.stdout, c.name+" 1.4.2 is already installed at "+dest) {
				t.Fatalf("again: %+v", r)
			}
		})
	}
}

func TestInstallRefusesABadDownload(t *testing.T) {
	f := newFakeReleases(t, "ai-ecoverse/slicc-jerk", "jerk", "v1.4.2", buildCompanion(t, "jerk", "1.4.2"))
	env, dest := installEnv(t, f)
	f.set(nil, strings.Repeat("0", 64)+"  "+f.asset+"\n")
	if r := runBin(t, env, binary, "install", "jerk"); r.code != 1 || !strings.Contains(r.stderr, "does not match its SHA-256 in SHA256SUMS") {
		t.Fatalf("checksum: %+v", r)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("a mismatched download was installed")
	}
	payload := buildCompanion(t, "jerk", "1.4.1")
	sum := sha256.Sum256(payload)
	f.set(payload, hex.EncodeToString(sum[:])+"  "+f.asset+"\n")
	if r := runBin(t, env, binary, "install", "jerk"); r.code != 1 || !strings.Contains(r.stderr, `reports version "1.4.1", expected 1.4.2`) {
		t.Fatalf("version: %+v", r)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("a binary with the wrong version was installed")
	}
}

func TestInstallUsage(t *testing.T) {
	for _, args := range [][]string{{"install"}, {"install", "gh"}, {"install", "jerk", "fnox"}} {
		if r := runBin(t, isolatedEnv(t), binary, args...); r.code != 2 || !strings.Contains(r.stderr, "usage: slicc install jerk|fnox") {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	r := runBin(t, isolatedEnv(t), binary, "install", "--help")
	if r.code != 0 || !strings.Contains(r.stdout, "ai-ecoverse/fnox-build") || !strings.Contains(r.stdout, "SHA256SUMS") {
		t.Fatalf("%+v", r)
	}
	if r := runBin(t, isolatedEnv(t), binary, "--help"); !strings.Contains(r.stdout, "slicc install jerk|fnox") {
		t.Fatalf("%+v", r)
	}
}

func TestInstalledCompanionsAreOnTheChildrensPath(t *testing.T) {
	requireBinary(t, "ssh")
	home := t.TempDir()
	data := filepath.Join(home, ".local", "share", "slicc")
	switch runtime.GOOS {
	case "darwin":
		data = filepath.Join(home, "Library", "Application Support", "slicc")
	case "windows":
		data = filepath.Join(home, "Local", "slicc")
	}
	bin := filepath.Join(data, "bin")
	writeFile(t, filepath.Join(bin, exeName("jerk")), "")
	env := append(homeEnv(home), "XDG_DATA_HOME="+filepath.Join(home, ".local", "share"), "LOCALAPPDATA="+filepath.Join(home, "Local"))
	b := launchBrowser(t)
	n := startSSHNodeEnv(t, b, pageServer(t), home, env, runnerArgs()...)
	out, errOut, code := systemSSH(t, n.fixture(t), n.hello.SSHHostKey, "", "env PATH")
	parts := filepath.SplitList(strings.TrimSpace(strings.ReplaceAll(out, "\r", "")))
	if code != 0 || len(parts) < 2 || parts[len(parts)-1] != bin {
		t.Fatalf("code %d stderr %q: PATH %v doesn't end with %s", code, errOut, parts, bin)
	}
}
