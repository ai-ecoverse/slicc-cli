package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
  .then((r) => process.stdout.write(r.runner));
`

func decideRunner(t *testing.T, input map[string]any) string {
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
	return string(out)
}

func TestCIPoolKeepsTheForkRule(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	ci := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.Contains(ci, "          FORCED: ${{ vars.CI_RUNNER }}\n          POOL: ${{ vars.CI_POOL || 'cloud-run-job' }}\n") {
		t.Fatal("ci.yml's decider must take its pool from vars.CI_POOL, defaulting to cloud-run-job")
	}
	pool := map[string]any{"POOL": "cloud-run-bench"}
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
		want  string
	}{
		{"fork pull request", map[string]any{"event": "pull_request", "payload": pr("someone/slicc-cli"), "env": pool}, "ubuntu-latest"},
		{"same-repo pull request", map[string]any{"event": "pull_request", "payload": pr("ai-ecoverse/slicc-cli"), "env": pool}, "cloud-run-bench"},
		{"merge group behind a fork", map[string]any{"event": "merge_group", "payload": group, "env": pool, "queue": []any{entry(1, "fake-sha-0001", true), entry(2, "fake-sha-0002", false)}}, "ubuntu-latest"},
		{"same-repo merge group", map[string]any{"event": "merge_group", "payload": group, "env": pool, "queue": []any{entry(1, "fake-sha-0001", false), entry(2, "fake-sha-0002", false)}}, "cloud-run-bench"},
		{"merge group not in the queue", map[string]any{"event": "merge_group", "payload": group, "env": pool}, "ubuntu-latest"},
		{"push", map[string]any{"event": "push", "payload": map[string]any{}, "env": pool}, "cloud-run-bench"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := decideRunner(t, c.input); got != c.want {
				t.Fatalf("runner %q, want %q", got, c.want)
			}
		})
	}
}
