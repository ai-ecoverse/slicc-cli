package browser

import (
	"net/url"
	"regexp"
	"strings"
)

var sevenHost = regexp.MustCompile(`^https://([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\.sliccy\.ai$`)

func NormalizeOrigin(raw string) string {
	trimmed := strings.TrimRight(strings.ToLower(strings.TrimSpace(raw)), "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func IsSevenOrigin(origin string, extra []string) bool {
	if m := sevenHost.FindStringSubmatch(origin); m != nil && m[1] != "www" {
		return true
	}
	n := NormalizeOrigin(origin)
	if n == "" {
		return false
	}
	for _, e := range extra {
		if NormalizeOrigin(e) == n {
			return true
		}
	}
	return false
}

func OriginOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}
