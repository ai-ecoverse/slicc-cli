//go:build !windows

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestPromptStylesOnATerminal(t *testing.T) {
	s := newLocalSeven(t)
	s.p.agentDo(`queue({steps: [{event: {t: 'text', id: 'a1', delta: 'Hi'}}, {event: {t: 'tool', id: 't1', call: 'c1', name: 'bash', summary: 'pwd'}}, {event: {t: 'tool-result', id: 'r1', call: 'c1', ok: true, summary: '/tmp'}}]})`)
	cmd := exec.Command(binary, s.args("prompt", "where")...)
	cmd.Env = append(os.Environ(), "SLICC_NO_TELEMETRY=1", "TERM=xterm-256color", "NO_COLOR=")
	tty, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tty.Close() }()
	var mu sync.Mutex
	var screen bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := tty.Read(buf)
			mu.Lock()
			screen.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("prompt: %v", err)
		}
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("prompt did not exit")
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	got := strings.ReplaceAll(screen.String(), "\r\n", "\n")
	mu.Unlock()
	for _, want := range []string{"Hi\n", "\x1b[1;36m⚙ bash\x1b[0m \x1b[2mpwd\x1b[0m", "\x1b[2m  ↳ /tmp\x1b[0m"} {
		if !strings.Contains(got, want) {
			t.Fatalf("terminal lacks %q:\n%q", want, got)
		}
	}
}
