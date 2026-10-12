package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type latestRegistry struct {
	*httptest.Server
	mu    sync.Mutex
	asked []string
}

func newLatestRegistry(t *testing.T, latest map[string]string, published ...string) *latestRegistry {
	t.Helper()
	have := map[string]bool{}
	for _, p := range published {
		have[p] = true
	}
	r := &latestRegistry{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/")
		r.mu.Lock()
		r.asked = append(r.asked, path)
		r.mu.Unlock()
		at := strings.LastIndex(path, "/")
		if at <= 0 {
			http.NotFound(w, req)
			return
		}
		name, version := path[:at], path[at+1:]
		if version == "latest" && latest[name] != "" {
			version = latest[name]
		} else if !have[name+"@"+version] {
			http.NotFound(w, req)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": name, "version": version})
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *latestRegistry) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.asked...)
}

func stageWithEnv(t *testing.T, node string, env []string, extra ...string) (string, result) {
	t.Helper()
	from, out := t.TempDir(), t.TempDir()
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, assetName()), data, 0o755); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"../../scripts/npm-stage.mjs", "--version", "1.2.3-test", "--from", from, "--out", out, "--only", target()}, extra...)
	r := runBin(t, env, node, args...)
	if r.code != 0 {
		t.Fatalf("npm-stage: %+v", r)
	}
	return out, r
}

func optionalDeps(t *testing.T, dir string) map[string]string {
	t.Helper()
	var pkg struct {
		Optional map[string]string `json:"optionalDependencies"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "slicc", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	return pkg.Optional
}

func TestNPMStageAddsCompanionsThatAreOnTheRegistry(t *testing.T) {
	node := requireNode(t)
	reg := newLatestRegistry(t, map[string]string{"@ai-ecoverse/slicc-jerk": "1.4.0"})
	env := publishEnv(t, "npm_config_registry="+reg.URL+"/")
	out, _ := stageWithEnv(t, node, env)
	if deps := optionalDeps(t, out); len(deps) != 1 || len(reg.requests()) != 0 {
		t.Fatalf("without --companions: %v, asked %v", deps, reg.requests())
	}
	out, r := stageWithEnv(t, node, env, "--companions")
	deps := optionalDeps(t, out)
	if deps["@ai-ecoverse/slicc-jerk"] != "^1.4.0" || deps["@ai-ecoverse/slicc-"+target()] != "1.2.3-test" || len(deps) != 2 {
		t.Fatalf("with --companions: %v", deps)
	}
	if !strings.Contains(r.stdout, "@ai-ecoverse/fnox has no release on the registry yet; leaving it out") {
		t.Fatalf("%+v", r)
	}
	_, r = stageWithEnv(t, node, publishEnv(t), "--companions")
	if !strings.Contains(r.stdout, "@ai-ecoverse/slicc-jerk has no release on the registry yet") {
		t.Fatalf("an unreachable registry: %+v", r)
	}
}

func TestNPMPublishDoesNotWaitForCompanions(t *testing.T) {
	node := requireNode(t)
	dep := "@ai-ecoverse/slicc-" + target() + "@1.2.3-test"
	reg := newLatestRegistry(t, map[string]string{"@ai-ecoverse/slicc-jerk": "1.4.0", "@ai-ecoverse/fnox": "0.2.0"}, dep)
	out, _ := stageWithEnv(t, node, publishEnv(t, "npm_config_registry="+reg.URL+"/"), "--name", "sliccy", "--launcher-only", "--version", "7.0.0-next.1.2.3", "--deps-version", "1.2.3-test", "--companions")
	if deps := optionalDeps(t, out); deps["@ai-ecoverse/fnox"] != "^0.2.0" || len(deps) != 3 {
		t.Fatalf("deps %v", deps)
	}
	r := publish(t, node, out, publishEnv(t, "npm_config_registry="+reg.URL+"/", "SLICCY_NEXT_PUBLISH=true", "SLICC_REGISTRY_WAIT_SECONDS=0"), "--tag", "next", "--dry-run", "--require-registry")
	if r.code != 0 || !strings.Contains(r.stdout, "every optional dependency of sliccy@7.0.0-next.1.2.3 is on the registry") || strings.Contains(r.stdout+r.stderr, "waiting for") {
		t.Fatalf("the registry check waited for a companion: %+v", r)
	}
	for _, asked := range reg.requests() {
		if strings.Contains(asked, "^") {
			t.Fatalf("the registry check asked for a companion range: %v", reg.requests())
		}
	}
}

func TestNPMLauncherPutsCompanionsOnPath(t *testing.T) {
	node := requireNode(t)
	tool := filepath.Join(t.TempDir(), exeName("printpath"))
	if out, err := exec.Command("go", "build", "-o", tool, "./testdata/printpath").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	modules := filepath.Join(t.TempDir(), "node_modules")
	install(t, stage(t, node, tool), modules, true)
	read := func() map[string]string {
		t.Helper()
		r := launcher(t, node, modules, isolatedEnv(t))
		var got map[string]string
		if r.code != 0 || json.Unmarshal([]byte(r.stdout), &got) != nil {
			t.Fatalf("%+v", r)
		}
		return got
	}
	bins := filepath.Join(modules, ".bin")
	data, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bins, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bins, exeName("jerk")), data, 0o755); err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(bins); err == nil {
		bins = real
	}
	under := func(p string) bool {
		real, err := filepath.EvalSymlinks(p)
		return err == nil && strings.HasPrefix(real, bins)
	}
	if got := read(); under(got["jerk"]) || got["launcher"] != "npm" {
		t.Fatalf("jerk came from .bin without its package: %v", got)
	}
	writeFile(t, filepath.Join(modules, "@ai-ecoverse", "slicc-jerk", "package.json"), `{"name":"@ai-ecoverse/slicc-jerk","bin":{"jerk":"bin/jerk.js"}}`)
	if got := read(); !under(got["jerk"]) {
		t.Fatalf("jerk isn't on the PATH from %s: %v", bins, got)
	}
}
