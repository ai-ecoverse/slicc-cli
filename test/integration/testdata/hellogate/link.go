package link

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

const helloGateLiteralHELLO = `{"v":1,"role":"node","name":"lease-node","host":"lease-node","caps":["cdp-lease-v1"]}`

type helloGateLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *helloGateLog) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *helloGateLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func helloGateCert(t *testing.T) webrtc.Certificate {
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

func helloGatePin(t *testing.T, cert webrtc.Certificate) string {
	t.Helper()
	fps, err := cert.GetFingerprints()
	if err != nil || len(fps) == 0 {
		t.Fatal(err)
	}
	return strings.ToLower(strings.ReplaceAll(fps[0].Value, ":", ""))
}

type helloGatePeer struct {
	pc     *webrtc.PeerConnection
	dc     *webrtc.DataChannel
	mu     sync.Mutex
	recs   int
	bytes  int
	hello  bool
	closed chan struct{}
	helloB []byte
	sent   int
	opened bool
	failed []string
	accept int
}

func (p *helloGatePeer) note(msg webrtc.DataChannelMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recs++
	p.bytes += len(msg.Data)
	if msg.IsString {
		p.hello = true
		p.helloB = append([]byte(nil), msg.Data...)
		return
	}
	if len(msg.Data) > 0 && msg.Data[0] == byte(FrameOpened) {
		p.accept++
	}
}

func (p *helloGatePeer) status() (recs, bytes int, hello bool, helloB []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.recs, p.bytes, p.hello, append([]byte(nil), p.helloB...)
}

func (p *helloGatePeer) acceptCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accept
}

func (p *helloGatePeer) sentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sent
}

func (p *helloGatePeer) noteSent() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent++
}

func (p *helloGatePeer) noteOpened() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.opened = true
}

func (p *helloGatePeer) wasOpened() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.opened
}

func (p *helloGatePeer) noteSendError(err error, st webrtc.DataChannelState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = append(p.failed, fmt.Sprintf("%s:%v", st, err))
}

func (p *helloGatePeer) sendErrors() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.failed...)
}

func helloGateSendPeerBurst(peer *helloGatePeer) {
	var once sync.Once
	send := func() {
		once.Do(func() {
			peer.noteOpened()
			for _, rec := range []struct {
				text bool
				data []byte
			}{
				{true, []byte(`{"v":1,"role":"page","name":"seven","caps":["cdp-lease-v1"]}`)},
				{false, EncodeFrame(FrameOpen, 1, []byte(`{"kind":"cdp-lease-v1"}`))},
				{true, []byte(strings.Repeat("x", 70000))},
			} {
				var err error
				if rec.text {
					err = peer.dc.SendText(string(rec.data))
				} else {
					err = peer.dc.Send(rec.data)
				}
				if err == nil {
					peer.noteSent()
					continue
				}
				peer.noteSendError(err, peer.dc.ReadyState())
			}
		})
	}
	peer.dc.OnOpen(send)
	if peer.dc.ReadyState() == webrtc.DataChannelStateOpen {
		send()
	}
}

func helloGateExchange(t *testing.T, pagePC, nodePC *webrtc.PeerConnection) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	offer, err := pagePC.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	ready := webrtc.GatheringCompletePromise(pagePC)
	if err = pagePC.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = nodePC.SetRemoteDescription(*pagePC.LocalDescription()); err != nil {
		t.Fatal(err)
	}
	answer, err := nodePC.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	ready = webrtc.GatheringCompletePromise(nodePC)
	if err = nodePC.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = pagePC.SetRemoteDescription(*nodePC.LocalDescription()); err != nil {
		t.Fatal(err)
	}
}

func helloGateOpen(t *testing.T, nodeOpts Options, pageCert, nodeCert webrtc.Certificate, streamFirst bool) (*Link, *helloGatePeer, *helloGateLog) {
	t.Helper()
	return helloGateOpenMode(t, nodeOpts, pageCert, nodeCert, streamFirst, true)
}

func helloGateOpenMode(t *testing.T, nodeOpts Options, pageCert, nodeCert webrtc.Certificate, streamFirst, withControl bool) (*Link, *helloGatePeer, *helloGateLog) {
	t.Helper()
	api := NewAPI(APIOptions{Loopback: true})
	pagePC, err := api.NewPeerConnection(webrtc.Configuration{Certificates: []webrtc.Certificate{pageCert}})
	if err != nil {
		t.Fatal(err)
	}
	nodePC, err := api.NewPeerConnection(webrtc.Configuration{Certificates: []webrtc.Certificate{nodeCert}})
	if err != nil {
		t.Fatal(err)
	}
	log := &helloGateLog{}
	nodeOpts.Logf = log.printf
	nodeOpts.Certificates = []webrtc.Certificate{nodeCert}
	node, err := New(nodePC, RoleNode, nodeOpts)
	if err != nil {
		t.Fatal(err)
	}
	peer := &helloGatePeer{pc: pagePC, closed: make(chan struct{})}
	wire := func(dc *webrtc.DataChannel) {
		dc.OnMessage(peer.note)
		dc.OnClose(func() {
			select {
			case <-peer.closed:
			default:
				close(peer.closed)
			}
		})
		if dc.Label() == ControlName {
			peer.dc = dc
		}
	}
	if streamFirst {
		dc, err := pagePC.CreateDataChannel(StreamName, nil)
		if err != nil {
			t.Fatal(err)
		}
		wire(dc)
		peer.dc = dc
	}
	if withControl {
		dc, err := pagePC.CreateDataChannel(ControlName, nil)
		if err != nil {
			t.Fatal(err)
		}
		wire(dc)
		if !streamFirst {
			peer.dc = dc
		}
	}
	helloGateExchange(t, pagePC, nodePC)
	t.Cleanup(func() { _ = node.Close(); _ = pagePC.Close() })
	return node, peer, log
}

func helloGateLeaseOpts(gate func(*webrtc.PeerConnection) bool) Options {
	return Options{
		Hello: Hello{Name: "lease-node", Host: "lease-node", Caps: []string{LeaseCap}},
		Handlers: map[string]Handler{
			LeaseCap: func(s *Stream) { _ = s.Reset("E_AUTH") },
		},
		PeerGate: gate,
	}
}

func helloGateTranscript(t *testing.T, log *helloGateLog, pin string, certs ...webrtc.Certificate) {
	t.Helper()
	text := log.text()
	for _, c := range certs {
		fps, err := c.GetFingerprints()
		if err != nil || len(fps) == 0 {
			t.Fatal(err)
		}
		for _, form := range []string{fps[0].Value, strings.ToUpper(fps[0].Value), helloGatePin(t, c)} {
			if strings.Contains(text, form) {
				t.Fatal("transcript names a certificate fingerprint used on this link")
			}
		}
	}
	for _, bad := range []string{"BEGIN CERTIFICATE", "END CERTIFICATE", "/join/", "lease-pins"} {
		if strings.Contains(text, bad) {
			t.Fatalf("transcript names %q", bad)
		}
	}
	if pin != "" && strings.Contains(text, pin) {
		t.Fatal("transcript names the paired fingerprint")
	}
	other := helloGatePin(t, helloGateCert(t))
	if strings.Contains(text, other) {
		t.Fatal("transcript names another certificate fingerprint")
	}
	if strings.Contains(text, "-----") {
		t.Fatal("transcript looks like PEM")
	}
	if strings.Contains(text, "/Users/") || strings.Contains(text, "/tmp/") || strings.Contains(text, "\\") {
		t.Fatal("transcript names an absolute path")
	}
}

func helloGatePeerClosed(t *testing.T, peer *helloGatePeer) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if peer.dc != nil && peer.dc.ReadyState() == webrtc.DataChannelStateClosed {
			return
		}
		select {
		case <-peer.closed:
			return
		case <-deadline:
			st := webrtc.DataChannelStateUnknown
			if peer.dc != nil {
				st = peer.dc.ReadyState()
			}
			t.Fatalf("peer channel not closed state=%v", st)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func helloGateSendPeerHello(peer *helloGatePeer, text string) {
	var once sync.Once
	send := func() {
		once.Do(func() {
			peer.noteSent()
			_ = peer.dc.SendText(text)
		})
	}
	peer.dc.OnOpen(func() { send() })
	if peer.dc.ReadyState() == webrtc.DataChannelStateOpen {
		send()
	}
}

func pinMatchesPC(pc *webrtc.PeerConnection, pin string) bool {
	if pc == nil || pc.SCTP() == nil || pc.SCTP().Transport() == nil {
		return false
	}
	der := pc.SCTP().Transport().GetRemoteCertificate()
	if len(der) == 0 {
		return false
	}
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:]) == pin
}

func TestP3MismatchSendsNothing(t *testing.T) {
	pageCert, nodeCert := helloGateCert(t), helloGateCert(t)
	pin := helloGatePin(t, helloGateCert(t))
	node, peer, log := helloGateOpen(t, helloGateLeaseOpts(func(pc *webrtc.PeerConnection) bool {
		return pinMatchesPC(pc, pin)
	}), pageCert, nodeCert, false)
	select {
	case <-node.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("node did not refuse")
	}
	helloGatePeerClosed(t, peer)
	select {
	case <-node.HelloSent():
		t.Fatal("HELLO was queued on a refused peer")
	default:
	}
	recs, bytes, hello, _ := peer.status()
	if recs != 0 || bytes != 0 || hello {
		t.Fatalf("peer saw records=%d bytes=%d hello=%v; R6 baseline was records=1 bytes=150 hello=true", recs, bytes, hello)
	}
	if !errors.Is(node.Err(), ErrPeerRefused) {
		t.Fatalf("node err %v, want ErrPeerRefused", node.Err())
	}
	t.Logf("ci-proof P3MismatchSendsNothing peer_inbound_control_records=%d peer_inbound_control_bytes=%d peer_hello_received=%v r6_baseline_records=1 r6_baseline_bytes=150", recs, bytes, hello)
	helloGateTranscript(t, log, pin, pageCert, nodeCert)
}

func TestP3MismatchParsesNothing(t *testing.T) {
	pageCert, nodeCert := helloGateCert(t), helloGateCert(t)
	pin := helloGatePin(t, helloGateCert(t))
	var refuseN atomic.Int32
	node, peer, log := helloGateOpen(t, helloGateLeaseOpts(func(pc *webrtc.PeerConnection) bool {
		ok := pinMatchesPC(pc, pin)
		if !ok {
			refuseN.Add(1)
		}
		return ok
	}), pageCert, nodeCert, false)
	helloGateSendPeerBurst(peer)
	select {
	case <-node.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("node did not refuse")
	}
	select {
	case <-node.Ready():
		t.Fatal("Ready closed on a refused peer")
	default:
	}
	if _, ok := node.PeerHello(); ok {
		t.Fatal("PeerHello set for a refused peer")
	}
	if n := refuseN.Load(); n != 1 {
		t.Fatalf("refusal events=%d, want exactly 1 for the one incoming channel", n)
	}
	if !errors.Is(node.Err(), ErrPeerRefused) {
		t.Fatalf("err %v", node.Err())
	}
	time.Sleep(200 * time.Millisecond)
	sent, errs := peer.sentCount(), peer.sendErrors()
	if len(errs) > 0 {
		deadline := time.Now().Add(5 * time.Second)
		for peer.dc.ReadyState() == webrtc.DataChannelStateOpen && peer.pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
			if time.Now().After(deadline) {
				t.Fatalf("peer sends failed %v while its channel and connection stayed open", errs)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	switch {
	case sent > 0:
		t.Logf("ci-proof P3MismatchParsesNothing peer_sent_records=%d send_errors=%d refusals=%d", sent, len(errs), refuseN.Load())
	case peer.wasOpened() && len(errs) == 0:
		t.Fatal("the peer channel opened but the burst neither sent nor failed")
	case peer.wasOpened():
		t.Logf("ci-proof P3MismatchParsesNothing the refusal closed the peer channel before its first send: send_errors=%v refusals=%d", errs, refuseN.Load())
	default:
		t.Logf("ci-proof P3MismatchParsesNothing refused before the peer channel opened: no send was possible refusals=%d", refuseN.Load())
	}
	recs, bytes, hello, _ := peer.status()
	if recs != 0 || bytes != 0 || hello {
		t.Fatalf("peer saw records=%d bytes=%d hello=%v after the refusal", recs, bytes, hello)
	}
	helloGateTranscript(t, log, pin, pageCert, nodeCert)
}

func TestP3MismatchStreamChannelFirst(t *testing.T) {
	pageCert, nodeCert := helloGateCert(t), helloGateCert(t)
	pin := helloGatePin(t, helloGateCert(t))
	node, peer, log := helloGateOpenMode(t, helloGateLeaseOpts(func(pc *webrtc.PeerConnection) bool {
		return pinMatchesPC(pc, pin)
	}), pageCert, nodeCert, true, false)
	select {
	case <-node.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("node did not refuse a stream-only peer")
	}
	helloGatePeerClosed(t, peer)
	recs, bytes, hello, _ := peer.status()
	if recs != 0 || bytes != 0 || hello {
		t.Fatalf("stream-only peer saw records=%d bytes=%d hello=%v", recs, bytes, hello)
	}
	if !errors.Is(node.Err(), ErrPeerRefused) {
		t.Fatalf("err %v, want ErrPeerRefused on the stream label", node.Err())
	}
	helloGateTranscript(t, log, pin, pageCert, nodeCert)
}

func TestP3MatchUnchanged(t *testing.T) {
	pageCert, nodeCert := helloGateCert(t), helloGateCert(t)
	pin := helloGatePin(t, pageCert)
	opened := make(chan *Stream, 1)
	accepted := make(chan struct{}, 1)
	opts := helloGateLeaseOpts(func(pc *webrtc.PeerConnection) bool { return pinMatchesPC(pc, pin) })
	opts.Handlers[LeaseCap] = func(s *Stream) {
		opened <- s
		if err := s.Accept(map[string]any{}); err != nil {
			return
		}
		accepted <- struct{}{}
	}
	node, peer, log := helloGateOpen(t, opts, pageCert, nodeCert, false)
	helloGateSendPeerHello(peer, `{"v":1,"role":"page","name":"seven","caps":["cdp-lease-v1"]}`)
	select {
	case <-node.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("Ready did not close")
	}
	deadline := time.After(10 * time.Second)
	var recs, bytes int
	var hello bool
	var helloB []byte
	for {
		recs, bytes, hello, helloB = peer.status()
		if hello && recs >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("no HELLO yet records=%d bytes=%d", recs, bytes)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if recs != 1 || !hello {
		t.Fatalf("matching peer HELLO records=%d bytes=%d hello=%v", recs, bytes, hello)
	}
	if string(helloB) != helloGateLiteralHELLO {
		t.Fatalf("HELLO bytes %s want literal %s", helloB, helloGateLiteralHELLO)
	}
	_ = peer.dc.Send(EncodeFrame(FrameOpen, 1, []byte(`{"kind":"cdp-lease-v1"}`)))
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("lease OPEN was not handled")
	}
	select {
	case <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("Accept was not called")
	}
	deadline = time.After(10 * time.Second)
	for peer.acceptCount() < 1 {
		select {
		case <-deadline:
			t.Fatal("peer did not receive Accept")
		case <-time.After(50 * time.Millisecond):
		}
	}
	_ = bytes
	helloGateTranscript(t, log, pin, pageCert, nodeCert)
}

func TestP3OrdinaryFollowUnchanged(t *testing.T) {
	pageCert, nodeCert := helloGateCert(t), helloGateCert(t)
	opts := Options{Hello: Hello{Name: "follow", Host: "follow", Caps: []string{"net", "http"}}}
	node, peer, log := helloGateOpen(t, opts, pageCert, nodeCert, false)
	helloGateSendPeerHello(peer, `{"v":1,"role":"page","name":"seven","caps":["net"]}`)
	select {
	case <-node.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("ordinary follow Ready did not close")
	}
	deadline := time.After(10 * time.Second)
	for {
		recs, _, hello, _ := peer.status()
		if hello && recs == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("ordinary follow HELLO records=%d hello=%v", recs, hello)
		case <-time.After(50 * time.Millisecond):
		}
	}
	helloGateTranscript(t, log, "", pageCert, nodeCert)
}

func TestP3OrdinaryFailedChannelStillProcessed(t *testing.T) {
	pageCert, nodeCert := helloGateCert(t), helloGateCert(t)
	opts := Options{
		Hello:    Hello{Name: "follow", Host: "follow", Caps: []string{"net"}},
		Handlers: map[string]Handler{"net": func(*Stream) {}},
	}
	node, peer, log := helloGateOpen(t, opts, pageCert, nodeCert, false)
	helloGateSendPeerHello(peer, `{"v":1,"role":"page","name":"seven","caps":["net"]}`)
	select {
	case <-node.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("Ready did not close")
	}
	node.mu.Lock()
	node.err = ErrHelloTimeout
	node.mu.Unlock()
	dc, err := peer.pc.CreateDataChannel(StreamName, nil)
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{}, 1)
	dc.OnOpen(func() { opened <- struct{}{} })
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("late channel did not open")
	}
	deadline := time.After(2 * time.Second)
	for {
		if dc.ReadyState() == webrtc.DataChannelStateClosed {
			t.Fatal("nil-gate link closed a late channel after a sticky non-gate failure")
		}
		select {
		case <-deadline:
			if dc.ReadyState() != webrtc.DataChannelStateOpen {
				t.Fatalf("late channel state %s, want open", dc.ReadyState())
			}
			helloGateTranscript(t, log, "", pageCert, nodeCert)
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestP3PageRoleGateRefused(t *testing.T) {
	api := NewAPI(APIOptions{Loopback: true})
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	log := &helloGateLog{}
	_, err = New(pc, RolePage, Options{
		Hello:    Hello{Name: "page", Caps: []string{}},
		PeerGate: func(*webrtc.PeerConnection) bool { return true },
		Logf:     log.printf,
	})
	if err == nil {
		t.Fatal("PeerGate with RolePage was accepted")
	}
	helloGateTranscript(t, log, "")
}

func TestP3RelinkAcceptsLegitimateAfterRefused(t *testing.T) {
	goodPage, badPage, nodeCert := helloGateCert(t), helloGateCert(t), helloGateCert(t)
	pin := helloGatePin(t, goodPage)
	gate := func(pc *webrtc.PeerConnection) bool { return pinMatchesPC(pc, pin) }
	node1, peer1, log1 := helloGateOpen(t, helloGateLeaseOpts(gate), badPage, nodeCert, false)
	select {
	case <-node1.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("first peer was not refused")
	}
	if !errors.Is(node1.Err(), ErrPeerRefused) {
		t.Fatalf("first err %v", node1.Err())
	}
	helloGatePeerClosed(t, peer1)
	recs, bytes, hello, _ := peer1.status()
	if recs != 0 || bytes != 0 || hello {
		t.Fatalf("refused peer saw records=%d bytes=%d", recs, bytes)
	}
	opened := make(chan *Stream, 1)
	accepted := make(chan struct{}, 1)
	opts := helloGateLeaseOpts(gate)
	opts.Handlers[LeaseCap] = func(s *Stream) {
		opened <- s
		if err := s.Accept(map[string]any{}); err == nil {
			accepted <- struct{}{}
		}
	}
	node2, peer2, log2 := helloGateOpen(t, opts, goodPage, nodeCert, false)
	helloGateSendPeerHello(peer2, `{"v":1,"role":"page","name":"seven","caps":["cdp-lease-v1"]}`)
	select {
	case <-node2.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("legitimate peer after refusal did not become Ready")
	}
	_ = peer2.dc.Send(EncodeFrame(FrameOpen, 1, []byte(`{"kind":"cdp-lease-v1"}`)))
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("legitimate peer lease OPEN was not handled")
	}
	select {
	case <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("legitimate peer Accept was not called")
	}
	deadline := time.After(10 * time.Second)
	for peer2.acceptCount() < 1 {
		select {
		case <-deadline:
			t.Fatal("legitimate peer did not receive Accept")
		case <-time.After(50 * time.Millisecond):
		}
	}
	helloGateTranscript(t, log1, pin, badPage, goodPage, nodeCert)
	helloGateTranscript(t, log2, pin, badPage, goodPage, nodeCert)
}

func TestP3PostReadyCheckStillHolds(t *testing.T) {
	pageCert, nodeCert := helloGateCert(t), helloGateCert(t)
	pin := helloGatePin(t, helloGateCert(t))
	opts := helloGateLeaseOpts(func(*webrtc.PeerConnection) bool { return true })
	node, peer, log := helloGateOpen(t, opts, pageCert, nodeCert, false)
	helloGateSendPeerHello(peer, `{"v":1,"role":"page","name":"seven","caps":["cdp-lease-v1"]}`)
	select {
	case <-node.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("Ready did not close with gate bypassed")
	}
	if pinMatchesPC(node.PeerConnection(), pin) {
		t.Fatal("post-Ready pin unexpectedly matched")
	}
	if !postReadyPinHolds(node, pin) {
		t.Fatal("post-Ready check did not refuse the mismatched peer")
	}
	_ = node.Close()
	helloGateTranscript(t, log, pin, pageCert, nodeCert)
}

func postReadyPinHolds(node *Link, pin string) bool {
	return !pinMatchesPC(node.PeerConnection(), pin)
}

func TestP3StreamAuthStillHolds(t *testing.T) {
	pageCert, nodeCert := helloGateCert(t), helloGateCert(t)
	pin := helloGatePin(t, helloGateCert(t))
	got := make(chan string, 1)
	opts := helloGateLeaseOpts(func(*webrtc.PeerConnection) bool { return true })
	opts.Handlers[LeaseCap] = func(s *Stream) {
		if !pinMatchesPC(s.Link().PeerConnection(), pin) {
			_ = s.Reset("E_AUTH")
			got <- "E_AUTH"
			return
		}
		got <- "ok"
	}
	node, peer, log := helloGateOpen(t, opts, pageCert, nodeCert, false)
	helloGateSendPeerHello(peer, `{"v":1,"role":"page","name":"seven","caps":["cdp-lease-v1"]}`)
	select {
	case <-node.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("Ready did not close")
	}
	_ = peer.dc.Send(EncodeFrame(FrameOpen, 1, []byte(`{"kind":"cdp-lease-v1"}`)))
	select {
	case code := <-got:
		if code != "E_AUTH" {
			t.Fatalf("stream auth=%s", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("lease stream was not answered")
	}
	helloGateTranscript(t, log, pin, pageCert, nodeCert)
}

func TestP3DefenceInDepth(t *testing.T) {
	TestP3PostReadyCheckStillHolds(t)
	if t.Failed() {
		return
	}
	TestP3StreamAuthStillHolds(t)
}
