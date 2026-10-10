package policy

import "net/netip"

type special struct {
	prefix netip.Prefix
	why    string
}

var (
	nat64      = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFour  = netip.MustParsePrefix("2002::/16")
	teredo     = netip.MustParsePrefix("2001::/32")
	thisNet    = netip.MustParsePrefix("0.0.0.0/8")
	unspec6    = netip.MustParsePrefix("::/128")
)

var alwaysDenied = []special{
	{nat64Local, "local-use NAT64 (64:ff9b:1::/48) is always denied"},
	{teredo, "Teredo (2001::/32) is always denied"},
	{thisNet, "0.0.0.0/8 is always denied"},
	{unspec6, ":: is always denied"},
}

var reachNever = []special{
	{nat64, "NAT64 addresses are checked as the IPv4 address they embed"},
	{nat64Local, "local-use NAT64 (64:ff9b:1::/48) is always denied"},
	{sixToFour, "6to4 addresses are checked as the IPv4 address they embed"},
	{teredo, "Teredo (2001::/32) is always denied"},
	{thisNet, "0.0.0.0/8 is always denied"},
	{unspec6, ":: is always denied"},
}

func normalize(addr netip.Addr) (netip.Addr, string) {
	addr = addr.WithZone("").Unmap()
	if addr.Is6() {
		b := addr.As16()
		switch {
		case nat64.Contains(addr):
			addr = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
		case sixToFour.Contains(addr):
			addr = netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
		}
	}
	for _, s := range alwaysDenied {
		if s.prefix.Contains(addr) {
			return addr, s.why
		}
	}
	return addr, ""
}
