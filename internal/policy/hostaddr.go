package policy

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

const hostAddrTTL = 5 * time.Second

type addrCache struct {
	mu      sync.Mutex
	load    func() ([]net.Addr, error)
	now     func() time.Time
	fetched time.Time
	addrs   map[netip.Addr]bool
	bcast   map[netip.Addr]bool
	err     error
}

var hostAddrs = &addrCache{load: net.InterfaceAddrs, now: time.Now}

func (c *addrCache) isHost(addr netip.Addr) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.addrs == nil && c.err == nil || c.now().Sub(c.fetched) >= hostAddrTTL {
		c.refresh()
	}
	if c.err != nil {
		return false, c.err
	}
	return c.addrs[addr], nil
}

func (c *addrCache) isBroadcast(addr netip.Addr) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.addrs == nil && c.err == nil || c.now().Sub(c.fetched) >= hostAddrTTL {
		c.refresh()
	}
	if c.err != nil {
		return false, c.err
	}
	return c.bcast[addr], nil
}

func (c *addrCache) refresh() {
	c.fetched = c.now()
	list, err := c.load()
	if err != nil {
		c.addrs, c.bcast, c.err = nil, nil, err
		return
	}
	addrs, bcast := map[netip.Addr]bool{}, map[netip.Addr]bool{}
	for _, a := range list {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
			if b, ok := broadcastOf(v); ok {
				bcast[b] = true
			}
		case *net.IPAddr:
			ip = v.IP
		}
		if na, ok := netip.AddrFromSlice(ip); ok {
			addrs[na.Unmap()] = true
		}
	}
	c.addrs, c.bcast, c.err = addrs, bcast, nil
}

func broadcastOf(n *net.IPNet) (netip.Addr, bool) {
	ip4, mask := n.IP.To4(), n.Mask
	if len(mask) == net.IPv6len {
		mask = mask[12:]
	}
	if ip4 == nil || len(mask) != net.IPv4len {
		return netip.Addr{}, false
	}
	if ones, _ := mask.Size(); ones >= 31 {
		return netip.Addr{}, false
	}
	var b [4]byte
	for i := range b {
		b[i] = ip4[i] | ^mask[i]
	}
	return netip.AddrFrom4(b), true
}
