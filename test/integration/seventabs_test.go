package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

type readyTabs struct {
	mu  sync.Mutex
	ids map[string]bool
}

func (r *readyTabs) has(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ids[id]
}

func (r *readyTabs) set(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ids == nil {
		r.ids = map[string]bool{}
	}
	r.ids[id] = true
}

type waitLog struct {
	mu  sync.Mutex
	got []signal.Waiting
}

func (l *waitLog) add(w signal.Waiting) {
	l.mu.Lock()
	l.got = append(l.got, w)
	l.mu.Unlock()
}

func (l *waitLog) all() []signal.Waiting {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]signal.Waiting(nil), l.got...)
}

func (l *waitLog) wait(t *testing.T, reason string, candidates int) signal.Waiting {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		for _, w := range l.all() {
			if w.Reason == reason && w.Candidates == candidates {
				return w
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no waiting %s with %d candidates: %+v", reason, candidates, l.all())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *crashBrowser) setReady(ready func(string) bool) {
	f.mu.Lock()
	f.ready = ready
	f.mu.Unlock()
}

func (f *crashBrowser) targetOf(session string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[session]
}

func (f *crashBrowser) helloCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hellos)
}

func TestTheSevenTabThatIsReadyIsLinkedOverAnEmptyOne(t *testing.T) {
	f := newCrashBrowser(t)
	f.open("T2")
	ready := &readyTabs{}
	ready.set("T2")
	f.setReady(ready.has)
	cs := startCrashSignal(t, f, quickRetries())
	session, nonce := f.waitHello(t, 1)
	if target := f.targetOf(session); target != "T2" {
		t.Fatalf("hello went to %s, want the ready tab T2; calls %v", target, f.callList())
	}
	if n := f.count("Runtime.addBinding"); n != 1 {
		t.Fatalf("%d addBinding calls, want 1 on the ready tab only: %v", n, f.callList())
	}
	f.offer(session, nonce, f.lastContext())
	if m := cs.next(t, signal.TypeOffer); m.Nonce != nonce {
		t.Fatalf("offer nonce %q, want %q", m.Nonce, nonce)
	}
	cs.stillUp(t)
}

func TestALinkMovesToTheSevenTabThatGetsReady(t *testing.T) {
	f := newCrashBrowser(t)
	ready := &readyTabs{}
	f.setReady(ready.has)
	waits := &waitLog{}
	opts := quickRetries()
	opts.ReadyTimeout = 100 * time.Millisecond
	opts.Waiting = waits.add
	cs := startCrashSignal(t, f, opts)
	w := waits.wait(t, signal.WaitNotReady, 1)
	if w.Message != signal.NotReady(1) {
		t.Fatalf("message %q", w.Message)
	}
	f.open("T2")
	w = waits.wait(t, signal.WaitNotReady, 2)
	if !strings.Contains(w.Message, "2 seven tabs are open") {
		t.Fatalf("message %q", w.Message)
	}
	if n := f.helloCount(); n != 0 {
		t.Fatalf("%d hellos to tabs that aren't ready", n)
	}
	ready.set("T2")
	session, nonce := f.waitHello(t, 1)
	if target := f.targetOf(session); target != "T2" {
		t.Fatalf("hello went to %s, want T2", target)
	}
	cs.status.wait(t, "the seven tab isn't ready to link; moving to another seven tab that is")
	f.offer(session, nonce, f.lastContext())
	if m := cs.next(t, signal.TypeOffer); m.Nonce != nonce {
		t.Fatalf("offer nonce %q, want %q", m.Nonce, nonce)
	}
	n := len(waits.all())
	time.Sleep(300 * time.Millisecond)
	if got := len(waits.all()); got != n {
		t.Fatalf("still waiting after the hello: %+v", waits.all())
	}
	cs.stillUp(t)
}

func TestNoSevenTabAtAllSaysWhyItWaits(t *testing.T) {
	t.Setenv(signal.QuietEnv, "100ms")
	f := newCrashBrowser(t)
	f.targets, f.order = map[string]bool{}, nil
	waits := &waitLog{}
	opts := quickRetries()
	opts.Waiting = waits.add
	cs := startCrashSignal(t, f, opts)
	cs.status.wait(t, "waiting for a seven tab")
	if w := waits.wait(t, signal.WaitNoTab, 0); w.Message != signal.NoSevenTab {
		t.Fatalf("message %q", w.Message)
	}
	f.open("T3")
	cs.link(t, 1)
}

type waitingLine struct {
	Schema     int    `json:"schema"`
	Event      string `json:"event"`
	Mode       string `json:"mode"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	Candidates *int   `json:"candidates"`
}

func waitingFrom(t *testing.T, env []string, args ...string) waitingLine {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("slicc stderr:\n%s", logs.String())
		}
	})
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stdout closed before a waiting event")
			}
			var w waitingLine
			if json.Unmarshal([]byte(line), &w) != nil {
				t.Fatalf("not JSON on stdout: %q", line)
			}
			if w.Event == string(flow.Waiting) {
				return w
			}
		case <-deadline:
			t.Fatal("no waiting event")
		}
	}
}

func TestJSONSaysWhySliccWaitsForSeven(t *testing.T) {
	for _, mode := range []string{"attach", "default"} {
		t.Run(mode, func(t *testing.T) {
			f := newCrashBrowser(t)
			f.open("T2")
			f.setReady((&readyTabs{}).has)
			env := isolatedEnv(t, signal.ReadyEnv+"=100ms")
			args := []string{"attach", "--json", "--no-kernel", "--no-jerk", "--cdp", f.srv.URL}
			if mode == "default" {
				args = []string{"--json", "--yes", "--no-kernel", "--no-jerk", "--cdp", f.srv.URL, "--browser", "cdp:" + strings.TrimPrefix(f.srv.URL, "http://")}
			}
			w := waitingFrom(t, env, args...)
			if w.Schema != 1 || w.Reason != signal.WaitNotReady || w.Candidates == nil || *w.Candidates != 2 || w.Message != signal.NotReady(2) {
				t.Fatalf("waiting %+v", w)
			}
			if want := map[string]string{"attach": "cdp", "default": ""}[mode]; w.Mode != want {
				t.Fatalf("mode %q, want %q", w.Mode, want)
			}
		})
	}
}

func TestALaunchedBrowserKeepsOneSevenTab(t *testing.T) {
	env, _ := launchEnv(t)
	env, id := testChrome(t, env)
	env = overrideEnv(env, map[string]string{signal.ReadyEnv: "300ms"})
	profile := "it-onetab-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	srv := pageServer(t)
	pageURL := srv.URL + "/?single&late=3000"
	f := startFlow(t, env, "--json", "--browser", id, "--profile", profile, "--headless", "--page", pageURL, "--no-kernel", "--no-jerk")
	f.next(t, "discovered")
	l := f.next(t, "launched")
	t.Cleanup(func() { _ = os.RemoveAll(l.ProfileDir) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := cdp.Dial(ctx, l.CDP)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Call(ctx, "", "Target.createTarget", map[string]any{"url": pageURL}, nil); err != nil {
		t.Fatal(err)
	}
	f.next(t, "attached")
	for linked := false; !linked; {
		select {
		case e, ok := <-f.events:
			if !ok {
				t.Fatalf("stdout closed before linked; events %v", f.kinds())
			}
			f.seen = append(f.seen, e)
			switch e.Event {
			case "linked":
				linked = true
			case "waiting":
				if !strings.Contains(e.raw, `"reason":"`+signal.WaitNotReady+`"`) {
					t.Fatalf("waiting %s", e.raw)
				}
			default:
				t.Fatalf("event %s before linked", e.raw)
			}
		case <-time.After(90 * time.Second):
			t.Fatalf("no linked; events %v", f.kinds())
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for countTabs(t, client, pageURL) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("%d seven tabs after the link, want 1", countTabs(t, client, pageURL))
		}
		time.Sleep(100 * time.Millisecond)
	}
	p := dialPage(t, l.CDP, pageURL)
	echoOverLink(t, p)
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestSixWithABareOriginPageOpensSevensPath(t *testing.T) {
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP www.sliccy.ai ~NOTFOUND, MAP seven.sliccy.ai ~NOTFOUND")
	v, err := getVersion(b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := url.Parse(fmt.Sprint(v["webSocketDebuggerUrl"]))
	h := newFakeHome(t)
	dir := filepath.Join(sixRootIn(h), "browser-coding-agent-chrome")
	makeSixTree(t, dir, fmt.Sprintf("%d\n%s", b.port, ws.Path))
	holdSixLock(t, dir, b.cmd.Process.Pid)
	root := "https://www.sliccy.ai/"
	openTabAt(t, b, root)
	f := startFlow(t, sixEnv(h, "isolated"), "--json", "--profile", "six", "--page", "https://www.sliccy.ai", "--no-kernel")
	f.next(t, "discovered")
	f.next(t, "attached")
	if fb := f.next(t, "foreign-browser"); !strings.Contains(fb.raw, `"openedTab":true`) {
		t.Fatalf("foreign-browser %s", fb.raw)
	}
	deadline := time.Now().Add(10 * time.Second)
	for countTabs(t, b.client, flow.SixPage) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no tab on %s", flow.SixPage)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := countTabs(t, b.client, root); n != 1 {
		t.Fatalf("%d tabs on %s, want the one there before", n, root)
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}
