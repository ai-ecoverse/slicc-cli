package integration

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/tcp"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/pion/webrtc/v4"
)

type pipeConn struct {
	in     chan signal.Message
	done   chan struct{}
	closer sync.Once
	mu     sync.Mutex
	pages  map[string]chan signal.Message
}

func newPipeConn() *pipeConn {
	return &pipeConn{in: make(chan signal.Message, 256), done: make(chan struct{}), pages: map[string]chan signal.Message{}}
}

func (p *pipeConn) route(nonce string) chan signal.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan signal.Message, 256)
	p.pages[nonce] = ch
	return ch
}

func (p *pipeConn) Recv(ctx context.Context) (signal.Message, error) {
	select {
	case m := <-p.in:
		return m, nil
	case <-p.done:
		return signal.Message{}, io.EOF
	case <-ctx.Done():
		return signal.Message{}, ctx.Err()
	}
}

func (p *pipeConn) Send(ctx context.Context, m signal.Message) error {
	p.mu.Lock()
	ch := p.pages[m.Nonce]
	p.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case ch <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *pipeConn) Close() error {
	p.closer.Do(func() { close(p.done) })
	return nil
}

type pionPage struct {
	t     *testing.T
	conn  *pipeConn
	nonce string
	l     *link.Link
}

func (pc *pipeConn) startPage(t *testing.T, nonce string) *pionPage {
	t.Helper()
	inbox := pc.route(nonce)
	api := link.NewAPI(link.APIOptions{Loopback: true})
	peer, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	l, err := link.New(peer, link.RolePage, link.Options{Hello: link.Hello{Name: "pion page", Caps: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var mu sync.Mutex
	offered := false
	var early []signal.Message
	send := func(m signal.Message) {
		mu.Lock()
		defer mu.Unlock()
		if !offered && m.T == signal.TypeCandidate {
			early = append(early, m)
			return
		}
		pc.in <- m
	}
	peer.OnICECandidate(func(c *webrtc.ICECandidate) {
		m := signal.Message{T: signal.TypeCandidate, Nonce: nonce}
		if c != nil {
			init := c.ToJSON()
			m.Candidate = &signal.Candidate{Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex, UsernameFragment: init.UsernameFragment}
		}
		send(m)
	})
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	pc.in <- signal.Message{T: signal.TypeOffer, Nonce: nonce, SDP: offer.SDP}
	mu.Lock()
	offered = true
	for _, m := range early {
		pc.in <- m
	}
	early = nil
	mu.Unlock()
	go func() {
		for {
			select {
			case m := <-inbox:
				switch m.T {
				case signal.TypeAnswer:
					_ = peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
				case signal.TypeCandidate:
					if m.Candidate != nil {
						_ = peer.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate.Candidate, SDPMid: m.Candidate.SDPMid, SDPMLineIndex: m.Candidate.SDPMLineIndex, UsernameFragment: m.Candidate.UsernameFragment})
					}
				}
			case <-l.Done():
				return
			}
		}
	}()
	return &pionPage{t: t, conn: pc, nonce: nonce, l: l}
}

type eventLog struct {
	mu     sync.Mutex
	events []node.Event
	wake   chan struct{}
}

func (e *eventLog) add(ev node.Event) {
	e.mu.Lock()
	e.events = append(e.events, ev)
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *eventLog) kinds() []node.EventKind {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]node.EventKind, len(e.events))
	for i, ev := range e.events {
		out[i] = ev.Kind
	}
	return out
}

func (e *eventLog) waitFor(t *testing.T, kind node.EventKind, n int) node.Event {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		e.mu.Lock()
		seen := 0
		for _, ev := range e.events {
			if ev.Kind == kind {
				seen++
				if seen == n {
					e.mu.Unlock()
					return ev
				}
			}
		}
		e.mu.Unlock()
		select {
		case <-e.wake:
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatalf("no %s event #%d; events %v", kind, n, e.kinds())
		}
	}
}

func TestNodeEvents(t *testing.T) {
	echo := serveTCP(t, func(c net.Conn) { _, _ = io.Copy(c, c) })
	host, port := hostPort(t, echo)
	n := node.New(node.Config{Name: "events node", Version: "test", Policy: policy.Local(0), Exit: true})
	tcp.Register(n)
	log := &eventLog{wake: make(chan struct{}, 1)}
	unsubscribe := n.Subscribe(log.add)
	other := &eventLog{wake: make(chan struct{}, 1)}
	stopOther := n.Subscribe(other.add)
	conn := newPipeConn()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- n.Serve(ctx, conn) }()

	page := conn.startPage(t, "nonce-1")
	connecting := log.waitFor(t, node.EventConnecting, 1)
	linked := log.waitFor(t, node.EventLinked, 1)
	if connecting.Link == nil || linked.Link != connecting.Link || linked.Peer.Name != "pion page" || linked.Peer.Role != link.RolePage {
		t.Fatalf("connecting %+v, linked %+v", connecting, linked)
	}
	other.waitFor(t, node.EventLinked, 1)
	stopOther()
	stopOther()

	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	s, err := page.l.Open(sctx, map[string]any{"kind": "tcp", "host": host, "port": port}, link.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(s, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo %q %v", buf, err)
	}
	open := log.waitFor(t, node.EventStreamOpen, 1)
	if open.Stream != "tcp" || open.Link != linked.Link {
		t.Fatalf("stream-open %+v", open)
	}
	_ = s.Reset("ECANCELED")
	closed := log.waitFor(t, node.EventStreamClose, 1)
	if closed.Stream != "tcp" {
		t.Fatalf("stream-close %+v", closed)
	}

	conn.in <- signal.Message{T: signal.TypeBye, Nonce: "nonce-1", Reason: "page says bye"}
	down := log.waitFor(t, node.EventLinkDown, 1)
	if down.Link != linked.Link || down.Err != nil || down.Reason != "page says bye" {
		t.Fatalf("link-down %+v", down)
	}

	conn.startPage(t, "nonce-2")
	log.waitFor(t, node.EventLinked, 2)
	conn.startPage(t, "nonce-3")
	replaced := log.waitFor(t, node.EventLinkDown, 2)
	if replaced.Reason != "replaced by a new offer" {
		t.Fatalf("replaced %+v", replaced)
	}
	third := log.waitFor(t, node.EventLinked, 3)

	cancel()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	exit := log.waitFor(t, node.EventLinkDown, 3)
	if exit.Link != third.Link || exit.Reason != "slicc is exiting" {
		t.Fatalf("exit %+v", exit)
	}
	want := []node.EventKind{
		node.EventConnecting, node.EventLinked, node.EventStreamOpen, node.EventStreamClose, node.EventLinkDown,
		node.EventConnecting, node.EventLinked, node.EventLinkDown, node.EventConnecting, node.EventLinked, node.EventLinkDown,
	}
	got := log.kinds()
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events %v, want %v", got, want)
		}
	}
	if kinds := other.kinds(); len(kinds) != 2 {
		t.Fatalf("unsubscribed log got %v", kinds)
	}
	unsubscribe()
}
