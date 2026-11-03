package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/coder/websocket"
)

const crashSevenURL = "https://seven.sliccy.ai/"

type crashBrowser struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	conns       map[*websocket.Conn]bool
	targets     map[string]bool
	order       []string
	crashed     map[string]bool
	sessions    map[string]string
	sawCrash    map[string]bool
	nextSession int
	nextContext int
	hangBinding int
	keepCrashed bool
	calls       []string
	hellos      []string
	held        []held
}

type held struct {
	c       *websocket.Conn
	id      int64
	session string
}

var helloNonce = regexp.MustCompile(`"nonce":"([^"]+)"`)

func newCrashBrowser(t *testing.T) *crashBrowser {
	t.Helper()
	f := &crashBrowser{t: t, targets: map[string]bool{"T1": true}, order: []string{"T1"}, conns: map[*websocket.Conn]bool{}, crashed: map[string]bool{}, sessions: map[string]string{}, sawCrash: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"Browser": "Crashy/1.0", "Protocol-Version": "1.3", "webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/x"})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/devtools/browser/x", f.serve)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *crashBrowser) serve(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(-1)
	f.mu.Lock()
	f.conns[c] = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.conns, c)
		f.mu.Unlock()
		_ = c.CloseNow()
	}()
	for {
		_, data, err := c.Read(context.Background())
		if err != nil {
			return
		}
		var m struct {
			ID        int64           `json:"id"`
			Method    string          `json:"method"`
			SessionID string          `json:"sessionId"`
			Params    json.RawMessage `json:"params"`
		}
		if json.Unmarshal(data, &m) != nil {
			return
		}
		f.handle(c, m.ID, m.Method, m.SessionID, m.Params)
	}
}

func (f *crashBrowser) send(v any) {
	b, _ := json.Marshal(v)
	f.mu.Lock()
	var conns []*websocket.Conn
	for c := range f.conns {
		conns = append(conns, c)
	}
	f.mu.Unlock()
	for _, c := range conns {
		_ = c.Write(context.Background(), websocket.MessageText, b)
	}
}

func (f *crashBrowser) answer(c *websocket.Conn, v any) {
	b, _ := json.Marshal(v)
	_ = c.Write(context.Background(), websocket.MessageText, b)
}

func (f *crashBrowser) event(session, method string, params any) {
	m := map[string]any{"method": method, "params": params}
	if session != "" {
		m["sessionId"] = session
	}
	f.send(m)
}

func (f *crashBrowser) info(id string) map[string]any {
	return map[string]any{"targetId": id, "type": "page", "title": "seven", "url": crashSevenURL, "attached": true}
}

func (f *crashBrowser) handle(c *websocket.Conn, id int64, method, session string, params json.RawMessage) {
	reply := func(result any) {
		m := map[string]any{"id": id, "result": result}
		if session != "" {
			m["sessionId"] = session
		}
		f.answer(c, m)
	}
	hold := func() {
		f.mu.Lock()
		f.held = append(f.held, held{c: c, id: id, session: session})
		f.mu.Unlock()
	}
	f.mu.Lock()
	f.calls = append(f.calls, method)
	target := f.sessions[session]
	crashed := target != "" && f.crashed[target]
	f.mu.Unlock()
	switch method {
	case "Target.getTargets":
		f.mu.Lock()
		var infos []any
		for _, t := range f.order {
			if f.targets[t] {
				infos = append(infos, f.info(t))
			}
		}
		f.mu.Unlock()
		reply(map[string]any{"targetInfos": infos})
	case "Target.attachToTarget":
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(params, &p)
		f.mu.Lock()
		f.nextSession++
		sid := "S" + strconv.Itoa(f.nextSession)
		f.sessions[sid] = p.TargetID
		f.mu.Unlock()
		reply(map[string]any{"sessionId": sid})
	case "Inspector.enable":
		if crashed {
			f.mu.Lock()
			f.sawCrash[session] = true
			f.mu.Unlock()
			f.event(session, "Inspector.targetCrashed", map[string]any{})
		}
		reply(map[string]any{})
	case "Runtime.addBinding":
		f.mu.Lock()
		hang := f.hangBinding > 0 && !crashed
		if hang {
			f.hangBinding--
		}
		f.mu.Unlock()
		if crashed || hang {
			hold()
			return
		}
		reply(map[string]any{})
	case "Runtime.enable":
		if crashed {
			hold()
			return
		}
		f.mu.Lock()
		f.nextContext++
		ctxID := f.nextContext
		f.mu.Unlock()
		f.event(session, "Runtime.executionContextCreated", map[string]any{"context": map[string]any{
			"id": ctxID, "origin": strings.TrimSuffix(crashSevenURL, "/"), "name": "",
			"auxData": map[string]any{"isDefault": true, "frameId": target},
		}})
		reply(map[string]any{})
	case "Runtime.evaluate":
		if crashed {
			hold()
			return
		}
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if strings.Contains(p.Expression, `"t":"hello"`) {
			if m := helloNonce.FindStringSubmatch(p.Expression); m != nil {
				f.mu.Lock()
				f.hellos = append(f.hellos, session+" "+m[1])
				f.mu.Unlock()
			}
			reply(map[string]any{"result": map[string]any{"type": "string", "value": "ok"}})
			return
		}
		reply(map[string]any{"result": map[string]any{"type": "undefined"}})
	case "Page.reload":
		f.mu.Lock()
		keep := f.keepCrashed
		f.mu.Unlock()
		if crashed && keep {
			hold()
			return
		}
		f.revive(target)
		reply(map[string]any{})
	default:
		reply(map[string]any{})
	}
}

func (f *crashBrowser) crash() {
	target := "T1"
	f.mu.Lock()
	f.crashed[target] = true
	var sessions []string
	for s, t := range f.sessions {
		if t == target {
			sessions = append(sessions, s)
			f.sawCrash[s] = true
		}
	}
	f.mu.Unlock()
	f.event("", "Target.targetCrashed", map[string]any{"targetId": target, "status": "crashed", "errorCode": 133})
	for _, s := range sessions {
		f.event(s, "Inspector.targetCrashed", map[string]any{})
	}
}

func (f *crashBrowser) revive(target string) {
	f.mu.Lock()
	if !f.crashed[target] {
		f.mu.Unlock()
		return
	}
	delete(f.crashed, target)
	var sessions []string
	for s, t := range f.sessions {
		if t == target && f.sawCrash[s] {
			sessions = append(sessions, s)
			delete(f.sawCrash, s)
		}
	}
	var stale []held
	keep := f.held[:0]
	for _, h := range f.held {
		if f.sessions[h.session] == target {
			stale = append(stale, h)
		} else {
			keep = append(keep, h)
		}
	}
	f.held = keep
	f.mu.Unlock()
	for _, h := range stale {
		f.answer(h.c, map[string]any{"id": h.id, "sessionId": h.session, "error": map[string]any{"code": -32000, "message": "Target crashed"}})
	}
	for _, s := range sessions {
		f.event(s, "Inspector.targetReloadedAfterCrash", map[string]any{})
	}
	f.event("", "Target.targetInfoChanged", map[string]any{"targetInfo": f.info(target)})
}

func (f *crashBrowser) destroy(target string) {
	f.mu.Lock()
	delete(f.targets, target)
	var sessions []string
	for s, t := range f.sessions {
		if t == target {
			sessions = append(sessions, s)
		}
	}
	f.mu.Unlock()
	for _, s := range sessions {
		f.event("", "Target.detachedFromTarget", map[string]any{"sessionId": s, "targetId": target})
	}
	f.event("", "Target.targetDestroyed", map[string]any{"targetId": target})
}

func (f *crashBrowser) open(target string) {
	f.mu.Lock()
	f.targets[target] = true
	f.order = append(f.order, target)
	f.mu.Unlock()
	f.event("", "Target.targetCreated", map[string]any{"targetInfo": f.info(target)})
}

func (f *crashBrowser) drop() {
	f.mu.Lock()
	var conns []*websocket.Conn
	for c := range f.conns {
		conns = append(conns, c)
	}
	f.mu.Unlock()
	for _, c := range conns {
		_ = c.CloseNow()
	}
}

func (f *crashBrowser) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.calls {
		if m == method {
			n++
		}
	}
	return n
}

func (f *crashBrowser) waitHello(t *testing.T, n int) (string, string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		f.mu.Lock()
		if len(f.hellos) >= n {
			parts := strings.SplitN(f.hellos[n-1], " ", 2)
			f.mu.Unlock()
			return parts[0], parts[1]
		}
		f.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("no hello #%d; calls: %v", n, f.callList())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *crashBrowser) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *crashBrowser) offer(session, nonce string, ctxID int) {
	payload, _ := json.Marshal(map[string]any{"t": "offer", "nonce": nonce, "sdp": "v=0"})
	f.event(session, "Runtime.bindingCalled", map[string]any{"name": signal.BindingName, "payload": string(payload), "executionContextId": ctxID})
}

func (f *crashBrowser) lastContext() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextContext
}

type statusLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *statusLog) add(format string, a ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, sprintf(format, a...))
	l.mu.Unlock()
}

func (l *statusLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func (l *statusLog) has(want string) int {
	n := 0
	for _, line := range l.all() {
		if strings.HasPrefix(line, want) {
			n++
		}
	}
	return n
}

func (l *statusLog) wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for l.has(want) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("status %q never seen: %q", want, l.all())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type crashSignal struct {
	f      *crashBrowser
	sig    *signal.CDP
	status *statusLog
	msgs   chan signal.Message
	ended  chan error
}

func startCrashSignal(t *testing.T, f *crashBrowser, opts signal.CDPOptions) *crashSignal {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := cdp.Dial(ctx, f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	origins, err := signal.NewOrigins(nil)
	if err != nil {
		t.Fatal(err)
	}
	cs := &crashSignal{f: f, status: &statusLog{}, msgs: make(chan signal.Message, 64), ended: make(chan error, 1)}
	opts.Client, opts.Origins, opts.Status = client, origins, cs.status.add
	cs.sig = signal.NewCDP(opts)
	go func() {
		for {
			m, err := cs.sig.Recv(context.Background())
			if err != nil {
				cs.ended <- err
				return
			}
			cs.msgs <- m
		}
	}()
	t.Cleanup(func() {
		_ = cs.sig.Close()
		_ = client.Close()
	})
	return cs
}

func quickRetries() signal.CDPOptions {
	return signal.CDPOptions{SetupTimeout: 300 * time.Millisecond, RetryMin: 20 * time.Millisecond, RetryMax: 100 * time.Millisecond, ReloadTimeout: 300 * time.Millisecond}
}

func (cs *crashSignal) next(t *testing.T, kind string) signal.Message {
	t.Helper()
	timeout := time.After(20 * time.Second)
	for {
		select {
		case m := <-cs.msgs:
			if m.T == kind {
				return m
			}
		case err := <-cs.ended:
			t.Fatalf("the signal ended with %v while waiting for %s; status %q", err, kind, cs.status.all())
		case <-timeout:
			t.Fatalf("no %s; status %q", kind, cs.status.all())
		}
	}
}

func (cs *crashSignal) link(t *testing.T, n int) string {
	t.Helper()
	session, nonce := cs.f.waitHello(t, n)
	cs.f.offer(session, nonce, cs.f.lastContext())
	if m := cs.next(t, signal.TypeOffer); m.Nonce != nonce {
		t.Fatalf("offer nonce %q, want %q", m.Nonce, nonce)
	}
	return nonce
}

func (cs *crashSignal) stillUp(t *testing.T) {
	t.Helper()
	select {
	case err := <-cs.ended:
		t.Fatalf("the signal ended: %v; status %q", err, cs.status.all())
	default:
	}
}

func TestACrashedSevenTabIsReloadedAndLinkedAgain(t *testing.T) {
	f := newCrashBrowser(t)
	cs := startCrashSignal(t, f, quickRetries())
	first := cs.link(t, 1)
	bindings := f.count("Runtime.addBinding")
	f.crash()
	if bye := cs.next(t, signal.TypeBye); bye.Nonce != first || bye.Reason != "seven tab crashed" {
		t.Fatalf("bye %+v", bye)
	}
	cs.status.wait(t, "seven tab crashed")
	cs.status.wait(t, "reloading the seven tab")
	second := cs.link(t, 2)
	if second == first {
		t.Fatal("the same nonce after the reload")
	}
	if n := f.count("Page.reload"); n != 1 {
		t.Fatalf("%d reloads, want 1", n)
	}
	if n := f.count("Runtime.addBinding") - bindings; n != 1 {
		t.Fatalf("%d addBinding calls after the crash, want 1 on the reloaded tab: %v", n, f.callList())
	}
	if n := cs.status.has("seven tab closed"); n != 0 {
		t.Fatalf("a crash reported as a close: %q", cs.status.all())
	}
	if n := cs.status.has("found seven at"); n != 2 {
		t.Fatalf("found seven %d times: %q", n, cs.status.all())
	}
	cs.stillUp(t)
}

func TestASevenTabThatKeepsCrashingWaitsForTheUser(t *testing.T) {
	f := newCrashBrowser(t)
	f.keepCrashed = true
	cs := startCrashSignal(t, f, quickRetries())
	cs.link(t, 1)
	f.crash()
	cs.status.wait(t, signal.KeepsCrashing)
	if n := f.count("Page.reload"); n != signal.CrashReloads {
		t.Fatalf("%d reloads, want %d", n, signal.CrashReloads)
	}
	time.Sleep(500 * time.Millisecond)
	if n := f.count("Page.reload"); n != signal.CrashReloads {
		t.Fatalf("reloaded again after giving up: %d", n)
	}
	cs.stillUp(t)
	f.revive("T1")
	cs.link(t, 2)
	cs.stillUp(t)
}

func TestASevenTabThatKeepsCrashingGivesWayToANewOne(t *testing.T) {
	f := newCrashBrowser(t)
	f.keepCrashed = true
	cs := startCrashSignal(t, f, quickRetries())
	cs.link(t, 1)
	f.crash()
	cs.status.wait(t, signal.KeepsCrashing)
	f.open("T2")
	session, _ := f.waitHello(t, 2)
	f.mu.Lock()
	target := f.sessions[session]
	f.mu.Unlock()
	if target != "T2" {
		t.Fatalf("linked %s, want the new tab T2", target)
	}
	cs.stillUp(t)
}

func TestASetupThatTimesOutOnALiveTabIsTriedAgain(t *testing.T) {
	f := newCrashBrowser(t)
	f.hangBinding = 2
	cs := startCrashSignal(t, f, quickRetries())
	cs.link(t, 1)
	if n := cs.status.has("couldn't link the seven tab (context deadline exceeded), trying again in"); n != 2 {
		t.Fatalf("%d retries reported: %q", n, cs.status.all())
	}
	cs.stillUp(t)
}

func TestAClosedSevenTabStillEndsTheLinkAndWaitsForAnother(t *testing.T) {
	f := newCrashBrowser(t)
	cs := startCrashSignal(t, f, quickRetries())
	first := cs.link(t, 1)
	f.destroy("T1")
	if bye := cs.next(t, signal.TypeBye); bye.Nonce != first || bye.Reason != "seven tab closed" {
		t.Fatalf("bye %+v", bye)
	}
	cs.status.wait(t, "seven tab closed")
	cs.status.wait(t, "waiting for a seven tab")
	if n := f.count("Page.reload"); n != 0 {
		t.Fatalf("a closed tab was reloaded %d times", n)
	}
	f.open("T2")
	cs.link(t, 2)
	if n := cs.status.has("seven tab crashed"); n != 0 {
		t.Fatalf("a close reported as a crash: %q", cs.status.all())
	}
}

func TestALostCDPConnectionEndsTheSignal(t *testing.T) {
	f := newCrashBrowser(t)
	cs := startCrashSignal(t, f, quickRetries())
	cs.link(t, 1)
	f.drop()
	select {
	case err := <-cs.ended:
		if err == nil || errors.Is(err, signal.ErrClosed) {
			t.Fatalf("ended with %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("the signal outlived its CDP connection; status %q", cs.status.all())
	}
}

func TestALostCDPConnectionEndsTheSignalWhileACrashedTabWaits(t *testing.T) {
	f := newCrashBrowser(t)
	f.keepCrashed = true
	cs := startCrashSignal(t, f, quickRetries())
	cs.link(t, 1)
	f.crash()
	cs.status.wait(t, signal.KeepsCrashing)
	f.drop()
	select {
	case err := <-cs.ended:
		if err == nil {
			t.Fatal("ended without an error")
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("the signal outlived its CDP connection; status %q", cs.status.all())
	}
}

func startCrashBinary(t *testing.T, args ...string) (*syncBuffer, <-chan error) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = isolatedEnv(t)
	logs := &syncBuffer{}
	cmd.Stderr = logs
	cmd.Stdout = logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
		if t.Failed() {
			t.Logf("slicc output:\n%s", logs.String())
		}
	})
	return logs, exited
}

func outlivesACrash(t *testing.T, f *crashBrowser, logs *syncBuffer, exited <-chan error) {
	t.Helper()
	f.waitHello(t, 1)
	f.crash()
	f.waitHello(t, 2)
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-exited:
		t.Fatalf("slicc exited after the crash: %v\n%s", err, logs.String())
	default:
	}
	out := logs.String()
	for _, want := range []string{"seven tab crashed\n", "reloading the seven tab\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "found seven at https://seven.sliccy.ai\n"); n != 2 {
		t.Fatalf("found seven %d times:\n%s", n, out)
	}
	if strings.Contains(out, "seven tab closed") {
		t.Fatalf("a crash reported as a close:\n%s", out)
	}
}

func TestAttachOutlivesACrashedSevenTab(t *testing.T) {
	f := newCrashBrowser(t)
	logs, exited := startCrashBinary(t, "attach", "--no-kernel", "--no-jerk", "--cdp", f.srv.URL)
	outlivesACrash(t, f, logs, exited)
}

func TestTheDefaultFlowOutlivesACrashedSevenTab(t *testing.T) {
	f := newCrashBrowser(t)
	id := "cdp:" + strings.TrimPrefix(f.srv.URL, "http://")
	logs, exited := startCrashBinary(t, "--cdp", f.srv.URL, "--browser", id, "--plain", "--yes", "--no-kernel", "--no-jerk")
	outlivesACrash(t, f, logs, exited)
}

func TestTheLinkComesBackAfterTheSevenTabCrashes(t *testing.T) {
	env := setupLink(t, false)
	var st struct {
		Nonce string `json:"nonce"`
	}
	env.p.eval("linkTest.status()", &st)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = env.p.session.Call(ctx, "Page.crash", nil, nil)
	cancel()
	env.a.waitStderr(t, "slicc: seven tab crashed\n", 15*time.Second)
	env.a.waitStderr(t, "slicc: reloading the seven tab\n", 15*time.Second)
	env.p.waitFor("typeof globalThis.linkTest === 'object' && document.readyState === 'complete'", 30*time.Second)
	if nonce := env.waitUp(st.Nonce); nonce == st.Nonce || nonce == "" {
		t.Fatalf("nonce %q after the crash, was %q", nonce, st.Nonce)
	}
	if r := env.echoTest(300000, false); !r.OK {
		t.Fatalf("after the crash: %+v", r)
	}
	if out := env.a.stderr.String(); strings.Contains(out, "seven tab closed") {
		t.Fatalf("a crash reported as a close:\n%s", out)
	}
}
