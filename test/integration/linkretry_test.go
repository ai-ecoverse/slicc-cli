package integration

import (
	"encoding/json"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

const failedLinkStatus = "link failed ("

type cutConn struct {
	net.PacketConn
	cut atomic.Bool
}

func (c *cutConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, addr, err := c.PacketConn.ReadFrom(p)
		if err != nil || !c.cut.Load() {
			return n, addr, err
		}
	}
}

func (c *cutConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.cut.Load() {
		return len(p), nil
	}
	return c.PacketConn.WriteTo(p, addr)
}

type sevenPeer struct {
	session string
	nonce   string
	conn    *cutConn
	pc      *webrtc.PeerConnection
	l       *link.Link
}

func (p *sevenPeer) cutNetwork() { p.conn.cut.Store(true) }

func (p *sevenPeer) misbehave(t *testing.T) {
	t.Helper()
	if _, err := p.pc.CreateDataChannel(link.ControlName, nil); err != nil {
		t.Fatal(err)
	}
}

type fakeSeven struct {
	f      *crashBrowser
	mu     sync.Mutex
	peers  map[string]*sevenPeer
	inbox  chan signal.Message
	opened atomic.Int32
}

func newFakeSeven(t *testing.T, f *crashBrowser) *fakeSeven {
	t.Helper()
	s := &fakeSeven{f: f, peers: map[string]*sevenPeer{}, inbox: make(chan signal.Message, 256)}
	prefix := "globalThis." + signal.ReceiverName + "?.("
	f.mu.Lock()
	f.onEval = func(expr string) {
		if !strings.HasPrefix(expr, prefix) || !strings.HasSuffix(expr, ")") {
			return
		}
		m, err := signal.Parse([]byte(strings.TrimSuffix(strings.TrimPrefix(expr, prefix), ")")))
		if err == nil {
			s.inbox <- m
		}
	}
	f.mu.Unlock()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case m := <-s.inbox:
				s.mu.Lock()
				p := s.peers[m.Nonce]
				s.mu.Unlock()
				if p == nil {
					continue
				}
				switch m.T {
				case signal.TypeAnswer:
					_ = p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
				case signal.TypeCandidate:
					if m.Candidate != nil {
						_ = p.pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate.Candidate, SDPMid: m.Candidate.SDPMid, SDPMLineIndex: m.Candidate.SDPMLineIndex, UsernameFragment: m.Candidate.UsernameFragment})
					}
				}
			case <-done:
				return
			}
		}
	}()
	return s
}

func (s *fakeSeven) post(session string, ctxID int, m map[string]any) {
	payload, _ := json.Marshal(m)
	s.f.event(session, "Runtime.bindingCalled", map[string]any{"name": signal.BindingName, "payload": string(payload), "executionContextId": ctxID})
}

func (s *fakeSeven) badOffer(t *testing.T, n int) (string, string) {
	t.Helper()
	session, nonce := s.f.waitHelloWithin(t, n, 40*time.Second)
	s.post(session, s.f.lastContext(), map[string]any{"t": "offer", "nonce": nonce, "sdp": "v=0"})
	return session, nonce
}

func (s *fakeSeven) link(t *testing.T, n int) *sevenPeer {
	t.Helper()
	session, nonce := s.f.waitHelloWithin(t, n, 40*time.Second)
	return s.linkWith(t, n, session, nonce)
}

func (s *fakeSeven) linkWith(t *testing.T, n int, session, nonce string) *sevenPeer {
	t.Helper()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn := &cutConn{PacketConn: udp}
	mux := ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: conn})
	se := webrtc.SettingEngine{}
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	se.SetIncludeLoopbackCandidate(true)
	se.SetICEUDPMux(mux)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	handlers := map[string]link.Handler{"agent": func(st *link.Stream) {
		s.opened.Add(1)
		_ = st.Accept(map[string]any{"head": ""})
		<-st.Done()
	}}
	l, err := link.New(pc, link.RolePage, link.Options{Hello: link.Hello{Name: "fake seven", Caps: []string{"agent"}}, Handlers: handlers})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = l.Close()
		_ = mux.Close()
		_ = udp.Close()
	})
	p := &sevenPeer{session: session, nonce: nonce, conn: conn, pc: pc, l: l}
	s.mu.Lock()
	s.peers[nonce] = p
	s.mu.Unlock()
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered
	s.post(session, s.f.lastContext(), map[string]any{"t": "offer", "nonce": nonce, "sdp": pc.LocalDescription().SDP})
	select {
	case <-l.Ready():
	case <-l.Done():
		t.Fatalf("link #%d closed before it came up: %v", n, l.Err())
	case <-time.After(20 * time.Second):
		t.Fatalf("link #%d never came up", n)
	}
	return p
}

func (s *fakeSeven) noHello(t *testing.T, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		s.f.mu.Lock()
		got := len(s.f.hellos)
		s.f.mu.Unlock()
		if got >= n {
			t.Fatalf("hello #%d arrived: %v", n, s.f.callList())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitOutput(t *testing.T, p *crashProc, want string, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for strings.Count(p.logs.String(), want) < n {
		if time.Now().After(deadline) {
			t.Fatalf("output has %q fewer than %d times:\n%s", want, n, p.logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func setUpAgainAfterFailure(t *testing.T, f *crashBrowser, s *fakeSeven, p *crashProc, up string, fail func(*sevenPeer), status string) {
	t.Helper()
	first := s.link(t, 1)
	waitOutput(t, p, up, 1, 5*time.Second)
	enables, attaches := f.count("Runtime.enable"), f.count("Target.attachToTarget")
	fail(first)
	session, nonce := f.waitHelloWithin(t, 2, 40*time.Second)
	if nonce == first.nonce {
		t.Fatal("the same nonce for the new hello")
	}
	if session != first.session {
		t.Fatalf("the new hello went to session %s, want the same tab's session %s", session, first.session)
	}
	if n := f.count("Runtime.enable") - enables; n != 0 {
		t.Fatalf("slicc set the tab up again (%d Runtime.enable) instead of greeting the same context", n)
	}
	if n := f.count("Target.attachToTarget") - attaches; n != 0 {
		t.Fatalf("slicc attached %d more times", n)
	}
	if n := f.count("Page.reload"); n != 0 {
		t.Fatalf("slicc reloaded the tab %d times", n)
	}
	waitOutput(t, p, status, 1, 5*time.Second)
	s.post(first.session, f.lastContext(), map[string]any{"t": "offer", "nonce": first.nonce, "sdp": "v=0"})
	second := s.link(t, 2)
	if second.nonce != nonce {
		t.Fatalf("linked nonce %q, want %q", second.nonce, nonce)
	}
	if out := p.logs.String(); strings.Contains(out, "could not answer seven") {
		t.Fatalf("slicc answered an offer with the old nonce:\n%s", out)
	}
	select {
	case <-p.exited:
		t.Fatalf("slicc exited: %v\n%s", p.err, p.logs.String())
	default:
	}
}

func TestAttachSetsUpALinkAgainAfterTheNetworkDrops(t *testing.T) {
	f := newCrashBrowser(t)
	s := newFakeSeven(t, f)
	p := startCrashBinary(t, "attach", "--no-kernel", "--no-jerk", "--cdp", f.srv.URL)
	up := "link up with \"fake seven\"\n"
	setUpAgainAfterFailure(t, f, s, p, up, (*sevenPeer).cutNetwork, "link failed (link: connection failed during ICE: no candidate pair); asking seven to set it up again in 1s\n")
	waitOutput(t, p, up, 2, 5*time.Second)
}

func TestTheDefaultFlowSetsUpALinkAgainAfterItFails(t *testing.T) {
	f := newCrashBrowser(t)
	s := newFakeSeven(t, f)
	id := "cdp:" + strings.TrimPrefix(f.srv.URL, "http://")
	p := startCrashBinary(t, "--cdp", f.srv.URL, "--browser", id, "--plain", "--yes", "--no-kernel", "--no-jerk")
	up := "linked with \"fake seven\""
	setUpAgainAfterFailure(t, f, s, p, up, func(sp *sevenPeer) { sp.misbehave(t) }, "link failed (link: protocol violation: a second control channel); asking seven to set it up again in 1s\n")
	waitOutput(t, p, up, 2, 5*time.Second)
}

func TestAgentWatchGetsALinkBackAfterItFails(t *testing.T) {
	f := newCrashBrowser(t)
	s := newFakeSeven(t, f)
	p := startCrashBinary(t, "watch", "--cdp", f.srv.URL, "--plain")
	first := s.link(t, 1)
	waitOpened(t, s, 1)
	first.misbehave(t)
	second := s.link(t, 2)
	if second.nonce == first.nonce || second.session != first.session {
		t.Fatalf("second link %s/%s, first %s/%s", second.session, second.nonce, first.session, first.nonce)
	}
	waitOpened(t, s, 2)
	waitOutput(t, p, "watching fake seven", 2, 10*time.Second)
}

func waitOpened(t *testing.T, s *fakeSeven, n int32) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for s.opened.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d agent streams, want %d", s.opened.Load(), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAFailedLinkBacksOffAndALinkResetsTheWait(t *testing.T) {
	f := newCrashBrowser(t)
	s := newFakeSeven(t, f)
	p := startCrashBinary(t, "attach", "--no-kernel", "--no-jerk", "--cdp", f.srv.URL)
	first := s.link(t, 1)
	waitOutput(t, p, "link up with \"fake seven\"\n", 1, 5*time.Second)
	first.misbehave(t)
	var at []time.Time
	for n := 2; n <= 4; n++ {
		s.badOffer(t, n)
		at = append(at, time.Now())
	}
	if gap := at[1].Sub(at[0]); gap < 1800*time.Millisecond || gap > 3500*time.Millisecond {
		t.Fatalf("second retry after %s, want about 2s", gap)
	}
	if gap := at[2].Sub(at[1]); gap < 3800*time.Millisecond || gap > 5500*time.Millisecond {
		t.Fatalf("third retry after %s, want about 4s", gap)
	}
	waitOutput(t, p, "asking seven to set it up again in 8s\n", 1, 5*time.Second)
	linked := s.link(t, 5)
	waitOutput(t, p, "link up with \"fake seven\"\n", 2, 5*time.Second)
	linked.misbehave(t)
	start := time.Now()
	f.waitHelloWithin(t, 6, 10*time.Second)
	if gap := time.Since(start); gap > 2500*time.Millisecond {
		t.Fatalf("retry after a link that came up took %s, want about 1s", gap)
	}
	out := p.logs.String()
	for _, want := range []string{"again in 1s\n", "again in 2s\n", "again in 4s\n", "again in 8s\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "again in 1s\n"); n != 2 {
		t.Fatalf("%d waits of 1s, want 2 (the first failure and the one after a link):\n%s", n, out)
	}
}

func TestALinkSevenClosesIsNotSetUpAgain(t *testing.T) {
	f := newCrashBrowser(t)
	s := newFakeSeven(t, f)
	p := startCrashBinary(t, "attach", "--no-kernel", "--no-jerk", "--cdp", f.srv.URL)
	first := s.link(t, 1)
	_ = first.l.Close()
	waitOutput(t, p, "link down: ", 1, 10*time.Second)
	s.noHello(t, 2, 3*time.Second)
	if out := p.logs.String(); strings.Contains(out, failedLinkStatus) {
		t.Fatalf("slicc set up a link seven closed:\n%s", out)
	}
}

func TestAReloadAfterAFailedLinkGreetsTheNewPageOnce(t *testing.T) {
	f := newCrashBrowser(t)
	s := newFakeSeven(t, f)
	p := startCrashBinary(t, "attach", "--no-kernel", "--no-jerk", "--cdp", f.srv.URL)
	first := s.link(t, 1)
	first.misbehave(t)
	waitOutput(t, p, failedLinkStatus, 1, 10*time.Second)
	f.event(first.session, "Runtime.executionContextsCleared", map[string]any{})
	f.mu.Lock()
	f.nextContext++
	ctxID := f.nextContext
	f.mu.Unlock()
	f.event(first.session, "Runtime.executionContextCreated", map[string]any{"context": map[string]any{
		"id": ctxID, "origin": strings.TrimSuffix(crashSevenURL, "/"), "name": "",
		"auxData": map[string]any{"isDefault": true, "frameId": "T1"},
	}})
	s.link(t, 2)
	s.noHello(t, 3, 3*time.Second)
}

func TestALinkThatComesUpDuringTheWaitCancelsTheRetry(t *testing.T) {
	f := newCrashBrowser(t)
	s := newFakeSeven(t, f)
	p := startCrashBinary(t, "attach", "--no-kernel", "--no-jerk", "--cdp", f.srv.URL)
	s.badOffer(t, 1)
	s.badOffer(t, 2)
	session, nonce := s.badOffer(t, 3)
	waitOutput(t, p, "asking seven to set it up again in 4s\n", 1, 10*time.Second)
	s.linkWith(t, 3, session, nonce)
	waitOutput(t, p, "link up with \"fake seven\"\n", 1, 5*time.Second)
	s.noHello(t, 4, 6*time.Second)
}

func TestAByeFromSevenDuringTheWaitCancelsTheRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(*testing.T, *fakeSeven, *crashProc) (string, string)
	}{
		{"after a link failed", func(t *testing.T, s *fakeSeven, p *crashProc) (string, string) {
			first := s.link(t, 1)
			waitOutput(t, p, "link up with \"fake seven\"\n", 1, 5*time.Second)
			first.misbehave(t)
			return first.session, first.nonce
		}},
		{"after an offer slicc couldn't answer", func(t *testing.T, s *fakeSeven, p *crashProc) (string, string) {
			return s.badOffer(t, 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCrashBrowser(t)
			s := newFakeSeven(t, f)
			p := startCrashBinary(t, "attach", "--no-kernel", "--no-jerk", "--cdp", f.srv.URL)
			session, nonce := tc.fail(t, s, p)
			waitOutput(t, p, failedLinkStatus, 1, 10*time.Second)
			s.post(session, f.lastContext(), map[string]any{"t": "bye", "nonce": nonce, "reason": "unlinked"})
			s.noHello(t, 2, 3*time.Second)
		})
	}
}

func TestAStaleByeDuringTheWaitDoesNotCancelTheRetry(t *testing.T) {
	f := newCrashBrowser(t)
	s := newFakeSeven(t, f)
	p := startCrashBinary(t, "attach", "--no-kernel", "--no-jerk", "--cdp", f.srv.URL)
	session, nonce := s.badOffer(t, 1)
	waitOutput(t, p, "asking seven to set it up again in 1s\n", 1, 10*time.Second)
	s.post(session, f.lastContext(), map[string]any{"t": "bye", "nonce": "not-" + nonce, "reason": "unlinked"})
	s.post(session, f.lastContext(), map[string]any{"t": "bye", "nonce": "stale", "reason": "unlinked"})
	f.waitHelloWithin(t, 2, 5*time.Second)
	select {
	case <-p.exited:
		t.Fatalf("slicc exited: %v\n%s", p.err, p.logs.String())
	default:
	}
}
