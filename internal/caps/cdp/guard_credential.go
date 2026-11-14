package cdp

import (
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

var credentialDomains = []string{"Storage.", "IndexedDB.", "CacheStorage.", "DOMStorage.", "FileSystem."}

var credentialExempt = map[string]bool{
	"Storage.getCookies":                                 true,
	"Storage.setCookies":                                 true,
	"Storage.clearCookies":                               true,
	"Storage.getTrustTokens":                             true,
	"Storage.getAffectedUrlsForThirdPartyCookieMetadata": true,
	"Storage.getStorageKeyForFrame":                      true,
}

func credentialStorageMethod(method string) bool {
	for _, d := range credentialDomains {
		if strings.HasPrefix(method, d) {
			return !credentialExempt[method]
		}
	}
	return false
}

func credentialRule(c *command) string {
	if credentialStorageMethod(c.method) && credentialNamed(c.params, 0) {
		return refusedCredential
	}
	return ""
}

func credentialString(k, s string) bool {
	switch {
	case k == "topLevelSite" || k == "ownerSite":
		return protect.CredentialSite(s)
	case urlKeys[k]:
		return protect.CredentialURL(s)
	case k == "storageKey":
		return protect.CredentialStorageKey(s)
	case k == "cacheId":
		return protect.CredentialStorageKey(strings.SplitN(s, "|", 2)[0])
	}
	return false
}

func credentialNamed(v any, depth int) bool {
	if depth > maxScanDepth {
		return true
	}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if skipKeys[k] {
				continue
			}
			if s, ok := val.(string); ok {
				if credentialString(k, s) {
					return true
				}
				continue
			}
			if credentialNamed(val, depth+1) {
				return true
			}
		}
	case []any:
		for _, val := range t {
			if credentialNamed(val, depth+1) {
				return true
			}
		}
	}
	return false
}

func filterStrings(v any, drop func(string) bool) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok && drop(s) {
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

func filterTrustTokens(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		e, _ := item.(map[string]any)
		if o, _ := e["issuerOrigin"].(string); protect.CredentialURL(o) {
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

func (g *guard) auditIssueEvent(m *message, data []byte) browserVerdict {
	return g.withParams(m, data, func(p map[string]any) (bool, bool, [][]byte) {
		issue, _ := p["issue"].(map[string]any)
		return !g.issueNamesLocked(m.SessionID, "", issue["details"], 0), false, nil
	})
}

func cookieIssueKnown(c map[string]any) bool {
	cookie, _ := c["cookie"].(map[string]any)
	d, _ := cookie["domain"].(string)
	req, _ := c["request"].(map[string]any)
	u, _ := req["url"].(string)
	cu, _ := c["cookieUrl"].(string)
	return d != "" || u != "" || cu != ""
}

func (g *guard) issueNamesLocked(session, key string, v any, depth int) bool {
	if depth > maxScanDepth {
		return true
	}
	switch t := v.(type) {
	case map[string]any:
		if c, ok := t["cookieIssueDetails"].(map[string]any); ok && !cookieIssueKnown(c) {
			return true
		}
		for k, val := range t {
			if g.issueNamesLocked(session, k, val, depth+1) {
				return true
			}
		}
	case []any:
		for _, val := range t {
			if g.issueNamesLocked(session, key, val, depth+1) {
				return true
			}
		}
	case string:
		return g.issueStringLocked(session, key, t)
	}
	return false
}

func (g *guard) cookieLineNamesLocked(line string) bool {
	for _, attr := range strings.Split(line, ";")[1:] {
		name, v, _ := strings.Cut(attr, "=")
		if strings.EqualFold(strings.TrimSpace(name), "domain") && (g.o.CookieDomain(v) || protect.CredentialCookieDomain(v)) {
			return true
		}
	}
	return false
}

func (g *guard) issueStringLocked(session, key, s string) bool {
	k := strings.ToLower(key)
	switch {
	case k == "rawcookieline":
		return g.cookieLineNamesLocked(s)
	case k == "domain":
		return g.o.CookieDomain(s) || protect.CredentialCookieDomain(s)
	case k == "requestid":
		return g.requestMarkedLocked(session, s) || g.credMarkedLocked(session, s)
	case strings.HasSuffix(k, "url") || strings.HasSuffix(k, "urls") || strings.Contains(k, "origin") || strings.Contains(k, "site"):
		if g.o.Loaded(s) || protect.CredentialURL(s) {
			return true
		}
		return strings.Contains(k, "site") && (protect.CredentialSite(s) || g.o.CookieDomain(protect.Host(s)))
	}
	return false
}
