package integration

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type agentProc struct {
	cmd    *exec.Cmd
	stdout *syncBuffer
	stderr *syncBuffer
	exited chan int
}

func startAgentCmd(t *testing.T, env []string, stdin io.Reader, args ...string) *agentProc {
	t.Helper()
	cmd := exec.Command(binary, args...)
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(append([]string(nil), env...), "SLICC_NO_TELEMETRY=1")
	cmd.Stdin = stdin
	p := &agentProc{cmd: cmd, stdout: &syncBuffer{}, stderr: &syncBuffer{}, exited: make(chan int, 1)}
	cmd.Stdout, cmd.Stderr = p.stdout, p.stderr
	prepareInterruptible(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		err := cmd.Wait()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
		p.exited <- code
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		if t.Failed() {
			t.Logf("slicc %s\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), p.stdout.String(), p.stderr.String())
		}
	})
	return p
}

func (p *agentProc) wait(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case code := <-p.exited:
		return code
	case <-time.After(timeout):
		t.Fatalf("slicc did not exit\nstdout:\n%s\nstderr:\n%s", p.stdout.String(), p.stderr.String())
		return -1
	}
}

func (p *agentProc) waitFor(t *testing.T, buf *syncBuffer, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !strings.Contains(buf.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("never saw %q\nstdout:\n%s\nstderr:\n%s", want, p.stdout.String(), p.stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type agentOpen struct {
	Kind       string `json:"kind"`
	Op         string `json:"op"`
	WhenBusy   string `json:"whenBusy"`
	Thinking   bool   `json:"thinking"`
	Since      string `json:"since"`
	Length     int    `json:"length"`
	TextLength int    `json:"textLength"`
}

type agentState struct {
	Allow   bool        `json:"allow"`
	Busy    bool        `json:"busy"`
	Opens   []agentOpen `json:"opens"`
	Prompts []string    `json:"prompts"`
	Aborts  int         `json:"aborts"`
	Watches int         `json:"watches"`
	Held    bool        `json:"held"`
	Log     int         `json:"log"`
}

func (p *page) agent() agentState {
	var st agentState
	p.eval("linkTest.agent.state()", &st)
	return st
}

func (p *page) agentDo(expr string) {
	var out any
	p.eval("linkTest.agent."+expr, &out)
}

func (p *page) waitAgent(t *testing.T, what string, ok func(agentState) bool) agentState {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		st := p.agent()
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fake agent never got %s: %+v", what, st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type pageLink struct {
	Node      *string  `json:"node"`
	HelloNode *string  `json:"helloNode"`
	Up        bool     `json:"up"`
	Caps      []string `json:"caps"`
	Current   bool     `json:"current"`
}

func (p *page) links() []pageLink {
	var out []pageLink
	p.eval("linkTest.links()", &out)
	return out
}

func upLinks(ls []pageLink) []pageLink {
	var out []pageLink
	for _, l := range ls {
		if l.Up {
			out = append(out, l)
		}
	}
	return out
}

type localSeven struct {
	b      *chromeProc
	p      *page
	origin string
}

func newLocalSeven(t *testing.T) *localSeven {
	t.Helper()
	srv := pageServer(t)
	b, p := launchWithPage(t, srv.URL+"/index.html")
	return &localSeven{b: b, p: p, origin: srv.URL}
}

func (s *localSeven) args(verb string, rest ...string) []string {
	return append([]string{verb, "--cdp", s.b.endpoint, "--origin", s.origin}, rest...)
}

func (s *localSeven) run(t *testing.T, stdin io.Reader, verb string, rest ...string) *agentProc {
	t.Helper()
	return startAgentCmd(t, nil, stdin, s.args(verb, rest...)...)
}
