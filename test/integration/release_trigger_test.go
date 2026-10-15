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
		"if: ${{ github.event_name != 'workflow_run' && ",
	} {
		if !strings.Contains(release, want) {
			t.Fatalf("release.yml lacks %q", want)
		}
	}
	if strings.Contains(release, "\n  push:") {
		t.Fatal("release.yml runs on push again, besides CI's workflow_run")
	}
}
