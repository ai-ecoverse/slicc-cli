package protect

import (
	"slices"
	"strings"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
)

var credentialFloor = []string{
	"https://ims-na1.adobelogin.com",
	"https://ims-na1-stg1.adobelogin.com",
}

const credentialRefusalFallback = refusedPrefix + "this origin holds sign-in credentials"

var (
	credentialRules   = sliccbrowsers.Default().CredentialOrigins
	credentialOrigins = unionCredentialOrigins(credentialFloor, credentialRules.Origins)
	credentialHosts   = hostsOf(credentialOrigins)
	credentialRefusal = credentialRefusalOrFallback(credentialRules.Refusal)
)

func catalogOriginAccepted(o string) bool {
	o = strings.TrimSpace(o)
	if o == "" {
		return false
	}
	u := parse(o)
	return u != nil && scheme(u) == "https" && hostOf(u) != ""
}

func RejectedCatalogOrigins(catalog []string) []string {
	var rejected []string
	for _, o := range catalog {
		raw := strings.TrimSpace(o)
		if raw == "" {
			continue
		}
		if !catalogOriginAccepted(raw) {
			rejected = append(rejected, raw)
		}
	}
	return rejected
}

func unionCredentialOrigins(floor, catalog []string) []string {
	out := slices.Clone(floor)
	for _, o := range catalog {
		o = strings.TrimSpace(o)
		if o == "" || slices.Contains(out, o) || !catalogOriginAccepted(o) {
			continue
		}
		out = append(out, o)
	}
	if len(out) == 0 {
		return slices.Clone(credentialFloor)
	}
	return out
}

func credentialRefusalOrFallback(refusal string) string {
	if strings.HasPrefix(refusal, refusedPrefix) && len(refusal) > len(refusedPrefix) {
		return refusal
	}
	return credentialRefusalFallback
}

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

func CredentialFloor() []string {
	return slices.Clone(credentialFloor)
}

func MergeCredentialOrigins(catalog []string) []string {
	return unionCredentialOrigins(credentialFloor, catalog)
}

func CredentialRefusalText(catalogRefusal string) string {
	return credentialRefusalOrFallback(catalogRefusal)
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
