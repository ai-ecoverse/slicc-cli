package policy

import "net/netip"

var limitedBroadcast = netip.AddrFrom4([4]byte{255, 255, 255, 255})

func (p *Policy) CheckDatagram(addr netip.Addr, port uint16) error {
	ap := netip.AddrPortFrom(addr, port)
	if why := datagramNever(p, addr); why != "" {
		return &Error{Addr: ap, Reason: why}
	}
	if checked, _ := normalize(addr); checked != addr.WithZone("").Unmap() {
		if why := datagramNever(p, checked); why != "" {
			return &Error{Addr: ap, Checked: checked, Reason: why}
		}
	}
	return p.check(ap)
}

func datagramNever(p *Policy, addr netip.Addr) string {
	addr = addr.WithZone("").Unmap()
	switch {
	case addr.IsUnspecified():
		return "the unspecified address is always refused"
	case addr.IsMulticast():
		return "multicast is always refused"
	case addr == limitedBroadcast:
		return "broadcast is always refused"
	}
	if !addr.Is4() || p == nil || p.hosts == nil {
		return ""
	}
	bcast, err := p.hosts.isBroadcast(addr)
	switch {
	case err != nil:
		return "this machine's broadcast addresses are unknown: " + err.Error()
	case bcast:
		return "broadcast is always refused"
	}
	return ""
}
