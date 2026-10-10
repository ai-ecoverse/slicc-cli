package node

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"

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
	Exit         bool
	Prefixes     []string
	Logf         func(format string, args ...any)
	Status       func(format string, args ...any)
	Certificates []webrtc.Certificate
	OnLink       func(*link.Link)
}

type Node struct {
	cfg        Config
	caps       []string
	handlers   map[string]link.Handler
	sshHostKey string
	api        *webrtc.API
	helloHooks []func(*link.Hello)
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
	if !slices.Contains(n.caps, capability) {
		n.caps = append(n.caps, capability)
	}
	n.handlers[kind] = h
}

func (n *Node) OnHello(f func(*link.Hello)) { n.helloHooks = append(n.helloHooks, f) }

func (n *Node) SetSSHHostKey(key string) { n.sshHostKey = key }

func (n *Node) Policy() *policy.Policy { return n.cfg.Policy }

func (n *Node) Dialer() *policy.Dialer { return policy.NewDialer(n.cfg.Policy) }

func (n *Node) Logf(format string, args ...any) { n.cfg.Logf(format, args...) }

func (n *Node) Caps() []string { return slices.Clone(n.caps) }

func (n *Node) Hello() link.Hello {
	h := link.Hello{
		Name:       n.cfg.Name,
		Host:       n.cfg.Host,
		Version:    n.cfg.Version,
		Mode:       n.cfg.Mode,
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
		handlers[k] = v
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
	nonce string
	l     *link.Link
}

func (n *Node) Serve(ctx context.Context, conn signal.Conn) error {
	var cur *current
	closeCur := func(reason string, bye bool) {
		if cur == nil {
			return
		}
		if bye {
			_ = conn.Send(context.Background(), signal.Message{T: signal.TypeBye, Nonce: cur.nonce, Reason: reason})
		}
		_ = cur.l.Close()
		cur = nil
	}
	defer closeCur("slicc is exiting", true)
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
			closeCur("", false)
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
				closeCur("", false)
			}
		}
	}
}

func (n *Node) answer(ctx context.Context, conn signal.Conn, m signal.Message, opts link.Options) *current {
	l, err := link.Answer(ctx, n.API(), m, conn.Send, opts)
	if err != nil {
		n.cfg.Status("could not answer seven: %v", err)
		return nil
	}
	if n.cfg.OnLink != nil {
		n.cfg.OnLink(l)
	}
	go n.watch(l)
	return &current{nonce: m.Nonce, l: l}
}

func (n *Node) watch(l *link.Link) {
	select {
	case <-l.Ready():
		h, _ := l.PeerHello()
		name := h.Name
		if name == "" {
			name = "seven"
		}
		n.cfg.Status("link up with %q", name)
	case <-l.Done():
	}
	<-l.Done()
	if err := l.Err(); err != nil && !errors.Is(err, link.ErrClosed) {
		n.cfg.Status("link down: %v", err)
	}
}
