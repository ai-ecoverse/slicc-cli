package integration

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const toolScript = `{steps: [
  {event: {t: 'text', id: 'a1', delta: 'Hello'}},
  {hold: true},
  {event: {t: 'text', id: 'a1', delta: ', world.'}},
  {event: {t: 'thinking', id: 'k1', delta: 'pondering'}},
  {event: {t: 'tool', id: 't1', call: 'c1', name: 'bash', summary: 'ls -la'}},
  {event: {t: 'tool-result', id: 'r1', call: 'c1', ok: true, summary: '3 files'}},
  {event: {t: 'tool', id: 't2', call: 'c2', name: 'read_file', summary: 'notes.md'}},
  {event: {t: 'tool-result', id: 'r2', call: 'c2', ok: false, summary: 'no such file'}},
  {event: {t: 'notice', id: 'n1', text: 'retrying in 2s'}},
  {event: {t: 'text', id: 'a2', delta: 'Done.'}}
]}`

func TestPromptLocalStreamsTextToStdoutAndToolsToStderr(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("queue(" + toolScript + ")")
	pr := s.run(t, nil, "prompt", "what", "is", "here?")
	pr.waitFor(t, pr.stdout, "Hello", 60*time.Second)
	st := s.p.waitAgent(t, "a held run", func(st agentState) bool { return st.Held })
	if strings.Contains(pr.stdout.String(), "world") {
		t.Fatal("stdout ran ahead of the stream")
	}
	s.p.agentDo("release()")
	if code := pr.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got, want := pr.stdout.String(), "Hello, world.\n\nDone.\n"; got != want {
		t.Fatalf("stdout %q, want %q", got, want)
	}
	errOut := pr.stderr.String()
	for _, want := range []string{"tool bash: ls -la\n", "tool bash ok: 3 files\n", "tool read_file: notes.md\n", "tool read_file failed: no such file\n", "notice: retrying in 2s\n"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "pondering") || strings.Contains(errOut, "\x1b[") {
		t.Fatalf("stderr shows thinking or escapes without asking:\n%q", errOut)
	}
	st = s.p.agent()
	if len(st.Prompts) != 1 || st.Prompts[0] != "what is here?" {
		t.Fatalf("prompts %q", st.Prompts)
	}
	open := st.Opens[0]
	if open.Kind != "agent" || open.Op != "prompt" || open.WhenBusy != "followUp" || open.Thinking {
		t.Fatalf("open %+v", open)
	}
}

func TestPromptThinkingGoesToStderr(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo(`queue({steps: [{event: {t: 'thinking', id: 'k1', delta: 'hmm, '}}, {event: {t: 'thinking', id: 'k1', delta: 'yes'}}, {event: {t: 'text', id: 'a1', delta: 'Yes.'}}]})`)
	pr := s.run(t, nil, "prompt", "--thinking", "think")
	if code := pr.wait(t, 60*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if pr.stdout.String() != "Yes.\n" || !strings.Contains(pr.stderr.String(), "thinking: hmm, yes\n") {
		t.Fatalf("stdout %q stderr %q", pr.stdout.String(), pr.stderr.String())
	}
	if !s.p.agent().Opens[0].Thinking {
		t.Fatal("the OPEN didn't ask for thinking")
	}
}

func TestPromptExitCodes(t *testing.T) {
	s := newLocalSeven(t)
	cases := []struct {
		script string
		code   int
		stderr string
	}{
		{`{steps: [{event: {t: 'error', id: 'e1', message: 'model overloaded', detail: '529'}}], end: {reason: 'error', message: 'model overloaded'}}`, 1, "the run failed: model overloaded"},
		{`{steps: [{event: {t: 'text', id: 'a1', delta: 'partial'}}], end: {reason: 'aborted'}}`, 1, "the run was aborted"},
		{`{steps: [{event: {t: 'text', id: 'a1', delta: 'fine'}}], end: {reason: 'stop'}}`, 0, ""},
	}
	for _, c := range cases {
		s.p.agentDo("queue(" + c.script + ")")
		pr := s.run(t, nil, "prompt", "go")
		code := pr.wait(t, 60*time.Second)
		if code != c.code || !strings.Contains(pr.stderr.String(), c.stderr) {
			t.Fatalf("%s: exit %d, stderr %q", c.script, code, pr.stderr.String())
		}
	}
}

func TestPromptReadsFilesAndStdin(t *testing.T) {
	s := newLocalSeven(t)
	dir := t.TempDir()
	short := filepath.Join(dir, "short.txt")
	if err := os.WriteFile(short, []byte("from a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("0123456789abcdef", 8192) + "ü"
	longFile := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(longFile, []byte(long+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"@" + short}, {"-"}, {"@" + longFile}} {
		pr := s.run(t, strings.NewReader("from stdin\n"), "prompt", args...)
		if code := pr.wait(t, 60*time.Second); code != 0 || pr.stdout.String() != "ok\n" {
			t.Fatalf("%v: exit %d, stdout %q", args, code, pr.stdout.String())
		}
	}
	st := s.p.agent()
	if len(st.Prompts) != 3 || st.Prompts[0] != "from a file" || st.Prompts[1] != "from stdin" || st.Prompts[2] != long {
		t.Fatalf("prompts %d: %.40q", len(st.Prompts), st.Prompts)
	}
	if st.Opens[2].Length != len(long) || st.Opens[2].TextLength != 0 || st.Opens[0].TextLength != len("from a file") {
		t.Fatalf("opens %+v", st.Opens)
	}
	pr := s.run(t, nil, "prompt", "@"+filepath.Join(dir, "missing.txt"))
	if code := pr.wait(t, 30*time.Second); code != 2 {
		t.Fatalf("a missing file: exit %d", code)
	}
	pr = s.run(t, strings.NewReader(""), "prompt", "-")
	if code := pr.wait(t, 30*time.Second); code != 2 || !strings.Contains(pr.stderr.String(), "empty") {
		t.Fatalf("an empty prompt: exit %d, %s", code, pr.stderr.String())
	}
}

func TestPromptJSONPrintsTheRawEvents(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo(`queue({steps: [{event: {t: 'text', id: 'a1', delta: 'Hi'}}, {event: {t: 'tool', id: 't1', call: 'c1', name: 'bash', summary: 'pwd'}}]})`)
	pr := s.run(t, nil, "prompt", "--json", "hi")
	if code := pr.wait(t, 60*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var kinds []string
	sc := bufio.NewScanner(strings.NewReader(pr.stdout.String()))
	for sc.Scan() {
		var e map[string]any
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("not NDJSON: %q", sc.Text())
		}
		kinds = append(kinds, e["t"].(string))
	}
	if strings.Join(kinds, ",") != "user,run,text,tool,run,end" {
		t.Fatalf("events %v", kinds)
	}
	if strings.Contains(pr.stderr.String(), "tool bash") {
		t.Fatalf("--json still rendered: %s", pr.stderr.String())
	}
}

func TestPromptWithoutTheCapExitsThree(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("configure({allow: false})")
	want := "this seven doesn't allow remote control from this device; turn it on in seven's Linked devices"
	for _, verb := range [][]string{{"prompt", "hi"}, {"watch"}, {"abort"}} {
		pr := s.run(t, nil, verb[0], verb[1:]...)
		if code := pr.wait(t, 60*time.Second); code != 3 || !strings.Contains(pr.stderr.String(), want) {
			t.Fatalf("%v: exit %d, stderr %q", verb, code, pr.stderr.String())
		}
	}
	if st := s.p.agent(); len(st.Opens) != 0 {
		t.Fatalf("opened agent streams without the cap: %+v", st.Opens)
	}
}

func TestPromptWhenBusy(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("configure({busy: true})")
	pr := s.run(t, nil, "prompt", "--reject", "now")
	if code := pr.wait(t, 60*time.Second); code != 1 || !strings.Contains(pr.stderr.String(), "seven is busy") {
		t.Fatalf("reject: exit %d, %q", code, pr.stderr.String())
	}
	long := strings.Repeat("x", 70*1024)
	pr = s.run(t, strings.NewReader(long), "prompt", "--reject", "-")
	if code := pr.wait(t, 60*time.Second); code != 1 || !strings.Contains(pr.stderr.String(), "seven is busy") {
		t.Fatalf("a long reject: exit %d, %q", code, pr.stderr.String())
	}
	pr = s.run(t, nil, "prompt", "--steer", "this way")
	if code := pr.wait(t, 60*time.Second); code != 0 {
		t.Fatalf("steer: exit %d", code)
	}
	st := s.p.agent()
	if len(st.Opens) != 3 || st.Opens[0].WhenBusy != "reject" || st.Opens[2].WhenBusy != "steer" {
		t.Fatalf("opens %+v", st.Opens)
	}
	pr = s.run(t, nil, "prompt", "--steer", "--reject", "x")
	if code := pr.wait(t, 30*time.Second); code != 2 {
		t.Fatalf("--steer --reject: exit %d", code)
	}
}

func TestPromptInterrupt(t *testing.T) {
	s := newLocalSeven(t)
	for _, abort := range []bool{false, true} {
		s.p.agentDo(`queue({steps: [{event: {t: 'text', id: 'a1', delta: 'working'}}, {hold: true}]})`)
		args := []string{"long job"}
		if abort {
			args = append([]string{"--abort-on-interrupt"}, args...)
		}
		pr := s.run(t, nil, "prompt", args...)
		pr.waitFor(t, pr.stdout, "working", 60*time.Second)
		before := s.p.agent().Aborts
		if err := interrupt(pr.cmd); err != nil {
			t.Fatal(err)
		}
		if code := pr.wait(t, 30*time.Second); code != 130 {
			t.Fatalf("abort %v: exit %d", abort, code)
		}
		st := s.p.agent()
		if abort && st.Aborts != before+1 || !abort && st.Aborts != before {
			t.Fatalf("abort %v: aborts %d → %d", abort, before, st.Aborts)
		}
		if !abort {
			s.p.agentDo("release()")
		}
		s.p.waitAgent(t, "an idle agent", func(st agentState) bool { return !st.Busy })
	}
}

func TestAbort(t *testing.T) {
	s := newLocalSeven(t)
	ab := s.run(t, nil, "abort")
	if code := ab.wait(t, 60*time.Second); code != 0 || !strings.Contains(ab.stderr.String(), "nothing was running") {
		t.Fatalf("idle abort: exit %d, %q", code, ab.stderr.String())
	}
	s.p.agentDo(`queue({steps: [{event: {t: 'text', id: 'a1', delta: 'busy'}}, {hold: true}, {event: {t: 'text', id: 'a1', delta: 'never'}}]})`)
	pr := s.run(t, nil, "prompt", "work")
	pr.waitFor(t, pr.stdout, "busy", 60*time.Second)
	ab = s.run(t, nil, "abort")
	if code := ab.wait(t, 60*time.Second); code != 0 || !strings.Contains(ab.stderr.String(), "aborted the run") {
		t.Fatalf("abort: exit %d, %q", code, ab.stderr.String())
	}
	if code := pr.wait(t, 30*time.Second); code != 1 || !strings.Contains(pr.stderr.String(), "the run was aborted") || strings.Contains(pr.stdout.String(), "never") {
		t.Fatalf("aborted prompt: exit %d, %q", code, pr.stderr.String())
	}
}

func TestPromptNextToAttachKeepsBothLocalLinks(t *testing.T) {
	s := newLocalSeven(t)
	echo := serveTCP(t, func(c net.Conn) {
		_, _ = io.Copy(c, c)
		_ = c.(*net.TCPConn).CloseWrite()
		_, _ = io.Copy(io.Discard, c)
	})
	host, port := hostPort(t, echo)
	a := startAttach(t, "--cdp", s.b.endpoint, "--origin", s.origin, "--no-kernel")
	a.waitStderr(t, "link up with", 60*time.Second)
	s.p.agentDo(`queue({steps: [{event: {t: 'text', id: 'a1', delta: 'side by side'}}, {hold: true}]})`)
	pr := s.run(t, nil, "prompt", "hi")
	pr.waitFor(t, pr.stdout, "side by side", 60*time.Second)
	up := upLinks(s.p.links())
	if len(up) != 2 || up[0].HelloNode == nil || up[1].HelloNode == nil || *up[0].HelloNode == *up[1].HelloNode || *up[0].Node != *up[0].HelloNode {
		t.Fatalf("links %+v", s.p.links())
	}
	for _, l := range up {
		if l.Caps == nil || (len(l.Caps) == 0) == strings.Contains(strings.Join(l.Caps, ","), "net") {
			t.Fatalf("links %+v", up)
		}
	}
	s.p.agentDo("release()")
	if code := pr.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(s.p.links()) != 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if ls := s.p.links(); len(ls) != 1 || !ls[0].Up || !ls[0].Current {
		t.Fatalf("after prompt: %+v", ls)
	}
	var res struct {
		OK bool `json:"ok"`
	}
	s.p.eval("linkTest.echo("+jsArgs(host, port, 1000)+")", &res)
	if !res.OK {
		t.Fatal("the bridge link doesn't carry tcp after prompt left")
	}
	if strings.Contains(a.stderr.String(), "replaced") {
		t.Fatalf("attach's link was replaced:\n%s", a.stderr.String())
	}
}

func TestAgentUsage(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"prompt", "hi"}, "--cdp"},
		{[]string{"watch"}, "--cdp"},
		{[]string{"abort", "--cdp", "http://127.0.0.1:1", "extra"}, "unexpected"},
		{[]string{"prompt", "--cdp", "http://127.0.0.1:1"}, "say what to send"},
		{[]string{"http://127.0.0.1:1/join/tok.00112233445566778899", "prompt"}, "say what to send"},
		{[]string{"http://127.0.0.1:1/join/tok.00112233445566778899", "watch", "--cdp", "x"}, "flag provided but not defined"},
		{[]string{"http://127.0.0.1:1/join/tok.00112233445566778899", "frob"}, "follow, prompt, watch or abort"},
	}
	for _, c := range cases {
		_, errOut, code := slicc(t, c.args...)
		if code != 2 || !strings.Contains(errOut, c.want) {
			t.Fatalf("%v: exit %d, %q", c.args, code, errOut)
		}
		if strings.Contains(errOut, "tok.00112233445566778899") {
			t.Fatalf("%v echoed the join URL", c.args)
		}
	}
	for _, verb := range []string{"prompt", "watch", "abort"} {
		out, _, code := slicc(t, verb, "--help")
		if code != 0 || !strings.Contains(out, "usage: slicc <join-url> "+verb) {
			t.Fatalf("%s --help: %d %q", verb, code, out)
		}
	}
}
