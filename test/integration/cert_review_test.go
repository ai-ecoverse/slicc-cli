package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

const certTools = "../../tools/cert-review/"

type certAPI struct {
	t        *testing.T
	mu       sync.Mutex
	routes   map[string]any
	statuses []map[string]any
	srv      *httptest.Server
}

func newCertAPI(t *testing.T, routes map[string]any) *certAPI {
	f := &certAPI{t: t, routes: routes}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/statuses/") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			body["sha"] = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			f.statuses = append(f.statuses, body)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "{}")
			return
		}
		v, ok := f.routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if s, raw := v.(string); raw {
			_, _ = io.WriteString(w, s)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *certAPI) posted() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.statuses...)
}

type toolRun struct {
	out     string
	code    int
	outputs map[string]string
}

func certTool(t *testing.T, api string, script string, args ...string) toolRun {
	t.Helper()
	node := requireNode(t)
	gho := filepath.Join(t.TempDir(), "github_output")
	cmd := exec.Command(node, append([]string{certTools + script}, args...)...)
	cmd.Env = append(os.Environ(), "GITHUB_API_URL="+api, "GH_TOKEN=fake-value-0001", "GITHUB_OUTPUT="+gho, "GITHUB_STEP_SUMMARY=")
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]string{}
	data, _ := os.ReadFile(gho)
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			outputs[k] = v
		}
	}
	return toolRun{string(out), code, outputs}
}

func writeJSON(t *testing.T, path string, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func shaHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func manifestOf(sums string, files map[string]string, packages map[string]string) map[string]any {
	pk := map[string]any{}
	for name, sha := range packages {
		pk[name] = map[string]any{"version": "1.2.4-pr.7.1", "sha256": sha}
	}
	return map[string]any{"version": "1.2.4-pr.7.1", "sums": sums, "files": files, "packages": pk, "problems": []string{}}
}

const certRunStart = "2026-01-01T10:00:00Z"

func certArtifact(id int, name string) map[string]any {
	return map[string]any{"id": id, "name": name, "digest": "sha256:" + shaHex([]byte(name)), "created_at": "2026-01-01T10:05:00Z"}
}

func declared(arts []map[string]any) string {
	parts := []string{}
	for _, a := range arts {
		parts = append(parts, fmt.Sprintf("%v=%v:%s", a["name"], a["id"], strings.TrimPrefix(a["digest"].(string), "sha256:")))
	}
	return strings.Join(parts, ", ")
}

func withRun(routes map[string]any, arts []map[string]any) map[string]any {
	if routes == nil {
		routes = map[string]any{}
	}
	routes["/repos/ai-ecoverse/slicc-cli/actions/runs/1"] = map[string]any{"run_attempt": 1, "run_started_at": certRunStart}
	routes["/repos/ai-ecoverse/slicc-cli/actions/runs/1/artifacts"] = map[string]any{"total_count": len(arts), "artifacts": arts}
	return routes
}

func buildArtifacts() []map[string]any {
	return []map[string]any{certArtifact(201, "cert-build-candidate-1.2.4-pr.7.1"), certArtifact(202, "cert-build-base-1.2.4-pr.7.1")}
}

func onlyStatus(t *testing.T, f *certAPI) map[string]any {
	t.Helper()
	got := f.posted()
	if len(got) != 1 {
		t.Fatalf("want one status, got %v", got)
	}
	return got[0]
}

func TestCertReviewClassifiesAPullRequestByWhatShips(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"SHA256SUMS": "fake-sums-1", "slicc-linux-amd64": "fake-bin-1", "slicc-wasip1-wasm.wasm": "fake-wasm-1"}
	pkgs := func(slicc, wasi string) map[string]string {
		return map[string]string{"@ai-ecoverse/slicc": slicc, "@ai-ecoverse/wasi-slicc": wasi}
	}
	same := writeJSON(t, filepath.Join(dir, "base.json"), manifestOf("fake-sums-1", files, pkgs("fake-tgz-1", "fake-wasi-1")))
	identical := writeJSON(t, filepath.Join(dir, "identical.json"), manifestOf("fake-sums-1", files, pkgs("fake-tgz-1", "fake-wasi-1")))
	readme := writeJSON(t, filepath.Join(dir, "readme.json"), manifestOf("fake-sums-1", files, pkgs("fake-tgz-2", "fake-wasi-1")))
	wasm := map[string]string{"SHA256SUMS": "fake-sums-2", "slicc-linux-amd64": "fake-bin-1", "slicc-wasip1-wasm.wasm": "fake-wasm-2"}
	code := writeJSON(t, filepath.Join(dir, "code.json"), manifestOf("fake-sums-2", wasm, pkgs("fake-tgz-1", "fake-wasi-1")))
	host := writeJSON(t, filepath.Join(dir, "host.json"), manifestOf("fake-sums-1", files, pkgs("fake-tgz-1", "fake-wasi-2")))
	linux := map[string]string{"SHA256SUMS": "fake-sums-3", "slicc-linux-amd64": "fake-bin-2", "slicc-wasip1-wasm.wasm": "fake-wasm-1"}
	native := writeJSON(t, filepath.Join(dir, "native.json"), manifestOf("fake-sums-3", linux, pkgs("fake-tgz-1", "fake-wasi-1")))
	cases := []struct {
		name, candidate, labels, forced, state, desc, goOn, bios string
		exit                                                     int
	}{
		{"a dev pin that ships nothing new", identical, "", "", "success", "not behaviour: identical build outputs", "false", "false", 0},
		{"the same, with merge-ok", identical, "merge-ok", "", "success", "not behaviour", "false", "false", 0},
		{"a README change that changes a tarball", readme, "", "", "failure", "waits for merge-ok to be reviewed (changes npm @ai-ecoverse/slicc)", "false", "false", 0},
		{"a code change", code, "", "", "failure", "changes SHA256SUMS, slicc-wasip1-wasm.wasm", "false", "true", 0},
		{"a code change with merge-ok", code, "bug,merge-ok", "", "pending", "reviewing #7 at fake-head-00", "true", "true", 0},
		{"a wasi-slicc host change with merge-ok", host, "merge-ok", "", "pending", "reviewing #7", "true", "true", 0},
		{"a native-only change with merge-ok", native, "merge-ok", "", "pending", "reviewing #7", "true", "false", 0},
		{"release.yml with identical outputs", identical, "", ".github/workflows/release.yml", "failure", "changes .github/workflows/release.yml", "false", "false", 0},
		{"no candidate build", filepath.Join(dir, "missing.json"), "merge-ok", "", "error", "couldn't build the pull request", "", "", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			arts := buildArtifacts()
			f := newCertAPI(t, withRun(nil, arts))
			r := certTool(t, f.srv.URL, "pr.mjs", "classify", "--pr", "7", "--head", "fake-head-0001", "--labels", c.labels, "--forced", c.forced, "--run", "1", "--artifacts", declared(arts), "--base-manifest", same, "--candidate-manifest", c.candidate)
			if r.code != c.exit {
				t.Fatalf("exit %d, want %d:\n%s", r.code, c.exit, r.out)
			}
			s := onlyStatus(t, f)
			if s["state"] != c.state || !strings.Contains(s["description"].(string), c.desc) || s["context"] != "cert-review" || s["sha"] != "fake-head-0001" {
				t.Fatalf("status %v, want %s with %q", s, c.state, c.desc)
			}
			if r.outputs["go"] != c.goOn {
				t.Fatalf("go=%q, want %q", r.outputs["go"], c.goOn)
			}
			if r.outputs["bios"] != c.bios {
				t.Fatalf("bios=%q, want %q: seven's canary runs when the wasm or the wasi-slicc tarball differs, and only then", r.outputs["bios"], c.bios)
			}
		})
	}
}

type planCase struct {
	name, action, label, guard, state, build, forced string
	files                                            []map[string]any
	head                                             []map[string]any
	runStatus                                        string
}

func TestCertReviewPlanBuildsOnlyOnTheEventsThatMatter(t *testing.T) {
	files := func(names ...string) []map[string]any {
		out := []map[string]any{}
		for _, n := range names {
			out = append(out, map[string]any{"filename": n})
		}
		return out
	}
	statuses := "/repos/ai-ecoverse/slicc-cli/commits/fake-head-0001/statuses"
	pending := []map[string]any{{"context": "cert-review", "state": "pending", "target_url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/5"}}
	cases := []planCase{
		{name: "a push", action: "synchronize", guard: "PASS", state: "pending", build: "true", forced: "tools/cert-review/lib.mjs,.github/workflows/release.yml", files: files("cmd/slicc/main.go", "tools/cert-review/lib.mjs", ".github/workflows/release.yml")},
		{name: "merge-ok added", action: "labeled", label: "merge-ok", guard: "PASS", state: "pending", build: "true", files: files("cmd/slicc/main.go")},
		{name: "another label", action: "labeled", label: "bug", guard: "PASS", build: "false", files: files("cmd/slicc/main.go")},
		{name: "ready for review", action: "ready_for_review", guard: "PASS", build: "false", files: files("cmd/slicc/main.go")},
		{name: "the self-guard refused", action: "synchronize", guard: "FAIL", state: "failure", build: "false", files: files("tools/cert-review/lib.mjs")},
		{name: "the self-guard didn't finish", action: "synchronize", state: "error", build: "false", files: files("cmd/slicc/main.go")},
		{name: "the approval label on a pull request that doesn't change the reviewer plans again", action: "labeled", label: "cert-review-approved", guard: "PASS", state: "pending", build: "true", files: files("cmd/slicc/main.go")},
		{name: "another label finds a pending status whose run ended", action: "labeled", label: "bug", guard: "PASS", state: "error", build: "false", files: files("cmd/slicc/main.go"), head: pending, runStatus: "completed"},
		{name: "another label leaves a pending status whose run is still going", action: "labeled", label: "bug", guard: "PASS", build: "false", files: files("cmd/slicc/main.go"), head: pending, runStatus: "in_progress"},
	}
	for _, path := range []string{"scripts/npm-publish.mjs", "scripts/package.sh", "scripts/release-plan.mjs", "scripts/go-major.mjs", "scripts/go-warm.sh", "scripts/sliccy-next.mjs", "scripts/npm-stage.mjs", "install.sh", "npm/slicc.js", "npm/wasi-slicc/host/slicc-host.mjs", ".releaserc.json", "package.json", "package-lock.json", ".github/workflows/ci.yml", ".github/workflows/sigstore.yml", ".github/workflows/new-workflow.yml", ".github/actions/some-action/action.yml", ".github/runner.mjs", "tools/cert-review/lib.mjs"} {
		cases = append(cases, planCase{name: "always behaviour: " + path, action: "synchronize", guard: "PASS", state: "pending", build: "true", forced: path, files: files(path, "README.md")})
	}
	for _, path := range []string{"README.md", "cmd/slicc/main.go", "go.mod", "go.sum", "renovate.json", "test/integration/cli_test.go"} {
		cases = append(cases, planCase{name: "by build output: " + path, action: "synchronize", guard: "PASS", state: "pending", build: "true", forced: "", files: files(path)})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			routes := map[string]any{"/repos/ai-ecoverse/slicc-cli/pulls/7/files": c.files, "/repos/ai-ecoverse/slicc-cli/actions/runs/5": map[string]any{"status": c.runStatus}}
			if c.head != nil {
				routes[statuses] = c.head
			}
			f := newCertAPI(t, routes)
			r := certTool(t, f.srv.URL, "pr.mjs", "plan", "--pr", "7", "--head", "fake-head-0001", "--action", c.action, "--label", c.label, "--labels", "merge-ok", "--run", "1", "--guard", c.guard)
			if r.code != 0 {
				t.Fatalf("exit %d:\n%s", r.code, r.out)
			}
			got := f.posted()
			if c.state == "" && len(got) != 0 || c.state != "" && (len(got) != 1 || got[0]["state"] != c.state) {
				t.Fatalf("statuses %v, want %q", got, c.state)
			}
			if r.outputs["build"] != c.build {
				t.Fatalf("build=%q, want %q", r.outputs["build"], c.build)
			}
			if c.build == "true" && r.outputs["forced"] != c.forced {
				t.Fatalf("forced=%q, want %q", r.outputs["forced"], c.forced)
			}
		})
	}
}

func TestCertReviewDeltaNamesTheGoTestsAPullRequestChanges(t *testing.T) {
	patch := strings.Join([]string{
		"@@ -10,6 +10,7 @@ func TestOld(t *testing.T) {",
		" \ta := 1",
		"+\tb := 2",
		" }",
		"@@ -30,2 +31,6 @@ func TestOld(t *testing.T) {",
		"+func TestAdded(t *testing.T) {",
		"+\tcheck(t)",
		"+}",
		"@@ -50,3 +55,3 @@ func check(t *testing.T) {",
		"-\told()",
		"+\tnu()",
		"@@ -70,3 +75,0 @@ func TestOld(t *testing.T) {",
		"-func TestRemoved(t *testing.T) {",
		"-}",
	}, "\n")
	helperPatch := "@@ -5,3 +5,3 @@ func startFake(t *testing.T) {\n-\tlisten(0)\n+\tlisten(1)\n"
	source := "package integration\n\nfunc TestOld(t *testing.T) {}\nfunc TestAdded(t *testing.T) {}\nfunc TestSibling(t *testing.T) {}\nfunc check(t *testing.T) {}\n"
	routes := map[string]any{
		"/repos/ai-ecoverse/slicc-cli/pulls/9": map[string]any{"number": 9, "title": "fix: keep the link up", "head": map[string]any{"sha": "fake-head-0009"}},
		"/repos/ai-ecoverse/slicc-cli/pulls/9/files": []map[string]any{
			{"filename": "internal/link/link.go", "status": "modified", "patch": "@@ -1 +1 @@\n-a\n+b"},
			{"filename": "test/integration/link_test.go", "status": "modified", "patch": patch},
			{"filename": "test/integration/fakes_test.go", "status": "modified", "patch": helperPatch},
			{"filename": "test/integration/gone_test.go", "status": "removed"},
		},
		"/repos/ai-ecoverse/slicc-cli/contents/test/integration/link_test.go":  source,
		"/repos/ai-ecoverse/slicc-cli/contents/test/integration/fakes_test.go": "package integration\n\nfunc startFake(t *testing.T) {}\n",
	}
	f := newCertAPI(t, routes)
	out := filepath.Join(t.TempDir(), "delta.json")
	r := certTool(t, f.srv.URL, "delta.mjs", "--pr", "9", "--out", out)
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
	var delta struct {
		Run string `json:"run"`
		Row struct {
			Kind    string              `json:"kind"`
			Names   []string            `json:"names"`
			Gaps    []string            `json:"gaps"`
			Dropped []map[string]string `json:"dropped"`
		} `json:"row"`
	}
	data, _ := os.ReadFile(out)
	if err := json.Unmarshal(data, &delta); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(delta.Row.Names) != "[TestAdded TestOld TestSibling]" || delta.Run != "^(TestAdded|TestOld|TestSibling)$" || delta.Row.Kind != "fix" {
		t.Fatalf("a changed helper in a file with tests changes every test in it: %s", data)
	}
	if fmt.Sprint(delta.Row.Gaps) != "[helper startFake in test/integration/fakes_test.go]" {
		t.Fatalf("a helper in a file without tests is a gap, never a guess: %s", data)
	}
	dropped := []string{}
	for _, d := range delta.Row.Dropped {
		dropped = append(dropped, d["file"]+" "+d["test"])
	}
	sort.Strings(dropped)
	if fmt.Sprint(dropped) != "[test/integration/gone_test.go  test/integration/link_test.go TestRemoved]" {
		t.Fatalf("dropped %v", dropped)
	}
}

func writeModule(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	files["go.mod"] = "module example.invalid/cert\n\ngo 1.26\n"
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCertReviewTheNegativeFailsOnMainAndPassesOnThePullRequest(t *testing.T) {
	work := t.TempDir()
	code := func(v string) string { return "package p\n\nfunc Answer() int { return " + v + " }\n" }
	test := "package p\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Answer() != 42 {\n\t\tt.Fatal(\"wrong answer\")\n\t}\n}\n\nfunc TestOther(t *testing.T) {}\n"
	newAPI := "package p\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Brand() != 42 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n"
	trees := map[string]map[string]string{
		"main":     {"p/p.go": code("41"), "p/p_test.go": test},
		"fixed":    {"p/p.go": code("42"), "p/p_test.go": test},
		"nochange": {"p/p.go": code("42"), "p/p_test.go": test},
		"newapi":   {"p/p.go": code("42"), "p/p_test.go": newAPI},
	}
	for name, files := range trees {
		writeModule(t, filepath.Join(work, name), files)
	}
	suite := func(tree, run string) string {
		out := filepath.Join(work, "ev", tree)
		r := certTool(t, "", "suite.mjs", "--tree", filepath.Join(work, tree), "--out", out, "--run", run, "--pkg", "./...", "--timeout", "5")
		if !strings.Contains(r.out, "suite: exit") {
			t.Fatalf("suite on %s:\n%s", tree, r.out)
		}
		return out + ".json"
	}
	delta := writeJSON(t, filepath.Join(work, "delta.json"), map[string]any{"row": map[string]any{"pr": 9, "title": "fix: the answer", "kind": "fix", "fix": true, "names": []string{"TestAnswer"}, "tests": []any{}, "gaps": []any{}, "dropped": []any{}}})
	rebuild := writeJSON(t, filepath.Join(work, "rebuild.json"), map[string]any{"match": true, "differ": []string{}, "files": 9})
	candidate := suite("fixed", "")
	decision := func(prev string) string {
		out := filepath.Join(work, "findings.json")
		r := certTool(t, "", "findings.mjs", "--delta", delta, "--candidate", candidate, "--prev", prev, "--rebuild", rebuild, "--out", out)
		if r.code != 0 {
			t.Fatalf("findings:\n%s", r.out)
		}
		var f struct {
			Row struct {
				Decision string `json:"decision"`
			} `json:"row"`
		}
		data, _ := os.ReadFile(out)
		_ = json.Unmarshal(data, &f)
		return f.Row.Decision
	}
	if got := decision(suite("main", "^(TestAnswer)$")); got != "negative" {
		t.Fatalf("a fix whose test fails on main: %s, want negative", got)
	}
	if got := decision(suite("nochange", "^(TestAnswer)$")); got != "no-negative" {
		t.Fatalf("a fix whose test passes on main too: %s, want no-negative", got)
	}
	if got := decision(suite("newapi", "^(TestAnswer)$")); got != "prev-not-run" {
		t.Fatalf("a test that doesn't compile on main: %s, want prev-not-run, never a NEGATIVE", got)
	}
}

type recordCase struct {
	name, key, state, want string
	downloads              string
	bios                   string
	biosOff                string
	canary                 func(r map[string]any)
	noCanary               bool
	bare                   bool
	declare                func(string) string
	artifacts              func(arts []map[string]any) []map[string]any
	verdict                any
	mutate                 func(dir string)
	rebuildMatch           bool
	incomplete             bool
	noIssues               bool
	runs                   []map[string]any
}

func TestCertReviewRecordCertifiesOnlyTheBytesItBuiltWithEveryPieceOfEvidence(t *testing.T) {
	green := []map[string]any{
		{"name": "ci", "status": "completed", "conclusion": "success"},
		{"name": "test (ubuntu-latest, 1/2)", "status": "completed", "conclusion": "success"},
		{"name": "test (macos-latest, 1/1)", "status": "completed", "conclusion": "success"},
		{"name": "test (windows-latest, 1/1)", "status": "completed", "conclusion": "success"},
		{"name": "cert-review-pr / plan", "status": "completed", "conclusion": "success"},
	}
	noMac := []map[string]any{green[0], green[1], green[3]}
	certified := map[string]any{"verdict": "certified", "known": []string{"ai-ecoverse/slicc-cli#5"}}
	cases := []recordCase{
		{name: "everything there", key: "true", state: "success", want: "certified ai-ecoverse/slicc-cli PR #7 at fake-head-00, build 1.2.4-pr.7.1 SHA256SUMS sha256=", verdict: certified, rebuildMatch: true, runs: green},
		{name: "an unchanged wasm says it skipped seven's canary", key: "true", state: "success", want: "; bios: not needed, wasm unchanged; reproducible:", verdict: certified, rebuildMatch: true, runs: green},
		{name: "a new wasm that seven's canary passes", key: "true", state: "success", want: "; bios: follow-chat pass at slicc-bios@" + biosPin(t).Commit + "; reproducible:", bios: "true", verdict: certified, rebuildMatch: true, runs: green},
		{name: "classify said nothing about the wasm", key: "true", state: "error", want: "classify said bios=nothing, so whether bios's canary was needed is unknown", bios: "unset", verdict: certified, rebuildMatch: true, runs: green},
		{name: "classify said neither true nor false about the wasm", key: "true", state: "error", want: "classify said bios=maybe, so whether bios's canary was needed is unknown", bios: "maybe", verdict: certified, rebuildMatch: true, runs: green},
		{name: "a new wasm while the canary is off names the issue", key: "true", state: "success", want: "; bios: off (ai-ecoverse/slicc-bios#600), wasm changed; reproducible:", bios: "true", biosOff: "ai-ecoverse/slicc-bios#600", noCanary: true, verdict: certified, rebuildMatch: true, runs: green},
		{name: "an unchanged wasm while the canary is off needed no canary", key: "true", state: "success", want: "; bios: not needed, wasm unchanged; reproducible:", biosOff: "slicc-bios#600", verdict: certified, rebuildMatch: true, runs: green},
		{name: "the canary off without an issue", key: "true", state: "error", want: "the canary is off without an issue that says why (--bios-off for now)", bios: "true", biosOff: "for now", noCanary: true, verdict: certified, rebuildMatch: true, runs: green},
		{name: "the canary off without an issue, on an unchanged wasm", key: "true", state: "error", want: "the canary is off without an issue that says why (--bios-off for-now)", biosOff: "for-now", verdict: certified, rebuildMatch: true, runs: green},
		{name: "a new wasm without a canary result", key: "true", state: "error", want: "bios: the canary left no result (its job ended skipped)", bios: "true", noCanary: true, verdict: certified, rebuildMatch: true, runs: green},
		{name: "a failing shard", key: "true", state: "failure", want: "bios: 1 failing in follow-chat: follow-slicc.test.mjs › follows a seven (follow-chat)", bios: "true", verdict: certified, rebuildMatch: true, runs: green, canary: func(r map[string]any) {
			r["ok"] = false
			r["counts"].(map[string]any)["pass"] = 1
			r["counts"].(map[string]any)["regression"] = 1
			r["regressions"] = []any{map[string]any{"shard": "follow-chat", "test": "follow-slicc.test.mjs › follows a seven", "log": "fake-log"}}
			r["tests"].([]any)[0].(map[string]any)["verdict"] = "regression"
		}},
		{name: "a flaky smoke", key: "true", state: "error", want: "bios: 1 flaky in follow-chat", bios: "true", verdict: certified, rebuildMatch: true, runs: green, canary: func(r map[string]any) {
			r["counts"].(map[string]any)["flaky"] = 1
		}},
		{name: "the in-kernel slicc's test skipped", key: "true", state: "error", want: "bios: follow-slicc.test.mjs didn't pass", bios: "true", verdict: certified, rebuildMatch: true, runs: green, canary: func(r map[string]any) {
			r["counts"].(map[string]any)["pass"] = 1
			r["counts"].(map[string]any)["skip"] = 1
			r["tests"].([]any)[0].(map[string]any)["verdict"] = "skip"
		}},
		{name: "the pinned shard has no result", key: "true", state: "error", want: "(no result from follow-chat)", bios: "true", verdict: certified, rebuildMatch: true, runs: green, canary: func(r map[string]any) {
			r["missingShards"] = []any{"follow-chat"}
			r["tests"] = []any{}
		}},
		{name: "the canary's missingShards isn't a list", key: "true", state: "error", want: "bios: the shards that ran aren't follow-chat", bios: "true", verdict: certified, rebuildMatch: true, runs: green, canary: func(r map[string]any) {
			r["missingShards"] = map[string]any{"follow-chat": true}
		}},
		{name: "a failing shard whose names aren't a list", key: "true", state: "failure", want: "bios: 1 failing in follow-chat: unnamed", bios: "true", verdict: certified, rebuildMatch: true, runs: green, canary: func(r map[string]any) {
			r["ok"] = false
			r["counts"].(map[string]any)["pass"] = 1
			r["counts"].(map[string]any)["fail"] = 1
			r["regressions"] = map[string]any{"test": "fake"}
			r["failures"] = "fake"
			r["tests"].([]any)[1].(map[string]any)["verdict"] = "fail"
		}},
		{name: "the canary ran another bios", key: "true", state: "error", want: "bios: the canary ran slicc-bios@" + strings.Repeat("f", 40) + ", not the pinned", bios: "true", verdict: certified, rebuildMatch: true, runs: green, canary: func(r map[string]any) {
			r["biosRef"] = strings.Repeat("f", 40)
		}},
		{name: "the canary ran in another run", key: "true", state: "error", want: "bios: the canary's run is https://github.com/ai-ecoverse/slicc-cli/actions/runs/2", bios: "true", verdict: certified, rebuildMatch: true, runs: green, canary: func(r map[string]any) {
			r["url"] = "https://github.com/ai-ecoverse/slicc-cli/actions/runs/2"
		}},
		{name: "the canary took its candidate from another artifact", key: "true", state: "error", want: "bios: the canary didn't take this build's @ai-ecoverse/wasi-slicc from cert-build-wasi-slicc-1.2.4-pr.7.1", bios: "true", verdict: certified, rebuildMatch: true, runs: green, canary: func(r map[string]any) {
			c := r["candidates"].([]any)[0].(map[string]any)
			c["status"] = strings.Replace(c["status"].(string), "cert-build-wasi-slicc", "fake-planted", 1)
		}},
		{name: "the canary ran other bytes", key: "true", state: "failure", want: "bios: the canary ran @ai-ecoverse/wasi-slicc sha256 " + shaHex([]byte("fake-other")), bios: "true", verdict: certified, rebuildMatch: true, runs: green, canary: func(r map[string]any) {
			r["overrides"].([]any)[0].(map[string]any)["sha256"] = shaHex([]byte("fake-other"))
		}},
		{name: "the canary's report under its id has another digest", key: "true", state: "error", want: "bios-canary's digest is sha256:" + shaHex([]byte("fake-forged")), bios: "true", verdict: certified, rebuildMatch: true, runs: green, artifacts: func(arts []map[string]any) []map[string]any {
			for _, a := range arts {
				if a["name"] == "bios-canary" {
					a["digest"] = "sha256:" + shaHex([]byte("fake-forged"))
				}
			}
			return arts
		}},
		{name: "the canary's report didn't download whole", key: "true", state: "error", want: "the bios artifact didn't download whole (failure", bios: "true", verdict: certified, rebuildMatch: true, runs: green, downloads: "verdict=success,build=success,delta=success,rebuild=success,suite-candidate=success,suite-base=success,bios=failure"},
		{name: "swapped bytes", key: "true", state: "failure", want: "slicc-linux-amd64 re-hashes to", verdict: certified, rebuildMatch: true, runs: green, mutate: func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, "release", "slicc-linux-amd64"), []byte("fake-other-bytes"), 0o644)
		}},
		{name: "not reproducible, whatever the reviewer's folder says", key: "true", state: "failure", want: "not reproducible: a git archive of the merge commit builds different slicc-linux-amd64", verdict: certified, rebuildMatch: false, runs: green},
		{name: "no reviewer key", key: "false", state: "error", want: "INCOMPLETE: no reviewer key", verdict: certified, rebuildMatch: true, runs: green},
		{name: "no macOS result in CI", key: "true", state: "error", want: "CI has no macos-latest test run on the head", verdict: certified, rebuildMatch: true, runs: noMac},
		{name: "the reviewer left nothing", key: "true", state: "error", want: "no readable verdict.json", rebuildMatch: true, runs: green},
		{name: "a findings.json in the reviewer's folder can't clear a coded INCOMPLETE", key: "true", state: "error", want: "the candidate suite has no complete result", verdict: certified, rebuildMatch: true, incomplete: true, runs: green},
		{name: "a known issue that doesn't exist", key: "true", state: "error", want: "known issue ai-ecoverse/slicc-cli#404 not found", verdict: map[string]any{"verdict": "certified", "known": []string{"#404"}}, rebuildMatch: true, runs: green},
		{name: "the known-issue list can't be read", key: "true", state: "error", want: "the known issues couldn't be read", verdict: certified, rebuildMatch: true, noIssues: true, runs: green},
		{name: "the reviewer failed it", key: "true", state: "failure", want: "the reviewer: leaks the token", verdict: map[string]any{"verdict": "fail", "reason": "leaks the token"}, rebuildMatch: true, runs: green},
		{name: "a planted artifact that no producer declared", key: "true", state: "error", want: "cert-review-suite-base-1.2.4-pr.7.1 (artifact 299) is in the run, but no producer declared it", verdict: certified, rebuildMatch: true, runs: green, artifacts: func(arts []map[string]any) []map[string]any {
			return append(arts, certArtifact(299, "cert-review-suite-base-1.2.4-pr.7.1"))
		}},
		{name: "an artifact whose digest isn't its producer's", key: "true", state: "error", want: "cert-review-suite-base-1.2.4-pr.7.1's digest is sha256:" + shaHex([]byte("fake-forged")) + ", its producer said sha256:" + shaHex([]byte("cert-review-suite-base-1.2.4-pr.7.1")), verdict: certified, rebuildMatch: true, runs: green, artifacts: func(arts []map[string]any) []map[string]any {
			arts[5]["digest"] = "sha256:" + shaHex([]byte("fake-forged"))
			return arts
		}},
		{name: "an artifact under its producer's id with another name", key: "true", state: "error", want: "artifact 203 is cert-review-delta-1.2.4-pr.7.1, its producer declared cert-review-rebuild-1.2.4-pr.7.1", verdict: certified, rebuildMatch: true, runs: green, artifacts: func(arts []map[string]any) []map[string]any {
			arts[2]["name"] = "cert-review-delta-1.2.4-pr.7.1"
			return arts
		}},
		{name: "no declarations at all", key: "true", state: "error", want: "no producer declared its artifacts", verdict: certified, rebuildMatch: true, runs: green, bare: true},
		{name: "a producer that declared no artifact", key: "true", state: "error", want: "cert-review-delta-1.2.4-pr.7.1 has no artifact from its producer", verdict: certified, rebuildMatch: true, runs: green, declare: func(d string) string {
			return regexp.MustCompile(`cert-review-delta-1\.2\.4-pr\.7\.1=\d+:[0-9a-f]+`).ReplaceAllString(d, "cert-review-delta-1.2.4-pr.7.1=:")
		}},
		{name: "a download that failed its digest check", key: "true", state: "error", want: "the suite-base artifact didn't download whole (failure: a digest mismatch or an unknown id)", verdict: certified, rebuildMatch: true, runs: green, downloads: "verdict=success,build=success,delta=success,rebuild=success,suite-candidate=success,suite-base=failure"},
		{name: "an earlier attempt's leftover is neither evidence nor an alarm", key: "true", state: "success", want: "certified ai-ecoverse/slicc-cli PR #7", verdict: certified, rebuildMatch: true, runs: green, artifacts: func(arts []map[string]any) []map[string]any {
			old := certArtifact(150, "cert-review-suite-base-1.2.4-pr.7.1")
			old["created_at"] = "2026-01-01T09:59:59Z"
			return append(arts, old)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { recordOnce(t, c) })
	}
}

func recordOnce(t *testing.T, c recordCase) {
	dir := t.TempDir()
	art := filepath.Join(dir, "build")
	bins := map[string][]byte{"slicc-linux-amd64": []byte("fake-linux-binary"), "slicc-wasip1-wasm.wasm": []byte("fake-wasm"), "install.sh": []byte("#!/bin/sh\n")}
	names := []string{}
	for n := range bins {
		names = append(names, n)
	}
	sort.Strings(names)
	var sums strings.Builder
	files := map[string]string{}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(art, "release"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(art, "release", n), bins[n], 0o644); err != nil {
			t.Fatal(err)
		}
		files[n] = shaHex(bins[n])
		fmt.Fprintf(&sums, "%s  %s\n", files[n], n)
	}
	if err := os.WriteFile(filepath.Join(art, "release", "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	sumsSHA := shaHex([]byte(sums.String()))
	files["SHA256SUMS"] = sumsSHA
	wasi := []byte("fake-wasi-slicc-tarball")
	wasiSHA := shaHex(wasi)
	if err := os.MkdirAll(filepath.Join(art, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(art, "npm", "ai-ecoverse-wasi-slicc-1.2.4-pr.7.1.tgz"), wasi, 0o644); err != nil {
		t.Fatal(err)
	}
	packages := map[string]any{"@ai-ecoverse/wasi-slicc": map[string]any{"version": "1.2.4-pr.7.1", "file": "ai-ecoverse-wasi-slicc-1.2.4-pr.7.1.tgz", "sha256": wasiSHA}}
	writeJSON(t, filepath.Join(art, "manifest.json"), map[string]any{"version": "1.2.4-pr.7.1", "go": "go version fake", "sums": sumsSHA, "files": files, "packages": packages})
	if c.mutate != nil {
		c.mutate(art)
	}
	review := filepath.Join(dir, "review")
	if c.verdict != nil {
		writeJSON(t, filepath.Join(review, "verdict.json"), c.verdict)
	}
	differ := []string{}
	if !c.rebuildMatch {
		differ = []string{"slicc-linux-amd64"}
	}
	coded := []any{}
	if !c.rebuildMatch {
		coded = append(coded, map[string]any{"kind": "not-reproducible", "differ": differ})
	}
	if c.incomplete {
		coded = append(coded, map[string]any{"kind": "missing-evidence", "what": "the candidate suite", "blocking": true})
	}
	findings := writeJSON(t, filepath.Join(dir, "coded", "findings.json"), map[string]any{
		"row":      map[string]any{"decision": "negative"},
		"rebuild":  map[string]any{"match": c.rebuildMatch, "files": 4, "differ": differ},
		"findings": coded,
	})
	writeJSON(t, filepath.Join(review, "findings.json"), map[string]any{
		"row":      map[string]any{"decision": "negative"},
		"rebuild":  map[string]any{"match": true, "files": 4, "differ": []string{}},
		"findings": []any{},
	})
	routes := map[string]any{
		"/repos/ai-ecoverse/slicc-cli/commits/fake-head-0007/check-runs": map[string]any{"check_runs": c.runs},
		"/repos/ai-ecoverse/slicc-cli/issues":                            []any{},
		"/repos/ai-ecoverse/slicc-cli/issues/5":                          map[string]any{"state": "open"},
	}
	if c.noIssues {
		delete(routes, "/repos/ai-ecoverse/slicc-cli/issues")
	}
	arts := buildArtifacts()
	for i, n := range []string{"cert-review-rebuild", "cert-review-delta", "cert-review-suite-candidate", "cert-review-suite-base", "cert-verdict", "cert-evidence"} {
		arts = append(arts, certArtifact(203+i, n+"-1.2.4-pr.7.1"))
	}
	arts = append(arts, certArtifact(210, "cert-build-wasi-slicc-1.2.4-pr.7.1"))
	declare := declared(arts)
	pin := biosPin(t)
	bios := c.bios
	if bios == "" {
		bios = "false"
	}
	canaryDir := filepath.Join(dir, "bios")
	canaryArtifact := ":"
	if bios == "true" && !c.noCanary {
		report := certArtifact(209, pin.Label+"-canary")
		arts = append(arts, report)
		canaryArtifact = "209:" + strings.TrimPrefix(report["digest"].(string), "sha256:")
		result := map[string]any{
			"ok": true, "run": "canary", "biosRef": pin.Commit, "url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/1", "repeat": 1,
			"overrides":   []any{map[string]any{"name": pin.Package, "version": "1.2.4-pr.7.1", "sha256": wasiSHA}},
			"candidates":  []any{map[string]any{"package": pin.Package + "@1.2.4-pr.7.1", "status": "candidate (artifact 1/cert-build-wasi-slicc-1.2.4-pr.7.1, sha256 " + wasiSHA + ")", "via": "artifact"}},
			"counts":      map[string]any{"pass": 2, "skip": 0, "fail": 0, "regression": 0, "both": 0, "flaky": 0},
			"regressions": []any{}, "failures": []any{}, "both": []any{}, "flaky": []any{},
			"missingShards": []any{}, "unexplainedExits": []any{}, "refused": []any{}, "ambiguous": []any{},
			"tests": []any{
				map[string]any{"shard": "follow-chat", "key": "test/integration/follow-slicc.test.mjs › follows a seven", "test": "follow-slicc.test.mjs › follows a seven", "verdict": "pass"},
				map[string]any{"shard": "follow-chat", "key": "test/integration/chat.test.mjs › chats", "test": "chat.test.mjs › chats", "verdict": "pass"},
			},
			"verdict": "canary: PASS 2 tests",
		}
		if c.canary != nil {
			c.canary(result)
		}
		writeJSON(t, filepath.Join(canaryDir, "result.json"), result)
	}
	if c.declare != nil {
		declare = c.declare(declare)
	}
	if c.artifacts != nil {
		arts = c.artifacts(arts)
	}
	downloads := "verdict=success, build=success, delta=success, rebuild=success, suite-candidate=success, suite-base=success, bios=skipped"
	if c.downloads != "" {
		downloads = c.downloads
	}
	f := newCertAPI(t, withRun(routes, arts))
	out := filepath.Join(dir, "record")
	args := []string{"--version", "1.2.4-pr.7.1", "--pr", "7", "--head", "fake-head-0007", "--commit", "fake-merge-0007", "--run", "https://example.invalid/run/1", "--run-id", "1", "--downloads", downloads}
	if !c.bare {
		args = append(args, "--artifacts", declare)
	}
	if bios != "unset" {
		args = append(args, "--bios", bios, "--bios-job", map[bool]string{true: "success", false: "skipped"}[bios == "true" && !c.noCanary], "--bios-artifact", canaryArtifact, "--bios-dir", canaryDir)
	}
	if c.biosOff != "" {
		args = append(args, "--bios-off", c.biosOff)
	}
	r := certTool(t, f.srv.URL, "record.mjs", append(args, "--artifact", art, "--sums", sumsSHA, "--findings", findings, "--review", review, "--jobs", "build=success,rebuild=success,delta=success,suite=success", "--key", c.key, "--out", out)...)
	var rec struct {
		State string `json:"state"`
		Line  string `json:"line"`
	}
	data, err := os.ReadFile(filepath.Join(out, "record.json"))
	if err != nil {
		t.Fatalf("no record:\n%s", r.out)
	}
	_ = json.Unmarshal(data, &rec)
	if rec.State != c.state || !strings.Contains(rec.Line, c.want) {
		t.Fatalf("state %s, line %q; want %s with %q\n%s", rec.State, rec.Line, c.state, c.want, r.out)
	}
	if (r.code == 0) != (c.state == "success") {
		t.Fatalf("exit %d for %s", r.code, c.state)
	}
	if c.state == "success" && (!strings.Contains(rec.Line, "sha256="+sumsSHA) || !strings.Contains(rec.Line, "known: ai-ecoverse/slicc-cli#5") || !strings.Contains(rec.Line, "wasm sha256="+files["slicc-wasip1-wasm.wasm"])) {
		t.Fatalf("the certified line must carry the SHA256SUMS and wasm hashes and the open known issue: %s", rec.Line)
	}
}

func TestCertReviewMergeGroupIsALookupOfEachPullRequestsStatus(t *testing.T) {
	routes := map[string]any{
		"/repos/ai-ecoverse/slicc-cli/compare/fake-base...fake-group": map[string]any{"commits": []map[string]any{
			{"commit": map[string]any{"message": "Merge pull request #11 from ai-ecoverse/a"}},
			{"commit": map[string]any{"message": "Merge pull request #12 from ai-ecoverse/b"}},
		}},
		"/repos/ai-ecoverse/slicc-cli/pulls/11":                        map[string]any{"head": map[string]any{"sha": "fake-head-0011"}},
		"/repos/ai-ecoverse/slicc-cli/pulls/12":                        map[string]any{"head": map[string]any{"sha": "fake-head-0012"}},
		"/repos/ai-ecoverse/slicc-cli/commits/fake-head-0011/statuses": []map[string]any{{"context": "cert-review", "state": "success", "description": "certified ai-ecoverse/slicc-cli PR #11"}},
		"/repos/ai-ecoverse/slicc-cli/commits/fake-head-0012/statuses": []map[string]any{{"context": "cert-review", "state": "success", "description": "not behaviour: identical build outputs"}},
	}
	f := newCertAPI(t, routes)
	r := certTool(t, f.srv.URL, "pr.mjs", "lookup", "--base", "fake-base", "--head", "fake-group", "--run", "1", "--guard", "PASS")
	s := onlyStatus(t, f)
	if r.code != 0 || s["state"] != "success" || s["sha"] != "fake-group" || !strings.Contains(s["description"].(string), "#12 not behaviour") {
		t.Fatalf("exit %d, status %v\n%s", r.code, s, r.out)
	}
	routes["/repos/ai-ecoverse/slicc-cli/commits/fake-head-0012/statuses"] = []map[string]any{{"context": "cert-review", "state": "failure", "description": "waits for merge-ok"}}
	f = newCertAPI(t, routes)
	r = certTool(t, f.srv.URL, "pr.mjs", "lookup", "--base", "fake-base", "--head", "fake-group", "--run", "1", "--guard", "PASS")
	s = onlyStatus(t, f)
	if r.code != 1 || s["state"] != "failure" || !strings.Contains(s["description"].(string), "#12 has a failure cert-review") {
		t.Fatalf("exit %d, status %v", r.code, s)
	}
}

func jobsOf(t *testing.T, text string) map[string]string {
	t.Helper()
	_, body, ok := strings.Cut(text, "\njobs:\n")
	if !ok {
		t.Fatal("no jobs")
	}
	jobs := map[string]string{}
	re := regexp.MustCompile(`(?m)^  ([a-z][a-z-]*):\n`)
	idx := re.FindAllStringSubmatchIndex(body, -1)
	for i, m := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		jobs[body[m[2]:m[3]]] = body[m[1]:end]
	}
	return jobs
}

func TestCertReviewWorkflowKeepsTheCandidateAwayFromSecretsAndPools(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/cert-review-pr.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	jobs := jobsOf(t, text)
	want := []string{"guard", "plan", "build", "classify", "rebuild", "delta", "suite", "bios", "review", "record", "report", "group"}
	for _, name := range want {
		if _, ok := jobs[name]; !ok {
			t.Fatalf("no %s job", name)
		}
	}
	if len(jobs) != len(want) {
		t.Fatalf("jobs %v", jobs)
	}
	if !strings.Contains(text, "types: [opened, synchronize, reopened, labeled, unlabeled, ready_for_review]\n  merge_group:\n") {
		t.Fatal("triggers")
	}
	if !strings.Contains(text, "format('cert-review-pr-noop-{0}', github.run_id)") {
		t.Fatal("other labels must get a concurrency group of their own")
	}
	if strings.Contains(text, "always()") || strings.Contains(text, "id-token") {
		t.Fatal("no always(), no id-token")
	}
	for _, pool := range []string{"cloud-run-bench", "cloud-run-job"} {
		if strings.Contains(text, pool) {
			t.Fatalf("names the pool %s", pool)
		}
	}
	sameRepo := "github.event.pull_request.head.repo.full_name == github.repository"
	for name, body := range jobs {
		runsOn := regexp.MustCompile(`(?m)^    runs-on: (.*)$`).FindStringSubmatch(body)
		if name == "bios" {
			if runsOn != nil || !strings.Contains(body, "\n    uses: ai-ecoverse/slicc-bios/") {
				t.Errorf("bios is a call of slicc-bios's canary, which picks hosted runners for the candidate's shards")
			}
		} else if runsOn == nil {
			t.Fatalf("%s has no runs-on", name)
		}
		switch name {
		case "bios":
		case "rebuild", "suite":
			if runsOn[1] != "${{ vars.CERT_RUNNER || 'ubuntu-latest' }}" {
				t.Errorf("%s runs on %s, want the heavy pool", name, runsOn[1])
			}
		case "build":
			if runsOn[1] != "ubuntu-latest" {
				t.Errorf("build runs on %s, want hosted", runsOn[1])
			}
		default:
			if runsOn[1] != "${{ vars.CERT_LIGHT_RUNNER || 'ubuntu-latest' }}" {
				t.Errorf("%s runs on %s, want the light pool", name, runsOn[1])
			}
		}
		if name == "build" || name == "rebuild" || name == "suite" || name == "bios" {
			if cond := regexp.MustCompile(`(?m)^    if: (.*)$`).FindStringSubmatch(body); cond == nil || !strings.Contains(cond[1], sameRepo) {
				t.Errorf("%s runs candidate code, so it must check the same-repo rule itself", name)
			}
		}
		if strings.Contains(body, "statuses: write") != (name == "plan" || name == "classify" || name == "report" || name == "group") {
			t.Errorf("%s: statuses: write only where a status is posted", name)
		}
		if strings.Contains(body, "pull-requests: write") != (name == "guard" || name == "report") {
			t.Errorf("%s: pull-requests: write only in guard and report", name)
		}
		if strings.Contains(body, "secrets.") != (name == "review") {
			t.Errorf("%s reads a secret", name)
		}
		if name != "guard" && strings.Contains(body, "actions/checkout@") && !strings.Contains(body, "persist-credentials: false") {
			t.Errorf("%s keeps the checkout's credentials", name)
		}
		for _, cond := range regexp.MustCompile(`(?m)^    if: (.*)$`).FindAllStringSubmatch(body, -1) {
			if strings.Contains(cond[1], "!cancelled()") && !strings.HasPrefix(cond[1], "${{") {
				t.Errorf("%s: an if with !cancelled() must be an explicit expression", name)
			}
		}
	}
	review := jobs["review"]
	if strings.Count(review, "secrets.AWS_BEARER_TOKEN_BEDROCK") != 2 || !strings.Contains(review, "PRESENT: ${{ secrets.AWS_BEARER_TOKEN_BEDROCK != '' }}") || !strings.Contains(review, "if: steps.key.outputs.present == 'true'\n        uses: anthropics/claude-code-action/base-action@") {
		t.Fatal("the Bedrock key is only checked for and used in the review step, which skips without it")
	}
	if !strings.Contains(jobs["record"], `--key "${KEY:-false}"`) {
		t.Fatal("record must hear that the key was missing, and say INCOMPLETE")
	}
	if strings.Contains(text, "gh.sh") || strings.Contains(text, "cli/cli") || !strings.Contains(review, "if ! command -v gh > /dev/null; then") {
		t.Fatal("no job downloads gh: the review uses the runner's own, or is INCOMPLETE")
	}
	if strings.Contains(review, "/cert-review/findings.json") || !strings.Contains(jobs["record"], "node tools/cert-review/findings.mjs") || !strings.Contains(jobs["record"], `--findings "$RUNNER_TEMP/findings.json"`) || !strings.Contains(jobs["record"], "node tools/cert-review/gather.mjs") {
		t.Fatal("record recomputes the findings from the code-made evidence, never from the reviewer's folder")
	}
	for name, body := range jobs {
		if name != "rebuild" && name != "delta" && name != "suite" && strings.Contains(body, "\n          name: cert-review-") {
			t.Errorf("%s uploads into the code-made evidence (cert-review-*)", name)
		}
	}
	if !strings.Contains(jobs["group"], "          ref: ${{ github.event.merge_group.base_ref }}\n") {
		t.Fatal("the merge group's tools come from main (base_ref), never from the group's speculative parent")
	}
	workflows, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(workflows) == 0 {
		t.Fatalf("workflows: %v", err)
	}
	for _, w := range workflows {
		data, err := os.ReadFile(w)
		if err != nil {
			t.Fatal(err)
		}
		if m := regexp.MustCompile(`ref:\s*\$\{\{[^}]*(merge_group\.base_sha|merge_group\.head_sha|pull_request\.head\.)`).FindSubmatch(data); m != nil {
			t.Errorf("%s checks out %s: tools come from main or the pull request's base, never from a speculative group commit or the code under test", filepath.Base(w), m[1])
		}
	}
	if strings.Contains(jobs["group"], "claude") || strings.Contains(jobs["group"], "package.sh") {
		t.Fatal("the merge group only looks up")
	}
	lib, err := os.ReadFile(certTools + "lib.mjs")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)export const REVIEWER_GLOBS = \[(.*?)\];`).FindSubmatch(lib)
	if m == nil {
		t.Fatal("no REVIEWER_GLOBS")
	}
	globs := regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(m[1], -1)
	paths := []string{}
	for _, g := range globs {
		paths = append(paths, "            "+string(g[1]))
	}
	if !strings.Contains(jobs["guard"], "          paths: |\n"+strings.Join(paths, "\n")+"\n") {
		t.Fatal("the self-guard's paths must equal REVIEWER_GLOBS")
	}
	if !strings.Contains(jobs["guard"], "        uses: ai-ecoverse/slicc-shared-web/.github/actions/cert-self-guard@f342d7cf558c817ce517bbbbc4cc46df6eac2f97\n") {
		t.Fatal("the self-guard is slicc-shared-web v1.24.2 or later, whose globs cross dot segments: an older pin lets a pull request touching tools/cert-review/a/.b/c.mjs pass without cert-review-approved")
	}
}

type biosPinned struct {
	Repository string   `json:"repository"`
	Workflow   string   `json:"workflow"`
	Commit     string   `json:"commit"`
	Package    string   `json:"package"`
	Label      string   `json:"label"`
	Shards     []string `json:"shards"`
	Tests      []string `json:"tests"`
	Repeat     int      `json:"repeat"`
}

func biosPin(t *testing.T) biosPinned {
	t.Helper()
	data, err := os.ReadFile(certTools + "bios.json")
	if err != nil {
		t.Fatal(err)
	}
	var pin biosPinned
	if err := json.Unmarshal(data, &pin); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(pin.Commit) || pin.Repository != "ai-ecoverse/slicc-bios" || pin.Package != "@ai-ecoverse/wasi-slicc" || len(pin.Shards) == 0 || len(pin.Tests) == 0 || pin.Repeat < 1 {
		t.Fatalf("bios.json pins slicc-bios by commit, the wasi-slicc package, its shards and tests: %s", data)
	}
	return pin
}

func TestCertReviewRunsSevensCanaryOnANewWasmOnly(t *testing.T) {
	pin := biosPin(t)
	_, jobs, _ := workflowSteps(t, "cert-review-pr.yml")
	bios, ok := jobs["bios"]
	if !ok {
		t.Fatal("no bios job: a pull request that changes the wasm is certified without running it inside seven")
	}
	shards, _ := json.Marshal(pin.Shards)
	for _, line := range []string{
		"    needs: [plan, build, classify]\n",
		"    if: ${{ !cancelled() && needs.classify.outputs.go == 'true' && needs.classify.outputs.bios == 'true' && github.event.pull_request.head.repo.full_name == github.repository }}\n",
		"    permissions:\n      contents: read\n      actions: read\n    uses: ",
		"    uses: " + pin.Repository + "/" + pin.Workflow + "@" + pin.Commit + "\n",
		"      bios-ref: " + pin.Commit + "\n",
		"      shards: '" + string(shards) + "'\n",
		fmt.Sprintf("      repeat: %d\n", pin.Repeat),
		"      label: " + pin.Label + "\n",
		"      candidate-package: '" + pin.Package + "'\n",
		"      candidate-artifact: cert-build-wasi-slicc-${{ needs.plan.outputs.version }}\n",
		"      candidate-run: ${{ github.run_id }}\n",
		"      candidate-repo: ${{ github.repository }}\n",
		"      candidate-sha256: ${{ needs.build.outputs.wasi-sha256 }}\n",
		"      candidate-artifact-id: ${{ needs.build.outputs.wasi-id }}\n",
		"      candidate-artifact-digest: ${{ needs.build.outputs.wasi-digest }}\n",
		"      candidate-binding: strict\n",
	} {
		if !strings.Contains(bios, line) {
			t.Errorf("the bios job lacks %q:\n%s", line, bios)
		}
	}
	if strings.Contains(bios, "secrets") || strings.Contains(bios, "write") {
		t.Error("the canary needs no secret and no write: none is passed")
	}
	build := jobs["build"]
	for _, line := range []string{
		"      wasi-sha256: ${{ matrix.side == 'candidate' && steps.manifest.outputs.wasi || '' }}\n",
		`echo "wasi=$(node -p 'require(process.argv[1]).packages["` + pin.Package + `"].sha256' "$RUNNER_TEMP/out/manifest.json")" >> "$GITHUB_OUTPUT"`,
		"      - id: wasi\n",
		"        if: matrix.side == 'candidate'\n",
		"          path: ${{ runner.temp }}/out/npm/ai-ecoverse-wasi-slicc-${{ needs.plan.outputs.version }}.tgz\n",
	} {
		if !strings.Contains(build, line) {
			t.Errorf("build hands the canary its own wasi-slicc tarball and sha256: no %q", line)
		}
	}
	lib, err := os.ReadFile(certTools + "lib.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lib), "export const BIOS_OUTPUTS = ['slicc-wasip1-wasm.wasm', 'npm "+pin.Package+"'];") || !strings.Contains(jobs["classify"], "      bios: ${{ steps.classify.outputs.bios }}\n") {
		t.Error("classify says whether the wasm or the wasi-slicc tarball differs")
	}
	record := jobs["record"]
	for _, line := range []string{
		"    needs: [plan, build, classify, rebuild, delta, suite, review, bios]\n",
		"      BIOS: ${{ needs.classify.outputs.bios }}\n",
		"      BIOS_JOB: ${{ needs.bios.result }}\n",
		"      BIOS_ARTIFACT: ${{ needs.bios.outputs.artifact-id }}:${{ needs.bios.outputs.artifact-digest }}\n",
		"          artifact-ids: ${{ needs.bios.outputs.artifact-id }}\n          path: ${{ runner.temp }}/bios\n",
		"bios=${{ steps.dl-bios.outcome }}",
		`--bios "$BIOS" --bios-job "$BIOS_JOB" --bios-artifact "$BIOS_ARTIFACT"`,
		`--bios-dir "$RUNNER_TEMP/bios"`,
	} {
		if !strings.Contains(record, line) {
			t.Errorf("record reads the canary's report by its id and digest: no %q", line)
		}
	}
	if strings.Contains(record, "bios=${{ needs.bios.result }}") {
		t.Error("a failing canary is a FAIL, not a job that didn't finish")
	}
}

func TestCertReviewClassifiesOnlyTheBuildsItsProducersDeclared(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"SHA256SUMS": "fake-sums-1", "slicc-linux-amd64": "fake-bin-1", "slicc-wasip1-wasm.wasm": "fake-wasm-1"}
	base := writeJSON(t, filepath.Join(dir, "base.json"), manifestOf("fake-sums-1", files, map[string]string{"@ai-ecoverse/slicc": "fake-tgz-1"}))
	forged := writeJSON(t, filepath.Join(dir, "forged.json"), manifestOf("fake-sums-1", files, map[string]string{"@ai-ecoverse/slicc": "fake-tgz-1"}))
	cases := []struct {
		name, want string
		mutate     func(arts []map[string]any) []map[string]any
	}{
		{"a planted artifact", "cert-review-suite-base-1.2.4-pr.7.1 (artifact 299) is in the run, but no producer declared it", func(arts []map[string]any) []map[string]any {
			return append(arts, certArtifact(299, "cert-review-suite-base-1.2.4-pr.7.1"))
		}},
		{"a base build whose digest isn't its producer's", "cert-build-base-1.2.4-pr.7.1's digest is sha256:" + shaHex([]byte("fake-forged")), func(arts []map[string]any) []map[string]any {
			arts[1]["digest"] = "sha256:" + shaHex([]byte("fake-forged"))
			return arts
		}},
		{"the run's artifacts can't be listed", "the run's artifacts couldn't be listed", nil},
		{"no declarations at all", "no producer declared its artifacts", func(arts []map[string]any) []map[string]any { return arts }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			arts := buildArtifacts()
			args := []string{"classify", "--pr", "7", "--head", "fake-head-0001", "--labels", "merge-ok", "--run", "1", "--base-manifest", base, "--candidate-manifest", forged}
			if !strings.HasPrefix(c.name, "no declarations") {
				args = append(args, "--artifacts", declared(arts))
			}
			routes := map[string]any{}
			if c.mutate != nil {
				routes = withRun(routes, c.mutate(arts))
			}
			f := newCertAPI(t, routes)
			r := certTool(t, f.srv.URL, "pr.mjs", args...)
			s := onlyStatus(t, f)
			if r.code != 1 || s["state"] != "error" || !strings.HasPrefix(s["description"].(string), "INCOMPLETE: ") || !strings.Contains(r.out, c.want) || r.outputs["go"] != "" {
				t.Fatalf("identical builds from unbound artifacts must never be not behaviour: exit %d, status %v\n%s", r.code, s, r.out)
			}
		})
	}
}

func TestCertReviewGathersEachProducersOwnEvidenceOnly(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "art")
	put := func(name, body string) {
		p := filepath.Join(in, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("delta/delta.json", `{"row":"fake-delta"}`)
	put("rebuild/rebuild.json", `{"match":true}`)
	put("suite-candidate/candidate.json", `{"fake":"candidate"}`)
	put("suite-candidate/candidate.log", "fake-candidate-log")
	put("suite-candidate/prev.json", `{"fake":"forged negative"}`)
	put("suite-candidate/rebuild.json", `{"fake":"forged rebuild"}`)
	put("suite-base/prev.json", `{"fake":"main"}`)
	put("suite-base/prev.retry.jsonl", "fake-retry")
	out := filepath.Join(dir, "evidence")
	r := certTool(t, "", "gather.mjs", "--in", in, "--downloads", "delta=success, rebuild=failure, suite-candidate=success, suite-base=success", "--out", out)
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
	want := map[string]string{"delta.json": `{"row":"fake-delta"}`, "candidate.json": `{"fake":"candidate"}`, "prev.json": `{"fake":"main"}`, "logs/candidate.log": "fake-candidate-log", "logs/prev.retry.jsonl": "fake-retry"}
	got := map[string]string{}
	_ = filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(out, p)
			data, _ := os.ReadFile(p)
			got[filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("a producer's artifact gives only its own evidence, and a failed download none:\n got %v\nwant %v\n%s", got, want, r.out)
	}
}

type workflowStep struct{ job, text string }

func workflowSteps(t *testing.T, file string) (string, map[string]string, []workflowStep) {
	t.Helper()
	raw, err := os.ReadFile("../../.github/workflows/" + file)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	jobs := jobsOf(t, text)
	steps := []workflowStep{}
	for name, body := range jobs {
		parts := strings.Split(body, "\n      - ")
		for _, p := range parts[1:] {
			steps = append(steps, workflowStep{name, "        " + p})
		}
	}
	return text, jobs, steps
}

func stepField(step, key string) string {
	m := regexp.MustCompile(`(?m)^ {8}(?:  )?` + regexp.QuoteMeta(key) + `: (.*)$`).FindStringSubmatch(step)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestCertWorkflowsBindEveryArtifactByIdAndDigest(t *testing.T) {
	binders := map[string]map[string]string{
		"cert-review-pr.yml": {"record": "record", "classify": "build"},
		"cert-verify.yml":    {"verify": "verify"},
	}
	for file, bind := range binders {
		t.Run(file, func(t *testing.T) {
			text, jobs, steps := workflowSteps(t, file)
			for _, banned := range []string{"overwrite:", "pattern:", "merge-multiple:"} {
				if strings.Contains(text, banned) {
					t.Errorf("%s has %q: every upload is new and every download is by id", file, banned)
				}
			}
			uploads := map[string][]string{}
			for _, s := range steps {
				switch {
				case strings.Contains(s.text, "uses: actions/download-artifact@"):
					ids := regexp.MustCompile(`(?m)^ {10}artifact-ids: \$\{\{ (\S+) \}\}$`).FindStringSubmatch(s.text)
					if ids == nil || stepField(s.text, "name") != "" || stepField(s.text, "digest-mismatch") != "error" {
						t.Errorf("%s downloads without artifact-ids and digest-mismatch: error, or by name:\n%s", s.job, s.text)
						continue
					}
					if stepField(s.text, "if") != ids[1]+" != ''" {
						t.Errorf("%s: an empty artifact-ids downloads every artifact in the run, so the step needs if: %s != ''", s.job, ids[1])
					}
					if !strings.HasPrefix(ids[1], "needs.") {
						t.Errorf("%s downloads %s, not a producer job's output", s.job, ids[1])
					}
					if stepField(s.text, "continue-on-error") == "true" {
						id := stepField(s.text, "id")
						if id == "" || !strings.Contains(jobs[s.job], "${{ steps."+id+".outcome }}") {
							t.Errorf("%s goes on after a failed download without reading its outcome:\n%s", s.job, s.text)
						}
					}
				case strings.Contains(s.text, "uses: actions/upload-artifact@"):
					id := stepField(s.text, "id")
					name := stepField(s.text, "name")
					if id == "" || !strings.HasPrefix(name, "cert-") {
						t.Errorf("%s uploads %q without an id, or outside cert-*", s.job, name)
						continue
					}
					outputs := regexp.MustCompile(`(?m)^      ([a-z-]+): (.*steps\.`+regexp.QuoteMeta(id)+`\.outputs\.artifact-(id|digest).*)$`).FindAllStringSubmatch(jobs[s.job], -1)
					matrix := regexp.MustCompile(`\$\{\{ matrix\.(\w+) \}\}`).FindStringSubmatch(name)
					sides := []string{""}
					if matrix != nil {
						m := regexp.MustCompile(`(?m)^        ` + matrix[1] + `: \[(.*)\]$`).FindStringSubmatch(jobs[s.job])
						if m == nil {
							t.Fatalf("%s has no matrix %s", s.job, matrix[1])
						}
						sides = strings.Split(m[1], ", ")
					}
					for _, side := range sides {
						key := map[string]string{}
						for _, o := range outputs {
							if matrix == nil || strings.Contains(o[2], "matrix."+matrix[1]+" == '"+side+"' && ") {
								key[o[3]] = o[1]
							}
						}
						if key["id"] == "" || key["digest"] == "" {
							t.Errorf("%s doesn't hand on %s's artifact-id and artifact-digest for %q as its own outputs", s.job, name, side)
							continue
						}
						full := name
						if matrix != nil {
							full = strings.ReplaceAll(name, "${{ matrix."+matrix[1]+" }}", side)
						}
						uploads[s.job] = append(uploads[s.job], full+"=${{ needs."+s.job+".outputs."+key["id"]+" }}:${{ needs."+s.job+".outputs."+key["digest"]+" }}")
					}
				}
			}
			for binder, of := range bind {
				body := jobs[binder]
				if !strings.Contains(body, "      actions: read\n") || !strings.Contains(body, `--artifacts "$ARTIFACTS"`) {
					t.Errorf("%s must list the run's artifacts (actions: read) and get every declared one (--artifacts)", binder)
				}
				want := []string{}
				for job, decl := range uploads {
					if job != binder && (of == binder || job == of) {
						want = append(want, decl...)
					}
				}
				if len(want) == 0 {
					t.Errorf("%s binds nothing", binder)
				}
				for _, d := range want {
					if !strings.Contains(body, d) {
						t.Errorf("%s doesn't declare %s", binder, d)
					}
				}
			}
			if file == "cert-review-pr.yml" {
				record := jobs["record"]
				for _, flag := range []string{`--run-id "$GITHUB_RUN_ID"`, `--downloads "$DOWNLOADS"`, `node tools/cert-review/gather.mjs --in "$RUNNER_TEMP/art" --downloads "$DOWNLOADS" --out "$ev"`} {
					if !strings.Contains(record, flag) {
						t.Errorf("record lacks %s", flag)
					}
				}
				if !strings.Contains(jobs["review"], `node tools/cert-review/gather.mjs --in "$RUNNER_TEMP/art" --downloads "$DOWNLOADS" --out "$ev"`) {
					t.Error("the review reads each producer's own evidence only, through gather")
				}
				for _, job := range []string{"review", "record"} {
					for _, s := range steps {
						if s.job != job || !strings.Contains(s.text, "uses: actions/download-artifact@") || !strings.Contains(s.text, "/art/") {
							continue
						}
						id := strings.TrimPrefix(stepField(s.text, "id"), "dl-")
						if !strings.Contains(jobs[job], id+"=${{ steps.dl-"+id+".outcome }}") {
							t.Errorf("%s doesn't tell gather and record how %s downloaded", job, id)
						}
					}
				}
			}
		})
	}
}
