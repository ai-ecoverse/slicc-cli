package integration

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fenceEnv struct {
	*linkEnv
	home, slicc, fnox, jerk string
	ro, rw                  *fsClient
}

func fenceRel() (slicc, jerk string) {
	switch runtime.GOOS {
	case "darwin":
		return "Library/Application Support/slicc", ".local/state/slicc-jerk"
	case "windows":
		return "Roaming/slicc", "Local/slicc-jerk"
	}
	return ".config/slicc", ".local/state/slicc-jerk"
}

func setupFence(t *testing.T) *fenceEnv {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, fsSecret)
	slicc, jerk := fenceRel()
	e := &fenceEnv{home: home, slicc: slicc, fnox: ".config/fnox", jerk: jerk}
	writeFile(t, filepath.Join(home, filepath.FromSlash(slicc), "vault-test.key"), "pairing key")
	writeFile(t, filepath.Join(home, ".config", "fnox", "config.toml"), "[secrets]")
	writeFile(t, filepath.Join(home, ".config", "fnox", "sub", "x.txt"), "x")
	writeFile(t, filepath.Join(home, filepath.FromSlash(jerk), "leases.toml"), "lease map")
	writeFile(t, filepath.Join(home, ".config", "other", "ok.txt"), "ok")
	writeFile(t, filepath.Join(home, "project", "readme.md"), "hi")
	env := []string{
		"HOME=" + home, "USERPROFILE=" + home,
		"APPDATA=" + filepath.Join(home, "Roaming"), "LOCALAPPDATA=" + filepath.Join(home, "Local"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"FNOX_AGE_KEY_FILE=" + filepath.Join(home, "keys", "age.txt"),
		"FNOX_CONFIG_DIR=", "FNOX_STATE_DIR=", "SLICC_JERK_CONFIG=",
	}
	e.linkEnv = setupLinkEnv(t, false, env,
		"--mount", home+":home:ro",
		"--mount", home+":rw",
		"--mount", filepath.Join(home, ".config", "fnox")+":fnox",
		"--mount", filepath.Join(home, ".config", "fnox", "sub")+":fnoxsub",
		"--mount", filepath.Join(home, filepath.FromSlash(slicc))+":slicc",
		"--mount", filepath.Join(home, ".config", "other")+":other",
	)
	e.ro = &fsClient{p: e.p, mount: "home", secret: fsSecret}
	e.rw = &fsClient{p: e.p, mount: "rw", secret: fsSecret}
	return e
}

func TestFSFence(t *testing.T) {
	e := setupFence(t)
	t.Run("fenced folders aren't mounted", e.testFenceMounts)
	t.Run("fenced paths are refused", e.testFencePaths)
	t.Run("ancestors can't be moved", e.testFenceAncestors)
	t.Run("short names are fenced too", e.testFenceShortNames)
	t.Run("watch hides fenced paths", e.testFenceWatch)
}

func (e *fenceEnv) testFenceMounts(t *testing.T) {
	var h struct {
		Mounts []struct {
			Name string `json:"name"`
		} `json:"mounts"`
	}
	e.p.eval("linkTest.ready()", &h)
	var names []string
	for _, m := range h.Mounts {
		names = append(names, m.Name)
	}
	if strings.Join(names, ",") != "home,rw,other" {
		t.Fatalf("mounts %v", names)
	}
	errs := e.a.stderr.String()
	for _, name := range []string{"fnox", "fnoxsub", "slicc"} {
		if !strings.Contains(errs, ":"+name+": slicc never shares this folder") {
			t.Fatalf("no warning for %s:\n%s", name, errs)
		}
	}
}

func (e *fenceEnv) testFencePaths(t *testing.T) {
	refused := []string{
		e.fnox, e.fnox + "/config.toml", e.fnox + "/sub/x.txt",
		e.slicc, e.slicc + "/vault-test.key", e.slicc + "/missing",
		e.jerk, e.jerk + "/leases.toml",
		"keys/age.txt",
	}
	if runtime.GOOS != "linux" {
		refused = append(refused, strings.ToUpper(e.fnox)+"/config.toml")
	}
	for _, c := range []*fsClient{e.ro, e.rw} {
		for _, p := range refused {
			c.errno(t, "stat", map[string]any{"path": p}, "EACCES")
		}
		c.errno(t, "open", map[string]any{"path": e.fnox + "/config.toml"}, "EACCES")
		c.errno(t, "list", map[string]any{"path": e.fnox}, "EACCES")
		if l := c.list(t, ".config"); l["fnox"] == nil || l["other"] == nil {
			t.Fatalf("list .config: %v", l)
		}
		c.ok(t, "stat", map[string]any{"path": ".config/other/ok.txt"})
	}
	if r := e.ro.js(t, "list", "home", e.fnox); r.reset() != "EACCES" || !strings.Contains(r.str("detail"), "slicc never shares this path") {
		t.Fatalf("list %s: %v", e.fnox, r)
	}
	e.rw.errno(t, "open", map[string]any{"path": e.fnox + "/new.toml", "create": true}, "EACCES")
	e.rw.errno(t, "mkdir", map[string]any{"path": e.fnox + "/d"}, "EACCES")
	e.rw.errno(t, "setattr", map[string]any{"path": e.fnox + "/config.toml", "size": 0}, "EACCES")
	e.rw.errno(t, "unlink", map[string]any{"path": e.fnox + "/config.toml"}, "EACCES")
	if runtime.GOOS != "windows" {
		mustSymlink(t, filepath.Join(e.home, ".config", "fnox"), filepath.Join(e.home, "fnox-link"))
		e.rw.errno(t, "stat", map[string]any{"path": "fnox-link/config.toml"}, "EACCES")
		e.rw.errno(t, "open", map[string]any{"path": "fnox-link/config.toml"}, "EACCES")
	}
	if got := readFile(t, filepath.Join(e.home, ".config", "fnox", "config.toml")); got != "[secrets]" {
		t.Fatalf("fnox config changed: %q", got)
	}
}

func (e *fenceEnv) testFenceAncestors(t *testing.T) {
	c := e.rw
	c.errno(t, "rename", map[string]any{"from": ".config", "to": "cfg"}, "EACCES")
	c.errno(t, "rename", map[string]any{"from": e.fnox, "to": "fnox-moved"}, "EACCES")
	c.errno(t, "rmdir", map[string]any{"path": ".config"}, "EACCES")
	c.ok(t, "mkdir", map[string]any{"path": "staging"})
	c.errno(t, "rename", map[string]any{"from": "staging", "to": "keys"}, "EACCES")
	if runtime.GOOS != "windows" {
		c.errno(t, "symlink", map[string]any{"target": "staging", "path": "keys"}, "EACCES")
	}
	c.ok(t, "rename", map[string]any{"from": "staging", "to": "staged"})
	c.ok(t, "rename", map[string]any{"from": ".config/other/ok.txt", "to": ".config/other/ok2.txt"})
	if _, err := os.Stat(filepath.Join(e.home, ".config", "fnox", "config.toml")); err != nil {
		t.Fatal(err)
	}
}

func (e *fenceEnv) testFenceWatch(t *testing.T) {
	w := e.rw.watch(t, "rw")
	time.Sleep(300 * time.Millisecond)
	at := w.mark(t)
	writeFile(t, filepath.Join(e.home, ".config", "fnox", "config.toml"), "[secrets]\nA = 1")
	writeFile(t, filepath.Join(e.home, filepath.FromSlash(e.jerk), "leases.toml"), "changed")
	time.Sleep(200 * time.Millisecond)
	writeFile(t, filepath.Join(e.home, "project", "readme.md"), "changed")
	w.until(t, at, "project/readme.md", changed("rw", "project/readme.md"))
	time.Sleep(200 * time.Millisecond)
	lines, _ := w.state(t)
	for _, l := range lines[at:] {
		for _, p := range paths(l) {
			if strings.HasPrefix(p, e.fnox) || strings.HasPrefix(p, e.jerk) || p == ".config" || p == path.Dir(e.jerk) {
				t.Fatalf("watch showed %q: %v", p, lines[at:])
			}
		}
	}
}

func (e *fenceEnv) testFenceShortNames(t *testing.T) {
	short := shortName(filepath.Join(e.home, ".config", "fnox"))
	parent := shortName(filepath.Join(e.home, ".config"))
	if short == "" || short == "fnox" || parent == "" || parent == ".config" {
		t.Skip("no 8.3 names on this volume")
	}
	c := e.rw
	c.errno(t, "stat", map[string]any{"path": ".config/" + short}, "EACCES")
	c.errno(t, "stat", map[string]any{"path": parent + "/" + short + "/config.toml"}, "EACCES")
	c.errno(t, "rename", map[string]any{"from": ".config/" + short, "to": "moved"}, "EACCES")
	c.errno(t, "rename", map[string]any{"from": parent, "to": "moved"}, "EACCES")
}
