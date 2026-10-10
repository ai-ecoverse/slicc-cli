package policy

import (
	"net/netip"
	"slices"
	"strings"
)

type entry struct {
	rule Rule
	deny bool
}

type Policy struct {
	base      string
	entries   []entry
	allow     []Rule
	deny      []Rule
	userAllow []Rule
	userDeny  []Rule
}

var remoteDeny = []string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"100.64.0.0/10",
	"169.254.0.0/16", "fe80::/10",
	"fc00::/7",
	"224.0.0.0/4", "ff00::/8",
	"240.0.0.0/4", "255.255.255.255/32", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32", "100::/64",
}

var localAllows = []string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"fc00::/7",
}

var loopbackHosts = []string{"127.0.0.0/8", "127.0.0.1/32", "::1/128"}

func New() *Policy {
	return &Policy{base: "every address"}
}

func Remote() *Policy {
	p := &Policy{base: "every public address"}
	for _, s := range remoteDeny {
		p.entries = append(p.entries, entry{rule: mustRule(s), deny: true})
	}
	return p
}

func Local(loopbackPorts ...uint16) *Policy {
	p := &Policy{base: "every public address, this machine, and the local network"}
	for _, s := range remoteDeny {
		if !slices.Contains(localAllows, s) {
			p.entries = append(p.entries, entry{rule: mustRule(s), deny: true})
		}
	}
	seen := map[uint16]bool{}
	for _, port := range loopbackPorts {
		if port == 0 || seen[port] {
			continue
		}
		seen[port] = true
		for _, host := range loopbackHosts {
			r := Rule{Prefix: netip.MustParsePrefix(host), Port: port}
			p.entries = append(p.entries, entry{rule: r, deny: true})
			p.deny = append(p.deny, r)
		}
	}
	return p
}

func mustRule(s string) Rule {
	r, err := ParseRule(s)
	if err != nil {
		panic(err)
	}
	return r
}

func (p *Policy) With(allow, deny []Rule) *Policy {
	allow, deny = canonicalRules(allow), canonicalRules(deny)
	q := &Policy{
		base:      p.base,
		entries:   slices.Clone(p.entries),
		allow:     slices.Clone(p.allow),
		deny:      slices.Clone(p.deny),
		userAllow: append(slices.Clone(p.userAllow), allow...),
		userDeny:  append(slices.Clone(p.userDeny), deny...),
	}
	for _, r := range allow {
		q.entries = append(q.entries, entry{rule: r})
		q.allow = append(q.allow, r)
	}
	for _, r := range deny {
		q.entries = append(q.entries, entry{rule: r, deny: true})
		q.deny = append(q.deny, r)
	}
	return q
}

func canonicalRules(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	for i, r := range rules {
		out[i] = Rule{Prefix: canonicalPrefix(r.Prefix.Masked()), Port: r.Port}
	}
	return out
}

func (p *Policy) Check(addr netip.Addr, port uint16) error {
	return p.check(netip.AddrPortFrom(addr, port))
}

func (p *Policy) check(ap netip.AddrPort) error {
	if p == nil {
		return &Error{Addr: ap, Reason: "no policy"}
	}
	addr, why := normalize(ap.Addr())
	if why != "" {
		return &Error{Addr: ap, Checked: addr, Reason: why}
	}
	if e, ok := p.decide(addr, ap.Port()); ok && e.deny {
		return &Error{Addr: ap, Checked: addr, Rule: e.rule.String()}
	}
	return nil
}

func (p *Policy) decide(addr netip.Addr, port uint16) (entry, bool) {
	var best entry
	found := false
	for _, e := range p.entries {
		if e.rule.matches(addr, port) && (!found || beats(e, best)) {
			best, found = e, true
		}
	}
	return best, found
}

func beats(a, b entry) bool {
	if a.rule.Prefix.Bits() != b.rule.Prefix.Bits() {
		return a.rule.Prefix.Bits() > b.rule.Prefix.Bits()
	}
	if (a.rule.Port != 0) != (b.rule.Port != 0) {
		return a.rule.Port != 0
	}
	return a.deny && !b.deny
}

func (p *Policy) AllowsAny(addr netip.Addr) bool {
	if p == nil {
		return false
	}
	checked, why := normalize(addr)
	if why != "" {
		return false
	}
	ports := []uint16{0}
	for _, e := range p.entries {
		if e.rule.Port != 0 && e.rule.Prefix.Contains(checked) {
			ports = append(ports, e.rule.Port)
		}
	}
	for _, port := range ports {
		if e, ok := p.decide(checked, port); !ok || !e.deny {
			return true
		}
	}
	return false
}

func (p *Policy) Filter(addrs []netip.Addr) []netip.Addr {
	out := []netip.Addr{}
	for _, a := range addrs {
		if p.AllowsAny(a) {
			out = append(out, a.Unmap())
		}
	}
	return out
}

func (p *Policy) Summary() string {
	if p == nil {
		return "nothing"
	}
	s := p.base
	if len(p.userAllow) > 0 {
		s += "; also allows " + strings.Join(ruleStrings(p.userAllow), ", ")
	}
	if len(p.userDeny) > 0 {
		s += "; also denies " + strings.Join(ruleStrings(p.userDeny), ", ")
	}
	return s
}

func (p *Policy) Allow() []string {
	if p == nil {
		return []string{}
	}
	return ruleStrings(p.allow)
}

func (p *Policy) Deny() []string {
	if p == nil {
		return []string{}
	}
	return ruleStrings(p.deny)
}

func ruleStrings(rules []Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.String()
	}
	return out
}
