package browser

import (
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
)

var sevenHost = regexp.MustCompile("^" + regexp.QuoteMeta(catalog.Seven.OriginScheme+"://") + strings.TrimPrefix(catalog.Seven.OriginHostPattern, "^"))

var excludedLabelPrefixes = func() []string {
	var c struct {
		Seven struct {
			ExcludedLabelPrefixes []string `json:"excludedLabelPrefixes"`
		} `json:"seven"`
	}
	_ = json.Unmarshal(sliccbrowsers.CatalogJSON(), &c)
	return c.Seven.ExcludedLabelPrefixes
}()

func excludedLabel(label string) bool {
	if slices.Contains(catalog.Seven.ExcludedLabels, label) {
		return true
	}
	for _, p := range excludedLabelPrefixes {
		if p != "" && strings.HasPrefix(label, p) {
			return true
		}
	}
	return false
}

func NormalizeOrigin(raw string) string {
	trimmed := strings.TrimRight(strings.ToLower(strings.TrimSpace(raw)), "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return ""
	}
	return u.Scheme + "://" + withoutDefaultPort(u)
}

var defaultPorts = map[string]string{"http": "80", "https": "443"}

func withoutDefaultPort(u *url.URL) string {
	host := strings.ToLower(u.Host)
	if p := u.Port(); p != "" && p == defaultPorts[strings.ToLower(u.Scheme)] {
		return strings.TrimSuffix(host, ":"+p)
	}
	return host
}

func IsSevenOrigin(origin string, extra []string) bool {
	if m := sevenHost.FindStringSubmatch(origin); m != nil && !excludedLabel(m[1]) {
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
	return strings.ToLower(u.Scheme) + "://" + withoutDefaultPort(u)
}
