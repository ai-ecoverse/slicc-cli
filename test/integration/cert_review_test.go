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
	same := writeJSON(t, filepath.Join(dir, "base.json"), manifestOf("fake-sums-1", files, map[string]string{"@ai-ecoverse/slicc": "fake-tgz-1"}))
	identical := writeJSON(t, filepath.Join(dir, "identical.json"), manifestOf("fake-sums-1", files, map[string]string{"@ai-ecoverse/slicc": "fake-tgz-1"}))
	readme := writeJSON(t, filepath.Join(dir, "readme.json"), manifestOf("fake-sums-1", files, map[string]string{"@ai-ecoverse/slicc": "fake-tgz-2"}))
	wasm := map[string]string{"SHA256SUMS": "fake-sums-2", "slicc-linux-amd64": "fake-bin-1", "slicc-wasip1-wasm.wasm": "fake-wasm-2"}
	code := writeJSON(t, filepath.Join(dir, "code.json"), manifestOf("fake-sums-2", wasm, map[string]string{"@ai-ecoverse/slicc": "fake-tgz-1"}))
	cases := []struct {
		name, candidate, labels, forced, state, desc, goOn string
		exit                                               int
	}{
		{"a dev pin that ships nothing new", identical, "", "", "success", "not behaviour: identical build outputs", "false", 0},
		{"the same, with merge-ok", identical, "merge-ok", "", "success", "not behaviour", "false", 0},
		{"a README change that changes a tarball", readme, "", "", "failure", "waits for merge-ok to be reviewed (changes npm @ai-ecoverse/slicc)", "false", 0},
		{"a code change", code, "", "", "failure", "changes SHA256SUMS, slicc-wasip1-wasm.wasm", "false", 0},
		{"a code change with merge-ok", code, "bug,merge-ok", "", "pending", "reviewing #7 at fake-head-00", "true", 0},
		{"release.yml with identical outputs", identical, "", ".github/workflows/release.yml", "failure", "changes .github/workflows/release.yml", "false", 0},
		{"no candidate build", filepath.Join(dir, "missing.json"), "merge-ok", "", "error", "couldn't build the pull request", "", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCertAPI(t, nil)
			r := certTool(t, f.srv.URL, "pr.mjs", "classify", "--pr", "7", "--head", "fake-head-0001", "--labels", c.labels, "--forced", c.forced, "--run", "1", "--base-manifest", same, "--candidate-manifest", c.candidate)
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
		})
	}
}

func TestCertReviewPlanBuildsOnlyOnTheEventsThatMatter(t *testing.T) {
	routes := map[string]any{"/repos/ai-ecoverse/slicc-cli/pulls/7/files": []map[string]any{{"filename": "cmd/slicc/main.go"}, {"filename": "tools/cert-review/lib.mjs"}, {"filename": ".github/workflows/release.yml"}}}
	cases := []struct {
		name, action, label, guard, state, build string
	}{
		{"a push", "synchronize", "", "PASS", "pending", "true"},
		{"merge-ok added", "labeled", "merge-ok", "PASS", "pending", "true"},
		{"another label", "labeled", "bug", "PASS", "", "false"},
		{"ready for review", "ready_for_review", "", "PASS", "", "false"},
		{"the self-guard refused", "synchronize", "", "FAIL", "failure", "false"},
		{"the self-guard didn't finish", "synchronize", "", "", "error", "false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
			if c.build == "true" && r.outputs["forced"] != "tools/cert-review/lib.mjs,.github/workflows/release.yml" {
				t.Fatalf("forced=%q", r.outputs["forced"])
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
	verdict                any
	mutate                 func(dir string)
	rebuildMatch           bool
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
		{name: "swapped bytes", key: "true", state: "failure", want: "slicc-linux-amd64 re-hashes to", verdict: certified, rebuildMatch: true, runs: green, mutate: func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, "release", "slicc-linux-amd64"), []byte("fake-other-bytes"), 0o644)
		}},
		{name: "not reproducible", key: "true", state: "failure", want: "not reproducible: a git archive of the merge commit builds different slicc-linux-amd64", verdict: certified, rebuildMatch: false, runs: green},
		{name: "no reviewer key", key: "false", state: "error", want: "INCOMPLETE: no reviewer key", verdict: certified, rebuildMatch: true, runs: green},
		{name: "no macOS result in CI", key: "true", state: "error", want: "CI has no macos-latest test run on the head", verdict: certified, rebuildMatch: true, runs: noMac},
		{name: "the reviewer left nothing", key: "true", state: "error", want: "no readable verdict.json", rebuildMatch: true, runs: green},
		{name: "the reviewer failed it", key: "true", state: "failure", want: "the reviewer: leaks the token", verdict: map[string]any{"verdict": "fail", "reason": "leaks the token"}, rebuildMatch: true, runs: green},
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
	writeJSON(t, filepath.Join(art, "manifest.json"), map[string]any{"version": "1.2.4-pr.7.1", "go": "go version fake", "sums": sumsSHA, "files": files, "packages": map[string]any{}})
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
	findings := writeJSON(t, filepath.Join(review, "findings.json"), map[string]any{
		"row":      map[string]any{"decision": "negative"},
		"rebuild":  map[string]any{"match": c.rebuildMatch, "files": 4, "differ": differ},
		"findings": map[bool][]any{true: {}, false: {map[string]any{"kind": "not-reproducible", "differ": differ}}}[c.rebuildMatch],
	})
	f := newCertAPI(t, map[string]any{
		"/repos/ai-ecoverse/slicc-cli/commits/fake-head-0007/check-runs": map[string]any{"check_runs": c.runs},
		"/repos/ai-ecoverse/slicc-cli/issues":                            []any{},
		"/repos/ai-ecoverse/slicc-cli/issues/5":                          map[string]any{"state": "open"},
	})
	out := filepath.Join(dir, "record")
	r := certTool(t, f.srv.URL, "record.mjs", "--version", "1.2.4-pr.7.1", "--pr", "7", "--head", "fake-head-0007", "--commit", "fake-merge-0007", "--run", "https://example.invalid/run/1", "--artifact", art, "--sums", sumsSHA, "--findings", findings, "--review", review, "--jobs", "build=success,rebuild=success,delta=success,suite=success", "--key", c.key, "--out", out)
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
	want := []string{"guard", "plan", "build", "classify", "rebuild", "delta", "suite", "review", "record", "report", "group"}
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
		if runsOn == nil {
			t.Fatalf("%s has no runs-on", name)
		}
		switch name {
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
		if name == "build" || name == "rebuild" || name == "suite" {
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
}
