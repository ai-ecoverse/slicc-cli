package integration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const inventoryOne = `{v: 1, instance: {id: 'pL4vT2nQ8sK1mX6zR0bYcA', name: 'seven on sliccy.test'}, seq: 4, changedAt: 1767268800000, reportedAt: Date.now() - 2000, counts: {running: 1, queued: 0, waiting: 0, error: 0, idle: 1}, agents: [{id: 'hR7cZ2mVq9LwX4eN1sKj0A', kind: 'cone', status: 'running', queued: 1, provider: 'anthropic', model: 'claude-opus-4-6', since: Date.now() - 65000}, {id: 'x3Lk9aQeV2bN0cR5tY8uWg', kind: 'scoop', parent: 'hR7cZ2mVq9LwX4eN1sKj0A', status: 'idle', queued: 0, provider: 'azure', since: Date.now() - 4000, later: 'kept'}]}`

const inventoryTwo = `{v: 1, instance: {id: 'pL4vT2nQ8sK1mX6zR0bYcA'}, seq: 5, changedAt: 1767268801000, reportedAt: 1767268802000, counts: {running: 0, queued: 0, waiting: 0, error: 1, idle: 0}, agents: [{id: 'hR7cZ2mVq9LwX4eN1sKj0A', kind: 'cone', status: 'error', queued: 0, since: 1767268801000}]}`

func (p *page) waitInventories(t *testing.T, n int) {
	t.Helper()
	p.waitAgent(t, "an inventory watch", func(st agentState) bool {
		opens := 0
		for _, o := range st.Opens {
			if o.Op == "inventory" {
				opens++
			}
		}
		return st.Inventories == 1 && opens >= n
	})
}

func inventoryOpens(st agentState) []agentOpen {
	var out []agentOpen
	for _, o := range st.Opens {
		if o.Op == "inventory" {
			out = append(out, o)
		}
	}
	return out
}

func TestAgentsPrintsOneSnapshotAsATable(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("inventory(" + inventoryOne + ")")
	pr := s.run(t, nil, "agents")
	if code := pr.wait(t, 60*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimRight(pr.stdout.String(), "\n"), "\n")
	want := []string{
		"seven on sliccy.test: 1 running, 1 idle (reported 2s ago)",
		"ID        KIND   PARENT    STATUS   QUEUED  MODEL                      SINCE",
		"hR7cZ2mV  cone   -         running  1       anthropic/claude-opus-4-6  1m",
		"x3Lk9aQe  scoop  hR7cZ2mV  idle     0       azure                      2s",
	}
	if len(lines) != len(want) {
		t.Fatalf("stdout:\n%s", pr.stdout.String())
	}
	for i := range want {
		if i == 0 && strings.HasPrefix(lines[0], "seven on sliccy.test: 1 running, 1 idle (reported ") {
			continue
		}
		if lines[i] != want[i] {
			t.Fatalf("line %d %q, want %q\n%s", i, lines[i], want[i], pr.stdout.String())
		}
	}
	opens := inventoryOpens(s.p.agent())
	if len(opens) != 1 || opens[0].Watch {
		t.Fatalf("opens %+v", opens)
	}
}

func TestAgentsWatchJSONFollowsAndReconnects(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("inventory(" + inventoryOne + ")")
	w := s.run(t, nil, "agents", "--watch", "--json")
	s.p.waitInventories(t, 1)
	w.waitFor(t, w.stdout, `"seq":4`, 30*time.Second)
	s.p.agentDo("inventory(" + inventoryTwo + ")")
	w.waitFor(t, w.stdout, `"seq":5`, 30*time.Second)
	var n any
	s.p.eval(`linkTest.agent.resetInventories()`, &n)
	s.p.waitInventories(t, 2)
	w.waitFor(t, w.stdout, `"event":"reconnecting"`, 30*time.Second)
	deadline := time.Now().Add(30 * time.Second)
	for strings.Count(w.stdout.String(), `"seq":5`) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("no second snapshot:\n%s", w.stdout.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := interrupt(w.cmd); err != nil {
		t.Fatal(err)
	}
	if code := w.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, o := range inventoryOpens(s.p.agent()) {
		if !o.Watch {
			t.Fatalf("an inventory open without watch: %+v", o)
		}
	}
	var out bytes.Buffer
	for _, line := range strings.Split(strings.TrimSpace(w.stdout.String()), "\n") {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON: %q", line)
		}
		if !strings.HasPrefix(line, `{"schema":1,"event":"`) {
			t.Fatalf("line %q", line)
		}
		line = placeholder(t, line, m, "receivedAt", "retryAt", "wait")
		if strings.Contains(line, `"seq":4`) {
			line = placeholder(t, line, m, "reportedAt")
			line = sinceHolder(line)
		}
		out.WriteString(line + "\n")
	}
	got := out.String()
	if strings.Count(got, `"event":"inventory"`) != 3 || strings.Count(got, `"event":"reconnecting"`) != 1 || !strings.Contains(got, `"later":"kept"`) || strings.Contains(got, `"t":`) {
		t.Fatalf("stdout:\n%s", got)
	}
	nativeFixture(t, "agents.ndjson", out.Bytes())
}

func placeholder(t *testing.T, line string, m map[string]json.RawMessage, keys ...string) string {
	t.Helper()
	for _, k := range keys {
		if v, ok := m[k]; ok {
			line = strings.Replace(line, `"`+k+`":`+string(v), `"`+k+`":"<`+k+`>"`, 1)
		}
	}
	return line
}

func sinceHolder(line string) string {
	var out strings.Builder
	for {
		at := strings.Index(line, `"since":`)
		if at < 0 {
			out.WriteString(line)
			return out.String()
		}
		at += len(`"since":`)
		out.WriteString(line[:at])
		end := at
		for end < len(line) && line[end] >= '0' && line[end] <= '9' {
			end++
		}
		out.WriteString(`"<since>"`)
		line = line[end:]
	}
}

func TestAgentsRefusalsAndOlderSevens(t *testing.T) {
	s := newLocalSeven(t)
	cases := []struct {
		setup string
		args  []string
		code  int
		says  string
	}{
		{"inventory(null)", nil, 1, "slicc agents: this seven doesn't report its agents; update it"},
		{"inventory(null)", []string{"--watch"}, 1, "slicc agents: this seven doesn't report its agents; update it"},
		{"inventory(null, 'EOPNOTSUPP: this agent has no inventory')", nil, 1, "this seven doesn't report its agents; update it"},
		{"inventory(null, 'EOPNOTSUPP: the agent is not running')", nil, 1, "slicc agents: the agent is not running"},
		{"inventory(null, 'EACCES')", nil, 3, "turn it on in seven's Linked devices"},
	}
	for _, c := range cases {
		s.p.agentDo(c.setup)
		pr := s.run(t, nil, "agents", c.args...)
		if code := pr.wait(t, 60*time.Second); code != c.code || !strings.Contains(pr.stderr.String(), c.says) || pr.stdout.String() != "" {
			t.Fatalf("%s %v: exit %d, stdout %q, stderr %q", c.setup, c.args, code, pr.stdout.String(), pr.stderr.String())
		}
	}
	s.p.agentDo("inventory(" + inventoryTwo + ")")
	s.p.agentDo("configure({allow: false})")
	for _, args := range [][]string{nil, {"--watch"}} {
		pr := s.run(t, nil, "agents", args...)
		if code := pr.wait(t, 60*time.Second); code != 3 || !strings.Contains(pr.stderr.String(), "turn it on in seven's Linked devices") {
			t.Fatalf("%v without the cap: exit %d, %s", args, code, pr.stderr.String())
		}
	}
}

func TestAgentsUsage(t *testing.T) {
	out, errOut, code := slicc(t, "agents", "--help")
	if code != 0 || !strings.HasPrefix(out, "usage: slicc <join-url> agents [--watch] [--json]") || errOut != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, errOut, code := slicc(t, "agents"); code != 2 || !strings.Contains(errOut, "give a join URL (slicc <join-url> agents) or --cdp <url>") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	if _, errOut, code := slicc(t, "agents", "--cdp", "http://127.0.0.1:1", "extra"); code != 2 || !strings.Contains(errOut, "unexpected arguments") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

func TestAgentsWatchSaysWhenTheAgentIsNotRunning(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("inventory(null, 'EOPNOTSUPP: the agent is not running')")
	j := s.run(t, nil, "agents", "--watch", "--json")
	j.waitFor(t, j.stdout, `{"schema":1,"event":"error","message":"the agent is not running","retryable":true}`, 30*time.Second)
	j.waitFor(t, j.stdout, `"event":"reconnecting"`, 30*time.Second)
	text := s.run(t, nil, "agents", "--watch")
	text.waitFor(t, text.stderr, "slicc: the agent is not running; trying again", 30*time.Second)
	s.p.agentDo("inventory(" + inventoryTwo + ")")
	j.waitFor(t, j.stdout, `"event":"inventory"`, 30*time.Second)
	text.waitFor(t, text.stdout, "1 error", 30*time.Second)
	for _, pr := range []*agentProc{j, text} {
		if err := interrupt(pr.cmd); err != nil {
			t.Fatal(err)
		}
		if code := pr.wait(t, 30*time.Second); code != 0 {
			t.Fatalf("exit %d", code)
		}
	}
}
