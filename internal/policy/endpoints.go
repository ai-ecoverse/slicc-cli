package policy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const endpointWhy = "a cdp browser endpoint"

type endpoints struct {
	mu   sync.RWMutex
	byID map[string][]netip.AddrPort
}

func newEndpoints() *endpoints {
	return &endpoints{byID: map[string][]netip.AddrPort{}}
}

func (e *endpoints) set(id string, addrs []netip.AddrPort) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.byID[id] = addrs
}

func (e *endpoints) union(id string, addrs []netip.AddrPort) {
	e.mu.Lock()
	defer e.mu.Unlock()
	list := e.byID[id]
	for _, ap := range addrs {
		if !slices.Contains(list, ap) {
			list = append(list, ap)
		}
	}
	e.byID[id] = list
}

func (e *endpoints) get(id string) []netip.AddrPort {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return slices.Clone(e.byID[id])
}

func (e *endpoints) has(ap netip.AddrPort) bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, list := range e.byID {
		if slices.Contains(list, ap) {
			return true
		}
	}
	return false
}

func (e *endpoints) ports(addr netip.Addr) []uint16 {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []uint16
	for _, list := range e.byID {
		for _, ap := range list {
			if ap.Addr() == addr {
				out = append(out, ap.Port())
			}
		}
	}
	return out
}

func (e *endpoints) rules() []Rule {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	ids := make([]string, 0, len(e.byID))
	for id := range e.byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var out []Rule
	for _, id := range ids {
		for _, ap := range e.byID[id] {
			out = append(out, Rule{Prefix: netip.PrefixFrom(ap.Addr(), ap.Addr().BitLen()), Port: ap.Port()})
		}
	}
	return out
}

func normalizeEndpoints(addrs []netip.AddrPort) []netip.AddrPort {
	var out []netip.AddrPort
	for _, ap := range addrs {
		if ap.Port() == 0 || !ap.Addr().IsValid() {
			continue
		}
		addr, _ := normalize(ap.Addr())
		n := netip.AddrPortFrom(addr, ap.Port())
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func (p *Policy) ProtectEndpoints(id string, addrs []netip.AddrPort) {
	if p == nil || p.endpoints == nil {
		return
	}
	p.endpoints.set(id, normalizeEndpoints(addrs))
}

func (p *Policy) AddProtectedEndpoints(id string, addrs []netip.AddrPort) {
	if p == nil || p.endpoints == nil {
		return
	}
	p.endpoints.union(id, normalizeEndpoints(addrs))
}

func (p *Policy) RefreshBrowser(ctx context.Context, id string, hostports []string, hosts *Hosts) error {
	var names []string
	for _, hp := range hostports {
		host, _, err := net.SplitHostPort(hp)
		if err != nil {
			continue
		}
		if _, err := netip.ParseAddr(host); err != nil {
			names = append(names, hp)
		}
	}
	if len(names) == 0 {
		return nil
	}
	addrs, err := ResolveEndpoints(ctx, names, hosts)
	p.AddProtectedEndpoints(id, addrs)
	return err
}

func (p *Policy) ProtectedEndpoints(id string) []netip.AddrPort {
	if p == nil || p.endpoints == nil {
		return nil
	}
	return p.endpoints.get(id)
}

func (p *Policy) ProtectBrowser(ctx context.Context, id string, hostports []string, hosts *Hosts) error {
	addrs, err := ResolveEndpoints(ctx, hostports, hosts)
	if err != nil {
		addrs = append(addrs, p.ProtectedEndpoints(id)...)
	}
	p.ProtectEndpoints(id, addrs)
	return err
}

func ResolveEndpoints(ctx context.Context, hostports []string, hosts *Hosts) ([]netip.AddrPort, error) {
	var out []netip.AddrPort
	var errs []error
	for _, hp := range hostports {
		host, portStr, err := net.SplitHostPort(hp)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil || port == 0 {
			errs = append(errs, fmt.Errorf("endpoint %s: bad port", hp))
			continue
		}
		addrs, err := resolveHost(ctx, host, hosts)
		if err != nil {
			errs = append(errs, fmt.Errorf("endpoint %s: %w", hp, err))
			continue
		}
		for _, a := range addrs {
			out = append(out, netip.AddrPortFrom(a.WithZone("").Unmap(), uint16(port)))
		}
	}
	return out, errors.Join(errs...)
}

func resolveHost(ctx context.Context, host string, hosts *Hosts) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}
	if addrs, ok := hosts.Lookup("ip", host); ok {
		if len(addrs) == 0 {
			return nil, errors.New("no addresses")
		}
		return addrs, nil
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}, nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err == nil && len(addrs) == 0 {
		err = errors.New("no addresses")
	}
	return addrs, err
}
