package cdp

import (
	"encoding/base64"
	"encoding/json"
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
	return g.sevenCookieLine(line) || credentialCookieLine(line)
}

func cookieLineDomains(line string) ([]string, bool) {
	for i := range len(line) {
		if c := line[i]; c < 0x20 && c != '\t' || c == 0x7f {
			return nil, false
		}
	}
	var out []string
	for _, attr := range strings.Split(line, ";")[1:] {
		name, v, _ := strings.Cut(attr, "=")
		if !strings.EqualFold(strings.TrimSpace(name), "domain") {
			continue
		}
		d := strings.Trim(strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"`))), ".")
		if d == "" {
			continue
		}
		if !cookieHost(d) {
			return nil, false
		}
		out = append(out, d)
	}
	return out, true
}

func cookieHost(d string) bool {
	for i := range len(d) {
		c := d[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && !strings.ContainsRune("-._:[]", rune(c)) {
			return false
		}
	}
	return true
}

func credentialCookieLine(line string) bool {
	domains, ok := cookieLineDomains(line)
	if !ok {
		return true
	}
	for _, d := range domains {
		if protect.CredentialCookieDomain(d) {
			return true
		}
	}
	return false
}

func plantHeaderTextOK(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c < 0x20 && c != '\t' || c == 0x7f {
			return false
		}
	}
	return true
}

func plantHeaderNameOK(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c <= ' ' || c == 0x7f {
			return false
		}
	}
	return true
}

func plantResponseCodeOK(c *command) bool {
	raw, has := c.params["responseCode"]
	if !has {
		return true
	}
	switch v := raw.(type) {
	case json.Number:
		_, err := v.Int64()
		return err == nil
	case float64:
		return v == float64(int64(v))
	case int, int32, int64:
		return true
	default:
		return false
	}
}

func plantResponsePhraseRule(c *command) string {
	raw, has := c.params["responsePhrase"]
	if !has {
		return ""
	}
	s, ok := raw.(string)
	if !ok || !plantHeaderTextOK(s) {
		return refusedCredential
	}
	if strings.Contains(s, ":") && credentialCookieLine(s) {
		return refusedCredential
	}
	return ""
}

func plantResponseCookies(c *command) (lines []string, ok bool) {
	if raw, has := c.params["binaryResponseHeaders"]; has {
		s, _ := raw.(string)
		decoded, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, false
		}
		for _, part := range strings.Split(string(decoded), "\x00") {
			name, val, found := strings.Cut(part, ":")
			if !found {
				continue
			}
			n := strings.TrimSpace(name)
			if strings.EqualFold(n, "set-cookie") || strings.EqualFold(n, "set-cookie2") {
				lines = append(lines, strings.TrimSpace(val))
			}
		}
	}
	raw, has := c.params["responseHeaders"]
	if !has {
		return lines, true
	}
	list, isArr := raw.([]any)
	if !isArr {
		return nil, false
	}
	for _, h := range list {
		header, isMap := h.(map[string]any)
		if !isMap {
			return nil, false
		}
		name, nameOK := header["name"].(string)
		val, valOK := header["value"].(string)
		if !nameOK || !valOK || !plantHeaderNameOK(name) || !plantHeaderTextOK(val) {
			return nil, false
		}
		if strings.EqualFold(name, "Set-Cookie") || strings.EqualFold(name, "Set-Cookie2") {
			lines = append(lines, val)
		}
	}
	return lines, true
}

func (g *guard) plantCookieRuleLocked(c *command, id string) string {
	if c.method != "Fetch.fulfillRequest" && c.method != "Fetch.continueResponse" {
		return ""
	}
	if !plantResponseCodeOK(c) {
		return refusedCredential
	}
	if reason := plantResponsePhraseRule(c); reason != "" {
		return reason
	}
	lines, ok := plantResponseCookies(c)
	if !ok {
		return refusedCredential
	}
	if len(lines) == 0 {
		return ""
	}
	url, known := g.pauseURLs[requestKey{c.session, id}]
	if !known || url == "" {
		return refusedCredential
	}
	if protect.CredentialURL(url) {
		return refusedCredential
	}
	for _, line := range lines {
		if credentialCookieLine(line) {
			return refusedCredential
		}
	}
	return ""
}

func (g *guard) sevenCookieLine(line string) bool {
	domains, ok := cookieLineDomains(line)
	if !ok {
		return false
	}
	for _, d := range domains {
		if g.o.CookieDomain(d) {
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
