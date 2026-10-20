package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	return platformDepsAt("1.2.4")
}

func platformDepsAt(version string) []string {
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
	run("tag", "v1.2.5-rc.1")
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

func TestSliccyNextPublishesOnlyFromTheTagsCommit(t *testing.T) {
	node := requireNode(t)
	repo, run := gitRepo(t)
	commitLauncher(t, repo, run, "launcher 1.2.4\n", "v1.2.4")
	commitLauncher(t, repo, run, "unreleased\n", "")
	reg := newFakeRegistry(t, platformDeps()...)
	r := sliccyNext(t, node, repo, filepath.Join(t.TempDir(), "out"), registryEnv(t, reg))
	if r.code == 0 || !strings.Contains(r.stderr, "isn't v1.2.4's commit") || strings.Contains(r.stdout, "staging") || reg.wasAsked(platformDeps()[0]) {
		t.Fatalf("main past the tag: npm would record main's commit, and cert-verify requires the tag's: %+v", r)
	}
	if r := sliccyNext(t, node, repo, filepath.Join(t.TempDir(), "out"), registryEnv(t, reg), "--dry-run"); r.code != 0 || !strings.Contains(r.stdout, "sliccy-next: PASS sliccy@7.0.0-next.1.2.4 (dry run)") {
		t.Fatalf("the pull request's dry run checks from a commit past the tag: %+v", r)
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

func TestSliccyNextDryRunChecksThePreviousReleaseWhileTheLatestPublishes(t *testing.T) {
	node := requireNode(t)
	repo, run := gitRepo(t)
	commitLauncher(t, repo, run, "launcher 1.2.3\n", "v1.2.3")
	commitLauncher(t, repo, run, "launcher 1.2.4\n", "v1.2.4")
	latest := platformDeps()
	out := filepath.Join(t.TempDir(), "out")
	reg := newFakeRegistry(t, append(platformDepsAt("1.2.3"), latest[1:]...)...)
	r := sliccyNext(t, node, repo, out, registryEnvWaiting(t, reg, 120), "--dry-run")
	if r.code != 0 || strings.Contains(r.stdout, "waiting for") || !strings.Contains(r.stdout, "npm doesn't serve every platform package of v1.2.4 yet, so its release is still publishing; the dry run checks v1.2.3 instead") || !strings.Contains(r.stdout, "sliccy-next: PASS sliccy@7.0.0-next.1.2.3 (dry run)") {
		t.Fatalf("a full build that starts while v1.2.4 reaches npm waited out its job: %+v", r)
	}
	if launcherJS, err := os.ReadFile(filepath.Join(out, "slicc", "bin", "slicc.js")); err != nil || string(launcherJS) != "launcher 1.2.3\n" {
		t.Fatalf("launcher %q (%v), want the one at v1.2.3", launcherJS, err)
	}
	r = sliccyNext(t, node, repo, filepath.Join(t.TempDir(), "out"), registryEnv(t, newFakeRegistry(t, latest[1:]...)), "--dry-run")
	if r.code == 0 || !strings.Contains(r.stderr, "refusing to publish sliccy@7.0.0-next.1.2.3; not on the registry") || strings.Contains(r.stdout, "PASS") {
		t.Fatalf("neither release on npm: %+v", r)
	}
	r = sliccyNext(t, node, repo, filepath.Join(t.TempDir(), "out"), registryEnv(t, newFakeRegistry(t, append(platformDepsAt("1.2.3"), latest[1:]...)...)))
	if r.code == 0 || !strings.Contains(r.stderr, "refusing to publish sliccy@7.0.0-next.1.2.4; not on the registry: "+latest[0]) || strings.Contains(r.stdout, "v1.2.3") {
		t.Fatalf("a real publish fell back to an older release: %+v", r)
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
	data := readWorkflow(t, "release.yml")
	for _, want := range []string{
		"sliccy_next_only:",
		"node scripts/sliccy-next.mjs --dry-run",
		"run: sh scripts/verdict.sh sliccy-next node scripts/sliccy-next.mjs\n",
	} {
		if !strings.Contains(data, want) {
			t.Fatalf("release.yml lacks %q", want)
		}
	}
}

func TestSliccyNextWaitsForNpmOnAnUbuntuRunnerAfterTheRelease(t *testing.T) {
	raw, err := os.ReadFile("../../.releaserc.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "--tag next") || strings.Contains(string(raw), "npm-next") {
		t.Fatal("semantic-release on the macOS release job publishes only latest; sliccy@next waits for npm in the sliccy-next job")
	}
	job := workflowJobs(t, readWorkflow(t, "release.yml"))["sliccy-next"]
	for _, want := range []string{"    needs: release\n", "    runs-on: ubuntu-latest\n", "          ref: ${{ github.event.workflow_run.head_sha }}\n          fetch-depth: 0\n"} {
		if !strings.Contains(job.body, want) {
			t.Fatalf("the sliccy-next job lacks %q:\n%s", want, job.body)
		}
	}
	wait, minutes := 0, 0
	for _, line := range strings.Split(job.body, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "SLICC_REGISTRY_WAIT_SECONDS: "); ok {
			wait, _ = strconv.Atoi(strings.Trim(v, "'"))
		}
		if v, ok := strings.CutPrefix(line, "    timeout-minutes: "); ok {
			minutes, _ = strconv.Atoi(v)
		}
	}
	if wait < 1800 || minutes*60 < wait+600 {
		t.Fatalf("npm took 25 min to serve @ai-ecoverse/slicc-linux-arm64@1.44.4; the job waits %d s within %d min", wait, minutes)
	}
	base := func(event, ref string) ghCtx {
		return ghCtx{"cancelled()": false, "github.event_name": event, "github.ref": ref, "inputs.sliccy_next_only": nil, "needs.release.result": "skipped", "needs.release.outputs.version": ""}
	}
	released := base("workflow_run", "refs/heads/main")
	released["needs.release.result"], released["needs.release.outputs.version"] = "success", "1.44.4"
	nothing := base("workflow_run", "refs/heads/main")
	nothing["needs.release.result"] = "success"
	failed := base("workflow_run", "refs/heads/main")
	failed["needs.release.result"] = "failure"
	recovery := base("workflow_dispatch", "refs/heads/main")
	recovery["inputs.sliccy_next_only"] = true
	manual := base("workflow_dispatch", "refs/heads/main")
	manual["inputs.sliccy_next_only"], manual["needs.release.result"], manual["needs.release.outputs.version"] = false, "success", "1.44.4"
	branch := base("workflow_dispatch", "refs/heads/other")
	branch["inputs.sliccy_next_only"] = true
	stale := base("workflow_run", "refs/heads/main")
	cancelled := base("workflow_run", "refs/heads/main")
	cancelled["cancelled()"], cancelled["needs.release.result"], cancelled["needs.release.outputs.version"] = true, "success", "1.44.4"
	pr := base("pull_request", "refs/pull/7/merge")
	pr["needs.release.result"], pr["needs.release.outputs.version"] = "success", "0.0.0-dry-run.7"
	for _, c := range []struct {
		name string
		ctx  ghCtx
		runs bool
	}{
		{"after a release on main", released, true},
		{"after a release run that released nothing", nothing, false},
		{"after a failed release", failed, false},
		{"after a release skipped as stale", stale, false},
		{"a cancelled run", cancelled, false},
		{"the sliccy_next_only recovery", recovery, true},
		{"a manual release on main", manual, true},
		{"the recovery on a branch", branch, false},
		{"a pull request dry run", pr, false},
	} {
		if got := ghTruthy(ghTemplate(t, job.cond, c.ctx)); got != c.runs {
			t.Errorf("%s: the sliccy-next job runs: %v, want %v", c.name, got, c.runs)
		}
	}
}

func TestSliccyNextWaitsForAPlatformPackageNpmServesLate(t *testing.T) {
	node := requireNode(t)
	repo, run := gitRepo(t)
	commitLauncher(t, repo, run, "launcher\n", "v1.2.4")
	deps := platformDeps()
	for _, c := range []struct {
		name string
		wait int
		ok   bool
	}{
		{"a wait shorter than npm takes refuses, naming why", 0, false},
		{"a wait long enough publishes", 3, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg := newFakeRegistry(t, deps...)
			reg.late(deps[2], 1)
			r := sliccyNext(t, node, repo, filepath.Join(t.TempDir(), "out"), registryEnvWaiting(t, reg, c.wait), "--dry-run")
			if c.ok && (r.code != 0 || !strings.Contains(r.stdout, "waiting for "+deps[2]+" (HTTP 404) to appear on the registry") || !strings.Contains(r.stdout, "every optional dependency of sliccy@7.0.0-next.1.2.4 is on the registry")) {
				t.Fatalf("%+v", r)
			}
			if !c.ok && (r.code == 0 || !strings.Contains(r.stderr, "refusing to publish sliccy@7.0.0-next.1.2.4; not on the registry: "+deps[2]+" (HTTP 404)")) {
				t.Fatalf("%+v", r)
			}
		})
	}
}
