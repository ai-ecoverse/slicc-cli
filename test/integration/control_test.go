package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
)

func ctlSocket(t *testing.T, pid int) string {
	t.Helper()
	dir, err := ctl.Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strconv.Itoa(pid)+".sock")
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := ctl.Query(context.Background(), path); err == nil {
			return path
		}
		if time.Now().After(deadline) {
			t.Fatalf("no control socket at %s", path)
		}
	}
}

func rawCtl(t *testing.T, path, op string) string {
	t.Helper()
	r, err := ctl.Send(context.Background(), path, op)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	b, _ := json.Marshal(r)
	return string(b)
}

type controlLog struct{ buf bytes.Buffer }

func (c *controlLog) add(t *testing.T, what string, v any) {
	t.Helper()
	enc := json.NewEncoder(&c.buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"step": what, "line": v}); err != nil {
		t.Fatal(err)
	}
}

func closedEvent(t *testing.T, f *flowProc) map[string]any {
	t.Helper()
	lastIsClosed(t, f)
	return normalizeEvent(t, f.seen[len(f.seen)-1].raw)
}

func TestControlStopAndDetachEndALoopbackRun(t *testing.T) {
	e := newLoopEnv(t)
	var log controlLog
	for _, op := range []string{ctl.OpStop, ctl.OpDetach} {
		f := startFlow(t, e.env, "pair", "--json", "--no-kernel")
		f.next(t, "pairing")
		path := ctlSocket(t, f.cmd.Process.Pid)
		st, err := ctl.Query(context.Background(), path)
		if err != nil || !slices.Equal(st.Ops, []string{ctl.OpStatus, ctl.OpDetach, ctl.OpStop, ctl.OpRevoke}) {
			t.Fatalf("status %+v %v", st, err)
		}
		log.add(t, "status.ops", st.Ops)
		if r, err := ctl.Send(context.Background(), path, "frobnicate"); err != nil || r.OK || r.Error != "EINVAL" {
			t.Fatalf("unknown op: %+v %v", r, err)
		}
		reply := rawCtl(t, path, op)
		if reply != `{"v":1,"ok":true}` {
			t.Fatalf("%s: %s", op, reply)
		}
		log.add(t, op, json.RawMessage(`{"v":1,"ok":true}`))
		if code := f.rest(t); code != 0 {
			t.Fatalf("%s: exit %d", op, code)
		}
		c := closedEvent(t, f)
		_, kept := c["kept"]
		if kept != (op == ctl.OpDetach) {
			t.Fatalf("%s: closed %v", op, c)
		}
		log.add(t, "closed after "+op, c)
	}
	nativeFixture(t, "control.ndjson", log.buf.Bytes())
}

func TestKeepBrowsersTurnsSIGTERMIntoDetach(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no SIGTERM to send")
	}
	e := newLoopEnv(t)
	for _, keep := range []bool{false, true} {
		args := []string{"--json", "--no-kernel", "--page", "https://seven.sliccy.ai/"}
		if keep {
			args = append(args, "--keep-browsers")
		}
		f := startFlow(t, e.env, args...)
		f.next(t, "discovered")
		f.next(t, "mode")
		f.next(t, "pairing")
		if err := f.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if code := f.rest(t); code != 0 {
			t.Fatalf("exit %d", code)
		}
		c := closedEvent(t, f)
		if _, kept := c["kept"]; kept != keep {
			t.Fatalf("--keep-browsers %v: closed %v", keep, c)
		}
	}
}

func TestControlDetachLeavesTheLaunchedBrowserForTheNextRun(t *testing.T) {
	env, _ := launchEnv(t)
	env, chromeID := testChrome(t, env)
	srv := pageServer(t)
	pageURL := srv.URL + "/index.html"
	profile := "it-detach-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	common := []string{"--json", "--page", pageURL, "--name", "it-detach", "--no-kernel"}

	first := startFlow(t, env, append([]string{"--browser", chromeID, "--profile", profile, "--headless"}, common...)...)
	first.next(t, "discovered")
	l := first.next(t, "launched")
	t.Cleanup(func() { closeLeftBrowser(t, l.CDP, l.PID, l.ProfileDir) })
	first.next(t, "attached")
	first.next(t, "linked")
	if reply := rawCtl(t, ctlSocket(t, first.cmd.Process.Pid), ctl.OpDetach); !strings.Contains(reply, `"ok":true`) {
		t.Fatalf("detach: %s", reply)
	}
	if code := first.rest(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lastIsClosed(t, first)
	var closed struct {
		Browsers []string `json:"browsers"`
		Kept     []string `json:"kept"`
	}
	if err := json.Unmarshal([]byte(first.seen[len(first.seen)-1].raw), &closed); err != nil || len(closed.Browsers) != 0 || !slices.Equal(closed.Kept, []string{chromeID}) {
		t.Fatalf("closed %s", first.seen[len(first.seen)-1].raw)
	}
	if !slices.Contains(first.kinds(), "link-lost") {
		t.Fatalf("no bye before closing: %v", first.kinds())
	}
	time.Sleep(2 * time.Second)
	if _, err := getVersion(l.CDP); err != nil {
		t.Fatalf("detach closed the browser: %v", err)
	}

	second := startFlow(t, overrideEnv(env, map[string]string{"SLICC_DISCOVERY": "home"}), append([]string{"--yes"}, common...)...)
	d := second.next(t, "discovered")
	found := false
	for _, e := range d.Entries {
		if e.Endpoint != nil && e.Endpoint.SevenOpen {
			found = true
		}
	}
	if !found {
		t.Fatalf("discovery didn't find the kept browser's seven tab: %s", d.raw)
	}
	if a := second.next(t, "attached"); a.Launched || a.CDP != l.CDP || a.Role != "seven" {
		t.Fatalf("attached %s, want the kept browser at %s", a.raw, l.CDP)
	}
	second.next(t, "linked")
	if reply := rawCtl(t, ctlSocket(t, second.cmd.Process.Pid), ctl.OpStop); !strings.Contains(reply, `"ok":true`) {
		t.Fatalf("stop: %s", reply)
	}
	if code := second.rest(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if c := closedEvent(t, second); len(c["browsers"].([]any)) != 0 || c["kept"] != nil {
		t.Fatalf("closed %v; the second run only attached", c)
	}
	if _, err := getVersion(l.CDP); err != nil {
		t.Fatalf("stop closed a browser the run only attached to: %v", err)
	}
}
