package policy

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

type Rule struct {
	Prefix netip.Prefix
	Port   uint16
}

func (r Rule) String() string {
	if r.Port == 0 {
		return r.Prefix.String()
	}
	if r.Prefix.Addr().Is6() {
		return fmt.Sprintf("[%s]/%d:%d", r.Prefix.Addr(), r.Prefix.Bits(), r.Port)
	}
	return fmt.Sprintf("%s:%d", r.Prefix, r.Port)
}

func (r Rule) matches(addr netip.Addr, port uint16) bool {
	return (r.Port == 0 || r.Port == port) && r.Prefix.Contains(addr)
}

func ParseRule(s string) (Rule, error) {
	r, err := parseRule(strings.TrimSpace(s))
	if err != nil {
		return Rule{}, fmt.Errorf("policy: %q: %w", s, err)
	}
	return r, nil
}

func parseRule(s string) (Rule, error) {
	addrPart, bitsPart, portPart, err := splitRule(s)
	if err != nil {
		return Rule{}, err
	}
	addr, err := netip.ParseAddr(addrPart)
	if err != nil || addr.Zone() != "" {
		return Rule{}, fmt.Errorf("%q is not an IP address", addrPart)
	}
	bits := addr.BitLen()
	if bitsPart != "" {
		bits, err = strconv.Atoi(bitsPart)
		if err != nil || bits < 0 || bits > addr.BitLen() || bitsPart != strconv.Itoa(bits) {
			return Rule{}, fmt.Errorf("%q is not a prefix length", bitsPart)
		}
	}
	var port uint16
	if portPart != "" {
		n, err := strconv.ParseUint(portPart, 10, 16)
		if err != nil || n == 0 || portPart != strconv.FormatUint(n, 10) {
			return Rule{}, fmt.Errorf("%q is not a port", portPart)
		}
		port = uint16(n)
	}
	prefix := canonicalPrefix(netip.PrefixFrom(addr, bits).Masked())
	if why := neverMatches(prefix); why != "" {
		return Rule{}, fmt.Errorf("never matches: %s", why)
	}
	return Rule{Prefix: prefix, Port: port}, nil
}

func splitRule(s string) (addr, bits, port string, err error) {
	if s == "" {
		return "", "", "", fmt.Errorf("empty rule")
	}
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return "", "", "", fmt.Errorf("missing ]")
		}
		addr, rest := s[1:end], s[end+1:]
		if !strings.Contains(addr, ":") {
			return "", "", "", fmt.Errorf("brackets are only for IPv6")
		}
		if b, ok := strings.CutPrefix(rest, "/"); ok {
			rest = b
			bits, rest, _ = strings.Cut(rest, ":")
			if bits == "" {
				return "", "", "", fmt.Errorf("missing prefix length")
			}
			if rest != "" {
				return addr, bits, rest, nil
			}
			if strings.HasSuffix(s, ":") {
				return "", "", "", fmt.Errorf("missing port")
			}
			return addr, bits, "", nil
		}
		if rest == "" {
			return addr, "", "", nil
		}
		if p, ok := strings.CutPrefix(rest, ":"); ok && p != "" {
			return addr, "", p, nil
		}
		return "", "", "", fmt.Errorf("unexpected %q after ]", rest)
	}
	if a, rest, ok := strings.Cut(s, "/"); ok {
		bits, port, hasPort := strings.Cut(rest, ":")
		if bits == "" || (hasPort && port == "") {
			return "", "", "", fmt.Errorf("malformed rule")
		}
		return a, bits, port, nil
	}
	if strings.Count(s, ":") == 1 {
		a, port, _ := strings.Cut(s, ":")
		if port == "" {
			return "", "", "", fmt.Errorf("missing port")
		}
		return a, "", port, nil
	}
	return s, "", "", nil
}

func canonicalPrefix(p netip.Prefix) netip.Prefix {
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
	}
	return p
}

func neverMatches(p netip.Prefix) string {
	for _, r := range reachNever {
		if p.Bits() >= r.prefix.Bits() && r.prefix.Contains(p.Addr()) {
			return r.why
		}
	}
	return ""
}

type Rules []Rule

func (r Rules) String() string {
	return strings.Join(ruleStrings(r), ",")
}

func (r *Rules) Set(value string) error {
	for part := range strings.SplitSeq(value, ",") {
		rule, err := ParseRule(part)
		if err != nil {
			return err
		}
		*r = append(*r, rule)
	}
	return nil
}

func ParseRules(values []string) (Rules, error) {
	var r Rules
	for _, v := range values {
		if err := r.Set(v); err != nil {
			return nil, err
		}
	}
	return r, nil
}
