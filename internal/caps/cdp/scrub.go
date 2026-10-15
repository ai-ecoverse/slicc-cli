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
	if !g.scrubValue(m, 0) {
		return data
	}
	return encode(m)
}

func (g *guard) scrubValue(v any, depth int) bool {
	if depth > maxScanDepth {
		return false
	}
	changed := false
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if freeForm[k] {
				continue
			}
			if s, ok := val.(string); ok {
				if g.o.URL(s) {
					t[k] = redactURL(s)
					changed = true
				}
				continue
			}
			changed = g.scrubValue(val, depth+1) || changed
		}
	case []any:
		for i, val := range t {
			if s, ok := val.(string); ok {
				if g.o.URL(s) {
					t[i] = redactURL(s)
					changed = true
				}
				continue
			}
			changed = g.scrubValue(val, depth+1) || changed
		}
	}
	return changed
}
