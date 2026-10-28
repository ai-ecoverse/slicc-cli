package cdp

import (
	"cmp"
	"slices"
	"strings"
)

var opaque = map[string]bool{"value": true, "preview": true, "customPreview": true, "arguments": true, "returnValue": true}

var headerLists = map[string]bool{"headers": true, "requestHeaders": true, "responseHeaders": true}

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
			if r, ok := g.cutEmbedded(str); ok {
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
			if r, ok := g.cutEmbedded(str); ok {
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

func authorityByte(c byte) bool {
	return schemeByte(c) || c == '_' || c == ':' || c == '[' || c == ']' || c == '%' || c >= 0x80
}

type embeddedURL struct {
	protected bool
	origin    string
}

func (g *guard) embeddedCuts(s string, offsets []int) []urlCut {
	var cuts []urlCut
	cache := map[string]embeddedURL{}
	for from := 0; from < len(s); {
		rel := strings.Index(s[from:], "://")
		if rel < 0 {
			break
		}
		at := from + rel
		end := urlRunEnd(s, at, offsets)
		for _, start := range schemeStarts(s, at) {
			u := s[start:end]
			v, known := cache[u]
			if !known {
				v.protected = g.o.Loaded(u)
				if v.protected {
					v.origin = redactURL(u)
				}
				if len(cache) < 64 {
					cache[u] = v
				}
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
	return append(cuts, g.unschemedCuts(s, offsets)...)
}

func (g *guard) unschemedCuts(s string, offsets []int) []urlCut {
	var cuts []urlCut
	for at := 0; at < len(s); at++ {
		if s[at] != '/' {
			continue
		}
		start := at
		for start > 0 && authorityByte(s[start-1]) {
			start--
		}
		if start == at || start >= 3 && s[start-3:start] == "://" || !g.o.MayNameHost(s[start:at]) {
			continue
		}
		for start < at {
			host := s[start:at]
			if g.o.URL("http://"+host+"/") || g.o.URL("https://"+host+"/") {
				end := urlRunEnd(s, at, offsets)
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
	return cuts
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
	if !strings.ContainsAny(s, "/%") || !strings.Contains(s, "%") && !strings.Contains(s, "://") && !g.o.MayNameHost(s) {
		return s, false
	}
	var offsets []int
	layer := s
	var cuts []urlCut
	for depth := 0; ; depth++ {
		cuts = append(cuts, g.embeddedCuts(layer, offsets)...)
		next, positions := decodeURLLayer(layer, offsets)
		if next == layer {
			break
		}
		if depth == maxScrubLayers {
			return "", true
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
