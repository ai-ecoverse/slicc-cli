package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func stageSliccy(t *testing.T, node, version string) string {
	t.Helper()
	return stage(t, node, binary, "--name", "sliccy", "--launcher-only", "--version", version, "--deps-version", "1.2.3-test")
}

func publishEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	env := []string{}
	for _, kv := range isolatedEnv(t) {
		key := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if key == "SLICCY_NEXT_PUBLISH" || key == "NODE_AUTH_TOKEN" || key == "NPM_TOKEN" || strings.HasPrefix(key, "NPM_CONFIG_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, append([]string{"npm_config_registry=http://127.0.0.1:1/", "npm_config_fetch_retries=0"}, extra...)...)
}

func publish(t *testing.T, node, dir string, env []string, args ...string) result {
	t.Helper()
	return runBin(t, env, node, append([]string{"../../scripts/npm-publish.mjs", "--dir", dir}, args...)...)
}

func TestSliccyNextIsStagedAgainstTheReleasePackages(t *testing.T) {
	node := requireNode(t)
	staged := stageSliccy(t, node, "7.0.0-next.1.2.3-test")
	entries, err := os.ReadDir(staged)
	if err != nil || len(entries) != 1 {
		t.Fatalf("staged %v (%v), want only the launcher", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(staged, "slicc", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Name     string            `json:"name"`
		Version  string            `json:"version"`
		Bin      map[string]string `json:"bin"`
		Optional map[string]string `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "sliccy" || pkg.Version != "7.0.0-next.1.2.3-test" || pkg.Bin["sliccy"] != "bin/slicc.js" || pkg.Bin["slicc"] != "bin/slicc.js" || len(pkg.Bin) != 2 {
		t.Fatalf("%+v", pkg)
	}
	if pkg.Optional["@ai-ecoverse/slicc-"+target()] != "1.2.3-test" || len(pkg.Optional) != 1 {
		t.Fatalf("optionalDependencies %v", pkg.Optional)
	}
	readme, err := os.ReadFile(filepath.Join(staged, "slicc", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"preview of the new SLICC, seven", "npx sliccy@next", "npx sliccy@6", "two commands, `sliccy` and `slicc`"} {
		if !strings.Contains(string(readme), want) {
			t.Fatalf("README lacks %q:\n%s", want, readme)
		}
	}
	modules := filepath.Join(t.TempDir(), "node_modules")
	install(t, staged, modules, false)
	platform := stage(t, node, binary)
	if err := os.CopyFS(filepath.Join(modules, "@ai-ecoverse", "slicc-"+target()), os.DirFS(filepath.Join(platform, "slicc-"+target()))); err != nil {
		t.Fatal(err)
	}
	if r := launcher(t, node, modules, isolatedEnv(t), "--version"); r.code != 0 || r.stdout != "slicc 1.2.3-test\n" {
		t.Fatalf("%+v", r)
	}
}

func TestSliccyOnlyPublishesToNext(t *testing.T) {
	node := requireNode(t)
	good := stageSliccy(t, node, "7.0.0-next.1.2.3")
	bad := stageSliccy(t, node, "7.1.0-next.1.2.3")
	refusals := []struct {
		dir  string
		args []string
		want string
	}{
		{good, []string{"--dry-run"}, "with the tag latest; it only goes to next"},
		{good, []string{"--dry-run", "--tag", "latest"}, "with the tag latest; it only goes to next"},
		{good, []string{"--dry-run", "--tag", "beta"}, "with the tag beta; it only goes to next"},
		{good, []string{"--tag", "latest"}, "with the tag latest; it only goes to next"},
		{bad, []string{"--dry-run", "--tag", "next"}, "sliccy@7.1.0-next.1.2.3; its version must match"},
		{bad, []string{"--tag", "next"}, "its version must match"},
	}
	for _, c := range refusals {
		r := publish(t, node, c.dir, publishEnv(t, "SLICCY_NEXT_PUBLISH=true"), c.args...)
		if r.code == 0 || !strings.Contains(r.stderr, c.want) || strings.Contains(r.stdout+r.stderr, "Publishing to") {
			t.Fatalf("%v: %+v", c.args, r)
		}
	}
	r := publish(t, node, good, publishEnv(t), "--tag", "next")
	if r.code != 0 || !strings.Contains(r.stdout, "skipping sliccy@7.0.0-next.1.2.3; set the repository variable SLICCY_NEXT_PUBLISH") {
		t.Fatalf("gated off: %+v", r)
	}
	r = publish(t, node, good, publishEnv(t), "--tag", "next", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stderr, "with tag next") || !strings.Contains(r.stderr, "(dry-run)") {
		t.Fatalf("dry run: %+v", r)
	}
}

type fakeRegistry struct {
	*httptest.Server
	mu        sync.Mutex
	published map[string]bool
	asked     map[string]bool
}

func (f *fakeRegistry) wasAsked(dep string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[dep]
}

func newFakeRegistry(t *testing.T, published ...string) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{published: map[string]bool{}, asked: map[string]bool{}}
	for _, p := range published {
		f.published[p] = true
	}
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		at := strings.LastIndex(path, "/")
		if at > 0 {
			f.mu.Lock()
			f.asked[path[:at]+"@"+path[at+1:]] = true
			f.mu.Unlock()
		}
		if r.Method == http.MethodGet && at > 0 && f.published[path[:at]+"@"+path[at+1:]] {
			_ = json.NewEncoder(w).Encode(map[string]string{"name": path[:at], "version": path[at+1:]})
			return
		}
		http.NotFound(w, r)
	}))
	f.Start()
	t.Cleanup(f.Close)
	return f
}

func registryEnv(t *testing.T, reg *fakeRegistry) []string {
	t.Helper()
	return registryEnvWaiting(t, reg, 0)
}

func registryEnvWaiting(t *testing.T, reg *fakeRegistry, seconds int) []string {
	t.Helper()
	env := []string{}
	for _, kv := range publishEnv(t, "SLICCY_NEXT_PUBLISH=true", "SLICC_REGISTRY_WAIT_SECONDS="+strconv.Itoa(seconds)) {
		if !strings.HasPrefix(kv, "npm_config_registry=") {
			env = append(env, kv)
		}
	}
	return append(env, "npm_config_registry="+reg.URL+"/")
}

func TestSliccyNeedsItsPlatformPackagesOnTheRegistry(t *testing.T) {
	node := requireNode(t)
	staged := stageSliccy(t, node, "7.0.0-next.1.2.3-test")
	dep := "@ai-ecoverse/slicc-" + target() + "@1.2.3-test"
	r := publish(t, node, staged, registryEnv(t, newFakeRegistry(t)), "--tag", "next")
	if r.code == 0 || !strings.Contains(r.stderr, "refusing to publish sliccy@7.0.0-next.1.2.3-test; not on the registry: "+dep) || strings.Contains(r.stdout+r.stderr, "Publishing to") {
		t.Fatalf("missing dependency: %+v", r)
	}
	r = publish(t, node, staged, registryEnvWaiting(t, newFakeRegistry(t, dep), 120), "--tag", "next")
	if !strings.Contains(r.stdout, "every optional dependency of sliccy@7.0.0-next.1.2.3-test is on the registry") || strings.Contains(r.stderr, "not on the registry") {
		t.Fatalf("dependency present: %+v", r)
	}
}

func requireNPM(t *testing.T) string {
	t.Helper()
	npm, err := exec.LookPath("npm")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("npm is required for the npx tests")
		}
		t.Skip("npm is not installed")
	}
	return npm
}

func npmPack(t *testing.T, npm, dir string, env []string) string {
	t.Helper()
	dest := t.TempDir()
	cmd := exec.Command(npm, "pack", dir, "--pack-destination", dest)
	cmd.Env, cmd.Dir = env, dest
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("npm pack %s: %v\n%s", dir, err, out)
	}
	tarballs, _ := filepath.Glob(filepath.Join(dest, "*.tgz"))
	if len(tarballs) != 1 {
		t.Fatalf("npm pack %s gave %v", dir, tarballs)
	}
	return "file:" + filepath.ToSlash(tarballs[0])
}

func TestSliccyNextRunsThroughNpxUnderBothBins(t *testing.T) {
	node, npm := requireNode(t), requireNPM(t)
	env := append(isolatedEnv(t), "npm_config_cache="+t.TempDir(), "npm_config_registry=http://127.0.0.1:1/",
		"npm_config_fetch_retries=0", "npm_config_audit=false", "npm_config_fund=false", "npm_config_update_notifier=false")
	platform := npmPack(t, npm, filepath.Join(stage(t, node, binary), "slicc-"+target()), env)
	sliccy := npmPack(t, npm, filepath.Join(stage(t, node, binary, "--name", "sliccy", "--launcher-only", "--version", "7.0.0-next.1.2.3-test", "--deps-version", platform), "slicc"), env)
	for name, args := range map[string][]string{
		"npx sliccy":         {"exec", "--yes", "--", sliccy, "--version"},
		"the sliccy command": {"exec", "--yes", "--package", sliccy, "--", "sliccy", "--version"},
		"the slicc command":  {"exec", "--yes", "--package", sliccy, "--", "slicc", "--version"},
	} {
		cmd := exec.Command(npm, args...)
		cmd.Env, cmd.Dir = env, t.TempDir()
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "slicc 1.2.3-test") {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
}
