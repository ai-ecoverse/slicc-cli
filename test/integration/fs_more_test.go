package integration

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/fs"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

func mountNames(t *testing.T, p *page, expr string) [][]string {
	t.Helper()
	var updates [][]struct {
		Name     string `json:"name"`
		Readonly bool   `json:"readonly"`
	}
	p.eval(expr, &updates)
	out := make([][]string, 0, len(updates))
	for _, u := range updates {
		names := []string{}
		for _, m := range u {
			names = append(names, m.Name)
		}
		out = append(out, names)
	}
	return out
}

func waitMountUpdate(t *testing.T, p *page, want ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		updates := mountNames(t, p, "linkTest.mountUpdates()")
		if len(updates) > 0 && slices.Equal(updates[len(updates)-1], want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("mount updates %v, want last %v", updates, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestFSMountUpdates(t *testing.T) {
	f := newFSFixture(t)
	var srv *fs.Server
	liveBefore := true
	env := setupInProcess(t, policy.Local(), func(n *node.Node) {
		srv = fs.Register(n, fs.Load([]string{f.folder}, nil))
		liveBefore = srv.Live()
	})
	t.Cleanup(func() { _ = srv.Close() })
	var none *fs.Server
	if liveBefore || none.Live() {
		t.Fatalf("Live before a link %v, on no server %v", liveBefore, none.Live())
	}
	c := &fsClient{p: env.p, mount: "project", secret: fsSecret}
	if got := mountNames(t, env.p, "linkTest.ready().then((h) => [h.mounts])"); !slices.Equal(got[0], []string{"project"}) {
		t.Fatalf("HELLO mounts %v", got)
	}
	for deadline := time.Now().Add(10 * time.Second); !srv.Live(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("Live stayed false with a page that takes mounts updates")
		}
	}
	fh, _ := c.open(t, map[string]any{"path": "hello.txt", "write": true})
	w := c.watch(t, "project")
	docs := fs.Load([]string{f.docs + ":docs:ro"}, nil)
	if err := srv.Add(docs[0]); err != nil {
		t.Fatal(err)
	}
	waitMountUpdate(t, env.p, "project", "docs")
	if err := srv.Add(docs[0]); !errors.Is(err, fs.ErrTaken) {
		t.Fatalf("adding a taken name: %v", err)
	}
	d := &fsClient{p: env.p, mount: "docs", secret: fsSecret}
	d.ok(t, "stat", map[string]any{"path": "readme.md"})
	d.errno(t, "mkdir", map[string]any{"path": "x"}, "EROFS")
	if !srv.Remove("project") || srv.Remove("project") {
		t.Fatal("Remove should find project once")
	}
	waitMountUpdate(t, env.p, "docs")
	c.errno(t, "stat", map[string]any{"path": "hello.txt"}, "ENOENT")
	if r := c.read(t, fh, 0, 1, nil); r.reset() != "ENOENT" {
		t.Fatalf("read on a removed mount: %v", r)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ended := w.state(t); ended == "reset ENOENT" {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("the watch on a removed mount: %q", ended)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := srv.Add(fs.Load([]string{f.folder}, nil)[0]); err != nil {
		t.Fatal(err)
	}
	waitMountUpdate(t, env.p, "docs", "project")
	if r := c.read(t, fh, 0, 1, nil); r.reset() != "EBADF" {
		t.Fatalf("a handle from before the removal: %v", r)
	}
	if r := c.write(t, fh, 0, text("x")); r.reset() != "EBADF" {
		t.Fatalf("a write handle from before the removal: %v", r)
	}
	c.ok(t, "stat", map[string]any{"path": "hello.txt"})
	if got := srv.Mounts(); len(got) != 2 || got[0].Name != "docs" || got[1].Name != "project" {
		t.Fatalf("Mounts() %+v", got)
	}
}

func TestFSWatchesShareOneWatcher(t *testing.T) {
	f := newFSFixture(t)
	env := setupInProcess(t, policy.Local(), func(n *node.Node) {
		srv := fs.Register(n, fs.Load([]string{f.folder, f.folder + ":again"}, nil))
		t.Cleanup(func() { _ = srv.Close() })
	})
	c := &fsClient{p: env.p, mount: "project", secret: fsSecret}
	first := c.watch(t, "project", "again")
	second := c.watch(t, "project")
	first.c.js(t, "watchStop", first.id)
	third := c.watch(t, "again")
	at2, at3 := second.mark(t), third.mark(t)
	writeFile(t, filepath.Join(f.folder, "shared.txt"), "x")
	second.until(t, at2, "project shared.txt", changed("project", "shared.txt"))
	third.until(t, at3, "again shared.txt", changed("again", "shared.txt"))
}

func TestFollowMount(t *testing.T) {
	f := newFSFixture(t)
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]), "--mount", f.folder+":work:ro", "--mount", f.missing)
	fp.waitStderr(t, "slicc: fs: sharing work (read-only)", 10*time.Second)
	fp.waitStderr(t, "--mount "+f.missing+": not an existing folder, skipping", 10*time.Second)
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.waitUp(t, "")
	var hello struct {
		Caps   []string `json:"caps"`
		Mode   string   `json:"mode"`
		Mounts []struct {
			Name     string `json:"name"`
			Readonly bool   `json:"readonly"`
		} `json:"mounts"`
	}
	p.eval("linkTest.ready()", &hello)
	if hello.Mode != "remote" || !slices.Contains(hello.Caps, "fs") || slices.Contains(hello.Caps, "net") || len(hello.Mounts) != 1 || hello.Mounts[0].Name != "work" || !hello.Mounts[0].Readonly {
		t.Fatalf("HELLO %+v", hello)
	}
	c := &fsClient{p: p, mount: "work", secret: fsSecret}
	entries := c.list(t, "")
	if entries["hello.txt"].str("kind") != "file" {
		t.Fatalf("list %v", entries)
	}
	fh, attr := c.open(t, map[string]any{"path": "hello.txt"})
	if r := c.read(t, fh, 0, 100, attr.str("etag")); r.body(t) != "hello" {
		t.Fatalf("read %v", r)
	}
	c.errno(t, "mkdir", map[string]any{"path": "x"}, "EROFS")
	c.errno(t, "stat", map[string]any{"path": "../secret"}, "EACCES")
	w := c.watch(t, "work")
	at := w.mark(t)
	time.Sleep(200 * time.Millisecond)
	writeFile(t, filepath.Join(f.folder, "remote.txt"), "x")
	w.until(t, at, "work remote.txt", changed("work", "remote.txt"))
}

func TestFollowWithoutMountRefusesFS(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]))
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.waitUp(t, "")
	var hello map[string]any
	p.eval("linkTest.ready()", &hello)
	if _, ok := hello["mounts"]; ok || slices.Contains(fsStrings(hello["caps"]), "fs") {
		t.Fatalf("HELLO %v", hello)
	}
	c := &fsClient{p: p, mount: "x"}
	if r := c.callOn(t, "x", "stat", nil); r.reset() != "EOPNOTSUPP" {
		t.Fatalf("fs without --mount: %v", r)
	}
}

func fsStrings(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, s := range l {
		if str, ok := s.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func TestDefaultFlowMounts(t *testing.T) {
	f := newFSFixture(t)
	b := launchBrowser(t)
	env, _ := launchEnv(t)
	srv := pageServer(t)
	pageURL := srv.URL + "/"
	id := "cdp:127.0.0.1:" + strings.TrimPrefix(b.endpoint, "http://127.0.0.1:")
	flow := startFlow(t, append(env, "NO_COLOR=1"), "--plain", "--cdp", b.endpoint, "--browser", id, "--page", pageURL, "--no-kernel", "--mount", f.folder, "--mount", f.missing)
	flow.waitStderr(t, "--mount "+f.missing+": not an existing folder, skipping")
	flow.waitStderr(t, `linked with "link test page" in `+pageURL)
	if !strings.Contains(flow.stderr.String(), "offering net, http, credentialed, ws, udp, fs") {
		t.Fatalf("plain output does not offer fs:\n%s", flow.stderr.String())
	}
	p := dialPage(t, b.endpoint, pageURL)
	c := &fsClient{p: p, mount: "project", secret: fsSecret}
	fh, _ := c.open(t, map[string]any{"path": "from-default.txt", "create": true, "write": true})
	if r := c.write(t, fh, 0, text("default")); r.reset() != "" {
		t.Fatalf("write %v", r)
	}
	c.ok(t, "release", map[string]any{"fh": fh})
	if got := readFile(t, filepath.Join(f.folder, "from-default.txt")); got != "default" {
		t.Fatalf("content %q", got)
	}
	if err := interrupt(flow.cmd); err != nil {
		t.Fatal(err)
	}
	flow.wait(t)
}

func TestMountFlagsInHelp(t *testing.T) {
	for _, args := range [][]string{{"attach", "--help"}, {"https://www.sliccy.ai/join/trayx.5e5e5e5e5e5e5e5e", "follow", "--help"}, {"--help"}} {
		out, errOut, _ := slicc(t, args...)
		if !strings.Contains(out+errOut, "--mount <path>[:<name>][:ro]") {
			t.Fatalf("%v --help lacks --mount:\n%s%s", args, out, errOut)
		}
	}
}
