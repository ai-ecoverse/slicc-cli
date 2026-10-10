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

func (c *addrCache) refresh() {
	c.fetched = c.now()
	list, err := c.load()
	if err != nil {
		c.addrs, c.err = nil, err
		return
	}
	addrs := map[netip.Addr]bool{}
	for _, a := range list {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if na, ok := netip.AddrFromSlice(ip); ok {
			addrs[na.Unmap()] = true
		}
	}
	c.addrs, c.err = addrs, nil
}
