package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type trayRow struct {
	V          int        `json:"v"`
	PID        int        `json:"pid"`
	Mode       string     `json:"mode"`
	Version    string     `json:"version"`
	Seven      string     `json:"seven"`
	Origin     string     `json:"origin"`
	Hub        string     `json:"hub"`
	Device     string     `json:"device"`
	State      string     `json:"state"`
	Since      time.Time  `json:"since"`
	Attempt    int        `json:"attempt"`
	RetryAt    *time.Time `json:"retryAt"`
	Code       string     `json:"code"`
	Offers     []string   `json:"offers"`
	Dials      int        `json:"dials"`
	Links      int        `json:"links"`
	LastLinked *time.Time `json:"lastLinked"`
}

func trayRuntime(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS != "windows" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "slr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	switch runtime.GOOS {
	case "linux":
		t.Setenv("XDG_RUNTIME_DIR", dir)
		return filepath.Join(dir, "slicc")
	case "windows":
		t.Setenv("LOCALAPPDATA", dir)
		return filepath.Join(dir, "slicc", "run")
	}
	t.Setenv("TMPDIR", dir)
	return filepath.Join(dir, "slicc-"+strconv.Itoa(os.Getuid()))
}

func trayJSON(t *testing.T, env []string, args ...string) []trayRow {
	t.Helper()
	out, errOut, code := sliccEnv(t, env, append([]string{"tray", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("slicc tray --json: code %d, stderr %q", code, errOut)
	}
	var rows []trayRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("slicc tray --json printed %q: %v", out, err)
	}
	return rows
}

func trayRowFor(t *testing.T, env []string, pid int, state string) trayRow {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var rows []trayRow
	for {
		rows = trayJSON(t, env)
		for _, r := range rows {
			if r.PID == pid && (state == "" || r.State == state) {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("slicc tray never listed PID %d in state %q: %+v", pid, state, rows)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func deviceID(fingerprint string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(fingerprint, "sha-256 "), ":", ""))
}

func TestTrayListsAFollowAndWipesItOnceKilled(t *testing.T) {
	sockets := trayRuntime(t)
	env := append(configEnv(t), "SLICC_NO_TELEMETRY=1")
	h := newFakeHub(t)
	tr := h.addTray("follow")
	h.with(func() { tr.NoLeader = true })
	fp := startFollow(t, env, h.joinURL(tr), "--name", "inventory")
	fingerprint, _ := fp.fingerprint(t)
	pid := fp.cmd.Process.Pid
	r := trayRowFor(t, env, pid, "")
	if r.V != 1 || r.Mode != "follow" || r.Device != deviceID(fingerprint) || !strings.HasPrefix(h.joinURL(tr), r.Hub+"/") || r.Dials < 1 {
		t.Fatalf("row %+v (device %s)", r, deviceID(fingerprint))
	}
	if r.State != "dialing" && r.State != "reconnecting" && r.State != "waiting for offer" {
		t.Fatalf("state %q while the hub has no leader", r.State)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(sockets)
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("socket directory %v %v", fi, err)
		}
	}
	sock := filepath.Join(sockets, strconv.Itoa(pid)+".sock")
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("no control socket at %s: %v", sock, err)
	}

	out, errOut, code := sliccEnv(t, env, "tray")
	if code != 0 || !strings.Contains(out, "PID") || !strings.Contains(out, strconv.Itoa(pid)+"  follow") {
		t.Fatalf("slicc tray: code %d\n%s\n%s", code, out, errOut)
	}
	short := r.Device[:4] + "…" + r.Device[len(r.Device)-4:]
	if !strings.Contains(out, short) {
		t.Fatalf("slicc tray lacks the device %s:\n%s", short, out)
	}
	raw, _, _ := sliccEnv(t, env, "tray", "--json")
	assertRedacted(t, out+errOut+raw, h, tr)

	out, _, code = sliccEnv(t, env, "tray", "-q")
	if code != 0 || !regexp.MustCompile(`(?m)^`+strconv.Itoa(pid)+`$`).MatchString(out) {
		t.Fatalf("slicc tray -q: code %d, %q", code, out)
	}

	fp.stop(t)
	out, errOut, code = sliccEnv(t, env, "tray")
	if code != 0 || strings.Contains(out, strconv.Itoa(pid)) {
		t.Fatalf("slicc tray lists a killed follow: code %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(sock); err == nil && !strings.Contains(errOut, "removed the stale socket of PID "+strconv.Itoa(pid)) {
		t.Fatalf("the stale socket is neither removed nor reported: %q", errOut)
	}
	if _, err := os.Stat(sock); err == nil {
		t.Fatalf("the stale socket %s is still there", sock)
	}

	rows := trayJSON(t, env, "-a")
	var known *trayRow
	for i := range rows {
		if rows[i].Device == r.Device {
			known = &rows[i]
		}
	}
	if known == nil || known.PID != 0 || known.State != "not running" || known.Mode != "follow" || known.Hub != r.Hub {
		t.Fatalf("slicc tray -a: %+v", rows)
	}
	out, errOut, _ = sliccEnv(t, env, "tray", "-a")
	if !strings.Contains(out, "not running") || !strings.Contains(out, short) {
		t.Fatalf("slicc tray -a:\n%s", out)
	}
	assertRedacted(t, out+errOut, h, tr)
}

func TestTrayShowsALinkedFollow(t *testing.T) {
	trayRuntime(t)
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]), "--exit", "--name", "laptop")
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.waitUp(t, "")
	r := trayRowFor(t, env, fp.cmd.Process.Pid, "connected")
	if r.Seven != "link test page" || r.Links != 1 || r.LastLinked == nil || len(r.Offers) == 0 || r.Offers[0] != "exit" {
		t.Fatalf("row %+v", r)
	}
	out, _, _ := sliccEnv(t, env, "tray")
	if !regexp.MustCompile(strconv.Itoa(fp.cmd.Process.Pid) + `\s+follow\s+link test page\s+\S+\s+connected \d+s\s+exit`).MatchString(out) {
		t.Fatalf("slicc tray:\n%s", out)
	}
	assertFollowRedacted(t, fp, h, trays...)
	assertRedacted(t, out, h, trays...)
}

func TestTrayListsLocalModes(t *testing.T) {
	trayRuntime(t)
	a := startAttachEnv(t, true, nil, "--signal", "stdio", "--kernel-port", "8123")
	a.waitStderr(t, "setup messages on stdin and stdout", 10*time.Second)
	e := newLoopEnv(t)
	o := startLoop(t, e, "pair")
	o.waitStderr(t, "listening on")
	env := append(os.Environ(), "SLICC_NO_TELEMETRY=1")
	ar := trayRowFor(t, env, a.cmd.Process.Pid, "waiting")
	if ar.Mode != "attach" || ar.Device != "" || !contains(ar.Offers, "exit") || !contains(ar.Offers, "kernel:8123") {
		t.Fatalf("attach row %+v", ar)
	}
	pr := trayRowFor(t, env, o.cmd.Process.Pid, "waiting")
	if pr.Mode != "pair" || len(pr.Device) != 64 {
		t.Fatalf("pair row %+v", pr)
	}
	out, _, _ := sliccEnv(t, env, "tray", "-q")
	for _, pid := range []int{a.cmd.Process.Pid, o.cmd.Process.Pid} {
		if !regexp.MustCompile(`(?m)^` + strconv.Itoa(pid) + `$`).MatchString(out) {
			t.Fatalf("slicc tray -q lacks %d: %q", pid, out)
		}
	}
}

func TestTrayRefusesASymlinkedRuntimeDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the per-user directory's ACL guards it on Windows")
	}
	sockets := trayRuntime(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, sockets); err != nil {
		t.Fatal(err)
	}
	a := startAttachEnv(t, true, nil, "--signal", "stdio")
	a.waitStderr(t, "control socket: ", 10*time.Second)
	entries, _ := os.ReadDir(elsewhere)
	if len(entries) != 0 {
		t.Fatalf("slicc served a control socket through a symlink: %v", entries)
	}
	env := append(os.Environ(), "SLICC_NO_TELEMETRY=1")
	for _, r := range trayJSON(t, env) {
		if r.PID == a.cmd.Process.Pid {
			t.Fatalf("slicc tray listed %+v through a symlink", r)
		}
	}
}

func TestTrayTightensAnOpenRuntimeDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes on Windows")
	}
	sockets := trayRuntime(t)
	if err := os.Mkdir(sockets, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sockets, 0o755); err != nil {
		t.Fatal(err)
	}
	a := startAttachEnv(t, true, nil, "--signal", "stdio")
	env := append(os.Environ(), "SLICC_NO_TELEMETRY=1")
	trayRowFor(t, env, a.cmd.Process.Pid, "")
	if fi, err := os.Stat(sockets); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("socket directory %v %v", fi, err)
	}
}

func TestTrayUsage(t *testing.T) {
	trayRuntime(t)
	out, _, code := slicc(t, "tray", "--help")
	if code != 0 || !strings.Contains(out, "usage: slicc tray [--json] [-q] [-a]") || !strings.Contains(out, "xargs kill") {
		t.Fatalf("tray --help: code %d, %q", code, out)
	}
	for _, args := range [][]string{{"tray", "stop"}, {"tray", "-q", "--json"}, {"tray", "--bogus"}} {
		if _, _, code := slicc(t, args...); code != 2 {
			t.Fatalf("%v: code %d, want 2", args, code)
		}
	}
	out, errOut, code := slicc(t, "tray")
	if code != 0 || out != "" || !strings.Contains(errOut, "no slicc on this machine holds a link") {
		t.Fatalf("empty tray: code %d, %q, %q", code, out, errOut)
	}
	if out, _, _ := slicc(t, "--help"); !strings.Contains(out, "slicc tray") {
		t.Fatalf("--help doesn't list tray")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
