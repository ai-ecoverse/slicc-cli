package integration

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	chromeChildEnv = "SLICC_IT_CHROME_CHILD"
	chromePIDLine  = "slicc-it-chrome-pid "
)

type lifeline struct {
	once    sync.Once
	release func()
}

var lifelines = struct {
	sync.Mutex
	held map[*lifeline]bool
}{held: map[*lifeline]bool{}}

func holdLifeline(release func()) *lifeline {
	l := &lifeline{release: release}
	lifelines.Lock()
	lifelines.held[l] = true
	lifelines.Unlock()
	return l
}

func (l *lifeline) cut() {
	if l == nil {
		return
	}
	l.once.Do(l.release)
	lifelines.Lock()
	delete(lifelines.held, l)
	lifelines.Unlock()
}

func cutAllLifelines() {
	lifelines.Lock()
	all := make([]*lifeline, 0, len(lifelines.held))
	for l := range lifelines.held {
		all = append(all, l)
	}
	lifelines.Unlock()
	for _, l := range all {
		l.cut()
	}
}

func cutLifelinesOnSignal() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cutAllLifelines()
		os.Exit(1)
	}()
}

func TestHarnessChromeChild(t *testing.T) {
	if os.Getenv(chromeChildEnv) != "1" {
		t.Skip("runs only inside TestHarnessChromeDiesWithItsTestBinary")
	}
	b := launchBrowser(t)
	_, _ = os.Stdout.WriteString(chromePIDLine + strconv.Itoa(b.cmd.Process.Pid) + "\n")
	time.Sleep(5 * time.Minute)
}

func TestHarnessChromeDiesWithItsTestBinary(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("no Chrome found; set SLICC_TEST_CHROME")
		}
		t.Skip(harnessChromeNotice)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestHarnessChromeChild$", "-test.count=1", "-test.v", "-test.timeout=10m")
	child.Env = append(offCIEnv(root), "SLICC_TEST_CHROME="+chrome, chromeChildEnv+"=1")
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	pid := 0
	var seen []string
	for sc := bufio.NewScanner(out); pid == 0 && sc.Scan(); {
		line := sc.Text()
		seen = append(seen, line)
		if rest, ok := strings.CutPrefix(line, chromePIDLine); ok {
			pid, _ = strconv.Atoi(rest)
		}
	}
	if pid == 0 {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatalf("the child test binary never started Chrome:\n%s", tail(strings.Join(seen, "\n"), 40))
	}
	t.Cleanup(func() { killOrphan(pid) })
	go func() { _, _ = io.Copy(io.Discard, out) }()
	if !processAlive(pid) {
		t.Fatalf("Chrome (pid %d) wasn't running before its test binary was killed", pid)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("Chrome (pid %d) outlived its killed test binary by 10s\n%s", pid, chromeTree(pid))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
