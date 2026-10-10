package policy

import (
	"context"
	"net"
	"net/netip"
	"syscall"
	"time"
)

const ConnectTimeout = 30 * time.Second

type Dialer struct {
	Policy *Policy
	Base   net.Dialer
}

func NewDialer(p *Policy) *Dialer {
	return &Dialer{Policy: p, Base: net.Dialer{Timeout: ConnectTimeout}}
}

func (d *Dialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6", "udp", "udp4", "udp6":
	default:
		return nil, &net.OpError{Op: "dial", Net: network, Err: &Error{Reason: "network " + network + " is not allowed"}}
	}
	nd := d.Base
	base, baseCtx := nd.Control, nd.ControlContext
	nd.Control = nil
	nd.ControlContext = func(ctx context.Context, network, address string, c syscall.RawConn) error {
		if err := d.Control(network, address, c); err != nil {
			return err
		}
		if baseCtx != nil {
			return baseCtx(ctx, network, address, c)
		}
		if base != nil {
			return base(network, address, c)
		}
		return nil
	}
	return nd.DialContext(ctx, network, address)
}

func (d *Dialer) Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return &Error{Reason: address + " is not an IP address and port"}
	}
	return d.Policy.check(ap)
}
