package integration

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

func readRepo(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

func ciYAML(t *testing.T) string {
	t.Helper()
	return readRepo(t, ".github/workflows/ci.yml")
}

func ciInput(t *testing.T, name string) string {
	t.Helper()
	m := regexp.MustCompile(`\n {6}` + regexp.QuoteMeta(name) + `: (.+)\n`).FindStringSubmatch(ciYAML(t))
	if m == nil {
		return ""
	}
	return m[1]
}

type packageManifest struct {
	Scripts         map[string]string `json:"scripts"`
	DevDependencies map[string]string `json:"devDependencies"`
	Fledgling       struct {
		Workflow string   `json:"workflow"`
		Repo     string   `json:"repo"`
		Include  []string `json:"include"`
	} `json:"fledgling"`
}

func manifest(t *testing.T) packageManifest {
	t.Helper()
	var m packageManifest
	if err := json.Unmarshal([]byte(readRepo(t, "package.json")), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func npmScript(t *testing.T, name string) string {
	t.Helper()
	return manifest(t).Scripts[name]
}

func TestCIIsOneCallerOfTheSharedPipelineAtTheHarnessRelease(t *testing.T) {
	for dir, want := range map[string][]string{"../../.github": {"workflows"}, "../../.github/workflows": {"ci.yml"}} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s holds %v, want only %v", dir, got, want)
		}
	}
	version := manifest(t).DevDependencies["@ai-ecoverse/slicc-shared-web"]
	if !regexp.MustCompile(`^2\.[0-9]+\.[0-9]+$`).MatchString(version) {
		t.Fatalf("the @ai-ecoverse/slicc-shared-web devDependency is %q, want an exact 2.x", version)
	}
	ci := ciYAML(t)
	head := "\njobs:\n  ci:\n    uses: ai-ecoverse/slicc-shared-web/.github/workflows/pipeline.yml@v" + version + "\n    secrets: inherit\n    with:\n"
	if !strings.Contains(ci, head) {
		t.Fatalf("ci.yml doesn't call pipeline.yml@v%s with secrets: inherit:\n%s", version, ci)
	}
	if jobs := regexp.MustCompile(`(?m)^  [a-z-]+:$`).FindAllString(strings.SplitN(ci, "\njobs:\n", 2)[1], -1); len(jobs) != 1 {
		t.Fatalf("ci.yml has jobs %v, want only ci", jobs)
	}
}

func TestCIRunsOnPullRequestsMergeGroupsAndMainAndCancelsOnlyPullRequests(t *testing.T) {
	ci := ciYAML(t)
	for _, want := range []string{
		"\non:\n  pull_request:\n  merge_group:\n  push:\n    branches: [main]\n",
		"\nconcurrency:\n  group: ci-${{ github.ref }}\n  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n",
		"\npermissions:\n  contents: write\n  actions: read\n  issues: write\n  pull-requests: write\n  statuses: write\n  id-token: write\n  attestations: write\n\n",
	} {
		if !strings.Contains(ci, want) {
			t.Errorf("ci.yml lacks %q", want)
		}
	}
	for _, banned := range []string{"workflow_dispatch", "schedule:", "workflow_run", "workflow_call", "vars.", "runs-on", "review-renovate", "release-environment", "deploy"} {
		if strings.Contains(ci, banned) {
			t.Errorf("ci.yml names %q", banned)
		}
	}
}

func TestTheWorkflowHasNoCommentsButRenovateAnnotations(t *testing.T) {
	for i, line := range strings.Split(ciYAML(t), "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "# renovate:") {
			t.Errorf("ci.yml line %d is a comment: %s", i+1, line)
		}
	}
}

func TestThePipelineLintsAndTestsWhatThisRepoRunsLocally(t *testing.T) {
	lint := "      lint: |\n        # renovate: datasource=github-releases depName=golangci/golangci-lint\n        GOLANGCI_LINT=v2.15.0\n        curl -fsSL \"https://raw.githubusercontent.com/golangci/golangci-lint/$GOLANGCI_LINT/install.sh\" | sh -s -- -b \"$RUNNER_TEMP/golangci-lint\" \"$GOLANGCI_LINT\"\n        PATH=\"$RUNNER_TEMP/golangci-lint:$PATH\" npm run lint\n        npm run lint:sh\n"
	if !strings.Contains(ciYAML(t), lint) {
		t.Errorf("ci.yml's lint doesn't install the pinned golangci-lint and run npm run lint and npm run lint:sh:\n%s", ciYAML(t))
	}
	for _, name := range []string{"test", "test-glob", "require-tests", "node-options"} {
		if got := ciInput(t, name); got != "" {
			t.Errorf("ci.yml sets %s: %s; npm test is the pipeline's default command and writes the JUnit report the integration check counts", name, got)
		}
	}
	lint = npmScript(t, "lint")
	for _, want := range []string{"go vet ./...", "GOOS=darwin go vet ./...", "GOOS=windows go vet ./...", "golangci-lint run", "go mod tidy -diff"} {
		if !strings.Contains(lint, want) {
			t.Errorf("npm run lint lacks %q: %s", want, lint)
		}
	}
	if !strings.Contains(npmScript(t, "lint:sh"), "shellcheck -s sh install.sh") {
		t.Errorf("npm run lint:sh doesn't check install.sh")
	}
	if test := npmScript(t, "test"); !strings.HasPrefix(test, "node scripts/test-shard.mjs ci/"+ciInput(t, "shards")+" -race ") || !strings.HasSuffix(test, " -v ./test/integration/...") {
		t.Errorf("npm test is %q; it runs the integration tests with -race and -v on the pipeline's %s shards", test, ciInput(t, "shards"))
	}
}

func TestSemanticReleasePublishesFromTheTrustedCIWorkflow(t *testing.T) {
	m := manifest(t)
	if m.Fledgling.Workflow != "ci.yml" || m.Fledgling.Repo != "ai-ecoverse/slicc-cli" {
		t.Errorf("fledgling trusts %s in %s, want ci.yml in ai-ecoverse/slicc-cli", m.Fledgling.Workflow, m.Fledgling.Repo)
	}
	staged := map[string]bool{}
	for _, name := range m.Fledgling.Include {
		staged[name] = true
	}
	for _, name := range []string{"@ai-ecoverse/slicc", "@ai-ecoverse/wasi-slicc", "sliccy"} {
		if !staged[name] {
			t.Errorf("fledgling.include lacks %s, which the release publishes", name)
		}
	}
	rc := readRepo(t, ".releaserc.json")
	for _, want := range []string{
		`"branches": ["main"]`,
		`"prepareCmd": "sh scripts/verdict.sh release-build bash scripts/release-build.sh '${nextRelease.version}'"`,
		"sh scripts/verdict.sh npm-publish node scripts/npm-publish.mjs --dir dist/npm-next --tag next",
		`{ "path": "artifacts/release/SHA256SUMS", "label": "SHA256SUMS" }`,
	} {
		if !strings.Contains(rc, want) {
			t.Errorf(".releaserc.json lacks %s", want)
		}
	}
	if !strings.Contains(readRepo(t, "scripts/npm-publish.mjs"), "args.push(dryRun ? '--dry-run' : '--provenance');") {
		t.Error("npm-publish.mjs doesn't publish with provenance")
	}
	if !strings.Contains(readRepo(t, "scripts/release-build.sh"), `SLICC_SIGN="${SLICC_SIGN:-required}" bash scripts/package.sh`) {
		t.Error("the release build doesn't require signing and notarization")
	}
	if got := ciInput(t, "release-secrets"); got != "APPLE_CERTIFICATE_BASE64 APPLE_DEVELOPER_CERTIFICATE_PASSWORD APPLE_API_KEY_ID APPLE_API_KEY_ISSUER_ID APPLE_API_KEY_P8_BASE64" {
		t.Errorf("release-secrets is %q; the release signs and notarizes with the five Apple secrets", got)
	}
	if got := ciInput(t, "attest"); got != "artifacts/release/SHA256SUMS" {
		t.Errorf("attest is %q; slicc update needs the pipeline's attestation of SHA256SUMS", got)
	}
	if got := ciInput(t, "after-release"); got != `scripts/go-warm.sh "$RELEASE_VERSION"` {
		t.Errorf("after-release is %q", got)
	}
}

func TestRenovateKeepsTheSharedPresetAndTheBrowsersGroup(t *testing.T) {
	var config struct {
		Extends      []string `json:"extends"`
		PackageRules []struct {
			MatchPackageNames []string `json:"matchPackageNames"`
			GroupSlug         string   `json:"groupSlug"`
		} `json:"packageRules"`
		CustomManagers []any `json:"customManagers"`
	}
	if err := json.Unmarshal([]byte(readRepo(t, "renovate.json")), &config); err != nil {
		t.Fatal(err)
	}
	if strings.Join(config.Extends, " ") != "github>ai-ecoverse/slicc-shared-web" {
		t.Errorf("renovate.json extends %v", config.Extends)
	}
	if len(config.CustomManagers) != 0 {
		t.Errorf("renovate.json has %d custom managers; nothing in the workflow needs one", len(config.CustomManagers))
	}
	found := false
	for _, rule := range config.PackageRules {
		found = found || rule.GroupSlug == "slicc-browsers" && strings.Join(rule.MatchPackageNames, " ") == "@ai-ecoverse/slicc-browsers github.com/ai-ecoverse/slicc-browsers"
	}
	if !found {
		t.Error("renovate.json no longer moves slicc-browsers' npm and Go pins together")
	}
}
