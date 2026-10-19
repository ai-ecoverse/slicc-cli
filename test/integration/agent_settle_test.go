package integration

import (
	"strings"
	"testing"
	"time"
)

func (p *page) busyWatches(t *testing.T, opens int) agentState {
	t.Helper()
	return p.waitAgent(t, "a busy watch", func(st agentState) bool {
		n := 0
		for _, o := range st.Opens {
			if o.Op == "watch" && o.Busy {
				n++
			}
		}
		return st.Watches == 1 && n >= opens
	})
}

func stillRunning(t *testing.T, p *agentProc, d time.Duration) {
	t.Helper()
	select {
	case code := <-p.exited:
		t.Fatalf("slicc exited %d before seven settled\nstdout:\n%s\nstderr:\n%s", code, p.stdout.String(), p.stderr.String())
	case <-time.After(d):
	}
}

const scoopScript = `{steps: [{event: {t: 'text', id: 'a1', delta: 'Working on it.'}}, {scoop: 'research'}]}`

const lickRun = `(linkTest.agent.configure({busy: true}), linkTest.agent.scoop('research', false), linkTest.agent.emit([
  {t: 'run', state: 'start'},
  {t: 'notice', id: 'n1', text: 'lick scoop-notify: research'},
  {t: 'text', id: 'a2', delta: 'The answer is 42.'},
  {t: 'run', state: 'end', reason: 'stop'}
]), linkTest.agent.configure({busy: false}))`

func TestPromptAllSettledOutlivesTheScoops(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("queue(" + scoopScript + ")")
	pr := s.run(t, nil, "prompt", "--allsettled", "0s", "find", "it")
	st := s.p.busyWatches(t, 2)
	if o := lastWatch(st); o.Since != "a1" || !o.Busy {
		t.Fatalf("the settle watch %+v", o)
	}
	if st.Opens[0].Op != "watch" || !st.Opens[0].Busy || st.Opens[1].Op != "prompt" {
		t.Fatalf("slicc didn't check for busy before sending: %+v", st.Opens)
	}
	stillRunning(t, pr, 1500*time.Millisecond)
	var n any
	s.p.eval(lickRun, &n)
	if code := pr.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got, want := pr.stdout.String(), "Working on it.\n\nThe answer is 42.\n"; got != want {
		t.Fatalf("stdout %q, want %q", got, want)
	}
	if !strings.Contains(pr.stderr.String(), "notice: lick scoop-notify: research\n") {
		t.Fatalf("stderr:\n%s", pr.stderr.String())
	}
}

func TestPromptAllSettledJSON(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("queue(" + scoopScript + ")")
	pr := s.run(t, nil, "prompt", "--json", "--allsettled", "0s", "go")
	s.p.busyWatches(t, 2)
	stillRunning(t, pr, time.Second)
	s.p.agentDo("scoop('research', false)")
	if code := pr.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := eventKinds(t, pr.stdout.String()); got != "user,run,text,run,end,busy,settled" {
		t.Fatalf("events %s\n%s", got, pr.stdout.String())
	}
	if !strings.HasSuffix(pr.stdout.String(), "{\"t\":\"busy\",\"busy\":false}\n{\"t\":\"settled\"}\n") {
		t.Fatalf("stdout %q", pr.stdout.String())
	}
}

func TestPromptAllSettledFailedRunExitsAtOnce(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo(`queue({steps: [{scoop: 'research'}], end: {reason: 'error', message: 'model overloaded'}})`)
	pr := s.run(t, nil, "prompt", "--allsettled", "2m", "go")
	if code := pr.wait(t, 60*time.Second); code != 1 || !strings.Contains(pr.stderr.String(), "the run failed: model overloaded") {
		t.Fatalf("exit %d, stderr %q", code, pr.stderr.String())
	}
}

func TestAllSettledNeedsASevenThatReportsBusy(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("configure({reportsBusy: false})")
	want := "this seven doesn't report when its agent is idle; update it"
	for _, args := range [][]string{{"prompt", "--allsettled", "0s", "hi"}, {"wait"}} {
		pr := s.run(t, nil, args[0], args[1:]...)
		if code := pr.wait(t, 60*time.Second); code != 1 || !strings.Contains(pr.stderr.String(), want) {
			t.Fatalf("%v: exit %d, stderr %q", args, code, pr.stderr.String())
		}
	}
	if st := s.p.agent(); len(st.Prompts) != 0 {
		t.Fatalf("sent a prompt it can't settle: %q", st.Prompts)
	}
}

func TestWaitReturnsOnceSevenIsIdle(t *testing.T) {
	s := newLocalSeven(t)
	pr := s.run(t, nil, "wait")
	if code := pr.wait(t, 60*time.Second); code != 0 || pr.stdout.String() != "" || !strings.Contains(pr.stderr.String(), "slicc: seven is idle\n") {
		t.Fatalf("an idle seven: exit %d, stdout %q, stderr %q", code, pr.stdout.String(), pr.stderr.String())
	}
	s.p.agentDo("scoop('research', true)")
	pr = s.run(t, nil, "wait", "--json")
	s.p.busyWatches(t, 2)
	stillRunning(t, pr, 1500*time.Millisecond)
	s.p.agentDo("configure({busy: true})")
	s.p.agentDo("scoop('research', false)")
	s.p.agentDo(`emit([{t: 'text', id: 'a7', delta: 'done'}])`)
	stillRunning(t, pr, time.Second)
	s.p.agentDo("configure({busy: false})")
	if code := pr.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got, want := pr.stdout.String(), "{\"t\":\"text\",\"id\":\"a7\",\"delta\":\"done\"}\n{\"t\":\"busy\",\"busy\":false}\n{\"t\":\"settled\"}\n"; got != want {
		t.Fatalf("stdout %q, want %q", got, want)
	}
	if st := s.p.agent(); len(st.Prompts) != 0 || st.Aborts != 0 {
		t.Fatalf("wait changed the agent: %+v", st)
	}
}

func TestWaitTimesOut(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo("scoop('research', true)")
	start := time.Now()
	pr := s.run(t, nil, "wait", "--timeout", "3s")
	code := pr.wait(t, 60*time.Second)
	if code != 124 || !strings.Contains(pr.stderr.String(), "slicc wait: seven didn't settle within 3s\n") {
		t.Fatalf("exit %d, stderr %q", code, pr.stderr.String())
	}
	if time.Since(start) < 3*time.Second {
		t.Fatalf("gave up after %s", time.Since(start))
	}
}

func TestWaitAllSettledWaitsForQuiet(t *testing.T) {
	s := newLocalSeven(t)
	pr := s.run(t, nil, "wait", "--allsettled", "3s")
	s.p.busyWatches(t, 1)
	time.Sleep(1500 * time.Millisecond)
	s.p.agentDo(`emit([{t: 'notice', id: 'n1', text: 'compacted'}])`)
	sent := time.Now()
	if code := pr.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if d := time.Since(sent); d < 2500*time.Millisecond {
		t.Fatalf("settled %s after the last event, within the quiet period", d)
	}
}

func TestWaitUsage(t *testing.T) {
	for _, args := range [][]string{
		{"wait", "--cdp", "http://127.0.0.1:1", "extra"},
		{"wait", "--cdp", "http://127.0.0.1:1", "--allsettled", "-1s"},
		{"wait", "--cdp", "http://127.0.0.1:1", "--timeout", "soon"},
		{"wait", "--cdp", "http://127.0.0.1:1", "--plain"},
		{"prompt", "--cdp", "http://127.0.0.1:1", "--allsettled", "later", "hi"},
		{"watch", "--cdp", "http://127.0.0.1:1", "--allsettled", "1s"},
	} {
		if _, errOut, code := slicc(t, args...); code != 2 {
			t.Fatalf("%v: exit %d, %q", args, code, errOut)
		}
	}
	out, _, code := slicc(t, "wait", "--help")
	if code != 0 || !strings.Contains(out, "--allsettled <quiet>") || !strings.Contains(out, "124 on timeout") {
		t.Fatalf("wait --help: %d\n%s", code, out)
	}
	out, _, _ = slicc(t, "--help")
	for _, want := range []string{"prompt [--allsettled", "wait [--allsettled", "<join-url> abort"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--help lacks %q:\n%s", want, out)
		}
	}
}

func TestWaitOverStdio(t *testing.T) {
	_, p := launchWithPage(t, pageServer(t).URL+"/index.html")
	p.agentDo("scoop('research', true)")
	w := startStdioAgent(t, p, "wait", "--signal", "stdio")
	p.busyWatches(t, 1)
	stillRunning(t, w.agentProc, time.Second)
	p.agentDo("scoop('research', false)")
	if code := w.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := eventKinds(t, w.events.String()); got != "busy,settled" {
		t.Fatalf("events %s", got)
	}
}
