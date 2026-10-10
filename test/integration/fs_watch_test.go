package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

type fsWatch struct {
	c  *fsClient
	id float64
}

func (c *fsClient) watch(t *testing.T, mounts ...string) *fsWatch {
	t.Helper()
	r := c.js(t, "watch", mounts)
	if r.reset() != "" {
		t.Fatalf("watch %v: %v", mounts, r)
	}
	return &fsWatch{c: c, id: r.num("id")}
}

func (w *fsWatch) state(t *testing.T) ([]fsRes, string) {
	t.Helper()
	r := w.c.js(t, "watchState", w.id)
	var lines []fsRes
	for _, l := range r["lines"].([]any) {
		lines = append(lines, fsRes(l.(map[string]any)))
	}
	ended, _ := r["ended"].(string)
	return lines, ended
}

func paths(l fsRes) []string {
	var out []string
	ps, _ := l["paths"].([]any)
	for _, p := range ps {
		out = append(out, p.(string))
	}
	return out
}

func (w *fsWatch) mark(t *testing.T) int {
	t.Helper()
	lines, _ := w.state(t)
	return len(lines)
}

func (w *fsWatch) until(t *testing.T, from int, what string, match func(fsRes) bool) fsRes {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		lines, _ := w.state(t)
		for _, l := range lines[min(from, len(lines)):] {
			if match(l) {
				return l
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no watch line for %s in %v", what, lines)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func changed(mount, path string) func(fsRes) bool {
	return func(l fsRes) bool {
		return l.str("mount") == mount && (l["all"] == true || slices.Contains(paths(l), path))
	}
}

func (e *fsEnv) testFSWatch(t *testing.T) {
	c := e.c
	for _, mounts := range [][]string{{}, {"nowhere"}, {"project", "nowhere"}} {
		r := c.js(t, "watch", mounts)
		want := "ENOENT"
		if len(mounts) == 0 {
			want = "EINVAL"
		}
		if r.reset() != want {
			t.Fatalf("watch %v: %v", mounts, r)
		}
	}
	if r := c.js(t, "raw", map[string]any{"kind": "fs", "op": "watch"}, false); r.reset() != "EINVAL" {
		t.Fatalf("watch without mounts: %v", r)
	}
	w := c.watch(t, "project", "docs")
	if err := os.MkdirAll(filepath.Join(e.folder, "watched"), 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	at := w.mark(t)
	writeFile(t, filepath.Join(e.folder, "watched", "a.txt"), "a")
	l := w.until(t, at, "watched/a.txt", changed("project", "watched/a.txt"))
	if l["all"] != true && !slices.Contains(paths(l), "watched") {
		t.Fatalf("no parent in %v", l)
	}
	at = w.mark(t)
	writeFile(t, filepath.Join(e.docs, "new.md"), "new")
	w.until(t, at, "docs/new.md", changed("docs", "new.md"))
	at = w.mark(t)
	c.ok(t, "mkdir", map[string]any{"path": "inside-made"})
	w.until(t, at, "inside-made", changed("project", "inside-made"))
	time.Sleep(200 * time.Millisecond)
	at = w.mark(t)
	fh, _ := c.open(t, map[string]any{"path": "inside-made/f.txt", "create": true, "write": true})
	c.write(t, fh, 0, text("x"))
	c.ok(t, "release", map[string]any{"fh": fh})
	w.until(t, at, "inside-made/f.txt", changed("project", "inside-made/f.txt"))
	if err := os.MkdirAll(filepath.Join(e.folder, "deep", "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	at = w.mark(t)
	writeFile(t, filepath.Join(e.folder, "deep", "a", "b", "c.txt"), "c")
	w.until(t, at, "deep/a/b/c.txt", changed("project", "deep/a/b/c.txt"))
	e.testFSWatchBurst(t, w)
	e.testFSWatchLost(t, w)
	w.c.js(t, "watchStop", w.id)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ended := w.state(t); ended == "end" {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("the watch did not end: %q", ended)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (e *fsEnv) testFSWatchBurst(t *testing.T, w *fsWatch) {
	lines, _ := w.state(t)
	seen := len(lines)
	dir := filepath.Join(e.folder, "burst")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 250 {
				_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f-%d-%d", g, i)), nil, 0o644)
			}
		}()
	}
	wg.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for {
		lines, _ := w.state(t)
		named := map[string]bool{}
		for _, l := range lines[seen:] {
			if len(paths(l)) > 256 {
				t.Fatalf("a line with %d paths", len(paths(l)))
			}
			if l.str("mount") == "project" && l["all"] == true {
				return
			}
			for _, p := range paths(l) {
				named[p] = true
			}
		}
		missing := 0
		for g := range 8 {
			for i := range 250 {
				if !named[fmt.Sprintf("burst/f-%d-%d", g, i)] {
					missing++
				}
			}
		}
		if missing == 0 {
			t.Logf("the burst came in %d lines of at most 256 paths", len(lines)-seen)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("2000 changes: no all and %d never named, in %d lines", missing, len(lines)-seen)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (e *fsEnv) testFSWatchLost(t *testing.T, w *fsWatch) {
	lines, _ := w.state(t)
	seen := len(lines)
	away := e.docs + "-away"
	if err := os.Rename(e.docs, away); err != nil {
		if runtime.GOOS == "windows" {
			t.Logf("Windows keeps the watched folder: %v", err)
			return
		}
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if err := os.Rename(away, e.docs); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		lines, _ := w.state(t)
		alls := 0
		for _, l := range lines[seen:] {
			if l.str("mount") == "docs" && l["all"] == true {
				alls++
			}
		}
		if alls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lost and back: %v", lines[seen:])
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	at := w.mark(t)
	writeFile(t, filepath.Join(e.docs, "after.md"), "after")
	w.until(t, at, "docs/after.md", changed("docs", "after.md"))
}
