package integration

import (
	"crypto/sha256"
	"encoding/base64"
	le "encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fsMaxIO = 16 * 1024 * 1024

type fsRes map[string]any

func (r fsRes) reset() string {
	s, _ := r["reset"].(string)
	return s
}

func (r fsRes) str(k string) string {
	s, _ := r[k].(string)
	return s
}

func (r fsRes) num(k string) float64 {
	f, _ := r[k].(float64)
	return f
}

func (r fsRes) obj(k string) fsRes {
	m, _ := r[k].(map[string]any)
	return fsRes(m)
}

func (r fsRes) body(t *testing.T) string {
	t.Helper()
	s, ok := r["body64"].(string)
	if !ok {
		t.Fatalf("no body kept: %v", r)
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type fsClient struct {
	p      *page
	mount  string
	secret string
}

func (c *fsClient) js(t *testing.T, fn string, args ...any) fsRes {
	t.Helper()
	var r fsRes
	c.p.eval("linkTest.fs."+fn+"("+jsArgs(args...)+")", &r)
	if e, ok := r["error"].(string); ok {
		t.Fatalf("fs.%s%v: %s", fn, args, e)
	}
	if c.secret != "" && strings.Contains(r.str("detail"), c.secret) {
		t.Fatalf("an error shows a host path: %v", r)
	}
	return r
}

func (c *fsClient) callOn(t *testing.T, mount, op string, args map[string]any, opts ...map[string]any) fsRes {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	o := map[string]any{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return c.js(t, "call", mount, op, args, o)
}

func (c *fsClient) ok(t *testing.T, op string, args map[string]any, opts ...map[string]any) fsRes {
	t.Helper()
	r := c.callOn(t, c.mount, op, args, opts...)
	if r.reset() != "" {
		t.Fatalf("%s %v: %s %s", op, args, r.reset(), r.str("detail"))
	}
	return r
}

func (c *fsClient) errno(t *testing.T, op string, args map[string]any, want string, opts ...map[string]any) {
	t.Helper()
	r := c.callOn(t, c.mount, op, args, opts...)
	if r.reset() != want {
		t.Fatalf("%s %v: got %v, want %s", op, args, r, want)
	}
	if r.str("detail") == "" {
		t.Fatalf("%s %v: no message in %v", op, args, r)
	}
}

func (c *fsClient) open(t *testing.T, args map[string]any) (float64, fsRes) {
	t.Helper()
	r := c.ok(t, "open", args)
	return r.num("fh"), r.obj("attr")
}

func (c *fsClient) read(t *testing.T, fh float64, offset, size int, ifMatch any) fsRes {
	t.Helper()
	return c.js(t, "read", c.mount, fh, offset, size, ifMatch)
}

func (c *fsClient) write(t *testing.T, fh float64, offset int, data map[string]any, opts ...map[string]any) fsRes {
	t.Helper()
	o := map[string]any{}
	if len(opts) > 0 {
		o = opts[0]
	}
	return c.js(t, "write", c.mount, fh, offset, data, o)
}

func (c *fsClient) list(t *testing.T, path string) map[string]fsRes {
	t.Helper()
	r := c.js(t, "list", c.mount, path)
	if r.reset() != "" {
		t.Fatalf("list %q: %v", path, r)
	}
	out := map[string]fsRes{}
	entries, _ := r["entries"].([]any)
	for _, e := range entries {
		m := fsRes(e.(map[string]any))
		out[m.str("name")] = m.obj("attr")
	}
	return out
}

func text(s string) map[string]any { return map[string]any{"text": s} }

func prngData(size, seed int) map[string]any {
	return map[string]any{"prng": map[string]any{"size": size, "seed": seed}}
}

func prngBytes(size int, seed uint32) []byte {
	out := make([]byte, 0, size)
	x := seed
	for at := 0; at < size; at += 65536 {
		n := min(65536, size-at)
		words := make([]byte, (n+3)/4*4)
		for i := 0; i < len(words); i += 4 {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			le.LittleEndian.PutUint32(words[i:], x)
		}
		out = append(out, words[:n]...)
	}
	return out
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type fsFixture struct {
	base, folder, outside, docs, missing string
	symlinks                             bool
}

const fsSecret = "slicc-fs-host-dir"

func newFSFixture(t *testing.T) *fsFixture {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(tmp, fsSecret)
	f := &fsFixture{base: base, folder: filepath.Join(base, "project"), outside: filepath.Join(base, "secret"), docs: filepath.Join(base, "docs"), missing: filepath.Join(base, "missing")}
	for _, d := range []string{f.folder, f.outside, f.docs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(f.outside, "key.txt"), "top secret")
	writeFile(t, filepath.Join(f.folder, "hello.txt"), "hello")
	writeFile(t, filepath.Join(f.docs, "readme.md"), "docs")
	f.symlinks = os.Symlink(f.outside, filepath.Join(f.folder, "escape")) == nil
	if f.symlinks {
		mustSymlink(t, filepath.Join(f.outside, "key.txt"), filepath.Join(f.folder, "key-link"))
		mustSymlink(t, "hello.txt", filepath.Join(f.folder, "inner-link"))
	} else if runtime.GOOS != "windows" {
		t.Fatal("cannot create symlinks")
	}
	return f
}

func mustSymlink(t *testing.T, target, p string) {
	t.Helper()
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type fsEnv struct {
	*linkEnv
	*fsFixture
	c *fsClient
}

func setupFS(t *testing.T) *fsEnv {
	t.Helper()
	f := newFSFixture(t)
	env := setupLink(t, false,
		"--mount", f.folder,
		"--mount", f.docs+":docs:ro",
		"--mount", f.missing,
		"--mount", filepath.Join(f.folder, "hello.txt"),
		"--mount", f.outside+":project",
		"--mount", f.outside+":..",
	)
	return &fsEnv{linkEnv: env, fsFixture: f, c: &fsClient{p: env.p, mount: "project", secret: fsSecret}}
}

func TestFS(t *testing.T) {
	e := setupFS(t)
	ping := e.c.js(t, "watch", []string{"project"})
	started := time.Now()
	t.Run("hello and mounts", e.testFSHello)
	t.Run("confinement", e.testFSConfinement)
	t.Run("swap between check and use", e.testFSSwap)
	t.Run("read-only", e.testFSReadOnly)
	t.Run("setattr", e.testFSSetattr)
	t.Run("metadata errnos", e.testFSErrnos)
	t.Run("argument types", e.testFSArguments)
	t.Run("case-only rename", e.testFSCaseRename)
	t.Run("writes in place", e.testFSWrites)
	t.Run("reads and ESTALE", e.testFSReads)
	t.Run("large files and holes", e.testFSLarge)
	t.Run("list", e.testFSList)
	t.Run("handle limit", e.testFSHandleLimit)
	t.Run("watch", e.testFSWatch)
	t.Run("watch pings", func(t *testing.T) {
		time.Sleep(time.Until(started.Add(16 * time.Second)))
		st := e.c.js(t, "watchState", ping.num("id"))
		found := false
		for _, l := range st["lines"].([]any) {
			found = found || fsRes(l.(map[string]any)).num("ping") == 1
		}
		if !found {
			t.Fatalf("no ping after 16 s: %v", st)
		}
	})
	t.Run("handles die with the link", e.testFSHandlesDie)
}

func (e *fsEnv) testFSHello(t *testing.T) {
	var h struct {
		Caps   []string `json:"caps"`
		Mounts []struct {
			Name         string `json:"name"`
			Readonly     bool   `json:"readonly"`
			Capabilities struct {
				MaxIO           int    `json:"maxIo"`
				Symlinks        bool   `json:"symlinks"`
				Chmod           bool   `json:"chmod"`
				CaseInsensitive *bool  `json:"caseInsensitive"`
				Normalization   string `json:"normalization"`
				Ranges          bool   `json:"ranges"`
			} `json:"capabilities"`
		} `json:"mounts"`
	}
	e.p.eval("linkTest.ready()", &h)
	if !strings.Contains(","+strings.Join(h.Caps, ",")+",", ",fs,") {
		t.Fatalf("caps %v", h.Caps)
	}
	if len(h.Mounts) != 2 || h.Mounts[0].Name != "project" || h.Mounts[0].Readonly || h.Mounts[1].Name != "docs" || !h.Mounts[1].Readonly {
		t.Fatalf("mounts %+v", h.Mounts)
	}
	native := runtime.GOOS != "windows"
	norm := map[string]string{"darwin": "nfd-insensitive"}[runtime.GOOS]
	if norm == "" {
		norm = "none"
	}
	for _, m := range h.Mounts {
		c := m.Capabilities
		if c.MaxIO != fsMaxIO || c.Symlinks != native || c.Chmod != native || c.CaseInsensitive == nil || c.Normalization != norm || !c.Ranges {
			t.Fatalf("capabilities of %s: %+v", m.Name, c)
		}
		if runtime.GOOS == "linux" && *c.CaseInsensitive {
			t.Fatalf("a Linux temp dir probed case-insensitive")
		}
	}
	raw, _ := json.Marshal(h)
	if strings.Contains(string(raw), fsSecret) {
		t.Fatalf("HELLO shows a host path: %s", raw)
	}
	errs := e.a.stderr.String()
	for _, want := range []string{
		"--mount " + e.missing + ": not an existing folder, skipping",
		"--mount " + filepath.Join(e.folder, "hello.txt") + ": not an existing folder, skipping",
		"--mount " + e.outside + ":project: the name project is taken, skipping",
		"--mount " + e.outside + ":..: invalid name, skipping",
		"slicc: fs: sharing project, docs (read-only)",
	} {
		if !strings.Contains(errs, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, errs)
		}
	}
}

func (e *fsEnv) testFSConfinement(t *testing.T) {
	c := e.c
	for _, p := range []string{"..", "../secret/key.txt", "a/../../secret", "/etc/passwd", "/"} {
		c.errno(t, "stat", map[string]any{"path": p}, "EACCES")
	}
	c.errno(t, "stat", map[string]any{"path": "a\x00b"}, "EINVAL")
	if runtime.GOOS == "windows" {
		c.errno(t, "stat", map[string]any{"path": `..\secret`}, "EINVAL")
		c.errno(t, "stat", map[string]any{"path": "hello.txt:stream"}, "EINVAL")
		c.errno(t, "stat", map[string]any{"path": "C:/Windows"}, "EINVAL")
		c.errno(t, "symlink", map[string]any{"target": "hello.txt", "path": "l"}, "EPERM")
		c.errno(t, "stat", map[string]any{"path": "CON"}, "ENOENT")
		c.errno(t, "stat", map[string]any{"path": "nul"}, "ENOENT")
	}
	c.errno(t, "rename", map[string]any{"from": "hello.txt", "to": "../stolen.txt"}, "EACCES")
	if !e.symlinks {
		t.Log("no symlinks on this host; only the path rules are checked")
		e.testFSJunction(t)
		return
	}
	c.errno(t, "stat", map[string]any{"path": "escape/key.txt"}, "EACCES")
	c.errno(t, "list", map[string]any{"path": "escape"}, "ENOTDIR")
	if r := c.js(t, "list", "project", "escape"); r.reset() != "ENOTDIR" {
		t.Fatalf("list escape: %v", r)
	}
	c.errno(t, "open", map[string]any{"path": "escape/key.txt"}, "EACCES")
	c.errno(t, "open", map[string]any{"path": "key-link"}, "ELOOP")
	c.errno(t, "open", map[string]any{"path": "key-link", "write": true, "truncate": true}, "ELOOP")
	c.errno(t, "open", map[string]any{"path": "inner-link"}, "ELOOP")
	c.errno(t, "mkdir", map[string]any{"path": "escape/new"}, "EACCES")
	c.errno(t, "rename", map[string]any{"from": "escape/key.txt", "to": "mine.txt"}, "EACCES")
	c.errno(t, "unlink", map[string]any{"path": "escape/key.txt"}, "EACCES")
	c.errno(t, "setattr", map[string]any{"path": "key-link", "mode": 0o777}, "EINVAL")
	c.errno(t, "setattr", map[string]any{"path": "key-link", "size": 0}, "EINVAL")
	if runtime.GOOS != "windows" {
		c.ok(t, "symlink", map[string]any{"target": "/", "path": "root-link"})
		c.errno(t, "list", map[string]any{"path": "root-link/etc"}, "EACCES")
		c.errno(t, "stat", map[string]any{"path": "root-link/etc/passwd"}, "EACCES")
		c.errno(t, "symlink", map[string]any{"target": "a\x00b", "path": "nul-link"}, "EINVAL")
	}
	if r := c.ok(t, "readlink", map[string]any{"path": "inner-link"}); r.str("target") != "hello.txt" {
		t.Fatalf("readlink %v", r)
	}
	if r := c.ok(t, "stat", map[string]any{"path": "inner-link"}); r.str("kind") != "symlink" {
		t.Fatalf("stat inner-link %v", r)
	}
	c.ok(t, "unlink", map[string]any{"path": "key-link"})
	if readFile(t, filepath.Join(e.outside, "key.txt")) != "top secret" {
		t.Fatal("the outside file changed")
	}
	e.testFSJunction(t)
}

func (e *fsEnv) testFSJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		return
	}
	junction := filepath.Join(e.folder, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, e.outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
	c := e.c
	c.errno(t, "stat", map[string]any{"path": "junction/key.txt"}, "EACCES")
	c.errno(t, "open", map[string]any{"path": "junction/key.txt"}, "EACCES")
	c.errno(t, "list", map[string]any{"path": "junction"}, "ENOTDIR")
	c.errno(t, "open", map[string]any{"path": "junction"}, "ELOOP")
	if r := c.ok(t, "stat", map[string]any{"path": "junction"}); r.str("kind") != "symlink" {
		t.Fatalf("stat junction %v", r)
	}
	if r := c.ok(t, "readlink", map[string]any{"path": "junction"}); !strings.HasSuffix(r.str("target"), "secret") {
		t.Fatalf("readlink junction %v", r)
	}
	c.errno(t, "rmdir", map[string]any{"path": "junction"}, "ENOTDIR")
	c.ok(t, "unlink", map[string]any{"path": "junction"})
	if readFile(t, filepath.Join(e.outside, "key.txt")) != "top secret" {
		t.Fatal("the outside file changed")
	}
}

func (e *fsEnv) testFSSwap(t *testing.T) {
	if !e.symlinks || runtime.GOOS == "windows" {
		t.Skip("the page can't create symlinks on this host (HELLO symlinks: false)")
	}
	if err := os.MkdirAll(filepath.Join(e.folder, "swap"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(e.folder, "swap", "key.txt"), "inside")
	r := e.c.js(t, "race", "project", e.outside, 200)
	t.Logf("race: %v", r)
	if r.num("leaks") != 0 || r.num("swaps") != 200 {
		t.Fatalf("race: %v", r)
	}
	fh, _ := e.c.open(t, map[string]any{"path": "swap/key.txt"})
	if got := e.c.read(t, fh, 0, 100, nil); got.body(t) != "inside" {
		t.Fatalf("after the race: %v", got)
	}
	e.c.ok(t, "release", map[string]any{"fh": fh})
	if readFile(t, filepath.Join(e.outside, "key.txt")) != "top secret" {
		t.Fatal("the outside file changed")
	}
}

func (e *fsEnv) testFSReadOnly(t *testing.T) {
	c := e.c
	ro := map[string]any{"readonly": true}
	c.ok(t, "stat", map[string]any{"path": "hello.txt"}, ro)
	for _, a := range []map[string]any{
		{"op": "mkdir", "path": "x"},
		{"op": "rmdir", "path": "x"},
		{"op": "unlink", "path": "hello.txt"},
		{"op": "rename", "from": "hello.txt", "to": "bye.txt"},
		{"op": "symlink", "target": "hello.txt", "path": "l"},
		{"op": "setattr", "path": "hello.txt", "mode": 0o600},
		{"op": "setattr", "path": "hello.txt", "size": 0},
		{"op": "open", "path": "hello.txt", "write": true},
		{"op": "open", "path": "new.txt", "create": true},
		{"op": "open", "path": "hello.txt", "truncate": true},
		{"op": "open", "path": "hello.txt", "exclusive": true},
	} {
		op := a["op"].(string)
		delete(a, "op")
		c.errno(t, op, a, "EROFS", ro)
	}
	fh, _ := c.open(t, map[string]any{"path": "hello.txt"})
	if r := c.write(t, fh, 0, text("x")); r.reset() != "EBADF" {
		t.Fatalf("write on a name handle: %v", r)
	}
	wfh, _ := c.open(t, map[string]any{"path": "hello.txt", "write": true})
	if r := c.write(t, wfh, 0, text("x"), ro); r.reset() != "EROFS" {
		t.Fatalf("read-only write: %v", r)
	}
	c.ok(t, "release", map[string]any{"fh": wfh})
	c.ok(t, "release", map[string]any{"fh": fh})
	if readFile(t, filepath.Join(e.folder, "hello.txt")) != "hello" {
		t.Fatal("a read-only call changed hello.txt")
	}
	docs := &fsClient{p: e.p, mount: "docs", secret: fsSecret}
	docs.ok(t, "stat", map[string]any{"path": "readme.md"})
	docs.errno(t, "mkdir", map[string]any{"path": "x"}, "EROFS")
	docs.errno(t, "setattr", map[string]any{"path": "x", "size": 0}, "EROFS")
	docs.errno(t, "open", map[string]any{"path": "readme.md", "write": true}, "EROFS")
	docs.errno(t, "unlink", map[string]any{"path": "readme.md"}, "EROFS")
	dfh, _ := docs.open(t, map[string]any{"path": "readme.md"})
	if r := docs.read(t, dfh, 0, 100, nil); r.body(t) != "docs" {
		t.Fatalf("read docs: %v", r)
	}
	if r := docs.write(t, dfh, 0, text("x")); r.reset() != "EROFS" {
		t.Fatalf("write on a read-only export: %v", r)
	}
	docs.ok(t, "release", map[string]any{"fh": dfh})
	if readFile(t, filepath.Join(e.docs, "readme.md")) != "docs" {
		t.Fatal("the read-only export changed")
	}
}

func (e *fsEnv) testFSSetattr(t *testing.T) {
	c := e.c
	p := filepath.Join(e.folder, "sized.txt")
	writeFile(t, p, "hello, world")
	c.ok(t, "setattr", map[string]any{"path": "sized.txt", "size": 5})
	if got := readFile(t, p); got != "hello" {
		t.Fatalf("truncated to %q", got)
	}
	c.ok(t, "setattr", map[string]any{"path": "sized.txt", "size": 9})
	if got := readFile(t, p); got != "hello\x00\x00\x00\x00" {
		t.Fatalf("extended to %q", got)
	}
	if r := c.ok(t, "stat", map[string]any{"path": "sized.txt"}); r.num("size") != 9 {
		t.Fatalf("stat %v", r)
	}
	c.ok(t, "setattr", map[string]any{"path": "sized.txt", "size": 0, "mtime": 1_000_000_000_000})
	attr := c.ok(t, "stat", map[string]any{"path": "sized.txt"})
	if attr.num("size") != 0 || attr.num("mtime") != 1_000_000_000_000 {
		t.Fatalf("stat %v", attr)
	}
	c.ok(t, "setattr", map[string]any{"path": "sized.txt", "mtime": 1_500_000_000_123.5})
	if r := c.ok(t, "stat", map[string]any{"path": "sized.txt"}); r.num("mtime") < 1_500_000_000_123 || r.num("mtime") > 1_500_000_000_124 {
		t.Fatalf("fractional mtime %v", r)
	}
	fh, _ := c.open(t, map[string]any{"path": "sized.txt", "write": true})
	c.ok(t, "setattr", map[string]any{"path": "sized.txt", "size": 3})
	if r := c.write(t, fh, 3, text("abc")); r.reset() != "" {
		t.Fatalf("write %v", r)
	}
	if r := c.ok(t, "release", map[string]any{"fh": fh}); r.obj("attr").num("size") != 6 {
		t.Fatalf("release %v", r)
	}
	if got := readFile(t, p); got != "\x00\x00\x00abc" {
		t.Fatalf("content %q", got)
	}
	c.ok(t, "mkdir", map[string]any{"path": "sized-dir"})
	c.errno(t, "setattr", map[string]any{"path": "sized-dir", "size": 0}, "EISDIR")
	c.errno(t, "setattr", map[string]any{"path": "", "size": 0}, "EISDIR")
	c.errno(t, "setattr", map[string]any{"path": "gone.txt", "size": 0}, "ENOENT")
	for _, size := range []any{-1, 1.5, "3", float64(1 << 53), nil} {
		c.errno(t, "setattr", map[string]any{"path": "sized.txt", "size": size}, "EINVAL")
	}
	for _, mode := range []any{-1, 0o10000, 1.5, "644", nil} {
		c.errno(t, "setattr", map[string]any{"path": "sized.txt", "mode": mode}, "EINVAL")
	}
	for _, mtime := range []any{"1", nil, true} {
		c.errno(t, "setattr", map[string]any{"path": "sized.txt", "mtime": mtime}, "EINVAL")
	}
	if r := c.ok(t, "stat", map[string]any{"path": "sized.txt"}); r.num("size") != 6 {
		t.Fatalf("a refused setattr changed the file: %v", r)
	}
	if runtime.GOOS != "windows" {
		c.ok(t, "setattr", map[string]any{"path": "sized.txt", "mode": 0o640})
		if r := c.ok(t, "stat", map[string]any{"path": "sized.txt"}); r.num("mode") != 0o640 {
			t.Fatalf("mode %v", r)
		}
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
			t.Fatalf("host mode %v", fi.Mode())
		}
	} else {
		c.ok(t, "setattr", map[string]any{"path": "sized.txt", "mode": 0o444})
		if r := c.ok(t, "stat", map[string]any{"path": "sized.txt"}); r.num("mode") != 0o444 {
			t.Fatalf("read-only attribute %v", r)
		}
		c.ok(t, "setattr", map[string]any{"path": "sized.txt", "mode": 0o666})
	}
}

func (e *fsEnv) testFSErrnos(t *testing.T) {
	c := e.c
	c.ok(t, "mkdir", map[string]any{"path": "dir"})
	c.errno(t, "mkdir", map[string]any{"path": "dir"}, "EEXIST")
	c.errno(t, "mkdir", map[string]any{"path": "no/such/dir"}, "ENOENT")
	c.ok(t, "mkdir", map[string]any{"path": "dir/sub"})
	c.errno(t, "rmdir", map[string]any{"path": "dir"}, "ENOTEMPTY")
	c.errno(t, "unlink", map[string]any{"path": "dir"}, "EISDIR")
	c.errno(t, "rmdir", map[string]any{"path": "hello.txt"}, "ENOTDIR")
	c.errno(t, "rmdir", map[string]any{"path": "gone"}, "ENOENT")
	c.errno(t, "unlink", map[string]any{"path": "gone.txt"}, "ENOENT")
	c.errno(t, "stat", map[string]any{"path": "gone.txt"}, "ENOENT")
	c.errno(t, "stat", map[string]any{"path": "hello.txt/x"}, "ENOTDIR")
	c.errno(t, "readlink", map[string]any{"path": "hello.txt"}, "EINVAL")
	c.errno(t, "unlink", map[string]any{"path": ""}, "EBUSY")
	c.errno(t, "rmdir", map[string]any{"path": ""}, "EBUSY")
	c.errno(t, "rename", map[string]any{"from": "", "to": "x"}, "EBUSY")
	c.errno(t, "rename", map[string]any{"from": "dir", "to": "."}, "EBUSY")
	c.errno(t, "rename", map[string]any{"from": "gone", "to": "x"}, "ENOENT")
	c.errno(t, "read", map[string]any{"fh": 9999, "offset": 0, "size": 1}, "EBADF")
	c.errno(t, "release", map[string]any{"fh": 9999}, "EBADF")
	c.errno(t, "nope", nil, "EINVAL")
	if r := c.callOn(t, "nowhere", "stat", map[string]any{"path": ""}); r.reset() != "ENOENT" || r.str("detail") != "no such folder" {
		t.Fatalf("unknown mount: %v", r)
	}
	if r := c.js(t, "raw", map[string]any{"kind": "fs", "mount": "project"}); r.reset() != "EINVAL" {
		t.Fatalf("no op: %v", r)
	}
	c.ok(t, "mkdir", map[string]any{"path": "full"})
	c.ok(t, "mkdir", map[string]any{"path": "full/x"})
	c.errno(t, "rename", map[string]any{"from": "dir", "to": "full"}, "ENOTEMPTY")
	c.errno(t, "rename", map[string]any{"from": "dir", "to": "hello.txt"}, "ENOTDIR")
	c.errno(t, "rename", map[string]any{"from": "hello.txt", "to": "dir"}, "EISDIR")
	c.ok(t, "mkdir", map[string]any{"path": "empty"})
	c.ok(t, "rename", map[string]any{"from": "full/x", "to": "empty"})
	c.ok(t, "rmdir", map[string]any{"path": "dir/sub"})
	c.ok(t, "rename", map[string]any{"from": "dir", "to": "moved"})
	writeFile(t, filepath.Join(e.folder, "a.txt"), "a")
	writeFile(t, filepath.Join(e.folder, "b.txt"), "b")
	c.ok(t, "rename", map[string]any{"from": "a.txt", "to": "b.txt"})
	if readFile(t, filepath.Join(e.folder, "b.txt")) != "a" {
		t.Fatal("rename onto a file did not replace it")
	}
	entries := c.list(t, "")
	if _, ok := entries["dir"]; ok || entries["moved"].str("kind") != "directory" || entries["b.txt"].str("kind") != "file" {
		t.Fatalf("list %v", entries)
	}
	c.ok(t, "setattr", map[string]any{"path": "hello.txt", "mtime": 1_000_000_000_000})
	if r := c.ok(t, "stat", map[string]any{"path": "hello.txt"}); r.num("mtime") != 1_000_000_000_000 {
		t.Fatalf("mtime %v", r)
	}
	st := c.ok(t, "statfs", nil)
	if st.num("bsize") <= 0 || st.num("blocks") <= 0 || st.num("bavail") > st.num("blocks") {
		t.Fatalf("statfs %v", st)
	}
	long := strings.Repeat("x", 300)
	if r := c.callOn(t, "project", "stat", map[string]any{"path": long}); r.reset() != "ENAMETOOLONG" && (runtime.GOOS != "windows" || r.reset() != "ENOENT") {
		t.Fatalf("a 300-byte name: %v", r)
	}
	attr := c.ok(t, "stat", map[string]any{"path": "hello.txt"})
	if !etagShape.MatchString(attr.str("etag")) || attr.num("ino") == 0 {
		t.Fatalf("etag %v", attr)
	}
	parts := strings.Split(strings.Trim(attr.str("etag"), `"`), "-")
	ino, _ := strconv.ParseUint(parts[2], 10, 64)
	if parts[0] != itoa(attr.num("size")) || parts[1] != "1000000000000000000" || float64(ino) != attr.num("ino") {
		t.Fatalf("etag %v does not match its attr", attr)
	}
}

var etagShape = regexp.MustCompile(`^"[0-9]+-[0-9]+-[0-9]+"$`)

func itoa(f float64) string {
	b, _ := json.Marshal(int64(f))
	return string(b)
}

func (e *fsEnv) testFSArguments(t *testing.T) {
	c := e.c
	c.errno(t, "stat", map[string]any{"path": 7}, "EINVAL")
	c.errno(t, "rename", map[string]any{"from": "hello.txt"}, "EINVAL")
	c.errno(t, "rename", map[string]any{"from": nil, "to": "x"}, "EINVAL")
	c.errno(t, "symlink", map[string]any{"path": "l"}, "EINVAL")
	c.errno(t, "open", map[string]any{"path": "hello.txt", "mode": "rw"}, "EINVAL")
	fh, _ := c.open(t, map[string]any{"path": "hello.txt"})
	for _, a := range []map[string]any{
		{"fh": fh, "offset": -1, "size": 1},
		{"fh": fh, "offset": 0, "size": fsMaxIO + 1},
		{"fh": fh, "offset": "0", "size": 1},
		{"fh": fh, "offset": 0},
	} {
		c.errno(t, "read", a, "EINVAL")
	}
	c.errno(t, "read", map[string]any{"fh": "1", "offset": 0, "size": 1}, "EBADF")
	if r := c.js(t, "raw", map[string]any{"kind": "fs", "mount": "project", "op": "write", "fh": fh, "offset": 1.5}, false); r.reset() != "EINVAL" {
		t.Fatalf("write with a bad offset: %v", r)
	}
	c.ok(t, "stat", map[string]any{"path": nil})
	c.ok(t, "stat", map[string]any{"path": "./hello.txt"})
	c.ok(t, "stat", map[string]any{"path": "hello.txt/"})
	c.ok(t, "release", map[string]any{"fh": fh})
}

func (e *fsEnv) testFSCaseRename(t *testing.T) {
	c := e.c
	writeFile(t, filepath.Join(e.folder, "Case.txt"), "case")
	c.ok(t, "rename", map[string]any{"from": "Case.txt", "to": "case.txt"})
	entries := c.list(t, "")
	if _, ok := entries["case.txt"]; !ok {
		t.Fatalf("list %v", entries)
	}
	var h struct {
		Mounts []struct {
			Capabilities struct {
				CaseInsensitive bool `json:"caseInsensitive"`
			} `json:"capabilities"`
		} `json:"mounts"`
	}
	e.p.eval("linkTest.ready()", &h)
	if _, ok := entries["Case.txt"]; ok && h.Mounts[0].Capabilities.CaseInsensitive {
		t.Fatalf("both names after a case-only rename: %v", entries)
	}
	if h.Mounts[0].Capabilities.CaseInsensitive {
		c.ok(t, "stat", map[string]any{"path": "CASE.TXT"})
	} else {
		c.errno(t, "stat", map[string]any{"path": "CASE.TXT"}, "ENOENT")
	}
	if readFile(t, filepath.Join(e.folder, "case.txt")) != "case" {
		t.Fatal("content lost")
	}
}

func (e *fsEnv) testFSWrites(t *testing.T) {
	c := e.c
	linked := filepath.Join(e.folder, "linked.txt")
	writeFile(t, linked, "old content")
	if err := os.Link(linked, filepath.Join(e.folder, "other-name.txt")); err != nil {
		t.Fatal(err)
	}
	fh, attr := c.open(t, map[string]any{"path": "linked.txt", "write": true, "truncate": true})
	if attr.num("size") != 0 || attr.str("kind") != "file" {
		t.Fatalf("open %v", attr)
	}
	if r := c.write(t, fh, 6, text("world")); r.reset() != "" {
		t.Fatalf("write %v", r)
	}
	if r := c.write(t, fh, 0, text("hello ")); r.reset() != "" {
		t.Fatalf("write %v", r)
	}
	if r := c.read(t, fh, 0, 100, "not the etag"); r.body(t) != "hello world" {
		t.Fatalf("read on a write handle: %v", r)
	}
	if r := c.ok(t, "release", map[string]any{"fh": fh}); r.obj("attr").num("size") != 11 {
		t.Fatalf("release %v", r)
	}
	if got := readFile(t, filepath.Join(e.folder, "other-name.txt")); got != "hello world" {
		t.Fatalf("hard link sees %q", got)
	}
	if r := c.write(t, fh, 0, text("x")); r.reset() != "EBADF" {
		t.Fatalf("write after release: %v", r)
	}
	c.errno(t, "open", map[string]any{"path": "linked.txt", "create": true, "exclusive": true}, "EEXIST")
	c.errno(t, "open", map[string]any{"path": "moved", "write": true}, "EISDIR")
	c.errno(t, "open", map[string]any{"path": "moved"}, "EISDIR")
	c.errno(t, "open", map[string]any{"path": "moved", "create": true, "exclusive": true}, "EEXIST")
	c.errno(t, "open", map[string]any{"path": "", "write": true}, "EISDIR")
	c.errno(t, "open", map[string]any{"path": "nope/x", "create": true, "write": true}, "ENOENT")
	c.errno(t, "open", map[string]any{"path": "nope.txt", "write": true}, "ENOENT")
	created, _ := c.open(t, map[string]any{"path": "fresh.txt", "create": true, "exclusive": true})
	if r := c.write(t, created, 4, text("tail")); r.reset() != "" {
		t.Fatalf("write %v", r)
	}
	c.ok(t, "release", map[string]any{"fh": created})
	if got := readFile(t, filepath.Join(e.folder, "fresh.txt")); got != "\x00\x00\x00\x00tail" {
		t.Fatalf("fresh.txt %q", got)
	}
	again, _ := c.open(t, map[string]any{"path": "fresh.txt", "write": true})
	if r := c.write(t, again, 0, prngData(fsMaxIO+1, 7)); r.reset() != "EINVAL" {
		t.Fatalf("a write past maxIo: %v", r)
	}
	if fi, _ := os.Stat(filepath.Join(e.folder, "fresh.txt")); fi.Size() > fsMaxIO {
		t.Fatalf("a refused write left %d bytes", fi.Size())
	}
	docs := &fsClient{p: e.p, mount: "docs"}
	if r := docs.write(t, again, 0, text("x")); r.reset() != "EROFS" {
		t.Fatalf("a write sent to a read-only mount: %v", r)
	}
	ofh, _ := docs.open(t, map[string]any{"path": "readme.md"})
	if r := c.read(t, ofh, 0, 10, nil); r.reset() != "EBADF" {
		t.Fatalf("an fh from another mount: %v", r)
	}
	if r := docs.read(t, again, 0, 10, nil); r.reset() != "EBADF" {
		t.Fatalf("an fh used on another mount: %v", r)
	}
	docs.ok(t, "release", map[string]any{"fh": ofh})
	c.ok(t, "release", map[string]any{"fh": again})
	mode := c.ok(t, "open", map[string]any{"path": "private.txt", "create": true, "mode": 0o600})
	c.ok(t, "release", map[string]any{"fh": mode.num("fh")})
	if fi, _ := os.Stat(filepath.Join(e.folder, "private.txt")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("created with %v", fi.Mode())
	}
}

func (e *fsEnv) testFSReads(t *testing.T) {
	c := e.c
	writeFile(t, filepath.Join(e.folder, "data.bin"), "abcdefghij")
	fh, attr := c.open(t, map[string]any{"path": "data.bin"})
	etag := attr.str("etag")
	w := c.read(t, fh, 2, 4, etag)
	h := w.obj("head")
	if w.body(t) != "cdef" || h.str("etag") != etag || h.num("size") != 10 || h.num("offset") != 2 || h.num("length") != 4 {
		t.Fatalf("window %v", w)
	}
	if r := c.read(t, fh, 8, 100, etag); r.body(t) != "ij" || r.obj("head").num("length") != 2 {
		t.Fatalf("tail %v", r)
	}
	if r := c.read(t, fh, 50, 10, etag); r.num("bytes") != 0 || r.obj("head").num("length") != 0 {
		t.Fatalf("past EOF %v", r)
	}
	if r := c.read(t, fh, 0, 4, nil); r.body(t) != "abcd" {
		t.Fatalf("without ifMatch %v", r)
	}
	big := c.js(t, "read", "project", fh, 0, 10, etag, map[string]any{"shared": false})
	if big.body(t) != "abcdefghij" {
		t.Fatalf("on its own channel %v", big)
	}
	time.Sleep(20 * time.Millisecond)
	writeFile(t, filepath.Join(e.folder, "data.bin"), "ABCDEFGHIJK")
	if r := c.read(t, fh, 0, 4, etag); r.reset() != "ESTALE" || r.str("detail") != "file changed since it was opened" {
		t.Fatalf("stale read %v", r)
	}
	if r := c.read(t, fh, 0, 4, nil); r.body(t) != "ABCD" {
		t.Fatalf("fresh read %v", r)
	}
	if r := c.ok(t, "release", map[string]any{"fh": fh}); len(r) != 0 {
		t.Fatalf("release of a name handle %v", r)
	}
	c.errno(t, "read", map[string]any{"fh": fh, "offset": 0, "size": 1}, "EBADF")
	gone, _ := c.open(t, map[string]any{"path": "data.bin"})
	if err := os.Remove(filepath.Join(e.folder, "data.bin")); err != nil {
		t.Fatal(err)
	}
	c.errno(t, "read", map[string]any{"fh": gone, "offset": 0, "size": 1}, "ENOENT")
	c.ok(t, "release", map[string]any{"fh": gone})
}

func (e *fsEnv) testFSLarge(t *testing.T) {
	c := e.c
	size := 2*fsMaxIO + 12345
	fh, _ := c.open(t, map[string]any{"path": "big.bin", "create": true, "truncate": true})
	var want []byte
	for i, off := 0, 0; off < size; i, off = i+1, off+fsMaxIO {
		n := min(fsMaxIO, size-off)
		if r := c.write(t, fh, off, prngData(n, 100+i)); r.reset() != "" {
			t.Fatalf("write %d: %v", off, r)
		}
		want = append(want, prngBytes(n, uint32(100+i))...)
	}
	c.ok(t, "release", map[string]any{"fh": fh})
	b, err := os.ReadFile(filepath.Join(e.folder, "big.bin"))
	if err != nil || sha(b) != sha(want) {
		t.Fatalf("big.bin on disk: %d bytes, %v", len(b), err)
	}
	rfh, attr := c.open(t, map[string]any{"path": "big.bin"})
	r := c.js(t, "readPages", "project", rfh, attr.num("size"), fsMaxIO, attr.str("etag"))
	if r.reset() != "" || r.str("sha256") != sha(want) {
		t.Fatalf("read back: %v", r["sha256"])
	}
	hole, _ := c.open(t, map[string]any{"path": "holes.bin", "create": true, "write": true})
	at := 40 << 20
	if r := c.write(t, hole, at, prngData(1<<20, 9)); r.reset() != "" {
		t.Fatalf("write past a hole %v", r)
	}
	if r := c.write(t, hole, 3, text("abc")); r.reset() != "" {
		t.Fatalf("write in the hole %v", r)
	}
	c.ok(t, "release", map[string]any{"fh": hole})
	expect := make([]byte, at)
	copy(expect[3:], "abc")
	expect = append(expect, prngBytes(1<<20, 9)...)
	hfh, hattr := c.open(t, map[string]any{"path": "holes.bin"})
	if hattr.num("size") != float64(len(expect)) {
		t.Fatalf("holes.bin size %v", hattr)
	}
	if r := c.js(t, "readPages", "project", hfh, hattr.num("size"), fsMaxIO, hattr.str("etag")); r.str("sha256") != sha(expect) {
		t.Fatalf("holes read back wrong: %v", r["bytes"])
	}
	if r := c.js(t, "readPages", "project", hfh, hattr.num("size"), 1<<20, hattr.str("etag")); r.str("sha256") != sha(expect) {
		t.Fatalf("holes in 1 MiB pages wrong: %v", r["bytes"])
	}
}

func (e *fsEnv) testFSList(t *testing.T) {
	c := e.c
	dir := filepath.Join(e.folder, "many")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 1500 {
		writeFile(t, filepath.Join(dir, "file-with-a-long-name-"+itoa(float64(i))+".txt"), "x")
	}
	entries := c.list(t, "many")
	if len(entries) != 1500 || entries["file-with-a-long-name-1499.txt"].num("size") != 1 {
		t.Fatalf("list of 1500: %d", len(entries))
	}
	c.errno(t, "list", map[string]any{"path": "hello.txt"}, "ENOTDIR")
	c.errno(t, "list", map[string]any{"path": "gone"}, "ENOENT")
	root := c.list(t, "")
	if root["hello.txt"].str("kind") != "file" || root["many"].str("kind") != "directory" {
		t.Fatalf("root list %v", root)
	}
}

func (e *fsEnv) testFSHandleLimit(t *testing.T) {
	r := e.c.js(t, "openMany", "docs", "readme.md", 4097)
	if r.num("ok") != 4096 || r.num("EMFILE") != 1 {
		t.Fatalf("4097 opens: %v", r)
	}
	e.c.ok(t, "release", map[string]any{"fh": e.c.ok(t, "open", map[string]any{"path": "hello.txt"}).num("fh")})
	if r := e.c.js(t, "releaseAll", "docs"); r.num("ok") != 4096 {
		t.Fatalf("release: %v", r)
	}
	docs := &fsClient{p: e.p, mount: "docs"}
	docs.ok(t, "release", map[string]any{"fh": docs.ok(t, "open", map[string]any{"path": "readme.md"}).num("fh")})
}

func (e *fsEnv) testFSHandlesDie(t *testing.T) {
	c := e.c
	fh, _ := c.open(t, map[string]any{"path": "dying.txt", "create": true, "write": true})
	if r := c.write(t, fh, 0, text("kept")); r.reset() != "" {
		t.Fatalf("write %v", r)
	}
	name, _ := c.open(t, map[string]any{"path": "hello.txt"})
	pid := e.a.cmd.Process.Pid
	if runtime.GOOS == "linux" && !openIn(pid, filepath.Join(e.folder, "dying.txt")) {
		t.Fatal("the write handle isn't open in slicc")
	}
	old := e.waitUp("")
	e.p.reload()
	e.waitUp(old)
	c.errno(t, "release", map[string]any{"fh": fh}, "EBADF")
	c.errno(t, "read", map[string]any{"fh": name, "offset": 0, "size": 1}, "EBADF")
	if readFile(t, filepath.Join(e.folder, "dying.txt")) != "kept" {
		t.Fatal("what was written is gone")
	}
	if runtime.GOOS == "linux" && openIn(pid, filepath.Join(e.folder, "dying.txt")) {
		t.Fatal("the write handle outlived its link")
	}
	if r := c.ok(t, "open", map[string]any{"path": "hello.txt"}); r.num("fh") != 1 {
		t.Fatalf("a new link numbers handles from 1: %v", r)
	}
}

func openIn(pid int, path string) bool {
	dir := filepath.Join("/proc", itoa(float64(pid)), "fd")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if target, err := os.Readlink(filepath.Join(dir, e.Name())); err == nil && target == path {
			return true
		}
	}
	return false
}
