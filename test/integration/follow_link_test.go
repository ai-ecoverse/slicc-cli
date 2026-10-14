package integration

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

type trayLeader struct {
	t      *testing.T
	h      *fakeHub
	p      *page
	toPage chan string

	mu         sync.Mutex
	swallow    int
	joinFail   *hubFailure
	answerFail *hubFailure
	failLater  time.Duration
}

func newTrayLeader(t *testing.T, h *fakeHub, p *page, trays ...*fakeTray) *trayLeader {
	t.Helper()
	l := &trayLeader{t: t, h: h, p: p, toPage: make(chan string, 1024)}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sub := p.b.client.Subscribe()
	t.Cleanup(sub.Close)
	if err := p.session.AddBinding(ctx, "sliccLinkSend"); err != nil {
		t.Fatal(err)
	}
	if err := p.session.Call(ctx, "Runtime.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	h.with(func() {
		for _, tr := range trays {
			tr.OfferSDP = ""
			tr.OnJoin = func(b *hubBootstrap) { l.onJoin(b) }
			tr.OnFollower = func(action string, b *hubBootstrap, body map[string]any) { l.onFollower(action, b, body) }
		}
	})
	go l.fromPage(sub, trays)
	go l.deliver()
	return l
}

func (l *trayLeader) push(m map[string]any) {
	b, _ := json.Marshal(m)
	l.toPage <- string(b)
}

func (l *trayLeader) onJoin(b *hubBootstrap) {
	l.mu.Lock()
	fail := l.joinFail
	l.mu.Unlock()
	if fail != nil {
		f := *fail
		f.FailedAt = hubNow()
		b.State, b.Failure = "failed", &f
		appendEvent(b, hubEvent{Type: "bootstrap.failed", Failure: &f})
		return
	}
	l.push(map[string]any{"t": "hello", "nonce": b.ID})
}

func (l *trayLeader) onFollower(action string, b *hubBootstrap, body map[string]any) {
	switch action {
	case "answer":
		l.mu.Lock()
		answerFail := l.answerFail
		l.mu.Unlock()
		if answerFail != nil {
			l.failAfterAnswer(b, body, *answerFail)
			return
		}
		l.mu.Lock()
		swallow := l.swallow > 0
		if swallow {
			l.swallow--
		}
		l.mu.Unlock()
		if swallow {
			after := hubRetryAfterMs
			f := &hubFailure{Code: "LEADER_NOT_CONNECTED", Message: "Leader control channel disconnected before bootstrap completed", Retryable: true, RetryAfterMs: &after, FailedAt: hubNow()}
			b.State, b.Failure = "failed", f
			appendEvent(b, hubEvent{Type: "bootstrap.failed", Failure: f})
			return
		}
		answer, _ := body["answer"].(map[string]any)
		l.push(map[string]any{"t": "answer", "nonce": b.ID, "sdp": answer["sdp"]})
	case "ice-candidate":
		l.push(map[string]any{"t": "candidate", "nonce": b.ID, "candidate": body["candidate"]})
	}
}

func (l *trayLeader) failAfterAnswer(b *hubBootstrap, body map[string]any, f hubFailure) {
	l.mu.Lock()
	later := l.failLater
	l.mu.Unlock()
	fail := func() {
		f.FailedAt = hubNow()
		b.State, b.Failure = "failed", &f
		appendEvent(b, hubEvent{Type: "bootstrap.failed", Failure: &f})
	}
	if later == 0 {
		fail()
		l.push(map[string]any{"t": "bye", "nonce": b.ID, "reason": f.Code})
		return
	}
	answer, _ := body["answer"].(map[string]any)
	l.push(map[string]any{"t": "answer", "nonce": b.ID, "sdp": answer["sdp"]})
	l.push(map[string]any{"t": "bye", "nonce": b.ID, "reason": f.Code})
	go func() {
		time.Sleep(later)
		l.h.with(fail)
	}()
}

func (l *trayLeader) fromPage(sub *cdp.Subscription, trays []*fakeTray) {
	for ev := range sub.Events() {
		if ev.SessionID != l.p.session.ID || ev.Method != "Runtime.bindingCalled" {
			continue
		}
		var bc cdp.BindingCalled
		if json.Unmarshal(ev.Params, &bc) != nil || bc.Name != "sliccLinkSend" {
			continue
		}
		var m struct {
			T         string        `json:"t"`
			Nonce     string        `json:"nonce"`
			SDP       string        `json:"sdp"`
			Candidate *hubCandidate `json:"candidate"`
		}
		if json.Unmarshal([]byte(bc.Payload), &m) != nil {
			continue
		}
		l.h.with(func() {
			for _, tr := range trays {
				for _, b := range tr.bootstraps {
					if b.ID != m.Nonce || b.State == "failed" {
						continue
					}
					switch {
					case m.T == "offer":
						appendEvent(b, hubEvent{Type: "bootstrap.offer", Offer: map[string]string{"type": "offer", "sdp": m.SDP}})
						b.State = "offered"
					case m.T == "candidate" && m.Candidate != nil && m.Candidate.Candidate != "":
						appendEvent(b, hubEvent{Type: "bootstrap.ice_candidate", Candidate: m.Candidate})
					}
				}
			}
		})
	}
}

func (l *trayLeader) deliver() {
	for line := range l.toPage {
		expr := "typeof globalThis.sliccLinkReceive === 'function' ? (globalThis.sliccLinkReceive(" + line + "), true) : false"
		for i := 0; i < 300; i++ {
			var ok bool
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := l.p.session.Evaluate(c, expr, cdp.EvaluateOptions{ReturnByValue: true}, &ok)
			cancel()
			if err == nil && ok {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

type pageStatus struct {
	Nonce string `json:"nonce"`
	Up    bool   `json:"up"`
}

func (p *page) status() pageStatus {
	var st pageStatus
	p.eval("linkTest.status()", &st)
	return st
}

func (p *page) waitUp(t *testing.T, notNonce string) pageStatus {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		st := p.status()
		if st.Up && st.Nonce != notNonce {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("the page link never came up (last %+v)", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (p *page) fingerprint() string {
	var fp string
	p.eval("linkTest.fingerprint()", &fp)
	return fp
}

type followProc struct {
	*attachProc
	exited chan int
}

func startFollow(t *testing.T, env []string, joinURL string, args ...string) *followProc {
	t.Helper()
	return startFollowIn(t, "", env, joinURL, args...)
}

func startFollowIn(t *testing.T, dir string, env []string, joinURL string, args ...string) *followProc {
	t.Helper()
	cmd := exec.Command(binary, append([]string{joinURL, "follow"}, args...)...)
	cmd.Env = env
	cmd.Dir = dir
	fp := &followProc{attachProc: &attachProc{cmd: cmd, stderr: &syncBuffer{}}, exited: make(chan int, 1)}
	cmd.Stderr = fp.stderr
	cmd.Stdout = fp.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		err := cmd.Wait()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
		fp.exited <- code
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		if t.Failed() || os.Getenv("SLICC_TEST_SHOW") != "" {
			t.Logf("slicc follow output:\n%s", fp.stderr.String())
		}
	})
	return fp
}

func (fp *followProc) wait(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case code := <-fp.exited:
		return code
	case <-time.After(timeout):
		t.Fatalf("slicc follow did not exit:\n%s", fp.stderr.String())
		return -1
	}
}

func (fp *followProc) stop(t *testing.T) {
	t.Helper()
	_ = fp.cmd.Process.Kill()
	fp.wait(t, 10*time.Second)
}

var fingerprintLine = regexp.MustCompile(`slicc: (new device|device), fingerprint (sha-256 [0-9A-F:]+)`)

func (fp *followProc) fingerprint(t *testing.T) (string, bool) {
	t.Helper()
	fp.waitStderr(t, "fingerprint sha-256", 10*time.Second)
	m := fingerprintLine.FindStringSubmatch(fp.stderr.String())
	if m == nil {
		t.Fatalf("no fingerprint line:\n%s", fp.stderr.String())
	}
	return m[2], m[1] == "new device"
}

func followSetup(t *testing.T, trays int) ([]string, *fakeHub, []*fakeTray, *page, *trayLeader) {
	t.Helper()
	b := launchBrowser(t)
	p := b.openPage(t, pageServer(t).URL+"/index.html")
	env := append(configEnv(t), "SLICC_DEBUG=1", "SLICC_NO_TELEMETRY=1")
	h := newFakeHub(t)
	ts := make([]*fakeTray, trays)
	for i := range ts {
		ts[i] = h.addTray("follow")
	}
	l := newTrayLeader(t, h, p, ts...)
	return env, h, ts, p, l
}

func assertFollowRedacted(t *testing.T, fp *followProc, h *fakeHub, trays ...*fakeTray) {
	t.Helper()
	assertRedacted(t, fp.stderr.String(), h, trays...)
	for _, s := range []string{"turn-user-secret", "turn-credential-secret"} {
		if strings.Contains(fp.stderr.String(), s) {
			t.Fatalf("output leaks %q", s)
		}
	}
}

func TestFollowLinksThroughTheTrayAndServesTCP(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	echo := serveTCP(t, func(c net.Conn) {
		_, _ = io.Copy(c, c)
		_ = c.(*net.TCPConn).CloseWrite()
		_, _ = io.Copy(io.Discard, c)
	})
	host, port := hostPort(t, echo)
	fp := startFollow(t, env, h.joinURL(trays[0]), "--exit", "--allow", "127.0.0.1/32", "--name", "laptop", "--hosts", writeHosts(t, hostsFile))
	fingerprint, created := fp.fingerprint(t)
	if !created {
		t.Fatal("first run did not mint a device")
	}
	fp.waitStderr(t, "slicc: exit on (tcp, resolve, http, ws)", 10*time.Second)
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.waitUp(t, "")
	if got := p.fingerprint(); got != fingerprint || !strings.HasPrefix(got, "sha-256 ") {
		t.Fatalf("page sees fingerprint %q, slicc says %q", got, fingerprint)
	}
	var res struct {
		OK    bool   `json:"ok"`
		Reset string `json:"reset"`
	}
	p.eval("linkTest.echo("+jsArgs(host, port, 300000)+")", &res)
	if !res.OK {
		t.Fatalf("echo %+v", res)
	}
	p.eval("linkTest.echo("+jsArgs("127.0.0.2", port, 10)+")", &res)
	if res.Reset != "EACCES" {
		t.Fatalf("loopback outside the allow rule: %+v", res)
	}
	followExitKinds(t, p)
	var hello struct {
		Opened any    `json:"opened"`
		Reset  string `json:"reset"`
	}
	p.eval("linkTest.open({kind: 'unknown'})", &hello)
	if hello.Reset != "EOPNOTSUPP" {
		t.Fatalf("unknown kind %+v", hello)
	}
	assertFollowRedacted(t, fp, h, trays...)
}

func TestFollowWithoutExitRefusesTCP(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]))
	fp.waitStderr(t, "slicc: exit off", 10*time.Second)
	p.waitUp(t, "")
	var res struct {
		Reset string `json:"reset"`
	}
	p.eval("linkTest.echo("+jsArgs("1.1.1.1", 443, 10)+")", &res)
	if res.Reset != "EOPNOTSUPP" {
		t.Fatalf("tcp without --exit: %+v", res)
	}
	for _, open := range []string{`{kind: 'resolve', name: 'localhost', family: 0}`, `{kind: 'http'}`, `{kind: 'ws', url: 'ws://1.1.1.1/'}`, `{kind: 'cdp', browser: 'chrome'}`} {
		p.eval("linkTest.open("+open+")", &res)
		if res.Reset != "EOPNOTSUPP" {
			t.Fatalf("%s without --exit: %+v", open, res)
		}
	}
}

func TestFollowReconnectsAndKeepsItsIdentity(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]))
	fingerprint, _ := fp.fingerprint(t)
	first := p.waitUp(t, "")
	var dropped map[string]any
	p.eval("linkTest.drop()", &dropped)
	fp.waitStderr(t, "slicc: reconnecting in", 30*time.Second)
	second := p.waitUp(t, first.Nonce)
	if p.fingerprint() != fingerprint {
		t.Fatal("the fingerprint changed on reconnect")
	}
	reqs := attachRequests(h, trays[0].Token)
	if len(reqs) < 2 || reqs[0].Body["controllerId"] == reqs[len(reqs)-1].Body["controllerId"] {
		t.Fatalf("attaches %d, controller ids must differ per dial", len(reqs))
	}
	if second.Nonce == first.Nonce {
		t.Fatal("the reconnect reused the old bootstrap")
	}
	fp.stop(t)

	again := startFollow(t, env, h.joinURL(trays[0]))
	fp2, created := again.fingerprint(t)
	if created || fp2 != fingerprint {
		t.Fatalf("restart minted a new identity: %s (new %v), was %s", fp2, created, fingerprint)
	}
	p.waitUp(t, second.Nonce)
	if p.fingerprint() != fingerprint {
		t.Fatal("the page sees a different fingerprint after a restart")
	}
	if runtime.GOOS != "windows" {
		path, _ := tray.DefaultCertsPath()
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("certificate file mode %v %v", fi, err)
		}
	}
	assertFollowRedacted(t, again, h, trays...)
}

func TestFollowRestartsAFailedBootstrap(t *testing.T) {
	env, h, trays, p, l := followSetup(t, 1)
	l.mu.Lock()
	l.swallow = 1
	l.mu.Unlock()
	fp := startFollow(t, env, h.joinURL(trays[0]))
	st := p.waitUp(t, "")
	h.with(func() {
		if len(trays[0].bootstraps) < 2 || st.Nonce != trays[0].bootstraps[len(trays[0].bootstraps)-1].ID {
			t.Errorf("link is not on the retried bootstrap (%d bootstraps)", len(trays[0].bootstraps))
		}
	})
	retried := false
	for _, r := range h.requestLog() {
		retried = retried || r.Action == "retry"
	}
	if !retried {
		t.Fatal("no retry")
	}
	fp.waitStderr(t, "slicc: linked to link test page", 10*time.Second)
}

func TestFollowUnlinkOverTheLink(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]))
	before, _ := fp.fingerprint(t)
	p.waitUp(t, "")
	var res struct {
		Opened map[string]any `json:"opened"`
		Reset  string         `json:"reset"`
	}
	p.eval("linkTest.open({kind: 'unlink'})", &res)
	if res.Opened == nil {
		t.Fatalf("unlink %+v", res)
	}
	if code := fp.wait(t, 15*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(fp.stderr.String(), "slicc: unlinked by link test page\n") {
		t.Fatalf("no unlink line:\n%s", fp.stderr.String())
	}
	assertForgotten(t, h.joinURL(trays[0]))
	after := refollowFingerprint(t, env, h.joinURL(trays[0]))
	if after == before {
		t.Fatal("re-follow kept the old identity")
	}
}

func TestFollowUnlinkedByTheLeader(t *testing.T) {
	env, h, trays, p, l := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]))
	before, _ := fp.fingerprint(t)
	p.waitUp(t, "")
	l.mu.Lock()
	l.joinFail = &hubFailure{Code: "UNLINKED", Message: "This device was unlinked", Retryable: false}
	l.mu.Unlock()
	var dropped map[string]any
	p.eval("linkTest.drop()", &dropped)
	if code := fp.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(fp.stderr.String(), "slicc: unlinked by link test page\n") {
		t.Fatalf("no unlink line:\n%s", fp.stderr.String())
	}
	assertForgotten(t, h.joinURL(trays[0]))
	l.mu.Lock()
	l.joinFail = nil
	l.mu.Unlock()
	if refollowFingerprint(t, env, h.joinURL(trays[0])) == before {
		t.Fatal("re-follow kept the old identity")
	}
}

func refollowFingerprint(t *testing.T, env []string, joinURL string) string {
	t.Helper()
	fp := startFollow(t, env, joinURL)
	got, created := fp.fingerprint(t)
	if !created {
		t.Fatal("re-follow did not mint a new device")
	}
	fp.stop(t)
	return got
}

func assertForgotten(t *testing.T, joinURL string) {
	t.Helper()
	statePath, _ := tray.DefaultStatePath()
	if _, ok := tray.OpenState(statePath).Last(); ok {
		t.Fatal("join state survived the unlink")
	}
	certPath, _ := tray.DefaultCertsPath()
	data, _ := os.ReadFile(certPath)
	if strings.Contains(string(data), tray.HubOrigin(joinURL)) {
		t.Fatal("certificate survived the unlink")
	}
}

func TestFollowJoinRotationOverTheLink(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 2)
	other := newFakeHub(t)
	foreign := other.addTray("foreign")
	fp := startFollow(t, env, h.joinURL(trays[0]))
	first := p.waitUp(t, "")
	var res struct {
		Opened map[string]any `json:"opened"`
		Reset  string         `json:"reset"`
		Detail string         `json:"detail"`
	}
	p.eval("linkTest.open("+jsArgs(map[string]string{"kind": "join", "url": other.joinURL(foreign)})+")", &res)
	if res.Reset != "EACCES" {
		t.Fatalf("foreign join URL: %+v", res)
	}
	statePath, _ := tray.DefaultStatePath()
	if tray.OpenState(statePath).Current(h.joinURL(trays[0])) != h.joinURL(trays[0]) {
		t.Fatal("a foreign join URL was stored")
	}
	res = struct {
		Opened map[string]any `json:"opened"`
		Reset  string         `json:"reset"`
		Detail string         `json:"detail"`
	}{}
	p.eval("linkTest.open("+jsArgs(map[string]string{"kind": "join", "url": h.joinURL(trays[1])})+")", &res)
	if res.Opened == nil {
		t.Fatalf("join %+v", res)
	}
	if tray.OpenState(statePath).Current(h.joinURL(trays[0])) != h.joinURL(trays[1]) {
		t.Fatal("the rotated join URL was not stored")
	}
	if p.status().Nonce != first.Nonce {
		t.Fatal("rotation dropped the link")
	}
	var dropped map[string]any
	p.eval("linkTest.drop()", &dropped)
	p.waitUp(t, first.Nonce)
	if len(attachRequests(h, trays[1].Token)) == 0 {
		t.Fatal("the reconnect did not use the rotated join URL")
	}
	if len(other.requestLog()) != 0 {
		t.Fatal("the foreign hub was contacted")
	}
	assertFollowRedacted(t, fp, h, trays...)
	assertNoSecret(t, fp.stderr.String(), other.joinURL(foreign))
}

func TestFollowStopsOnTerminalFailures(t *testing.T) {
	env := append(configEnv(t), "SLICC_NO_TELEMETRY=1")
	h := newFakeHub(t)
	expired := h.addTray("expired")
	untrusted := h.addTray("untrusted")
	old := h.addTray("old")
	h.with(func() {
		expired.Expired = true
		for _, c := range []struct {
			tr   *fakeTray
			code string
		}{{untrusted, "UNTRUSTED"}, {old, "UNSUPPORTED_RUNTIME"}} {
			code := c.code
			c.tr.OnJoin = func(b *hubBootstrap) {
				f := &hubFailure{Code: code, Message: "refused by the leader", FailedAt: hubNow()}
				b.State, b.Failure = "failed", f
				appendEvent(b, hubEvent{Type: "bootstrap.failed", Failure: f})
			}
		}
	})
	cases := []struct {
		joinURL, want string
	}{
		{h.joinURL(expired), "seven's tray has expired; get a new join URL from seven (TRAY_EXPIRED)"},
		{h.srv.URL + "/join/unknown.00112233445566778899", "the join URL is not valid; get a new one from seven (INVALID_JOIN_CAPABILITY)"},
		{h.joinURL(untrusted), "seven does not trust this device; link it again from seven (UNTRUSTED)"},
		{h.joinURL(old), "seven does not support this slicc version; update slicc or seven (UNSUPPORTED_RUNTIME)"},
	}
	for _, c := range cases {
		fp := startFollow(t, env, c.joinURL)
		if code := fp.wait(t, 20*time.Second); code != 1 || !strings.Contains(fp.stderr.String(), "slicc: "+c.want+"\n") {
			t.Fatalf("%s: exit %d:\n%s", c.want, code, fp.stderr.String())
		}
		assertNoSecret(t, fp.stderr.String(), c.joinURL)
	}
}

func TestFollowReportsDialFailuresToTelemetry(t *testing.T) {
	bin := buildVersion(t, "1.0.0", t.TempDir())
	env := configEnv(t)
	h := newFakeHub(t)
	expired := h.addTray("expired")
	h.with(func() { expired.Expired = true })
	col := newCollector(t)
	r := runBin(t, append(env, col.env()...), bin, h.joinURL(expired), "follow")
	if r.code != 1 {
		t.Fatalf("%+v", r)
	}
	failure := col.find("error")
	if failure == nil || failure.Source != "signal" || !strings.Contains(failure.Target, "TRAY_EXPIRED") {
		t.Fatalf("beacons: %+v", col.got())
	}
	for _, b := range col.got() {
		assertNoSecret(t, b.raw, h.joinURL(expired))
	}
}

func TestFollowUnlinkedAfterTheAnswer(t *testing.T) {
	for _, later := range []time.Duration{0, 1500 * time.Millisecond} {
		t.Run(later.String(), func(t *testing.T) { unlinkedAfterAnswer(t, later) })
	}
}

func unlinkedAfterAnswer(t *testing.T, later time.Duration) {
	env, h, trays, p, l := followSetup(t, 1)
	l.mu.Lock()
	l.failLater = later
	l.mu.Unlock()
	fp := startFollow(t, env, h.joinURL(trays[0]))
	before, _ := fp.fingerprint(t)
	first := p.waitUp(t, "")
	l.mu.Lock()
	l.answerFail = &hubFailure{Code: "UNLINKED", Message: "This device was unlinked", Retryable: false}
	l.mu.Unlock()
	var dropped map[string]any
	p.eval("linkTest.drop()", &dropped)
	if code := fp.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := fp.stderr.String()
	if !strings.Contains(out, "slicc: unlinked by link test page\n") {
		t.Fatalf("no unlink line:\n%s", out)
	}
	answered := 0
	h.with(func() {
		for _, a := range trays[0].answers {
			if a.BootstrapID != first.Nonce {
				answered++
			}
		}
	})
	if answered == 0 {
		t.Fatal("the unlink did not come after an answer")
	}
	assertForgotten(t, h.joinURL(trays[0]))
	l.mu.Lock()
	l.answerFail = nil
	l.mu.Unlock()
	if refollowFingerprint(t, env, h.joinURL(trays[0])) == before {
		t.Fatal("re-follow kept the old identity")
	}
}

func TestFollowStopsWhenTheAnswerHasNoFingerprint(t *testing.T) {
	env, h, trays, _, l := followSetup(t, 1)
	l.mu.Lock()
	l.answerFail = &hubFailure{Code: "NO_FINGERPRINT", Message: "answer has no sha-256 fingerprint", Retryable: false}
	l.mu.Unlock()
	fp := startFollow(t, env, h.joinURL(trays[0]))
	if code := fp.wait(t, 30*time.Second); code != 1 {
		t.Fatalf("exit %d", code)
	}
	want := "slicc: internal error: seven found no SHA-256 fingerprint in this device's answer; please report this (NO_FINGERPRINT)\n"
	if !strings.Contains(fp.stderr.String(), want) {
		t.Fatalf("output:\n%s", fp.stderr.String())
	}
}

func TestFollowCDP(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]), "--cdp", p.b.endpoint)
	fp.waitStderr(t, "slicc: cdp on (chrome): seven gets full control", 15*time.Second)
	p.waitUp(t, "")
	var hello struct {
		Caps     []string       `json:"caps"`
		Browsers []helloBrowser `json:"browsers"`
	}
	p.eval("linkTest.ready()", &hello)
	if !strings.Contains(strings.Join(hello.Caps, ","), "cdp") || len(hello.Browsers) != 1 || hello.Browsers[0].ID != "chrome" || hello.Browsers[0].Seven {
		t.Fatalf("hello %+v", hello)
	}
	var r struct {
		TargetID string    `json:"targetId"`
		Closed   cdpClosed `json:"closed"`
		Reset    string    `json:"reset"`
		Error    string    `json:"error"`
	}
	p.eval("linkTest.cdp.createTarget('chrome', 'about:blank')", &r)
	if r.TargetID == "" || !r.Closed.End {
		t.Fatalf("createTarget through follow: %+v", r)
	}
	if !targetIDs(t, p.b)[r.TargetID] {
		t.Fatalf("target %s is not in the followed browser", r.TargetID)
	}
	p.b.closeTarget(r.TargetID)
}

func TestFollowExitProtectsTheCDPPort(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	echo := serveTCP(t, func(c net.Conn) {
		_, _ = io.Copy(c, c)
		_ = c.(*net.TCPConn).CloseWrite()
		_, _ = io.Copy(io.Discard, c)
	})
	host, port := hostPort(t, echo)
	args := []string{"--exit", "--allow", "127.0.0.1/32", "--allow", "[::1]/128", "--cdp", p.b.endpoint}
	lan, haveLAN := findLANAddress()
	if haveLAN {
		args = append(args, "--allow", netip.PrefixFrom(lan, lan.BitLen()).String())
	}
	fp := startFollow(t, env, h.joinURL(trays[0]), args...)
	fp.waitStderr(t, "slicc: cdp on (chrome)", 15*time.Second)
	p.waitUp(t, "")
	var hello struct {
		Policy struct {
			Deny []string `json:"deny"`
		} `json:"policy"`
	}
	p.eval("linkTest.ready()", &hello)
	want := "127.0.0.1/32:" + strconv.Itoa(p.b.port)
	if !slices.Contains(hello.Policy.Deny, want) {
		t.Fatalf("policy deny %v lacks %s", hello.Policy.Deny, want)
	}
	hosts := []string{"127.0.0.1", "127.0.0.2", "::1", "localhost"}
	if haveLAN {
		hosts = append(hosts, lan.String())
	}
	for _, target := range hosts {
		var r echoResult
		p.eval("linkTest.open("+jsArgs(map[string]any{"kind": "tcp", "host": target, "port": p.b.port}, false)+")", &r)
		if r.Reset != "EACCES" {
			t.Fatalf("tcp to %s:%d (the debugging port): %+v", target, p.b.port, r)
		}
	}
	var res struct {
		OK    bool   `json:"ok"`
		Reset string `json:"reset"`
	}
	p.eval("linkTest.echo("+jsArgs(host, port, 100000)+")", &res)
	if !res.OK {
		t.Fatalf("echo on another loopback port: %+v", res)
	}
}

func fakeCDPVersion(t *testing.T, wsPort int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			ws := "ws://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(wsPort)) + "/devtools/browser/fake"
			_ = json.NewEncoder(w).Encode(map[string]string{"Browser": "HeadlessChrome/140.0.0.0", "Protocol-Version": "1.3", "webSocketDebuggerUrl": ws})
		case "/json/list":
			_, _ = io.WriteString(w, "[]")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFollowExitProtectsEveryAddressOfACDPEndpoint(t *testing.T) {
	lan := lanAddress(t)
	env, h, trays, p, _ := followSetup(t, 1)
	wsPort := closedPort(t)
	cdpSrv := fakeCDPVersion(t, wsPort)
	cdpPort := cdpSrv.Listener.Addr().(*net.TCPAddr).Port
	other := closedPortOn(t, lan)
	hosts := writeHosts(t, "127.0.0.1 localhost\n"+lan.String()+" localhost\n")
	lanNet := netip.PrefixFrom(lan, min(24, lan.BitLen())).Masked().String()
	cdpURL := "http://localhost:" + strconv.Itoa(cdpPort)
	fp := startFollow(t, env, h.joinURL(trays[0]), "--exit", "--allow", lanNet, "--allow", "127.0.0.1/32", "--hosts", hosts, "--cdp", cdpURL)
	fp.waitStderr(t, "slicc: cdp on (chrome)", 30*time.Second)
	p.waitUp(t, "")
	var hello struct {
		Policy struct {
			Deny []string `json:"deny"`
		} `json:"policy"`
	}
	p.eval("linkTest.ready()", &hello)
	lanRule := policy.Rule{Prefix: netip.PrefixFrom(lan, lan.BitLen()), Port: uint16(cdpPort)}.String()
	for _, want := range []string{lanRule, "127.0.0.1/32:" + strconv.Itoa(cdpPort), "127.0.0.1/32:" + strconv.Itoa(wsPort)} {
		if !slices.Contains(hello.Policy.Deny, want) {
			t.Fatalf("policy deny %v lacks %s", hello.Policy.Deny, want)
		}
	}
	open := func(host string, port int) echoResult {
		var r echoResult
		p.eval("linkTest.open("+jsArgs(map[string]any{"kind": "tcp", "host": host, "port": port}, false)+")", &r)
		return r
	}
	for _, c := range []struct {
		host string
		port int
	}{{lan.String(), cdpPort}, {"127.0.0.1", cdpPort}, {"127.0.0.1", wsPort}} {
		if r := open(c.host, c.port); r.Reset != "EACCES" {
			t.Fatalf("tcp to the cdp endpoint %s:%d: %+v", c.host, c.port, r)
		}
	}
	if r := open(lan.String(), other); r.Reset == "EACCES" {
		t.Fatalf("tcp to another port on %s was refused by the policy: %+v", lan, r)
	}
}

func closedPortOn(t *testing.T, addr netip.Addr) int {
	t.Helper()
	ln, err := net.Listen("tcp", netip.AddrPortFrom(addr, 0).String())
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func TestAttachProtectsEveryAddressOfACDPEndpoint(t *testing.T) {
	lan := lanAddress(t)
	wsPort := closedPort(t)
	cdpSrv := fakeCDPVersion(t, wsPort)
	cdpPort := cdpSrv.Listener.Addr().(*net.TCPAddr).Port
	other := closedPortOn(t, lan)
	hosts := writeHosts(t, "127.0.0.1 localhost\n"+lan.String()+" localhost\n")
	env := setupLink(t, false, "--hosts", hosts, "--cdp-extra", "http://localhost:"+strconv.Itoa(cdpPort), "--allow", netip.PrefixFrom(lan, lan.BitLen()).String())
	for _, c := range []struct {
		host string
		port int
	}{{lan.String(), cdpPort}, {"127.0.0.1", cdpPort}, {"127.0.0.1", wsPort}} {
		var r echoResult
		env.p.eval("linkTest.open("+jsArgs(map[string]any{"kind": "tcp", "host": c.host, "port": c.port}, false)+")", &r)
		if r.Reset != "EACCES" {
			t.Fatalf("tcp to the cdp endpoint %s:%d: %+v", c.host, c.port, r)
		}
	}
	var r echoResult
	env.p.eval("linkTest.open("+jsArgs(map[string]any{"kind": "tcp", "host": lan.String(), "port": other}, false)+")", &r)
	if r.Reset == "EACCES" {
		t.Fatalf("tcp to another port on %s was refused by the policy: %+v", lan, r)
	}
}
