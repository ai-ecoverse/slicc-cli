package udp

import (
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

const (
	Kind         = "udp"
	Cap          = "udp"
	IdleTimeout  = 5 * time.Minute
	MaxPeers     = 4096
	socketBuffer = 1 << 20
)

type Options struct {
	Policy  *policy.Policy
	Logf    func(format string, args ...any)
	Idle    time.Duration
	Dropped func(s *link.Stream, count int)
	listen  func(family int) (*net.UDPConn, error)
}

type Opened struct {
	LocalAddr string `json:"localAddr"`
}

func Register(n *node.Node) {
	RegisterOptions(n, Options{Policy: n.Policy(), Logf: n.Logf, Dropped: n.Dropped})
}

func RegisterOptions(n *node.Node, o Options) {
	n.Handle(Cap, Kind, Handler(o))
}

func Handler(o Options) link.Handler {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Idle <= 0 {
		o.Idle = IdleTimeout
	}
	if o.Dropped == nil {
		o.Dropped = func(*link.Stream, int) {}
	}
	if o.listen == nil {
		o.listen = Listen
	}
	return func(s *link.Stream) {
		serve(s, o)
	}
}

func ParseOpen(raw json.RawMessage) (int, error) {
	var o struct {
		Family *int `json:"family"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return 0, protoError("malformed udp OPEN")
	}
	if o.Family == nil || *o.Family != 4 && *o.Family != 6 {
		return 0, protoError("udp OPEN needs family 4 or 6")
	}
	return *o.Family, nil
}

func serve(s *link.Stream, o Options) {
	family, err := ParseOpen(s.OpenPayload())
	if err != nil {
		o.Logf("udp: EPROTO, a malformed OPEN")
		_ = s.Reset("EPROTO: " + err.Error())
		return
	}
	c, err := o.listen(family)
	if err != nil {
		o.Logf("udp: ENETUNREACH: %v", err)
		_ = s.Reset("ENETUNREACH: " + err.Error())
		return
	}
	local := c.LocalAddr().String()
	if err := s.Accept(Opened{LocalAddr: local}); err != nil {
		_ = c.Close()
		return
	}
	o.Logf("udp %s: open", local)
	x := newSession(s, c, o, family, local, func(n int) { o.Dropped(s, n) })
	x.run()
}

var errNoInterface = errors.New("no interface has an address of this family")

func Listen(family int) (*net.UDPConn, error) {
	return listen(family, upAddrs(), net.ListenUDP)
}

func listen(family int, addrs []net.Addr, listenUDP func(string, *net.UDPAddr) (*net.UDPConn, error)) (*net.UDPConn, error) {
	if !hasFamily(family, addrs) {
		return nil, errNoInterface
	}
	network, ip := "udp4", net.IPv4zero
	if family == 6 {
		network, ip = "udp6", net.IPv6unspecified
	}
	c, err := listenUDP(network, &net.UDPAddr{IP: ip})
	if err != nil {
		return nil, err
	}
	_ = c.SetWriteBuffer(socketBuffer)
	_ = c.SetReadBuffer(socketBuffer)
	recvErrors(c, family)
	return c, nil
}

func upAddrs() []net.Addr {
	ifs, _ := net.Interfaces()
	var out []net.Addr
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp != 0 {
			addrs, _ := ifc.Addrs()
			out = append(out, addrs...)
		}
	}
	return out
}

func hasFamily(family int, addrs []net.Addr) bool {
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if ok && (n.IP.To4() != nil) == (family == 4) {
			return true
		}
	}
	return false
}
