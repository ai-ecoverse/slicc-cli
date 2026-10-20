//go:build !windows

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

var (
	cursorForward = regexp.MustCompile(`\x1b\[(\d*)C`)
	ansiSequence  = regexp.MustCompile(`\x1b\[[0-9;?<>=$]*[A-Za-z~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[=>()][0-9A-Za-z]?`)
	screenURL     = regexp.MustCompile(`https?://\S+#slicc-pair=\d+\.[A-Za-z0-9_-]{22}\.[0-9a-f]{64}`)
)

type ttyProc struct {
	cmd  *exec.Cmd
	tty  *os.File
	mu   sync.Mutex
	raw  bytes.Buffer
	done chan error
}

func startTTY(t *testing.T, env []string, args ...string) *ttyProc {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = overrideEnv(env, map[string]string{"TERM": "xterm-256color", "NO_COLOR": "1", "SLICC_NO_TELEMETRY": "1"})
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 60, Cols: 250})
	if err != nil {
		t.Fatal(err)
	}
	p := &ttyProc{cmd: cmd, tty: tty, done: make(chan error, 1)}
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := tty.Read(buf)
			p.mu.Lock()
			p.raw.Write(buf[:n])
			p.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
		_ = tty.Close()
		if t.Failed() {
			t.Logf("screen:\n%s", p.text())
		}
	})
	return p
}

func (p *ttyProc) text() string {
	p.mu.Lock()
	s := p.raw.String()
	p.mu.Unlock()
	s = cursorForward.ReplaceAllStringFunc(s, func(m string) string {
		n, err := strconv.Atoi(cursorForward.FindStringSubmatch(m)[1])
		if err != nil {
			n = 1
		}
		return strings.Repeat(" ", n)
	})
	return strings.ReplaceAll(ansiSequence.ReplaceAllString(s, ""), "\r", "")
}

func (p *ttyProc) since(mark int) string {
	s := p.text()
	return s[min(mark, len(s)):]
}

func (p *ttyProc) wait(t *testing.T, from int, want string) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if s := p.since(from); strings.Contains(s, want) {
			return s
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the terminal never showed %q", want)
	return ""
}

func (p *ttyProc) keys(t *testing.T, s string) {
	t.Helper()
	if _, err := p.tty.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultBrowserRowPairsInTheDashboard(t *testing.T) {
	e := newLoopEnv(t)
	opened := filepath.Join(t.TempDir(), "opened")
	e.env = overrideEnv(e.env, map[string]string{"SLICC_OPEN_COMMAND": buildFakeOpen(t), "SLICC_FAKEOPEN_OUT": opened})
	b := launchBrowser(t)
	srv := pageServer(t)
	page := srv.URL + "/index.html?ports=" + e.ports()
	p := startTTY(t, e.env, "--page", page, "--name", "it-pair", "--no-kernel")

	picker := p.wait(t, 0, "Default browser")
	var items []string
	for _, l := range strings.Split(picker[strings.LastIndex(picker, "choose where seven runs"):], "\n") {
		if strings.Contains(l, "○") || strings.Contains(l, "▸ ") {
			items = append(items, l)
		}
	}
	at := -1
	for i, l := range items {
		if strings.Contains(l, "Default browser") {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatalf("no Default browser row in %q", items)
	}
	if !strings.Contains(p.text(), "Pair  seven in your everyday browser, linked over loopback without cdp") {
		t.Fatal("no Pair section")
	}
	p.keys(t, strings.Repeat("j", at))
	p.wait(t, 0, "↵ Opens seven in your default browser and pairs it over loopback")
	mark := len(p.text())
	p.keys(t, "\r")
	dash := p.wait(t, mark, "waiting for seven")
	for _, want := range []string{"Default browser  paired over loopback · no cdp  hosts seven", "↵ new pairing URL", "opened the pairing URL in the default browser"} {
		dash = p.wait(t, mark, want)
	}
	u := screenURL.FindString(dash)
	if u == "" {
		t.Fatalf("no pairing URL on the dashboard:\n%s", dash)
	}
	got, _ := os.ReadFile(opened)
	if strings.TrimSpace(string(got)) != u {
		t.Fatalf("the opener got %q, the dashboard shows %q", got, u)
	}
	if strings.Contains(p.text(), "No SLICC browser or debugging endpoint found") {
		t.Fatal("the fallback notice came up on a terminal")
	}

	mark = len(p.text())
	p.keys(t, "\r")
	p.wait(t, mark, "a new pairing URL, good once for 10 minutes")
	fresh := screenURL.FindAllString(p.since(mark), -1)
	if len(fresh) == 0 || fresh[len(fresh)-1] == u {
		t.Fatalf("Enter gave no new pairing URL: %q", fresh)
	}
	u = fresh[len(fresh)-1]

	pg := b.openPage(t, u)
	if r := confirm(t, pg, ""); !r.Linked {
		t.Fatalf("pairing: %+v", r)
	}
	p.wait(t, mark, `linked with "link test page"`)

	p.keys(t, "q")
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatalf("exit: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("slicc did not exit after q")
	}
}
