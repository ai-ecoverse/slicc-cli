package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

type stdioAgent struct {
	*agentProc
	events *syncBuffer
}

func startStdioAgent(t *testing.T, p *page, args ...string) *stdioAgent {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "SLICC_NO_TELEMETRY=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	a := &stdioAgent{agentProc: &agentProc{cmd: cmd, stdout: &syncBuffer{}, stderr: &syncBuffer{}, exited: make(chan int, 1)}, events: &syncBuffer{}}
	cmd.Stderr = a.stderr
	prepareInterruptible(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sub := p.b.client.Subscribe()
	t.Cleanup(sub.Close)
	if err := p.session.AddBinding(ctx, "sliccLinkSend"); err != nil {
		t.Fatal(err)
	}
	if err := p.session.Call(ctx, "Runtime.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	go func() {
		for ev := range sub.Events() {
			var bc cdp.BindingCalled
			if ev.SessionID != p.session.ID || ev.Method != "Runtime.bindingCalled" || json.Unmarshal(ev.Params, &bc) != nil || bc.Name != "sliccLinkSend" {
				continue
			}
			mu.Lock()
			_, _ = io.WriteString(stdin, bc.Payload+"\n")
			mu.Unlock()
		}
	}()
	relayed := make(chan struct{})
	go func() {
		defer close(relayed)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		for sc.Scan() {
			line := sc.Text()
			var m map[string]any
			_ = json.Unmarshal([]byte(line), &m)
			if _, setup := m["nonce"]; !setup {
				_, _ = a.events.Write([]byte(line + "\n"))
				continue
			}
			_, _ = a.stdout.Write([]byte(line + "\n"))
			expr := "typeof globalThis.sliccLinkReceive === 'function' ? (globalThis.sliccLinkReceive(" + line + "), true) : false"
			for i := 0; i < 300; i++ {
				var ok bool
				c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := p.session.Evaluate(c, expr, cdp.EvaluateOptions{ReturnByValue: true}, &ok)
				cancel()
				if err == nil && ok {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()
	go func() {
		<-relayed
		err := cmd.Wait()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
		a.exited <- code
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		if t.Failed() {
			t.Logf("slicc %s\nsetup:\n%s\nevents:\n%s\nstderr:\n%s", strings.Join(args, " "), a.stdout.String(), a.events.String(), a.stderr.String())
		}
	})
	return a
}

func eventKinds(t *testing.T, ndjson string) string {
	t.Helper()
	var kinds []string
	for _, line := range strings.Split(strings.TrimSpace(ndjson), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("not NDJSON: %q", line)
		}
		kinds = append(kinds, e["t"].(string))
	}
	return strings.Join(kinds, ",")
}

func TestAgentOverStdio(t *testing.T) {
	b := launchBrowser(t)
	p := b.openPage(t, pageServer(t).URL+"/index.html")
	p.agentDo(`queue({steps: [{event: {t: 'text', id: 'a1', delta: 'Hi'}}, {event: {t: 'tool', id: 't1', call: 'c1', name: 'bash', summary: 'pwd'}}]})`)
	pr := startStdioAgent(t, p, "prompt", "--signal", "stdio", "hello", "there")
	if code := pr.wait(t, 60*time.Second); code != 0 {
		t.Fatalf("prompt exit %d", code)
	}
	if got := eventKinds(t, pr.events.String()); got != "user,run,text,tool,run,end" {
		t.Fatalf("events %s", got)
	}
	if !strings.Contains(pr.stdout.String(), `"t":"bye"`) {
		t.Fatalf("no bye on exit:\n%s", pr.stdout.String())
	}
	if st := p.agent(); len(st.Prompts) != 1 || st.Prompts[0] != "hello there" {
		t.Fatalf("prompts %q", st.Prompts)
	}

	w := startStdioAgent(t, p, "watch", "--signal", "stdio")
	p.waitWatches(t, 1)
	p.agentDo(`emit([{t: 'notice', id: 'n9', text: 'compacted'}])`)
	w.waitFor(t, w.events, `"compacted"`, 30*time.Second)
	ab := startStdioAgent(t, p, "abort", "--signal", "stdio")
	if code := ab.wait(t, 60*time.Second); code != 0 || !strings.Contains(ab.stderr.String(), "nothing was running") {
		t.Fatalf("abort: exit %d, %s", code, ab.stderr.String())
	}
	if err := interrupt(w.cmd); err != nil {
		t.Fatal(err)
	}
	if code := w.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("watch exit %d", code)
	}

	_, errOut, code := slicc(t, "prompt", "--signal", "stdio", "-")
	if code != 2 || !strings.Contains(errOut, "stdin carries the setup") {
		t.Fatalf("prompt from stdin over stdio: %d %q", code, errOut)
	}
}
