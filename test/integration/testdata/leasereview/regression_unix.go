//go:build unix

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func TestLeaseReviewRestoreBeforeMakeRaw(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty")
	}
	m, s, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close(); _ = s.Close() }()
	cp := &coldPair{interrupted: true}
	sawRaw := false
	afterMakeRaw = func(int) { sawRaw = true }
	t.Cleanup(func() { afterMakeRaw = nil })
	type hid struct {
		got []byte
		err error
	}
	early := make(chan hid, 1)
	go func() {
		got, err := readHidden(s, func() {}, cp)
		early <- hid{got, err}
	}()
	select {
	case r := <-early:
		if r.err == nil || !errors.Is(r.err, errInterrupted) || r.got != nil {
			t.Fatalf("interrupted before entry: %q %v", r.got, r.err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("interrupted before entry blocked inside MakeRaw/Read")
	}
	if sawRaw {
		t.Fatal("MakeRaw ran after interrupt")
	}
	on, err := termiosEcho(int(s.Fd()))
	if err != nil || !on {
		t.Fatalf("MakeRaw ran after interrupt: echo=%v err=%v", on, err)
	}
	cp = &coldPair{}
	cp.mu.Lock()
	locked := true
	defer func() {
		if locked {
			cp.mu.Unlock()
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = readHidden(s, func() {}, cp)
	}()
	time.Sleep(300 * time.Millisecond)
	on, err = termiosEcho(int(s.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Fatal("the terminal went raw before the restore was published")
	}
	cp.mu.Unlock()
	locked = false
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		cp.mu.Lock()
		held, raw := cp.restore != nil, cp.raw
		cp.mu.Unlock()
		if held && raw {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("readHidden never reached raw mode with a held restore: held=%v raw=%v", held, raw)
		}
	}
	_, _ = m.Write([]byte{4})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("readHidden did not return")
	}
	cp.mu.Lock()
	restored, held := cp.restored, cp.restore != nil
	cp.mu.Unlock()
	if !restored || held {
		t.Fatalf("ordinary teardown left restore=%v restored=%v", held, restored)
	}
	if on, err := termiosEcho(int(s.Fd())); err != nil || !on {
		t.Fatalf("echo was not restored: %v %v", on, err)
	}
	t.Run("EchoStillOn", func(t *testing.T) {
		m2, s2, err := pty.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = m2.Close(); _ = s2.Close() }()
		afterMakeRaw = func(fd int) {
			if err := termiosForceEcho(fd); err != nil {
				t.Errorf("re-enable echo: %v", err)
			}
		}
		t.Cleanup(func() { afterMakeRaw = nil })
		type hid struct {
			got []byte
			err error
		}
		done := make(chan hid, 1)
		go func() {
			got, err := readHidden(s2, func() {}, &coldPair{})
			done <- hid{got, err}
		}()
		var r hid
		select {
		case r = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("echo check missing: readHidden proceeded with echo on")
		}
		if r.got != nil || r.err == nil || !strings.Contains(r.err.Error(), "echo could not be disabled") {
			t.Fatalf("echo still on: %q %v", r.got, r.err)
		}
		if on, err := termiosEcho(int(s2.Fd())); err != nil || !on {
			t.Fatalf("refused path left echo off: %v %v", on, err)
		}
	})
	t.Run("InterruptedBeforePrompt", func(t *testing.T) {
		m2, s2, err := pty.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = m2.Close(); _ = s2.Close() }()
		oldCand, oldTTY := pasteDisableCandidates, pasteDisableOpenTTY
		pasteDisableCandidates = func() []*os.File { return []*os.File{s2} }
		pasteDisableOpenTTY = func() (*os.File, error) { return nil, errors.New("no tty") }
		t.Cleanup(func() { pasteDisableCandidates, pasteDisableOpenTTY = oldCand, oldTTY })
		cp := &coldPair{}
		afterMakeRaw = func(int) {
			cp.mu.Lock()
			cp.interrupted = true
			cp.mu.Unlock()
		}
		t.Cleanup(func() { afterMakeRaw = nil })
		type hid struct {
			got []byte
			err error
		}
		prompted := false
		done := make(chan hid, 1)
		go func() {
			got, err := readHidden(s2, func() { prompted = true }, cp)
			done <- hid{got, err}
		}()
		var r hid
		select {
		case r = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("interrupted before prompt blocked inside Read")
		}
		if prompted || r.got != nil || r.err == nil || !errors.Is(r.err, errInterrupted) {
			t.Fatalf("interrupted before prompt: prompted=%v %q %v", prompted, r.got, r.err)
		}
	})
	t.Run("InterruptedBeforeRead", func(t *testing.T) {
		m2, s2, err := pty.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = m2.Close(); _ = s2.Close() }()
		oldCand, oldTTY := pasteDisableCandidates, pasteDisableOpenTTY
		pasteDisableCandidates = func() []*os.File { return []*os.File{s2} }
		pasteDisableOpenTTY = func() (*os.File, error) { return nil, errors.New("no tty") }
		t.Cleanup(func() { pasteDisableCandidates, pasteDisableOpenTTY = oldCand, oldTTY })
		cp := &coldPair{}
		type hid struct {
			got []byte
			err error
		}
		done := make(chan hid, 1)
		go func() {
			got, err := readHidden(s2, func() {
				cp.mu.Lock()
				cp.interrupted = true
				cp.mu.Unlock()
			}, cp)
			done <- hid{got, err}
		}()
		var r hid
		select {
		case r = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("interrupted before read blocked inside Read")
		}
		if r.got != nil || r.err == nil || !errors.Is(r.err, errInterrupted) {
			t.Fatalf("interrupted before read: %q %v", r.got, r.err)
		}
	})
}

func TestLeaseReviewStopIgnoredWhileRaw(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SigIgn probe; CI StopIgnoredAtHiddenJoin covers the PTY stop property")
	}
	undo := ignoreStopSignals()
	defer undo()
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	var sigIgn uint64
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "SigIgn:") {
			_, err := fmt.Sscanf(line, "SigIgn:\t%x", &sigIgn)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	const sigTSTP = 20
	if sigIgn&(1<<(sigTSTP-1)) == 0 {
		t.Fatal("ignoreStopSignals did not ignore SIGTSTP")
	}
}

type hiddenRun struct {
	got  []byte
	err  error
	echo []byte
	rest string
}

func hiddenPTY(t *testing.T, chunks []string, gap time.Duration) hiddenRun {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("pty")
	}
	m, s, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close(); _ = s.Close() }()
	oldCand, oldTTY := pasteDisableCandidates, pasteDisableOpenTTY
	pasteDisableCandidates = func() []*os.File { return []*os.File{s} }
	pasteDisableOpenTTY = func() (*os.File, error) { return nil, errors.New("no tty") }
	t.Cleanup(func() { pasteDisableCandidates, pasteDisableOpenTTY = oldCand, oldTTY })
	var mu sync.Mutex
	var echoed []byte
	go func() {
		b := make([]byte, 512)
		for {
			n, err := m.Read(b)
			mu.Lock()
			echoed = append(echoed, b[:n]...)
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	cp := &coldPair{}
	type res struct {
		b []byte
		e error
	}
	done := make(chan res, 1)
	go func() {
		b, e := readHidden(s, func() {}, cp)
		done <- res{b, e}
	}()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		cp.mu.Lock()
		raw := cp.raw
		cp.mu.Unlock()
		if raw {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("readHidden never went raw")
		}
	}
	for i, c := range chunks {
		if i > 0 {
			time.Sleep(gap)
		}
		if _, err := m.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	var out hiddenRun
	select {
	case r := <-done:
		out.got, out.err = r.b, r.e
	case <-time.After(5 * time.Second):
		t.Fatalf("readHidden did not return for %q", chunks)
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	out.echo = append([]byte(nil), echoed...)
	mu.Unlock()
	if _, err := m.Write([]byte("next\n")); err != nil {
		t.Fatal(err)
	}
	_ = s.SetReadDeadline(time.Now().Add(time.Second))
	line, _ := bufio.NewReader(s).ReadString('\n')
	out.rest = line
	return out
}

func pasteDisableOnly(echo []byte) bool {
	const disable = "\x1b[?2004l"
	for len(echo) > 0 {
		if bytes.HasPrefix(echo, []byte(disable)) {
			echo = echo[len(disable):]
			continue
		}
		return false
	}
	return true
}

func TestLeaseReviewHiddenPaste(t *testing.T) {
	const url = "https://hub.example/join/secret-token-1234"
	got := hiddenPTY(t, []string{url[:10], url[10:], "\r"}, 60*time.Millisecond)
	if got.err != nil || string(got.got) != url {
		t.Fatalf("chunked URL returned %q, %v", got.got, got.err)
	}
	if !bytes.Contains(got.echo, []byte("\x1b[?2004l")) || !pasteDisableOnly(got.echo) || bytes.Contains(got.echo, []byte("secret-token")) {
		t.Fatalf("paste disable / echo: %q", got.echo)
	}
	if got.rest != "next\n" && got.rest != "next\r\n" {
		t.Fatalf("leftover after Enter: %q", got.rest)
	}
	t.Run("Oversize", func(t *testing.T) {
		big := strings.Repeat("a", 5000)
		chunks := []string{big[:2000], big[2000:4000], big[4000:], "\nextra-secret\r"}
		got := hiddenPTY(t, chunks, 40*time.Millisecond)
		if got.err == nil || !strings.Contains(got.err.Error(), "too long") {
			t.Fatalf("oversize: %q %v", got.got, got.err)
		}
		if !pasteDisableOnly(got.echo) || bytes.Contains(got.echo, []byte("extra-secret")) || bytes.Contains(got.echo, []byte("aaa")) {
			t.Fatalf("oversize echoed: %q", got.echo)
		}
		if got.rest != "next\n" && got.rest != "next\r\n" {
			t.Fatalf("oversize leftover: %q", got.rest)
		}
	})
	t.Run("EmbeddedNewline", func(t *testing.T) {
		got := hiddenPTY(t, []string{"https://hub.example/join/", "part\nmore-secret\r"}, 40*time.Millisecond)
		if got.err != nil || string(got.got) != "https://hub.example/join/part" {
			t.Fatalf("embedded newline returned %q, %v", got.got, got.err)
		}
		if !pasteDisableOnly(got.echo) || bytes.Contains(got.echo, []byte("more-secret")) {
			t.Fatalf("bytes after newline were echoed: %q", got.echo)
		}
	})
	t.Run("EscapeRefused", func(t *testing.T) {
		got := hiddenPTY(t, []string{"https://hub.example/join/\x1b[200~secret\r"}, 40*time.Millisecond)
		if got.err == nil || !strings.Contains(got.err.Error(), "refused") {
			t.Fatalf("escape: %q %v", got.got, got.err)
		}
		if !pasteDisableOnly(got.echo) || bytes.Contains(got.echo, []byte("secret")) {
			t.Fatalf("refused escape echoed: %q", got.echo)
		}
	})
	t.Run("LateChunk", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("pty")
		}
		m, s, err := pty.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = m.Close(); _ = s.Close() }()
		oldCand, oldTTY := pasteDisableCandidates, pasteDisableOpenTTY
		pasteDisableCandidates = func() []*os.File { return []*os.File{s} }
		pasteDisableOpenTTY = func() (*os.File, error) { return nil, errors.New("no tty") }
		t.Cleanup(func() { pasteDisableCandidates, pasteDisableOpenTTY = oldCand, oldTTY })
		var mu sync.Mutex
		var echoed []byte
		go func() {
			b := make([]byte, 512)
			for {
				n, err := m.Read(b)
				mu.Lock()
				echoed = append(echoed, b[:n]...)
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}()
		cp := &coldPair{}
		done := make(chan hiddenRun, 1)
		go func() {
			b, e := readHidden(s, func() {}, cp)
			done <- hiddenRun{got: b, err: e}
		}()
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			cp.mu.Lock()
			raw := cp.raw
			cp.mu.Unlock()
			if raw {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("readHidden never went raw")
			}
		}
		if _, err := m.Write([]byte(url + "\r")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(120 * time.Millisecond)
		if _, err := m.Write([]byte("second-secret-line\r")); err != nil {
			t.Fatal(err)
		}
		var out hiddenRun
		select {
		case out = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("late chunk did not finish")
		}
		if out.err != nil || string(out.got) != url {
			t.Fatalf("late chunk returned %q %v", out.got, out.err)
		}
		mu.Lock()
		echo := append([]byte(nil), echoed...)
		mu.Unlock()
		if bytes.Contains(echo, []byte("second-secret")) {
			t.Fatalf("late secret echoed: %q", echo)
		}
		if _, err := m.Write([]byte("next\n")); err != nil {
			t.Fatal(err)
		}
		_ = s.SetReadDeadline(time.Now().Add(time.Second))
		rest, _ := bufio.NewReader(s).ReadString('\n')
		if rest != "next\n" && rest != "next\r\n" {
			t.Fatalf("late chunk leftover: %q", rest)
		}
	})
	t.Run("EscapeCtrlC", func(t *testing.T) {
		got := hiddenPTY(t, []string{"https://hub.example/join/\x1b", "\x03"}, 40*time.Millisecond)
		if got.err == nil || !errors.Is(got.err, errInterrupted) {
			t.Fatalf("escape ctrl-c: %q %v", got.got, got.err)
		}
	})
	t.Run("PeerHangup", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("pty")
		}
		m, s, err := pty.Open()
		if err != nil {
			t.Fatal(err)
		}
		oldCand, oldTTY := pasteDisableCandidates, pasteDisableOpenTTY
		pasteDisableCandidates = func() []*os.File { return []*os.File{s} }
		pasteDisableOpenTTY = func() (*os.File, error) { return nil, errors.New("no tty") }
		defer func() { pasteDisableCandidates, pasteDisableOpenTTY = oldCand, oldTTY }()
		cp := &coldPair{}
		done := make(chan hiddenRun, 1)
		go func() {
			b, e := readHidden(s, func() {}, cp)
			done <- hiddenRun{got: b, err: e}
		}()
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			cp.mu.Lock()
			raw := cp.raw
			cp.mu.Unlock()
			if raw {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("readHidden never went raw")
			}
		}
		if _, err := m.Write([]byte(url + "\r")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
		_ = m.Close()
		start := time.Now()
		select {
		case out := <-done:
			if took := time.Since(start); took > 1500*time.Millisecond {
				t.Fatalf("hangup drain took %s", took)
			}
			if out.err != nil || string(out.got) != url {
				t.Fatalf("hangup returned %q %v", out.got, out.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("hangup drain spun")
		}
		_ = s.Close()
	})
	t.Run("EINTRRetry", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("pty")
		}
		old := quietDrainPoll
		var n int
		quietDrainPoll = func(fds []unix.PollFd, timeout int) (int, error) {
			n++
			if n == 1 {
				return 0, unix.EINTR
			}
			return old(fds, timeout)
		}
		t.Cleanup(func() { quietDrainPoll = old })
		got := hiddenPTY(t, []string{url + "\r"}, 0)
		if got.err != nil || string(got.got) != url {
			t.Fatalf("eintr: %q %v", got.got, got.err)
		}
		if n < 2 {
			t.Fatal("EINTR did not retry the quiet drain")
		}
	})
}

func TestLeaseReviewPasteDisableTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty")
	}
	openPair := func(t *testing.T) (master, slave *os.File) {
		t.Helper()
		m, s, err := pty.Open()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Close(); _ = s.Close() })
		return m, s
	}
	t.Run("SameDevice", func(t *testing.T) {
		ma, sa := openPair(t)
		mb, sb := openPair(t)
		ro, err := os.OpenFile(sa.Name(), os.O_RDONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ro.Close() })
		oldCand, oldTTY := pasteDisableCandidates, pasteDisableOpenTTY
		pasteDisableCandidates = func() []*os.File { return []*os.File{sb, sa} }
		pasteDisableOpenTTY = func() (*os.File, error) { return nil, errors.New("no tty") }
		t.Cleanup(func() { pasteDisableCandidates, pasteDisableOpenTTY = oldCand, oldTTY })
		readMaster := func(m *os.File) <-chan []byte {
			ch := make(chan []byte, 1)
			go func() {
				buf := make([]byte, 32)
				n, _ := m.Read(buf)
				ch <- append([]byte(nil), buf[:n]...)
			}()
			return ch
		}
		gotA, gotB := readMaster(ma), readMaster(mb)
		if err := writePasteDisable(ro); err != nil {
			t.Fatalf("stdin A stderr A stdout B: %v", err)
		}
		select {
		case buf := <-gotA:
			if !bytes.Contains(buf, []byte("\x1b[?2004l")) {
				t.Fatalf("disable not written to A: %q", buf)
			}
		case <-time.After(time.Second):
			t.Fatal("disable not written to A")
		}
		select {
		case buf := <-gotB:
			if bytes.Contains(buf, []byte("\x1b[?2004l")) {
				t.Fatalf("disable written to B: %q", buf)
			}
		case <-time.After(200 * time.Millisecond):
		}
	})
	t.Run("NoMatch", func(t *testing.T) {
		_, sa := openPair(t)
		_, sb := openPair(t)
		ro, err := os.OpenFile(sa.Name(), os.O_RDONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ro.Close() })
		oldCand, oldTTY := pasteDisableCandidates, pasteDisableOpenTTY
		pasteDisableCandidates = func() []*os.File { return []*os.File{sb} }
		pasteDisableOpenTTY = func() (*os.File, error) { return nil, errors.New("no tty") }
		t.Cleanup(func() { pasteDisableCandidates, pasteDisableOpenTTY = oldCand, oldTTY })
		if err := writePasteDisable(ro); err == nil || !strings.Contains(err.Error(), "no terminal") {
			t.Fatalf("mismatched devices: %v", err)
		}
	})
	t.Run("NoWritable", func(t *testing.T) {
		_, s := openPair(t)
		ro, err := os.OpenFile(s.Name(), os.O_RDONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ro.Close() })
		oldCand, oldTTY := pasteDisableCandidates, pasteDisableOpenTTY
		pasteDisableCandidates = func() []*os.File { return []*os.File{ro} }
		pasteDisableOpenTTY = func() (*os.File, error) { return nil, errors.New("no tty") }
		t.Cleanup(func() { pasteDisableCandidates, pasteDisableOpenTTY = oldCand, oldTTY })
		if err := writePasteDisable(ro); err == nil || !strings.Contains(err.Error(), "no terminal") {
			t.Fatalf("no writable tty: %v", err)
		}
	})
}

func TestLeaseReviewHiddenCRLF(t *testing.T) {
	const url = "https://hub.example/join/secret-token-1234"
	for _, row := range []struct {
		name   string
		chunks []string
		gap    time.Duration
	}{
		{"Whole", []string{url + "\r\n"}, 0},
		{"Split", []string{url, "\r\n"}, 40 * time.Millisecond},
	} {
		t.Run(row.name, func(t *testing.T) {
			got := hiddenPTY(t, row.chunks, row.gap)
			if got.err != nil || string(got.got) != url {
				t.Fatalf("CRLF returned %q, %v", got.got, got.err)
			}
			if !pasteDisableOnly(got.echo) {
				t.Fatalf("CRLF echoed %q", got.echo)
			}
			if got.rest != "next\n" && got.rest != "next\r\n" {
				t.Fatalf("the line feed after Enter was left in the terminal queue: %q", got.rest)
			}
		})
	}
}
