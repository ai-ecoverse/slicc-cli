package cdp

import "encoding/json"

const (
	documentsOnlySignal       = "documents-only"
	refusedDocumentsRendering = "this browser shows slicc no frames, so a rendering of this page could show a protected one; read the page with Runtime.evaluate instead"
)

func documentsOnlyRefusal(raw json.RawMessage) bool {
	var e struct {
		Data string `json:"data"`
	}
	return json.Unmarshal(raw, &e) == nil && e.Data == documentsOnlySignal
}

func holdsDocuments(raw json.RawMessage) bool {
	var r struct {
		HoldsDocuments bool `json:"holdsDocuments"`
	}
	return json.Unmarshal(raw, &r) == nil && r.HoldsDocuments
}

func (g *guard) documentRequestPatterns() []any {
	var out []any
	for _, pattern := range g.o.DocumentPatterns() {
		out = append(out, map[string]any{"urlPattern": pattern, "requestStage": "Request", "resourceType": "Document"})
	}
	return out
}

func (g *guard) fetchStepLocked(nt *navTarget, session string, step func(m message) [][]byte) func(m message) [][]byte {
	return func(m message) [][]byte {
		if m.Error == nil || !documentsOnlyRefusal(m.Error) {
			return step(m)
		}
		g.logf("cdp: the browser pauses only documents here: %s", m.Error)
		return [][]byte{g.callbackLocked(session, "Fetch.enable", map[string]any{"patterns": g.documentRequestPatterns()}, func(res message) [][]byte {
			if res.Error == nil && !holdsDocuments(res.Result) {
				res.Error = json.RawMessage(`{"message":"the browser didn't say it holds documents"}`)
			}
			nt.documentsOnly = res.Error == nil
			return step(res)
		})}
	}
}

func (g *guard) contentRuleLocked(c *command) string {
	if reason := g.conformanceLocked(c); reason != "" {
		return reason
	}
	return g.documentsOnlyRuleLocked(c)
}

func (g *guard) documentsOnlyRuleLocked(c *command) string {
	s := g.sessions[c.session]
	if s == nil {
		return ""
	}
	nt := g.navTargets[s.target]
	if nt == nil || !nt.documentsOnly {
		return ""
	}
	if framePaintMethods[c.method] || c.method == "Page.printToPDF" {
		return refusedDocumentsRendering
	}
	return ""
}
