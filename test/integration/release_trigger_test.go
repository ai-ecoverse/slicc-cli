package integration

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseFollowsAGreenCIPushOnMain(t *testing.T) {
	read := func(name string) string {
		raw, err := os.ReadFile("../../.github/workflows/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(raw), "\r\n", "\n")
	}
	if ci := read("ci.yml"); !strings.HasPrefix(ci, "name: CI\n") || !strings.Contains(ci, "node scripts/sliccy-next.mjs --dry-run") {
		t.Fatal("ci.yml must be named CI, which release.yml follows, and check the npm packages on every pull request")
	}
	release := read("release.yml")
	for _, want := range []string{
		"  workflow_run:\n    workflows: [CI]\n    types: [completed]\n    branches: [main]\n",
		"github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.event == 'push'",
		"github.event.workflow_run.head_repository.full_name == github.repository",
		"ref: ${{ github.event.workflow_run.head_sha }}",
		"    if: ${{ github.event_name == 'workflow_dispatch' && !inputs.sliccy_next_only }}\n    name: CI on this commit\n",
		"(needs.ci.result == 'success' || github.event_name == 'pull_request' || github.event_name == 'workflow_run'",
		"    needs: [ci, fresh]\n",
		"${{ !cancelled() && needs.fresh.outputs.stale != 'true' && ",
		"git ls-remote \"$REPO\" refs/heads/main",
	} {
		if !strings.Contains(release, want) {
			t.Fatalf("release.yml lacks %q", want)
		}
	}
	if strings.Contains(release, "\n  push:") {
		t.Fatal("release.yml runs on push again, besides CI's workflow_run")
	}
}

type workflowBlock struct {
	name string
	cond string
	body string
}

func workflowJobs(t *testing.T, text string) map[string]workflowBlock {
	t.Helper()
	jobs := map[string]workflowBlock{}
	at := strings.Index(text, "\njobs:\n")
	if at < 0 {
		t.Fatal("release.yml has no jobs")
	}
	lines := strings.Split(text[at+len("\njobs:\n"):], "\n")
	name := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			name = strings.TrimSuffix(strings.TrimSpace(line), ":")
			jobs[name] = workflowBlock{name: name}
			continue
		}
		if name != "" {
			b := jobs[name]
			b.body += line + "\n"
			jobs[name] = b
		}
	}
	for name, b := range jobs {
		b.cond = blockCond(b.body, "    ")
		jobs[name] = b
	}
	return jobs
}

func blockCond(body, indent string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, indent+"if:") {
			continue
		}
		cond := strings.TrimSpace(strings.TrimPrefix(line, indent+"if:"))
		for _, more := range lines[i+1:] {
			if !strings.HasPrefix(more, indent+"  ") {
				break
			}
			cond += " " + strings.TrimSpace(more)
		}
		return cond
	}
	return ""
}

func jobSteps(body string) []workflowBlock {
	var steps []workflowBlock
	for i, part := range strings.Split(body, "\n      - ")[1:] {
		b := workflowBlock{name: strings.SplitN(part, "\n", 2)[0], body: "        " + part}
		if i == 0 && strings.HasPrefix(b.name, "steps:") {
			continue
		}
		b.cond = blockCond(b.body, "        ")
		steps = append(steps, b)
	}
	return steps
}

func TestReleasePublishesOnlyFromMain(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	const onMain = "github.ref == 'refs/heads/main'"
	const dry = "DRY_RUN: ${{ github.ref != 'refs/heads/main' }}"
	if n := strings.Count(text, "DRY_RUN:"); n != 1 || !strings.Contains(text, "      "+dry+"\n") {
		t.Errorf("release.yml sets DRY_RUN %d times; want it once, as %q", n, dry)
	}
	publishes := func(body string) string {
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "--dry-run") {
				continue
			}
			for _, marker := range []string{"semantic-release", "scripts/npm-publish.mjs", "scripts/sliccy-next.mjs", "actions/attest-build-provenance", "scripts/go-warm.sh", "releases/latest"} {
				if strings.Contains(line, marker) && !strings.Contains(line, "release-plan.mjs") {
					return marker
				}
			}
		}
		return ""
	}
	jobs := workflowJobs(t, text)
	if jobs["release"].body == "" || jobs["sliccy-next"].body == "" || jobs["install-smoke"].body == "" {
		t.Fatalf("release.yml lacks the release, sliccy-next or install-smoke job: %v", jobs)
	}
	guarded := map[string]bool{}
	for _, job := range jobs {
		for _, step := range jobSteps(job.body) {
			marker := publishes(step.body)
			if marker == "" {
				continue
			}
			guarded[marker] = true
			if !strings.Contains(job.cond, onMain) && !strings.Contains(step.cond, onMain) {
				t.Errorf("job %s, step %q runs %s without requiring %s (job if %q, step if %q)", job.name, step.name, marker, onMain, job.cond, step.cond)
			}
			if strings.Contains(step.cond, "||") && !strings.Contains(job.cond, onMain) {
				t.Errorf("job %s, step %q: its if %q has an || that can bypass %s", job.name, step.name, step.cond, onMain)
			}
		}
	}
	for _, marker := range []string{"semantic-release", "scripts/sliccy-next.mjs", "actions/attest-build-provenance", "scripts/go-warm.sh", "releases/latest"} {
		if !guarded[marker] {
			t.Errorf("found no step that runs %s; the guard check is out of date", marker)
		}
	}
	for _, name := range []string{"sliccy-next", "install-smoke"} {
		if !strings.Contains(jobs[name].cond, onMain) {
			t.Errorf("job %s does not require %s: %q", name, onMain, jobs[name].cond)
		}
	}
}
