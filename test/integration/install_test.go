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
	attestations     []byte
	attested         string
	asked            atomic.Int32
	sig              *fakeSigstore
}

func (f *fakeReleases) attest(t *testing.T, o bundleOptions) {
	t.Helper()
	f.attestOver(t, o, false)
}

func (f *fakeReleases) attestOver(t *testing.T, o bundleOptions, sums bool) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256(f.payload)
	f.attested = hex.EncodeToString(sum[:])
	if sums {
		sum = sha256.Sum256([]byte(f.sums))
		f.attested = hex.EncodeToString(sum[:])
	}
	if o.san == "" {
		o.san = releaseSAN(f.repo)
	}
	f.attestations = attestationList(f.sig.bundle(t, hex.EncodeToString(sum[:]), o))
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
	f := &fakeReleases{repo: repo, asset: name + "-" + runtime.GOOS + "-" + runtime.GOARCH, tag: tag, payload: payload, sig: newFakeSigstore(t)}
	if runtime.GOOS == "windows" {
		f.asset += ".exe"
	}
	sum := sha256.Sum256(payload)
	f.sums = fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), f.asset)
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.asked.Add(1)
		f.mu.Lock()
		payload, sums, attestations, attested := f.payload, f.sums, f.attestations, f.attested
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
		case "/repos/" + f.repo + "/attestations/sha256:" + attested:
			if attestations == nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(attestations)
		default:
			http.NotFound(w, r)
		}
	}))
	f.Start()
	t.Cleanup(f.Close)
	f.attest(t, bundleOptions{})
	return f
}

func installEnv(t *testing.T, f *fakeReleases) ([]string, string) {
	t.Helper()
	env := isolatedEnv(t, "SLICC_UPDATE_API_BASE="+f.URL, f.sig.env())
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
			if r.code != 0 || !strings.Contains(r.stdout, "installed "+c.name+" 1.4.2 at "+dest) || !strings.Contains(r.stdout, "the build provenance attestation by "+releaseSAN(c.repo)) {
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
	f.attest(t, bundleOptions{})
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

func TestInstallRequiresTheAttestation(t *testing.T) {
	f := newFakeReleases(t, "ai-ecoverse/fnox-build", "fnox", "v0.9.0", buildCompanion(t, "fnox", "0.9.0"))
	env, dest := installEnv(t, f)
	f.mu.Lock()
	f.attestations = nil
	f.mu.Unlock()
	if r := runBin(t, env, binary, "install", "fnox"); r.code != 1 || !strings.Contains(r.stderr, "fetching the attestation of "+f.asset) {
		t.Fatalf("no attestation: %+v", r)
	}
	f.attest(t, bundleOptions{san: releaseSAN("ai-ecoverse/fnox")})
	if r := runBin(t, env, binary, "install", "fnox"); r.code != 1 || !strings.Contains(r.stderr, "no attestation of "+f.asset+" by "+releaseSAN("ai-ecoverse/fnox-build")+" verifies: attestation identity") {
		t.Fatalf("the fork's identity: %+v", r)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("an unattested binary was installed")
	}
	r := runBin(t, env, binary, "install", "fnox", "--checksum-only")
	if r.code != 0 || !strings.Contains(r.stderr, "warning: --checksum-only skips the attestation") || !strings.Contains(r.stdout, "SHA256SUMS only; the attestation wasn't checked") {
		t.Fatalf("--checksum-only: %+v", r)
	}
}

func TestInstallTakesAnAttestationOverSHA256SUMS(t *testing.T) {
	f := newFakeReleases(t, "ai-ecoverse/slicc-jerk", "jerk", "v0.3.0", buildCompanion(t, "jerk", "0.3.0"))
	f.attestOver(t, bundleOptions{}, true)
	env, dest := installEnv(t, f)
	if r := runBin(t, env, binary, "install", "jerk"); r.code != 0 || !strings.Contains(r.stdout, "installed jerk 0.3.0 at "+dest) {
		t.Fatalf("%+v", r)
	}
	f.attestOver(t, bundleOptions{san: releaseSAN("ai-ecoverse/slicc-cli")}, true)
	if r := runBin(t, env, binary, "install", "jerk"); r.code != 0 {
		t.Fatalf("already installed: %+v", r)
	}
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if r := runBin(t, env, binary, "install", "jerk"); r.code != 1 || !strings.Contains(r.stderr, "no attestation of SHA256SUMS by "+releaseSAN("ai-ecoverse/slicc-jerk")+" verifies") {
		t.Fatalf("SHA256SUMS attested by another repo: %+v", r)
	}
}

func TestInstallRefusesJerkBeforeTheReleaseLayout(t *testing.T) {
	f := newFakeReleases(t, "ai-ecoverse/slicc-jerk", "jerk", "v0.2.9", buildCompanion(t, "jerk", "0.2.9"))
	env, dest := installEnv(t, f)
	if r := runBin(t, env, binary, "install", "jerk"); r.code != 1 || !strings.Contains(r.stderr, "ai-ecoverse/slicc-jerk 0.2.9 predates the release layout slicc installs from (ai-ecoverse/slicc-jerk 0.3.0 and later)") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("an old jerk was installed")
	}
}
