package tray

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

type Conn struct {
	s *Session

	mu      sync.Mutex
	nonce   string
	settled bool
	wake    chan struct{}
}

func NewConn(s *Session) *Conn {
	return &Conn{s: s, nonce: s.BootstrapID(), wake: make(chan struct{})}
}

func (c *Conn) Session() *Session { return c.s }

func (c *Conn) Settle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.settled {
		c.settled = true
		close(c.wake)
	}
}

func (c *Conn) isSettled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settled
}

func (c *Conn) Recv(ctx context.Context) (signal.Message, error) {
	if c.isSettled() {
		return signal.Message{}, c.idle(ctx)
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-c.wake:
			cancel()
		case <-rctx.Done():
		}
	}()
	ev, err := c.s.Recv(rctx)
	if err != nil {
		if errors.Is(err, ErrClosed) {
			return signal.Message{}, io.EOF
		}
		if c.isSettled() && ctx.Err() == nil {
			return signal.Message{}, c.idle(ctx)
		}
		return signal.Message{}, err
	}
	return c.message(ev), nil
}

func (c *Conn) idle(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.s.done:
		return io.EOF
	}
}

func (c *Conn) message(ev Event) signal.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch ev.Kind {
	case EventRestart:
		old := c.nonce
		c.nonce = c.s.BootstrapID()
		return signal.Message{T: signal.TypeBye, Nonce: old, Reason: "restart"}
	case EventCandidate:
		cand := signal.Candidate(ev.Candidate)
		return signal.Message{T: signal.TypeCandidate, Nonce: c.nonce, Candidate: &cand}
	default:
		c.nonce = c.s.BootstrapID()
		return signal.Message{T: signal.TypeOffer, V: signal.Version, Nonce: c.nonce, SDP: ev.SDP, ICEServers: toSignalICE(c.s.ICEServers())}
	}
}

func toSignalICE(servers []ICEServer) []signal.ICEServer {
	out := make([]signal.ICEServer, 0, len(servers))
	for _, s := range servers {
		out = append(out, signal.ICEServer(s))
	}
	return out
}

func (c *Conn) Send(ctx context.Context, m signal.Message) error {
	c.mu.Lock()
	current := c.nonce
	c.mu.Unlock()
	if m.Nonce != current {
		return nil
	}
	switch m.T {
	case signal.TypeAnswer:
		return c.s.SendAnswer(ctx, m.SDP)
	case signal.TypeCandidate:
		if m.Candidate == nil {
			return nil
		}
		return c.s.SendCandidate(ctx, Candidate(*m.Candidate))
	default:
		return nil
	}
}

func (c *Conn) Close() error { return c.s.Close() }
