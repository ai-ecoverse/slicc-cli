package node

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
	"github.com/pion/webrtc/v4"
)

type Config struct {
	Name         string
	Host         string
	Version      string
	Mode         string
	Policy       *policy.Policy
	Hosts        *policy.Hosts
	Exit         bool
	Prefixes     []string
	Logf         func(format string, args ...any)
	Status       func(format string, args ...any)
	Certificates []webrtc.Certificate
}

type Node struct {
	cfg        Config
	caps       []string
	handlers   map[string]link.Handler
	sshHostKey string
	api        *webrtc.API
	helloHooks []func(*link.Hello)
	subs       subscribers
	origins    sync.Map
}

func New(cfg Config) *Node {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Status == nil {
		cfg.Status = func(string, ...any) {}
	}
	if cfg.Host == "" {
		cfg.Host = DefaultHost()
	}
	if cfg.Name == "" {
		cfg.Name = "slicc on " + cfg.Host
	}
	if cfg.Mode == "" {
		cfg.Mode = "local"
	}
	if cfg.Prefixes == nil {
		cfg.Prefixes = []string{}
	}
	return &Node{cfg: cfg, caps: []string{}, handlers: map[string]link.Handler{}}
}

func DefaultHost() string {
	host, _ := os.Hostname()
	host = strings.ToLower(strings.SplitN(host, ".", 2)[0])
	if host == "" {
		return "slicc"
	}
	return host
}

func (n *Node) Handle(capability, kind string, h link.Handler) {
	n.Advertise(capability)
	n.handlers[kind] = h
}

func (n *Node) Advertise(capability string) {
	if !slices.Contains(n.caps, capability) {
		n.caps = append(n.caps, capability)
	}
}

func (n *Node) OnHello(f func(*link.Hello)) { n.helloHooks = append(n.helloHooks, f) }

func (n *Node) SetSSHHostKey(key string) { n.sshHostKey = key }

func (n *Node) Policy() *policy.Policy { return n.cfg.Policy }

func (n *Node) Dialer() *policy.Dialer {
	d := policy.NewDialer(n.cfg.Policy)
	d.Hosts = n.cfg.Hosts
	return d
}

func (n *Node) Resolver() *policy.Resolver {
	r := policy.NewResolver(n.cfg.Policy)
	r.Hosts = n.cfg.Hosts
	return r
}

func (n *Node) Logf(format string, args ...any) { n.cfg.Logf(format, args...) }

func (n *Node) Local() bool { return n.cfg.Mode == "local" }

func (n *Node) PeerOrigin(l *link.Link) string {
	v, _ := n.origins.Load(l)
	s, _ := v.(string)
	return s
}

func (n *Node) Caps() []string { return slices.Clone(n.caps) }

func (n *Node) Hello() link.Hello {
	h := link.Hello{
		Name:       n.cfg.Name,
		Host:       n.cfg.Host,
		Version:    n.cfg.Version,
		Mode:       n.cfg.Mode,
		Node:       signal.NodeID(),
		Caps:       slices.Clone(n.caps),
		Routes:     &link.Routes{Prefixes: slices.Clone(n.cfg.Prefixes), Exit: n.cfg.Exit},
		SSHHostKey: n.sshHostKey,
	}
	if p := n.cfg.Policy; p != nil {
		h.Policy = &link.Policy{Allow: p.Allow(), Deny: p.Deny(), Summary: p.Summary()}
	}
	for _, f := range n.helloHooks {
		f(&h)
	}
	return h
}

func (n *Node) Options() link.Options {
	handlers := make(map[string]link.Handler, len(n.handlers))
	for k, v := range n.handlers {
		handlers[k] = n.counted(k, v)
	}
	return link.Options{Hello: n.Hello(), Handlers: handlers, Logf: n.cfg.Logf, Certificates: n.cfg.Certificates}
}

func (n *Node) API() *webrtc.API {
	if n.api == nil {
		n.api = link.NewAPI(link.APIOptions{Logf: n.cfg.Logf, Loopback: true})
	}
	return n.api
}

type current struct {
	nonce   string
	l       *link.Link
	mu      sync.Mutex
	reason  string
	watched chan struct{}
}

func (c *current) setReason(r string) {
	c.mu.Lock()
	c.reason = r
	c.mu.Unlock()
}

func (c *current) getReason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reason
}

type Bye string

func (b Bye) Error() string { return string(b) }

func byeReason(ctx context.Context) string {
	var b Bye
	if errors.As(context.Cause(ctx), &b) {
		return string(b)
	}
	return "slicc is exiting"
}

func (n *Node) Serve(ctx context.Context, conn signal.Conn) error {
	var cur *current
	closeCur := func(reason string, bye bool) {
		if cur == nil {
			return
		}
		cur.setReason(reason)
		if bye {
			_ = conn.Send(context.Background(), signal.Message{T: signal.TypeBye, Nonce: cur.nonce, Reason: reason})
		}
		_ = cur.l.Close()
		<-cur.watched
		cur = nil
	}
	defer func() { closeCur(byeReason(ctx), true) }()
	for {
		m, err := conn.Recv(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		switch m.T {
		case signal.TypeOffer:
			closeCur("replaced by a new offer", false)
			cur = n.answer(ctx, conn, m, n.Options())
		case signal.TypeCandidate:
			if cur != nil && cur.nonce == m.Nonce {
				if err := cur.l.AddCandidate(m.Candidate); err != nil {
					n.cfg.Logf("node: candidate: %v", err)
				}
			}
		case signal.TypeBye:
			if cur != nil && cur.nonce == m.Nonce {
				n.cfg.Status("link closed by seven: %s", m.Reason)
				closeCur(m.Reason, false)
			}
		}
	}
}

func (n *Node) answer(ctx context.Context, conn signal.Conn, m signal.Message, opts link.Options) *current {
	l, err := link.Answer(ctx, n.API(), m, conn.Send, opts)
	if err != nil {
		n.cfg.Status("could not answer seven: %v", err)
		n.emit(Event{Kind: EventLinkDown, Err: err})
		return nil
	}
	c := &current{nonce: m.Nonce, l: l, watched: make(chan struct{})}
	if m.Origin != "" {
		n.origins.Store(l, m.Origin)
	}
	n.emit(Event{Kind: EventConnecting, Link: l})
	go n.watch(c)
	return c
}

func (n *Node) watch(c *current) {
	defer close(c.watched)
	l := c.l
	select {
	case <-l.Ready():
		h, _ := l.PeerHello()
		name := h.Name
		if name == "" {
			name = "seven"
		}
		n.cfg.Status("link up with %q", name)
		n.emit(Event{Kind: EventLinked, Link: l, Peer: h})
	case <-l.Done():
	}
	<-l.Done()
	n.origins.Delete(l)
	e := Event{Kind: EventLinkDown, Link: l, Reason: c.getReason()}
	if err := l.Err(); e.Reason == "" && err != nil && !errors.Is(err, link.ErrClosed) {
		n.cfg.Status("link down: %v", err)
		e.Err = err
	}
	n.emit(e)
}
