package cdp

var freeForm = map[string]bool{
	"value": true, "unserializableValue": true, "description": true, "expression": true, "functionDeclaration": true,
	"arguments": true, "text": true, "data": true, "body": true, "postData": true, "postDataEntries": true,
	"bytes": true, "html": true, "source": true, "outerHTML": true, "nodeValue": true,
}

func (g *guard) scrub(data []byte) []byte {
	if !g.o.Mentions(data) {
		return data
	}
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
		if !ok || !g.o.URL(s) {
			return "", false
		}
		changed = true
		return redactURL(s), true
	}
	stack := []any{root}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				if freeForm[k] {
					continue
				}
				if r, ok := cut(val); ok {
					t[k] = r
				} else {
					stack = append(stack, val)
				}
			}
		case []any:
			for i, val := range t {
				if r, ok := cut(val); ok {
					t[i] = r
				} else {
					stack = append(stack, val)
				}
			}
		}
	}
	return changed
}
