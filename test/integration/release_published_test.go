package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostReleaseJobsFollowOnlyAPublishedVersion(t *testing.T) {
	jobs := workflowJobs(t, readWorkflow(t, "release.yml"))
	release := jobs["release"]
	if !strings.Contains(release.body, "    outputs:\n      version: ${{ steps.published.outputs.version }}\n") {
		t.Fatalf("the release job's version output must come from the published check, not the planned build version:\n%s", release.body)
	}
	at := -1
	for i, step := range jobSteps(release.body) {
		if strings.Contains(step.body, "run: npx semantic-release\n") {
			at = i
		}
		if strings.Contains(step.body, "        id: published\n") {
			if at < 0 || at >= i {
				t.Errorf("the published check must run after semantic-release:\n%s", release.body)
			}
			if !strings.Contains(step.cond, "github.ref == 'refs/heads/main'") {
				t.Errorf("the published check runs off main: %q", step.cond)
			}
			if !strings.Contains(step.body, "          SHA: ${{ github.event.workflow_run.head_sha || github.sha }}\n") {
				t.Errorf("the published check must compare the tag with the commit the release built:\n%s", step.body)
			}
		}
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	sh := requireSh(t)
	script := stepRun(t, "release.yml", "release", "published")
	origin, run := gitRepo(t)
	run("commit", "-q", "--allow-empty", "-m", "built")
	built := strings.TrimSpace(gitOut(t, origin, "rev-parse", "HEAD"))
	run("commit", "-q", "--allow-empty", "-m", "newer")
	newer := strings.TrimSpace(gitOut(t, origin, "rev-parse", "HEAD"))
	work := filepath.Join(t.TempDir(), "work")
	if out, err := exec.Command("git", "clone", "-q", origin, work).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	check := func(version string) (string, string, int) {
		t.Helper()
		output := filepath.Join(t.TempDir(), "out")
		if err := os.WriteFile(output, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(sh, "-e", "-c", script)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "VERSION="+version, "SHA="+built, "GITHUB_OUTPUT="+output)
		out, err := cmd.CombinedOutput()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		return string(out), string(got), code
	}
	run("tag", "v1.0.1", built)
	run("tag", "-a", "-m", "annotated", "v1.0.2", built)
	run("tag", "v1.0.3", newer)
	run("tag", "-a", "-m", "annotated", "v1.0.4", newer)
	for _, c := range []struct {
		name, version, output, says string
	}{
		{"semantic-release tagged the built commit", "1.0.1", "version=1.0.1\n", "published: PASS v1.0.1 is tagged on " + built},
		{"an annotated tag on the built commit", "1.0.2", "version=1.0.2\n", "published: PASS v1.0.2 is tagged on " + built},
		{"semantic-release skipped because main moved", "1.0.9", "", "published: PASS nothing published: no tag v1.0.9"},
		{"a newer run released the version on another commit", "1.0.3", "", "is tagged on " + newer + ", not " + built},
		{"an annotated tag on another commit", "1.0.4", "", "is tagged on " + newer + ", not " + built},
		{"a prefix of a published version", "1.0", "", "no tag v1.0;"},
	} {
		out, got, code := check(c.version)
		if code != 0 || got != c.output || !strings.Contains(out, c.says) {
			t.Errorf("%s: exit %d, output %q, said:\n%s\nwant exit 0, output %q and %q", c.name, code, got, out, c.output, c.says)
		}
	}
	if out, err := exec.Command("git", "-C", work, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing")).CombinedOutput(); err != nil {
		t.Fatalf("git remote set-url: %v\n%s", err, out)
	}
	if out, got, code := check("1.0.1"); code == 0 || got != "" {
		t.Errorf("an unreadable origin must fail the release job, not skip its smoke tests: exit %d, output %q:\n%s", code, got, out)
	}

	planned := with(releaseAfterCI("current"), "needs.release.result", "success", "needs.release.outputs.version", "1.45.1", "inputs.sliccy_next_only", nil)
	skipped := with(planned, "needs.release.outputs.version", "")
	for _, name := range []string{"install-smoke", "sliccy-next"} {
		if !ghTruthy(ghTemplate(t, jobs[name].cond, planned)) {
			t.Errorf("%s doesn't run after a published release", name)
		}
		if ghTruthy(ghTemplate(t, jobs[name].cond, skipped)) {
			t.Errorf("%s runs although semantic-release published nothing", name)
		}
	}
	recovery := with(releaseCtx("workflow_dispatch", "refs/heads/main"), "inputs.sliccy_next_only", true, "needs.release.result", "skipped", "needs.release.outputs.version", "")
	if !ghTruthy(ghTemplate(t, jobs["sliccy-next"].cond, recovery)) {
		t.Error("the sliccy_next_only recovery no longer runs sliccy-next")
	}

	line := lineWith(t, jobs["report"].body, "scripts/release-issue.mjs")
	for _, c := range []struct {
		name, results, writes, want string
	}{
		{"a green run whose release published nothing", "full=success build=success smoke=success release=success sliccy-next=skipped install-smoke=skipped", "", "release-issue: PASS green; no open release-red issue"},
		{"a failed release is still red", "full=success build=success smoke=success release=failure sliccy-next=skipped install-smoke=skipped", "POST /issues", "release-issue: PASS red; opened #77"},
	} {
		api := newFakeActionsAPI(t)
		api.set(func() { api.label = true })
		out, code := runStep(t, line, "GITHUB_API_URL="+api.srv.URL, "GITHUB_SERVER_URL=https://github.com", "GITHUB_RUN_ID=4242", "SHA="+built, "RESULTS="+c.results)
		if code != 0 || !strings.Contains(out, c.want) {
			t.Errorf("%s: exit %d, want 0 and %q:\n%s", c.name, code, c.want, out)
		}
		if got := strings.Join(api.writes(), " "); got != c.writes {
			t.Errorf("%s: wrote %q, want %q", c.name, got, c.writes)
		}
	}
}

func gitOut(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}
