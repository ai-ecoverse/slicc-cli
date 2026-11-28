package signal

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

type Origins struct {
	extra []string
}

func NewOrigins(extra []string) (*Origins, error) {
	o := &Origins{}
	p := protect.MustNew(nil)
	for _, e := range extra {
		n := browser.NormalizeOrigin(e)
		if n == "" {
			return nil, fmt.Errorf("signal: %q is not an origin (scheme://host[:port])", e)
		}
		if protectedExtra(n, p) {
			return nil, fmt.Errorf("--origin %s is on a protected host and is not a seven page", n)
		}
		o.extra = append(o.extra, n)
	}
	return o, nil
}

func protectedExtra(n string, p *protect.Origins) bool {
	u, err := url.Parse(n)
	if err != nil || u.Scheme == "" {
		return true
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return !browser.IsSevenOrigin(n, nil) && p.Loaded(n)
	default:
		h := protect.Host(n)
		if h == "" {
			h = authorityHost(u)
		}
		if h == "" {
			return true
		}
		proj := "https://" + h
		return p.URL(proj) || !protect.Parses(proj) && protect.ZoneHost(h)
	}
}

func authorityHost(u *url.URL) string {
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

func (o *Origins) Allowed(origin string) bool {
	var extra []string
	if o != nil {
		extra = o.extra
	}
	return browser.IsSevenOrigin(origin, extra)
}

func (o *Origins) AllowedURL(raw string) bool {
	origin := browser.OriginOf(raw)
	return origin != "" && o.Allowed(origin)
}

func BlankOrigin(origin string) bool {
	return origin == "" || origin == "://"
}
