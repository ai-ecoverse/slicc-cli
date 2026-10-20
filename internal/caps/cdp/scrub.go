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
	failed := g.scrubFailed(m)
	if !g.scrubValue(m) && !worlds && !failed {
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

func (g *guard) scrubMap(t map[string]any, headers bool, changed *bool) []scrubItem {
	var more []scrubItem
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

func (g *guard) cutEmbedded(s string) (string, bool) {
	if !strings.Contains(s, "://") {
		return s, false
	}
	var b strings.Builder
	changed := false
	i := 0
	for {
		j := strings.Index(s[i:], "://")
		if j < 0 {
			break
		}
		j += i
		end := j + 3
		for end < len(s) && !urlEnd(s[end]) {
			end++
		}
		cut := false
		for _, start := range schemeStarts(s, j) {
			if u := s[start:end]; g.o.Loaded(u) {
				b.WriteString(s[i:start])
				b.WriteString(redactURL(u))
				i, cut, changed = end, true, true
				break
			}
		}
		if !cut {
			b.WriteString(s[i : j+3])
			i = j + 3
		}
	}
	b.WriteString(s[i:])
	return b.String(), changed
}
