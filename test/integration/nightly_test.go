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
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestNightlyRunsTheFullMatrixOnMain(t *testing.T) {
	ci := readWorkflow(t, "ci.yml")
	if !strings.Contains(ci, "\n  workflow_call:\n    inputs:\n      full:\n") || !strings.Contains(ci, "        type: boolean\n        default: false\n") {
		t.Fatal("ci.yml must take a boolean workflow_call input full, false by default")
	}
	nightly := readWorkflow(t, "nightly.yml")
	if !strings.HasPrefix(nightly, "name: Nightly\n") {
		t.Fatal("nightly.yml must be named Nightly")
	}
	cron := regexp.MustCompile(`\n  schedule:\n    - cron: '([0-9]{1,2}) ([0-9]{1,2}) \* \* \*'\n  workflow_dispatch:\n`).FindStringSubmatch(nightly)
	if cron == nil {
		t.Fatal("nightly.yml must run once a day on a schedule and by workflow_dispatch")
	}
	if strings.Contains(nightly, "\n  push:") || strings.Contains(nightly, "\n  pull_request") {
		t.Fatal("nightly.yml must not run on pushes or pull requests")
	}
	jobs := workflowJobs(t, nightly)
	full := jobs["full"]
	if !strings.Contains(full.body, "    uses: ./.github/workflows/ci.yml\n") {
		t.Fatalf("the nightly's full job must call ci.yml:\n%s", full.body)
	}
	with := regexp.MustCompile(`\n    with:\n      full: (\S+)\n`).FindStringSubmatch(full.body)
	if with == nil {
		t.Fatalf("the nightly's full job passes no full input:\n%s", full.body)
	}
	input := ghEval(t, with[1], ghCtx{})
	for _, ref := range []string{"refs/heads/main", "refs/heads/other"} {
		got := ghTruthy(ghTemplate(t, full.cond, ghCtx{"github.ref": ref}))
		if got != (ref == "refs/heads/main") {
			t.Errorf("the nightly's full job runs on %s: %v", ref, got)
		}
	}
	legs, installers := ciMatrix(t, ghCtx{"github.event_name": "schedule", "inputs.full": input})
	if want := legsOf(fullMatrix); fmt.Sprint(legs) != fmt.Sprint(want) {
		t.Errorf("the nightly tests\n  %v\nwant every shard on every OS with -race and the guard vectors:\n  %v", legs, want)
	}
	if got := strings.Join(installers, " "); got != "ubuntu-curl macos-curl alpine-wget" {
		t.Errorf("the nightly checks the installer on %q, want ubuntu-curl macos-curl alpine-wget", got)
	}
	grant := permissionsOf(full.body)
	for name, job := range workflowJobs(t, ci) {
		for perm, level := range permissionsOf(job.body) {
			if rank(grant[perm]) < rank(level) {
				t.Errorf("ci.yml's %s job needs %s: %s, which the nightly's full job doesn't grant", name, perm, level)
			}
		}
	}
	for name, job := range jobs {
		if got := permissionsOf(job.body)["issues"]; (got == "write") != (name == "report") {
			t.Errorf("nightly.yml's %s job has issues: %q; only report may write issues", name, got)
		}
	}
	if strings.Contains(strings.SplitN(nightly, "\njobs:\n", 2)[0], "issues:") {
		t.Error("nightly.yml grants issues to every job")
	}
	report := jobs["report"]
	if !strings.Contains(report.body, "    needs: full\n") || !ghTruthy(ghTemplate(t, report.cond, ghCtx{"github.ref": "refs/heads/main"})) || !strings.Contains(report.cond, "always()") {
		t.Errorf("the report job must follow the full job on main whatever its result: %q", report.cond)
	}
	if !strings.Contains(report.body, "    runs-on: ${{ vars.CERT_LIGHT_RUNNER || 'ubuntu-latest' }}\n") || !strings.Contains(report.body, "          RESULT: ${{ needs.full.result }}\n") {
		t.Errorf("the report job must run on the light runner with the full job's result:\n%s", report.body)
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

func TestReleaseRequiresTheNightlyGateOnMain(t *testing.T) {
	jobs := workflowJobs(t, readWorkflow(t, "release.yml"))
	gate, release := jobs["nightly"], jobs["release"]
	if gate.body == "" {
		t.Fatal("release.yml has no nightly gate job")
	}
	if !strings.Contains(gate.body, "    runs-on: ${{ vars.CERT_LIGHT_RUNNER || 'ubuntu-latest' }}\n") {
		t.Errorf("the nightly gate must run on vars.CERT_LIGHT_RUNNER:\n%s", gate.body)
	}
	if !strings.Contains(gate.body, "        run: sh scripts/verdict.sh nightly node scripts/nightly-gate.mjs\n") {
		t.Errorf("the nightly gate must require the gate's verdict:\n%s", gate.body)
	}
	if !strings.Contains(release.body, "    needs: [ci, fresh, nightly]\n") {
		t.Errorf("the release job doesn't wait for the nightly gate")
	}
	base := func(event, ref string) ghCtx {
		return ghCtx{
			"github.event_name": event, "github.ref": ref, "github.repository": "ai-ecoverse/slicc-cli", "cancelled()": false,
			"inputs.sliccy_next_only": false, "needs.fresh.outputs.verdict": "", "needs.ci.result": "skipped",
			"github.event.workflow_run.conclusion": nil, "github.event.workflow_run.event": nil, "github.event.workflow_run.head_repository.full_name": nil,
		}
	}
	afterCI := base("workflow_run", "refs/heads/main")
	afterCI["github.event.workflow_run.conclusion"], afterCI["github.event.workflow_run.event"] = "success", "push"
	afterCI["github.event.workflow_run.head_repository.full_name"] = "ai-ecoverse/slicc-cli"
	afterCI["needs.fresh.outputs.verdict"] = "current"
	dispatch := base("workflow_dispatch", "refs/heads/main")
	dispatch["needs.ci.result"] = "success"
	branch := base("workflow_dispatch", "refs/heads/other")
	branch["needs.ci.result"] = "success"
	pr := base("pull_request", "refs/pull/7/merge")
	for _, c := range []struct {
		name   string
		ctx    ghCtx
		gates  bool
		result map[string]bool
	}{
		{"after CI on main", afterCI, true, map[string]bool{"success": true, "failure": false, "cancelled": false, "skipped": false}},
		{"manual run on main", dispatch, true, map[string]bool{"success": true, "failure": false, "skipped": false}},
		{"manual dry run on a branch", branch, false, map[string]bool{"skipped": true}},
		{"pull request dry run", pr, false, map[string]bool{"skipped": true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ghTruthy(ghTemplate(t, gate.cond, c.ctx)); got != c.gates {
				t.Errorf("the nightly gate runs: %v, want %v", got, c.gates)
			}
			for result, want := range c.result {
				ctx := ghCtx{"needs.nightly.result": result}
				for k, v := range c.ctx {
					ctx[k] = v
				}
				if got := ghTruthy(ghTemplate(t, release.cond, ctx)); got != want {
					t.Errorf("with the nightly gate %s, the release job runs: %v, want %v", result, got, want)
				}
			}
		})
	}
	stale := base("workflow_run", "refs/heads/main")
	stale["needs.fresh.outputs.verdict"] = "stale"
	if ghTruthy(ghTemplate(t, gate.cond, stale)) {
		t.Error("the nightly gate runs for a stale CI run, which releases nothing")
	}
}

type fakeActionsAPI struct {
	mu     sync.Mutex
	runs   []map[string]any
	fail   bool
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
		case "GET /actions/workflows/nightly.yml/runs":
			if f.fail {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			runs := []map[string]any{}
			if q.Get("branch") == "main" && q.Get("status") == "completed" {
				runs = f.runs
			}
			send(map[string]any{"total_count": len(runs), "workflow_runs": runs})
		case "GET /issues":
			issues := []map[string]any{}
			if q.Get("labels") == "nightly-red" && q.Get("state") == "open" {
				issues = f.issues
			}
			send(issues)
		case "GET /actions/runs/4242/jobs":
			send(map[string]any{"jobs": f.jobs})
		case "GET /labels/nightly-red":
			if !f.label {
				http.NotFound(w, r)
				return
			}
			send(map[string]any{"name": "nightly-red"})
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

func TestNightlyGateRefusesUnlessTheLatestNightlyPassed(t *testing.T) {
	line := lineWith(t, workflowJobs(t, readWorkflow(t, "release.yml"))["nightly"].body, "scripts/nightly-gate.mjs")
	const sha = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	run := func(conclusion string, startedAgo, createdAgo time.Duration) map[string]any {
		now := time.Now().UTC()
		return map[string]any{
			"id": 101, "html_url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/101", "head_sha": sha, "head_branch": "main",
			"status": "completed", "conclusion": conclusion,
			"created_at": now.Add(-createdAgo).Format(time.RFC3339), "run_started_at": now.Add(-startedAgo).Format(time.RFC3339),
		}
	}
	for _, c := range []struct {
		name string
		runs []map[string]any
		fail bool
		code int
		want string
	}{
		{"green", []map[string]any{run("success", 2*time.Hour, 2*time.Hour)}, false, 0, `nightly: PASS https://github.com/ai-ecoverse/slicc-cli/actions/runs/101 ` + sha + ` 2h0[01]m`},
		{"green re-run of an older nightly", []map[string]any{run("success", time.Hour, 50*time.Hour)}, false, 0, `nightly: PASS \S+ ` + sha + ` 1h0[01]m`},
		{"red", []map[string]any{run("failure", 2*time.Hour, 2*time.Hour)}, false, 1, `nightly: FAIL \S+ ` + sha + ` 2h0[01]m: the latest nightly ended failure`},
		{"cancelled", []map[string]any{run("cancelled", 3*time.Hour, 3*time.Hour)}, false, 1, `the latest nightly ended cancelled`},
		{"timed out", []map[string]any{run("timed_out", 3*time.Hour, 3*time.Hour)}, false, 1, `the latest nightly ended timed_out`},
		{"missing", nil, false, 1, `nightly: FAIL no completed nightly.yml run on main`},
		{"stale", []map[string]any{run("success", 40*time.Hour, 40*time.Hour)}, false, 1, `nightly: FAIL \S+ ` + sha + ` 40h0[01]m: the latest nightly is older than 36h`},
		{"API down", nil, true, 1, `nightly: FAIL could not read the latest nightly: GET \S+ HTTP 500`},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := newFakeActionsAPI(t)
			api.set(func() { api.runs, api.fail = c.runs, c.fail })
			out, code := runStep(t, line, "GITHUB_API_URL="+api.srv.URL)
			if code != c.code || !regexp.MustCompile(c.want).MatchString(out) {
				t.Fatalf("exit %d, want %d and /%s/:\n%s", code, c.code, c.want, out)
			}
			if c.code != 0 && !strings.Contains(out, "gh workflow run nightly.yml --ref main") {
				t.Errorf("a refusal must say how to unblock:\n%s", out)
			}
		})
	}
}

func TestNightlyReportOpensCommentsAndClosesTheIssue(t *testing.T) {
	line := lineWith(t, workflowJobs(t, readWorkflow(t, "nightly.yml"))["report"].body, "scripts/nightly-issue.mjs")
	const sha = "1234512345123451234512345123451234512345"
	open := []map[string]any{{"number": 5, "title": "Nightly full matrix is red"}, {"number": 9, "pull_request": map[string]any{}}}
	jobs := []map[string]any{
		{"name": "full / test (macos-latest, 2/4)", "conclusion": "failure", "html_url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/4242/job/1"},
		{"name": "full / test (windows-latest, 3/3)", "conclusion": "timed_out", "html_url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/4242/job/2"},
		{"name": "full / test (ubuntu-latest, 1/2)", "conclusion": "success", "html_url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/4242/job/3"},
		{"name": "report", "conclusion": nil, "html_url": "https://github.com/ai-ecoverse/slicc-cli/actions/runs/4242/job/4"},
	}
	for _, c := range []struct {
		name   string
		result string
		issues []map[string]any
		label  bool
		writes string
		want   string
		body   string
	}{
		{"red opens an issue", "failure", nil, false, "POST /issues POST /labels", "nightly-issue: PASS red; opened #77", "POST /issues"},
		{"cancelled opens an issue", "cancelled", nil, true, "POST /issues", "nightly-issue: PASS red; opened #77", "POST /issues"},
		{"red comments on the open issue", "failure", open, true, "POST /issues/5/comments", "nightly-issue: PASS red; commented on #5", "POST /issues/5/comments"},
		{"green closes the open issue", "success", open, true, "PATCH /issues/5 POST /issues/5/comments", "nightly-issue: PASS green; closed #5", ""},
		{"green without an issue", "success", nil, true, "", "nightly-issue: PASS green; no open nightly-red issue", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := newFakeActionsAPI(t)
			api.set(func() { api.issues, api.label, api.jobs = c.issues, c.label, jobs })
			out, code := runStep(t, line, "GITHUB_API_URL="+api.srv.URL, "GITHUB_SERVER_URL=https://github.com", "GITHUB_RUN_ID=4242", "GITHUB_SHA="+sha, "RESULT="+c.result)
			if code != 0 || !strings.Contains(out, c.want) {
				t.Fatalf("exit %d, want 0 and %q:\n%s", code, c.want, out)
			}
			if got := strings.Join(api.writes(), " "); got != c.writes {
				t.Errorf("wrote %q, want %q", got, c.writes)
			}
			if c.body != "" {
				text := ghString(api.body(c.body)["body"])
				for _, want := range []string{"https://github.com/ai-ecoverse/slicc-cli/actions/runs/4242", sha, "full / test (macos-latest, 2/4)", "full / test (windows-latest, 3/3)", "**" + c.result + "**", "gh workflow run nightly.yml --ref main"} {
					if !strings.Contains(text, want) {
						t.Errorf("%s body lacks %q:\n%s", c.body, want, text)
					}
				}
				if strings.Contains(text, "ubuntu-latest, 1/2") || strings.Contains(text, "[report]") {
					t.Errorf("%s body lists a job that didn't fail:\n%s", c.body, text)
				}
			}
			if c.body == "POST /issues" && fmt.Sprint(api.body("POST /issues")["labels"]) != "[nightly-red]" {
				t.Errorf("the new issue is labelled %v, want [nightly-red]", api.body("POST /issues")["labels"])
			}
			if strings.Contains(c.writes, "PATCH") && ghString(api.body("PATCH /issues/5")["state"]) != "closed" {
				t.Errorf("the green nightly sent %v, want state closed", api.body("PATCH /issues/5"))
			}
		})
	}
	api := newFakeActionsAPI(t)
	out, code := runStep(t, line, "GITHUB_API_URL="+api.srv.URL, "GITHUB_RUN_ID=", "GITHUB_SHA=", "RESULT=failure")
	if code == 0 || !strings.Contains(out, "nightly-issue: FAIL") {
		t.Errorf("without a run id the report must fail, got exit %d:\n%s", code, out)
	}
}
