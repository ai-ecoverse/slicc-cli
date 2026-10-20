package cdp

import "strings"

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
	if !g.scrubValue(m) && !worlds && !assets {
		return data
	}
	return encode(m)
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

func sepPart(s string, i int, raw byte, enc string) int {
	switch {
	case i < len(s) && s[i] == raw:
		return 1
	case i+len(enc) <= len(s) && strings.EqualFold(s[i:i+len(enc)], enc):
		return len(enc)
	}
	return 0
}

func sepAt(s string, i int) int {
	n := sepPart(s, i, ':', "%3a")
	if n == 0 {
		return 0
	}
	for range 2 {
		m := sepPart(s, i+n, '/', "%2f")
		if m == 0 {
			return 0
		}
		n += m
	}
	return n
}

func nextSep(s string, from int) (int, int) {
	for i := from; i < len(s); i++ {
		if n := sepAt(s, i); n > 0 {
			return i, n
		}
	}
	return -1, 0
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

func unescapeOnce(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (g *guard) cutEmbedded(s string) (string, bool) {
	if !strings.Contains(s, "://") && !strings.Contains(s, "%") {
		return s, false
	}
	var b strings.Builder
	changed := false
	i := 0
	for {
		j, n := nextSep(s, i)
		if j < 0 {
			break
		}
		end := j + n
		for end < len(s) && !urlEnd(s[end]) && (n == 3 || s[end] != '&') {
			end++
		}
		cut := false
		for _, start := range schemeStarts(s, j) {
			u := s[start:end]
			if n != 3 {
				u = unescapeOnce(u)
			}
			if g.o.Loaded(u) {
				b.WriteString(s[i:start])
				b.WriteString(redactURL(u))
				i, cut, changed = end, true, true
				break
			}
		}
		if !cut {
			b.WriteString(s[i : j+n])
			i = j + n
		}
	}
	b.WriteString(s[i:])
	return b.String(), changed
}
