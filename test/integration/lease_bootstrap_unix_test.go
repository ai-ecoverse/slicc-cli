//go:build !windows

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/lease"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

const coldOrigin = "https://billing.example"

var preDialPin = regexp.MustCompile(`Broker SHA-256 fingerprint for (\S+): ([0-9a-f]{64})\n`)

type coldFixture struct {
	env      []string
	hub      *fakeHub
	conns    *atomic.Int64
	tray     *fakeTray
	endpoint string
	page     *page
	seven    string
	jkt      string
	certs    string
}

func countedHub(t *testing.T) (*fakeHub, *atomic.Int64) {
	t.Helper()
	h := &fakeHub{t: t, trays: map[string]*fakeTray{}}
	n := &atomic.Int64{}
	h.srv = httptest.NewUnstartedServer(http.HandlerFunc(h.serve))
	h.srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			n.Add(1)
		}
	}
	h.srv.Start()
	t.Cleanup(h.srv.Close)
	return h, n
}

func holdCDPStartup(t *testing.T) (endpoint string, dialed <-chan struct{}, release func()) {
	t.Helper()
	ready := make(chan struct{})
	var dialOnce sync.Once
	stop := make(chan struct{})
	var stopOnce sync.Once
	release = func() {
		stopOnce.Do(func() { close(stop) })
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/0.0.0.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/hold",
		})
	})
	mux.HandleFunc("/devtools/browser/hold", func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack", http.StatusInternalServerError)
			return
		}
		c, _, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		select {
		case <-stop:
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			dialOnce.Do(func() { close(ready) })
		}
	}
	srv.Start()
	t.Cleanup(func() {
		release()
		srv.Close()
	})
	return srv.URL, ready, release
}

func newColdFixture(t *testing.T, chrome bool) *coldFixture {
	t.Helper()
	fx := &coldFixture{seven: strings.Repeat("5e", 32), endpoint: "http://127.0.0.1:9"}
	fx.hub, fx.conns = countedHub(t)
	fx.tray = fx.hub.addTray("first")
	if chrome {
		fx.endpoint = launchBrowser(t).endpoint
		_, fx.page = launchWithPage(t, pageServer(t).URL+"/index.html?lease")
		newTrayLeader(t, fx.hub, fx.page, fx.tray)
		fx.page.eval("leaseFingerprint()", &fx.seven)
		fx.page.eval("leaseTest.init()", &fx.jkt)
	}
	dir, err := os.MkdirTemp("/tmp", "sl-boot-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMPDIR", dir)
	t.Setenv("XDG_RUNTIME_DIR", dir)
	fx.env = configEnv(t)
	base, err := tray.DefaultCertsPath()
	if err != nil {
		t.Fatal(err)
	}
	fx.certs = base + ".lease"
	if len(coldEntries(t)) != 0 {
		t.Fatal("expected a fresh owner control directory")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		rows, err := ctl.ListLocal(ctx)
		if err != nil {
			t.Error(err)
		}
		for _, row := range rows {
			if row.Status != nil && row.Status.PID == row.PID {
				_, _ = ctl.Send(ctx, row.Path, ctl.OpStop)
			}
		}
	})
	return fx
}

func coldEntries(t *testing.T) []ctl.Entry {
	t.Helper()
	rows, err := ctl.ListLocal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (fx *coldFixture) review(t *testing.T) *ttyProc {
	t.Helper()
	jkt := fx.jkt
	if jkt == "" {
		jkt = strings.Repeat("A", 43)
	}
	return startTTY(t, fx.env, "lease", "review", "--to", "jkt:"+jkt, "--allow", coldOrigin, "--for", "5m")
}

func (fx *coldFixture) toPin(t *testing.T, p *ttyProc, hub string) (string, string) {
	t.Helper()
	p.wait(t, 0, "literal loopback HTTP origin:")
	p.keys(t, fx.endpoint+"\n")
	p.wait(t, 0, "Local link label:")
	p.keys(t, "first-pair\n")
	p.wait(t, 0, "Tray hub origin of the requester's Seven (the hub it joined, https://www.sliccy.ai in production; not the Seven page address):")
	p.keys(t, hub+"\n")
	screen := p.wait(t, 0, "fingerprint from your trusted task conversation")
	m := preDialPin.FindStringSubmatchIndex(screen)
	if m == nil || m[1] > strings.Index(screen, "Expected Seven DTLS fingerprint") || !strings.Contains(screen[m[1]:], "Nothing has been dialed yet.") {
		t.Fatalf("no full broker pin before the Seven pin prompt:\n%s", screen)
	}
	if n := fx.conns.Load(); n != 0 || len(fx.hub.requestLog()) != 0 || len(coldEntries(t)) != 0 {
		t.Fatalf("before the join URL: %d hub connections, %d sockets", n, len(coldEntries(t)))
	}
	info, err := os.Stat(fx.certs)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lease identity store: %v", err)
	}
	return screen[m[2]:m[3]], screen[m[4]:m[5]]
}

func (fx *coldFixture) toJoin(t *testing.T, p *ttyProc, hub string) string {
	t.Helper()
	_, pin := fx.toPin(t, p, hub)
	p.keys(t, fx.seven+"\n")
	p.wait(t, 0, "Paste its join URL here (hidden):")
	return pin
}

func (fx *coldFixture) secretsHidden(t *testing.T, p *ttyProc) {
	t.Helper()
	pemData, _ := os.ReadFile(fx.certs)
	p.mu.Lock()
	raw := p.raw.String()
	p.mu.Unlock()
	for _, s := range []string{raw, p.text()} {
		if strings.Contains(s, fx.tray.Token) || strings.Contains(s, "/join/") || strings.Contains(s, "PRIVATE KEY") || strings.Contains(s, "BEGIN CERTIFICATE") || len(pemData) > 0 && strings.Contains(s, string(pemData)) {
			t.Fatal("the transcript shows the join URL or identity material")
		}
	}
}

func foreignStillAnswers(t *testing.T, path string, pid int) {
	t.Helper()
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		t.Fatalf("the foreign socket no longer accepts connections: %v", err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	body, _ := json.Marshal(ctl.Request{V: ctl.V, Op: ctl.OpStatus})
	if _, err := c.Write(append(body, '\n')); err != nil {
		t.Fatalf("the foreign socket does not take a request: %v", err)
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		t.Fatalf("the foreign socket does not answer: %v", err)
	}
	var st ctl.Status
	if json.Unmarshal(line, &st) != nil || st.PID != pid {
		t.Fatalf("the foreign socket answered %q", line)
	}
}

func holdsClaim(t *testing.T, fx *coldFixture) bool {
	t.Helper()
	matches, _ := filepath.Glob(fx.certs + ".claim.*")
	if len(matches) == 0 {
		return false
	}
	for _, path := range matches {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		}
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return false
}

func (fx *coldFixture) procClean(t *testing.T, pid int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	for _, f := range []string{"cmdline", "environ"} {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/%s", pid, f))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), fx.tray.Token) || strings.Contains(string(b), "/join/") || strings.Contains(string(b), "PRIVATE KEY") {
			t.Fatalf("the broker's %s holds a secret", f)
		}
	}
}

func (fx *coldFixture) toReview(t *testing.T, p *ttyProc) {
	t.Helper()
	pin := fx.toJoin(t, p, fx.hub.srv.URL)
	p.keys(t, fx.hub.joinURL(fx.tray)+"\n")
	p.wait(t, 0, "type REVIEW:")
	rows := coldEntries(t)
	if len(rows) != 1 || rows[0].Status == nil {
		t.Fatalf("expected one live detached broker, got %d sockets", len(rows))
	}
	row := rows[0]
	if row.PID == p.cmd.Process.Pid || row.Status.PID != row.PID || row.Status.Mode != "follow" || !slices.Equal(row.Status.Offers, []string{lease.Cap}) || row.Status.Device != pin {
		t.Fatalf("the detached broker does not carry the pre-dial pin %s: %+v", pin, row.Status)
	}
	fx.secretsHidden(t, p)
	fx.procClean(t, row.PID)
}

func (p *ttyProc) exit(t *testing.T, code int, within time.Duration) {
	t.Helper()
	select {
	case err := <-p.done:
		var exit *exec.ExitError
		if code == 0 && err != nil || code != 0 && (!errors.As(err, &exit) || exit.ExitCode() != code) {
			t.Fatalf("review exited with %v, want %d\n%s", err, code, p.text())
		}
	case <-time.After(within):
		t.Fatal("review did not exit")
	}
	for prev, deadline := p.text(), time.Now().Add(5*time.Second); time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		now := p.text()
		if now == prev {
			return
		}
		prev = now
	}
}

func (p *ttyProc) saw(all ...string) bool {
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		text := p.text()
		missing := false
		for _, s := range all {
			missing = missing || !strings.Contains(text, s)
		}
		if !missing || time.Now().After(deadline) {
			return !missing
		}
	}
}

func (fx *coldFixture) pending(t *testing.T, holder string, ttl int) {
	t.Helper()
	fx.pendingAt(t, holder, coldOrigin, ttl)
}

func (fx *coldFixture) pendingAt(t *testing.T, holder, origin string, ttl int) string {
	t.Helper()
	var r struct{ Type, Request string }
	fx.page.eval(fmt.Sprintf(`%s.request([{kind:'origin',url:%q}],{ttlSeconds:%d})`, holder, origin, ttl), &r)
	if r.Type != "pending" || r.Request == "" {
		t.Fatal("holder did not establish a proven pending request")
	}
	return r.Request
}

func (fx *coldFixture) watch(holder string) {
	fx.page.eval(fmt.Sprintf(`(globalThis[%q] = %s.grant().catch(e => 'reset:' + (e && e.code)), true)`, holder+"Outcome", holder), nil)
}

func (fx *coldFixture) outcome(holder string) string {
	var s string
	fx.page.evalWithin(fmt.Sprintf("globalThis[%q]", holder+"Outcome"), &s, 30*time.Second)
	return s
}

func notGranted(s string) bool {
	return strings.HasPrefix(s, "E_") || strings.HasPrefix(s, "reset:")
}

func gone(t *testing.T) {
	t.Helper()
	if rows := coldEntries(t); len(rows) != 0 {
		t.Fatalf("the started broker is still listed: %d sockets", len(rows))
	}
}

func TestSignedLeaseFirstPairReview(t *testing.T) {
	t.Run("ApproveMatchingHolder", func(t *testing.T) {
		fx := newColdFixture(t, true)
		p := fx.review(t)
		hub, pin := fx.toPin(t, p, strings.ToUpper(fx.hub.srv.URL)+"/")
		if hub != fx.hub.srv.URL {
			t.Fatalf("printed hub origin %q is not canonical", hub)
		}
		p.keys(t, fx.seven+"\n")
		p.wait(t, 0, "Paste its join URL here (hidden):")
		p.keys(t, fx.hub.joinURL(fx.tray)+"\n")
		p.wait(t, 0, "type REVIEW:")
		rows := coldEntries(t)
		if len(rows) != 1 || rows[0].Status == nil || rows[0].Status.Device != pin || rows[0].PID == p.cmd.Process.Pid {
			t.Fatal("the detached broker does not carry the pre-dial pin")
		}
		row := rows[0]
		fx.secretsHidden(t, p)
		fx.procClean(t, row.PID)
		fx.page.waitUp(t, "")
		fx.page.eval("otherHolder.init()", nil)
		const otherOrigin = "https://other.example"
		other := fx.pendingAt(t, "otherHolder", otherOrigin, 600)
		fx.pending(t, "leaseTest", 300)
		p.keys(t, "REVIEW\n")
		screen := p.wait(t, 0, "Type APPROVE to issue")
		if !strings.Contains(screen, "Link first-pair · Browser "+fx.endpoint+" · one new tab · holder jkt:"+fx.jkt) || !strings.Contains(screen, "origin: "+coldOrigin) || !strings.Contains(screen, "For 5m0s from approval") || strings.Contains(screen, otherOrigin) || strings.Contains(screen, "10m0s") {
			t.Fatal("approval did not display the matched link, browser, holder, typed scope and deadline")
		}
		p.keys(t, "APPROVE\n")
		p.exit(t, 0, 10*time.Second)
		var list lease.Snapshot
		if err := json.Unmarshal([]byte(rawCtl(t, row.Path, ctl.OpLeaseList)), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Active) != 1 {
			t.Fatal("approval left no active lease")
		}
		if !strings.Contains(p.text(), "Approved for 5m0s from approval") {
			t.Fatalf("approval did not print the request TTL:\n%s", p.text())
		}
		var leaseID, state string
		fx.page.eval("leaseTest.grant()", &leaseID)
		fx.page.eval("leaseTest.connect()", &state)
		if state != "ready" || len(list.Pending) != 1 || list.Pending[0].Request != other || list.Active[0].Lease != leaseID || list.Active[0].JKT != fx.jkt {
			t.Fatal("the approved job did not receive its lease, or another holder's request was consumed")
		}
		if rows := coldEntries(t); len(rows) != 1 || rows[0].PID != row.PID {
			t.Fatal("approval did not keep the started broker")
		}
		fx.secretsHidden(t, p)
	})
	t.Run("EnterCancels", func(t *testing.T) {
		fx := newColdFixture(t, true)
		p := fx.review(t)
		fx.toReview(t, p)
		fx.page.waitUp(t, "")
		fx.page.eval("otherHolder.init()", nil)
		fx.pending(t, "otherHolder", 300)
		fx.pending(t, "leaseTest", 300)
		fx.watch("leaseTest")
		fx.watch("otherHolder")
		p.keys(t, "REVIEW\n")
		p.wait(t, 0, "Type APPROVE to issue")
		p.keys(t, "\n")
		p.exit(t, 1, 20*time.Second)
		if !p.saw("not approved", "nothing was issued") {
			t.Fatal("review did not say that nothing was issued")
		}
		gone(t)
		wanted, other := fx.outcome("leaseTest"), fx.outcome("otherHolder")
		if !notGranted(wanted) || !notGranted(other) || wanted == "E_DENIED" || other == "E_DENIED" {
			t.Fatalf("outcomes %q and %q after cancel", wanted, other)
		}
		fx.secretsHidden(t, p)
	})
	t.Run("Deny", func(t *testing.T) {
		fx := newColdFixture(t, true)
		p := fx.review(t)
		fx.toReview(t, p)
		fx.page.waitUp(t, "")
		fx.page.eval("otherHolder.init()", nil)
		fx.pending(t, "otherHolder", 300)
		fx.pending(t, "leaseTest", 300)
		fx.watch("leaseTest")
		fx.watch("otherHolder")
		p.keys(t, "REVIEW\n")
		p.wait(t, 0, "Type APPROVE to issue")
		start := time.Now()
		p.keys(t, "DENY\n")
		p.exit(t, 0, 20*time.Second)
		if took := time.Since(start); took < 3*time.Second {
			t.Fatalf("deny returned after %s without the delivery grace", took)
		}
		if !p.saw("Denied.", "nothing was issued") {
			t.Fatal("review did not say that nothing was issued")
		}
		gone(t)
		wanted, other := fx.outcome("leaseTest"), fx.outcome("otherHolder")
		if wanted != "E_DENIED" || !notGranted(other) || other == "E_DENIED" {
			t.Fatalf("outcomes %q and %q after deny", wanted, other)
		}
		fx.secretsHidden(t, p)
	})
	t.Run("NoMatchWithinTheBound", func(t *testing.T) {
		fx := newColdFixture(t, true)
		p := fx.review(t)
		fx.toReview(t, p)
		fx.page.waitUp(t, "")
		fx.page.eval("otherHolder.init()", nil)
		fx.pending(t, "otherHolder", 300)
		fx.pending(t, "leaseTest", 600)
		fx.watch("leaseTest")
		fx.watch("otherHolder")
		start := time.Now()
		p.keys(t, "REVIEW\n")
		p.exit(t, 1, 40*time.Second)
		took := time.Since(start)
		if took < 15*time.Second || took > 20*time.Second || strings.Contains(p.text(), "Type APPROVE") || !p.saw("no matching proven request from jkt:"+fx.jkt+" within 15s", "nothing was issued") {
			t.Fatalf("no-match review after %s:\n%s", took, p.text())
		}
		t.Logf("no-match review ended %s after REVIEW", took.Round(100*time.Millisecond))
		gone(t)
		for _, h := range []string{"leaseTest", "otherHolder"} {
			if s := fx.outcome(h); !notGranted(s) || s == "E_DENIED" {
				t.Fatalf("%s outcome %q", h, s)
			}
		}
		fx.secretsHidden(t, p)
	})
	for _, tc := range []struct{ name, key, word string }{{"EOFAtReview", "\x04", "not approved"}, {"InterruptAtReview", "\x03", "interrupted"}} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newColdFixture(t, true)
			p := fx.review(t)
			fx.toReview(t, p)
			p.keys(t, tc.key)
			p.exit(t, 1, 20*time.Second)
			if !p.saw(tc.word, "stopped the lease-only link this review started; nothing was issued") {
				t.Fatalf("no nothing-issued line:\n%s", p.text())
			}
			gone(t)
			fx.secretsHidden(t, p)
		})
	}
	t.Run("InterruptBeforeApprove", func(t *testing.T) {
		fx := newColdFixture(t, true)
		p := fx.review(t)
		fx.toReview(t, p)
		fx.page.waitUp(t, "")
		fx.pending(t, "leaseTest", 300)
		p.keys(t, "REVIEW\n")
		p.wait(t, 0, "Type APPROVE to issue")
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		p.exit(t, 1, 20*time.Second)
		if !p.saw("nothing was issued") || strings.Contains(p.text(), "lease was issued") {
			t.Fatalf("signal before approve:\n%s", p.text())
		}
		gone(t)
		fx.secretsHidden(t, p)
	})
	t.Run("InterruptAfterApprove", func(t *testing.T) {
		fx := newColdFixture(t, true)
		p := fx.review(t)
		fx.toReview(t, p)
		fx.page.waitUp(t, "")
		fx.pending(t, "leaseTest", 300)
		p.keys(t, "REVIEW\n")
		p.wait(t, 0, "Type APPROVE to issue")
		p.keys(t, "APPROVE\n")
		var leaseID string
		fx.page.eval("leaseTest.grant()", &leaseID)
		if leaseID == "" {
			t.Fatal("holder did not receive a grant")
		}
		select {
		case err := <-p.done:
			var exit *exec.ExitError
			if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 0) {
				t.Fatalf("review exited with %v\n%s", err, p.text())
			}
			if !p.saw("Approved for ") {
				t.Fatalf("review exited 0 without printing the approved line:\n%s", p.text())
			}
		case <-time.After(500 * time.Millisecond):
			if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				select {
				case waitErr := <-p.done:
					t.Fatalf("review exited while it was being signalled: signal=%v wait=%v\n%s", err, waitErr, p.text())
				default:
					t.Fatalf("a running review could not be signalled: %v", err)
				}
			}
			p.exit(t, 1, 20*time.Second)
			if !p.saw("kept") {
				t.Fatalf("signal after grant did not report that the link was kept:\n%s", p.text())
			}
		}
		if text := p.text(); strings.Contains(text, "nothing was issued") || strings.Contains(text, "stopped the lease-only link") {
			t.Fatalf("a granted review claimed nothing was issued or stopped the link:\n%s", text)
		}
		rows := coldEntries(t)
		if len(rows) != 1 {
			t.Fatalf("grant must keep the broker: %d sockets", len(rows))
		}
		fx.secretsHidden(t, p)
	})
	t.Run("InterruptDuringStartup", func(t *testing.T) {
		fx := newColdFixture(t, false)
		endpoint, dialed, release := holdCDPStartup(t)
		fx.endpoint = endpoint
		p := fx.review(t)
		fx.toJoin(t, p, fx.hub.srv.URL)
		p.keys(t, fx.hub.joinURL(fx.tray)+"\n")
		select {
		case <-dialed:
		case <-time.After(15 * time.Second):
			t.Fatal("review started no broker")
		}
		var child int
		for deadline := time.Now().Add(2 * time.Second); child == 0; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("review started no broker")
			}
			out, _ := exec.Command("pgrep", "-P", strconv.Itoa(p.cmd.Process.Pid)).Output()
			child, _ = strconv.Atoi(strings.TrimSpace(string(out)))
		}
		dir, err := ctl.Dir()
		if err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, strconv.Itoa(child)+".sock")
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, qerr := ctl.QueryPID(ctx, sock, child)
		cancel()
		if qerr == nil {
			t.Fatal("the broker published its control socket before lease.New returned")
		}
		if strings.Contains(p.text(), "type REVIEW") {
			t.Fatal("the review had already trusted the broker before the startup interrupt")
		}
		if err := p.cmd.Process.Signal(syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		release()
		p.exit(t, 1, 20*time.Second)
		if !p.saw("nothing was issued") {
			t.Fatalf("startup interrupt:\n%s", p.text())
		}
		if strings.Contains(p.text(), "type REVIEW") {
			t.Fatalf("startup interrupt showed a trusted broker:\n%s", p.text())
		}
		for deadline := time.Now().Add(5 * time.Second); syscall.Kill(child, 0) == nil; time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("startup interrupt left the child process running")
			}
		}
		fx.secretsHidden(t, p)
	})
	t.Run("DeviceMismatch", func(t *testing.T) {
		fx := newColdFixture(t, true)
		p := fx.review(t)
		fx.toJoin(t, p, fx.hub.srv.URL)
		before := fx.conns.Load()
		if err := os.Remove(fx.certs); err != nil {
			t.Fatal(err)
		}
		p.keys(t, fx.hub.joinURL(fx.tray)+"\n")
		p.exit(t, 1, 30*time.Second)
		if !p.saw("different fingerprint than the one shown") || strings.Contains(p.text(), "type REVIEW") {
			t.Fatalf("a broker with another identity was kept:\n%s", p.text())
		}
		if fx.conns.Load() != before || len(attachRequests(fx.hub, fx.tray.Token)) != 0 {
			t.Fatal("device mismatch reached the hub")
		}
		gone(t)
		fx.secretsHidden(t, p)
	})
	t.Run("WarmRouteAppears", func(t *testing.T) {
		fx := newColdFixture(t, true)
		p := fx.review(t)
		pin := fx.toJoin(t, p, fx.hub.srv.URL)
		pair := exec.Command(binary, "lease", "pair", "--link", "warm", "--peer", fx.seven)
		pair.Env = fx.env
		if out, err := pair.CombinedOutput(); err != nil {
			t.Fatalf("pair: %v %s", err, out)
		}
		warm := exec.Command(binary, "-", "follow", "--lease-browser", fx.endpoint, "--name", "warm")
		warm.Env = fx.env
		warm.Stdin = strings.NewReader(fx.hub.joinURL(fx.hub.addTray("warm")) + "\n")
		if err := warm.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = warm.Process.Kill(); _ = warm.Wait() })
		var row ctl.Entry
		for deadline := time.Now().Add(20 * time.Second); row.Status == nil; time.Sleep(150 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the direct lease-only follower did not start")
			}
			for _, r := range coldEntries(t) {
				if r.PID == warm.Process.Pid && r.Status != nil && slices.Equal(r.Status.Offers, []string{lease.Cap}) {
					row = r
				}
			}
		}
		if row.Status.Device != pin {
			t.Fatalf("a follower started without review reports %s, the pre-dial pin was %s", row.Status.Device, pin)
		}
		p.keys(t, fx.hub.joinURL(fx.tray)+"\n")
		p.exit(t, 1, 20*time.Second)
		if !p.saw("appeared during the review; it was left alone") {
			t.Fatalf("review did not refuse:\n%s", p.text())
		}
		rows := coldEntries(t)
		if len(rows) != 1 || rows[0].PID != warm.Process.Pid || rows[0].Status == nil || syscall.Kill(warm.Process.Pid, 0) != nil {
			t.Fatal("review touched the route that appeared during it")
		}
		fx.secretsHidden(t, p)
	})
	t.Run("ClaimRace", func(t *testing.T) {
		fx := newColdFixture(t, false)
		release := make(chan struct{})
		hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		t.Cleanup(hang.Close)
		t.Cleanup(func() { close(release) })
		fx.endpoint = hang.URL
		pair := exec.Command(binary, "lease", "pair", "--link", "hold", "--peer", fx.seven)
		pair.Env = fx.env
		if out, err := pair.CombinedOutput(); err != nil {
			t.Fatalf("pair: %v %s", err, out)
		}
		join := fx.hub.joinURL(fx.tray)
		hold := exec.Command(binary, "-", "follow", "--lease-browser", fx.endpoint, "--name", "hold")
		hold.Env = fx.env
		hold.Stdin = strings.NewReader(join + "\n")
		if err := hold.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = hold.Process.Kill(); _ = hold.Wait() })
		for deadline := time.Now().Add(15 * time.Second); !holdsClaim(t, fx); time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the first lease-only follower did not hold the device claim")
			}
			if hold.ProcessState != nil && hold.ProcessState.Exited() {
				t.Fatal("the first lease-only follower exited before claiming")
			}
		}
		before := fx.conns.Load()
		second := exec.Command(binary, "-", "follow", "--lease-browser", fx.endpoint, "--name", "hold")
		second.Env = fx.env
		second.Stdin = strings.NewReader(join + "\n")
		out, err := second.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 3 {
			t.Fatalf("second follower exit=%v out=%s", err, out)
		}
		if n := fx.conns.Load(); n != before {
			t.Fatalf("second follower made %d hub connections", n-before)
		}
		if rows := coldEntries(t); len(rows) != 0 {
			t.Fatalf("second follower published a control socket: %d", len(rows))
		}
		for deadline := time.Now().Add(15 * time.Second); !holdsClaim(t, fx); time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the first follower does not hold the claim lock")
			}
		}
		_ = hold.Process.Kill()
		_ = hold.Wait()
		if holdsClaim(t, fx) {
			t.Fatal("the claim lock outlived the follower that held it")
		}
		again := exec.Command(binary, "-", "follow", "--lease-browser", fx.endpoint, "--name", "hold")
		again.Env = fx.env
		again.Stdin = strings.NewReader(join + "\n")
		if err := again.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = again.Wait(); close(done) }()
		t.Cleanup(func() { _ = again.Process.Kill(); <-done })
		for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			select {
			case <-done:
				t.Fatal("follower after release exited before reclaiming")
			default:
			}
			if holdsClaim(t, fx) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the claim was not taken again after the first follower died")
			}
		}
	})
	t.Run("ForeignSocketAnswers", func(t *testing.T) {
		fx := newColdFixture(t, false)
		release := make(chan struct{})
		hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		t.Cleanup(hang.Close)
		t.Cleanup(func() { close(release) })
		fx.endpoint = hang.URL
		p := fx.review(t)
		pin := fx.toJoin(t, p, fx.hub.srv.URL)
		p.keys(t, fx.hub.joinURL(fx.tray)+"\n")
		var child int
		for deadline := time.Now().Add(10 * time.Second); child == 0; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("review started no broker")
			}
			out, _ := exec.Command("pgrep", "-P", strconv.Itoa(p.cmd.Process.Pid)).Output()
			child, _ = strconv.Atoi(strings.TrimSpace(string(out)))
		}
		dir, err := ctl.Dir()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		ln, err := net.Listen("unix", filepath.Join(dir, strconv.Itoa(child)+".sock"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		st := ctl.NewHolder("follow", "fake").Snapshot()
		st.PID, st.Device, st.State, st.Offers = child, pin, ctl.StateConnected, []string{lease.Cap}
		st.Ops = append(st.Ops, ctl.OpStop, ctl.OpLeaseList, ctl.OpLeaseApprove, ctl.OpLeaseDeny, ctl.OpLeaseRevoke)
		body, _ := json.Marshal(st)
		reply := append(slices.Clone(body), '\n')
		var mu sync.Mutex
		var asked []string
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					defer func() { _ = c.Close() }()
					_ = c.SetDeadline(time.Now().Add(2 * time.Second))
					line, err := bufio.NewReader(c).ReadString('\n')
					if err != nil {
						return
					}
					mu.Lock()
					asked = append(asked, line)
					mu.Unlock()
					_, _ = c.Write(reply)
				}()
			}
		}()
		foreignStillAnswers(t, filepath.Join(dir, strconv.Itoa(child)+".sock"), child)
		mu.Lock()
		asked = nil
		mu.Unlock()
		p.exit(t, 1, 20*time.Second)
		if !p.saw("it was not trusted", "nothing was issued") {
			t.Fatalf("review trusted a foreign socket:\n%s", p.text())
		}
		mu.Lock()
		n := len(asked)
		mu.Unlock()
		if n != 0 {
			t.Fatalf("review sent %d requests to a process it did not start", n)
		}
		for deadline := time.Now().Add(5 * time.Second); syscall.Kill(child, 0) == nil; time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("review left its own broker running")
			}
		}
		if fx.conns.Load() != 0 {
			t.Fatal("the stopped broker reached the hub")
		}
		fx.secretsHidden(t, p)
	})
	t.Run("ForeignSocketAfterTrust", func(t *testing.T) {
		fx := newColdFixture(t, true)
		p := fx.review(t)
		fx.toReview(t, p)
		rows := coldEntries(t)
		if len(rows) != 1 {
			t.Fatal("expected a trusted broker")
		}
		child := rows[0].PID
		dir, err := ctl.Dir()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, strconv.Itoa(child)+".sock")
		_ = os.Remove(path)
		ln, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		st := ctl.NewHolder("follow", "fake").Snapshot()
		st.PID, st.Device, st.State, st.Offers = child, rows[0].Status.Device, ctl.StateConnected, []string{lease.Cap}
		st.Ops = append(st.Ops, ctl.OpStop, ctl.OpLeaseList, ctl.OpLeaseApprove, ctl.OpLeaseDeny, ctl.OpLeaseRevoke)
		body, _ := json.Marshal(st)
		reply := append(slices.Clone(body), '\n')
		var mu sync.Mutex
		var asked []string
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					defer func() { _ = c.Close() }()
					_ = c.SetDeadline(time.Now().Add(2 * time.Second))
					line, err := bufio.NewReader(c).ReadString('\n')
					if err != nil {
						return
					}
					mu.Lock()
					asked = append(asked, line)
					mu.Unlock()
					_, _ = c.Write(reply)
				}()
			}
		}()
		foreignStillAnswers(t, path, child)
		mu.Lock()
		asked = nil
		mu.Unlock()
		fx.page.waitUp(t, "")
		fx.pending(t, "leaseTest", 300)
		foreignStillAnswers(t, path, child)
		mu.Lock()
		asked = nil
		mu.Unlock()
		p.keys(t, "REVIEW\n")
		p.exit(t, 1, 40*time.Second)
		if !p.saw("nothing was issued") || (!strings.Contains(p.text(), "not trusted") && !strings.Contains(p.text(), "stopped")) {
			t.Fatalf("post-trust foreign socket:\n%s", p.text())
		}
		mu.Lock()
		n := len(asked)
		mu.Unlock()
		if n != 0 {
			t.Fatalf("review sent %d requests to a foreign socket after trust", n)
		}
		for deadline := time.Now().Add(5 * time.Second); syscall.Kill(child, 0) == nil; time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("review left its own broker running")
			}
		}
		fx.secretsHidden(t, p)
	})
	for _, tc := range []struct{ name, typed, join, want string }{
		{"OtherHost", "", "localhost", "the join URL is not on"},
		{"OtherPort", "", "port", "the join URL is not on"},
		{"HTTPSAgainstHTTP", "https", "", "the join URL is not on"},
		{"UserInfo", "user@", "", "expected the tray hub's https origin"},
		{"Path", "/seven", "", "expected the tray hub's https origin"},
		{"EOFAtJoinURL", "", "eof", "no valid join URL"},
		{"InterruptAtJoinURL", "", "interrupt", "interrupted"},
		{"TerminatedAtJoinURL", "", "term", "interrupted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newColdFixture(t, false)
			hub := fx.hub.srv.URL
			switch {
			case tc.typed == "https":
				hub = strings.Replace(hub, "http://", "https://", 1)
			case strings.HasSuffix(tc.typed, "@"):
				hub = strings.Replace(hub, "://", "://"+tc.typed, 1)
			case tc.typed != "":
				hub += tc.typed
			}
			join := fx.hub.joinURL(fx.tray)
			switch tc.join {
			case "localhost":
				join = strings.Replace(join, "127.0.0.1", "localhost", 1)
			case "port":
				u := fx.hub.srv.Listener.Addr().(*net.TCPAddr)
				join = strings.Replace(join, ":"+strconv.Itoa(u.Port), ":"+strconv.Itoa(u.Port+1), 1)
			}
			p := fx.review(t)
			if strings.Contains(tc.want, "origin") {
				p.wait(t, 0, "literal loopback HTTP origin:")
				p.keys(t, fx.endpoint+"\nfirst-pair\n"+hub+"\n")
				p.exit(t, 1, 10*time.Second)
				if _, err := os.Stat(fx.certs); err == nil {
					t.Fatal("an invalid hub origin created an identity")
				}
			} else {
				fx.toJoin(t, p, hub)
				switch tc.join {
				case "eof":
					p.keys(t, "\x04")
				case "interrupt":
					p.keys(t, "\x03")
				case "term":
					time.Sleep(300 * time.Millisecond)
					_ = p.cmd.Process.Signal(syscall.SIGTERM)
				default:
					p.keys(t, join+"\n")
				}
				p.exit(t, 1, 10*time.Second)
			}
			if !p.saw(tc.want, "nothing was issued") {
				t.Fatalf("missing %q:\n%s", tc.want, p.text())
			}
			if fx.conns.Load() != 0 || len(coldEntries(t)) != 0 {
				t.Fatal("a refused first pair dialed the hub or started a broker")
			}
			fx.secretsHidden(t, p)
		})
	}
	t.Run("StopIgnoredAtHiddenJoin", func(t *testing.T) {
		fx := newColdFixture(t, true)
		p := fx.review(t)
		fx.toJoin(t, p, fx.hub.srv.URL)
		time.Sleep(300 * time.Millisecond)
		if err := p.cmd.Process.Signal(syscall.SIGTSTP); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		if out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(p.cmd.Process.Pid)).Output(); err != nil || strings.Contains(string(out), "T") {
			t.Fatalf("SIGTSTP stopped the review at the hidden prompt: %q %v", out, err)
		}
		p.keys(t, fx.hub.joinURL(fx.tray)+"\n")
		p.wait(t, 0, "type REVIEW:")
		if len(coldEntries(t)) != 1 {
			t.Fatal("the join after the ignored stop did not start a broker")
		}
		p.keys(t, "\x04")
		p.exit(t, 1, 20*time.Second)
		if !p.saw("nothing was issued") {
			t.Fatalf("SIGTSTP left review stuck:\n%s", p.text())
		}
		gone(t)
		fx.secretsHidden(t, p)
	})
	t.Run("UpgradeKeepsTheStoredIdentity", func(t *testing.T) {
		fx := newColdFixture(t, false)
		key := tray.HubOrigin(fx.hub.joinURL(fx.tray))
		if _, _, err := tray.OpenCerts(fx.certs).Load(key); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(fx.certs)
		if err != nil {
			t.Fatal(err)
		}
		known := tray.OpenCerts(fx.certs).Known()
		p := fx.review(t)
		_, pin := fx.toPin(t, p, fx.hub.srv.URL)
		p.keys(t, "\x04")
		p.exit(t, 1, 10*time.Second)
		if !p.saw("nothing was issued") {
			t.Fatalf("missing transcript:\n%s", p.text())
		}
		after, _ := os.ReadFile(fx.certs)
		if len(known) != 1 || known[0].Origin != key || pin != known[0].Fingerprint || !bytes.Equal(before, after) {
			t.Fatalf("the pre-dial pin %s is not the identity stored under %s", pin, key)
		}
		fx.secretsHidden(t, p)
	})
	t.Run("PinNeverReusedSilently", func(t *testing.T) {
		fx := newColdFixture(t, false)
		p := fx.review(t)
		fx.toJoin(t, p, fx.hub.srv.URL)
		p.keys(t, "\x04")
		p.exit(t, 1, 10*time.Second)
		if !p.saw("nothing was issued") {
			t.Fatalf("missing transcript:\n%s", p.text())
		}
		fx.secretsHidden(t, p)
		other, _ := countedHub(t)
		p = fx.review(t)
		fx.toPin(t, p, other.srv.URL)
		p.keys(t, strings.Repeat("ab", 32)+"\n")
		p.exit(t, 1, 10*time.Second)
		if !p.saw("choose a new label", "nothing was issued") || fx.conns.Load() != 0 || len(coldEntries(t)) != 0 {
			t.Fatalf("a different Seven pin under a stored label was accepted:\n%s", p.text())
		}
		fx.secretsHidden(t, p)
		p = fx.review(t)
		fx.toPin(t, p, other.srv.URL)
		p.keys(t, fx.seven+"\n")
		p.wait(t, 0, "Paste its join URL here (hidden):")
		p.keys(t, "\x04")
		p.exit(t, 1, 10*time.Second)
		if !p.saw("no valid join URL", "nothing was issued") || fx.conns.Load() != 0 || len(coldEntries(t)) != 0 {
			t.Fatalf("the retyped Seven pin did not lead to the join URL prompt:\n%s", p.text())
		}
		fx.secretsHidden(t, p)
	})
	t.Run("NoTerminal", func(t *testing.T) {
		fx := newColdFixture(t, false)
		cmd := exec.Command(binary, "lease", "review", "--to", "jkt:"+strings.Repeat("A", 43), "--allow", coldOrigin, "--for", "5m")
		cmd.Env = fx.env
		cmd.Stdin = strings.NewReader(fx.endpoint + "\n")
		out, err := cmd.CombinedOutput()
		text := string(out)
		if err == nil || !strings.Contains(text, "requires a local terminal") || strings.Contains(text, "No lease-only link") {
			t.Fatalf("review without a terminal: %v %s", err, out)
		}
		if _, err := os.Stat(fx.certs); err == nil {
			t.Fatal("review without a terminal created an identity")
		}
	})
}

func TestSignedLeaseReviewRegression(t *testing.T) {
	if output, err := protectOverlay(t, leaseReviewTable, nil, nil); err != nil {
		t.Fatalf("lease review regression: %v\n%s", err, output)
	}
}

var leaseReviewTable = protectOverlayRun{
	pkg:     "./cmd/slicc",
	file:    "cmd/slicc/leasereview_test.go",
	regress: "test/integration/testdata/leasereview/regression.go",
	extra: map[string]string{
		"cmd/slicc/leasereview_unix_test.go":           "test/integration/testdata/leasereview/regression_unix.go",
		"cmd/slicc/leasereview_termios_linux_test.go":  "test/integration/testdata/leasereview/termios_linux.go",
		"cmd/slicc/leasereview_termios_darwin_test.go": "test/integration/testdata/leasereview/termios_darwin.go",
		"cmd/slicc/leasereview_termios_stub_test.go":   "test/integration/testdata/leasereview/termios_stub.go",
	},
	run:     "^TestLeaseReview",
	timeout: 5 * time.Minute,
}

func TestSignedLeaseReviewCatchesMutants(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(root, "cmd/slicc/leasecert.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ name, from, to, row string }{
		{"Case", "a, e := idna.Lookup.ToASCII(host)", "a, e := idna.Lookup.ToASCII(host)\n\t\ta = host", "TestLeaseReviewHubOrigin/Case"},
		{"DefaultPort", `if scheme == "https" && n == 443 || scheme == "http" && n == 80 {`, "if false {", "TestLeaseReviewHubOrigin/DefaultPortTyped"},
		{"TrailingDot", `host = strings.TrimSuffix(host, ".")`, `host = strings.TrimSuffix(host, "")`, "TestLeaseReviewHubOrigin/TrailingDotTyped"},
		{"UserInfo", "|| u.User != nil ", "", "TestLeaseReviewHubOrigin/UserInfo"},
		{"Lookalike", "return origin != \"\" && origin == hub", "return origin != \"\" && strings.HasPrefix(origin, hub)", "TestLeaseReviewHubOrigin/LookalikeSuffix"},
		{"Port", "\tif port != \"\" {\n\t\thost += \":\" + port\n\t}\n", "", "TestLeaseReviewHubOrigin/PortTyped"},
		{"SecondCert", "leaseIdentity(leaseCertStore(base), leaseCertKey(hub))", `leaseIdentity(leaseCertStore(base+".review"), leaseCertKey(hub))`, "TestLeaseReviewIdentity"},
		{"KeyWithoutPort", `func leaseCertKey(hub string) string { return tray.HubOrigin(hub + "/join/key") }`, "func leaseCertKey(hub string) string { return hub }", "TestLeaseReviewIdentityUpgrade"},
		{"LooseStore", "if f.Chmod(tight) != nil {\n\t\treturn errLeaseSecure\n\t}\n\treturn nil", "return nil", "TestLeaseReviewStoreModes"},
		{"OwnerRwSkipped", "if perm&need != need {\n\t\treturn errLeaseRead\n\t}", "if false {\n\t\treturn errLeaseRead\n\t}", "TestLeaseReviewStoreFailClosed"},
		{"DamagedReplace", "pemData, created, err = certs.LoadLease(origin)", "pemData, created, err = certs.Load(origin)", "TestLeaseReviewStoreFailClosed"},
	} {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			if n := strings.Count(string(source), m.from); n != 1 {
				t.Fatalf("mutant anchor %q appears %d times", m.from, n)
			}
			mutated := strings.Replace(string(source), m.from, m.to, 1)
			output, err := protectOverlay(t, leaseReviewTable, nil, map[string]string{"cmd/slicc/leasecert.go": mutated})
			if err == nil || !strings.Contains(output, "--- FAIL: "+m.row+" ") {
				t.Fatalf("mutant %s must fail %s\n%s", m.name, m.row, output)
			}
		})
	}
	claimSrc, err := os.ReadFile(filepath.Join(root, "cmd/slicc/leaseclaim.go"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("ClaimRemoved", func(t *testing.T) {
		t.Parallel()
		from := "ok, err := flock.TryLock(f)\n\tif err != nil {\n\t\t_ = f.Close()\n\t\treturn errLeaseClaimFailed\n\t}\n\tif !ok {\n\t\t_ = f.Close()\n\t\treturn errLeaseClaimHeld\n\t}"
		to := "ok, err := flock.TryLock(f)\n\t_, _ = ok, err\n\tif false {\n\t\t_ = f.Close()\n\t\treturn errLeaseClaimHeld\n\t}"
		if n := strings.Count(string(claimSrc), from); n != 1 {
			t.Fatalf("claim mutant anchor appears %d times", n)
		}
		mutated := strings.Replace(string(claimSrc), from, to, 1)
		output, err := protectOverlay(t, leaseReviewTable, nil, map[string]string{"cmd/slicc/leaseclaim.go": mutated})
		if err == nil || !strings.Contains(output, "--- FAIL: TestLeaseReviewDeviceClaim ") {
			t.Fatalf("mutant ClaimRemoved must fail TestLeaseReviewDeviceClaim\n%s", output)
		}
	})
	leaseSrc, err := os.ReadFile(filepath.Join(root, "cmd/slicc/lease.go"))
	if err != nil {
		t.Fatal(err)
	}
	unixSrc, err := os.ReadFile(filepath.Join(root, "cmd/slicc/lease_unix.go"))
	if err != nil {
		t.Fatal(err)
	}
	pasteSrc, err := os.ReadFile(filepath.Join(root, "cmd/slicc/leasepaste_unix.go"))
	if err != nil {
		t.Fatal(err)
	}
	quietSrc, err := os.ReadFile(filepath.Join(root, "cmd/slicc/leasequiet_unix.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ name, from, to, file, row string }{
		{"UnconditionalStop", "if cp.approving {\n\t\tcp.kept = true\n\t\tcp.approveUnknown = true\n\t\tcp.mu.Unlock()\n\t\tif restore != nil {\n\t\t\trestore()\n\t\t}\n\t\treturn \"interrupted during approval; the result is unknown; check slicc lease status and slicc lease revoke <lease-id>; the lease-only link was kept\"\n\t}", "if cp.approving {\n\t\tcp.mu.Unlock()\n\t\tif restore != nil {\n\t\t\trestore()\n\t\t}\n\t\treturn \"interrupted; nothing was issued\"\n\t}", "cmd/slicc/lease.go", "TestLeaseReviewInterruptAfterGrant"},
		{"SplitApprovingInterrupt", "if cp.approving {\n\t\tcp.kept = true\n\t\tcp.approveUnknown = true\n\t\tcp.mu.Unlock()\n\t\tif restore != nil {\n\t\t\trestore()\n\t\t}\n\t\treturn \"interrupted during approval; the result is unknown; check slicc lease status and slicc lease revoke <lease-id>; the lease-only link was kept\"\n\t}\n\tkept, unknown := cp.kept, cp.approveUnknown\n\tcp.mu.Unlock()", "approving := cp.approving\n\tkept, unknown := cp.kept, cp.approveUnknown\n\tcp.mu.Unlock()\n\tif afterInterruptSnap != nil {\n\t\tafterInterruptSnap()\n\t}\n\tif approving {\n\t\tcp.mu.Lock()\n\t\tcp.kept = true\n\t\tcp.approveUnknown = true\n\t\tcp.mu.Unlock()\n\t\tif restore != nil {\n\t\t\trestore()\n\t\t}\n\t\treturn \"interrupted during approval; the result is unknown; check slicc lease status and slicc lease revoke <lease-id>; the lease-only link was kept\"\n\t}", "cmd/slicc/lease.go", "TestLeaseReviewApproveInterruptRace/ApprovingFinishInterleave"},
		{"InterruptedBeforePromptRemoved", "cp.mu.Lock()\n\tif cp.interrupted {\n\t\tcp.mu.Unlock()\n\t\treturn nil, errInterrupted\n\t}\n\tcp.mu.Unlock()\n\tshow()\n\tcp.mu.Lock()\n\tif cp.interrupted {\n\t\tcp.mu.Unlock()\n\t\treturn nil, errInterrupted\n\t}\n\tcp.mu.Unlock()\n\treturn readHiddenLine(f, fd)", "show()\n\treturn readHiddenLine(f, fd)", "cmd/slicc/lease.go", "TestLeaseReviewRestoreBeforeMakeRaw/InterruptedBeforePrompt"},
		{"AliveRemoved", "if child == nil || !child.alive() {\n\t\treturn errLeaseLinkStopped\n\t}\n\treturn c.call(q)", "if child == nil {\n\t\treturn errLeaseLinkStopped\n\t}\n\treturn c.call(q)", "cmd/slicc/lease.go", "TestLeaseReviewChildCallAfterExit"},
		{"StopUnlinksForeign", "func (c *leaseChild) stop() {\n\tif c.proc == nil {\n\t\treturn\n\t}\n\tif c.alive() {\n\t\t_ = signalLeaseStop(c.proc)", "func (c *leaseChild) stop() {\n\tif c.proc == nil {\n\t\treturn\n\t}\n\tif c.path != \"\" {\n\t\t_ = os.Remove(c.path)\n\t\t_ = os.Remove(c.path + \".lock\")\n\t}\n\tif c.alive() {\n\t\t_ = signalLeaseStop(c.proc)", "cmd/slicc/lease.go", "TestLeaseReviewStopLeavesForeign"},
		{"InterruptedCheckRemoved", "cp.mu.Lock()\n\tif cp.interrupted || cp.restored {\n\t\tcp.mu.Unlock()\n\t\treturn nil, errInterrupted\n\t}\n\tcp.restore = restore\n\tcp.restored = false\n\t_, e = term.MakeRaw(fd)\n\tcp.raw = e == nil\n\tcp.mu.Unlock()", "cp.mu.Lock()\n\tcp.restore = restore\n\tcp.restored = false\n\t_, e = term.MakeRaw(fd)\n\tcp.raw = e == nil\n\tcp.mu.Unlock()", "cmd/slicc/lease.go", "TestLeaseReviewRestoreBeforeMakeRaw"},
		{"RestoreAfterMakeRaw", "cp.mu.Lock()\n\tif cp.interrupted || cp.restored {\n\t\tcp.mu.Unlock()\n\t\treturn nil, errInterrupted\n\t}\n\tcp.restore = restore\n\tcp.restored = false\n\t_, e = term.MakeRaw(fd)\n\tcp.raw = e == nil\n\tcp.mu.Unlock()\n\tif e != nil {\n\t\treturn nil, e\n\t}", "_, e = term.MakeRaw(fd)\n\tif e != nil {\n\t\treturn nil, e\n\t}\n\tcp.mu.Lock()\n\tcp.restore = restore\n\tcp.raw = true\n\tcp.mu.Unlock()", "cmd/slicc/lease.go", "TestLeaseReviewRestoreBeforeMakeRaw"},
		{"RestoreOmit", "_ = term.Restore(fd, old)", "_ = old", "cmd/slicc/lease.go", "TestLeaseReviewRestoreBeforeMakeRaw"},
		{"EchoCheckRemoved", "if afterMakeRaw != nil {\n\t\tafterMakeRaw(fd)\n\t}\n\tif on, err := termiosEcho(fd); err != nil || on {\n\t\treturn nil, errors.New(\"terminal echo could not be disabled\")\n\t}\n\tif e := writePasteDisable(f); e != nil {\n\t\treturn nil, e\n\t}", "if afterMakeRaw != nil {\n\t\tafterMakeRaw(fd)\n\t}\n\tif e := writePasteDisable(f); e != nil {\n\t\treturn nil, e\n\t}", "cmd/slicc/lease.go", "TestLeaseReviewRestoreBeforeMakeRaw"},
		{"StopSignalsNotIgnored", "func ignoreStopSignals() func() {\n\tossignal.Ignore(syscall.SIGTSTP)\n\treturn func() { ossignal.Reset(syscall.SIGTSTP) }\n}", "func ignoreStopSignals() func() {\n\tossignal.Reset(syscall.SIGTSTP)\n\treturn func() { ossignal.Reset(syscall.SIGTSTP) }\n}", "cmd/slicc/lease_unix.go", "TestLeaseReviewStopIgnoredWhileRaw"},
		{"DenyGraceRemoved", "case e == nil && word == \"DENY\":\n\t\ttime.Sleep(leaseDecisionFlush)\n\t\tcp.abandon()", "case e == nil && word == \"DENY\":\n\t\tcp.abandon()", "cmd/slicc/lease.go", "TestLeaseReviewDenyFlush"},
		{"PasteDisableRemoved", "if e := writePasteDisable(f); e != nil {\n\t\treturn nil, e\n\t}\n\tcp.mu.Lock()\n\tif cp.interrupted {\n\t\tcp.mu.Unlock()\n\t\treturn nil, errInterrupted\n\t}\n\tcp.mu.Unlock()\n\tshow()", "cp.mu.Lock()\n\tif cp.interrupted {\n\t\tcp.mu.Unlock()\n\t\treturn nil, errInterrupted\n\t}\n\tcp.mu.Unlock()\n\tshow()", "cmd/slicc/lease.go", "TestLeaseReviewHiddenPaste"},
		{"QuietDrainRemoved", "func endHiddenLine(fd int, over bool) error {\n\tflushLeaseInput(fd)\n\tquietDrainLeaseInput(fd)\n", "func endHiddenLine(fd int, over bool) error {\n\tflushLeaseInput(fd)\n", "cmd/slicc/lease.go", "TestLeaseReviewHiddenPaste"},
		{"ControlAccepted", "func refuseHiddenControl(f *os.File, fd int, b []byte) error {\n\tfor {\n\t\tif n, e := f.Read(b); e != nil || n == 0 {\n\t\t\tbreak\n\t\t}\n\t\tswitch b[0] {\n\t\tcase 3:\n\t\t\tflushLeaseInput(fd)\n\t\t\tquietDrainLeaseInput(fd)\n\t\t\treturn errInterrupted\n\t\tcase 4:\n\t\t\tflushLeaseInput(fd)\n\t\t\tquietDrainLeaseInput(fd)\n\t\t\treturn io.EOF\n\t\tcase '\\r', '\\n':\n\t\t\tflushLeaseInput(fd)\n\t\t\tquietDrainLeaseInput(fd)\n\t\t\treturn errors.New(\"join URL refused\")\n\t\t}\n\t}\n\tflushLeaseInput(fd)\n\tquietDrainLeaseInput(fd)\n\treturn errors.New(\"join URL refused\")\n}", "func refuseHiddenControl(f *os.File, fd int, b []byte) error {\n\treturn nil\n}", "cmd/slicc/lease.go", "TestLeaseReviewHiddenPaste"},
		{"FlushRemoved", "func endHiddenLine(fd int, over bool) error {\n\tflushLeaseInput(fd)\n\tquietDrainLeaseInput(fd)\n\tif over {\n\t\treturn errors.New(\"join URL too long\")\n\t}\n\treturn nil\n}", "func endHiddenLine(fd int, over bool) error {\n\tif over {\n\t\treturn errors.New(\"join URL too long\")\n\t}\n\treturn nil\n}", "cmd/slicc/lease.go", "TestLeaseReviewHiddenCRLF"},
		{"BeginApproveCheckRemoved", "if cp.interrupted {\n\t\treturn false\n\t}\n\tcp.approving = true\n\treturn true", "cp.approving = true\n\treturn true", "cmd/slicc/lease.go", "TestLeaseReviewApproveInterruptRace"},
		{"FinishApproveSplit", "e := c.call(ctl.Request{Op: ctl.OpLeaseApprove, Request: c.p.Request})\n\tif afterApproveReply != nil {\n\t\tafterApproveReply()\n\t}\n\tif e == nil {\n\t\tcp.finishApprove(approveGranted)\n\t\tfmt.Fprintf(cp.out, \"Approved for %s from approval.\\n\", (time.Duration(c.p.TTL) * time.Second).String())\n\t\treturn nil\n\t}", "e := c.call(ctl.Request{Op: ctl.OpLeaseApprove, Request: c.p.Request})\n\tif e == nil {\n\t\tcp.mu.Lock()\n\t\tcp.approving = false\n\t\tcp.mu.Unlock()\n\t\tif afterApproveReply != nil {\n\t\t\tafterApproveReply()\n\t\t}\n\t\tcp.mu.Lock()\n\t\tcp.kept = true\n\t\tcp.mu.Unlock()\n\t\tfmt.Fprintf(cp.out, \"Approved for %s from approval.\\n\", (time.Duration(c.p.TTL) * time.Second).String())\n\t\treturn nil\n\t}", "cmd/slicc/lease.go", "TestLeaseReviewApproveInterruptRace"},
		{"StartInterruptedCheckRemoved", "cp.mu.Lock()\n\tif cp.interrupted {\n\t\tcp.mu.Unlock()\n\t\treturn nil, errInterrupted\n\t}\n\tif e := cmd.Start(); e != nil {\n\t\tcp.mu.Unlock()\n\t\treturn nil, e\n\t}\n\tc := &leaseChild{proc: cmd.Process, exited: make(chan struct{}), pin: pin}\n\tcp.child = c\n\trace := cp.interrupted\n\tcp.mu.Unlock()", "cp.mu.Lock()\n\tif e := cmd.Start(); e != nil {\n\t\tcp.mu.Unlock()\n\t\treturn nil, e\n\t}\n\tc := &leaseChild{proc: cmd.Process, exited: make(chan struct{}), pin: pin}\n\tcp.child = c\n\trace := false\n\tcp.mu.Unlock()", "cmd/slicc/lease.go", "TestLeaseReviewStartInterrupted"},
		{"StaleAsUnknown", "if errors.Is(e, errDecisionRefused) {\n\t\tcp.finishApprove(approveClear)\n\t\treturn e\n\t}\n\tcp.finishApprove(approveUnknown)\n\tfmt.Fprintln(cp.out, \"The approval result is unknown; check slicc lease status and slicc lease revoke <lease-id>; the lease-only link was kept.\")\n\treturn nil", "cp.finishApprove(approveUnknown)\n\tfmt.Fprintln(cp.out, \"The approval result is unknown; check slicc lease status and slicc lease revoke <lease-id>; the lease-only link was kept.\")\n\treturn nil", "cmd/slicc/lease.go", "TestLeaseReviewApproveStaleReject"},
		{"DeviceBusyUnbounded", "wg.Add(1)\n\t\tgo func(i int, path string, pid int) {\n\t\t\tdefer wg.Done()\n\t\t\tif st, err := ctl.QueryPID(ctx, path, pid); err == nil {\n\t\t\t\trows[i] = &found{pid, st}\n\t\t\t}\n\t\t}(i, path, pid)", "wg.Add(1)\n\t\tfunc(i int, path string, pid int) {\n\t\t\tdefer wg.Done()\n\t\t\tif st, err := ctl.QueryPID(context.Background(), path, pid); err == nil {\n\t\t\t\trows[i] = &found{pid, st}\n\t\t\t}\n\t\t}(i, path, pid)", "cmd/slicc/leaseclaim.go", "TestLeaseReviewDeviceBusyBound"},
		{"PasteDeviceMatchRemoved", "if unix.Fstat(fd, &st) != nil || st.Rdev != in.Rdev || st.Ino != in.Ino {\n\t\treturn false\n\t}", "if unix.Fstat(fd, &st) != nil {\n\t\treturn false\n\t}", "cmd/slicc/leasepaste_unix.go", "TestLeaseReviewPasteDisableTarget"},
		{"QuietEINTRExit", "if err == unix.EINTR {\n\t\t\t\tcontinue\n\t\t\t}", "if err == unix.EINTR {\n\t\t\t\treturn\n\t\t\t}", "cmd/slicc/leasequiet_unix.go", "TestLeaseReviewHiddenPaste"},
		{"QuietHangupSpin", "re := fds[0].Revents\n\t\tif re&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {\n\t\t\treturn\n\t\t}\n\t\tif re&unix.POLLIN == 0 {\n\t\t\treturn\n\t\t}\n\t\tflushLeaseInput(fd)", "flushLeaseInput(fd)", "cmd/slicc/leasequiet_unix.go", "TestLeaseReviewHiddenPaste"},
	} {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			if m.name == "StopSignalsNotIgnored" && runtime.GOOS != "linux" {
				t.Skip("SigIgn probe is Linux-only")
			}
			src := string(leaseSrc)
			switch m.file {
			case "cmd/slicc/leaseclaim.go":
				src = string(claimSrc)
			case "cmd/slicc/lease_unix.go":
				src = string(unixSrc)
			case "cmd/slicc/leasepaste_unix.go":
				src = string(pasteSrc)
			case "cmd/slicc/leasequiet_unix.go":
				src = string(quietSrc)
			}
			if n := strings.Count(src, m.from); n != 1 {
				t.Fatalf("mutant %s anchor appears %d times", m.name, n)
			}
			mutated := strings.Replace(src, m.from, m.to, 1)
			output, err := protectOverlay(t, leaseReviewTable, nil, map[string]string{m.file: mutated})
			if err == nil || !strings.Contains(output, "--- FAIL: "+m.row+" ") {
				t.Fatalf("mutant %s must fail %s\n%s", m.name, m.row, output)
			}
		})
	}
}
