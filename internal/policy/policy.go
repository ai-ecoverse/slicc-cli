package policy

import (
	"net/netip"
	"slices"
	"strings"
)

type entry struct {
	rule Rule
	deny bool
	user bool
	hard bool
	why  string
}

type Policy struct {
	base      string
	entries   []entry
	allow     []Rule
	deny      []Rule
	userAllow []Rule
	userDeny  []Rule
	hostPorts []uint16
	hard      []uint16
	exposed   []uint16
	hosts     *addrCache
	endpoints *endpoints
	late      *latePorts
	live      *live
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
	"::/96", "::ffff:0:0:0/96",
}

var localAllows = []string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"fc00::/7",
}

var loopbackHosts = []string{"127.0.0.0/8", "127.0.0.1/32", "::1/128"}

func New() *Policy {
	return &Policy{base: "every address", endpoints: newEndpoints(), late: &latePorts{}, hosts: hostAddrs}
}

func Remote(protectedPorts ...uint16) *Policy {
	p := &Policy{base: "every public address", endpoints: newEndpoints(), late: &latePorts{}, hosts: hostAddrs}
	for _, s := range remoteDeny {
		p.entries = append(p.entries, entry{rule: mustRule(s), deny: true})
	}
	p.protect(protectedPorts)
	return p
}

func Local(protectedPorts ...uint16) *Policy {
	p := &Policy{base: "every public address, this machine, and the local network", endpoints: newEndpoints(), late: &latePorts{}, hosts: hostAddrs}
	for _, s := range remoteDeny {
		p.entries = append(p.entries, entry{rule: mustRule(s), deny: !slices.Contains(localAllows, s)})
	}
	p.protect(protectedPorts)
	return p
}

func (p *Policy) protect(ports []uint16) {
	for _, port := range ports {
		p.protectPort(port, false)
	}
	p.hosts = hostAddrs
}

func (p *Policy) protectPort(port uint16, hard bool) {
	if port == 0 {
		return
	}
	if hard && !slices.Contains(p.hard, port) {
		p.hard = append(p.hard, port)
		for i, e := range p.entries {
			if e.rule.Port == port && !e.user && e.deny && slices.Contains(loopbackHosts, e.rule.Prefix.String()) {
				p.entries[i].hard, p.entries[i].why = true, debugWhy
			}
		}
	}
	if slices.Contains(p.hostPorts, port) {
		return
	}
	p.hostPorts = append(p.hostPorts, port)
	for _, host := range loopbackHosts {
		r := Rule{Prefix: netip.MustParsePrefix(host), Port: port}
		p.entries = append(p.entries, entry{rule: r, deny: true, hard: hard, why: hardWhy(hard)})
		p.deny = append(p.deny, r)
	}
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
		hostPorts: slices.Clone(p.hostPorts),
		hard:      slices.Clone(p.hard),
		exposed:   slices.Clone(p.exposed),
		hosts:     p.hosts,
		endpoints: p.endpoints,
		late:      p.late,
		live:      p.live,
	}
	for _, r := range allow {
		q.entries = append(q.entries, entry{rule: r, user: true})
		q.allow = append(q.allow, r)
	}
	for _, r := range deny {
		q.entries = append(q.entries, entry{rule: r, deny: true, user: true})
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
		return &Error{Addr: ap, Checked: addr, Rule: e.rule.String(), Reason: e.why}
	}
	return nil
}

func (p *Policy) decide(addr netip.Addr, port uint16) (entry, bool) {
	var best entry
	found := false
	consider := func(e entry) {
		if e.rule.matches(addr, port) && (!found || beats(e, best)) {
			best, found = e, true
		}
	}
	for _, e := range p.entries {
		consider(e)
	}
	for _, e := range p.lateEntries(addr, port) {
		consider(e)
	}
	if e, ok := p.hostEntry(addr, port); ok {
		consider(e)
	}
	if port != 0 && p.endpoints.has(netip.AddrPortFrom(addr, port)) {
		consider(entry{rule: Rule{Prefix: netip.PrefixFrom(addr, addr.BitLen()), Port: port}, deny: true, why: endpointWhy})
	}
	return best, found
}

func (p *Policy) hostEntry(addr netip.Addr, port uint16) (entry, bool) {
	if port == 0 || p.hosts == nil || (!slices.Contains(p.hostPorts, port) && !p.late.has(port)) {
		return entry{}, false
	}
	why := "a protected port on an address of this machine"
	isHost, err := p.hosts.isHost(addr)
	if err != nil {
		why = "this machine's addresses are unknown: " + err.Error()
	} else if !isHost {
		return entry{}, false
	}
	hard := p.isHard(port)
	if hard && err == nil {
		why = debugWhy
	}
	r := Rule{Prefix: netip.PrefixFrom(addr, addr.BitLen()), Port: port}
	return entry{rule: r, deny: true, hard: hard, why: why}, true
}

func beats(a, b entry) bool {
	if a.hard != b.hard {
		return a.hard
	}
	if a.rule.Prefix.Bits() != b.rule.Prefix.Bits() {
		return a.rule.Prefix.Bits() > b.rule.Prefix.Bits()
	}
	if (a.rule.Port != 0) != (b.rule.Port != 0) {
		return a.rule.Port != 0
	}
	if a.user != b.user {
		return a.user
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
	ports := append([]uint16{0}, p.hostPorts...)
	ports = append(ports, p.late.list()...)
	ports = append(ports, p.endpoints.ports(checked)...)
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
	if len(p.exposed) > 0 {
		ports := Ports(p.exposed)
		s += "; exposes debugging ports " + strings.ReplaceAll(ports.String(), ",", ", ")
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
	out := ruleStrings(p.deny)
	for _, r := range p.endpoints.rules() {
		if s := r.String(); !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func ruleStrings(rules []Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.String()
	}
	return out
}
