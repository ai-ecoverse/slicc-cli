package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

const (
	Kind    = "resolve"
	Cap     = "net"
	TTL     = 30
	Timeout = 30 * time.Second
)

type Open struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Family *int   `json:"family"`
}

type Opened struct {
	Addresses []string `json:"addresses"`
	TTL       int      `json:"ttl"`
}

func Register(n *node.Node) {
	n.Handle(Cap, Kind, Handler(n.Resolver(), n.Logf))
}

func Handler(r *policy.Resolver, logf func(format string, args ...any)) link.Handler {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return func(s *link.Stream) {
		var req Open
		err := json.Unmarshal(s.OpenPayload(), &req)
		family := 0
		if req.Family != nil {
			family = *req.Family
		}
		name := strings.TrimSpace(req.Name)
		if err != nil || name == "" || (family != 0 && family != 4 && family != 6) {
			_ = s.Reset("EPROTO: resolve OPEN needs a name and a family of 0, 4 or 6")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), Timeout)
		defer cancel()
		go func() {
			select {
			case <-s.Done():
				cancel()
			case <-ctx.Done():
			}
		}()
		addrs, err := Lookup(ctx, r, name, family)
		if err != nil {
			logf("resolve %s: %v", name, err)
			if IsTimeout(err) {
				_ = s.Reset("ETIMEDOUT: " + err.Error())
				return
			}
			addrs = nil
		}
		out := Opened{Addresses: make([]string, 0, len(addrs)), TTL: TTL}
		for _, a := range addrs {
			out.Addresses = append(out.Addresses, a.String())
		}
		if err := s.Accept(out); err != nil {
			return
		}
		_ = s.CloseWrite()
	}
}

func Lookup(ctx context.Context, r *policy.Resolver, name string, family int) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(strings.Trim(name, "[]")); err == nil {
		a = a.Unmap()
		if !matches(a, family) {
			return []netip.Addr{}, nil
		}
		return r.Policy.Filter([]netip.Addr{a}), nil
	}
	network := "ip"
	switch family {
	case 4:
		network = "ip4"
	case 6:
		network = "ip6"
	}
	addrs, err := r.LookupNetIP(ctx, network, name)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addrs))
	seen := map[netip.Addr]bool{}
	for _, a := range addrs {
		a = a.Unmap()
		if !matches(a, family) || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out, nil
}

func matches(a netip.Addr, family int) bool {
	switch family {
	case 4:
		return a.Is4()
	case 6:
		return a.Is6()
	}
	return true
}

func IsTimeout(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTimeout && !dnsErr.IsNotFound
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded)
}
