package loopback

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

var ErrNoAnswer = errors.New("loopback: no answer")

type pending struct {
	answer   string
	cands    []*signal.Candidate
	complete chan struct{}
	finished bool
}

type peerConn struct {
	in   chan signal.Message
	done chan struct{}
	once sync.Once

	mu    sync.Mutex
	waits map[string]*pending
}

func newPeerConn() *peerConn {
	return &peerConn{in: make(chan signal.Message), done: make(chan struct{}), waits: map[string]*pending{}}
}

func (c *peerConn) offer(ctx context.Context, origin, sdp string, timeout time.Duration) (string, error) {
	nonce := signal.NewNonce()
	p := &pending{complete: make(chan struct{})}
	c.mu.Lock()
	c.waits[nonce] = p
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waits, nonce)
		c.mu.Unlock()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case c.in <- signal.Message{T: signal.TypeOffer, Nonce: nonce, SDP: sdp, Origin: origin}:
	case <-c.done:
		return "", signal.ErrClosed
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", ErrNoAnswer
	}
	select {
	case <-p.complete:
	case <-c.done:
		return "", signal.ErrClosed
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", ErrNoAnswer
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return withCandidates(p.answer, p.cands), nil
}

func (c *peerConn) Recv(ctx context.Context) (signal.Message, error) {
	select {
	case m := <-c.in:
		return m, nil
	case <-c.done:
		return signal.Message{}, io.EOF
	case <-ctx.Done():
		return signal.Message{}, ctx.Err()
	}
}

func (c *peerConn) Send(_ context.Context, m signal.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.waits[m.Nonce]
	if p == nil || p.finished {
		return nil
	}
	switch m.T {
	case signal.TypeAnswer:
		p.answer = m.SDP
	case signal.TypeCandidate:
		if m.Candidate != nil {
			p.cands = append(p.cands, m.Candidate)
		} else if p.answer != "" {
			p.finished = true
			close(p.complete)
		}
	}
	return nil
}

func (c *peerConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

var _ signal.Conn = (*peerConn)(nil)
