package integration

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func (p *page) waitWatches(t *testing.T, opens int) agentState {
	t.Helper()
	return p.waitAgent(t, "a watch", func(st agentState) bool {
		n := 0
		for _, o := range st.Opens {
			if o.Op == "watch" {
				n++
			}
		}
		return st.Watches == 1 && n >= opens
	})
}

func lastWatch(st agentState) agentOpen {
	var w agentOpen
	for _, o := range st.Opens {
		if o.Op == "watch" {
			w = o
		}
	}
	return w
}

func TestWatchLocalResumesWithoutDuplicates(t *testing.T) {
	s := newLocalSeven(t)
	w := s.run(t, nil, "watch")
	st := s.p.waitWatches(t, 1)
	if o := lastWatch(st); o.Since != "" || o.Thinking {
		t.Fatalf("first watch %+v", o)
	}
	s.p.agentDo(`emit([{t: 'user', id: 'u1', text: 'hi', delivered: 'run'}, {t: 'run', state: 'start'}, {t: 'text', id: 'a1', delta: 'Hel'}])`)
	w.waitFor(t, w.stdout, "Hel", 30*time.Second)
	var n any
	s.p.eval(`(linkTest.agent.resetWatches(), linkTest.agent.emit([{t: 'text', id: 'a1', delta: 'lo'}]))`, &n)
	st = s.p.waitWatches(t, 2)
	if o := lastWatch(st); o.Since != "a1" {
		t.Fatalf("resumed watch %+v", o)
	}
	s.p.agentDo(`emit([{t: 'tool', id: 't1', call: 'c1', name: 'bash', summary: 'ls'}, {t: 'tool-result', id: 'r1', call: 'c1', ok: true, summary: '2 files'}])`)
	w.waitFor(t, w.stderr, "tool bash ok: 2 files", 30*time.Second)
	s.p.eval(`(linkTest.agent.resetWatches(), linkTest.agent.emit([{t: 'text', id: 'a2', delta: 'Bye'}]))`, &n)
	st = s.p.waitWatches(t, 3)
	if o := lastWatch(st); o.Since != "r1" {
		t.Fatalf("second resume %+v", o)
	}
	s.p.agentDo(`emit([{t: 'run', state: 'end', reason: 'aborted'}])`)
	w.waitFor(t, w.stderr, "notice: run ended: aborted", 30*time.Second)
	w.waitFor(t, w.stdout, "Bye\n", 30*time.Second)
	if got, want := w.stdout.String(), "> hi\nHello\n\nBye\n"; got != want {
		t.Fatalf("stdout %q, want %q", got, want)
	}
	if errOut := w.stderr.String(); strings.Count(errOut, "tool bash: ls") != 1 || strings.Count(errOut, "tool bash ok: 2 files") != 1 {
		t.Fatalf("stderr:\n%s", errOut)
	}
	s.p.eval(`(linkTest.agent.resetWatches(), linkTest.agent.clear())`, &n)
	s.p.waitWatches(t, 4)
	w.waitFor(t, w.stderr, "notice: some events were missed", 30*time.Second)
	if err := interrupt(w.cmd); err != nil {
		t.Fatal(err)
	}
	if code := w.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("watch exit %d", code)
	}
}

func TestWatchJSONAndThinking(t *testing.T) {
	s := newLocalSeven(t)
	w := s.run(t, nil, "watch", "--json", "--thinking")
	st := s.p.waitWatches(t, 1)
	if !lastWatch(st).Thinking {
		t.Fatal("watch didn't ask for thinking")
	}
	s.p.agentDo(`emit([{t: 'thinking', id: 'k1', delta: 'hm'}, {t: 'text', id: 'a1', delta: 'Hel'}])`)
	w.waitFor(t, w.stdout, `"Hel"`, 30*time.Second)
	var n any
	s.p.eval(`(linkTest.agent.resetWatches(), linkTest.agent.emit([{t: 'text', id: 'a1', delta: 'lo'}]))`, &n)
	s.p.waitWatches(t, 2)
	w.waitFor(t, w.stdout, `"lo"`, 30*time.Second)
	want := `{"t":"thinking","id":"k1","delta":"hm"}` + "\n" + `{"t":"text","id":"a1","delta":"Hel"}` + "\n" + `{"delta":"lo","id":"a1","t":"text"}` + "\n"
	if got := w.stdout.String(); got != want {
		t.Fatalf("stdout %q, want %q", got, want)
	}
	if err := interrupt(w.cmd); err != nil {
		t.Fatal(err)
	}
	if code := w.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("watch exit %d", code)
	}
}

func TestPromptOverTheTrayNextToFollow(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	join := h.joinURL(trays[0])
	echo := serveTCP(t, func(c net.Conn) {
		_, _ = io.Copy(c, c)
		_ = c.(*net.TCPConn).CloseWrite()
		_, _ = io.Copy(io.Discard, c)
	})
	host, port := hostPort(t, echo)
	fp := startFollow(t, env, join, "--exit", "--allow", "127.0.0.1/32")
	fingerprint, _ := fp.fingerprint(t)
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.agentDo("queue(" + toolScript + ")")
	pr := startAgentCmd(t, env, nil, join, "prompt", "from", "afar")
	pr.waitFor(t, pr.stdout, "Hello", 60*time.Second)
	if p.fingerprint() != fingerprint {
		t.Fatalf("prompt is not the same device: %s vs %s", p.fingerprint(), fingerprint)
	}
	up := upLinks(p.links())
	if len(up) != 2 || up[0].HelloNode == nil || up[1].HelloNode == nil || *up[0].HelloNode == *up[1].HelloNode {
		t.Fatalf("links %+v", p.links())
	}
	p.agentDo("release()")
	if code := pr.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if pr.stdout.String() != "Hello, world.\n\nDone.\n" || !strings.Contains(pr.stderr.String(), "tool bash ok: 3 files") {
		t.Fatalf("stdout %q\nstderr %s", pr.stdout.String(), pr.stderr.String())
	}
	if st := p.agent(); len(st.Prompts) != 1 || st.Prompts[0] != "from afar" {
		t.Fatalf("prompts %q", st.Prompts)
	}
	deadline := time.Now().Add(15 * time.Second)
	for up := upLinks(p.links()); len(up) != 1 || !up[0].Current; up = upLinks(p.links()) {
		if time.Now().After(deadline) {
			t.Fatalf("the page did not fall back to follow's link after the prompt: links %+v", p.links())
		}
		time.Sleep(100 * time.Millisecond)
	}
	var res struct {
		OK bool `json:"ok"`
	}
	p.eval("linkTest.echo("+jsArgs(host, port, 1000)+")", &res)
	if !res.OK || strings.Contains(fp.stderr.String(), "reconnecting") {
		t.Fatalf("follow's link didn't survive the prompt (echo %v):\n%s", res.OK, fp.stderr.String())
	}
	assertRedacted(t, pr.stderr.String()+pr.stdout.String(), h, trays...)
}

func TestAgentOverTheTrayWithoutTheCap(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	p.agentDo("configure({allow: false})")
	pr := startAgentCmd(t, env, nil, h.joinURL(trays[0]), "prompt", "hi")
	if code := pr.wait(t, 90*time.Second); code != 3 || !strings.Contains(pr.stderr.String(), "turn it on in seven's Linked devices") {
		t.Fatalf("exit %d, %s", code, pr.stderr.String())
	}
}

func TestWatchOverTheTrayReconnects(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	w := startAgentCmd(t, env, nil, h.joinURL(trays[0]), "watch")
	p.waitWatches(t, 1)
	first := p.status()
	p.agentDo(`emit([{t: 'user', id: 'u1', text: 'hi', delivered: 'run'}, {t: 'text', id: 'a1', delta: 'one'}])`)
	w.waitFor(t, w.stdout, "one", 30*time.Second)
	var n any
	p.eval(`(linkTest.drop(), linkTest.agent.emit([{t: 'text', id: 'a1', delta: ' two'}, {t: 'notice', id: 'n1', text: 'compacted'}]))`, &n)
	p.waitUp(t, first.Nonce)
	st := p.waitWatches(t, 2)
	if o := lastWatch(st); o.Since != "a1" {
		t.Fatalf("resumed watch %+v", o)
	}
	w.waitFor(t, w.stderr, "notice: compacted", 30*time.Second)
	if got := w.stdout.String(); got != "> hi\none two" {
		t.Fatalf("stdout %q", got)
	}
	w.waitFor(t, w.stderr, "slicc: lost the link to link test page; reconnecting", 5*time.Second)
	if err := interrupt(w.cmd); err != nil {
		t.Fatal(err)
	}
	if code := w.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("watch exit %d", code)
	}
	assertRedacted(t, w.stderr.String(), h, trays...)
}
