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
	if !g.scrubValue(m) {
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
		more = append(more, scrubItem{v: val, headers: headerLists[k] || (headers && !isString(val)), urls: urlKey(k)})
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
		more = append(more, scrubItem{v: val, headers: it.headers, urls: it.urls})
	}
	return more
}

func isString(v any) bool {
	_, ok := v.(string)
	return ok
}
