package cdp

import (
	"cmp"
	"net"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
	whatwg "github.com/nlnwa/whatwg-url/url"
	"golang.org/x/net/idna"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var opaque = map[string]bool{"value": true, "preview": true, "customPreview": true, "arguments": true, "returnValue": true}

var headerLists = map[string]bool{"headers": true, "requestHeaders": true, "responseHeaders": true}

var hostParser = whatwg.NewParser()
var hostBase, _ = hostParser.Parse("http://host.invalid/")

func scrubKind(headers, urls bool) string {
	if headers {
		return "headers"
	}
	if urls {
		return "url"
	}
	return "metadata"
}

type scrubItem struct {
	v       any
	headers bool
	urls    bool
}

func urlKey(k string) bool {
	l := strings.ToLower(k)
	for _, s := range []string{"url", "urls", "origin", "origins"} {
		if strings.HasSuffix(l, s) {
			return true
		}
	}
	return l == "referrer" || l == "location" || l == "href" || l == "src"
}

func (g *guard) scrub(data []byte) []byte {
	m, ok := decodeObject(data)
	if !ok {
		return data
	}
	worlds := g.scrubWorlds(m)
	assets := g.scrubAssets(m)
	failed := g.scrubFailed(m)
	if !g.scrubValue(m) && !worlds && !assets && !failed {
		return data
	}
	return encode(m)
}

func (g *guard) scrubFailed(m map[string]any) bool {
	session, _ := m["sessionId"].(string)
	g.mu.Lock()
	forms := g.failedFormsLocked(session)
	g.mu.Unlock()
	if len(forms) == 0 {
		return false
	}
	changed := false
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case string:
			for _, f := range forms {
				if strings.Contains(t, f.form) {
					t, changed = strings.ReplaceAll(t, f.form, f.redacted), true
				}
			}
			return t
		case map[string]any:
			for k, val := range t {
				t[k] = walk(val)
			}
		case []any:
			for i, val := range t {
				t[i] = walk(val)
			}
		}
		return v
	}
	for k, val := range m {
		m[k] = walk(val)
	}
	return changed
}

func (g *guard) cut(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || !strings.Contains(s, ":") || !g.o.URL(s) {
		return "", false
	}
	return redactURL(s), true
}

func (g *guard) scrubValue(root any) bool {
	changed := false
	stack := []scrubItem{{v: root}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		var more []scrubItem
		switch t := it.v.(type) {
		case map[string]any:
			more = g.scrubMap(t, it.headers, &changed)
		case []any:
			more = g.scrubList(t, it, &changed)
		}
		stack = append(stack, more...)
	}
	return changed
}

func (g *guard) blankFragments(t map[string]any) bool {
	loaded := false
	for k, val := range t {
		if s, ok := val.(string); ok && urlKey(k) && g.o.Loaded(s) {
			loaded = true
		}
	}
	changed := false
	for k, val := range t {
		if s, ok := val.(string); ok && loaded && s != "" && strings.HasSuffix(strings.ToLower(k), "fragment") {
			t[k], changed = "", true
		}
	}
	return changed
}

func (g *guard) scrubMap(t map[string]any, headers bool, changed *bool) []scrubItem {
	var more []scrubItem
	if g.blankFragments(t) {
		*changed = true
	}
	for k, val := range t {
		if !headers && opaque[k] {
			continue
		}
		if headers || urlKey(k) {
			if r, ok := g.cut(val); ok {
				t[k], *changed = r, true
				continue
			}
		}
		if str, ok := val.(string); ok {
			if r, ok := g.cutEmbeddedKind(str, scrubKind(headers, urlKey(k))); ok {
				t[k], *changed = r, true
			}
			continue
		}
		more = append(more, scrubItem{v: val, headers: headerLists[k] || headers, urls: urlKey(k)})
	}
	return more
}

func (g *guard) scrubList(t []any, it scrubItem, changed *bool) []scrubItem {
	var more []scrubItem
	for i, val := range t {
		if it.headers || it.urls {
			if r, ok := g.cut(val); ok {
				t[i], *changed = r, true
				continue
			}
		}
		if str, ok := val.(string); ok {
			if r, ok := g.cutEmbeddedKind(str, scrubKind(it.headers, it.urls)); ok {
				t[i], *changed = r, true
			}
			continue
		}
		more = append(more, scrubItem{v: val, headers: it.headers, urls: it.urls})
	}
	return more
}

func schemeByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}

func urlEnd(c byte) bool {
	return c <= ' ' || c == 0x7f || strings.IndexByte("<>\"'`", c) >= 0
}

func schemeStarts(s string, at int) []int {
	var starts []int
	for at > 0 {
		start := at
		for start > 0 && schemeByte(s[start-1]) {
			start--
		}
		if start == at {
			break
		}
		starts = append(starts, start)
		if start == 0 || s[start-1] != ':' {
			break
		}
		at = start - 1
	}
	return starts
}

const maxScrubLayers = 5
const maxScrubURLs = 64
const maxScrubURLBytes = 64 * 1024
const maxScrubHostBytes = 4 * 1024

type urlBudget struct {
	calls int
	bytes int
}

func (b *urlBudget) take(calls, bytes int) bool {
	if b.calls < calls || b.bytes < bytes {
		return false
	}
	b.calls -= calls
	b.bytes -= bytes
	return true
}

type urlCut struct {
	start int
	end   int
	value string
}

func urlOffset(offsets []int, at int) int {
	if offsets == nil {
		return at
	}
	return offsets[at]
}

func urlRunEnd(s string, start int, offsets []int) int {
	end := start
	encoded := offsets != nil && offsets[start+1]-offsets[start] > 1
	for end < len(s) && !urlEnd(s[end]) && (!encoded || s[end] != '&' || offsets[end+1]-offsets[end] > 1) {
		end++
	}
	return end
}

type urlRuns struct {
	plain   int
	encoded int
}

func (r *urlRuns) end(s string, start int, offsets []int) int {
	end := &r.plain
	if offsets != nil && offsets[start+1]-offsets[start] > 1 {
		end = &r.encoded
	}
	if start >= *end {
		*end = urlRunEnd(s, start, offsets)
	}
	return *end
}

func authorityByte(c byte) bool {
	return schemeByte(c) || c == '_' || c == ':' || c == '[' || c == ']' || c == '%' || c >= 0x80
}

type embeddedURL struct {
	protected bool
	origin    string
}

func ordinaryPort(host string) (string, bool) {
	colon := strings.LastIndexByte(host, ':')
	if colon < 0 {
		return host, true
	}
	port := host[colon+1:]
	if len(port) == 0 || len(port) > 5 {
		return "", false
	}
	n := 0
	for i := range len(port) {
		if port[i] < '0' || port[i] > '9' {
			return "", false
		}
		n = n*10 + int(port[i]-'0')
	}
	if n > 65535 {
		return "", false
	}
	return host[:colon], true
}

func ordinaryDNS(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	host = strings.TrimSuffix(host, ".")
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		if len(label) >= 4 && strings.EqualFold(label[:4], "xn--") {
			return false
		}
		for i := range len(label) {
			if !schemeByte(label[i]) || label[i] == '+' {
				return false
			}
		}
	}
	last := host[strings.LastIndexByte(host, '.')+1:]
	if len(last) >= 2 && strings.EqualFold(last[:2], "0x") {
		return false
	}
	return strings.ContainsAny(last, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

func foldAuthority(host string) string {
	for range maxScrubLayers {
		next, _ := decodeURLLayer(host, nil)
		if next == host {
			break
		}
		host = next
	}
	host = cases.Fold().String(norm.NFKC.String(host))
	host = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || unicode.Is(unicode.Variation_Selector, r) {
			return -1
		}
		if r == '\u3002' || r == '\uff0e' || r == '\uff61' {
			return '.'
		}
		return r
	}, host)
	return cases.Fold().String(norm.NFKC.String(host))
}

func ipShapeRune(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || strings.ContainsRune("x.:[]+-", r)
}

func ipShapeSuffix(host string) bool {
	if at := strings.LastIndexFunc(host, func(r rune) bool { return !ipShapeRune(r) }); at >= 0 {
		_, size := utf8.DecodeRuneInString(host[at:])
		host = host[at+size:]
		colon := strings.IndexByte(host, ':')
		if colon < 0 {
			return false
		}
		host = host[colon+1:]
	}
	return strings.ContainsAny(host, "0123456789[")
}

func (g *guard) protectedHostNames() []string {
	rules := sliccbrowsers.Default().ProtectedOrigins
	names := append(slices.Clone(rules.Hosts), rules.Domains...)
	for _, origin := range g.o.Extra() {
		names = append(names, protect.Host(origin))
	}
	return names
}

func (g *guard) hasProtectedIP() bool {
	return slices.ContainsFunc(g.protectedHostNames(), func(name string) bool {
		return net.ParseIP(strings.Trim(name, "[]")) != nil
	})
}

func (g *guard) longAuthorityMayName(host string) bool {
	return g.normalizedAuthorityMayName(host, false)
}

func (g *guard) colonIPMayName(host string) bool {
	for {
		canonical, settled := canonicalAuthority(host)
		if settled && canonical != "" && g.o.MayNameHost(canonical) {
			return true
		}
		colon := strings.IndexByte(host, ':')
		if colon < 0 {
			return false
		}
		host = host[colon+1:]
	}
}

func (g *guard) normalizedAuthorityMayName(host string, colon bool) bool {
	host = foldAuthority(host)
	ascii := strings.Map(func(r rune) rune {
		if r >= utf8.RuneSelf || r == '%' {
			return '?'
		}
		return r
	}, host)
	if g.o.MayNameHost(ascii) {
		return true
	}
	ip := false
	for _, name := range g.protectedHostNames() {
		ip = ip || net.ParseIP(strings.Trim(name, "[]")) != nil
		if !strings.Contains(name, "xn--") && !strings.Contains(name, "%") {
			continue
		}
		decoded, err := idna.ToUnicode(name)
		if err != nil {
			return true
		}
		decoded = foldAuthority(decoded)
		if decoded != "" && strings.Contains(host, decoded) {
			return true
		}
	}
	if !ip || !colon {
		return ip && ipShapeSuffix(host)
	}
	return g.colonIPMayName(host)
}

func canonicalAuthority(host string) (string, bool) {
	if len(host) > maxScrubHostBytes {
		return "", false
	}
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end < 0 {
			return "", true
		}
		tail := host[end+1:]
		if tail != "" && !strings.HasPrefix(tail, ":") {
			return "", true
		}
		if tail != "" {
			if _, ok := ordinaryPort("host" + tail); !ok && tail != ":" {
				return "", false
			}
		}
		host = host[:end+1]
	} else if colon := strings.IndexByte(host, ':'); colon >= 0 {
		if strings.IndexByte(host[colon+1:], ':') >= 0 {
			return "", false
		}
		if _, ok := ordinaryPort(host); !ok && colon+1 != len(host) {
			return "", false
		}
		host = host[:colon]
	}
	u, err := hostParser.BasicParser(host, nil, hostBase.Clone(), whatwg.StateHostname)
	if err != nil || u == nil {
		return "", true
	}
	return u.Hostname(), true
}

func (g *guard) mayNameAuthority(host string) bool {
	if ordinaryDNS(host) && !g.o.MayNameHost(host) {
		return false
	}
	canonical, settled := canonicalAuthority(host)
	return !settled || canonical != "" && g.o.MayNameHost(canonical)
}

func (g *guard) ordinaryEmbedded(u string) bool {
	prefix := 0
	if len(u) >= 7 && strings.EqualFold(u[:7], "http://") {
		prefix = 7
	} else if len(u) >= 8 && strings.EqualFold(u[:8], "https://") {
		prefix = 8
	} else {
		return false
	}
	host := u[prefix:]
	if end := strings.IndexAny(host, "/?#\\"); end >= 0 {
		host = host[:end]
	}
	name, ok := ordinaryPort(host)
	if ok && !g.o.MayNameHost(name) && ordinaryDNS(name) {
		return true
	}
	canonical, settled := canonicalAuthority(host)
	return settled && canonical != "" && !g.o.MayNameHost(canonical)
}

func (g *guard) longEmbeddedSafe(prefix string, budget *urlBudget) bool {
	if strings.EqualFold(prefix, "http://") || strings.EqualFold(prefix, "https://") {
		return true
	}
	probe := prefix + "scrub-host.invalid/"
	return budget.take(1, len(probe)) && !g.o.Loaded(probe)
}

func (g *guard) loadEmbedded(u string, budget *urlBudget, cache map[string]embeddedURL) (embeddedURL, bool) {
	if g.ordinaryEmbedded(u) {
		return embeddedURL{}, true
	}
	if v, known := cache[u]; known {
		return v, true
	}
	if !budget.take(1, len(u)) {
		return embeddedURL{}, false
	}
	v := embeddedURL{protected: g.o.Loaded(u)}
	if v.protected {
		v.origin = redactURL(u)
	}
	if len(cache) < 64 {
		cache[u] = v
	}
	return v, true
}

func (g *guard) embeddedCuts(s string, offsets []int, budget *urlBudget) ([]urlCut, bool) {
	var cuts []urlCut
	var runs urlRuns
	cache := map[string]embeddedURL{}
	for from := 0; from < len(s); {
		rel := strings.Index(s[from:], "://")
		if rel < 0 {
			break
		}
		at := from + rel
		end := runs.end(s, at, offsets)
		authority := s[at+3 : end]
		if stop := strings.IndexAny(authority, "/?#\\"); stop >= 0 {
			authority = authority[:stop]
		}
		long := len(authority) > maxScrubHostBytes
		if long && g.longAuthorityMayName(authority) {
			return nil, false
		}
		for _, start := range schemeStarts(s, at) {
			u := s[start:end]
			if long {
				if !g.longEmbeddedSafe(s[start:at+3], budget) {
					return nil, false
				}
				continue
			}
			v, complete := g.loadEmbedded(u, budget, cache)
			if !complete {
				return nil, false
			}
			if v.protected {
				if !strings.EqualFold(strings.TrimSuffix(u, "/"), v.origin) {
					cuts = append(cuts, urlCut{urlOffset(offsets, start), urlOffset(offsets, end), v.origin})
				}
				break
			}
		}
		from = at + 3
	}
	more, complete := g.unschemedCuts(s, offsets, budget)
	return append(cuts, more...), complete
}

func (g *guard) unschemedHost(host string, budget *urlBudget) (bool, bool) {
	if !g.mayNameAuthority(host) {
		return false, true
	}
	if !budget.take(2, 2*(len(host)+len("https:///"))) {
		return false, false
	}
	return g.o.URL("http://"+host+"/") || g.o.URL("https://"+host+"/"), true
}

func (g *guard) skipUnschemedAuthority(s string, start, end int) bool {
	if start == end || start >= 3 && s[start-3:start] == "://" {
		return true
	}
	host := s[start:end]
	return len(host) <= maxScrubHostBytes && !strings.HasPrefix(host, "[") && strings.Count(host, ":") >= 2 && !g.normalizedAuthorityMayName(host, true)
}

func (g *guard) unschemedCuts(s string, offsets []int, budget *urlBudget) ([]urlCut, bool) {
	var cuts []urlCut
	var runs urlRuns
	for at := 0; at < len(s); at++ {
		if s[at] != '/' {
			continue
		}
		start := at
		for start > 0 && authorityByte(s[start-1]) {
			start--
		}
		if g.skipUnschemedAuthority(s, start, at) {
			continue
		}
		if len(s[start:at]) > maxScrubHostBytes {
			if g.longAuthorityMayName(s[start:at]) {
				return nil, false
			}
			continue
		}
		for start < at {
			host := s[start:at]
			protected, complete := g.unschemedHost(host, budget)
			if !complete {
				return nil, false
			}
			if protected {
				end := runs.end(s, at, offsets)
				if s[start:end] != host+"/" {
					cuts = append(cuts, urlCut{urlOffset(offsets, start), urlOffset(offsets, end), host})
				}
				break
			}
			colon := strings.IndexByte(s[start:at], ':')
			if colon < 0 {
				break
			}
			start += colon + 1
		}
	}
	return cuts, true
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func decodeURLLayer(s string, offsets []int) (string, []int) {
	if !strings.Contains(s, "%") {
		return s, offsets
	}
	var b strings.Builder
	b.Grow(len(s))
	next := make([]int, 1, len(s)+1)
	next[0] = urlOffset(offsets, 0)
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 2
				next = append(next, urlOffset(offsets, i+1))
				continue
			}
		}
		b.WriteByte(s[i])
		next = append(next, urlOffset(offsets, i+1))
	}
	return b.String(), next
}

func (g *guard) cutEmbedded(s string) (string, bool) {
	return g.cutEmbeddedKind(s, "metadata")
}

func (g *guard) blankEmbedded(s, kind string) (string, bool) {
	if g.logf != nil {
		g.logf("cdp: scrub blanked kind=%s length=%d", kind, len(s))
	}
	return "", true
}

func (g *guard) cutEmbeddedKind(s, kind string) (string, bool) {
	if !strings.ContainsAny(s, "/%") || !strings.Contains(s, "%") && !strings.Contains(s, "://") && !g.o.MayNameHost(s) && !g.hasProtectedIP() {
		return s, false
	}
	var offsets []int
	layer := s
	var cuts []urlCut
	budget := urlBudget{calls: maxScrubURLs, bytes: maxScrubURLBytes}
	for depth := 0; ; depth++ {
		more, complete := g.embeddedCuts(layer, offsets, &budget)
		if !complete {
			return g.blankEmbedded(s, kind)
		}
		cuts = append(cuts, more...)
		next, positions := decodeURLLayer(layer, offsets)
		if next == layer {
			break
		}
		if depth == maxScrubLayers {
			return g.blankEmbedded(s, kind)
		}
		layer, offsets = next, positions
	}
	if len(cuts) == 0 {
		return s, false
	}
	slices.SortFunc(cuts, func(a, b urlCut) int {
		if n := cmp.Compare(a.start, b.start); n != 0 {
			return n
		}
		return cmp.Compare(b.end, a.end)
	})
	var b strings.Builder
	at := 0
	for _, cut := range cuts {
		if cut.start < at {
			continue
		}
		b.WriteString(s[at:cut.start])
		b.WriteString(cut.value)
		at = cut.end
	}
	b.WriteString(s[at:])
	return b.String(), true
}
