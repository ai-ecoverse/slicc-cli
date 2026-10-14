//go:build !wasip1

package agentctl

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
)

type linkConn struct {
	l    *link.Link
	name string
}

func FromLink(l *link.Link, name string) Link { return &linkConn{l: l, name: name} }

func (c *linkConn) Allowed() bool {
	h, ok := c.l.PeerHello()
	return ok && h.HasCap(Cap)
}

func (c *linkConn) Name() string { return c.name }

func (c *linkConn) Done() <-chan struct{} { return c.l.Done() }

func (c *linkConn) Open(ctx context.Context, open any) (Pipe, error) {
	s, err := c.l.Open(ctx, open, link.OpenOptions{})
	if err != nil {
		return nil, err
	}
	return linkPipe{s}, nil
}

type linkPipe struct{ *link.Stream }

func (p linkPipe) Opened() json.RawMessage { return p.OpenedPayload() }

func linkCode(err error) (string, bool) {
	var re *link.ResetError
	if errors.As(err, &re) && re.Remote {
		return re.Code, true
	}
	return "", false
}
