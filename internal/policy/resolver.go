package policy

import (
	"context"
	"net"
	"net/netip"
)

type Resolver struct {
	Policy *Policy
	Base   *net.Resolver
}

func NewResolver(p *Policy) *Resolver {
	return &Resolver{Policy: p, Base: net.DefaultResolver}
}

func (r *Resolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	base := r.Base
	if base == nil {
		base = net.DefaultResolver
	}
	addrs, err := base.LookupNetIP(ctx, network, host)
	if err != nil {
		return nil, err
	}
	return r.Policy.Filter(addrs), nil
}
