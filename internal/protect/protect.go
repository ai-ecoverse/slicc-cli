package protect

import (
	"fmt"
	"net"
	"slices"
	"strings"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

type Opener int

const (
	NoOpener Opener = iota
	OpenerProtected
	OpenerUnprotected
	OpenerUnknown
)

const maxUnwrap = 8

var rules = sliccbrowsers.Default().ProtectedOrigins

func Refusal() string {
	return rules.Refusal
}

type Origins struct {
	extra []string
	hosts []string
}

func New(extra []string) (*Origins, error) {
	o := &Origins{}
	for _, e := range extra {
		u := parse(e)
		origin := originOf(u)
		if origin == "" {
			return nil, fmt.Errorf("protect: %q is not an origin", e)
		}
		if !slices.Contains(o.extra, origin) {
			o.extra = append(o.extra, origin)
		}
		if h := hostOf(u); !slices.Contains(o.hosts, h) {
			o.hosts = append(o.hosts, h)
		}
	}
	return o, nil
}

func MustNew(extra []string) *Origins {
	o, err := New(extra)
	if err != nil {
		panic(err)
	}
	return o
}

func (o *Origins) Extra() []string {
	if o == nil {
		return nil
	}
	return slices.Clone(o.extra)
}

func parse(raw string) *whatwg.Url {
	u, err := whatwg.Parse(raw)
	if err != nil {
		return nil
	}
	return u
}

func scheme(u *whatwg.Url) string {
	return strings.ToLower(u.Scheme())
}

func hostOf(u *whatwg.Url) string {
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

func originOf(u *whatwg.Url) string {
	if u == nil || u.Host() == "" {
		return ""
	}
	origin := scheme(u) + "://" + hostOf(u)
	if p := u.Port(); p != "" {
		origin += ":" + p
	}
	return origin
}

func under(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func unwrap(u *whatwg.Url) *whatwg.Url {
	for i := 0; u != nil && i < maxUnwrap && slices.Contains(rules.InnerSchemes, scheme(u)); i++ {
		u = parse(strings.TrimPrefix(u.Href(false), u.Protocol()))
	}
	return u
}

func alias(u *whatwg.Url) *whatwg.Url {
	if u == nil || scheme(u) != "about" || rules.AboutScheme == "" {
		return u
	}
	if slices.Contains(rules.Inheriting, "about:"+strings.ToLower(u.Pathname())) {
		return u
	}
	if a := parse(rules.AboutScheme + "://" + u.Pathname() + u.Search() + u.Hash()); a != nil {
		return a
	}
	return u
}

func (o *Origins) URL(raw string) bool {
	u := alias(unwrap(parse(raw)))
	if u == nil {
		return false
	}
	s := scheme(u)
	if slices.Contains(rules.AnySchemes, s) {
		return true
	}
	if slices.Contains(rules.ProtectedSchemes, s) {
		return !slices.Contains(rules.SchemeExceptions, s+"://"+hostOf(u))
	}
	if slices.Contains(rules.Schemes, s) {
		h := hostOf(u)
		if slices.Contains(rules.Hosts, h) {
			return true
		}
		for _, d := range rules.Domains {
			if under(h, d) {
				return true
			}
		}
	}
	origin := originOf(u)
	return origin != "" && o != nil && slices.Contains(o.extra, origin)
}

func (o *Origins) Loaded(raw string) bool {
	return o.URL(raw) || (raw != "" && !Parses(raw))
}

func (o *Origins) LoadedOrigin(raw string) bool {
	return raw != "null" && raw != "://" && o.Loaded(raw)
}

func Parses(raw string) bool {
	u := unwrap(parse(raw))
	return u != nil && !slices.Contains(rules.InnerSchemes, scheme(u))
}

func Inheriting(raw string) bool {
	if raw == "" {
		return true
	}
	u := parse(raw)
	return u != nil && scheme(u) == "about" && slices.Contains(rules.Inheriting, "about:"+strings.ToLower(u.Pathname()))
}

func HiddenType(typ string) bool {
	return typ != "" && slices.Contains(rules.TargetTypes, typ)
}

func (o *Origins) TargetOf(raw, typ string, previous bool, opener Opener) bool {
	return HiddenType(typ) || o.Target(raw, previous, opener)
}

func (o *Origins) Target(raw string, previous bool, opener Opener) bool {
	if Inheriting(raw) {
		return previous || opener == OpenerProtected || opener == OpenerUnknown
	}
	return o.Loaded(raw)
}

func (o *Origins) CookieDomain(domain string) bool {
	d := strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(domain), "."), ".")
	if d == "" {
		return false
	}
	for _, x := range rules.Domains {
		if under(d, x) || under(x, d) {
			return true
		}
	}
	for _, h := range rules.Hosts {
		if under(h, d) {
			return true
		}
	}
	if o == nil {
		return false
	}
	for _, h := range o.hosts {
		if under(h, d) {
			return true
		}
	}
	return false
}

func (o *Origins) StorageKey(key string) bool {
	parts := strings.Split(key, "^")
	if o.URL(parts[0]) {
		return true
	}
	for _, p := range parts[1:] {
		if site, ok := strings.CutPrefix(p, "0"); ok && o.URL(site) {
			return true
		}
	}
	return false
}

func (o *Origins) DocumentPatterns() []string {
	var out []string
	add := func(prefix, host, port string) {
		hosts := []string{host}
		if net.ParseIP(strings.Trim(host, "[]")) == nil {
			hosts = append(hosts, host+".")
		}
		for _, h := range hosts {
			p := prefix + h + port + "/*"
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	for _, d := range rules.Domains {
		add("*://*", d, "")
		add("*://*", d, ":*")
	}
	for _, h := range rules.Hosts {
		if slices.ContainsFunc(rules.Domains, func(d string) bool { return under(h, d) }) {
			continue
		}
		add("*://*", h, "")
		add("*://*", h, ":*")
	}
	if o == nil {
		return out
	}
	for _, e := range o.extra {
		u := parse(e)
		port := ""
		if p := u.Port(); p != "" {
			port = ":" + p
		}
		add(scheme(u)+"://*", hostOf(u), port)
	}
	return out
}

func Redact(raw string) string {
	if raw == "" {
		return ""
	}
	if Inheriting(raw) {
		return "about:" + strings.ToLower(parse(raw).Pathname())
	}
	u := unwrap(parse(raw))
	if u == nil {
		return ""
	}
	if origin := originOf(u); origin != "" {
		return origin
	}
	return scheme(u) + ":"
}

func Host(raw string) string {
	u := unwrap(parse(raw))
	if u == nil {
		return ""
	}
	return hostOf(u)
}
