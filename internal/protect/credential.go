package protect

import (
	"slices"
	"strings"
)

var credentialOrigins = []string{"https://ims-na1.adobelogin.com", "https://ims-na1-stg1.adobelogin.com"}

const credentialRefusal = refusedPrefix + "cookies and storage of a sign-in origin can't be read or changed through CDP"

var credentialHosts = hostsOf(credentialOrigins)

func hostsOf(origins []string) []string {
	var hosts []string
	for _, o := range origins {
		u := parse(o)
		if u == nil || scheme(u) != "https" || hostOf(u) == "" {
			panic("protect: credential origin " + o + " is not an https origin")
		}
		if h := hostOf(u); !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

func CredentialOrigins() []string {
	return slices.Clone(credentialOrigins)
}

func CredentialReason() string {
	return strings.TrimPrefix(credentialRefusal, refusedPrefix)
}

func CredentialURL(raw string) bool {
	u := unwrap(parse(strings.TrimSpace(raw)))
	if u == nil {
		return false
	}
	switch scheme(u) {
	case "http", "https", "ws", "wss":
	default:
		return false
	}
	return slices.Contains(credentialHosts, hostOf(u))
}

func CredentialCookieDomain(domain string) bool {
	d := strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "."), ".")
	if d == "" {
		return false
	}
	return slices.ContainsFunc(credentialHosts, func(h string) bool { return under(h, d) })
}

func CredentialSite(site string) bool {
	if site == "" {
		return false
	}
	if CredentialURL(site) {
		return true
	}
	u := parse(strings.TrimSpace(site))
	if u == nil {
		return false
	}
	if s := scheme(u); s != "http" && s != "https" {
		return false
	}
	return CredentialCookieDomain(hostOf(u))
}

func CredentialStorageKey(key string) bool {
	parts := strings.Split(key, "^")
	if CredentialURL(parts[0]) {
		return true
	}
	for _, p := range parts[1:] {
		if site, ok := strings.CutPrefix(p, "0"); ok && CredentialSite(site) {
			return true
		}
	}
	return false
}
