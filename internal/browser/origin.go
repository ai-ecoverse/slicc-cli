package browser

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var sevenHost = regexp.MustCompile("^" + regexp.QuoteMeta(catalog.Seven.OriginScheme+"://") + strings.TrimPrefix(catalog.Seven.OriginHostPattern, "^"))

func NormalizeOrigin(raw string) string {
	trimmed := strings.TrimRight(strings.ToLower(strings.TrimSpace(raw)), "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func IsSevenOrigin(origin string, extra []string) bool {
	if m := sevenHost.FindStringSubmatch(origin); m != nil && !slices.Contains(catalog.Seven.ExcludedLabels, m[1]) {
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
