package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	return repo, run
}

func commitLauncher(t *testing.T, repo string, run func(args ...string), content, tag string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "npm", "slicc.js"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", content)
	if tag != "" {
		run("tag", tag)
	}
}

func platformDeps() []string {
	const version = "1.2.4"
	deps := []string{}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"arm64", "amd64"} {
			deps = append(deps, "@ai-ecoverse/slicc-"+goos+"-"+arch+"@"+version)
		}
	}
	return deps
}

func sliccyNext(t *testing.T, node, repo, out string, env []string, args ...string) result {
	t.Helper()
	return runBin(t, env, node, append([]string{"../../scripts/sliccy-next.mjs", "--repo", repo, "--out", out}, args...)...)
}

func TestSliccyNextStagesTheLatestRelease(t *testing.T) {
	node := requireNode(t)
	repo, run := gitRepo(t)
	commitLauncher(t, repo, run, "launcher 1.2.3\n", "v1.2.3")
	commitLauncher(t, repo, run, "launcher 1.2.4\n", "v1.2.4")
	commitLauncher(t, repo, run, "launcher 1.2.5-rc\n", "v1.2.5-rc.1")
	commitLauncher(t, repo, run, "unreleased\n", "")
	if err := os.WriteFile(filepath.Join(repo, "npm", "slicc.js"), []byte("working tree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "sliccy-next")
	reg := newFakeRegistry(t, platformDeps()...)
	r := sliccyNext(t, node, repo, out, registryEnvWaiting(t, reg, 120), "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "the latest release is v1.2.4; staging sliccy@7.0.0-next.1.2.4") {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stdout, "every optional dependency of sliccy@7.0.0-next.1.2.4 is on the registry") || !strings.Contains(r.stderr, "with tag next") || !strings.Contains(r.stderr, "(dry-run)") {
		t.Fatalf("publish: %+v", r)
	}
	for _, dep := range platformDeps() {
		if !reg.wasAsked(dep) {
			t.Fatalf("the fake registry was never asked for %s", dep)
		}
	}
	launcherJS, err := os.ReadFile(filepath.Join(out, "slicc", "bin", "slicc.js"))
	if err != nil || string(launcherJS) != "launcher 1.2.4\n" {
		t.Fatalf("launcher %q (%v), want the one at v1.2.4", launcherJS, err)
	}
	var pkg struct {
		Name     string            `json:"name"`
		Version  string            `json:"version"`
		Bin      map[string]string `json:"bin"`
		Optional map[string]string `json:"optionalDependencies"`
	}
	data, err := os.ReadFile(filepath.Join(out, "slicc", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "sliccy" || pkg.Version != "7.0.0-next.1.2.4" || pkg.Bin["sliccy"] != "bin/slicc.js" || pkg.Bin["slicc"] != "bin/slicc.js" || len(pkg.Bin) != 2 || len(pkg.Optional) != 6 {
		t.Fatalf("%+v", pkg)
	}
	for _, v := range pkg.Optional {
		if v != "1.2.4" {
			t.Fatalf("optionalDependencies %v", pkg.Optional)
		}
	}
}

func TestSliccyNextChecksTheRegistryEvenInADryRun(t *testing.T) {
	node := requireNode(t)
	repo, run := gitRepo(t)
	commitLauncher(t, repo, run, "launcher\n", "v1.2.4")
	deps := platformDeps()
	r := sliccyNext(t, node, repo, filepath.Join(t.TempDir(), "out"), registryEnv(t, newFakeRegistry(t, deps[1:]...)), "--dry-run")
	if r.code == 0 || !strings.Contains(r.stderr, "refusing to publish sliccy@7.0.0-next.1.2.4; not on the registry: "+deps[0]) || strings.Contains(r.stdout+r.stderr, "Publishing to") {
		t.Fatalf("%+v", r)
	}
}

func TestSliccyNextNeedsAReleaseTag(t *testing.T) {
	node := requireNode(t)
	repo, run := gitRepo(t)
	commitLauncher(t, repo, run, "launcher\n", "v1.2.4-rc.1")
	r := sliccyNext(t, node, repo, filepath.Join(t.TempDir(), "out"), registryEnv(t, newFakeRegistry(t)), "--dry-run")
	if r.code != 1 || !strings.Contains(r.stderr, "no release tag (vX.Y.Z) is reachable from HEAD") {
		t.Fatalf("%+v", r)
	}
}

func TestSliccyNextIsGuardedLikeTheRelease(t *testing.T) {
	node := requireNode(t)
	repo, run := gitRepo(t)
	commitLauncher(t, repo, run, "launcher\n", "v1.2.4")
	env := []string{}
	for _, kv := range registryEnv(t, newFakeRegistry(t, platformDeps()...)) {
		if !strings.HasPrefix(kv, "SLICCY_NEXT_PUBLISH=") {
			env = append(env, kv)
		}
	}
	r := sliccyNext(t, node, repo, filepath.Join(t.TempDir(), "out"), env)
	if r.code != 0 || !strings.Contains(r.stdout, "skipping sliccy@7.0.0-next.1.2.4; set the repository variable SLICCY_NEXT_PUBLISH") {
		t.Fatalf("%+v", r)
	}
}

func TestReleaseWorkflowOffersTheSliccyNextRecovery(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	data := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, want := range []string{
		"sliccy_next_only:",
		"node scripts/sliccy-next.mjs --dry-run",
		"inputs.sliccy_next_only && github.ref == 'refs/heads/main'",
		"run: sh scripts/verdict.sh sliccy-next node scripts/sliccy-next.mjs\n",
	} {
		if !strings.Contains(data, want) {
			t.Fatalf("release.yml lacks %q", want)
		}
	}
}
