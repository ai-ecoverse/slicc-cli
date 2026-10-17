package policy

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

const debugWhy = "a browser debugging port"

type DebugPorts struct {
	Found   []uint16
	Given   []uint16
	Exposed []uint16
}

func hardWhy(hard bool) string {
	if hard {
		return debugWhy
	}
	return ""
}

func (p *Policy) isHard(port uint16) bool {
	return slices.Contains(p.hard, port) || p.late.has(port)
}

func (p *Policy) Guard(d DebugPorts) *Policy {
	for _, port := range d.Exposed {
		if port != 0 && !slices.Contains(p.exposed, port) && !slices.Contains(d.Given, port) && slices.Contains(d.Found, port) {
			p.exposed = append(p.exposed, port)
		}
	}
	for _, port := range d.Given {
		p.protectPort(port, true)
	}
	for _, port := range d.Found {
		if !slices.Contains(p.exposed, port) {
			p.protectPort(port, true)
		}
	}
	p.hosts = hostAddrs
	return p
}

type live struct {
	scan  func() []uint16
	found func(uint16)
}

func (p *Policy) Live(scan func() []uint16, found func(uint16)) {
	p.live = &live{scan: scan, found: found}
}

func (p *Policy) checkStream(ap netip.AddrPort) error {
	if err := p.check(ap); err != nil || p.live == nil || ap.Port() == 0 || slices.Contains(p.exposed, ap.Port()) {
		return err
	}
	addr, _ := normalize(ap.Addr())
	if !p.onThisMachine(addr) || !slices.Contains(p.live.scan(), ap.Port()) {
		return nil
	}
	if p.GuardPort(ap.Port()) && p.live.found != nil {
		p.live.found(ap.Port())
	}
	return p.check(ap)
}

func (p *Policy) onThisMachine(addr netip.Addr) bool {
	if addr.IsLoopback() {
		return true
	}
	if p.hosts == nil {
		return false
	}
	isHost, err := p.hosts.isHost(addr)
	return err != nil || isHost
}

func (p *Policy) Exposed() []uint16 {
	if p == nil {
		return nil
	}
	return slices.Clone(p.exposed)
}

type Ports []uint16

func (s *Ports) String() string {
	out := make([]string, len(*s))
	for i, p := range *s {
		out[i] = strconv.Itoa(int(p))
	}
	return strings.Join(out, ",")
}

func (s *Ports) Set(v string) error {
	for part := range strings.SplitSeq(v, ",") {
		n, err := strconv.ParseUint(strings.TrimSpace(part), 10, 16)
		if err != nil || n == 0 {
			return fmt.Errorf("%q is not a port (1-65535)", part)
		}
		*s = append(*s, uint16(n))
	}
	return nil
}
