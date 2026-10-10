package policy

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
)

type Hosts struct {
	names map[string][]netip.Addr
}

func LoadHosts(path string) (*Hosts, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("hosts file: %w", err)
	}
	defer func() { _ = f.Close() }()
	return ParseHosts(f)
}

func ParseHosts(r io.Reader) (*Hosts, error) {
	h := &Hosts{names: map[string][]netip.Addr{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		addr, err := netip.ParseAddr(fields[0])
		if err != nil {
			return nil, fmt.Errorf("hosts file line %d: %q is not an IP address", n, fields[0])
		}
		if len(fields) < 2 {
			return nil, fmt.Errorf("hosts file line %d: no name after %s", n, fields[0])
		}
		for _, name := range fields[1:] {
			key := hostKey(name)
			if key == "" {
				return nil, fmt.Errorf("hosts file line %d: empty name", n)
			}
			h.names[key] = append(h.names[key], addr)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("hosts file: %w", err)
	}
	return h, nil
}

func hostKey(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

func (h *Hosts) Lookup(network, name string) ([]netip.Addr, bool) {
	if h == nil {
		return nil, false
	}
	addrs, ok := h.names[hostKey(name)]
	if !ok {
		return nil, false
	}
	out := []netip.Addr{}
	for _, a := range addrs {
		switch {
		case strings.HasSuffix(network, "4") && !a.Unmap().Is4():
		case strings.HasSuffix(network, "6") && a.Unmap().Is4():
		default:
			out = append(out, a)
		}
	}
	return out, true
}
