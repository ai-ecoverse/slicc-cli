package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func swapDuringWatch(t *testing.T) *fsEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the page can't create symlinks on this host (HELLO symlinks: false)")
	}
	f := newFSFixture(t)
	env := setupLink(t, false, "--mount", f.folder)
	e := &fsEnv{linkEnv: env, fsFixture: f, c: &fsClient{p: env.p, mount: "project", secret: fsSecret}}
	if err := os.MkdirAll(filepath.Join(f.folder, "swap", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.folder, "swap", "key.txt"), "inside")
	mustSymlink(t, "/", filepath.Join(f.folder, "root-link"))
	mustSymlink(t, filepath.Dir(f.base), filepath.Join(f.folder, "swap", "up-link"))
	w := e.c.watch(t, "project")
	for range 3 {
		r := e.c.js(t, "race", "project", e.outside, 150)
		if r.num("leaks") != 0 || r.num("swaps") != 150 {
			t.Fatalf("race: %v", r)
		}
	}
	time.Sleep(300 * time.Millisecond)
	at := w.mark(t)
	writeFile(t, filepath.Join(f.folder, "after-race.txt"), "x")
	w.until(t, at, "after-race.txt", changed("project", "after-race.txt"))
	if _, ended := w.state(t); ended != "" {
		t.Fatalf("the watch ended: %q", ended)
	}
	return e
}

func waitExit(t *testing.T, e *fsEnv, within time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_, _ = e.a.cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("slicc attach (pid %d) did not exit within %s\n%s", e.a.cmd.Process.Pid, within, processReport(e.a.cmd.Process.Pid))
	}
}

func TestFSSwapDuringWatchThenSIGTERM(t *testing.T) {
	e := swapDuringWatch(t)
	if err := e.a.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitExit(t, e, 5*time.Second)
}

func TestFSSwapDuringWatchThenSIGKILL(t *testing.T) {
	e := swapDuringWatch(t)
	if err := e.a.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitExit(t, e, 5*time.Second)
}
