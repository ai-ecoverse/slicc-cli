package link

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
)

const interfaceProbeTimeout = 500 * time.Millisecond

type interfaceProbe struct {
	done       chan struct{}
	started    time.Time
	finished   time.Time
	interfaces []*transport.Interface
	err        error
}

type InterfaceDiscovery struct {
	mu      sync.Mutex
	probe   func() ([]*transport.Interface, error)
	timeout time.Duration
	running *interfaceProbe
}

func NewInterfaceDiscovery(probe func() ([]*transport.Interface, error), timeout time.Duration) *InterfaceDiscovery {
	if timeout <= 0 {
		timeout = interfaceProbeTimeout
	}
	return &InterfaceDiscovery{probe: probe, timeout: timeout}
}

var defaultInterfaceDiscovery = NewInterfaceDiscovery(func() ([]*transport.Interface, error) {
	n, err := stdnet.NewNet()
	if err != nil {
		return nil, err
	}
	return n.Interfaces()
}, interfaceProbeTimeout)

func (d *InterfaceDiscovery) Interfaces(ctx context.Context) ([]*transport.Interface, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	p := d.running
	if p != nil {
		select {
		case <-p.done:
			if time.Since(p.finished) >= time.Second {
				p = nil
			}
		default:
		}
	}
	if p == nil {
		p = &interfaceProbe{done: make(chan struct{}), started: time.Now()}
		d.running = p
		go d.run(p)
	}
	d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-p.done:
		return p.interfaces, p.err
	default:
	}
	wait, cancel := context.WithDeadline(ctx, p.started.Add(d.timeout))
	defer cancel()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return p.interfaces, p.err
	case <-wait.Done():
		return nil, wait.Err()
	}
}

func (d *InterfaceDiscovery) run(p *interfaceProbe) {
	defer func() { p.finished = time.Now(); close(p.done) }()
	defer func() {
		if recovered := recover(); recovered != nil {
			p.interfaces = nil
			p.err = errors.New("interface probe panicked")
		}
	}()
	p.interfaces, p.err = d.probe()
	if p.err != nil {
		p.err = errors.New("interface probe failed")
		return
	}
	hasAddresses := false
	for _, iface := range p.interfaces {
		if iface == nil {
			p.err = errors.New("interface probe returned an invalid interface")
			return
		}
		addresses, err := iface.Addrs()
		if errors.Is(err, transport.ErrNoAddressAssigned) {
			continue
		}
		if err != nil {
			p.err = errors.New("interface address probe failed")
			return
		}
		hasAddresses = hasAddresses || len(addresses) > 0
	}
	if hasAddresses {
		return
	}
	p.interfaces = nil
	p.err = errors.New("interface probe returned no addresses")
}

type discoveryNet struct {
	stdnet.Net
	loopback   bool
	discovery  *InterfaceDiscovery
	ctx        context.Context
	mu         sync.Mutex
	interfaces []*transport.Interface
	refreshed  time.Time
}

func (n *discoveryNet) Interfaces() ([]*transport.Interface, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if time.Since(n.refreshed) >= time.Second {
		interfaces, err := n.discovery.Interfaces(n.ctx)
		n.refreshed = time.Now()
		if err == nil {
			n.interfaces = interfaces
		}
	}
	return n.interfaces, nil
}

func (n *discoveryNet) InterfaceByIndex(index int) (*transport.Interface, error) {
	interfaces, err := n.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, iface := range interfaces {
		if iface != nil && iface.Index == index {
			return iface, nil
		}
	}
	return nil, transport.ErrInterfaceNotFound
}

func (n *discoveryNet) InterfaceByName(name string) (*transport.Interface, error) {
	interfaces, err := n.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, iface := range interfaces {
		if iface != nil && iface.Name == name {
			return iface, nil
		}
	}
	return nil, transport.ErrInterfaceNotFound
}

func (n *discoveryNet) UpdateInterfaces() error {
	_, err := n.Interfaces()
	return err
}

func hasMulticastInterface(interfaces []*transport.Interface, loopback bool) bool {
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || (!loopback && iface.Flags&net.FlagLoopback != 0) {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if multicastAddress(address, loopback) {
				return true
			}
		}
	}
	return false
}

func multicastAddress(address net.Addr, loopback bool) bool {
	addr, ok := netip.AddrFromSlice(interfaceIP(address))
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !loopback && addr.IsLoopback() {
		return false
	}
	if addr.Is6() {
		bytes := addr.As16()
		if [12]byte(bytes[:12]) == [12]byte{} || (bytes[0] == 0xfe && bytes[1]&0xc0 == 0xc0) {
			return false
		}
	}
	return true
}

func interfaceIP(address net.Addr) net.IP {
	switch address := address.(type) {
	case *net.IPNet:
		if address != nil {
			return address.IP
		}
	case *net.IPAddr:
		if address != nil {
			return address.IP
		}
	case *net.UDPAddr:
		if address != nil {
			return address.IP
		}
	case *net.TCPAddr:
		if address != nil {
			return address.IP
		}
	}
	return nil
}

func (n *discoveryNet) ListenUDP(network string, address *net.UDPAddr) (transport.UDPConn, error) {
	if address != nil && address.Port == 5353 && address.IP.IsMulticast() {
		n.mu.Lock()
		available := hasMulticastInterface(n.interfaces, n.loopback)
		n.mu.Unlock()
		if !available {
			return nil, errors.New("no ICE interfaces available for mDNS")
		}
	}
	return n.Net.ListenUDP(network, address)
}
