package signal

import (
	"fmt"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

type Origins struct {
	extra []string
}

func NewOrigins(extra []string) (*Origins, error) {
	o := &Origins{}
	for _, e := range extra {
		n := browser.NormalizeOrigin(e)
		if n == "" {
			return nil, fmt.Errorf("signal: %q is not an origin (scheme://host[:port])", e)
		}
		o.extra = append(o.extra, n)
	}
	return o, nil
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
