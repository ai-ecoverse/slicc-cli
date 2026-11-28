//go:build !windows

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var leaseBLBase = time.Now()

func leaseBLAt(t time.Time) string {
	if t.IsZero() {
		return "none"
	}
	return fmt.Sprintf("%.1f", float64(t.Sub(leaseBLBase).Microseconds())/1000)
}

func leaseBLSince(from, to time.Time) string {
	if from.IsZero() || to.IsZero() {
		return "none"
	}
	return fmt.Sprintf("%.1f", float64(to.Sub(from).Microseconds())/1000)
}

func leaseBLLine(t *testing.T, row string, kv ...any) {
	t.Helper()
	var b strings.Builder
	b.WriteString(leaseBaselinePrefix)
	b.WriteString(row)
	for i := 0; i+1 < len(kv); i += 2 {
		v := fmt.Sprint(kv[i+1])
		if v == "" {
			v = "empty"
		}
		fmt.Fprintf(&b, " %v=%s", kv[i], strings.Join(strings.Fields(v), "_"))
	}
	t.Log(b.String())
}

type leaseBLGate struct {
	ch   chan struct{}
	once sync.Once
}

type leaseBLSite struct {
	srv    *httptest.Server
	spki   string
	mu     sync.Mutex
	events map[string]time.Time
	gates  map[string]*leaseBLGate
	docs   int
}

func newLeaseBLSite(t *testing.T) *leaseBLSite {
	t.Helper()
	s := &leaseBLSite{events: map[string]time.Time{}, gates: map[string]*leaseBLGate{}}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	d := sha256.Sum256(s.srv.Certificate().RawSubjectPublicKeyInfo)
	s.spki = base64.StdEncoding.EncodeToString(d[:])
	return s
}

func (s *leaseBLSite) note(k string) {
	s.mu.Lock()
	if _, ok := s.events[k]; !ok {
		s.events[k] = time.Now()
	}
	s.mu.Unlock()
}

func (s *leaseBLSite) at(k string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.events[k]
}

func (s *leaseBLSite) wait(k string, d time.Duration) time.Time {
	for end := time.Now().Add(d); ; time.Sleep(5 * time.Millisecond) {
		if at := s.at(k); !at.IsZero() || time.Now().After(end) {
			return at
		}
	}
}

func (s *leaseBLSite) gate(n string) *leaseBLGate {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.gates[n]
	if g == nil {
		g = &leaseBLGate{ch: make(chan struct{})}
		s.gates[n] = g
	}
	return g
}

func (s *leaseBLSite) open(n string) { g := s.gate(n); g.once.Do(func() { close(g.ch) }) }

func (s *leaseBLSite) documents() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs
}

func (s *leaseBLSite) serve(w http.ResponseWriter, r *http.Request) {
	n := r.URL.Query().Get("n")
	if r.Header.Get("Sec-Fetch-Dest") == "document" {
		s.mu.Lock()
		s.docs++
		s.mu.Unlock()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "/fast":
		fmt.Fprintf(w, "<h1>Baseline fast %s</h1>", n)
	case "/slow":
		s.note("arrived:" + n)
		select {
		case <-s.gate(n).ch:
			s.note("released:" + n)
			fmt.Fprintf(w, "<h1>Baseline slow %s</h1>", n)
		case <-r.Context().Done():
			s.note("aborted:" + n)
		}
	case "/burst":
		ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
		fmt.Fprintf(w, `<h1>Baseline burst %s</h1><script>const stop=performance.now()+15000;const step=()=>{const e=performance.now()+%d;while(performance.now()<e);if(performance.now()<stop)setTimeout(step,0)};setTimeout(step,300)</script>`, n, ms)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

type leaseBLEnv struct {
	t      *testing.T
	site   *leaseBLSite
	owner  *chromeProc
	holder *chromeProc
	p      *page
	h      *fakeHub
	trays  []*fakeTray
	leader *trayLeader
	env    []string
	jkt    string
	follow *exec.Cmd
	log    *syncBuffer
	done   chan struct{}
}

func newLeaseBLEnv(t *testing.T, trays int) *leaseBLEnv {
	t.Helper()
	e := &leaseBLEnv{t: t, site: newLeaseBLSite(t)}
	e.owner = launchBrowserOn(t, 0, "--ignore-certificate-errors-spki-list="+e.site.spki, "--no-proxy-server")
	e.holder, e.p = launchWithPage(t, pageServer(t).URL+"/index.html?lease")
	e.h = newFakeHub(t)
	for i := 0; i < trays; i++ {
		e.trays = append(e.trays, e.h.addTray("baseline"))
	}
	e.leader = newTrayLeader(t, e.h, e.p, e.trays...)
	dir, err := os.MkdirTemp("/tmp", "slicc-lbl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMPDIR", dir)
	t.Setenv("XDG_RUNTIME_DIR", dir)
	e.env = append(configEnv(t), "SLICC_NO_TELEMETRY=1", "SLICC_NO_UPDATE_CHECK=1")
	var pin string
	e.p.eval("leaseFingerprint()", &pin)
	e.p.eval("leaseTest.init()", &e.jkt)
	e.p.eval("otherHolder.init()", nil)
	e.cli("pair", "--link", "fixture", "--peer", pin)
	e.start("")
	return e
}

func (e *leaseBLEnv) join() string { return e.h.joinURL(e.trays[0]) }

func (e *leaseBLEnv) start(notNonce string) {
	e.t.Helper()
	cmd := exec.Command(binary, "-", "follow", "--json", "--lease-browser", e.owner.endpoint, "--name", "fixture")
	cmd.Env = e.env
	cmd.Stdin = strings.NewReader(e.join() + "\n")
	log := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	e.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	e.follow, e.log, e.done = cmd, log, done
	(&attachProc{cmd: cmd, stderr: log}).waitStderr(e.t, "linked to", 30*time.Second)
	e.p.waitUp(e.t, notNonce)
}

func (e *leaseBLEnv) cli(args ...string) {
	e.t.Helper()
	cmd := exec.Command(binary, append([]string{"lease"}, args...)...)
	cmd.Env = e.env
	b, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("lease %s: %v: %s", args[0], err, b)
	}
}

func (e *leaseBLEnv) status() string {
	cmd := exec.Command(binary, "lease", "status")
	cmd.Env = e.env
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "error"
	}
	pend, act, routes := 0, 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var s struct {
			Pending []json.RawMessage `json:"pending"`
			Active  []json.RawMessage `json:"active"`
		}
		if json.Unmarshal([]byte(line), &s) == nil {
			routes++
			pend += len(s.Pending)
			act += len(s.Active)
		}
	}
	return fmt.Sprintf("routes:%d,pending:%d,active:%d", routes, pend, act)
}

func (e *leaseBLEnv) request(holder string, ttl int) string {
	e.t.Helper()
	var req struct{ Type, Request string }
	e.p.eval(holder+".request("+jsArgs([]any{map[string]string{"kind": "origin", "url": e.site.srv.URL}}, map[string]int{"ttlSeconds": ttl})+")", &req)
	if req.Type != "pending" || req.Request == "" {
		e.t.Fatalf("not pending: %v", req)
	}
	return req.Request
}

func (e *leaseBLEnv) grant(ttl int) string {
	e.t.Helper()
	reqID := e.request("leaseTest", ttl)
	e.cli("grant", "--no-input", "--to", "jkt:"+e.jkt, "--allow", e.site.srv.URL, "--for", strconv.Itoa(ttl)+"s", "--browser", e.owner.endpoint, "--link", "fixture")
	if reqID == "" {
		e.t.Fatal("empty request id")
	}
	var id, ready string
	e.p.eval("leaseTest.grant()", &id)
	e.p.eval("leaseTest.connect()", &ready)
	if ready != "ready" {
		e.t.Fatalf("connect %q", ready)
	}
	return id
}

func (e *leaseBLEnv) action(op string, args any) map[string]any {
	e.t.Helper()
	var reply map[string]any
	e.p.eval("leaseTest.action("+jsArgs(op, args)+")", &reply)
	return reply
}

func (e *leaseBLEnv) dispatch(op string, args any) {
	e.t.Helper()
	e.p.eval("leaseTest.dispatch("+jsArgs(op, args)+")", nil)
}

func (e *leaseBLEnv) connect(holder string) string {
	e.t.Helper()
	var ready string
	e.p.eval(holder+".connect()", &ready)
	return ready
}

func (e *leaseBLEnv) now() float64 {
	e.t.Helper()
	var v float64
	e.p.eval("leaseTest.now()", &v)
	return v
}

type leaseBLFrame struct {
	T    float64 `json:"t"`
	W    float64 `json:"w"`
	Type int     `json:"type"`
	Text string  `json:"text"`
}

func (e *leaseBLEnv) frames() []leaseBLFrame {
	e.t.Helper()
	var f []leaseBLFrame
	e.p.eval("leaseTest.seen()", &f)
	return f
}

func leaseBLFrames(fs []leaseBLFrame, after float64) (results, failures int, reset string, resetT, resetW float64) {
	reset = "none"
	for _, f := range fs {
		if f.T < after {
			continue
		}
		switch f.Type {
		case 3:
			if strings.Contains(f.Text, `"type":"result"`) {
				results++
			} else if strings.Contains(f.Text, `"type":"error"`) {
				failures++
			}
		case 5:
			if reset == "none" {
				reset, resetT, resetW = f.Text, f.T, f.W
			}
		}
	}
	return
}

func (e *leaseBLEnv) pageTargets() map[string]string {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts, err := e.owner.client.Targets(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]string{}
	for _, ti := range ts {
		if ti.Type == "page" && strings.HasPrefix(ti.URL, e.site.srv.URL) {
			out[ti.TargetID] = strings.TrimPrefix(ti.URL, e.site.srv.URL)
		}
	}
	return out
}

func (e *leaseBLEnv) leasedTab(part string) string {
	for id, u := range e.pageTargets() {
		if strings.Contains(u, part) {
			return id
		}
	}
	return ""
}

func (e *leaseBLEnv) tabGone(id string, d time.Duration) time.Time {
	for end := time.Now().Add(d); ; time.Sleep(10 * time.Millisecond) {
		if _, ok := e.pageTargets()[id]; !ok {
			return time.Now()
		}
		if time.Now().After(end) {
			return time.Time{}
		}
	}
}

func (e *leaseBLEnv) stopHolder() {
	e.t.Helper()
	if err := syscall.Kill(-e.holder.cmd.Process.Pid, syscall.SIGSTOP); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = syscall.Kill(-e.holder.cmd.Process.Pid, syscall.SIGCONT) })
}

func (e *leaseBLEnv) contHolder() {
	_ = syscall.Kill(-e.holder.cmd.Process.Pid, syscall.SIGCONT)
}

func (e *leaseBLEnv) waitLog(want string, from int, d time.Duration) time.Time {
	for end := time.Now().Add(d); ; time.Sleep(20 * time.Millisecond) {
		s := e.log.String()
		if len(s) > from && strings.Contains(s[from:], want) {
			return time.Now()
		}
		if time.Now().After(end) {
			return time.Time{}
		}
	}
}
