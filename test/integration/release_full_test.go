package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type ghCtx map[string]any

type ghParser struct {
	t    *testing.T
	src  string
	pos  int
	ctx  ghCtx
	expr string
}

func ghTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	}
	return true
}

func ghString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		return strconv.FormatBool(x)
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

func ghEqual(a, b any) bool {
	if sa, ok := a.(string); ok {
		if sb, ok := b.(string); ok {
			return strings.EqualFold(sa, sb)
		}
	}
	return a == b
}

func (p *ghParser) skip() {
	for p.pos < len(p.src) && strings.ContainsRune(" \t\n\r", rune(p.src[p.pos])) {
		p.pos++
	}
}

func (p *ghParser) eat(tok string) bool {
	p.skip()
	if strings.HasPrefix(p.src[p.pos:], tok) {
		p.pos += len(tok)
		return true
	}
	return false
}

func (p *ghParser) or() any {
	v := p.and()
	for p.eat("||") {
		r := p.and()
		if !ghTruthy(v) {
			v = r
		}
	}
	return v
}

func (p *ghParser) and() any {
	v := p.cmp()
	for p.eat("&&") {
		r := p.cmp()
		if ghTruthy(v) {
			v = r
		}
	}
	return v
}

func (p *ghParser) cmp() any {
	v := p.unary()
	switch {
	case p.eat("=="):
		return ghEqual(v, p.unary())
	case p.eat("!="):
		return !ghEqual(v, p.unary())
	}
	return v
}

func (p *ghParser) unary() any {
	p.skip()
	if strings.HasPrefix(p.src[p.pos:], "!") && !strings.HasPrefix(p.src[p.pos:], "!=") {
		p.pos++
		return !ghTruthy(p.unary())
	}
	return p.primary()
}

var ghIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*`)

func (p *ghParser) primary() any {
	p.skip()
	if p.eat("(") {
		v := p.or()
		if !p.eat(")") {
			p.t.Fatalf("unbalanced ( in %q", p.expr)
		}
		return v
	}
	if p.pos < len(p.src) && p.src[p.pos] == '\'' {
		end := p.pos + 1
		var b strings.Builder
		for {
			if end >= len(p.src) {
				p.t.Fatalf("unterminated string in %q", p.expr)
			}
			if p.src[end] == '\'' {
				if end+1 < len(p.src) && p.src[end+1] == '\'' {
					b.WriteByte('\'')
					end += 2
					continue
				}
				break
			}
			b.WriteByte(p.src[end])
			end++
		}
		p.pos = end + 1
		return b.String()
	}
	name := ghIdent.FindString(p.src[p.pos:])
	if name == "" {
		p.t.Fatalf("can't parse %q at %d in %q", p.src[p.pos:], p.pos, p.expr)
	}
	p.pos += len(name)
	switch name {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if p.eat("(") {
		var args []any
		for !p.eat(")") {
			args = append(args, p.or())
			p.eat(",")
		}
		switch name {
		case "fromJSON":
			var v any
			if err := json.Unmarshal([]byte(ghString(args[0])), &v); err != nil {
				p.t.Fatalf("fromJSON in %q: %v", p.expr, err)
			}
			return v
		case "startsWith":
			return strings.HasPrefix(strings.ToLower(ghString(args[0])), strings.ToLower(ghString(args[1])))
		case "always":
			return true
		case "cancelled":
			return p.ctx["cancelled()"] == true
		}
		p.t.Fatalf("unknown function %s in %q", name, p.expr)
	}
	v, ok := p.ctx[name]
	if !ok {
		p.t.Fatalf("%q uses %s, which the test context doesn't set", p.expr, name)
	}
	return v
}

func ghEval(t *testing.T, expr string, ctx ghCtx) any {
	t.Helper()
	p := &ghParser{t: t, src: expr, ctx: ctx, expr: expr}
	v := p.or()
	p.skip()
	if p.pos != len(p.src) {
		t.Fatalf("trailing %q in %q", p.src[p.pos:], expr)
	}
	return v
}

func ghTemplate(t *testing.T, tmpl string, ctx ghCtx) any {
	t.Helper()
	tmpl = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(tmpl), ">-"))
	if strings.HasPrefix(tmpl, "${{") && strings.HasSuffix(tmpl, "}}") && strings.Count(tmpl, "${{") == 1 {
		return ghEval(t, strings.TrimSpace(tmpl[3:len(tmpl)-2]), ctx)
	}
	if !strings.Contains(tmpl, "${{") {
		return ghEval(t, tmpl, ctx)
	}
	out := ""
	for {
		start := strings.Index(tmpl, "${{")
		if start < 0 {
			return out + tmpl
		}
		end := strings.Index(tmpl[start:], "}}")
		if end < 0 {
			t.Fatalf("unterminated expression in %q", tmpl)
		}
		out += tmpl[:start] + ghString(ghEval(t, strings.TrimSpace(tmpl[start+3:start+end]), ctx))
		tmpl = tmpl[start+end+2:]
	}
}

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../../.github/workflows/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

func foldedValue(t *testing.T, body, key string) string {
	t.Helper()
	at := strings.Index(body, "\n"+key+" >-\n")
	if at < 0 {
		t.Fatalf("no %q block in:\n%s", key, body)
	}
	indent := strings.Repeat(" ", len(key)-len(strings.TrimLeft(key, " "))+2)
	var parts []string
	for _, line := range strings.Split(body[at+len("\n"+key+" >-\n"):], "\n") {
		if !strings.HasPrefix(line, indent) {
			break
		}
		parts = append(parts, strings.TrimSpace(line))
	}
	return strings.Join(parts, " ")
}

func lineWith(t *testing.T, body, marker string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, marker) {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no line with %q in:\n%s", marker, body)
	return ""
}

type ciLeg struct {
	os, shard    string
	race, guards bool
}

func ciMatrix(t *testing.T, ctx ghCtx) ([]ciLeg, []string) {
	t.Helper()
	jobs := workflowJobs(t, readWorkflow(t, "ci.yml"))
	test := jobs["test"].body
	include, ok := ghTemplate(t, foldedValue(t, test, "        include:"), ctx).([]any)
	if !ok {
		t.Fatal("ci.yml's test matrix is not a list")
	}
	run := strings.TrimPrefix(lineWith(t, test, "sh scripts/verdict.sh test-shard"), "run: ")
	guard := strings.TrimPrefix(lineWith(t, test, "SLICC_GUARD_VECTORS:"), "SLICC_GUARD_VECTORS: ")
	var legs []ciLeg
	for _, raw := range include {
		m := raw.(map[string]any)
		leg := ghCtx{}
		for k, v := range ctx {
			leg[k] = v
		}
		leg["matrix.os"], leg["matrix.shard"] = m["os"], m["shard"]
		cmd := ghString(ghTemplate(t, run, leg))
		legs = append(legs, ciLeg{
			os:     ghString(m["os"]),
			shard:  ghString(m["shard"]),
			race:   strings.Contains(" "+cmd+" ", " -race "),
			guards: ghString(ghTemplate(t, guard, leg)) == "1",
		})
	}
	installers, ok := ghTemplate(t, foldedValue(t, jobs["installer"].body, "        include:"), ctx).([]any)
	if !ok {
		t.Fatal("ci.yml's installer matrix is not a list")
	}
	var names []string
	for _, raw := range installers {
		names = append(names, ghString(raw.(map[string]any)["name"]))
	}
	return legs, names
}

func legsOf(spec string) []ciLeg {
	var legs []ciLeg
	for _, part := range strings.Fields(spec) {
		f := strings.Split(part, ",")
		legs = append(legs, ciLeg{f[0], f[1], f[2] == "race", f[3] == "guards"})
	}
	return legs
}

const lightMatrix = "ubuntu-latest,1/3,race,guards ubuntu-latest,2/3,race,guards ubuntu-latest,3/3,race,guards macos-latest,1/1,-,- windows-latest,1/1,-,-"
const fullMatrix = "ubuntu-latest,1/3,race,guards ubuntu-latest,2/3,race,guards ubuntu-latest,3/3,race,guards macos-latest,1/4,race,guards macos-latest,2/4,race,guards macos-latest,3/4,race,guards macos-latest,4/4,race,guards windows-latest,1/3,race,guards windows-latest,2/3,race,guards windows-latest,3/3,race,guards"

func TestPushToMainRunsOnlyTheLightMatrix(t *testing.T) {
	for _, event := range []string{"push", "pull_request", "merge_group"} {
		t.Run(event, func(t *testing.T) {
			legs, installers := ciMatrix(t, ghCtx{"github.event_name": event, "inputs.full": nil})
			if want := legsOf(lightMatrix); fmt.Sprint(legs) != fmt.Sprint(want) {
				t.Errorf("a %s run tests\n  %v\nwant the light matrix, -race and the guard vectors on Linux only:\n  %v", event, legs, want)
			}
			if got := strings.Join(installers, " "); got != "ubuntu-curl alpine-wget" {
				t.Errorf("a %s run checks the installer on %q, want ubuntu-curl alpine-wget", event, got)
			}
		})
	}
}

func releaseCtx(event, ref string) ghCtx {
	return ghCtx{
		"github.event_name": event, "github.ref": ref, "github.repository": "ai-ecoverse/slicc-cli", "cancelled()": false,
		"inputs.sliccy_next_only": false, "needs.fresh.outputs.verdict": "", "needs.ci.result": "skipped", "needs.full.result": "skipped",
		"github.event.workflow_run.conclusion": nil, "github.event.workflow_run.event": nil, "github.event.workflow_run.head_repository.full_name": nil,
	}
}

func releaseAfterCI(verdict string) ghCtx {
	ctx := releaseCtx("workflow_run", "refs/heads/main")
	ctx["github.event.workflow_run.conclusion"], ctx["github.event.workflow_run.event"] = "success", "push"
	ctx["github.event.workflow_run.head_repository.full_name"] = "ai-ecoverse/slicc-cli"
	ctx["needs.fresh.outputs.verdict"] = verdict
	return ctx
}

func with(ctx ghCtx, kv ...any) ghCtx {
	out := ghCtx{}
	for k, v := range ctx {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func TestTheReleaseRunsTheFullMatrixOnTheCommitItReleases(t *testing.T) {
	if _, err := os.Stat("../../.github/workflows/nightly.yml"); err == nil {
		t.Fatal("nightly.yml is back; the release runs the full matrix on its own commit, so nothing that failed earlier holds a release up")
	}
	ci := readWorkflow(t, "ci.yml")
	if !strings.Contains(ci, "\n  workflow_call:\n    inputs:\n      full:\n") || !strings.Contains(ci, "        type: boolean\n        default: false\n") {
		t.Fatal("ci.yml must take a boolean workflow_call input full, false by default")
	}
	release := readWorkflow(t, "release.yml")
	jobs := workflowJobs(t, release)
	full := jobs["full"]
	if !strings.Contains(full.body, "    uses: ./.github/workflows/ci.yml\n") || !strings.Contains(full.body, "    needs: [ci, fresh]\n") {
		t.Fatalf("release.yml's full job must call ci.yml after the ci and fresh jobs:\n%s", full.body)
	}
	m := regexp.MustCompile(`\n    with:\n      full: (\S+)\n`).FindStringSubmatch(full.body)
	if m == nil {
		t.Fatalf("release.yml's full job passes no full input:\n%s", full.body)
	}
	input := ghEval(t, m[1], ghCtx{})
	dispatch := with(releaseCtx("workflow_dispatch", "refs/heads/main"), "needs.ci.result", "success")
	for _, c := range []struct {
		name string
		ctx  ghCtx
		want bool
	}{
		{"after CI on main", releaseAfterCI("current"), true},
		{"after CI when main can't be read", releaseAfterCI("unknown"), true},
		{"after CI on a commit main has moved past", releaseAfterCI("stale"), false},
		{"a manual run on main", dispatch, true},
		{"a manual run whose CI failed", with(dispatch, "needs.ci.result", "failure"), false},
		{"the sliccy_next_only recovery", with(dispatch, "inputs.sliccy_next_only", true, "needs.ci.result", "skipped"), false},
		{"a manual dry run on a branch", with(releaseCtx("workflow_dispatch", "refs/heads/other"), "needs.ci.result", "success"), false},
		{"a pull request dry run", releaseCtx("pull_request", "refs/pull/7/merge"), false},
		{"a cancelled run", with(releaseAfterCI("current"), "cancelled()", true), false},
	} {
		if got := ghTruthy(ghTemplate(t, full.cond, c.ctx)); got != c.want {
			t.Errorf("%s: the full build runs: %v, want %v", c.name, got, c.want)
		}
	}
	watch := jobs["watch"]
	if watch.cond != full.cond {
		t.Errorf("the watch job must run exactly when the full build does:\n  %q\n  %q", watch.cond, full.cond)
	}
	if perms := permissionsOf(watch.body); len(perms) != 1 || perms["actions"] != "write" {
		t.Errorf("the watch job must have actions: write and nothing else: %v", perms)
	}
	for _, want := range []string{"    runs-on: ${{ vars.CERT_LIGHT_RUNNER || 'ubuntu-latest' }}\n", `gh run cancel "$RUN"`, `$3 == "full / ci"`, `$3 == "build"`, `$2 == "failure"`} {
		if !strings.Contains(watch.body, want) {
			t.Errorf("the watch job lacks %q:\n%s", want, watch.body)
		}
	}
	for _, event := range []string{"workflow_run", "workflow_dispatch"} {
		legs, installers := ciMatrix(t, ghCtx{"github.event_name": event, "inputs.full": input})
		if want := legsOf(fullMatrix); fmt.Sprint(legs) != fmt.Sprint(want) {
			t.Errorf("the release's full build (%s) tests\n  %v\nwant every shard on every OS with -race and the guard vectors:\n  %v", event, legs, want)
		}
		if got := strings.Join(installers, " "); got != "ubuntu-curl macos-curl alpine-wget" {
			t.Errorf("the release's full build (%s) checks the installer on %q, want ubuntu-curl macos-curl alpine-wget", event, got)
		}
	}
	grant := permissionsOf(full.body)
	for name, job := range workflowJobs(t, ci) {
		for perm, level := range permissionsOf(job.body) {
			if rank(grant[perm]) < rank(level) {
				t.Errorf("ci.yml's %s job needs %s: %s, which release.yml's full job doesn't grant", name, perm, level)
			}
		}
	}
	for name, job := range jobs {
		if got := permissionsOf(job.body)["issues"]; (got == "write") != (name == "report" || name == "release") {
			t.Errorf("release.yml's %s job has issues: %q; only report (and semantic-release's release job) may write issues", name, got)
		}
	}
}

func permissionsOf(body string) map[string]string {
	perms := map[string]string{}
	at := strings.Index(body, "\n    permissions:\n")
	if at < 0 {
		return perms
	}
	for _, line := range strings.Split(body[at+len("\n    permissions:\n"):], "\n") {
		if !strings.HasPrefix(line, "      ") || strings.HasPrefix(line, "       ") {
			break
		}
		k, v, _ := strings.Cut(strings.TrimSpace(line), ":")
		perms[k] = strings.TrimSpace(v)
	}
	return perms
}

func rank(level string) int {
	return map[string]int{"read": 1, "write": 2}[level]
}

func TestReleasePublishesOnlyAfterItsOwnFullBuildPassed(t *testing.T) {
	jobs := workflowJobs(t, readWorkflow(t, "release.yml"))
	build, release, smoke, report := jobs["build"], jobs["release"], jobs["smoke"], jobs["report"]
	if jobs["nightly"].body != "" {
		t.Fatal("release.yml has a nightly gate again; a failure some time ago must not hold up a release")
	}
	if !strings.Contains(build.body, "    needs: [ci, fresh]\n") || !strings.Contains(build.body, "    runs-on: macos-latest\n") {
		t.Fatalf("the build job must run on macOS beside the full build, after ci and fresh:\n%s", build.body)
	}
	if !strings.Contains(release.body, "    needs: [build, full, smoke]\n") || !strings.Contains(smoke.body, "    needs: build\n") {
		t.Fatal("the release job must wait for the build, the full build and the smoke tests")
	}
	dispatch := with(releaseCtx("workflow_dispatch", "refs/heads/main"), "needs.ci.result", "success")
	for _, c := range []struct {
		name string
		ctx  ghCtx
		want bool
	}{
		{"after CI on main", releaseAfterCI("current"), true},
		{"after CI on a commit main has moved past", releaseAfterCI("stale"), false},
		{"manual run on main", dispatch, true},
		{"manual run whose CI failed", with(dispatch, "needs.ci.result", "failure"), false},
		{"manual dry run on a branch", with(releaseCtx("workflow_dispatch", "refs/heads/other"), "needs.ci.result", "success"), true},
		{"pull request dry run", releaseCtx("pull_request", "refs/pull/7/merge"), true},
	} {
		if got := ghTruthy(ghTemplate(t, build.cond, c.ctx)); got != c.want {
			t.Errorf("%s: the build job runs: %v, want %v", c.name, got, c.want)
		}
	}
	green := with(releaseAfterCI("current"), "needs.build.result", "success", "needs.build.outputs.version", "1.45.1", "needs.full.result", "success", "needs.smoke.result", "success")
	for _, c := range []struct {
		name string
		ctx  ghCtx
		want bool
	}{
		{"everything passed on main", green, true},
		{"a manual run on main", with(green, "github.event_name", "workflow_dispatch"), true},
		{"the full build failed", with(green, "needs.full.result", "failure"), false},
		{"the full build was cancelled", with(green, "needs.full.result", "cancelled"), false},
		{"the build failed", with(green, "needs.build.result", "failure"), false},
		{"a smoke test failed", with(green, "needs.smoke.result", "failure"), false},
		{"no release due", with(green, "needs.build.outputs.version", ""), false},
		{"a cancelled run", with(green, "cancelled()", true), false},
		{"a dry run on a branch", with(green, "github.ref", "refs/heads/other", "github.event_name", "workflow_dispatch"), false},
		{"a pull request dry run", with(green, "github.ref", "refs/pull/7/merge", "github.event_name", "pull_request", "needs.full.result", "skipped"), false},
	} {
		if got := ghTruthy(ghTemplate(t, release.cond, c.ctx)); got != c.want {
			t.Errorf("%s: the release job runs: %v, want %v", c.name, got, c.want)
		}
	}
	if !strings.Contains(report.body, "    needs: [full, build, smoke, release, sliccy-next, install-smoke]\n") {
		t.Errorf("the report job must follow the full build, the build, the release and its smoke tests:\n%s", report.body)
	}
	if !strings.Contains(report.body, "    runs-on: ${{ vars.CERT_LIGHT_RUNNER || 'ubuntu-latest' }}\n") || !strings.Contains(report.body, "          RESULTS: full=${{ needs.full.result }} build=${{ needs.build.result }} smoke=${{ needs.smoke.result }} release=${{ needs.release.result }} sliccy-next=${{ needs.sliccy-next.result }} install-smoke=${{ needs.install-smoke.result }}\n") {
		t.Errorf("the report job must run on the light runner with every result:\n%s", report.body)
	}
	for _, c := range []struct {
		name string
		ctx  ghCtx
		want bool
	}{
		{"a failed full build on main", with(releaseAfterCI("current"), "needs.full.result", "failure"), true},
		{"a green full build on main", with(releaseAfterCI("current"), "needs.full.result", "success"), true},
		{"a run the watch job cancelled", with(releaseAfterCI("current"), "needs.full.result", "failure", "cancelled()", true), true},
		{"a stale run that built nothing", releaseAfterCI("stale"), false},
		{"a pull request dry run", releaseCtx("pull_request", "refs/pull/7/merge"), false},
	} {
		if got := ghTruthy(ghTemplate(t, report.cond, c.ctx)); got != c.want {
			t.Errorf("%s: the report job runs: %v, want %v", c.name, got, c.want)
		}
	}
}

type fakeActionsAPI struct {
	mu     sync.Mutex
	issues []map[string]any
	label  bool
	jobs   []map[string]any
	calls  []string
	bodies map[string]map[string]any
	srv    *httptest.Server
}

func newFakeActionsAPI(t *testing.T) *fakeActionsAPI {
	f := &fakeActionsAPI{bodies: map[string]map[string]any{}}
	const repo = "/repos/ai-ecoverse/slicc-cli"
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		call := r.Method + " " + strings.TrimPrefix(r.URL.Path, repo)
		f.calls = append(f.calls, call)
		var body map[string]any
		if r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
				f.bodies[call] = body
			}
		}
		if r.Header.Get("Authorization") != "Bearer fake-value-0001" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		send := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch call {
		case "GET /issues":
			issues := []map[string]any{}
			if q.Get("labels") == "release-red" && q.Get("state") == "open" {
				issues = f.issues
			}
			send(issues)
		case "GET /actions/runs/4242/jobs":
			send(map[string]any{"jobs": f.jobs})
		case "GET /labels/release-red":
			if !f.label {
				http.NotFound(w, r)
				return
			}
			send(map[string]any{"name": "release-red"})
		case "POST /labels":
			f.label = true
			w.WriteHeader(http.StatusCreated)
			send(body)
		case "POST /issues":
			w.WriteHeader(http.StatusCreated)
			send(map[string]any{"number": 77})
		default:
			if strings.HasPrefix(call, "POST /issues/") || strings.HasPrefix(call, "PATCH /issues/") {
				send(map[string]any{})
				return
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeActionsAPI) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeActionsAPI) body(call string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[call]
}

func (f *fakeActionsAPI) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

func runStep(t *testing.T, line string, env ...string) (string, int) {
	t.Helper()
	sh := requireSh(t)
	requireNode(t)
	cmd := exec.Command(sh, "-c", strings.TrimPrefix(line, "run: "))
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), append([]string{"GITHUB_REPOSITORY=ai-ecoverse/slicc-cli", "GH_TOKEN=fake-value-0001", "GITHUB_TOKEN="}, env...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func TestReleaseReportOpensCommentsAndClosesTheIssue(t *testing.T) {
	line := lineWith(t, workflowJobs(t, readWorkflow(t, "release.yml"))["report"].body, "scripts/release-issue.mjs")
	const sha = "1234512345123451234512345123451234512345"
	open := []map[string]any{{"number": 5, "title": "Release from main failed"}, {"number": 9, "pull_request": map[string]any{}}}
	jobs := []map[string]any{
		{"name": "full / test (macos-latest, 2/4)", "conclusion": "failure", "html_url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/4242/job/1"},
		{"name": "full / test (windows-latest, 3/3)", "conclusion": "timed_out", "html_url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/4242/job/2"},
		{"name": "full / test (ubuntu-latest, 1/2)", "conclusion": "success", "html_url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/4242/job/3"},
		{"name": "report", "conclusion": nil, "html_url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/4242/job/4"},
	}
	const released = "full=success build=success smoke=success release=success sliccy-next=success install-smoke=success"
	for _, c := range []struct {
		name    string
		results string
		issues  []map[string]any
		label   bool
		writes  string
		want    string
		body    string
		failed  string
	}{
		{"a red full build opens an issue", "full=failure build=cancelled smoke=cancelled release=skipped sliccy-next=skipped install-smoke=skipped", nil, false, "POST /issues POST /labels", "release-issue: PASS red; opened #77", "POST /issues", "**full failure**"},
		{"a failed build opens an issue", "full=cancelled build=failure smoke=skipped release=skipped sliccy-next=skipped install-smoke=skipped", nil, true, "POST /issues", "release-issue: PASS red; opened #77", "POST /issues", "**build failure**"},
		{"a run cancelled without a failure reports nothing", "full=cancelled build=cancelled smoke=skipped release=skipped sliccy-next=skipped install-smoke=skipped", open, true, "", "release-issue: PASS cancelled (full, build) without a failure; nothing to report", "", ""},
		{"a failed release comments on the open issue", "full=success build=success smoke=success release=failure sliccy-next=skipped install-smoke=skipped", open, true, "POST /issues/5/comments", "release-issue: PASS red; commented on #5", "POST /issues/5/comments", "**release failure**"},
		{"a failed smoke test after the release is red", "full=success build=success smoke=success release=success sliccy-next=success install-smoke=failure", nil, true, "POST /issues", "release-issue: PASS red; opened #77", "POST /issues", "**install-smoke failure**"},
		{"a green release closes the open issue", released, open, true, "PATCH /issues/5 POST /issues/5/comments", "release-issue: PASS green; closed #5", "", ""},
		{"a green run that released nothing", "full=success build=success smoke=skipped release=skipped sliccy-next=skipped install-smoke=skipped", nil, true, "", "release-issue: PASS green; no open release-red issue", "", ""},
		{"no results is red", "", nil, true, "POST /issues", "release-issue: PASS red; opened #77", "POST /issues", "**no job results**"},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := newFakeActionsAPI(t)
			api.set(func() { api.issues, api.label, api.jobs = c.issues, c.label, jobs })
			out, code := runStep(t, line, "GITHUB_API_URL="+api.srv.URL, "GITHUB_SERVER_URL=https://github.com", "GITHUB_RUN_ID=4242", "SHA="+sha, "RESULTS="+c.results)
			if code != 0 || !strings.Contains(out, c.want) {
				t.Fatalf("exit %d, want 0 and %q:\n%s", code, c.want, out)
			}
			if got := strings.Join(api.writes(), " "); got != c.writes {
				t.Errorf("wrote %q, want %q", got, c.writes)
			}
			if c.body != "" {
				text := ghString(api.body(c.body)["body"])
				for _, want := range []string{"https://github.com/ai-ecoverse/slicc-cli/actions/runs/4242", sha, "full / test (macos-latest, 2/4)", "full / test (windows-latest, 3/3)", c.failed, "gh workflow run release.yml --ref main"} {
					if !strings.Contains(text, want) {
						t.Errorf("%s body lacks %q:\n%s", c.body, want, text)
					}
				}
				if strings.Contains(text, "ubuntu-latest, 1/2") || strings.Contains(text, "[report]") {
					t.Errorf("%s body lists a job that didn't fail:\n%s", c.body, text)
				}
			}
			if c.body == "POST /issues" && fmt.Sprint(api.body("POST /issues")["labels"]) != "[release-red]" {
				t.Errorf("the new issue is labelled %v, want [release-red]", api.body("POST /issues")["labels"])
			}
			if strings.Contains(c.writes, "PATCH") && ghString(api.body("PATCH /issues/5")["state"]) != "closed" {
				t.Errorf("the green release sent %v, want state closed", api.body("PATCH /issues/5"))
			}
		})
	}
	api := newFakeActionsAPI(t)
	out, code := runStep(t, line, "GITHUB_API_URL="+api.srv.URL, "GITHUB_RUN_ID=", "SHA=", "RESULTS=full=failure")
	if code == 0 || !strings.Contains(out, "release-issue: FAIL") {
		t.Errorf("without a run id the report must fail, got exit %d:\n%s", code, out)
	}
}

func TestCITestsRunOnNode26AndTheNPMLauncherAlsoOn24(t *testing.T) {
	jobs := workflowJobs(t, readWorkflow(t, "ci.yml"))
	if v := lineWith(t, jobs["test"].body, "node-version:"); v != "node-version: 26" {
		t.Errorf("the test job sets up %q, want Node 26: Node 24's V8 crashes the kernel harness about once in 100 runs on Windows", v)
	}
	assets := jobs["installer-assets"].body
	if v := lineWith(t, assets, "node-version:"); v != "node-version: 24" {
		t.Errorf("installer-assets sets up %q, want Node 24, what npm users still run", v)
	}
	run := lineWith(t, assets, "scripts/test-shard.mjs 1/1")
	m := regexp.MustCompile(`-run '([^']+)'`).FindStringSubmatch(run)
	if m == nil || !strings.Contains(run, "sh scripts/verdict.sh test-shard ") || !strings.Contains(run, " -v ") {
		t.Fatalf("installer-assets doesn't run the npm launcher tests through verdict.sh with -v: %q", run)
	}
	re := regexp.MustCompile(m[1])
	for _, name := range []string{"TestNPMLauncherRunsTheBinary", "TestNPMLauncherForwardsSignals", "TestNPMLauncherPutsCompanionsOnPath", "TestSliccyNextRunsThroughNpxUnderBothBins"} {
		if !re.MatchString(name) || !slices.Contains(declaredTests(t), name) {
			t.Errorf("installer-assets' Node 24 run doesn't run %s", name)
		}
	}
}
