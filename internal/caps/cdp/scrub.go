package cdp

import "strings"

var freeForm = map[string]bool{
	"value": true, "unserializableValue": true, "description": true, "expression": true, "functionDeclaration": true,
	"arguments": true, "text": true, "data": true, "body": true, "postData": true, "postDataEntries": true,
	"bytes": true, "html": true, "source": true, "outerHTML": true, "nodeValue": true, "entries": true,
}

var headerLists = map[string]bool{"headers": true, "requestHeaders": true, "responseHeaders": true}

type scrubItem struct {
	v       any
	headers bool
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

func (g *guard) scrubValue(root any) bool {
	changed := false
	cut := func(v any) (string, bool) {
		s, ok := v.(string)
		if !ok || !strings.Contains(s, ":") || !g.o.URL(s) {
			return "", false
		}
		changed = true
		return redactURL(s), true
	}
	stack := []scrubItem{{v: root}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch t := it.v.(type) {
		case map[string]any:
			for k, val := range t {
				if freeForm[k] && (k != "value" || !it.headers) {
					continue
				}
				if r, ok := cut(val); ok {
					t[k] = r
				} else {
					stack = append(stack, scrubItem{v: val, headers: headerLists[k]})
				}
			}
		case []any:
			for i, val := range t {
				if r, ok := cut(val); ok {
					t[i] = r
				} else {
					stack = append(stack, scrubItem{v: val, headers: it.headers})
				}
			}
		}
	}
	return changed
}
