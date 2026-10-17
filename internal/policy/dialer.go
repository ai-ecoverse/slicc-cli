package policy

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

const ConnectTimeout = 30 * time.Second

type Dialer struct {
	Policy *Policy
	Base   net.Dialer
	Hosts  *Hosts
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
	if host, port, err := net.SplitHostPort(address); err == nil {
		if addrs, ok := d.Hosts.Lookup(network, host); ok {
			return dialHosts(ctx, &nd, network, host, port, addrs)
		}
	}
	return nd.DialContext(ctx, network, address)
}

func dialHosts(ctx context.Context, nd *net.Dialer, network, host, port string, addrs []netip.Addr) (net.Conn, error) {
	if len(addrs) == 0 {
		return nil, &net.OpError{Op: "dial", Net: network, Err: &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}}
	}
	var first error
	for _, a := range addrs {
		c, err := nd.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return c, nil
		}
		if first == nil {
			first = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, first
}

func (d *Dialer) Control(network, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return &Error{Reason: address + " is not an IP address and port"}
	}
	if strings.HasPrefix(network, "tcp") {
		return d.Policy.checkStream(ap)
	}
	return d.Policy.check(ap)
}
