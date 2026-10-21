package integration

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	hostclip "github.com/ai-ecoverse/slicc-cli/internal/clipboard"
)

var clipboardTools = []string{"pbcopy", "pbpaste", "wl-copy", "wl-paste", "xclip", "xsel"}

const clipText = "héllo ✓ 日本\r\n\tsecond line\n"

type clipRes struct {
	Opened map[string]any `json:"opened"`
	Text64 string         `json:"text64"`
	Reset  string         `json:"reset"`
	Detail string         `json:"detail"`
	Error  string         `json:"error"`
}

func (r clipRes) text() string {
	b, _ := base64.StdEncoding.DecodeString(r.Text64)
	return string(b)
}

func (p *page) clipboard(t *testing.T, op string, text []byte) clipRes {
	t.Helper()
	var r clipRes
	arg := any(nil)
	if text != nil {
		arg = base64.StdEncoding.EncodeToString(text)
	}
	p.evalWithin("linkTest.clipboard("+jsArgs(op, arg)+")", &r, 60*time.Second)
	if r.Error != "" {
		t.Fatalf("clipboard %s: %s", op, r.Error)
	}
	return r
}

type fakeClip struct {
	dir   string
	store string
	log   string
}

var fakeClipExe []byte

func newFakeClip(t *testing.T, names ...string) *fakeClip {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses the Win32 clipboard, not a tool on PATH")
	}
	if fakeClipExe == nil {
		out := filepath.Join(t.TempDir(), "fakeclip")
		build := exec.Command("go", "build", "-o", out, "./testdata/fakeclip")
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		fakeClipExe = b
	}
	f := &fakeClip{dir: t.TempDir()}
	f.store = filepath.Join(f.dir, "store")
	f.log = filepath.Join(f.dir, "log")
	bin := filepath.Join(f.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		writeExe(t, filepath.Join(bin, n), fakeClipExe)
	}
	f.dir = bin
	return f
}

func (f *fakeClip) mode(t *testing.T, m string) {
	t.Helper()
	if err := os.WriteFile(f.store+".mode", []byte(m), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeClip) content(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.store)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f *fakeClip) calls(t *testing.T) []map[string]any {
	t.Helper()
	file, err := os.Open(f.log)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	var out []map[string]any
	for sc := bufio.NewScanner(file); sc.Scan(); {
		var c map[string]any
		_ = json.Unmarshal(sc.Bytes(), &c)
		out = append(out, c)
	}
	return out
}

func (f *fakeClip) env(display bool) []string {
	env := []string{"PATH=" + f.dir + string(os.PathListSeparator) + os.Getenv("PATH"), "FAKECLIP_FILE=" + f.store, "FAKECLIP_LOG=" + f.log}
	if display {
		env = append(env, "DISPLAY=:fake")
	}
	return env
}

func hostTools() []string {
	if runtime.GOOS == "darwin" {
		return []string{"pbcopy", "pbpaste"}
	}
	return []string{"xclip"}
}

func hostToolName() string {
	if runtime.GOOS == "darwin" {
		return "pbcopy"
	}
	return "xclip"
}

func setEnv(t *testing.T, env []string) {
	t.Helper()
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
}

func clipCode(err error) string {
	var ce *hostclip.Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	if err != nil {
		return "other: " + err.Error()
	}
	return ""
}

func TestClipboardToolBackends(t *testing.T) {
	type backend struct {
		tools   []string
		env     []string
		name    string
		read    string
		empties bool
	}
	var backends []backend
	switch runtime.GOOS {
	case "darwin":
		backends = []backend{{tools: []string{"pbcopy", "pbpaste"}, name: "pbcopy", read: "pbpaste -Prefer txt"}}
	case "windows":
		t.Skip("Windows uses the Win32 clipboard; TestClipboardRealBackend covers it")
	default:
		backends = []backend{
			{tools: []string{"wl-copy", "wl-paste", "xclip"}, env: []string{"WAYLAND_DISPLAY=wayland-fake", "DISPLAY=:fake"}, name: "wl-copy", read: "wl-paste --no-newline --type text", empties: true},
			{tools: []string{"xclip", "xsel"}, env: []string{"DISPLAY=:fake"}, name: "xclip", read: "xclip -selection clipboard -out -target UTF8_STRING", empties: true},
			{tools: []string{"xsel"}, env: []string{"DISPLAY=:fake"}, name: "xsel", read: "xsel --clipboard --output"},
			{tools: []string{"wl-copy", "wl-paste", "xsel"}, env: []string{"DISPLAY=:fake"}, name: "xsel", read: "xsel --clipboard --output"},
		}
	}
	for _, b := range backends {
		t.Run(b.name+" from "+strings.Join(b.tools, ","), func(t *testing.T) {
			f := newFakeClip(t, b.tools...)
			t.Setenv("WAYLAND_DISPLAY", "")
			t.Setenv("DISPLAY", "")
			setEnv(t, append([]string{"PATH=" + f.dir, "FAKECLIP_FILE=" + f.store, "FAKECLIP_LOG=" + f.log}, b.env...))
			c, why := hostclip.Find()
			if c == nil || c.Name != b.name {
				t.Fatalf("Find: %+v, %q", c, why)
			}
			ctx := context.Background()
			if err := c.Write(ctx, []byte(clipText)); err != nil {
				t.Fatal(err)
			}
			if got := f.content(t); got != clipText {
				t.Fatalf("the tool got %q", got)
			}
			got, err := c.Read(ctx)
			if err != nil || got != clipText {
				t.Fatalf("read back %q, %v", got, err)
			}
			calls := f.calls(t)
			last := calls[len(calls)-1]
			var args []string
			for _, a := range last["args"].([]any) {
				args = append(args, a.(string))
			}
			if line := strings.Join(append([]string{last["name"].(string)}, args...), " "); line != b.read {
				t.Fatalf("read ran %q, want %q", line, b.read)
			}
			if runtime.GOOS == "darwin" {
				for _, call := range calls {
					if call["lc_all"] != "en_US.UTF-8" || call["lang"] != "en_US.UTF-8" {
						t.Fatalf("%s ran without a UTF-8 locale: %v", call["name"], call)
					}
				}
			}
			for mode, want := range map[string]string{"fail": "EIO", "deny": "EACCES", "big": "EFBIG", "bad": "EILSEQ"} {
				f.mode(t, mode)
				if _, err := c.Read(ctx); clipCode(err) != want {
					t.Errorf("read with %s: %v, want %s", mode, err, want)
				}
			}
			f.mode(t, "fail")
			if err := c.Write(ctx, []byte("x")); clipCode(err) != "EIO" {
				t.Errorf("a failing write: %v", err)
			}
			if b.empties {
				f.mode(t, "empty")
				if got, err := c.Read(ctx); err != nil || got != "" {
					t.Errorf("an empty clipboard: %q, %v", got, err)
				}
			}
			f.mode(t, "")
			if err := c.Write(ctx, make([]byte, hostclip.MaxBytes+1)); clipCode(err) != "EFBIG" {
				t.Errorf("a write over 1 MiB: %v", err)
			}
			if err := c.Write(ctx, []byte{0xc3, 0x28}); clipCode(err) != "EILSEQ" {
				t.Errorf("a write that isn't UTF-8: %v", err)
			}
			for _, n := range b.tools {
				_ = os.Remove(filepath.Join(f.dir, n))
			}
			if _, err := c.Read(ctx); clipCode(err) != "ENOTSUP" {
				t.Errorf("a tool that went away: %v", err)
			}
		})
	}
	t.Run("absent", func(t *testing.T) {
		empty := t.TempDir()
		t.Setenv("PATH", empty)
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("DISPLAY", "")
		want := "no pbcopy and pbpaste on PATH"
		if runtime.GOOS != "darwin" {
			want = "no display (neither WAYLAND_DISPLAY nor DISPLAY is set)"
		}
		if c, why := hostclip.Find(); c != nil || why != want {
			t.Fatalf("no tools: %+v, %q", c, why)
		}
		if runtime.GOOS != "darwin" {
			t.Setenv("DISPLAY", ":fake")
			if c, why := hostclip.Find(); c != nil || why != "no wl-copy, xclip or xsel on PATH" {
				t.Fatalf("X11 without tools: %+v, %q", c, why)
			}
			t.Setenv("DISPLAY", "")
			t.Setenv("WAYLAND_DISPLAY", "wayland-fake")
			if c, why := hostclip.Find(); c != nil || why != "no wl-copy and wl-paste on PATH" {
				t.Fatalf("Wayland without tools: %+v, %q", c, why)
			}
		}
	})
}

func TestClipboardRealBackend(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("only on CI: this test uses the host's real clipboard")
	}
	t.Setenv("PATH", realGetenv("PATH"))
	if runtime.GOOS == "linux" {
		xvfb, err1 := exec.LookPath("Xvfb")
		_, err2 := exec.LookPath("xclip")
		if err1 != nil || err2 != nil {
			t.Skip("this runner has no Xvfb and xclip")
		}
		display := ":97"
		cmd := exec.Command(xvfb, display, "-nolisten", "tcp")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { reap(t, cmd, "Xvfb") })
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("DISPLAY", display)
		deadline := time.Now().Add(10 * time.Second)
		for exec.Command("xclip", "-selection", "clipboard", "-in").Run() != nil {
			if time.Now().After(deadline) {
				t.Skip("Xvfb didn't come up")
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	c, why := hostclip.Find()
	if c == nil {
		t.Fatalf("no clipboard: %s", why)
	}
	ctx := context.Background()
	for _, text := range []string{clipText, "", "trailing newline\n", strings.Repeat("ü", 300000)} {
		if err := c.Write(ctx, []byte(text)); err != nil {
			t.Fatalf("%s write: %v", c.Name, err)
		}
		got, err := c.Read(ctx)
		if err != nil || got != text {
			t.Fatalf("%s round trip of %d bytes: got %d bytes, %v", c.Name, len(text), len(got), err)
		}
	}
}

type clipHello struct {
	Caps      []string `json:"caps"`
	Clipboard *struct {
		Backend string `json:"backend"`
	} `json:"clipboard"`
}

func (p *page) clipHello() clipHello {
	var h clipHello
	p.eval("linkTest.ready()", &h)
	return h
}

func TestClipboardOverTheLink(t *testing.T) {
	f := newFakeClip(t, hostTools()...)
	tool := hostToolName()
	t.Run("write only by default", func(t *testing.T) {
		e := setupLinkEnv(t, false, f.env(true))
		e.a.waitStderr(t, "slicc: clipboard: offering "+tool+" (clipboard)", 10*time.Second)
		h := e.p.clipHello()
		if !slices.Contains(h.Caps, "clipboard") || slices.Contains(h.Caps, "clipboard-read") || h.Clipboard == nil || h.Clipboard.Backend != tool {
			t.Fatalf("HELLO %+v", h)
		}
		if r := e.p.clipboard(t, "write", []byte(clipText)); r.Reset != "" || r.Text64 != "" {
			t.Fatalf("write %+v", r)
		}
		if got := f.content(t); got != clipText {
			t.Fatalf("the clipboard holds %q", got)
		}
		if r := e.p.clipboard(t, "read", nil); r.Reset != "EACCES" {
			t.Fatalf("a read without clipboard-read: %+v", r)
		}
		if strings.Contains(e.a.stderr.String(), "second line") {
			t.Fatalf("slicc logged the clipboard's text:\n%s", e.a.stderr.String())
		}
	})
	t.Run("--clipboard reads too", func(t *testing.T) {
		e := setupLinkEnv(t, false, f.env(true), "--clipboard")
		e.a.waitStderr(t, "slicc: clipboard: offering "+tool+" (clipboard, clipboard-read)", 10*time.Second)
		if h := e.p.clipHello(); !slices.Contains(h.Caps, "clipboard") || !slices.Contains(h.Caps, "clipboard-read") {
			t.Fatalf("HELLO %+v", h)
		}
		if r := e.p.clipboard(t, "write", []byte(clipText)); r.Reset != "" {
			t.Fatalf("write %+v", r)
		}
		if r := e.p.clipboard(t, "read", nil); r.Reset != "" || r.text() != clipText {
			t.Fatalf("read %+v (%q)", r, r.text())
		}
		e.a.waitStderr(t, `slicc: clipboard: "link test page" read this machine's clipboard`, 5*time.Second)
		big := strings.Repeat("ß", hostclip.MaxBytes/2)
		if r := e.p.clipboard(t, "write", []byte(big)); r.Reset != "" {
			t.Fatalf("a 1 MiB write %+v", r.Reset)
		}
		if r := e.p.clipboard(t, "read", nil); r.Reset != "" || r.text() != big {
			t.Fatalf("a 1 MiB read: %s, %d bytes", r.Reset, len(r.text()))
		}
		for _, c := range []struct {
			op, mode string
			text     []byte
			want     string
		}{
			{"write", "", make([]byte, hostclip.MaxBytes+1), "EFBIG"},
			{"write", "", []byte{0xff, 0xfe}, "EILSEQ"},
			{"read", "deny", nil, "EACCES"},
			{"read", "fail", nil, "EIO"},
			{"write", "fail", []byte("x"), "EIO"},
			{"read", "big", nil, "EFBIG"},
			{"read", "bad", nil, "EILSEQ"},
			{"frobnicate", "", nil, "EPROTO"},
		} {
			f.mode(t, c.mode)
			if r := e.p.clipboard(t, c.op, c.text); r.Reset != c.want {
				t.Errorf("%s with %q: %+v, want %s", c.op, c.mode, r, c.want)
			}
		}
		f.mode(t, "")
		if strings.Contains(e.a.stderr.String(), "second line") || strings.Contains(e.a.stderr.String(), "ßß") {
			t.Fatalf("slicc logged the clipboard's text")
		}
	})
	t.Run("--no-clipboard", func(t *testing.T) {
		e := setupLinkEnv(t, false, f.env(true), "--no-clipboard")
		if h := e.p.clipHello(); slices.Contains(h.Caps, "clipboard") || h.Clipboard != nil {
			t.Fatalf("HELLO %+v", h)
		}
		if r := e.p.clipboard(t, "write", []byte("x")); r.Reset != "EOPNOTSUPP" {
			t.Fatalf("%+v", r)
		}
	})
	if runtime.GOOS != "darwin" {
		t.Run("no display", func(t *testing.T) {
			e := setupLinkEnv(t, false, f.env(false))
			e.a.waitStderr(t, "slicc: clipboard: no display (neither WAYLAND_DISPLAY nor DISPLAY is set); not offering it", 10*time.Second)
			if h := e.p.clipHello(); slices.Contains(h.Caps, "clipboard") {
				t.Fatalf("HELLO %+v", h)
			}
			if r := e.p.clipboard(t, "read", nil); r.Reset != "EOPNOTSUPP" {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestClipboardFlagConflict(t *testing.T) {
	for _, args := range [][]string{{"attach", "--cdp", "http://127.0.0.1:1", "--clipboard", "--no-clipboard"}, {"pair", "--clipboard", "--no-clipboard"}, {"--plain", "--clipboard", "--no-clipboard"}} {
		out, err := exec.Command(binary, args...).CombinedOutput()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 2 || !strings.Contains(string(out), "--no-clipboard can't go with --clipboard or --clipboard-write") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
}

func TestFollowClipboardFlags(t *testing.T) {
	f := newFakeClip(t, hostTools()...)
	for name, c := range map[string]struct {
		args        []string
		write, read bool
	}{
		"no flag":           {nil, false, false},
		"--clipboard-write": {[]string{"--clipboard-write"}, true, false},
		"--clipboard":       {[]string{"--clipboard"}, true, true},
	} {
		t.Run(name, func(t *testing.T) {
			env, h, trays, p, _ := followSetup(t, 1)
			fp := startFollow(t, append(env, f.env(true)...), h.joinURL(trays[0]), c.args...)
			fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
			p.waitUp(t, "")
			hello := p.clipHello()
			if slices.Contains(hello.Caps, "clipboard") != c.write || slices.Contains(hello.Caps, "clipboard-read") != c.read {
				t.Fatalf("HELLO %+v", hello)
			}
			r := p.clipboard(t, "read", nil)
			switch {
			case !c.write && r.Reset != "EOPNOTSUPP", c.write && !c.read && r.Reset != "EACCES", c.read && r.Reset != "":
				t.Fatalf("read: %+v", r)
			}
		})
	}
}

func TestClipboardOverTheLinkOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("only on Windows CI: this test uses the host's real clipboard")
	}
	e := setupLinkEnv(t, false, nil, "--clipboard")
	e.a.waitStderr(t, "slicc: clipboard: offering the Windows clipboard (clipboard, clipboard-read)", 10*time.Second)
	if h := e.p.clipHello(); !slices.Contains(h.Caps, "clipboard-read") || h.Clipboard == nil || h.Clipboard.Backend != "the Windows clipboard" {
		t.Fatalf("HELLO %+v", h)
	}
	if r := e.p.clipboard(t, "write", []byte(clipText)); r.Reset != "" {
		t.Fatalf("write %+v", r)
	}
	if r := e.p.clipboard(t, "read", nil); r.Reset != "" || r.text() != clipText {
		t.Fatalf("read %+v (%q)", r, r.text())
	}
}
