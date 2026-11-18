package integration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	approvalSudo    = "apr-1f0c5a2e-7b9d-4c1e-8a3f-2d6b9e0c4a71"
	approvalTool    = "apr-6b2d8e41-03c9-4f7a-9e15-c4a07d3b5f28"
	approvalCapture = "apr-9e4a1c07-5d3b-4e8f-a2c6-7f10b9d84e53"
)

func approvalItems(created, expires string) string {
	return `[
		{id: '` + approvalSudo + `', kind: 'sudo', agentId: 'hR7cZ2mVq9LwX4eN1sKj0A', agentName: 'cone', requester: {kind: 'agent', name: 'cone'}, detail: {command: 'apt-get install -y ripgrep', cwd: '/workspace'}, tier: 'user', always: true, stoppable: true, verdict: {level: 2, reason: 'it installs software'}, createdAt: ` + created + `, expiresAt: ` + expires + `},
		{id: '` + approvalTool + `', kind: 'guest-tool', agentId: 'x3Lk9aQeV2bN0cR5tY8uWg', agentName: 'research', requester: {kind: 'guest', name: 'Ada', where: 'sliccy.com'}, detail: {tool: 'bash', summary: 'curl https://example.com'}, tier: 'user', always: false, stoppable: true, judging: true, createdAt: ` + created + `, expiresAt: ` + expires + `, later: 'kept'},
		{id: '` + approvalCapture + `', kind: 'capture', requester: {kind: 'user', name: 'owner'}, detail: {what: 'screen', mode: 'video', seconds: 30, program: 'record'}, tier: 'user', always: false, stoppable: false, verdictError: 'the judge gave no verdict', createdAt: ` + created + `, expiresAt: ` + expires + `}
	]`
}

var approvalsFixed = approvalItems("1767268800000", "1767268920000")

func approvalsLive() string {
	return `(() => { const now = Date.now(); return ` + approvalItems("now - 5000", "now + 150000") + `; })()`
}

func approvalOpens(st agentState, op string) []agentOpen {
	var out []agentOpen
	for _, o := range st.Opens {
		if o.Op == op {
			out = append(out, o)
		}
	}
	return out
}

func TestApprovalsPrintsOneLinePerItem(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("approvals(" + approvalsLive() + ")")
	pr := s.run(t, nil, "approvals")
	if code := pr.wait(t, 60*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimRight(pr.stdout.String(), "\n"), "\n")
	want := []string{
		approvalSudo + "  sudo        cone      level 2     2m left  apt-get install -y ripgrep",
		approvalTool + "  guest-tool  research  judging     2m left  bash: curl https://example.com",
		approvalCapture + "  capture     owner     no verdict  2m left  screen video 30s for record",
	}
	if len(lines) != len(want) {
		t.Fatalf("stdout:\n%s", pr.stdout.String())
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d %q, want %q", i, lines[i], want[i])
		}
	}
	if opens := approvalOpens(s.p.agent(), "approvals"); len(opens) != 1 || opens[0].Watch {
		t.Fatalf("opens %+v", opens)
	}
	s.p.agentDo("approvals([])")
	empty := s.run(t, nil, "approvals")
	if code := empty.wait(t, 60*time.Second); code != 0 || empty.stdout.String() != "" || !strings.Contains(empty.stderr.String(), "slicc: nothing waits for an answer") {
		t.Fatalf("empty: exit %d, stdout %q, stderr %q", code, empty.stdout.String(), empty.stderr.String())
	}
}

func TestApprovalsWatchJSONFollowsDecisionsAndReconnects(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("approvals(" + approvalsFixed + ")")
	w := s.run(t, nil, "approvals", "--watch", "--json")
	w.waitFor(t, w.stdout, approvalCapture, 30*time.Second)
	d := s.run(t, nil, "decide", approvalSudo, "once", "--reason", "routine install", "--json")
	if code := d.wait(t, 60*time.Second); code != 0 {
		t.Fatalf("decide: exit %d, %s", code, d.stderr.String())
	}
	if got := d.stdout.String(); got != `{"schema":1,"event":"decided","id":"`+approvalSudo+`","answer":"once"}`+"\n" {
		t.Fatalf("decide stdout %q", got)
	}
	st := s.p.agent()
	if len(st.Decisions) != 1 || st.Decisions[0] != (agentDecision{ID: approvalSudo, Answer: "once", Reason: "routine install"}) {
		t.Fatalf("decisions %+v", st.Decisions)
	}
	deadline := time.Now().Add(30 * time.Second)
	for strings.Count(w.stdout.String(), `"event":"approvals"`) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("no second snapshot:\n%s", w.stdout.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	var n any
	s.p.eval(`linkTest.agent.resetApprovals()`, &n)
	w.waitFor(t, w.stdout, `"event":"reconnecting"`, 30*time.Second)
	for strings.Count(w.stdout.String(), `"event":"approvals"`) < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("no snapshot after the reconnect:\n%s", w.stdout.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := interrupt(w.cmd); err != nil {
		t.Fatal(err)
	}
	if code := w.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, o := range approvalOpens(s.p.agent(), "approvals") {
		if !o.Watch {
			t.Fatalf("an approvals open without watch: %+v", o)
		}
	}
	var out bytes.Buffer
	for _, line := range strings.Split(strings.TrimSpace(w.stdout.String()), "\n") {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil || !strings.HasPrefix(line, `{"schema":1,"event":"`) {
			t.Fatalf("line %q", line)
		}
		out.WriteString(placeholder(t, line, m, "receivedAt", "retryAt", "wait") + "\n")
	}
	got := out.String()
	if strings.Count(got, `"event":"approvals"`) != 3 || strings.Count(got, approvalSudo) != 1 || !strings.Contains(got, `"later":"kept"`) || strings.Contains(got, `"t":`) {
		t.Fatalf("stdout:\n%s", got)
	}
	nativeFixture(t, "approvals.ndjson", out.Bytes())
}

func TestDecideAnswersAndRefusals(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("approvals(" + approvalsFixed + ")")
	s.p.agentDo("refuse('" + approvalCapture + "', 'Only a person can stop a turn.')")
	cases := []struct {
		setup  string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"", []string{approvalTool, "deny"}, 0, "", "slicc: denied it\n"},
		{"", []string{approvalCapture, "stop"}, 1, "", "Only a person can stop a turn.\n"},
		{"", []string{approvalCapture, "stop", "--json"}, 1, `{"schema":1,"event":"error","message":"Only a person can stop a turn.","code":"EINVAL","retryable":false}` + "\n", "Only a person can stop a turn.\n"},
		{"", []string{"apr-gone", "once"}, 1, "", "seven has no approval with that id\n"},
		{"decideWith('EACCES: an agent in this seven can\\'t answer its approvals')", []string{approvalSudo, "always"}, 3, "", "slicc decide: an agent in this seven can't answer its approvals\n"},
		{"decideWith('EOPNOTSUPP: this agent has no approvals')", []string{approvalSudo, "always"}, 1, "", "slicc decide: this seven doesn't take answers to its approvals; update it\n"},
		{"decideWith('EOPNOTSUPP: the agent is not running')", []string{approvalSudo, "always"}, 1, "", "slicc decide: the agent is not running\n"},
		{"decideWith(null); approvals(null)", []string{approvalSudo, "always"}, 1, "", "slicc decide: this seven doesn't take answers to its approvals; update it\n"},
		{"", nil, 1, "", "slicc approvals: this seven doesn't report its approvals; update it\n"},
		{"approvals(null, 'EOPNOTSUPP: this agent has no approvals')", nil, 1, "", "slicc approvals: this seven doesn't report its approvals; update it\n"},
		{"approvals(null, 'EOPNOTSUPP: the agent is not running')", nil, 1, "", "slicc approvals: the agent is not running\n"},
		{"approvals([], 'EACCES')", nil, 3, "", "turn it on in seven's Linked devices\n"},
		{"approvals(" + approvalsFixed + "); configure({allow: false})", []string{approvalSudo, "once"}, 3, "", "slicc decide: this seven doesn't allow remote control from this device; turn it on in seven's Linked devices\n"},
		{"", nil, 3, "", "slicc approvals: this seven doesn't allow remote control from this device; turn it on in seven's Linked devices\n"},
	}
	for _, c := range cases {
		for _, call := range strings.Split(c.setup, "; ") {
			if call != "" {
				s.p.agentDo(call)
			}
		}
		verb := "decide"
		if c.args == nil {
			verb = "approvals"
		}
		pr := s.run(t, nil, verb, c.args...)
		code := pr.wait(t, 60*time.Second)
		if code != c.code || pr.stdout.String() != c.stdout || !strings.HasSuffix(pr.stderr.String(), c.stderr) {
			t.Fatalf("%s %s %v: exit %d, stdout %q, stderr %q", c.setup, verb, c.args, code, pr.stdout.String(), pr.stderr.String())
		}
	}
	for _, o := range approvalOpens(s.p.agent(), "decide") {
		if o.ID == "" || o.Answer == "" || o.Reason != "" {
			t.Fatalf("decide open %+v", o)
		}
	}
}

func TestApprovalsAndDecideUsage(t *testing.T) {
	out, errOut, code := slicc(t, "approvals", "--help")
	if code != 0 || !strings.HasPrefix(out, "usage: slicc <join-url> approvals [--watch] [--json] [--plain]") || errOut != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	out, errOut, code = slicc(t, "decide", "--help")
	if code != 0 || !strings.HasPrefix(out, "usage: slicc <join-url> decide <id> once|always|deny|stop [--reason <text>] [--json]") || errOut != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	cdp := []string{"--cdp", "http://127.0.0.1:1"}
	for _, c := range []struct {
		args []string
		says string
	}{
		{[]string{"approvals"}, "give a join URL (slicc <join-url> approvals) or --cdp <url>"},
		{append([]string{"approvals", "extra"}, cdp...), "unexpected arguments"},
		{append([]string{"decide"}, cdp...), "say which approval and how to answer"},
		{append([]string{"decide", approvalSudo}, cdp...), "say which approval and how to answer"},
		{append([]string{"decide", approvalSudo, "maybe"}, cdp...), `answer once, always, deny or stop, not "maybe"`},
		{append([]string{"decide", approvalSudo, "once", "twice"}, cdp...), "say which approval and how to answer"},
		{[]string{"decide", approvalSudo, "once"}, "give a join URL (slicc <join-url> decide) or --cdp <url>"},
		{append([]string{"decide", approvalSudo, "once", "--bogus"}, cdp...), "flag provided but not defined: -bogus"},
	} {
		if _, errOut, code := slicc(t, c.args...); code != 2 || !strings.Contains(errOut, c.says) {
			t.Fatalf("%v: code %d, stderr %q", c.args, code, errOut)
		}
	}
}
