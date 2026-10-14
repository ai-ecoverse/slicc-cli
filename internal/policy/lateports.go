package policy

import (
	"net/netip"
	"slices"
	"sync"
)

type latePorts struct {
	mu    sync.RWMutex
	ports []uint16
}

func (l *latePorts) has(port uint16) bool {
	if l == nil || port == 0 {
		return false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return slices.Contains(l.ports, port)
}

func (l *latePorts) list() []uint16 {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return slices.Clone(l.ports)
}

func (p *Policy) ProtectPort(port uint16) {
	if p == nil || p.late == nil || port == 0 || slices.Contains(p.hostPorts, port) {
		return
	}
	p.late.mu.Lock()
	defer p.late.mu.Unlock()
	if !slices.Contains(p.late.ports, port) {
		p.late.ports = append(p.late.ports, port)
	}
}

func (p *Policy) lateEntries(addr netip.Addr, port uint16) []entry {
	if !p.late.has(port) {
		return nil
	}
	var out []entry
	for _, host := range loopbackHosts {
		r := Rule{Prefix: netip.MustParsePrefix(host), Port: port}
		if r.matches(addr, port) {
			out = append(out, entry{rule: r, deny: true})
		}
	}
	return out
}
