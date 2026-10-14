package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

type relinkCheck struct {
	p      *page
	kernel *kernelEnv
	fs     *fsClient
}

func (c *relinkCheck) hello(t *testing.T) string {
	t.Helper()
	var h struct {
		Node     string   `json:"node"`
		Caps     []string `json:"caps"`
		Browsers []struct {
			ID    string `json:"id"`
			Seven bool   `json:"seven"`
		} `json:"browsers"`
		Mounts []struct {
			Name string `json:"name"`
		} `json:"mounts"`
	}
	c.p.eval("linkTest.hello()", &h)
	caps := strings.Join(h.Caps, ",")
	if h.Node == "" || !strings.Contains(caps, "cdp") || !strings.Contains(caps, "fs") {
		t.Fatalf("HELLO %+v", h)
	}
	if len(h.Browsers) != 1 || h.Browsers[0].ID != "chrome" || !h.Browsers[0].Seven {
		t.Fatalf("HELLO browsers %+v", h.Browsers)
	}
	if len(h.Mounts) != 1 || h.Mounts[0].Name != "project" {
		t.Fatalf("HELLO mounts %+v", h.Mounts)
	}
	return h.Node
}

func (c *relinkCheck) all(t *testing.T, round string) string {
	t.Helper()
	c.p.waitFor("linkTest.status().up", 30*time.Second)
	node := c.hello(t)
	echoOverLink(t, c.p)
	fh, _ := c.fs.open(t, map[string]any{"path": "hello.txt"})
	if r := c.fs.read(t, fh, 0, 64, nil); r.body(t) != "hello" {
		t.Fatalf("%s: read %v", round, r)
	}
	c.fs.ok(t, "release", map[string]any{"fh": fh})
	w, _ := c.fs.open(t, map[string]any{"path": round + ".txt", "create": true, "write": true})
	if r := c.fs.write(t, w, 0, text(round)); r.reset() != "" {
		t.Fatalf("%s: write %v", round, r)
	}
	c.fs.ok(t, "release", map[string]any{"fh": w})
	r := decodeReply(t, c.kernel.get(t, c.kernel.host(5173), "/"+round))
	if r.Port != 5173 || r.Path != "/"+round {
		t.Fatalf("%s: kernel reply %+v", round, r)
	}
	var v struct {
		Opened struct {
			Product string `json:"product"`
		} `json:"opened"`
		Reset string `json:"reset"`
		Error string `json:"error"`
	}
	c.p.eval("linkTest.cdp.version('chrome')", &v)
	if !strings.Contains(v.Opened.Product, "Chrome/") {
		t.Fatalf("%s: cdp %+v", round, v)
	}
	return node
}

func profileReleased(dir string) bool {
	if runtime.GOOS == "windows" {
		return os.RemoveAll(dir) == nil
	}
	_, err := os.Lstat(filepath.Join(dir, "SingletonLock"))
	return os.IsNotExist(err) && os.RemoveAll(dir) == nil
}

func closeLeftBrowser(t *testing.T, endpoint string, pid int, dir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c, err := cdp.Dial(ctx, endpoint); err == nil {
		_ = c.Call(ctx, "", "Browser.close", nil, nil)
		_ = c.Close()
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, err := getVersion(endpoint); err == nil {
			continue
		}
		if profileReleased(dir) {
			return
		}
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
	t.Errorf("the browser at %s did not close", endpoint)
}

func TestDefaultRelinksAfterACrash(t *testing.T) {
	env, _ := launchEnv(t)
	profile := "it-restart-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	env, chromeID := testChrome(t, env)
	fx := newFSFixture(t)
	srv := pageServer(t)
	pageURL := srv.URL + "/?kernel"
	port := freePort(t)
	common := []string{"--json", "--page", pageURL, "--name", "it-restart", "--mount", fx.folder, "--kernel-port", strconv.Itoa(port)}

	first := startFlow(t, env, append([]string{"--browser", chromeID, "--profile", profile, "--headless"}, common...)...)
	first.next(t, "discovered")
	l := first.next(t, "launched")
	t.Cleanup(func() { closeLeftBrowser(t, l.CDP, l.PID, l.ProfileDir) })
	if a := first.next(t, "attached"); !a.Launched || a.CDP != l.CDP {
		t.Fatalf("attached %s", a.raw)
	}
	first.next(t, "linked")

	c := &relinkCheck{
		p:      dialPage(t, l.CDP, pageURL),
		kernel: &kernelEnv{port: port, addr: fmt.Sprintf("127.0.0.1:%d", port)},
		fs:     &fsClient{mount: "project", secret: fsSecret},
	}
	c.fs.p = c.p
	before := c.all(t, "before")

	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the killed slicc did not exit")
	}
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		_, err := getVersion(l.CDP)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the browser died with slicc: %v", err)
		}
	}

	id := "cdp:127.0.0.1:" + strings.TrimPrefix(l.CDP, "http://127.0.0.1:")
	second := startFlow(t, env, append([]string{"--cdp", l.CDP, "--browser", id}, common...)...)
	d := second.next(t, "discovered")
	open := false
	for _, e := range d.Entries {
		if e.ID == id && e.Endpoint != nil && e.Endpoint.SevenOpen {
			open = true
		}
	}
	if !open {
		t.Fatalf("discovered %s has no %s with seven open", d.raw, id)
	}
	if a := second.next(t, "attached"); a.Browser != id || a.Role != "seven" || a.Launched {
		t.Fatalf("attached %s", a.raw)
	}
	if up := second.next(t, "linked"); up.Tab != pageURL {
		t.Fatalf("linked %s", up.raw)
	}
	if n := countTabs(t, c.p.b.client, pageURL); n != 1 {
		t.Fatalf("%d tabs at %s; the second run should link the open one", n, pageURL)
	}

	if after := c.all(t, "after"); after == before {
		t.Fatalf("the second run kept node id %s", before)
	}
	for _, name := range []string{"before.txt", "after.txt"} {
		if got := readFile(t, filepath.Join(fx.folder, name)); got != strings.TrimSuffix(name, ".txt") {
			t.Fatalf("%s holds %q", name, got)
		}
	}

	if err := interrupt(second.cmd); err != nil {
		t.Fatal(err)
	}
	second.next(t, "link-lost")
	if cl := second.next(t, "closed"); len(cl.Browsers) != 0 {
		t.Fatalf("closed %s; the second run didn't launch it", cl.raw)
	}
	if code := second.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := getVersion(l.CDP); err != nil {
		t.Fatalf("the second run closed a browser it only attached to: %v", err)
	}
}
