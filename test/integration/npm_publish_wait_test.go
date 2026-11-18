package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	waitNext    = "7.0.0-next.1.2.3"
	waitVersion = "1.2.3"
)

var waitDeps = []string{"@ai-ecoverse/slicc-darwin-arm64", "@ai-ecoverse/slicc-linux-amd64"}

type waitRegistry struct {
	*httptest.Server
	mu     sync.Mutex
	first  time.Time
	after  time.Duration
	never  bool
	misses map[string]int
	hits   map[string]int
}

func newWaitRegistry(t *testing.T, after time.Duration, never bool) *waitRegistry {
	t.Helper()
	reg := &waitRegistry{after: after, never: never, misses: map[string]int{}, hits: map[string]int{}}
	reg.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		at := strings.LastIndex(path, "/")
		reg.mu.Lock()
		now := time.Now()
		if reg.first.IsZero() {
			reg.first = now
		}
		seen := at > 0 && !reg.never && now.Sub(reg.first) >= reg.after && path[at+1:] == waitVersion && strings.HasPrefix(path, "@ai-ecoverse/slicc-")
		if seen {
			reg.hits[path]++
		} else {
			reg.misses[path]++
		}
		reg.mu.Unlock()
		if !seen {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"name": path[:at], "version": path[at+1:]})
	}))
	t.Cleanup(reg.Close)
	return reg
}

func (r *waitRegistry) missed(dep string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.misses[dep+"/"+waitVersion]
}

func (r *waitRegistry) asked() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, v := range r.misses {
		n += v
	}
	for _, v := range r.hits {
		n += v
	}
	return n
}

type fakeNPM struct {
	dir, log string
}

func newFakeNPM(t *testing.T, node string) fakeNPM {
	t.Helper()
	dir := t.TempDir()
	script := "#!" + node + `
const { appendFileSync } = require('node:fs');
const args = process.argv.slice(2);
appendFileSync(process.env.FAKE_NPM_LOG, JSON.stringify(args) + '\n');
if (args[0] === '--version') console.log('11.5.1');
else if (args[0] === 'config' && args[1] === 'get' && args[2] === 'registry') console.log(process.env.npm_config_registry || 'https://registry.npmjs.org/');
else if (args[0] === 'view') process.exit(1);
else if (args[0] === 'publish') {
  const fail = process.env.FAKE_NPM_FAIL;
  if (fail && args[1].endsWith('/' + fail)) {
    console.error('npm error E403 forbidden');
    process.exit(1);
  }
  console.log('published ' + args[1]);
} else process.exit(99);
`
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return fakeNPM{dir: dir, log: filepath.Join(t.TempDir(), "calls")}
}

func (f fakeNPM) calls(t *testing.T) [][]string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var call []string
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return calls
}

func (f fakeNPM) publishes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, call := range f.calls(t) {
		if call[0] == "publish" {
			out = append(out, strings.Join(call, " "))
		}
	}
	return out
}

func (f fakeNPM) reset(t *testing.T) {
	t.Helper()
	if err := os.Remove(f.log); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func waitEnv(t *testing.T, f fakeNPM, reg *waitRegistry, extra ...string) []string {
	t.Helper()
	env := []string{}
	for _, kv := range publishEnv(t) {
		key := strings.SplitN(kv, "=", 2)[0]
		switch key {
		case "npm_config_registry", "GITHUB_ACTIONS":
			continue
		case "PATH":
			kv = "PATH=" + f.dir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
		env = append(env, kv)
	}
	return append(env, append([]string{
		"npm_config_registry=" + reg.URL + "/",
		"FAKE_NPM_LOG=" + f.log,
		"SLICC_REGISTRY_WAIT_SECONDS=3",
		"SLICC_REGISTRY_POLL_SECONDS=1",
	}, extra...)...)
}

func writePackages(t *testing.T, pkgs map[string]map[string]any) string {
	t.Helper()
	root := t.TempDir()
	for dir, pkg := range pkgs {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(pkg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "package.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func platformDeps() map[string]string {
	deps := map[string]string{}
	for _, d := range waitDeps {
		deps[d] = waitVersion
	}
	return deps
}

func nextDir(t *testing.T, version string) string {
	t.Helper()
	return writePackages(t, map[string]map[string]any{"slicc": {"name": "sliccy", "version": version, "optionalDependencies": platformDeps()}})
}

var allPlatforms = []string{
	"@ai-ecoverse/slicc-darwin-amd64", "@ai-ecoverse/slicc-darwin-arm64",
	"@ai-ecoverse/slicc-linux-amd64", "@ai-ecoverse/slicc-linux-arm64",
	"@ai-ecoverse/slicc-windows-amd64", "@ai-ecoverse/slicc-windows-arm64",
}

func nextDirWith(t *testing.T, deps []string) string {
	t.Helper()
	pins := map[string]string{}
	for _, d := range deps {
		pins[d] = waitVersion
	}
	return writePackages(t, map[string]map[string]any{"slicc": {"name": "sliccy", "version": waitNext, "optionalDependencies": pins}})
}

func handlerRegistry(t *testing.T, h http.HandlerFunc) *waitRegistry {
	t.Helper()
	reg := &waitRegistry{misses: map[string]int{}, hits: map[string]int{}}
	reg.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.mu.Lock()
		reg.misses[strings.TrimPrefix(r.URL.Path, "/")]++
		reg.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(reg.Close)
	return reg
}

func latestDir(t *testing.T) string {
	t.Helper()
	pkgs := map[string]map[string]any{"slicc": {"name": "@ai-ecoverse/slicc", "version": waitVersion, "optionalDependencies": platformDeps()}}
	for _, d := range waitDeps {
		pkgs[strings.TrimPrefix(d, "@ai-ecoverse/")] = map[string]any{"name": d, "version": waitVersion}
	}
	return writePackages(t, pkgs)
}

func missingList() string {
	var parts []string
	for _, d := range waitDeps {
		parts = append(parts, d+"@"+waitVersion+" (HTTP 404)")
	}
	return strings.Join(parts, ", ")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func requirePosixNPM(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake npm uses a POSIX executable")
	}
	return requireNode(t)
}

func TestSliccyNextPublishesWhenItsPlatformPackagesAppearInTime(t *testing.T) {
	node := requirePosixNPM(t)
	f := newFakeNPM(t, node)
	reg := newWaitRegistry(t, 2*time.Second, false)
	dir := nextDir(t, waitNext)
	r := publish(t, node, dir, waitEnv(t, f, reg), "--tag", "next")
	if r.code != 0 || !strings.Contains(r.stdout, "every optional dependency of sliccy@"+waitNext+" is on the registry") {
		t.Fatalf("publish: %+v", r)
	}
	if lastLine(r.stdout) != "npm-publish: PASS next: published 1, 0 already published of 1 packages" || strings.Contains(r.stdout+r.stderr, "PARTIAL") || strings.Contains(r.stdout+r.stderr, "WARN") {
		t.Fatalf("verdict: %+v", r)
	}
	want := "publish " + filepath.Join(dir, "slicc") + " --access public --tag next --provenance"
	if got := f.publishes(t); len(got) != 1 || got[0] != want {
		t.Fatalf("publishes %q, want [%q]", got, want)
	}
	for _, d := range waitDeps {
		if n := reg.missed(d); n < 2 {
			t.Fatalf("the registry answered 404 for %s %d times before it appeared, want at least 2 polls one second apart: %+v", d, n, r)
		}
	}
	if n := strings.Count(r.stdout, "waiting for "); n < 2 {
		t.Fatalf("%d waiting lines, want at least 2 with SLICC_REGISTRY_POLL_SECONDS=1: %+v", n, r)
	}
}

func TestSliccyNextIsSkippedWhenItsPlatformPackagesNeverAppear(t *testing.T) {
	node := requirePosixNPM(t)
	f := newFakeNPM(t, node)
	reg := newWaitRegistry(t, 0, true)
	latest := latestDir(t)
	r := publish(t, node, latest, waitEnv(t, f, reg), "--tag", "latest")
	if r.code != 0 || lastLine(r.stdout) != "npm-publish: PASS latest: published 3, 0 already published of 3 packages" {
		t.Fatalf("latest leg: %+v", r)
	}
	latestPublishes := f.publishes(t)
	if len(latestPublishes) != 3 || reg.asked() != 0 {
		t.Fatalf("latest leg publishes %q, registry asked %d times", latestPublishes, reg.asked())
	}
	for _, p := range latestPublishes {
		if !strings.HasSuffix(p, " --access public --tag latest --provenance") {
			t.Fatalf("latest leg publish %q", p)
		}
	}

	f.reset(t)
	next := nextDir(t, waitNext)
	start := time.Now()
	r = publish(t, node, next, waitEnv(t, f, reg, "GITHUB_ACTIONS=true"), "--tag", "next")
	elapsed := time.Since(start)
	warn := "npm-publish: WARN next: sliccy@" + waitNext + " not published; not on the registry: " + missingList()
	if r.code != 0 {
		t.Fatalf("a timeout for sliccy@next fails the release: %+v", r)
	}
	if !strings.Contains(r.stdout, warn) || !strings.Contains(r.stderr, warn) {
		t.Fatalf("missing %q on stdout and stderr: %+v", warn, r)
	}
	if !strings.Contains(r.stdout, "::warning title=sliccy@next not published::") {
		t.Fatalf("missing the GitHub Actions annotation: %+v", r)
	}
	if !strings.HasPrefix(lastLine(r.stdout), "npm-publish: PARTIAL next: ") || strings.Contains(r.stdout, "npm-publish: PASS") {
		t.Fatalf("verdict: %+v", r)
	}
	if got := f.publishes(t); len(got) != 0 {
		t.Fatalf("published %q after the registry wait ran out", got)
	}
	for _, call := range f.calls(t) {
		joined := strings.Join(call, " ")
		if joined != "--version" && joined != "view sliccy@"+waitNext+" version" && joined != "config get registry" {
			t.Fatalf("the next leg ran npm %q", joined)
		}
	}
	for _, d := range waitDeps {
		if n := reg.missed(d); n < 2 {
			t.Fatalf("polled %s %d times in 3 s, want at least 2 with SLICC_REGISTRY_POLL_SECONDS=1", d, n)
		}
	}
	if elapsed < 3*time.Second || elapsed > 20*time.Second {
		t.Fatalf("gave up after %v with SLICC_REGISTRY_WAIT_SECONDS=3", elapsed)
	}

	f.reset(t)
	sh := requireSh(t)
	cmd := exec.Command(sh, "scripts/verdict.sh", "npm-publish", node, "scripts/npm-publish.mjs", "--dir", next, "--tag", "next")
	cmd.Dir = "../.."
	cmd.Env = waitEnv(t, f, reg)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), warn) || !strings.Contains(string(out), "npm-publish: PARTIAL next: ") || strings.Contains(string(out), "::warning") {
		t.Fatalf("verdict.sh npm-publish on a skipped sliccy@next: %v\n%s", err, out)
	}
	if got := f.publishes(t); len(got) != 0 {
		t.Fatalf("published %q through verdict.sh after the registry wait ran out", got)
	}
}

func TestNPMPublishStillFailsForOtherPackages(t *testing.T) {
	node := requirePosixNPM(t)
	f := newFakeNPM(t, node)
	reg := newWaitRegistry(t, 0, true)
	latest := latestDir(t)
	r := publish(t, node, latest, waitEnv(t, f, reg, "FAKE_NPM_FAIL=slicc-linux-amd64"), "--tag", "latest")
	if r.code != 1 || !strings.Contains(r.stderr, "npm-publish: npm publish @ai-ecoverse/slicc-linux-amd64@"+waitVersion+" failed") {
		t.Fatalf("a failing platform publish: %+v", r)
	}
	if out := r.stdout + r.stderr; strings.Contains(out, "npm-publish: PASS") || strings.Contains(out, "PARTIAL") || strings.Contains(out, "WARN") {
		t.Fatalf("a failing platform publish printed a verdict: %+v", r)
	}
	for _, p := range f.publishes(t) {
		if strings.Fields(p)[1] == filepath.Join(latest, "slicc") {
			t.Fatalf("published the launcher after a platform package failed: %q", p)
		}
	}

	f.reset(t)
	dir := writePackages(t, map[string]map[string]any{
		"slicc": {"name": "sliccy", "version": waitNext, "optionalDependencies": platformDeps()},
		"wasi":  {"name": "@ai-ecoverse/wasi-slicc", "version": waitVersion},
	})
	r = publish(t, node, dir, waitEnv(t, f, reg, "FAKE_NPM_FAIL=wasi"), "--tag", "next")
	if r.code != 1 || !strings.Contains(r.stderr, "npm-publish: npm publish @ai-ecoverse/wasi-slicc@"+waitVersion+" failed") {
		t.Fatalf("a failing package next to sliccy: %+v", r)
	}
	if out := r.stdout + r.stderr; strings.Contains(out, "npm-publish: PASS") || strings.Contains(out, "PARTIAL") {
		t.Fatalf("a failing package next to sliccy printed a verdict: %+v", r)
	}
	if got := f.publishes(t); len(got) != 1 || !strings.HasPrefix(got[0], "publish "+filepath.Join(dir, "wasi")+" ") {
		t.Fatalf("publishes %q", got)
	}
}

func TestNPMPublishKeepsTheSliccyTagGuards(t *testing.T) {
	node := requirePosixNPM(t)
	for _, c := range []struct {
		version, tag, want string
	}{
		{waitNext, "latest", "npm-publish: refusing to publish sliccy with the tag latest; it only goes to next"},
		{waitVersion, "next", "npm-publish: refusing to publish sliccy@" + waitVersion + "; its version must match"},
	} {
		f := newFakeNPM(t, node)
		reg := newWaitRegistry(t, 0, false)
		r := publish(t, node, nextDir(t, c.version), waitEnv(t, f, reg), "--tag", c.tag)
		if r.code != 1 || !strings.Contains(r.stderr, c.want) || strings.Contains(r.stdout+r.stderr, "npm-publish: PASS") || strings.Contains(r.stdout+r.stderr, "PARTIAL") {
			t.Fatalf("sliccy@%s --tag %s: %+v", c.version, c.tag, r)
		}
		if calls := f.calls(t); len(calls) != 0 || reg.asked() != 0 {
			t.Fatalf("sliccy@%s --tag %s ran npm %q and asked the registry %d times", c.version, c.tag, calls, reg.asked())
		}
	}
}

func TestNPMPublishDryRunIgnoresTheSliccySkip(t *testing.T) {
	node := requirePosixNPM(t)
	f := newFakeNPM(t, node)
	reg := newWaitRegistry(t, 0, true)
	dir := nextDir(t, waitNext)
	r := publish(t, node, dir, waitEnv(t, f, reg), "--tag", "next", "--dry-run")
	if r.code != 0 || lastLine(r.stdout) != "npm-publish: PASS next: checked 1 of 1 packages" || reg.asked() != 0 {
		t.Fatalf("dry run: %+v, registry asked %d times", r, reg.asked())
	}
	want := "publish " + filepath.Join(dir, "slicc") + " --access public --tag next --dry-run"
	if got := f.publishes(t); len(got) != 1 || got[0] != want {
		t.Fatalf("dry run publishes %q, want [%q]", got, want)
	}
	for _, call := range f.calls(t) {
		if call[0] != "publish" {
			t.Fatalf("dry run ran npm %q", call)
		}
	}

	f.reset(t)
	r = publish(t, node, dir, waitEnv(t, f, reg, "GITHUB_ACTIONS=true"), "--tag", "next", "--dry-run", "--require-registry")
	if r.code != 3 || !strings.Contains(r.stderr, "npm-publish: refusing to publish sliccy@"+waitNext+"; not on the registry: "+missingList()) {
		t.Fatalf("dry run with --require-registry: %+v", r)
	}
	if out := r.stdout + r.stderr; strings.Contains(out, "npm-publish: PASS") || strings.Contains(out, "PARTIAL") || strings.Contains(out, "WARN") || strings.Contains(out, "::warning") {
		t.Fatalf("dry run with --require-registry printed a verdict: %+v", r)
	}
	if got := f.publishes(t); len(got) != 0 {
		t.Fatalf("dry run with --require-registry published %q", got)
	}
}

func TestSliccyNextWaitEndsNearItsDeadlineWhenTheRegistryHangs(t *testing.T) {
	node := requirePosixNPM(t)
	f := newFakeNPM(t, node)
	done := make(chan struct{})
	reg := handlerRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	})
	t.Cleanup(func() { close(done) })
	start := time.Now()
	r := publish(t, node, nextDirWith(t, allPlatforms), waitEnv(t, f, reg, "SLICC_REGISTRY_WAIT_SECONDS=2"), "--tag", "next")
	elapsed := time.Since(start)
	if elapsed > 12*time.Second {
		t.Fatalf("a registry that never answers kept the script %v past a 2 s wait, want at most 10 s past the deadline: %+v", elapsed, r)
	}
	if r.code != 0 || !strings.Contains(r.stderr, "npm-publish: WARN next: sliccy@"+waitNext+" not published; not on the registry: ") || !strings.HasPrefix(lastLine(r.stdout), "npm-publish: PARTIAL next: ") {
		t.Fatalf("a registry that never answers: %+v", r)
	}
	for _, d := range allPlatforms {
		if !strings.Contains(r.stderr, d+"@"+waitVersion+" (no answer in ") {
			t.Fatalf("missing the timeout reason for %s: %+v", d, r)
		}
		if reg.missed(d) == 0 {
			t.Fatalf("never asked the registry for %s", d)
		}
	}
	if got := f.publishes(t); len(got) != 0 {
		t.Fatalf("published %q", got)
	}
}

func TestNPMPublishPrintsNoWorkflowCommandsFromTheRegistry(t *testing.T) {
	node := requirePosixNPM(t)
	f := newFakeNPM(t, node)
	reg := handlerRegistry(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"version": "9.9.9\n::error::boom\r\n::add-mask::x"})
	})
	r := publish(t, node, nextDir(t, waitNext), waitEnv(t, f, reg, "GITHUB_ACTIONS=true"), "--tag", "next")
	if r.code != 0 || !strings.HasPrefix(lastLine(r.stdout), "npm-publish: PARTIAL next: ") {
		t.Fatalf("a registry answering the wrong version: %+v", r)
	}
	annotations := 0
	for name, out := range map[string]string{"stdout": r.stdout, "stderr": r.stderr} {
		for _, line := range strings.FieldsFunc(out, func(c rune) bool { return c == '\n' || c == '\r' }) {
			if !strings.HasPrefix(strings.TrimSpace(line), "::") {
				continue
			}
			if name == "stdout" && strings.HasPrefix(line, "::warning title=sliccy@next not published::") {
				annotations++
				continue
			}
			t.Fatalf("%s carries a workflow command from the registry: %q\n%+v", name, line, r)
		}
	}
	if annotations != 1 {
		t.Fatalf("%d sliccy@next annotations, want 1: %+v", annotations, r)
	}
	if got := f.publishes(t); len(got) != 0 {
		t.Fatalf("published %q", got)
	}
}
