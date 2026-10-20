package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const deciderScript = `
const { pathToFileURL } = require('node:url');
const input = JSON.parse(process.argv[1]);
const github = { graphql: async () => ({ repository: { mergeQueue: { entries: { nodes: input.queue || [] } } } }) };
const context = { eventName: input.event, payload: input.payload, repo: { owner: 'ai-ecoverse', repo: 'slicc-cli' } };
import(pathToFileURL(input.file).href)
  .then(({ decide }) => decide({ context, github, env: input.env }))
  .then((r) => process.stdout.write(JSON.stringify({ runner: r.runner, light: r.light })));
`

type decision struct {
	Runner string `json:"runner"`
	Light  string `json:"light"`
}

func decideRunner(t *testing.T, input map[string]any) decision {
	t.Helper()
	file, err := filepath.Abs("../../.github/runner.mjs")
	if err != nil {
		t.Fatal(err)
	}
	input["file"] = file
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", "-e", deciderScript, string(raw)).CombinedOutput()
	if err != nil {
		t.Fatalf("decider: %v\n%s", err, out)
	}
	var d decision
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("decider printed %q: %v", out, err)
	}
	return d
}

func TestCIPoolKeepsTheForkRule(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	ci := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.Contains(ci, "          FORCED: ${{ vars.CI_RUNNER }}\n          POOL: ${{ vars.CERT_RUNNER || 'ubuntu-latest' }}\n          LIGHT: ${{ vars.CERT_LIGHT_RUNNER || 'ubuntu-latest' }}\n") {
		t.Fatal("ci.yml's decider must take its pools from vars.CERT_RUNNER and vars.CERT_LIGHT_RUNNER, defaulting to ubuntu-latest")
	}
	pool := map[string]any{"POOL": "fake-pool", "LIGHT": "fake-light-pool"}
	trusted, hosted := decision{"fake-pool", "fake-light-pool"}, decision{"ubuntu-latest", "ubuntu-latest"}
	pr := func(head string) map[string]any {
		return map[string]any{"pull_request": map[string]any{"head": map[string]any{"repo": map[string]any{"full_name": head}}}}
	}
	group := map[string]any{"merge_group": map[string]any{"base_ref": "refs/heads/main", "head_sha": "fake-sha-0002"}}
	entry := func(pos int, sha string, fork bool) map[string]any {
		return map[string]any{"position": pos, "headCommit": map[string]any{"oid": sha}, "pullRequest": map[string]any{"number": pos, "isCrossRepository": fork}}
	}
	for _, c := range []struct {
		name  string
		input map[string]any
		want  decision
	}{
		{"fork pull request", map[string]any{"event": "pull_request", "payload": pr("someone/slicc-cli"), "env": pool}, hosted},
		{"same-repo pull request", map[string]any{"event": "pull_request", "payload": pr("ai-ecoverse/slicc-cli"), "env": pool}, trusted},
		{"merge group behind a fork", map[string]any{"event": "merge_group", "payload": group, "env": pool, "queue": []any{entry(1, "fake-sha-0001", true), entry(2, "fake-sha-0002", false)}}, hosted},
		{"same-repo merge group", map[string]any{"event": "merge_group", "payload": group, "env": pool, "queue": []any{entry(1, "fake-sha-0001", false), entry(2, "fake-sha-0002", false)}}, trusted},
		{"merge group not in the queue", map[string]any{"event": "merge_group", "payload": group, "env": pool}, hosted},
		{"push", map[string]any{"event": "push", "payload": map[string]any{}, "env": pool}, trusted},
		{"release after CI", map[string]any{"event": "workflow_run", "payload": map[string]any{}, "env": pool}, trusted},
		{"manual release", map[string]any{"event": "workflow_dispatch", "payload": map[string]any{}, "env": pool}, trusted},
		{"schedule", map[string]any{"event": "schedule", "payload": map[string]any{}, "env": pool}, hosted},
		{"push without a light pool", map[string]any{"event": "push", "payload": map[string]any{}, "env": map[string]any{"POOL": "fake-pool"}}, decision{"fake-pool", "ubuntu-latest"}},
		{"forced", map[string]any{"event": "pull_request", "payload": pr("someone/slicc-cli"), "env": map[string]any{"FORCED": "fake-forced", "POOL": "fake-pool", "LIGHT": "fake-light-pool"}}, decision{"fake-forced", "fake-forced"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := decideRunner(t, c.input); got != c.want {
				t.Fatalf("decided %+v, want %+v", got, c.want)
			}
		})
	}
}

var hostedLabel = regexp.MustCompile(`^(ubuntu|macos|windows)-[0-9a-z.-]+$`)

func TestWorkflowsNameNoPoolAndKeepIDTokenJobsHosted(t *testing.T) {
	files, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("workflows: %v %v", files, err)
	}
	idJobs := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		for _, pool := range []string{"cloud-run-bench", "cloud-run-job"} {
			if strings.Contains(text, pool) {
				t.Errorf("%s names the pool %s; take it from vars.CERT_RUNNER or vars.CERT_LIGHT_RUNNER", filepath.Base(f), pool)
			}
		}
		if strings.Contains(text, "\npermissions:\n") && strings.Contains(strings.SplitN(text, "\njobs:\n", 2)[0], "id-token: write") {
			t.Errorf("%s grants id-token: write to every job", filepath.Base(f))
		}
		for name, job := range workflowJobs(t, text) {
			if !strings.Contains(job.body, "      id-token: write\n") {
				continue
			}
			idJobs++
			runsOn := ""
			for _, line := range strings.Split(job.body, "\n") {
				if v, ok := strings.CutPrefix(line, "    runs-on: "); ok {
					runsOn = strings.TrimSpace(v)
				}
			}
			if !hostedLabel.MatchString(runsOn) {
				t.Errorf("%s: job %s has id-token: write and runs on %q, not a GitHub-hosted label", filepath.Base(f), name, runsOn)
			}
		}
	}
	if idJobs < 2 {
		t.Errorf("found %d jobs with id-token: write; release.yml's release and sliccy-next should be among them", idJobs)
	}
}

const runnerJobScript = `
const input = JSON.parse(process.argv[1]);
const outputs = {};
const core = { setOutput: (k, v) => { outputs[k] = v; }, summary: { addRaw: () => ({ write: async () => {} }) } };
const github = {
  graphql: async () => ({ repository: { mergeQueue: { entries: { nodes: [] } } } }),
  rest: { issues: { listLabelsOnIssue: 'listLabelsOnIssue' } },
  paginate: async (fn, params) => {
    if (fn !== 'listLabelsOnIssue' || params.issue_number !== 7 || params.owner !== 'ai-ecoverse') throw new Error('unexpected call');
    if (!input.labels) throw new Error('fake API down');
    return input.labels;
  },
};
const context = { eventName: input.event, payload: input.payload, repo: { owner: 'ai-ecoverse', repo: 'slicc-cli' } };
process.env.GITHUB_WORKSPACE = input.workspace;
const AsyncFunction = (async () => {}).constructor;
new AsyncFunction('context', 'github', 'core', input.script)(context, github, core)
  .then(() => process.stdout.write(outputs.os));
`

func TestPullRequestsTestLinuxUnlessLabelledAllOS(t *testing.T) {
	ci := readWorkflow(t, "ci.yml")
	at := strings.Index(ci, "          script: |\n")
	if at < 0 {
		t.Fatal("ci.yml's runner job has no script")
	}
	var lines []string
	for _, line := range strings.Split(ci[at+len("          script: |\n"):], "\n") {
		if !strings.HasPrefix(line, "            ") {
			break
		}
		lines = append(lines, line[12:])
	}
	workspace, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	label := func(names ...string) []any {
		out := []any{}
		for _, n := range names {
			out = append(out, map[string]any{"name": n})
		}
		return out
	}
	pr := func(labels []any) map[string]any {
		return map[string]any{"pull_request": map[string]any{"number": 7, "labels": labels, "head": map[string]any{"repo": map[string]any{"full_name": "ai-ecoverse/slicc-cli"}}}}
	}
	for _, c := range []struct {
		name  string
		event string
		input map[string]any
		want  string
	}{
		{"push", "push", map[string]any{"payload": map[string]any{}}, "all"},
		{"merge group", "merge_group", map[string]any{"payload": map[string]any{"merge_group": map[string]any{"base_ref": "refs/heads/main", "head_sha": "fake-sha-0001"}}}, "all"},
		{"release", "workflow_run", map[string]any{"payload": map[string]any{}}, "all"},
		{"pull request", "pull_request", map[string]any{"payload": pr(label()), "labels": label("cert-review-approved")}, "linux"},
		{"pull request labelled all-os", "pull_request", map[string]any{"payload": pr(label()), "labels": label("cert-review-approved", "all-os")}, "all"},
		{"pull request whose label was removed after the event", "pull_request", map[string]any{"payload": pr(label("all-os")), "labels": label()}, "linux"},
		{"pull request when the API fails", "pull_request", map[string]any{"payload": pr(label("all-os"))}, "all"},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.input["event"], c.input["script"], c.input["workspace"] = c.event, strings.Join(lines, "\n"), workspace
			raw, err := json.Marshal(c.input)
			if err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("node", "-e", runnerJobScript, string(raw)).CombinedOutput()
			if err != nil || string(out) != c.want {
				t.Fatalf("os %q (%v), want %q", out, err, c.want)
			}
		})
	}
}
