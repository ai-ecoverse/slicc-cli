package cdp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	cdOrdinary = "https://ordinary.test/page"
	cdSeven    = "https://seven.sliccy.ai/embed"
	cdMarker   = "CD-CHILD-MARKER"
)

func cdGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, func(string, ...any) {})
	g.relaySession = "RELAY"
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: cdOrdinary, ctx: "default", document: "L1", documentRevision: 1}
	g.sessions["S"] = &session{id: "S", target: "PAGE", typ: "page"}
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	return g
}

func cdTree(childURL, marker string, nest map[string]any) map[string]any {
	child := map[string]any{
		"nodeId": 3, "nodeName": "#document", "documentURL": childURL, "baseURL": childURL,
		"children": []any{map[string]any{"nodeId": 4, "nodeName": "#text", "nodeValue": marker}},
	}
	owner := map[string]any{
		"nodeId": 2, "nodeName": "IFRAME", "frameId": "CD-IFRAME",
		"attributes":      []any{"src", childURL},
		"contentDocument": child,
	}
	for k, v := range nest {
		owner[k] = v
	}
	return map[string]any{
		"id": 1, "sessionId": "S",
		"result": map[string]any{
			"root": map[string]any{
				"nodeId": 1, "nodeName": "#document", "documentURL": cdOrdinary, "baseURL": cdOrdinary,
				"children": []any{owner},
			},
		},
	}
}

func cdInferMethod(msg map[string]any) string {
	if method, ok := msg["method"].(string); ok && method != "" {
		return method
	}
	result, _ := msg["result"].(map[string]any)
	if result != nil {
		if _, ok := result["documents"]; ok {
			return "DOMSnapshot.captureSnapshot"
		}
		if _, ok := result["domNodes"]; ok {
			return "DOMSnapshot.getSnapshot"
		}
		if _, ok := result["node"]; ok {
			return "DOM.describeNode"
		}
		if _, ok := result["nodes"]; ok {
			return "DOM.getFlattenedDocument"
		}
		if _, ok := result["root"]; ok {
			return "DOM.getDocument"
		}
		if _, ok := result["detachedNodes"]; ok {
			return "DOM.getDetachedDomNodes"
		}
	}
	return "DOM.getDocument"
}

func cdSessionOf(msg map[string]any) string {
	session, _ := msg["sessionId"].(string)
	return session
}

func cdStripThenScrub(t *testing.T, g *guard, raw []byte, method string, session string) []byte {
	t.Helper()
	if method != "" && (childDocCommandWalkMethods[method] || childDocEventWalkMethods[method]) {
		g.mu.Lock()
		out, drop := g.applyChildDocStripLocked(raw, method, session, nil)
		g.mu.Unlock()
		if drop {
			return nil
		}
		raw = out
	}
	return g.scrub(raw)
}

func cdExpectRefused(t *testing.T, out, why string) {
	t.Helper()
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, refusedTarget) {
		t.Fatalf("%s: want refusal, got %s", why, out)
	}
	if strings.Contains(out, cdMarker) {
		t.Fatalf("%s: refused reply must not carry marker: %s", why, out)
	}
}

func cdScrub(t *testing.T, g *guard, msg map[string]any) string {
	t.Helper()
	return cdScrubMethod(t, g, msg, cdInferMethod(msg))
}

func cdScrubMethod(t *testing.T, g *guard, msg map[string]any, method string) string {
	t.Helper()
	out := cdStripThenScrub(t, g, encode(msg), method, cdSessionOf(msg))
	if out == nil {
		return ""
	}
	return string(out)
}

func cdScrubRaw(t *testing.T, g *guard, raw []byte) []byte {
	t.Helper()
	m, ok := decodeObject(raw)
	if !ok {
		t.Fatal("decode scrub fixture")
	}
	return cdStripThenScrub(t, g, raw, cdInferMethod(m), cdSessionOf(m))
}

func cdByteIdentical(t *testing.T, g *guard, msg map[string]any, method string) {
	t.Helper()
	want := string(encode(msg))
	got := cdScrubMethod(t, g, msg, method)
	if got != want {
		t.Fatalf("want byte-identical\nwant=%s\ngot=%s", want, got)
	}
}

func cdAdmit(t *testing.T, g *guard, raw []byte) {
	t.Helper()
	c, n, ok := parseCommand(raw)
	if !ok {
		t.Fatal("parse command")
	}
	if v := g.fromCommand(c, n, ok); v.forward == nil && v.reply != nil {
		t.Fatalf("command refused: %s", v.reply)
	} else if v.forward == nil {
		t.Fatalf("command not forwarded: reply=%s", v.reply)
	}
}

func cdWritePage(g *guard, data []byte) []byte {
	if data == nil {
		return nil
	}
	return g.scrub(append([]byte(nil), data...))
}

func cdSettleIdentity(t *testing.T, g *guard, send [][]byte) []byte {
	t.Helper()
	var infoID int64
	for _, msg := range send {
		var call struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(msg, &call) == nil && call.Method == "Target.getTargetInfo" {
			infoID = call.ID
		}
	}
	if infoID == 0 {
		return nil
	}
	info, _ := json.Marshal(map[string]any{
		"id": infoID, "sessionId": "RELAY",
		"result": map[string]any{"targetInfo": map[string]any{"targetId": "PAGE", "type": "page", "url": cdOrdinary}},
	})
	settled := g.fromBrowser(info)
	if settled.forward != nil {
		return settled.forward
	}
	if len(settled.page) > 0 {
		return settled.page[0]
	}
	g.mu.Lock()
	page := g.takePendingPageLocked()
	g.mu.Unlock()
	if len(page) > 0 {
		return page[0]
	}
	return nil
}

func cdDeliverBrowser(t *testing.T, g *guard, data []byte) []byte {
	t.Helper()
	if g.relaySession == "" {
		g.relaySession = "RELAY"
	}
	v := g.fromBrowser(data)
	if v.forward != nil {
		return cdWritePage(g, v.forward)
	}
	if len(v.page) > 0 {
		return cdWritePage(g, v.page[0])
	}
	if delivered := cdSettleIdentity(t, g, v.send); delivered != nil {
		return cdWritePage(g, delivered)
	}
	g.mu.Lock()
	page := g.takePendingPageLocked()
	g.mu.Unlock()
	if len(page) > 0 {
		return cdWritePage(g, page[0])
	}
	return nil
}

func cdRuntimeValueReply(id int64, value any) map[string]any {
	return map[string]any{
		"id": id, "sessionId": "S",
		"result": map[string]any{
			"result": map[string]any{
				"type": "object", "value": value,
			},
		},
	}
}

func cdProtectedLookingValue() map[string]any {
	return map[string]any{
		"nodeName": "#document", "nodeType": 9,
		"documentURL": cdSeven, "baseURL": cdSeven,
		"children": []any{
			map[string]any{"nodeName": "#text", "nodeValue": "secret"},
		},
	}
}

func TestChildDocStripProtectedMarker(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdTree(cdSeven, cdMarker, nil))
	cdExpectRefused(t, out, "protected child document")
}

func TestChildDocKeepOrdinaryChild(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdTree("https://other.ordinary.test/frame", "ordinary-text", nil))
	if !strings.Contains(out, "ordinary-text") {
		t.Fatalf("ordinary child text must remain: %s", out)
	}
	if !strings.Contains(out, `"contentDocument"`) {
		t.Fatalf("ordinary contentDocument must remain: %s", out)
	}
}

func TestChildDocStripEmptyURL(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree("", cdMarker, nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	doc := owner["contentDocument"].(map[string]any)
	doc["documentURL"] = ""
	doc["baseURL"] = ""
	cdExpectRefused(t, cdScrub(t, g, msg), "empty-url child document")
}

func TestChildDocInheritAboutBlank(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree("about:blank", "blank-text", nil)
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, "blank-text") || !strings.Contains(out, `"contentDocument"`) {
		t.Fatalf("about:blank under ordinary parent must stay: %s", out)
	}
}

func TestChildDocNestedProtectedInOrdinary(t *testing.T) {
	g := cdGuard(t)
	inner := map[string]any{
		"nodeId": 5, "nodeName": "IFRAME", "frameId": "CD-INNER",
		"attributes": []any{"src", cdSeven},
		"contentDocument": map[string]any{
			"nodeId": 6, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
			"children": []any{map[string]any{"nodeId": 7, "nodeName": "#text", "nodeValue": cdMarker}},
		},
	}
	msg := cdTree("https://mid.ordinary.test/", "mid-text", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	doc := owner["contentDocument"].(map[string]any)
	doc["children"] = []any{
		map[string]any{"nodeId": 4, "nodeName": "#text", "nodeValue": "mid-text"},
		inner,
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "nested protected under ordinary mid")
}

func TestChildDocShadowRootProtected(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["shadowRoots"] = []any{map[string]any{
		"nodeId": 10, "nodeName": "#document-fragment",
		"children": []any{map[string]any{
			"nodeId": 11, "nodeName": "IFRAME", "frameId": "CD-SHADOW",
			"contentDocument": map[string]any{
				"nodeId": 12, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
				"children": []any{map[string]any{"nodeId": 13, "nodeName": "#text", "nodeValue": cdMarker}},
			},
		}},
	}}
	cdExpectRefused(t, cdScrub(t, g, msg), "shadowRoot protected")
}

func TestChildDocTemplateContentProtected(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["templateContent"] = map[string]any{
		"nodeId": 20, "nodeName": "#document-fragment",
		"children": []any{map[string]any{
			"nodeId": 21, "nodeName": "IFRAME", "frameId": "CD-TMPL",
			"contentDocument": map[string]any{
				"nodeId": 22, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
				"children": []any{map[string]any{"nodeId": 23, "nodeName": "#text", "nodeValue": cdMarker}},
			},
		}},
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "templateContent protected")
}

func TestChildDocPseudoElementProtected(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["pseudoElements"] = []any{map[string]any{
		"nodeId": 30, "nodeName": "IFRAME", "frameId": "CD-PSEUDO",
		"contentDocument": map[string]any{
			"nodeId": 31, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
			"children": []any{map[string]any{"nodeId": 32, "nodeName": "#text", "nodeValue": cdMarker}},
		},
	}}
	cdExpectRefused(t, cdScrub(t, g, msg), "pseudoElements protected")
}

func TestChildDocImportedDocumentProtected(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["importedDocument"] = map[string]any{
		"nodeId": 40, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
		"children": []any{map[string]any{"nodeId": 41, "nodeName": "#text", "nodeValue": cdMarker}},
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "importedDocument protected")
}

func TestChildDocSetChildNodesEvent(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"method": "DOM.setChildNodes", "sessionId": "S",
		"params": map[string]any{
			"parentId": 1,
			"nodes": []any{map[string]any{
				"nodeId": 2, "nodeName": "IFRAME", "frameId": "CD-EVT",
				"contentDocument": map[string]any{
					"nodeId": 3, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
					"children": []any{map[string]any{"nodeId": 4, "nodeName": "#text", "nodeValue": cdMarker}},
				},
			}},
		},
	}
	out := cdScrub(t, g, msg)
	if out != "" {
		t.Fatalf("setChildNodes event with protected child must drop: %s", out)
	}
}

func cdCaptureSnapshot(strings []any, docs []any) map[string]any {
	return map[string]any{
		"id": 2, "sessionId": "S",
		"result": map[string]any{
			"documents": docs,
			"strings":   strings,
		},
	}
}

func cdCaptureDoc(docURLIdx, baseURLIdx int, nodes map[string]any) map[string]any {
	if nodes == nil {
		nodes = map[string]any{
			"parentIndex":          []any{},
			"nodeType":             []any{},
			"nodeName":             []any{},
			"contentDocumentIndex": map[string]any{"index": []any{}, "value": []any{}},
		}
	}
	return map[string]any{
		"documentURL":     docURLIdx,
		"baseURL":         baseURLIdx,
		"title":           -1,
		"contentLanguage": -1,
		"encodingName":    -1,
		"publicId":        -1,
		"systemId":        -1,
		"frameId":         -1,
		"nodes":           nodes,
		"layout": map[string]any{
			"nodeIndex":        []any{},
			"bounds":           []any{},
			"styles":           []any{},
			"text":             []any{},
			"stackingContexts": []any{},
		},
		"textBoxes": map[string]any{"layoutIndex": []any{}, "bounds": []any{}, "start": []any{}, "length": []any{}},
	}
}

func TestChildDocCaptureSnapshotOrdinaryUnchanged(t *testing.T) {
	g := cdGuard(t)
	stringsTable := []any{cdOrdinary, "ordinary-title", "visible-text"}
	nodes := map[string]any{
		"parentIndex":          []any{-1},
		"nodeType":             []any{1},
		"nodeName":             []any{1},
		"contentDocumentIndex": map[string]any{"index": []any{}, "value": []any{}},
	}
	msg := cdCaptureSnapshot(stringsTable, []any{cdCaptureDoc(0, 0, nodes)})
	raw := encode(msg)
	out := cdScrubRaw(t, g, raw)
	if string(out) != string(raw) {
		t.Fatalf("ordinary captureSnapshot must be byte-identical: got %s", truncate(string(out), 300))
	}
}

func TestChildDocCaptureSnapshotProtectedRefused(t *testing.T) {
	g := cdGuard(t)
	stringsTable := []any{cdOrdinary, cdSeven, cdMarker, "owner-text"}
	nodes := map[string]any{
		"parentIndex": []any{-1, 0},
		"nodeType":    []any{1, 1},
		"nodeName":    []any{3, 3},
		"contentDocumentIndex": map[string]any{
			"index": []any{0},
			"value": []any{1},
		},
	}
	msg := cdCaptureSnapshot(stringsTable, []any{
		cdCaptureDoc(0, 0, nodes),
		cdCaptureDoc(1, 1, map[string]any{
			"parentIndex":          []any{-1},
			"nodeType":             []any{9},
			"nodeName":             []any{2},
			"contentDocumentIndex": map[string]any{"index": []any{}, "value": []any{}},
		}),
	})
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, refusedTarget) {
		t.Fatalf("protected captureSnapshot document must refuse whole reply: %s", out)
	}
	if strings.Contains(out, cdMarker) || strings.Contains(out, `"documents"`) {
		t.Fatalf("refused captureSnapshot must not deliver documents/marker: %s", out)
	}
}

func TestChildDocCaptureSnapshotBadIndexRefused(t *testing.T) {
	g := cdGuard(t)
	msg := cdCaptureSnapshot([]any{cdOrdinary}, []any{cdCaptureDoc(0, 99, nil)})
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, refusedTarget) {
		t.Fatalf("out-of-bounds StringIndex must refuse: %s", out)
	}
}

func TestChildDocCaptureSnapshotEmptyDocuments(t *testing.T) {
	g := cdGuard(t)
	msg := cdCaptureSnapshot([]any{}, []any{})
	raw := encode(msg)
	out := cdScrubRaw(t, g, raw)
	if string(out) != string(raw) {
		t.Fatalf("empty documents[] must pass unchanged: %s", string(out))
	}
}

func TestChildDocCaptureSnapshotIntURLsWithoutStringsRefused(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 20, "sessionId": "S",
		"result": map[string]any{
			"documents": []any{map[string]any{
				"documentURL": 0, "baseURL": 0,
				"nodes": map[string]any{"parentIndex": []any{}, "nodeType": []any{}, "nodeName": []any{}, "contentDocumentIndex": map[string]any{"index": []any{}, "value": []any{}}},
			}},
		},
	}
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, refusedTarget) {
		t.Fatalf("documents[] with int urls and no sibling strings[] must refuse: %s", out)
	}
}

func TestChildDocCaptureSnapshotLiteralProtectedRefused(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 21, "sessionId": "S",
		"result": map[string]any{
			"documents": []any{map[string]any{
				"documentURL": cdSeven, "baseURL": cdSeven,
				"nodes": map[string]any{"parentIndex": []any{}, "nodeType": []any{}, "nodeName": []any{}, "contentDocumentIndex": map[string]any{"index": []any{}, "value": []any{}}},
			}},
		},
	}
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, refusedTarget) {
		t.Fatalf("literal protected documentURL string must refuse: %s", out)
	}
}

func TestChildDocCaptureSnapshotAbsentURLFieldsRefused(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 22, "sessionId": "S",
		"result": map[string]any{
			"documents": []any{map[string]any{
				"nodes": map[string]any{"parentIndex": []any{}, "nodeType": []any{}, "nodeName": []any{}, "contentDocumentIndex": map[string]any{"index": []any{}, "value": []any{}}},
			}},
			"strings": []any{cdOrdinary},
		},
	}
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, refusedTarget) {
		t.Fatalf("document with absent documentURL/baseURL must refuse: %s", out)
	}
}

func TestChildDocCaptureSnapshotLiteralOrdinaryPasses(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 23, "sessionId": "S",
		"result": map[string]any{
			"documents": []any{map[string]any{
				"documentURL": cdOrdinary, "baseURL": cdOrdinary,
				"title": -1, "contentLanguage": -1, "encodingName": -1, "publicId": -1, "systemId": -1, "frameId": -1,
				"nodes": map[string]any{
					"parentIndex": []any{}, "nodeType": []any{}, "nodeName": []any{},
					"contentDocumentIndex": map[string]any{"index": []any{}, "value": []any{}},
				},
				"layout":    map[string]any{"nodeIndex": []any{}, "bounds": []any{}, "styles": []any{}, "text": []any{}, "stackingContexts": []any{}},
				"textBoxes": map[string]any{"layoutIndex": []any{}, "bounds": []any{}, "start": []any{}, "length": []any{}},
			}},
		},
	}
	raw := encode(msg)
	out := cdScrubRaw(t, g, raw)
	if string(out) != string(raw) {
		t.Fatalf("literal ordinary documentURL/baseURL strings must pass unchanged: %s", truncate(string(out), 300))
	}
}

func TestChildDocCaptureSnapshotNonStringNonIntURLRefused(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 24, "sessionId": "S",
		"result": map[string]any{
			"documents": []any{map[string]any{
				"documentURL": cdOrdinary, "baseURL": true,
				"nodes": map[string]any{"parentIndex": []any{}, "nodeType": []any{}, "nodeName": []any{}, "contentDocumentIndex": map[string]any{"index": []any{}, "value": []any{}}},
			}},
			"strings": []any{cdOrdinary},
		},
	}
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, refusedTarget) {
		t.Fatalf("non-string non-int documentURL/baseURL must refuse: %s", out)
	}
}

func TestChildDocGetSnapshotProtectedRefused(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 12, "sessionId": "S",
		"result": map[string]any{
			"domNodes": []any{
				map[string]any{
					"nodeType": 1, "nodeName": "IFRAME", "backendNodeId": 1,
					"documentURL": cdOrdinary, "baseURL": cdOrdinary,
					"contentDocumentIndex": 1,
				},
				map[string]any{
					"nodeType": 9, "nodeName": "#document", "backendNodeId": 2,
					"documentURL": cdSeven, "baseURL": cdSeven,
					"nodeValue": cdMarker,
				},
			},
		},
	}
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, refusedTarget) {
		t.Fatalf("getSnapshot with protected content document must refuse: %s", out)
	}
}

func TestChildDocGetSnapshotOrdinaryUnchanged(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 13, "sessionId": "S",
		"result": map[string]any{
			"domNodes": []any{
				map[string]any{
					"nodeType": 9, "nodeName": "#document", "backendNodeId": 1,
					"documentURL": cdOrdinary, "baseURL": cdOrdinary,
				},
				map[string]any{
					"nodeType": 1, "nodeName": "DIV", "backendNodeId": 2, "nodeValue": "ok",
				},
			},
		},
	}
	raw := encode(msg)
	out := cdScrubRaw(t, g, raw)
	if string(out) != string(raw) {
		t.Fatalf("ordinary getSnapshot must be byte-identical: %s", truncate(string(out), 300))
	}
}

func TestChildDocDescribeNodePierce(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 3, "sessionId": "S",
		"result": map[string]any{
			"node": map[string]any{
				"nodeId": 1, "nodeName": "IFRAME", "frameId": "CD-DESC",
				"contentDocument": map[string]any{
					"nodeId": 2, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
					"children": []any{map[string]any{"nodeId": 3, "nodeName": "#text", "nodeValue": cdMarker}},
				},
			},
		},
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "describeNode pierce protected")
}

func cdWideTree(n int) map[string]any {
	children := make([]any, 0, n)
	for i := 0; i < n; i++ {
		children = append(children, map[string]any{"nodeId": i + 10, "nodeName": "DIV", "nodeValue": fmt.Sprintf("n%d", i)})
	}
	return map[string]any{
		"id": 9, "sessionId": "S",
		"result": map[string]any{
			"root": map[string]any{
				"nodeId": 1, "nodeName": "#document", "documentURL": cdOrdinary, "baseURL": cdOrdinary,
				"children": children,
			},
		},
	}
}

func cdDeepTree(depth int) map[string]any {
	var node any = map[string]any{"nodeId": depth + 10, "nodeName": "DIV", "nodeValue": "leaf"}
	for i := depth - 1; i >= 0; i-- {
		node = map[string]any{
			"nodeId": i + 10, "nodeName": "DIV",
			"children": []any{node},
		}
	}
	return map[string]any{
		"id": 10, "sessionId": "S",
		"result": map[string]any{
			"root": map[string]any{
				"nodeId": 1, "nodeName": "#document", "documentURL": cdOrdinary, "baseURL": cdOrdinary,
				"children": []any{node},
			},
		},
	}
}

func cdCountMaps(v any) int {
	switch t := v.(type) {
	case map[string]any:
		n := 1
		for _, val := range t {
			n += cdCountMaps(val)
		}
		return n
	case []any:
		n := 0
		for _, item := range t {
			n += cdCountMaps(item)
		}
		return n
	default:
		return 0
	}
}

func TestChildDocBigOrdinaryTree(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	msg := cdWideTree(100_000)
	before := cdCountMaps(msg)
	beforeBytes := encode(msg)
	out, elapsed := cdResponseLockedStrip(t, g, 9001, beforeBytes)
	if out == nil {
		t.Fatal("100k ordinary tree must deliver through responseLocked")
	}
	if strings.Contains(string(out), `"error"`) {
		t.Fatalf("100k ordinary tree must not be refused: %s", truncate(string(out), 200))
	}
	after, ok := decodeObject(out)
	if !ok {
		t.Fatal("scrubbed reply must decode")
	}
	if cdCountMaps(after) != before {
		t.Fatalf("ordinary 100k tree map count changed: before=%d after=%d", before, cdCountMaps(after))
	}
	g.mu.Lock()
	again, drop := g.applyChildDocStripLocked(append([]byte(nil), beforeBytes...), "DOM.getDocument", "S", nil)
	g.mu.Unlock()
	if drop || string(again) != string(cdStripThenScrub(t, g, append([]byte(nil), beforeBytes...), "DOM.getDocument", "S")) {
		t.Fatal("ordinary tree strip must be stable under g.mu")
	}
	t.Logf("ci-proof childdoc-big-ordinary walk-outside-g.mu t100=%s", elapsed)
}

func TestChildDocDeepOrdinaryTree(t *testing.T) {
	g := cdGuard(t)
	msg := cdDeepTree(500)
	before := cdCountMaps(msg)
	out := cdScrubRaw(t, g, encode(msg))
	if strings.Contains(string(out), `"error"`) {
		t.Fatalf("depth-500 ordinary tree must not be refused: %s", truncate(string(out), 200))
	}
	after, ok := decodeObject(out)
	if !ok {
		t.Fatal("scrubbed reply must decode")
	}
	if cdCountMaps(after) != before {
		t.Fatalf("depth-500 tree map count changed: before=%d after=%d", before, cdCountMaps(after))
	}
	if !strings.Contains(string(out), `"nodeValue":"leaf"`) && !strings.Contains(string(out), `"nodeValue": "leaf"`) {
		t.Fatalf("deep leaf must remain: %s", truncate(string(out), 200))
	}
}

func TestChildDocBigTreeStripOneProtected(t *testing.T) {
	g := cdGuard(t)
	msg := cdWideTree(100_000)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	children := root["children"].([]any)
	children[0] = map[string]any{
		"nodeId": 2, "nodeName": "IFRAME", "frameId": "CD-BIG",
		"attributes": []any{"src", cdSeven},
		"contentDocument": map[string]any{
			"nodeId": 3, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
			"children": []any{map[string]any{"nodeId": 4, "nodeName": "#text", "nodeValue": cdMarker}},
		},
	}
	out := cdScrub(t, g, msg)
	cdExpectRefused(t, out, "protected child in 100k tree")
	if strings.Contains(out, cdMarker) {
		t.Fatalf("refusal must not carry marker: %s", truncate(out, 200))
	}
}

func cdWideTreeWithProtected(n int) map[string]any {
	msg := cdWideTree(n)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	children := root["children"].([]any)
	children[0] = map[string]any{
		"nodeId": 2, "nodeName": "IFRAME", "frameId": "CD-SCALE",
		"contentDocument": map[string]any{
			"nodeId": 3, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
			"children": []any{map[string]any{"nodeId": 4, "nodeName": "#text", "nodeValue": cdMarker}},
		},
	}
	return msg
}

func cdBestOf3(measure func() time.Duration) time.Duration {
	best := time.Duration(1<<63 - 1)
	for range 3 {
		d := measure()
		if d < best {
			best = d
		}
	}
	return best
}

func cdResponseLockedStrip(t *testing.T, g *guard, id int64, raw []byte) (out []byte, elapsed time.Duration) {
	t.Helper()
	admit, _ := json.Marshal(map[string]any{"id": id, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, admit)
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("decode strip fixture: %v", err)
	}
	tree["id"] = id
	tree["sessionId"] = "S"
	reply := encode(tree)
	start := time.Now()
	out = cdDeliverBrowser(t, g, reply)
	elapsed = time.Since(start)
	return out, elapsed
}

func TestChildDocStripScaling(t *testing.T) {
	nextID := int64(1000)
	runOnce := func(n int) time.Duration {
		g := cdGuard(t)
		g.relaySession = "RELAY"
		nextID++
		raw := encode(cdWideTreeWithProtected(n))
		out, elapsed := cdResponseLockedStrip(t, g, nextID, raw)
		if out == nil {
			t.Fatalf("%d-node refuse delivered nil", n)
		}
		if !strings.Contains(string(out), `"error"`) || strings.Contains(string(out), cdMarker) {
			t.Fatalf("%d-node refuse must error without marker: %s", n, truncate(string(out), 200))
		}
		return elapsed
	}
	_ = runOnce(50_000)
	t100 := cdBestOf3(func() time.Duration { return runOnce(100_000) })
	best400 := cdBestOf3(func() time.Duration { return runOnce(400_000) })
	limit := 6 * t100
	const ceiling = 10 * time.Second
	t.Logf("ci-proof childdoc-refuse-scaling walk-outside-g.mu t100=%s t400=%s ratio=%.2f ceiling=%s", t100, best400, float64(best400)/float64(t100), ceiling)
	if best400 > limit {
		t.Fatalf("refuse scaling not linear: t(400k)=%s > 6*t(100k)=%s", best400, limit)
	}
	if best400 > ceiling {
		t.Fatalf("t(400k)=%s exceeds absolute ceiling %s", best400, ceiling)
	}
}

func TestChildDocCaptureSnapshotScaleOrdinary(t *testing.T) {
	g := cdGuard(t)
	stringsTable := make([]any, 0, 500)
	docs := make([]any, 0, 500)
	for i := 0; i < 500; i++ {
		url := fmt.Sprintf("https://ord-%d.ordinary.test/", i)
		stringsTable = append(stringsTable, url)
		docs = append(docs, cdCaptureDoc(i, i, nil))
	}
	msg := cdCaptureSnapshot(stringsTable, docs)
	raw := encode(msg)
	out := cdScrubRaw(t, g, raw)
	if string(out) != string(raw) {
		t.Fatalf("500 ordinary captureSnapshot docs must be unchanged: %s", truncate(string(out), 200))
	}
}

func TestChildDocBothFieldsProtected(t *testing.T) {
	g := cdGuard(t)
	for _, row := range []struct {
		name, docURL, baseURL string
	}{
		{"protected-documentURL", cdSeven, "about:blank"},
		{"protected-baseURL", "about:blank", cdSeven},
	} {
		t.Run(row.name, func(t *testing.T) {
			msg := cdTree(row.docURL, cdMarker, nil)
			root := msg["result"].(map[string]any)["root"].(map[string]any)
			owner := root["children"].([]any)[0].(map[string]any)
			doc := owner["contentDocument"].(map[string]any)
			doc["documentURL"] = row.docURL
			doc["baseURL"] = row.baseURL
			cdExpectRefused(t, cdScrub(t, g, msg), row.name)
		})
	}
}

func TestChildDocOOPIFOwnerKeepsFrameId(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["frameId"] = "CD-OOPIF"
	owner["attributes"] = []any{"src", "https://other.ordinary.test/oopif"}
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, `"frameId":"CD-OOPIF"`) && !strings.Contains(out, `"frameId": "CD-OOPIF"`) {
		t.Fatalf("OOPIF owner without contentDocument must keep frameId: %s", out)
	}
}

func TestChildDocOrdinaryShadowOnly(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	delete(owner, "frameId")
	owner["shadowRoots"] = []any{map[string]any{
		"nodeId": 10, "nodeName": "#document-fragment",
		"children": []any{map[string]any{"nodeId": 11, "nodeName": "SPAN", "nodeValue": "shadow-ok"}},
	}}
	raw := encode(msg)
	out := cdScrubRaw(t, g, raw)
	if string(out) != string(raw) {
		t.Fatalf("ordinary shadow-only tree must be unchanged: %s", truncate(string(out), 300))
	}
}

func TestChildDocCredentialChildKept(t *testing.T) {
	g := cdGuard(t)
	cred := "https://ims-na1.adobelogin.com/login"
	out := cdScrub(t, g, cdTree(cred, "credential-text", nil))
	if !strings.Contains(out, "credential-text") || !strings.Contains(out, `"contentDocument"`) {
		t.Fatalf("credential-origin child must stay (sign-in pages stay drivable): %s", out)
	}
}

func cdNullNestMsg(field string) map[string]any {
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	if field == "children" {
		root["children"] = nil
		return msg
	}
	owner[field] = nil
	owner["frameId"] = "CD-NULL"
	return msg
}

func TestChildDocContentDocumentNull(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdNullNestMsg("contentDocument"))
	if strings.Contains(out, `"error"`) {
		t.Fatalf("null contentDocument must not refuse: %s", out)
	}
}

func TestChildDocChildrenNull(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdNullNestMsg("children"))
	if strings.Contains(out, `"error"`) {
		t.Fatalf("null children must not refuse: %s", out)
	}
}

func TestChildDocTemplateContentNull(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdNullNestMsg("templateContent"))
	if strings.Contains(out, `"error"`) {
		t.Fatalf("null templateContent must not refuse: %s", out)
	}
}

func TestChildDocShadowRootsNull(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdNullNestMsg("shadowRoots"))
	if strings.Contains(out, `"error"`) {
		t.Fatalf("null shadowRoots must not refuse: %s", out)
	}
}

func TestChildDocPseudoElementsNull(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdNullNestMsg("pseudoElements"))
	if strings.Contains(out, `"error"`) {
		t.Fatalf("null pseudoElements must not refuse: %s", out)
	}
}

func TestChildDocDistributedNodesNull(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdNullNestMsg("distributedNodes"))
	if strings.Contains(out, `"error"`) {
		t.Fatalf("null distributedNodes must not refuse: %s", out)
	}
}

func TestChildDocImportedDocumentNull(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdNullNestMsg("importedDocument"))
	if strings.Contains(out, `"error"`) {
		t.Fatalf("null importedDocument must not refuse: %s", out)
	}
}

func TestChildDocAssignedSlotNull(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdNullNestMsg("assignedSlot"))
	if strings.Contains(out, `"error"`) {
		t.Fatalf("null assignedSlot must not refuse: %s", out)
	}
}

func TestChildDocDistributedNodesEmpty(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["distributedNodes"] = []any{}
	raw := encode(msg)
	out := cdScrubRaw(t, g, raw)
	if string(out) != string(raw) {
		t.Fatalf("empty distributedNodes must pass unchanged: %s", truncate(string(out), 300))
	}
}

func TestChildDocAssignedSlotBackendNode(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["assignedSlot"] = map[string]any{
		"nodeType": 1, "nodeName": "SLOT", "backendNodeId": 99,
	}
	raw := encode(msg)
	out := cdScrubRaw(t, g, raw)
	if string(out) != string(raw) {
		t.Fatalf("assignedSlot BackendNode must pass unchanged: %s", truncate(string(out), 300))
	}
}

func TestChildDocRootWithoutDocumentURLKept(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 80, "sessionId": "S",
		"result": map[string]any{
			"root": map[string]any{
				"nodeName": "#document",
				"children": []any{map[string]any{"nodeId": 2, "nodeName": "DIV", "nodeValue": "root-ok"}},
			},
		},
	}
	raw := encode(msg)
	out := cdScrubRaw(t, g, raw)
	if strings.Contains(string(out), `"error"`) {
		t.Fatalf("root without documentURL must not refuse: %s", out)
	}
	if !strings.Contains(string(out), "root-ok") || !strings.Contains(string(out), `"root"`) {
		t.Fatalf("root without documentURL must stay: %s", out)
	}
}

func TestChildDocDescribeNodeDocumentWithoutURLKept(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 81, "sessionId": "S",
		"result": map[string]any{
			"node": map[string]any{
				"nodeName": "#document",
				"children": []any{map[string]any{"nodeId": 2, "nodeName": "DIV", "nodeValue": "desc-ok"}},
			},
		},
	}
	out := cdScrub(t, g, msg)
	if strings.Contains(out, `"error"`) || !strings.Contains(out, "desc-ok") || !strings.Contains(out, `"node"`) {
		t.Fatalf("describeNode result.node without documentURL must stay: %s", out)
	}
}

func TestChildDocFlattenedDocumentWithoutURLKept(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 82, "sessionId": "S",
		"result": map[string]any{
			"nodes": []any{
				map[string]any{"nodeId": 1, "nodeName": "#document", "nodeType": 9, "nodeValue": "flat-root-ok"},
				map[string]any{"nodeId": 2, "nodeName": "DIV", "nodeValue": "flat-child-ok"},
			},
		},
	}
	out := cdScrub(t, g, msg)
	if strings.Contains(out, `"error"`) || !strings.Contains(out, "flat-root-ok") || !strings.Contains(out, "flat-child-ok") {
		t.Fatalf("getFlattenedDocument top-level nodes without documentURL must stay: %s", out)
	}
}

func TestChildDocSetChildNodesDocumentWithoutURLKept(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"method": "DOM.setChildNodes", "sessionId": "S",
		"params": map[string]any{
			"parentId": 1,
			"nodes": []any{map[string]any{
				"nodeId": 2, "nodeName": "#document", "nodeType": 9, "nodeValue": "set-nodes-ok",
			}},
		},
	}
	out := cdScrub(t, g, msg)
	if strings.Contains(out, `"error"`) || !strings.Contains(out, "set-nodes-ok") {
		t.Fatalf("setChildNodes params.nodes top-level document without documentURL must stay: %s", out)
	}
}

func TestChildDocChildNodeInsertedDocumentWithoutURLKept(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"method": "DOM.childNodeInserted", "sessionId": "S",
		"params": map[string]any{
			"parentNodeId": 1, "previousNodeId": 0,
			"node": map[string]any{
				"nodeId": 2, "nodeName": "#document", "nodeType": 9, "nodeValue": "inserted-ok",
			},
		},
	}
	out := cdScrub(t, g, msg)
	if strings.Contains(out, `"error"`) || !strings.Contains(out, "inserted-ok") || !strings.Contains(out, `"node"`) {
		t.Fatalf("childNodeInserted params.node without documentURL must stay: %s", out)
	}
}

func TestChildDocDeeperRootKeyDocumentStripped(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["root"] = map[string]any{
		"nodeId": 90, "nodeName": "#document", "nodeType": 9,
		"documentURL": cdSeven, "baseURL": cdSeven,
		"children": []any{map[string]any{"nodeId": 91, "nodeName": "#text", "nodeValue": cdMarker}},
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "deeper key root protected")
}

func TestChildDocDeeperNodeKeyDocumentStripped(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["node"] = map[string]any{
		"nodeId": 92, "nodeName": "#document", "nodeType": 9,
		"documentURL": cdSeven, "baseURL": cdSeven,
		"children": []any{map[string]any{"nodeId": 93, "nodeName": "#text", "nodeValue": cdMarker}},
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "deeper key node protected")
}

func TestChildDocUnknownKeyDocumentStripped(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["futureChildDocument"] = map[string]any{
		"nodeId": 90, "nodeName": "#document", "nodeType": 9,
		"documentURL": cdSeven, "baseURL": cdSeven,
		"children": []any{map[string]any{"nodeId": 91, "nodeName": "#text", "nodeValue": cdMarker}},
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "unknown key protected document")
}

func TestChildDocExtensionFrameContentDocumentKept(t *testing.T) {
	g := cdGuard(t)
	ext := "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob/frame.html"
	msg := cdTree(ext, "ext-frame-text", nil)
	out := cdScrub(t, g, msg)
	if strings.Contains(out, `"error"`) {
		t.Fatalf("extension-frame contentDocument must not refuse: %s", out)
	}
	if !strings.Contains(out, `"contentDocument"`) || !strings.Contains(out, "ext-frame-text") {
		t.Fatalf("extension-frame contentDocument must stay (marked elsewhere): %s", out)
	}
}

func TestChildDocExtensionFrameOwnerMarkingStillHappens(t *testing.T) {
	o, err := protect.New([]string{"chrome-extension://abcdefghijklmnopabcdefghijklmnop"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/", ctx: "default", document: "L1", documentRevision: 1}
	g.targets["FRAME"] = &target{id: "FRAME", typ: "iframe", url: "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob/frame.html", ctx: "default", parentFrame: "PAGE"}
	g.sessions["S"] = &session{id: "S", target: "PAGE", typ: "page"}
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	g.noteExtensionFrameURLLocked("PAGE", "FRAME", "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob/frame.html")
	raw, _ := json.Marshal(map[string]any{"id": 3, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": 0}})
	c, n, ok := parseCommand(raw)
	if !ok {
		t.Fatal("parse DOM.getDocument")
	}
	if v := g.fromCommand(c, n, ok); v.forward == nil {
		t.Fatalf("DOM.getDocument refused: %s", v.reply)
	}
	ext := "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob/frame.html"
	reply := encode(map[string]any{
		"id": 3, "sessionId": "S",
		"result": map[string]any{
			"root": map[string]any{
				"nodeName": "#document",
				"children": []any{map[string]any{
					"nodeName": "IFRAME", "frameId": "FRAME", "nodeId": 2, "backendNodeId": 2,
					"contentDocument": map[string]any{
						"nodeId": 3, "nodeName": "#document", "documentURL": ext, "baseURL": ext,
						"children": []any{map[string]any{"nodeId": 4, "nodeName": "#text", "nodeValue": "ext-frame-text"}},
					},
				}},
			},
		},
	})
	v := g.fromBrowser(reply)
	var infoID int64
	for _, msg := range v.send {
		var call struct {
			ID        int64  `json:"id"`
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(msg, &call) == nil && call.Method == "Target.getTargetInfo" && call.SessionID == "RELAY" {
			infoID = call.ID
		}
	}
	if infoID == 0 {
		t.Fatalf("covered getDocument reply must identity-hold: forward=%s page=%d send=%d", v.forward, len(v.page), len(v.send))
	}
	info, _ := json.Marshal(map[string]any{
		"id": infoID, "sessionId": "RELAY",
		"result": map[string]any{"targetInfo": map[string]any{"targetId": "PAGE", "type": "page", "url": "https://ordinary.test/"}},
	})
	settled := g.fromBrowser(info)
	delivered := settled.forward
	if delivered == nil && len(settled.page) > 0 {
		delivered = settled.page[0]
	}
	if delivered == nil {
		g.mu.Lock()
		page := g.takePendingPageLocked()
		g.mu.Unlock()
		if len(page) > 0 {
			delivered = page[0]
		}
	}
	if delivered == nil {
		t.Fatal("getDocument reply dropped after identity settle")
	}
	out := string(g.scrub(append([]byte(nil), delivered...)))
	if !strings.Contains(out, `"sliccExtensionFrame"`) {
		t.Fatalf("extension-frame owner node unmarked after strip: %s", out)
	}
	if !strings.Contains(out, `"contentDocument"`) {
		t.Fatalf("extension-frame contentDocument must remain for marking path: %s", out)
	}
}

func TestChildDocNonExtensionProtectedChildStripped(t *testing.T) {
	TestChildDocStripProtectedMarker(t)
}

func TestChildDocNonStringURLStripped(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, cdMarker, nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	doc := owner["contentDocument"].(map[string]any)
	doc["documentURL"] = 42
	doc["baseURL"] = true
	cdExpectRefused(t, cdScrub(t, g, msg), "non-string URL fields")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func TestChildDocGetDocumentCovered(t *testing.T) {
	TestChildDocStripProtectedMarker(t)
}

func TestChildDocGetFlattenedDocumentCovered(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 4, "sessionId": "S",
		"result": map[string]any{
			"nodes": []any{
				map[string]any{"nodeId": 4, "parentId": 3, "backendNodeId": 40, "nodeName": "#text", "nodeValue": cdMarker},
				map[string]any{"nodeId": 5, "parentId": 3, "backendNodeId": 50, "nodeName": "BODY"},
				map[string]any{"nodeId": 3, "parentId": 2, "backendNodeId": 30, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven},
				map[string]any{
					"nodeId": 2, "parentId": 1, "backendNodeId": 20, "nodeName": "IFRAME", "frameId": "CD-FLAT",
					"contentDocument": map[string]any{
						"nodeId": 3, "backendNodeId": 30, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven,
					},
				},
				map[string]any{"nodeId": 1, "backendNodeId": 10, "nodeName": "#document", "nodeType": 9, "documentURL": cdOrdinary, "baseURL": cdOrdinary},
			},
		},
	}
	out := cdScrub(t, g, msg)
	cdExpectRefused(t, out, "flattened protected child")
}

func cdFlattenedUAShadowMsg() map[string]any {
	return map[string]any{
		"id": 4, "sessionId": "S",
		"result": map[string]any{
			"nodes": []any{
				map[string]any{"nodeId": 9, "parentId": 8, "backendNodeId": 90, "nodeName": "#text", "nodeValue": cdMarker},
				map[string]any{"nodeId": 8, "parentId": 7, "backendNodeId": 80, "nodeName": "DIV"},
				map[string]any{
					"nodeId": 6, "parentId": 5, "backendNodeId": 60, "nodeName": "INPUT",
					"shadowRoots": []any{map[string]any{
						"nodeId": 7, "backendNodeId": 70, "nodeName": "#document-fragment", "shadowRootType": "user-agent",
					}},
				},
				map[string]any{"nodeId": 5, "parentId": 4, "backendNodeId": 50, "nodeName": "BODY"},
				map[string]any{"nodeId": 4, "parentId": 3, "backendNodeId": 40, "nodeName": "HTML"},
				map[string]any{
					"nodeId": 2, "parentId": 1, "backendNodeId": 20, "nodeName": "IFRAME", "frameId": "CD-UA",
					"contentDocument": map[string]any{
						"nodeId": 3, "backendNodeId": 30, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven,
					},
				},
				map[string]any{"nodeId": 1, "backendNodeId": 10, "nodeName": "#document", "nodeType": 9, "documentURL": cdOrdinary, "baseURL": cdOrdinary},
			},
		},
	}
}

func TestChildDocFlattenedUAShadowRootPurged(t *testing.T) {
	g := cdGuard(t)
	out := cdScrub(t, g, cdFlattenedUAShadowMsg())
	cdExpectRefused(t, out, "UA-shadow under protected child")
}

func cdFlattenedChromeNode(id, parent int64, name string, extra map[string]any) map[string]any {
	n := map[string]any{
		"nodeId": id, "parentId": parent, "backendNodeId": id * 10, "nodeType": 1, "nodeName": name,
		"localName": strings.ToLower(name), "nodeValue": "", "childNodeCount": 0, "attributes": []any{},
	}
	for k, v := range extra {
		n[k] = v
	}
	return n
}

func TestChildDocFlattenedNestedFramePurged(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 4, "sessionId": "S",
		"result": map[string]any{
			"nodes": []any{
				cdFlattenedChromeNode(12, 11, "#text", map[string]any{"nodeType": 3, "nodeValue": cdMarker}),
				cdFlattenedChromeNode(8, 5, "IFRAME", map[string]any{"frameId": "CD-NEST", "contentDocument": map[string]any{
					"nodeId": 9, "backendNodeId": 90, "nodeName": "#document", "nodeType": 9, "documentURL": "about:srcdoc", "baseURL": "about:srcdoc",
				}}),
				cdFlattenedChromeNode(5, 4, "BODY", nil),
				cdFlattenedChromeNode(4, 3, "HTML", nil),
				cdFlattenedChromeNode(2, 1, "IFRAME", map[string]any{"frameId": "CD-OUTER", "contentDocument": map[string]any{
					"nodeId": 3, "backendNodeId": 30, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven,
				}}),
				map[string]any{"nodeId": 1, "backendNodeId": 10, "nodeName": "#document", "nodeType": 9, "documentURL": cdOrdinary, "baseURL": cdOrdinary},
			},
		},
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "nested frame inside protected child")
}

func TestChildDocFlattenedTemplateContentPurged(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 4, "sessionId": "S",
		"result": map[string]any{
			"nodes": []any{
				cdFlattenedChromeNode(8, 7, "#text", map[string]any{"nodeType": 3, "nodeValue": cdMarker}),
				cdFlattenedChromeNode(5, 4, "TEMPLATE", map[string]any{"templateContent": map[string]any{"nodeId": 6, "backendNodeId": 60, "nodeName": "#document-fragment"}}),
				cdFlattenedChromeNode(4, 3, "BODY", nil),
				cdFlattenedChromeNode(2, 1, "IFRAME", map[string]any{"frameId": "CD-TMPL", "contentDocument": map[string]any{
					"nodeId": 3, "backendNodeId": 30, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven,
				}}),
				map[string]any{"nodeId": 1, "backendNodeId": 10, "nodeName": "#document", "nodeType": 9, "documentURL": cdOrdinary, "baseURL": cdOrdinary},
			},
		},
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "template inside protected child")
}

func TestChildDocFlattenedPostOrderChainPurged(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 4, "sessionId": "S",
		"result": map[string]any{
			"nodes": []any{
				cdFlattenedChromeNode(6, 5, "#text", map[string]any{"nodeType": 3, "nodeValue": cdMarker}),
				cdFlattenedChromeNode(5, 4, "SPAN", nil),
				cdFlattenedChromeNode(4, 3, "BODY", nil),
				cdFlattenedChromeNode(3, 7, "HTML", nil),
				cdFlattenedChromeNode(2, 1, "IFRAME", map[string]any{"frameId": "CD-CHAIN", "contentDocument": map[string]any{
					"nodeId": 7, "backendNodeId": 70, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven,
				}}),
				map[string]any{"nodeId": 1, "backendNodeId": 10, "nodeName": "#document", "nodeType": 9, "documentURL": cdOrdinary, "baseURL": cdOrdinary},
			},
		},
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "post-order chain under protected child")
}

func cdFlattenedChromeOrdinaryMsg(n int) map[string]any {
	nodes := make([]any, 0, n+1)
	nodes = append(nodes, map[string]any{"nodeId": 1, "backendNodeId": 10, "nodeName": "#document", "nodeType": 9, "documentURL": cdOrdinary, "baseURL": cdOrdinary})
	for i := 1; i <= n; i++ {
		parent := int64(1)
		if i > 1 {
			parent = int64(i - 1 + 10)
		}
		nodes = append(nodes, cdFlattenedChromeNode(int64(i+10), parent, "DIV", nil))
	}
	return map[string]any{"id": 4, "sessionId": "S", "result": map[string]any{"nodes": nodes}}
}

func cdFlattenedChromeProtectedMsg(n, depth int) map[string]any {
	nodes := make([]any, 0, n+3)
	for i := n; i >= 1; i-- {
		parent := int64(i - 1 + 10)
		if i == 1 {
			parent = 10
		}
		extra := map[string]any{}
		if depth > 0 && i <= depth {
			extra["attributes"] = []any{"data-d", fmt.Sprintf("%d", i)}
		}
		nodes = append(nodes, cdFlattenedChromeNode(int64(i+10), parent, "DIV", extra))
	}
	nodes = append(nodes,
		cdFlattenedChromeNode(2, 1, "IFRAME", map[string]any{"frameId": "CD-SCALE", "contentDocument": map[string]any{
			"nodeId": 10, "backendNodeId": 30, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven,
		}}),
		map[string]any{"nodeId": 1, "backendNodeId": 10, "nodeName": "#document", "nodeType": 9, "documentURL": cdOrdinary, "baseURL": cdOrdinary},
	)
	return map[string]any{"id": 4, "sessionId": "S", "result": map[string]any{"nodes": nodes}}
}

func TestChildDocFlattenedPurgeChainLinear(t *testing.T) {
	type row struct {
		n, depth int
	}
	rows := []row{
		{100_000, 50},
		{100_000, 500},
		{20_000, 20_000},
		{100_000, 100},
		{400_000, 200},
	}
	var times []time.Duration
	for _, r := range rows {
		g := cdGuard(t)
		msg := cdFlattenedChromeProtectedMsg(r.n, r.depth)
		start := time.Now()
		out := cdScrub(t, g, msg)
		elapsed := time.Since(start)
		cdExpectRefused(t, out, fmt.Sprintf("flattened n=%d depth=%d", r.n, r.depth))
		times = append(times, elapsed)
		t.Logf("ci-proof flattened-refuse n=%d depth=%d elapsed=%s", r.n, r.depth, elapsed)
	}
	if times[1] > 6*times[0] {
		t.Fatalf("refuse walk must not depend on depth: n=100k depth50=%s depth500=%s", times[0], times[1])
	}
	const ceiling = 10 * time.Second
	if times[4] > ceiling {
		t.Fatalf("400k refuse walk %s exceeds %s", times[4], ceiling)
	}
	if times[4] > 6*times[3] {
		t.Fatalf("refuse scaling not linear: t(400k)=%s > 6*t(100k)=%s", times[4], 6*times[3])
	}
}

func TestChildDocFlattenedPurgeCycleSafe(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 4, "sessionId": "S",
		"result": map[string]any{
			"nodes": []any{
				cdFlattenedChromeNode(5, 4, "#text", map[string]any{"nodeType": 3, "nodeValue": cdMarker}),
				cdFlattenedChromeNode(4, 3, "DIV", nil),
				map[string]any{"nodeId": 3, "parentId": 4, "backendNodeId": 30, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven},
				cdFlattenedChromeNode(2, 1, "IFRAME", map[string]any{"frameId": "CD-CYCLE", "contentDocument": map[string]any{
					"nodeId": 3, "backendNodeId": 30, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven,
				}}),
				map[string]any{"nodeId": 1, "backendNodeId": 10, "nodeName": "#document", "nodeType": 9, "documentURL": cdOrdinary, "baseURL": cdOrdinary},
			},
		},
	}
	cdExpectRefused(t, cdScrub(t, g, msg), "cycle under protected child")
}

func TestChildDocDistributedNodesProtected(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "contentDocument")
	owner["distributedNodes"] = []any{map[string]any{
		"nodeId": 50, "nodeName": "IFRAME", "frameId": "CD-DIST",
		"contentDocument": map[string]any{
			"nodeId": 51, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
			"children": []any{map[string]any{"nodeId": 52, "nodeName": "#text", "nodeValue": cdMarker}},
		},
	}}
	cdExpectRefused(t, cdScrub(t, g, msg), "distributedNodes protected child")
}

func TestChildDocUnknownShapeRefused(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdOrdinary, "top", nil)
	root := msg["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	owner["contentDocument"] = "not-an-object"
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, refusedTarget) {
		t.Fatalf("unknown contentDocument shape must be refused: %s", out)
	}
}

func TestChildDocChildNodeInsertedEvent(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"method": "DOM.childNodeInserted", "sessionId": "S",
		"params": map[string]any{
			"parentNodeId": 1, "previousNodeId": 0,
			"node": map[string]any{
				"nodeId": 2, "nodeName": "IFRAME", "frameId": "CD-INS",
				"contentDocument": map[string]any{
					"nodeId": 3, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
					"children": []any{map[string]any{"nodeId": 4, "nodeName": "#text", "nodeValue": cdMarker}},
				},
			},
		},
	}
	out := cdScrub(t, g, msg)
	if out != "" {
		t.Fatalf("childNodeInserted with protected child must drop: %s", out)
	}
}

func TestChildDocAdmitMeasureLogged(t *testing.T) {
	var lines []string
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	g.relaySession = "RELAY"
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: cdOrdinary, ctx: "default", document: "L1", documentRevision: 1}
	g.sessions["S"] = &session{id: "S", target: "PAGE", typ: "page"}
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 1, "sessionId": "S", "method": "DOM.getDocument", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	for _, line := range lines {
		if strings.Contains(line, "child-doc admit measure") {
			t.Fatalf("admit alone must not log child-doc measure; lines=%v", lines)
		}
	}
	v := g.fromBrowser(encode(cdTree(cdSeven, cdMarker, nil)))
	_ = v
	found := false
	for _, line := range lines {
		if strings.Contains(line, "child-doc admit measure") && strings.Contains(line, "getDocumentRefused=true") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("refuse+mark must log child-doc measure; lines=%v", lines)
	}
}

func TestChildDocRuntimeEvaluateChildrenStringByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(11, map[string]any{"children": "Click me"})
	cdByteIdentical(t, g, msg, "Runtime.evaluate")
}

func TestChildDocRuntimeCallFunctionOnChildrenStringByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(12, map[string]any{"children": "Click me"})
	cdByteIdentical(t, g, msg, "Runtime.callFunctionOn")
}

func TestChildDocRuntimeEvaluateNestedChildrenTypeByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(13, map[string]any{"props": map[string]any{"children": map[string]any{"type": "b"}}})
	cdByteIdentical(t, g, msg, "Runtime.evaluate")
}

func TestChildDocRuntimeCallFunctionOnNestedChildrenTypeByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(14, map[string]any{"props": map[string]any{"children": map[string]any{"type": "b"}}})
	cdByteIdentical(t, g, msg, "Runtime.callFunctionOn")
}

func TestChildDocRuntimeEvaluateDocumentsStringsByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(15, map[string]any{
		"documents": []any{map[string]any{"title": "a"}},
		"strings":   []any{"x"},
	})
	cdByteIdentical(t, g, msg, "Runtime.evaluate")
}

func TestChildDocRuntimeCallFunctionOnDocumentsStringsByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(16, map[string]any{
		"documents": []any{map[string]any{"title": "a"}},
		"strings":   []any{"x"},
	})
	cdByteIdentical(t, g, msg, "Runtime.callFunctionOn")
}

func TestChildDocRuntimeEvaluateDocumentsWithoutStringsByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(17, map[string]any{"documents": []any{map[string]any{"title": "a"}}})
	cdByteIdentical(t, g, msg, "Runtime.evaluate")
}

func TestChildDocRuntimeCallFunctionOnDocumentsWithoutStringsByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(18, map[string]any{"documents": []any{map[string]any{"title": "a"}}})
	cdByteIdentical(t, g, msg, "Runtime.callFunctionOn")
}

func TestChildDocRuntimeEvaluateNodeTypeNineByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(19, map[string]any{"name": "x", "nodeType": 9})
	cdByteIdentical(t, g, msg, "Runtime.evaluate")
}

func TestChildDocRuntimeCallFunctionOnNodeTypeNineByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(20, map[string]any{"name": "x", "nodeType": 9})
	cdByteIdentical(t, g, msg, "Runtime.callFunctionOn")
}

func TestChildDocRuntimeEvaluateNestedDocumentKeyByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(21, map[string]any{"a": map[string]any{"nodeName": "#document"}})
	cdByteIdentical(t, g, msg, "Runtime.evaluate")
}

func TestChildDocRuntimeCallFunctionOnNestedDocumentKeyByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(22, map[string]any{"a": map[string]any{"nodeName": "#document"}})
	cdByteIdentical(t, g, msg, "Runtime.callFunctionOn")
}

func TestChildDocRuntimeEvaluateProtectedLookingDocumentDataByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(23, cdProtectedLookingValue())
	cdByteIdentical(t, g, msg, "Runtime.evaluate")
}

func TestChildDocRuntimeCallFunctionOnProtectedLookingDocumentDataByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := cdRuntimeValueReply(24, cdProtectedLookingValue())
	cdByteIdentical(t, g, msg, "Runtime.callFunctionOn")
}

func TestChildDocRuntimeConsoleAPICalledByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"method": "Runtime.consoleAPICalled", "sessionId": "S",
		"params": map[string]any{
			"type": "log",
			"args": []any{
				map[string]any{"type": "object", "value": map[string]any{
					"children": "Click me",
					"nested":   map[string]any{"documents": []any{map[string]any{"title": "a"}}, "strings": []any{"x"}},
					"doc":      map[string]any{"nodeName": "#document", "nodeType": 9, "documentURL": cdSeven},
				}},
			},
		},
	}
	want := string(encode(msg))
	got := string(g.scrub(encode(msg)))
	if got != want {
		t.Fatalf("Runtime.consoleAPICalled must stay byte-identical\nwant=%s\ngot=%s", want, got)
	}
}

func TestChildDocRuntimeBindingCalledByteIdentical(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"method": "Runtime.bindingCalled", "sessionId": "S",
		"params": map[string]any{
			"name": "slicc",
			"payload": map[string]any{
				"children":  "Click me",
				"documents": []any{map[string]any{"title": "a"}},
				"strings":   []any{"x"},
				"a":         map[string]any{"nodeName": "#document", "nodeType": 9},
			},
		},
	}
	want := string(encode(msg))
	got := string(g.scrub(encode(msg)))
	if got != want {
		t.Fatalf("Runtime.bindingCalled must stay byte-identical\nwant=%s\ngot=%s", want, got)
	}
}

func TestChildDocUnrecordedReplyNotWalked(t *testing.T) {
	g := cdGuard(t)
	msg := cdTree(cdSeven, cdMarker, nil)
	got := string(g.scrub(encode(msg)))
	if !strings.Contains(got, cdMarker) || !strings.Contains(got, `"contentDocument"`) {
		t.Fatalf("unrecorded reply must not strip child documents: %s", got)
	}
	if strings.Contains(got, `"error"`) {
		t.Fatalf("unrecorded reply must not refuse: %s", got)
	}
}

func TestChildDocPendingMethodClearedWithPending(t *testing.T) {
	g := cdGuard(t)
	raw, _ := json.Marshal(map[string]any{"id": 8, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	g.mu.Lock()
	if g.pendingMethods[callKey{"S", 8}] != "DOM.getDocument" {
		g.mu.Unlock()
		t.Fatalf("admit must carry method on pending")
	}
	g.mu.Unlock()
	reply := encode(cdTree(cdSeven, cdMarker, nil))
	var tree map[string]any
	_ = json.Unmarshal(reply, &tree)
	tree["id"] = 8
	out := cdDeliverBrowser(t, g, encode(tree))
	if out == nil {
		t.Fatal("expected delivered reply")
	}
	cdExpectRefused(t, string(out), "pendingMethods reply path")
	g.mu.Lock()
	if _, ok := g.pending[callKey{"S", 8}]; ok {
		g.mu.Unlock()
		t.Fatal("pending must clear with reply")
	}
	if _, ok := g.pendingMethods[callKey{"S", 8}]; ok {
		g.mu.Unlock()
		t.Fatal("pendingMethods must clear with pending")
	}
	g.mu.Unlock()
}

func TestChildDocMethodCapRefusesNewRequest(t *testing.T) {
	g := cdGuard(t)
	g.mu.Lock()
	for i := int64(0); i < 64; i++ {
		g.setPendingLocked(callKey{"S", i}, kindPlain, "DOM.getDocument")
	}
	g.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"id": 64, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": 0}})
	cdAdmit(t, g, raw)
	g.mu.Lock()
	if g.pendingMethods[callKey{"S", 64}] != "DOM.getDocument" {
		g.mu.Unlock()
		t.Fatal("production pending-bound methods must admit without a separate id-keyed cap")
	}
	g.mu.Unlock()
}

func TestChildDocIdReuseWalkThenRuntimeByteIdentical(t *testing.T) {
	g := cdGuard(t)
	raw, _ := json.Marshal(map[string]any{"id": 42, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": 0}})
	cdAdmit(t, g, raw)
	g.mu.Lock()
	if g.pendingMethods[callKey{"S", 42}] != "DOM.getDocument" {
		g.mu.Unlock()
		t.Fatal("expected walk method on pending")
	}
	g.clearPendingLocked(callKey{"S", 42})
	g.mu.Unlock()
	raw2, _ := json.Marshal(map[string]any{"id": 42, "method": "Runtime.evaluate", "sessionId": "S", "params": map[string]any{"expression": "1"}})
	cdAdmit(t, g, raw2)
	g.mu.Lock()
	if _, ok := g.pendingMethods[callKey{"S", 42}]; ok {
		g.mu.Unlock()
		t.Fatal("non-walk admit must not leave a walk method")
	}
	g.mu.Unlock()
	msg := cdRuntimeValueReply(42, cdProtectedLookingValue())
	cdByteIdentical(t, g, msg, "Runtime.evaluate")
}

func TestChildDocDetachClearsPendingMethodRecords(t *testing.T) {
	g := cdGuard(t)
	g.mu.Lock()
	g.sessions["OTHER"] = &session{id: "OTHER", target: "PAGE", typ: "page"}
	for i := int64(0); i < 8; i++ {
		g.setPendingLocked(callKey{"S", i}, kindPlain, "DOM.getDocument")
	}
	g.setPendingLocked(callKey{"OTHER", 1}, kindPlain, "DOM.describeNode")
	if len(g.pendingMethods) != 9 {
		g.mu.Unlock()
		t.Fatalf("setup size=%d", len(g.pendingMethods))
	}
	g.forgetSessionLocked("S")
	if n := len(g.pendingMethods); n != 1 {
		g.mu.Unlock()
		t.Fatalf("detach must clear session pendingMethods, left=%d want 1 other", n)
	}
	if _, ok := g.pendingMethods[callKey{"OTHER", 1}]; !ok {
		g.mu.Unlock()
		t.Fatal("other session method must remain")
	}
	g.forgetSessionLocked("OTHER")
	if len(g.pendingMethods) != 0 {
		g.mu.Unlock()
		t.Fatalf("map must be empty after all detaches, left=%d", len(g.pendingMethods))
	}
	g.mu.Unlock()
}

func TestChildDocDroppedIdentityHoldClearsMethodRecord(t *testing.T) {
	g := cdGuard(t)
	k := callKey{"S", 9}
	g.mu.Lock()
	g.setPendingLocked(k, kindPlain, "DOM.getDocument")
	g.clearPendingLocked(k)
	if len(g.pendingMethods) != 0 {
		g.mu.Unlock()
		t.Fatalf("clearing pending must clear method, left=%d", len(g.pendingMethods))
	}
	g.docIdentityHolds[k] = &docIdentityHold{id: 9, session: "S", target: "PAGE", data: []byte(`{"id":9}`)}
	g.dropDocIdentityHoldLocked(k)
	if len(g.pendingMethods) != 0 {
		g.mu.Unlock()
		t.Fatalf("method must already be gone with pending before hold drop, left=%d", len(g.pendingMethods))
	}
	g.mu.Unlock()
}

func TestChildDocIdentityHoldReleaseStripsProtectedChild(t *testing.T) {
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/", ctx: "default", document: "L1", documentRevision: 1}
	g.sessions["S"] = &session{id: "S", target: "PAGE", typ: "page"}
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 5, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	c, n, ok := parseCommand(raw)
	if !ok {
		t.Fatal("parse")
	}
	if v := g.fromCommand(c, n, ok); v.forward == nil {
		t.Fatalf("getDocument refused: %s", v.reply)
	}
	reply, _ := json.Marshal(map[string]any{
		"id": 5, "sessionId": "S",
		"result": map[string]any{
			"root": map[string]any{
				"nodeId": 1, "nodeName": "#document", "documentURL": cdOrdinary, "baseURL": cdOrdinary,
				"children": []any{map[string]any{
					"nodeId": 2, "nodeName": "IFRAME", "frameId": "CD-IFRAME",
					"contentDocument": map[string]any{
						"nodeId": 3, "nodeName": "#document", "documentURL": cdSeven, "baseURL": cdSeven,
						"children": []any{map[string]any{"nodeId": 4, "nodeName": "#text", "nodeValue": cdMarker}},
					},
				}},
			},
		},
	})
	v := g.fromBrowser(reply)
	delivered := v.forward
	if delivered == nil && len(v.page) > 0 {
		delivered = v.page[0]
	}
	if delivered == nil || !strings.Contains(string(delivered), `"error"`) {
		t.Fatalf("protected child must refuse without identity hold: forward=%s page=%d send=%d", v.forward, len(v.page), len(v.send))
	}
	if strings.Contains(string(delivered), cdMarker) {
		t.Fatalf("refusal must not carry marker: %s", delivered)
	}
	g.mu.Lock()
	h := g.docIdentityHolds[callKey{"S", 5}]
	g.mu.Unlock()
	if h != nil {
		t.Fatalf("error refusal must not enter identity hold")
	}
}

func TestChildDocDescribeNodeProtectedDocumentRefused(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 9, "sessionId": "S",
		"result": map[string]any{
			"node": map[string]any{
				"nodeId": 3, "nodeName": "#document", "nodeType": 9,
				"documentURL": cdSeven, "baseURL": cdSeven, "frameId": "CHILD-FRAME",
				"children": []any{map[string]any{"nodeId": 4, "nodeName": "#text", "nodeValue": cdMarker}},
			},
		},
	}
	out := cdScrub(t, g, msg)
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, refusedTarget) {
		t.Fatalf("describeNode of protected child document must refuse: %s", out)
	}
}

func TestChildDocSetChildNodesUnderChildDocumentFrameContent(t *testing.T) {
	g := cdGuard(t)
	g.mu.Lock()
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: cdOrdinary, ctx: "default", document: "L1", documentRevision: 1, protected: false}
	g.worlds["PAGE"] = &worldSet{contexts: map[int64]*worldCtx{
		1: {protectedFrame: "CHILD"},
	}}
	g.mu.Unlock()
	cmd, _, ok := parseCommand([]byte(`{"id":1,"sessionId":"S","method":"DOM.requestChildNodes","params":{"nodeId":3}}`))
	if !ok {
		t.Fatal("parse")
	}
	g.mu.Lock()
	reason := g.frameContentCheckLocked(cmd, "PAGE")
	g.mu.Unlock()
	if reason != refusedTarget {
		t.Fatalf("frames-protected must refuse DOM under child document via frameContentCheckLocked (worlds.go frameContentCheckLocked); got %q", reason)
	}
}

func TestChildDocRefusedEventDropped(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"method": "DOM.setChildNodes", "sessionId": "S",
		"params": map[string]any{
			"parentId": 1,
			"nodes": []any{map[string]any{
				"nodeId": 2, "nodeName": "IFRAME",
				"contentDocument": "not-an-object",
			}},
		},
	}
	v := g.fromBrowser(encode(msg))
	if v.forward != nil || len(v.page) != 0 {
		t.Fatalf("refused event must be dropped, not forwarded: forward=%s page=%d", v.forward, len(v.page))
	}
}

func TestChildDocBusyRefusalSameKeyStillStrips(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 7, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	busy, _, ok := parseCommand([]byte(`{"id":7,"sessionId":"S","method":"Runtime.evaluate","params":{"expression":"1"}}`))
	if !ok {
		t.Fatal("parse busy")
	}
	ref := g.fromCommand(busy, 0, true)
	if ref.reply == nil {
		t.Fatal("same-key command must busy-refuse")
	}
	_ = cdWritePage(g, ref.reply)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 7
	out := cdDeliverBrowser(t, g, encode(tree))
	if out == nil {
		t.Fatal("chrome reply must deliver")
	}
	cdExpectRefused(t, string(out), "busy refusal must not disable refuse of chrome reply")
}

func TestChildDocEarlyRefusalSameKeyStillStrips(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 7, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	early, _, ok := parseCommand([]byte(`{"id":7,"sessionId":"S","method":"Page.navigate","params":{"url":"about:blank"}}`))
	if !ok {
		t.Fatal("parse early")
	}
	ref := g.fromCommand(early, 0, true)
	if ref.reply == nil && ref.forward == nil {
		t.Fatal("early same-key must answer")
	}
	if ref.reply != nil {
		_ = cdWritePage(g, ref.reply)
	}
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 7
	out := cdDeliverBrowser(t, g, encode(tree))
	if out == nil {
		t.Fatal("chrome reply must deliver")
	}
	cdExpectRefused(t, string(out), "early refusal must not disable refuse")
}

func TestChildDocSliccActionSameKeyStillStrips(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 7, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	ack, _, ok := parseCommand([]byte(`{"id":7,"sessionId":"S","method":"Slicc.action","params":{}}`))
	if !ok {
		t.Fatal("parse action")
	}
	ref := g.fromCommand(ack, 0, true)
	if ref.reply == nil {
		t.Fatal("Slicc.action must ack")
	}
	_ = cdWritePage(g, ref.reply)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 7
	out := cdDeliverBrowser(t, g, encode(tree))
	if out == nil {
		t.Fatal("chrome reply must deliver")
	}
	cdExpectRefused(t, string(out), "Slicc.action ack must not disable refuse")
}

func TestChildDocIdentityHoldSameKeyStillStrips(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 7, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 7
	v := g.fromBrowser(encode(tree))
	delivered := v.forward
	if delivered == nil && len(v.page) > 0 {
		delivered = v.page[0]
	}
	if delivered == nil || !strings.Contains(string(delivered), `"error"`) {
		t.Fatalf("protected pierce must refuse: forward=%s", v.forward)
	}
	g.mu.Lock()
	h := g.docIdentityHolds[callKey{"S", 7}]
	g.mu.Unlock()
	if h != nil {
		t.Fatalf("error refusal must not enter identity hold")
	}
}

func TestChildDocIdentityHoldDataAlreadyStripped(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 5, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 5
	v := g.fromBrowser(encode(tree))
	if v.forward == nil || !strings.Contains(string(v.forward), `"error"`) {
		t.Fatalf("protected pierce must refuse on the wire: forward=%s", v.forward)
	}
	if strings.Contains(string(v.forward), cdMarker) {
		t.Fatalf("refusal must not carry marker: %s", v.forward)
	}
	g.mu.Lock()
	h := g.docIdentityHolds[callKey{"S", 5}]
	g.mu.Unlock()
	if h != nil {
		t.Fatalf("error refusal must not enter identity hold")
	}
}

func TestChildDocDecodeFailureWalkReplyRefused(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	raw, _ := json.Marshal(map[string]any{"id": 7, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	id := int64(7)
	m := &message{ID: &id, SessionID: "S"}
	g.mu.Lock()
	v := g.responseLocked(m, []byte(`null`))
	g.mu.Unlock()
	if v.forward == nil {
		t.Fatal("decode failure on walk reply must refuse, not drop")
	}
	if !strings.Contains(string(v.forward), "slicc: refused:") {
		t.Fatalf("walk-method decode failure must be refusal: %s", v.forward)
	}
	if m.Error == nil {
		t.Fatal("refusal must refresh m.Error from stripped bytes")
	}
}

func TestChildDocMarkPageFlagWhenIframeTargetExists(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.mu.Lock()
	g.targets["CD-IFRAME"] = &target{id: "CD-IFRAME", typ: "iframe", url: "https://other.ordinary.test/frame", ctx: "default", parentFrame: "PAGE"}
	g.mu.Unlock()
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 11, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 11
	_ = cdDeliverBrowser(t, g, encode(tree))
	g.mu.Lock()
	iframe := g.targets["CD-IFRAME"]
	page := g.targets["PAGE"]
	g.mu.Unlock()
	if iframe == nil {
		t.Fatal("iframe target must remain")
	}
	if iframe.protected {
		t.Fatalf("live iframe target must not set protected directly; page flag covers it")
	}
	if page == nil || !page.childDocDOMBlocked {
		t.Fatalf("iframe frameId mark must latch the page so it survives iframe destroy")
	}
}

func TestChildDocMarkWorldLatchWhenFrameMissing(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.mu.Lock()
	g.worlds["PAGE"] = &worldSet{contexts: map[int64]*worldCtx{
		1: {frame: "CD-MISSING", defaultWorld: true},
	}}
	g.mu.Unlock()
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 12, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 12
	root := tree["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	owner["frameId"] = "CD-MISSING"
	_ = cdDeliverBrowser(t, g, encode(tree))
	g.mu.Lock()
	wc := g.worlds["PAGE"].contexts[1]
	page := g.targets["PAGE"]
	g.mu.Unlock()
	if wc == nil || wc.protectedFrame != "CD-MISSING" {
		t.Fatalf("missing iframe target must latch world protectedFrame; got %+v", wc)
	}
	if page == nil || !page.childDocDOMBlocked {
		t.Fatalf("world latch mark must also latch the page")
	}
}

func TestChildDocMarkPageFallback(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 13, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 13
	root := tree["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "frameId")
	_ = cdDeliverBrowser(t, g, encode(tree))
	g.mu.Lock()
	page := g.targets["PAGE"]
	blocked := page != nil && page.childDocDOMBlocked
	g.mu.Unlock()
	if !blocked {
		t.Fatalf("no frameId and no world must set page.childDocDOMBlocked")
	}
}

func TestChildDocMarkClearedOnDestroy(t *testing.T) {
	g := cdGuard(t)
	g.mu.Lock()
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: cdOrdinary, ctx: "default", document: "L1", documentRevision: 1, childDocDOMBlocked: true}
	g.worlds["PAGE"] = &worldSet{contexts: map[int64]*worldCtx{1: {frame: "F", protectedFrame: "F"}}}
	g.destroyedLocked("PAGE")
	page := g.targets["PAGE"]
	if page == nil || !page.gone || page.childDocDOMBlocked {
		t.Fatalf("destroy must clear childDocDOMBlocked; page=%+v", page)
	}
	if g.worlds["PAGE"] != nil {
		t.Fatalf("destroy must forget worlds")
	}
	g.mu.Unlock()
}

func TestChildDocMarkClearedOnRootDocument(t *testing.T) {
	g := cdGuard(t)
	g.mu.Lock()
	page := &target{id: "PAGE", typ: "page", url: cdOrdinary, ctx: "default", document: "L0", documentRevision: 1, childDocDOMBlocked: true}
	g.targets["PAGE"] = page
	g.worlds["PAGE"] = &worldSet{contexts: map[int64]*worldCtx{1: {frame: "F", protectedFrame: "F"}}}
	g.rootDocumentLocked(page, "L1")
	if page.childDocDOMBlocked {
		t.Fatalf("rootDocument must clear childDocDOMBlocked")
	}
	if g.worlds["PAGE"].contexts[1].protectedFrame != "" {
		t.Fatalf("rootDocument must clear world protectedFrame latches")
	}
	g.mu.Unlock()
}

func TestChildDocMarkedDropsLaterDOMEvent(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 14, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 14
	root := tree["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "frameId")
	_ = cdDeliverBrowser(t, g, encode(tree))
	evt := map[string]any{
		"method": "DOM.characterDataModified", "sessionId": "S",
		"params": map[string]any{"nodeId": 9, "characterData": cdMarker},
	}
	v := g.fromBrowser(encode(evt))
	if v.forward != nil || len(v.page) != 0 {
		t.Fatalf("marked page must drop later DOM content event: forward=%s", v.forward)
	}
}

func TestChildDocNonArrayNodesRefused(t *testing.T) {
	g := cdGuard(t)
	msg := map[string]any{
		"id": 1, "sessionId": "S",
		"result": map[string]any{
			"nodes": map[string]any{"nodeId": 1, "nodeName": "#document", "documentURL": cdSeven},
		},
	}
	cdExpectRefused(t, cdScrubMethod(t, g, msg, "DOM.getFlattenedDocument"), "non-array nodes")
}

func TestChildDocMuFreeDuringWalk(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 15, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdWideTreeWithProtected(50_000)
	tree["id"] = 15
	tree["sessionId"] = "S"
	reply := encode(tree)
	entered := make(chan struct{})
	release := make(chan struct{})
	ChildDocTestBeforeWalk = func() {
		close(entered)
		<-release
	}
	defer func() { ChildDocTestBeforeWalk = nil }()
	done := make(chan []byte, 1)
	go func() {
		done <- cdDeliverBrowser(t, g, reply)
	}()
	<-entered
	if !g.mu.TryLock() {
		close(release)
		t.Fatal("g.mu must be free at before-walk gate (walk outside lock)")
	}
	g.mu.Unlock()
	close(release)
	out := <-done
	cdExpectRefused(t, string(out), "outside-mu gated refuse")
}

func TestChildDocMuFreeDuringEventWalk(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	evt := encode(map[string]any{
		"method": "DOM.setChildNodes", "sessionId": "S",
		"params": map[string]any{
			"parentId": 1,
			"nodes": []any{
				map[string]any{
					"nodeId": 2, "nodeName": "IFRAME", "frameId": "CD-EVT-MU",
					"contentDocument": map[string]any{
						"nodeId": 3, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven,
						"children": make([]any, 50_000),
					},
				},
			},
		},
	})
	entered := make(chan struct{})
	release := make(chan struct{})
	ChildDocTestBeforeWalk = func() {
		close(entered)
		<-release
	}
	defer func() { ChildDocTestBeforeWalk = nil }()
	done := make(chan browserVerdict, 1)
	go func() {
		done <- g.fromBrowser(evt)
	}()
	<-entered
	if !g.mu.TryLock() {
		close(release)
		t.Fatal("g.mu must be free at before-walk gate for events (walk outside lock)")
	}
	g.mu.Unlock()
	close(release)
	v := <-done
	if v.forward != nil {
		t.Fatalf("protected setChildNodes must drop after outside-mu walk: forward=%s", v.forward)
	}
}

func TestChildDocMarkEmitsDocumentUpdated(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.domEnabled["S"] = true
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 17, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 17
	root := tree["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "frameId")
	v := g.fromBrowser(encode(tree))
	found := false
	for _, msg := range v.page {
		if strings.Contains(string(msg), `"DOM.documentUpdated"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("first mark that frames-protects must synthesize DOM.documentUpdated; page=%d forward=%s", len(v.page), v.forward)
	}
}

func TestChildDocMarkedDropsBrowserDocumentUpdated(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.domEnabled["S"] = true
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 18, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 18
	root := tree["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "frameId")
	_ = cdDeliverBrowser(t, g, encode(tree))
	evt := encode(map[string]any{"method": "DOM.documentUpdated", "sessionId": "S", "params": map[string]any{}})
	v := g.fromBrowser(evt)
	if v.forward != nil || len(v.page) != 0 {
		t.Fatalf("browser documentUpdated on marked target must drop: forward=%s page=%d", v.forward, len(v.page))
	}
}

func TestChildDocUnmarkedExtendedDropEventsByteIdentical(t *testing.T) {
	g := cdGuard(t)
	for _, method := range []string{
		"DOM.childNodeRemoved",
		"DOM.childNodeCountUpdated",
		"DOM.attributeModified",
		"DOM.attributeRemoved",
		"DOM.pseudoElementAdded",
		"DOM.pseudoElementRemoved",
		"DOM.shadowRootPushed",
		"DOM.shadowRootPopped",
		"DOM.distributedNodesUpdated",
		"DOM.inlineStyleInvalidated",
	} {
		msg := map[string]any{
			"method": method, "sessionId": "S",
			"params": map[string]any{"nodeId": 1, "name": "class", "value": "x", "childNodeCount": 1},
		}
		raw := encode(msg)
		v := g.fromBrowser(raw)
		got := v.forward
		if got == nil && len(v.page) > 0 {
			got = v.page[0]
		}
		if got == nil {
			t.Fatalf("%s on unmarked target must forward", method)
		}
		if string(got) != string(raw) {
			t.Fatalf("%s unmarked must be byte-identical\nwant=%s\ngot=%s", method, raw, got)
		}
	}
}

func TestChildDocMarkDropListMicrobench(t *testing.T) {
	for _, nTargets := range []int{50, 200} {
		g := cdGuard(t)
		g.mu.Lock()
		for i := 0; i < nTargets; i++ {
			id := "IF-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			g.targets[id] = &target{id: id, typ: "iframe", url: cdOrdinary, ctx: "default", parentFrame: "PAGE"}
		}
		g.targets["PAGE"].childDocDOMBlocked = true
		g.mu.Unlock()
		evt := encode(map[string]any{
			"method": "DOM.attributeModified", "sessionId": "S",
			"params": map[string]any{"nodeId": 1, "name": "class", "value": "x"},
		})
		const rounds = 2000
		start := time.Now()
		for i := 0; i < rounds; i++ {
			_ = g.fromBrowser(evt)
		}
		elapsed := time.Since(start)
		per := elapsed / rounds
		t.Logf("ci-proof childdoc-mark-drop-microbench targets=%d rounds=%d total=%s per=%s", nTargets, rounds, elapsed, per)
	}
}

func TestChildDocWalkRace(t *testing.T) {
	stop := make(chan struct{})
	var mu sync.Mutex
	var guards []*guard
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			mu.Lock()
			gs := append([]*guard(nil), guards...)
			mu.Unlock()
			for _, g := range gs {
				g.mu.Lock()
				sid := "RACE-" + string(rune('A'+(i%26)))
				if i%2 == 0 {
					g.sessions[sid] = &session{id: sid, target: "PAGE", typ: "page"}
					g.targets["T-"+sid] = &target{id: "T-" + sid, typ: "iframe", url: cdOrdinary, ctx: "default"}
				} else {
					delete(g.sessions, sid)
					delete(g.targets, "T-"+sid)
				}
				g.mu.Unlock()
			}
			i++
		}
	}()
	defer func() { close(stop); wg.Wait() }()
	for round := 0; round < 8; round++ {
		gOrd := cdGuard(t)
		mu.Lock()
		guards = append(guards, gOrd)
		mu.Unlock()
		idOrd := int64(100 + round)
		rawOrd, _ := json.Marshal(map[string]any{"id": idOrd, "method": "DOM.getFlattenedDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
		cdAdmit(t, gOrd, rawOrd)
		ordinary := cdFlattenedChromeOrdinaryMsg(300)
		ordinary["id"] = idOrd
		ordinary["sessionId"] = "S"
		ordinaryRaw := encode(ordinary)
		outOrd := cdDeliverBrowser(t, gOrd, ordinaryRaw)
		if string(outOrd) != string(ordinaryRaw) {
			t.Fatalf("ordinary flattened must be byte-identical round=%d\nwant=%s\ngot=%s", round, truncate(string(ordinaryRaw), 300), truncate(string(outOrd), 300))
		}

		gEvt := cdGuard(t)
		mu.Lock()
		guards = append(guards, gEvt)
		mu.Unlock()
		evt := encode(map[string]any{
			"method": "DOM.setChildNodes", "sessionId": "S",
			"params": map[string]any{
				"parentId": 1,
				"nodes": []any{
					map[string]any{"nodeId": 2, "nodeName": "DIV", "nodeType": 1},
				},
			},
		})
		vEvt := gEvt.fromBrowser(evt)
		if vEvt.forward == nil || string(vEvt.forward) != string(evt) {
			t.Fatalf("ordinary setChildNodes must be byte-identical round=%d forward=%s", round, truncate(string(vEvt.forward), 300))
		}
		protEvt := encode(map[string]any{
			"method": "DOM.setChildNodes", "sessionId": "S",
			"params": map[string]any{
				"parentId": 1,
				"nodes": []any{
					map[string]any{
						"nodeId": 2, "nodeName": "IFRAME", "frameId": "CD-EVT",
						"contentDocument": map[string]any{
							"nodeId": 3, "nodeName": "#document", "nodeType": 9, "documentURL": cdSeven, "baseURL": cdSeven,
						},
					},
				},
			},
		})
		vProtEvt := gEvt.fromBrowser(protEvt)
		if vProtEvt.forward != nil {
			t.Fatalf("protected setChildNodes must drop round=%d forward=%s", round, truncate(string(vProtEvt.forward), 300))
		}

		gFlat := cdGuard(t)
		mu.Lock()
		guards = append(guards, gFlat)
		mu.Unlock()
		idFlat := int64(300 + round)
		rawFlat, _ := json.Marshal(map[string]any{"id": idFlat, "method": "DOM.getFlattenedDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
		cdAdmit(t, gFlat, rawFlat)
		flat := cdFlattenedChromeProtectedMsg(20_000, 50)
		flat["id"] = idFlat
		flat["sessionId"] = "S"
		cdExpectRefused(t, string(cdDeliverBrowser(t, gFlat, encode(flat))), "protected flattened under race")

		gDoc := cdGuard(t)
		mu.Lock()
		guards = append(guards, gDoc)
		mu.Unlock()
		idDoc := int64(400 + round)
		rawDoc, _ := json.Marshal(map[string]any{"id": idDoc, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
		cdAdmit(t, gDoc, rawDoc)
		tree := cdWideTreeWithProtected(5_000)
		tree["id"] = idDoc
		tree["sessionId"] = "S"
		cdExpectRefused(t, string(cdDeliverBrowser(t, gDoc, encode(tree))), "protected getDocument pierce under race")
	}
	t.Logf("ci-proof childdoc-walk-race rounds=8 ordinary-flattened+event+protected-flattened+getDocument under concurrent sessions/targets mutate")
}

func TestChildDocNonChromeIDKeyRefused(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 41, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	delete(tree, "id")
	tree["ID"] = float64(41)
	out := cdDeliverBrowser(t, g, encode(tree))
	cdExpectRefused(t, string(out), "ID key must still refuse via message id")
}

func TestChildDocBothIDKeysPreferMessageID(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 41, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	delete(tree, "id")
	body, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(`{"id":7,"ID":41,` + string(body[1:]))
	out := cdDeliverBrowser(t, g, body)
	cdExpectRefused(t, string(out), "id+ID must refuse under message id")
	if !strings.Contains(string(out), `"id":41`) {
		t.Fatalf("refusal must carry message id 41, got %s", out)
	}
	if strings.Contains(string(out), `"id":7`) {
		t.Fatalf("refusal must not use map id 7, got %s", out)
	}
}

func TestChildDocErrorNullStillWalks(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 42, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 42
	tree["error"] = nil
	out := cdDeliverBrowser(t, g, encode(tree))
	cdExpectRefused(t, string(out), "error:null must not skip the walk")
}

func TestChildDocChildFrameNavigatedKeepsMark(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.domEnabled["S"] = true
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 43, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 43
	root := tree["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "frameId")
	_ = cdDeliverBrowser(t, g, encode(tree))
	evt := encode(map[string]any{
		"method": "Page.frameNavigated", "sessionId": "S",
		"params": map[string]any{"frame": map[string]any{"id": "CHILD", "parentId": "PAGE", "url": cdOrdinary}},
	})
	_ = g.fromBrowser(evt)
	g.mu.Lock()
	blocked := g.targets["PAGE"] != nil && g.targets["PAGE"].childDocDOMBlocked
	g.mu.Unlock()
	if !blocked {
		t.Fatal("child-frame frameNavigated must not clear page mark")
	}
}

func TestChildDocNavigatedWithinDocumentKeepsMark(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 44, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 44
	root := tree["result"].(map[string]any)["root"].(map[string]any)
	owner := root["children"].([]any)[0].(map[string]any)
	delete(owner, "frameId")
	_ = cdDeliverBrowser(t, g, encode(tree))
	evt := encode(map[string]any{
		"method": "Page.navigatedWithinDocument", "sessionId": "S",
		"params": map[string]any{"frameId": "PAGE", "url": cdOrdinary + "#x"},
	})
	_ = g.fromBrowser(evt)
	g.mu.Lock()
	blocked := g.targets["PAGE"] != nil && g.targets["PAGE"].childDocDOMBlocked
	g.mu.Unlock()
	if !blocked {
		t.Fatal("navigatedWithinDocument must not clear page mark")
	}
}

func TestChildDocPendingGoneDropsReply(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 45, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	g.mu.Lock()
	delete(g.pending, callKey{"S", 45})
	delete(g.pendingMethods, callKey{"S", 45})
	g.mu.Unlock()
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 45
	v := g.fromBrowser(encode(tree))
	if v.forward != nil || len(v.page) != 0 {
		t.Fatalf("pending gone between peek and re-lock must drop: forward=%s page=%d", v.forward, len(v.page))
	}
}

func TestChildDocPendingMethodMismatchRefuses(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 46, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	ChildDocTestBeforeWalk = func() {
		g.mu.Lock()
		g.pendingMethods[callKey{"S", 46}] = "DOM.describeNode"
		g.mu.Unlock()
	}
	defer func() { ChildDocTestBeforeWalk = nil }()
	tree := cdTree(cdOrdinary, "no-marker", nil)
	tree["id"] = 46
	root := tree["result"].(map[string]any)["root"].(map[string]any)
	root["documentURL"] = cdOrdinary
	root["baseURL"] = cdOrdinary
	v := g.fromBrowser(encode(tree))
	got := v.forward
	if got == nil && len(v.page) > 0 {
		got = v.page[0]
	}
	cdExpectRefused(t, string(got), "pendingMethods mismatch must refuse")
}

func TestChildDocIframeDestroyKeepsPageMark(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.mu.Lock()
	g.targets["CD-IFRAME"] = &target{id: "CD-IFRAME", typ: "iframe", url: "https://other.ordinary.test/frame", ctx: "default", parentFrame: "PAGE"}
	g.mu.Unlock()
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 47, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 47
	_ = cdDeliverBrowser(t, g, encode(tree))
	g.mu.Lock()
	g.destroyedLocked("CD-IFRAME")
	page := g.targets["PAGE"]
	blocked := page != nil && page.childDocDOMBlocked
	g.mu.Unlock()
	if !blocked {
		t.Fatal("page mark must survive iframe target destroy")
	}
}

func TestChildDocSerialReaderOrderingEventNotOvertake(t *testing.T) {
	g := cdGuard(t)
	g.relaySession = "RELAY"
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", session: "S", done: true, fetchArmed: true}
	raw, _ := json.Marshal(map[string]any{"id": 16, "method": "DOM.getDocument", "sessionId": "S", "params": map[string]any{"depth": -1, "pierce": true}})
	cdAdmit(t, g, raw)
	tree := cdTree(cdSeven, cdMarker, nil)
	tree["id"] = 16
	reply := encode(tree)
	evt := encode(map[string]any{
		"method": "DOM.childNodeCountUpdated", "sessionId": "S",
		"params": map[string]any{"nodeId": 1, "childNodeCount": 2},
	})
	var got []string
	v1 := g.fromBrowser(reply)
	if v1.forward != nil {
		got = append(got, "reply:"+string(v1.forward))
	}
	for _, p := range v1.page {
		got = append(got, "reply:"+string(p))
	}
	v2 := g.fromBrowser(evt)
	if v2.forward != nil {
		got = append(got, "event:"+string(v2.forward))
	}
	for _, p := range v2.page {
		got = append(got, "event:"+string(p))
	}
	if len(got) == 0 || !strings.Contains(got[0], `"error"`) {
		t.Fatalf("serial path must deliver refusal first; got=%v", got)
	}
	if len(got) > 1 {
		t.Fatalf("marked page must drop later event so it cannot overtake; got=%v", got)
	}
}
