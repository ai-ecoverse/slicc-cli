//go:build !windows

package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"
)

const (
	hgHelloWindow    = 8 * time.Second
	hgHandshakeBound = 20 * time.Second
	hgOutcomeBound   = 20 * time.Second
)

var helloGateFollowerRows = []string{
	"TestP3FollowerMismatchSendsNothing",
	"TestP3FollowerMatchUnchanged",
	"TestP3FollowerRelinkAcceptsLegitimateAfterRefused",
	"TestP3FollowerPostReadyCheckStillHolds",
	"TestP3FollowerStreamAuthStillHolds",
	"TestP3FollowerPinFileUnchanged",
}

func helloGateFollowerMutants() []helloGateFollowerMutant {
	return []helloGateFollowerMutant{
		{name: "GateRemoved", red: []string{"TestP3FollowerMismatchSendsNothing"},
			edits: []helloGateEdit{{file: "cmd/slicc/leasefollow.go", anchor: helloGatePeerGateAssign, to: ""}}},
		{name: "GateAcceptsAnyLeaf", red: []string{"TestP3FollowerMismatchSendsNothing"},
			edits: []helloGateEdit{{file: "cmd/slicc/leasefollow.go", anchor: helloGatePinMatchBody, to: helloGateAnyLeafBody}}},
		{name: "ErrorMappingRemoved", red: []string{"TestP3FollowerMismatchSendsNothing"},
			edits: []helloGateEdit{{file: "cmd/slicc/leasefollow.go", anchor: helloGateFailMapping, to: ""}}},
		{name: "PostReadyCheckRemoved", red: []string{"TestP3FollowerPostReadyCheckStillHolds"},
			edits: append(slices.Clone(helloGatePostReadyVariant), helloGateEdit{file: "cmd/slicc/leasefollow.go", anchor: helloGatePostReady, to: helloGatePostReadyOff})},
		{name: "StreamAuthRemoved", red: []string{"TestP3FollowerStreamAuthStillHolds"},
			edits: append(slices.Clone(helloGateStreamAuthVariant), helloGateEdit{file: "cmd/slicc/leasefollow.go", anchor: helloGateStreamAuth, to: ""})},
	}
}

func helloGateFollowerBin() string {
	if v := os.Getenv(helloGateBinaryEnv); v != "" {
		return v
	}
	return binary
}

func helloGateBuildEnv(t *testing.T) []string {
	t.Helper()
	return helloGateEnvMode(t, false)
}

func helloGateMutatedBinary(t *testing.T, name string, edits []helloGateEdit) string {
	t.Helper()
	root := helloGateRoot(t)
	mutated, err := helloGateApply(root, edits)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	scratch := t.TempDir()
	replace := map[string]string{}
	for file, body := range mutated {
		dest := filepath.Join(scratch, strings.ReplaceAll(file, "/", "_"))
		if err := os.WriteFile(dest, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		replace[filepath.Join(root, file)] = dest
	}
	data, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(scratch, "overlay.json")
	if err := os.WriteFile(overlay, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(scratch, "slicc-hg")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-overlay", overlay, "-ldflags", "-X main.version=1.2.3-test", "-o", out, "./cmd/slicc")
	cmd.Dir = root
	cmd.Env = helloGateBuildEnv(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: mutated slicc build: %v\n%s", name, err, b)
	}
	return out
}

func helloGateFollowerChild(t *testing.T, row, bin string) (string, string) {
	t.Helper()
	env := helloGateBuildEnv(t)
	if bin != "" {
		env = append(env, helloGateBinaryEnv+"="+bin)
	}
	out, err := helloGateRunChild(t, row, env, 3*time.Minute)
	return out, helloGateClassify(row, out, err)
}

func helloGateFollowerNamed() []string {
	var rows []string
	for _, m := range helloGateFollowerMutants() {
		for _, row := range m.red {
			if !slices.Contains(rows, row) {
				rows = append(rows, row)
			}
		}
	}
	slices.Sort(rows)
	return rows
}

func TestHelloGateCatchesFollowerMutants(t *testing.T) {
	helloGateRequireAnchors(t)
	helloGateSelfTest(t)
	named := helloGateFollowerNamed()
	for _, row := range named {
		if !slices.Contains(helloGateFollowerRows, row) {
			t.Fatalf("a follower mutant names %s, which is not a follower row", row)
		}
	}
	helloGateEach(t, named, func(row string) string { return "baseline/" + row }, func(t *testing.T, row string) {
		out, kind := helloGateFollowerChild(t, row, "")
		if kind != "pass" {
			t.Fatalf("follower baseline row %s: %s, want pass\n%s", row, kind, helloGateTail(out, 80))
		}
	})
	if t.Failed() {
		t.FailNow()
	}
	helloGateEach(t, helloGateFollowerMutants(), func(m helloGateFollowerMutant) string { return m.name }, func(t *testing.T, m helloGateFollowerMutant) {
		bin := helloGateMutatedBinary(t, m.name, m.edits)
		for _, row := range m.red {
			out, kind := helloGateFollowerChild(t, row, bin)
			if kind != "killed" {
				t.Errorf("follower mutant %s: row %s %s, want killed (FAIL leaf, exit 1, handshake reached)\n%s", m.name, row, kind, helloGateTail(out, 80))
				continue
			}
			t.Logf("follower mutant %s: row %s killed", m.name, row)
		}
	})
}

func hgFollowerCert(t *testing.T) webrtc.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := webrtc.GenerateCertificate(key)
	if err != nil {
		t.Fatal(err)
	}
	return *cert
}

func hgFingerprint(t *testing.T, cert webrtc.Certificate) string {
	t.Helper()
	fps, err := cert.GetFingerprints()
	if err != nil || len(fps) == 0 {
		t.Fatalf("certificate fingerprint: %v", err)
	}
	return fps[0].Value
}

func hgFollowerPin(t *testing.T, cert webrtc.Certificate) string {
	t.Helper()
	return strings.ToLower(strings.ReplaceAll(hgFingerprint(t, cert), ":", ""))
}

func hgFakeLeaseBrowser(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/150.0.0.0",
			"Protocol-Version":     "1.3",
			"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/hellogate",
		})
	})
	mux.HandleFunc("/devtools/browser/hellogate", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		conn.SetReadLimit(1 << 20)
		ctx := r.Context()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var p struct {
				ID        int64  `json:"id"`
				Method    string `json:"method"`
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(data, &p) != nil {
				continue
			}
			var result map[string]any
			switch p.Method {
			case "Browser.getVersion":
				result = map[string]any{"product": "Chrome/150.0.0.0", "protocolVersion": "1.3"}
			case "Target.attachToBrowserTarget":
				result = map[string]any{"sessionId": "hg"}
			default:
				result = map[string]any{}
			}
			out, _ := json.Marshal(map[string]any{"id": p.ID, "sessionId": p.SessionID, "result": result})
			if conn.Write(ctx, websocket.MessageText, out) != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func hgCtlDir(rt string) string {
	if runtime.GOOS == "linux" {
		return filepath.Join(rt, "slicc")
	}
	return filepath.Join(rt, "slicc-"+strconv.Itoa(os.Getuid()))
}

func hgIsolateEnv(t *testing.T) (env []string, home, rt string) {
	t.Helper()
	home = t.TempDir()
	rt, err := os.MkdirTemp("/tmp", "hg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(rt) })
	if n := len(hgCtlDir(rt)) + len("/4294967295.sock"); n > 100 {
		t.Fatalf("control socket path would be %d bytes (limit 100); ctl would fall back to the shared directory", n)
	}
	vars := map[string]string{
		"HOME":               home,
		"USERPROFILE":        home,
		"XDG_CONFIG_HOME":    filepath.Join(home, ".config"),
		"XDG_RUNTIME_DIR":    rt,
		"TMPDIR":             rt,
		"TMP":                rt,
		"TEMP":               rt,
		"AppData":            filepath.Join(home, "AppData", "Roaming"),
		"APPDATA":            filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":       filepath.Join(home, "AppData", "Local"),
		"SLICC_NO_TELEMETRY": "1",
	}
	for k, v := range vars {
		env = append(env, k+"="+v)
	}
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if _, ok := vars[k]; ok || k == helloGateBinaryEnv {
			continue
		}
		env = append(env, e)
	}
	return env, home, rt
}

type hgPionPage struct {
	cert        webrtc.Certificate
	pc          *webrtc.PeerConnection
	dc          *webrtc.DataChannel
	mu          sync.Mutex
	recs        int
	bytes       int
	hello       bool
	raw         []byte
	answerFP    string
	ready       chan struct{}
	dtls        chan struct{}
	dtlsOnce    sync.Once
	flushAnswer func()
	sigMu       sync.Mutex
	remoteReady bool
	pendingICE  []hubCandidate
}

func (p *hgPionPage) noteRaw(msg webrtc.DataChannelMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recs++
	p.bytes += len(msg.Data)
	if msg.IsString && !p.hello {
		p.hello = true
		p.raw = append([]byte(nil), msg.Data...)
		close(p.ready)
	}
}

func (p *hgPionPage) status() (recs, bytes int, hello bool, raw []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.recs, p.bytes, p.hello, append([]byte(nil), p.raw...)
}

func (p *hgPionPage) terminal() bool {
	if p.dc.ReadyState() == webrtc.DataChannelStateClosed || p.dc.ReadyState() == webrtc.DataChannelStateClosing {
		return true
	}
	switch p.pc.ConnectionState() {
	case webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateDisconnected:
		return true
	}
	return false
}

func (p *hgPionPage) delayedHello(text string) {
	deadline := time.Now().Add(hgHelloWindow)
	for time.Now().Before(deadline) {
		if recs, _, _, _ := p.status(); recs > 0 {
			break
		}
		if p.terminal() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = p.dc.SendText(text)
}

func (p *hgPionPage) openLease(ctx context.Context) (string, error) {
	dc, err := p.pc.CreateDataChannel(link.StreamName, nil)
	if err != nil {
		return "", err
	}
	done := make(chan string, 1)
	var once sync.Once
	send := func() {
		once.Do(func() {
			_ = dc.Send(link.EncodeFrame(link.FrameOpen, 1, []byte(`{"kind":"cdp-lease-v1"}`)))
		})
	}
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		f, e := link.DecodeFrame(m.Data)
		if e != nil || f.Type != link.FrameReset {
			return
		}
		select {
		case done <- string(f.Payload):
		default:
		}
	})
	dc.OnOpen(send)
	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		send()
	}
	select {
	case code := <-done:
		return code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func hgAnswerFingerprint(sdp string) string {
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "a=fingerprint:sha-256 "); ok {
			return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), ":", ""))
		}
	}
	return ""
}

type hgPionLeader struct {
	t       *testing.T
	h       *fakeHub
	certs   []webrtc.Certificate
	mu      sync.Mutex
	n       int
	pages   map[string]*hgPionPage
	order   []string
	stopped bool
	jobs    sync.WaitGroup
	errs    []string
}

func newHGPionLeader(t *testing.T, h *fakeHub, tr *fakeTray, certs ...webrtc.Certificate) *hgPionLeader {
	t.Helper()
	if len(certs) == 0 {
		t.Fatal("pion tray leader needs at least one peer certificate")
	}
	l := &hgPionLeader{t: t, h: h, certs: certs, pages: map[string]*hgPionPage{}}
	t.Cleanup(l.stop)
	h.with(func() {
		tr.OfferSDP = ""
		tr.OnJoin = func(b *hubBootstrap) { l.spawn(func() { l.offer(b) }) }
		tr.OnFollower = func(action string, b *hubBootstrap, body map[string]any) { l.onFollower(action, b, body) }
	})
	return l
}

func (l *hgPionLeader) spawn(fn func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped {
		return
	}
	l.jobs.Add(1)
	go func() {
		defer l.jobs.Done()
		fn()
	}()
}

func (l *hgPionLeader) fail(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.stopped {
		l.errs = append(l.errs, fmt.Sprintf(format, args...))
	}
}

func (l *hgPionLeader) problems() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.errs)
}

func (l *hgPionLeader) check(t *testing.T) {
	t.Helper()
	if errs := l.problems(); len(errs) > 0 {
		t.Fatalf("fake page signalling: %s", strings.Join(errs, "; "))
	}
}

func (l *hgPionLeader) stop() {
	l.mu.Lock()
	l.stopped = true
	pages := make([]*hgPionPage, 0, len(l.pages))
	for _, p := range l.pages {
		pages = append(pages, p)
	}
	l.mu.Unlock()
	l.jobs.Wait()
	for _, p := range pages {
		_ = p.pc.Close()
	}
}

func (l *hgPionLeader) nextCert() webrtc.Certificate {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := min(l.n, len(l.certs)-1)
	l.n++
	return l.certs[i]
}

func (l *hgPionLeader) page(nonce string) *hgPionPage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pages[nonce]
}

func (l *hgPionLeader) pagesFor(t *testing.T, cert webrtc.Certificate) []*hgPionPage {
	t.Helper()
	want := hgFingerprint(t, cert)
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*hgPionPage
	for _, id := range l.order {
		if p := l.pages[id]; p != nil && hgFingerprint(t, p.cert) == want {
			out = append(out, p)
		}
	}
	return out
}

func (l *hgPionLeader) waitPage(t *testing.T, cert webrtc.Certificate, timeout time.Duration) *hgPionPage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if pages := l.pagesFor(t, cert); len(pages) > 0 {
			return pages[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: no page with the expected certificate was offered to the follower; signalling: %v", helloGatePhaseMark, l.problems())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (l *hgPionLeader) waitReadyPage(t *testing.T, cert webrtc.Certificate, timeout time.Duration) *hgPionPage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, p := range l.pagesFor(t, cert) {
			select {
			case <-p.ready:
				return p
			default:
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: no page with the expected certificate received the follower HELLO; signalling: %v", helloGatePhaseMark, l.problems())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (l *hgPionLeader) offer(b *hubBootstrap) {
	cert := l.nextCert()
	api := link.NewAPI(link.APIOptions{Loopback: true})
	pc, err := api.NewPeerConnection(webrtc.Configuration{Certificates: []webrtc.Certificate{cert}})
	if err != nil {
		l.fail("page peer: %v", err)
		return
	}
	dc, err := pc.CreateDataChannel(link.ControlName, nil)
	if err != nil {
		_ = pc.Close()
		l.fail("control channel: %v", err)
		return
	}
	page := &hgPionPage{cert: cert, pc: pc, dc: dc, ready: make(chan struct{}), dtls: make(chan struct{})}
	pc.SCTP().Transport().OnStateChange(func(st webrtc.DTLSTransportState) {
		if st == webrtc.DTLSTransportStateConnected {
			page.dtlsOnce.Do(func() { close(page.dtls) })
		}
	})
	hello, _ := json.Marshal(link.Hello{V: link.Version, Role: link.RolePage, Name: "seven", Caps: []string{link.LeaseCap}})
	dc.OnMessage(page.noteRaw)
	var helloOnce sync.Once
	startHello := func() { helloOnce.Do(func() { go page.delayedHello(string(hello)) }) }
	dc.OnOpen(startHello)
	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		startHello()
	}

	var earlyMu sync.Mutex
	var early []hubCandidate
	answered := false
	page.flushAnswer = func() {
		earlyMu.Lock()
		defer earlyMu.Unlock()
		if answered {
			return
		}
		answered = true
		pending := early
		early = nil
		l.h.with(func() {
			if b.State == "failed" {
				return
			}
			for i := range pending {
				cand := pending[i]
				appendEvent(b, hubEvent{Type: "bootstrap.ice_candidate", Candidate: &cand})
			}
		})
	}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		cand := hubCandidate{Candidate: init.Candidate, SDPMid: init.SDPMid}
		if init.SDPMLineIndex != nil {
			idx := int(*init.SDPMLineIndex)
			cand.SDPMLineIndex = &idx
		}
		earlyMu.Lock()
		defer earlyMu.Unlock()
		if !answered {
			early = append(early, cand)
			return
		}
		l.h.with(func() {
			if b.State == "failed" {
				return
			}
			appendEvent(b, hubEvent{Type: "bootstrap.ice_candidate", Candidate: &cand})
		})
	})
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		_ = pc.Close()
		return
	}
	l.pages[b.ID] = page
	l.order = append(l.order, b.ID)
	l.mu.Unlock()
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		l.fail("offer: %v", err)
		return
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		l.fail("local offer: %v", err)
		return
	}
	l.h.with(func() {
		if b.State == "failed" {
			return
		}
		appendEvent(b, hubEvent{Type: "bootstrap.offer", Offer: map[string]string{"type": "offer", "sdp": offer.SDP}})
		b.State = "offered"
	})
}

func hgCandidateInit(c hubCandidate) webrtc.ICECandidateInit {
	init := webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SDPMid}
	if c.SDPMLineIndex != nil {
		idx := uint16(*c.SDPMLineIndex)
		init.SDPMLineIndex = &idx
	}
	return init
}

func (l *hgPionLeader) onFollower(action string, b *hubBootstrap, body map[string]any) {
	page := l.page(b.ID)
	if page == nil {
		return
	}
	switch action {
	case "answer":
		answer, _ := body["answer"].(map[string]any)
		sdp, _ := answer["sdp"].(string)
		l.spawn(func() {
			page.sigMu.Lock()
			defer page.sigMu.Unlock()
			page.mu.Lock()
			page.answerFP = hgAnswerFingerprint(sdp)
			page.mu.Unlock()
			if err := page.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
				l.fail("remote answer: %v", err)
				return
			}
			page.remoteReady = true
			for _, c := range page.pendingICE {
				if err := page.pc.AddICECandidate(hgCandidateInit(c)); err != nil && !page.terminal() {
					l.fail("queued follower candidate: %v", err)
				}
			}
			page.pendingICE = nil
			l.spawn(page.flushAnswer)
		})
	case "ice-candidate":
		raw, _ := json.Marshal(body["candidate"])
		var c hubCandidate
		if json.Unmarshal(raw, &c) != nil || c.Candidate == "" {
			return
		}
		l.spawn(func() {
			page.sigMu.Lock()
			defer page.sigMu.Unlock()
			if !page.remoteReady {
				page.pendingICE = append(page.pendingICE, c)
				return
			}
			if err := page.pc.AddICECandidate(hgCandidateInit(c)); err != nil && !page.terminal() {
				l.fail("follower candidate: %v", err)
			}
		})
	}
}

type hgFollowerFix struct {
	env      []string
	home     string
	rt       string
	hub      *fakeHub
	tray     *fakeTray
	endpoint string
	label    string
	leader   *hgPionLeader
	log      *syncBuffer
	cmd      *exec.Cmd
	bin      string
	root     string
	pinCert  webrtc.Certificate
}

func hgFollowerSetup(t *testing.T, bin string, pinCert webrtc.Certificate, peerCerts ...webrtc.Certificate) *hgFollowerFix {
	t.Helper()
	if bin == "" {
		t.Fatal("no slicc binary: the follower rows run under the integration TestMain or with a driver-supplied binary")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("slicc binary: %v", err)
	}
	if len(peerCerts) == 0 {
		t.Fatal("the follower rows need at least one peer certificate")
	}
	fx := &hgFollowerFix{label: "hgpeer", endpoint: hgFakeLeaseBrowser(t), bin: bin, root: t.TempDir(), pinCert: pinCert}
	fx.env, fx.home, fx.rt = hgIsolateEnv(t)
	fx.hub = newFakeHub(t)
	fx.tray = fx.hub.addTray("hg")
	fx.leader = newHGPionLeader(t, fx.hub, fx.tray, peerCerts...)
	pair := exec.Command(fx.bin, "lease", "pair", "--link", fx.label, "--peer", hgFollowerPin(t, pinCert))
	pair.Env = fx.env
	if out, err := pair.CombinedOutput(); err != nil {
		t.Fatalf("lease pair: %v %s", err, out)
	}
	return fx
}

func (fx *hgFollowerFix) start(t *testing.T) {
	t.Helper()
	cmd := exec.Command(fx.bin, "-", "follow", "--lease-browser", fx.endpoint, "--name", fx.label)
	cmd.Env = fx.env
	cmd.Stdin = strings.NewReader(fx.hub.joinURL(fx.tray) + "\n")
	log := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fx.log, fx.cmd = log, cmd
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Log(log.String())
		}
	})
}

func (fx *hgFollowerFix) handshake(t *testing.T, page *hgPionPage) {
	t.Helper()
	select {
	case <-page.dtls:
	case <-time.After(hgHandshakeBound):
		t.Fatalf("%s: the page's DTLS transport never connected (follower output so far: %d bytes); signalling: %v", helloGatePhaseMark, len(fx.log.String()), fx.leader.problems())
	}
	der := page.pc.SCTP().Transport().GetRemoteCertificate()
	page.mu.Lock()
	want := page.answerFP
	page.mu.Unlock()
	sum := sha256.Sum256(der)
	if len(der) == 0 || want == "" || hex.EncodeToString(sum[:]) != want {
		t.Fatalf("%s: the page's DTLS peer is not the follower that answered (leaf %d bytes, answer fingerprint present %v)", helloGatePhaseMark, len(der), want != "")
	}
}

const (
	hgMismatch = "paired fingerprint mismatch"
	hgGeneric  = "lease peer did not establish an exclusive link"
	hgLinked   = "linked to "
)

func (fx *hgFollowerFix) outcome(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(hgOutcomeBound)
	for {
		text := fx.log.String()
		best, at := "", -1
		for _, w := range []string{hgMismatch, hgGeneric, hgLinked} {
			if i := strings.Index(text, w); i >= 0 && (at < 0 || i < at) {
				best, at = w, i
			}
		}
		if best != "" {
			return best
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the follower reported neither a refusal nor a link within %s", helloGatePhaseMark, hgOutcomeBound)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (fx *hgFollowerFix) waitAfter(t *testing.T, first, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		text := fx.log.String()
		if i := strings.Index(text, first); i >= 0 && strings.Contains(text[i+len(first):], want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the follower did not report %q after %q within %s", want, first, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (fx *hgFollowerFix) transcript(t *testing.T, peers ...webrtc.Certificate) {
	t.Helper()
	text := fx.log.String()
	if text == "" {
		t.Fatal("the follower printed nothing; the transcript check has no input")
	}
	certs := append([]webrtc.Certificate{fx.pinCert}, peers...)
	for _, c := range certs {
		fp := hgFingerprint(t, c)
		for _, form := range []string{fp, strings.ToLower(fp), strings.ToUpper(fp), strings.ToUpper(hgFollowerPin(t, c)), hgFollowerPin(t, c)} {
			if strings.Contains(text, form) {
				t.Fatal("the follower transcript contains a certificate fingerprint")
			}
		}
	}
	for _, bad := range []string{"-----BEGIN", "-----END", "CERTIFICATE-----", "PRIVATE KEY"} {
		if strings.Contains(text, bad) {
			t.Fatalf("the follower transcript contains PEM text %q", bad)
		}
	}
	if strings.Contains(text, fx.hub.joinURL(fx.tray)) || strings.Contains(text, fx.tray.Token) || strings.Contains(text, "/join/") {
		t.Fatal("the follower transcript contains the join URL")
	}
	for _, p := range []string{fx.root, fx.home, fx.rt} {
		if strings.Contains(text, p) {
			t.Fatalf("the follower transcript contains the fixture path %q", p)
		}
		if real, err := filepath.EvalSymlinks(p); err == nil && strings.Contains(text, real) {
			t.Fatalf("the follower transcript contains the fixture path %q", real)
		}
	}
}

func (fx *hgFollowerFix) brokerEmpty(t *testing.T) {
	t.Helper()
	sock := filepath.Join(hgCtlDir(fx.rt), strconv.Itoa(fx.cmd.Process.Pid)+".sock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the follower's control socket is not in the private control directory")
		}
		time.Sleep(50 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := ctl.Call(ctx, sock, ctl.Request{Op: ctl.OpLeaseList})
	if err != nil || !r.OK {
		t.Fatalf("lease list over the control socket: %v %+v", err, r)
	}
	var pending, active []json.RawMessage
	if json.Unmarshal(r.Pending, &pending) != nil || json.Unmarshal(r.Active, &active) != nil {
		t.Fatalf("lease list reply pending=%s active=%s", r.Pending, r.Active)
	}
	if len(pending) != 0 || len(active) != 0 {
		t.Fatalf("broker after a refusal has pending=%d active=%d, want 0 and 0", len(pending), len(active))
	}
}

func hgPinFilePath(fx *hgFollowerFix) string {
	base := filepath.Join(fx.home, ".config")
	if runtime.GOOS == "darwin" {
		base = filepath.Join(fx.home, "Library", "Application Support")
	}
	return filepath.Join(base, "slicc", "lease-pins", fx.label)
}

func hgDefenceBinary(t *testing.T, name string, variant []helloGateEdit) string {
	t.Helper()
	if bin := os.Getenv(helloGateBinaryEnv); bin != "" {
		return bin
	}
	return helloGateMutatedBinary(t, name, variant)
}

func hgPageClosed(page *hgPionPage, bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if page.terminal() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return page.terminal()
}

func TestP3FollowerMismatchSendsNothing(t *testing.T) {
	good, bad := hgFollowerCert(t), hgFollowerCert(t)
	fx := hgFollowerSetup(t, helloGateFollowerBin(), good, bad)
	fx.start(t)
	page := fx.leader.waitPage(t, bad, 20*time.Second)
	fx.handshake(t, page)
	switch got := fx.outcome(t); got {
	case hgLinked:
		t.Fatal("the follower linked to a peer whose certificate is not the paired one")
	case hgGeneric:
		t.Fatalf("the refusal surfaced as %q instead of %q", hgGeneric, hgMismatch)
	}
	recs, bytes, hello, _ := page.status()
	if recs != 0 || bytes != 0 || hello {
		t.Fatalf("peer saw records=%d bytes=%d hello=%v; R6 baseline was records=1 bytes=150 hello=true", recs, bytes, hello)
	}
	fx.brokerEmpty(t)
	closed := hgPageClosed(page, 3*time.Second)
	fx.leader.check(t)
	fx.transcript(t, bad)
	t.Logf("ci-proof TestP3FollowerMismatchSendsNothing records=%d bytes=%d hello=%v page_observed_close=%v r6_baseline_records=1 r6_baseline_bytes=150", recs, bytes, hello, closed)
}

func TestP3FollowerMatchUnchanged(t *testing.T) {
	pageCert := hgFollowerCert(t)
	fx := hgFollowerSetup(t, helloGateFollowerBin(), pageCert, pageCert)
	fx.start(t)
	page := fx.leader.waitPage(t, pageCert, 20*time.Second)
	fx.handshake(t, page)
	if got := fx.outcome(t); got != hgLinked {
		t.Fatalf("the follower reported %q, want a link to the paired peer", got)
	}
	page = fx.leader.waitReadyPage(t, pageCert, 10*time.Second)
	_, _, _, raw := page.status()
	var got link.Hello
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Node == "" {
		t.Fatal("HELLO has no node id")
	}
	want := `{"v":1,"role":"node","name":"hgpeer","host":"hgpeer","version":"1.2.3-test","mode":"remote","node":"` + got.Node + `","caps":["cdp-lease-v1"]}`
	if string(raw) != want {
		t.Fatalf("HELLO bytes %s, want literal %s", raw, want)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := page.openLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code != "E_INVALID" {
		t.Fatalf("lease OPEN reset %q, want E_INVALID from the broker (a bare OPEN reaches dispatch only)", code)
	}
	if recs, bytes, _, _ := page.status(); recs != 1 || bytes != len(want) {
		t.Fatalf("control channel carried records=%d bytes=%d, want exactly the one HELLO record of %d bytes", recs, bytes, len(want))
	}
	fx.leader.check(t)
	fx.transcript(t, pageCert)
}

func TestP3FollowerRelinkAcceptsLegitimateAfterRefused(t *testing.T) {
	good, bad := hgFollowerCert(t), hgFollowerCert(t)
	fx := hgFollowerSetup(t, helloGateFollowerBin(), good, bad, good)
	fx.start(t)
	refused := fx.leader.waitPage(t, bad, 20*time.Second)
	fx.handshake(t, refused)
	if got := fx.outcome(t); got != hgMismatch {
		t.Fatalf("the first peer produced %q, want %q", got, hgMismatch)
	}
	goodPage := fx.leader.waitPage(t, good, 30*time.Second)
	fx.handshake(t, goodPage)
	fx.waitAfter(t, hgMismatch, hgLinked, 20*time.Second)
	goodPage = fx.leader.waitReadyPage(t, good, 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := goodPage.openLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code != "E_INVALID" {
		t.Fatalf("legitimate peer lease OPEN reset %q, want E_INVALID (a bare OPEN reaches dispatch only)", code)
	}
	if recs, bytes, hello, _ := refused.status(); recs != 0 || bytes != 0 || hello {
		t.Fatalf("the refused peer saw records=%d bytes=%d hello=%v", recs, bytes, hello)
	}
	fx.leader.check(t)
	fx.transcript(t, bad, good)
}

func TestP3FollowerPostReadyCheckStillHolds(t *testing.T) {
	bin := hgDefenceBinary(t, "PostReadyCheckStillHolds", helloGatePostReadyVariant)
	good, bad := hgFollowerCert(t), hgFollowerCert(t)
	fx := hgFollowerSetup(t, bin, good, bad)
	fx.start(t)
	page := fx.leader.waitPage(t, bad, 20*time.Second)
	fx.handshake(t, page)
	select {
	case <-page.ready:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: the gate-less build sent no HELLO, so the handshake never reached the post-Ready check", helloGatePhaseMark)
	}
	switch got := fx.outcome(t); got {
	case hgLinked:
		t.Fatal("the post-Ready check did not refuse the mismatched peer: the follower linked")
	case hgGeneric:
		t.Fatalf("%s: the link failed before Ready (%q), so the post-Ready check was not exercised", helloGatePhaseMark, got)
	}
	fx.leader.check(t)
	fx.transcript(t, bad)
}

func TestP3FollowerStreamAuthStillHolds(t *testing.T) {
	bin := hgDefenceBinary(t, "StreamAuthStillHolds", helloGateStreamAuthVariant)
	pageCert, pinCert := hgFollowerCert(t), hgFollowerCert(t)
	fx := hgFollowerSetup(t, bin, pinCert, pageCert)
	fx.start(t)
	page := fx.leader.waitPage(t, pageCert, 20*time.Second)
	fx.handshake(t, page)
	if got := fx.outcome(t); got != hgLinked {
		t.Fatalf("%s: the gate-bypassed build reported %q, so no lease stream can be opened", helloGatePhaseMark, got)
	}
	page = fx.leader.waitReadyPage(t, pageCert, 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := page.openLease(ctx)
	if err != nil {
		t.Fatalf("%s: no RESET for the lease OPEN: %v", helloGatePhaseMark, err)
	}
	switch code {
	case "E_AUTH":
	case "E_INVALID":
		t.Fatal("lease OPEN from a peer that is not the paired one reached the broker (E_INVALID), want E_AUTH")
	default:
		t.Fatalf("%s: lease OPEN reset %q, neither E_AUTH nor the broker's E_INVALID", helloGatePhaseMark, code)
	}
	fx.leader.check(t)
	fx.transcript(t, pageCert)
}

func TestP3FollowerPinFileUnchanged(t *testing.T) {
	good, bad := hgFollowerCert(t), hgFollowerCert(t)
	fx := hgFollowerSetup(t, helloGateFollowerBin(), good, bad)
	path := hgPinFilePath(fx)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the pin file is not where production keeps it: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	fx.start(t)
	page := fx.leader.waitPage(t, bad, 20*time.Second)
	fx.handshake(t, page)
	if got := fx.outcome(t); got != hgMismatch {
		t.Fatalf("the refusal produced %q, want %q", got, hgMismatch)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || !info.ModTime().Equal(again.ModTime()) || info.Mode() != again.Mode() {
		t.Fatal("the pin file changed during a real refusal")
	}
	fx.leader.check(t)
	fx.transcript(t, bad)
}
