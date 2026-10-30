package policy

import (
	"context"
	"net"
	"net/netip"
	"strings"
)

type Resolver struct {
	Policy *Policy
	Base   *net.Resolver
	Hosts  *Hosts
}

func NewResolver(p *Policy) *Resolver {
	return &Resolver{Policy: p, Base: net.DefaultResolver}
}

func (r *Resolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	addrs, err := r.lookup(ctx, network, host)
	if err != nil {
		return nil, err
	}
	return r.Policy.Filter(addrs), nil
}

func (r *Resolver) lookup(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if addrs, ok := r.Hosts.Lookup(network, host); ok {
		return addrs, nil
	}
	base := r.Base
	if base == nil {
		base = net.DefaultResolver
	}
	return base.LookupNetIP(ctx, network, host)
}

func (r *Resolver) CheckStream(ctx context.Context, host string, port uint16) error {
	if r == nil || r.Policy == nil {
		return &Error{Reason: "no policy"}
	}
	if a, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return r.Policy.checkStream(netip.AddrPortFrom(a, port))
	}
	addrs, err := r.lookup(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return &Error{Reason: "can't resolve " + host}
	}
	for _, a := range addrs {
		if err := r.Policy.checkStream(netip.AddrPortFrom(a, port)); err != nil {
			return err
		}
	}
	return nil
}
