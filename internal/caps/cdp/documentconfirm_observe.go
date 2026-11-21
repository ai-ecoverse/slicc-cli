package cdp

import (
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func docConfirmTrackEnabled() bool {
	return docConfirmObserveDiagEnabled() || docConfirmBlankProtectedEnforceEnabled()
}

func docConfirmURLClass(url string) string {
	if url == "" {
		return "empty"
	}
	lower := strings.ToLower(url)
	switch {
	case lower == "about:blank" || strings.HasPrefix(lower, "about:blank"):
		return "blank"
	case lower == "about:srcdoc" || strings.HasPrefix(lower, "about:srcdoc"):
		return "about:srcdoc"
	case strings.HasPrefix(lower, "chrome-error:"):
		return "chrome-error"
	case strings.HasPrefix(lower, "chrome-extension:"):
		return "extension"
	case strings.HasPrefix(lower, "blob:"):
		return "blob"
	case strings.HasPrefix(lower, "filesystem:"):
		return "filesystem"
	case strings.HasPrefix(lower, "view-source:"):
		return "view-source"
	case protect.Unparseable(url):
		return "unparseable"
	}
	if c := docConfirmNewTabClass(url); c != "" {
		return c
	}
	s := protect.Scheme(url)
	if s == "http" || s == "https" || s == "data" {
		return "ordinary"
	}
	if protect.Inheriting(url) {
		return "ordinary"
	}
	return "other"
}

func docConfirmNewTabClass(url string) string {
	s := protect.Scheme(url)
	switch s {
	case "chrome", "chrome-untrusted", "edge", "brave", "opera", "vivaldi", "arc":
	default:
		return ""
	}
	h := strings.ToLower(protect.Host(url))
	switch h {
	case "new-tab-page", "newtab", "startpage":
		return s + "://" + h
	default:
		return ""
	}
}

func docConfirmOriginClass(origin string, o *protect.Origins) string {
	if origin == "" || origin == "://" || origin == "null" {
		return "opaque"
	}
	if o != nil && o.Loaded(origin) {
		return "protected"
	}
	s := protect.Scheme(origin)
	if s == "http" || s == "https" || s == "" {
		return "ordinary"
	}
	return "other"
}

func docConfirmProvenanceLabel(created, opened, errorOrdinary bool) string {
	switch {
	case created:
		return "created"
	case opened:
		return "opened"
	case errorOrdinary:
		return "errorProofOrdinary"
	default:
		return "none"
	}
}

func docConfirmIsBrowserUIURL(url string) bool {
	if strings.HasPrefix(strings.ToLower(url), "chrome-extension:") {
		return true
	}
	return docConfirmNewTabClass(url) != ""
}

func docConfirmAnnounceBlank(class string) bool {
	return class == "blank" || class == "empty"
}

func (g *guard) docConfirmObserveWouldVerdictLocked(frameURL, frameOrigin string) string {
	if frameURL == "" && frameOrigin == "" {
		return "admit"
	}
	url := frameURL
	if url == "" {
		return "admit"
	}
	if docConfirmIsBrowserUIURL(url) || chromeErrorDocument(url) {
		return "admit"
	}
	if g.o != nil && g.o.Loaded(url) {
		return "refuse-latch"
	}
	lowerProt := strings.ToLower(url)
	if strings.HasPrefix(lowerProt, "view-source:") || strings.HasPrefix(lowerProt, "blob:") || strings.HasPrefix(lowerProt, "filesystem:") {
		return "refuse-latch"
	}
	lower := strings.ToLower(url)
	blank := lower == "about:blank" || strings.HasPrefix(lower, "about:blank")
	if blank {
		switch docConfirmOriginClass(frameOrigin, g.o) {
		case "protected":
			return "refuse-latch"
		default:
			return "admit"
		}
	}
	if protect.Unparseable(url) {
		return "refuse-nonlatch"
	}
	if protect.Inheriting(url) {
		return "admit"
	}
	s := protect.Scheme(url)
	if s == "http" || s == "https" || s == "data" {
		return "admit"
	}
	return "admit"
}

func docConfirmObserveKey(target, path string) string {
	return target + "|" + path
}

func (g *guard) docConfirmObservePathLocked(target, typ string) string {
	if typ == "iframe" {
		return "nested"
	}
	if g.errorProofOfLocked(target) != errorProofNone {
		return "G"
	}
	if g.explicit[target] > 0 {
		return "explicit"
	}
	for _, t := range g.attachCalls {
		if t == target {
			return "explicit"
		}
	}
	if g.opened[target] || g.errorProofOfLocked(target) == errorProofOrdinary {
		return "provenance"
	}
	return "D"
}

func (g *guard) noteDocConfirmObserveLocked(target, path string) [][]byte {
	if !docConfirmTrackEnabled() || target == "" || path == "" {
		return nil
	}
	if !g.docConfirmObserveTargetLiveLocked(target) {
		return nil
	}
	key := docConfirmObserveKey(target, path)
	if g.docObserveDone[key] {
		return nil
	}
	if g.docObserveWant == nil {
		g.docObserveWant = map[string]bool{}
	}
	g.docObserveWant[key] = true
	if g.docObservePath == nil {
		g.docObservePath = map[string]string{}
	}
	if g.docObservePath[target] == "" {
		g.docObservePath[target] = path
	}
	if g.docObserveAnnounce == nil {
		g.docObserveAnnounce = map[string]string{}
	}
	if _, ok := g.docObserveAnnounce[target]; !ok {
		announced := ""
		if t := g.targets[target]; t != nil {
			announced = t.url
		}
		g.docObserveAnnounce[target] = announced
	}
	if g.docObserveTyp == nil {
		g.docObserveTyp = map[string]string{}
	}
	if g.docObserveTyp[target] == "" {
		typ := "page"
		if t := g.targets[target]; t != nil && t.typ != "" {
			typ = t.typ
		}
		g.docObserveTyp[target] = typ
	}
	return g.finishDocConfirmObserveLocked(target, path, false)
}

func (g *guard) docConfirmObserveEvidenceLocked(target string) (frameURL, frameOrigin string, ok bool) {
	nt := g.navTargets[target]
	switch {
	case nt != nil && nt.commits > 0:
		return nt.documentURL, nt.securityOrigin, true
	case nt != nil && nt.errorRecorded && nt.documentURL != "":
		return nt.documentURL, nt.securityOrigin, true
	case g.errorProofOfLocked(target) != errorProofNone:
		if t := g.targets[target]; t != nil {
			frameURL = t.url
		}
		if nt != nil {
			frameOrigin = nt.securityOrigin
			if frameURL == "" {
				frameURL = nt.documentURL
			}
		}
		return frameURL, frameOrigin, true
	default:
		return "", "", false
	}
}

func (g *guard) docConfirmBlankProtectedLatchLocked(announce, frameURL, frameOrigin string) bool {
	if !docConfirmBlankProtectedEnforceEnabled() {
		return false
	}
	if !docConfirmAnnounceBlank(docConfirmURLClass(announce)) {
		return false
	}
	if docConfirmIsBrowserUIURL(frameURL) || chromeErrorDocument(frameURL) {
		return false
	}
	return docConfirmOriginClass(frameOrigin, g.o) == "protected"
}

func (g *guard) docConfirmObserveFinishVerdictLocked(target, frameURL, frameOrigin string, atBound bool) (pathOverride, verdict string) {
	nt := g.navTargets[target]
	verdict = g.docConfirmObserveWouldVerdictLocked(frameURL, frameOrigin)
	if atBound && (nt == nil || nt.commits == 0) {
		verdict = "admit"
	}
	if chromeErrorDocument(frameURL) && g.errorProofOfLocked(target) != errorProofNone {
		return "G", "admit"
	}
	return "", verdict
}

func (g *guard) docConfirmObserveDiagLocked(target, path, announced, frameURL, frameOrigin, verdict string) {
	if !docConfirmObserveDiagEnabled() {
		return
	}
	kind := "would-refuse"
	if verdict == "admit" {
		kind = "would-admit"
	}
	typ := "page"
	if g.docObserveTyp != nil && g.docObserveTyp[target] != "" {
		typ = g.docObserveTyp[target]
	} else if t := g.targets[target]; t != nil && t.typ != "" {
		typ = t.typ
	}
	g.logDocConfirmObserveLocked(kind, docConfirmURLClass(announced), path, docConfirmURLClass(frameURL), docConfirmOriginClass(frameOrigin, g.o), typ, false, g.opened[target], g.errorProofOfLocked(target) == errorProofOrdinary, verdict)
}

func (g *guard) docConfirmObserveLatchSendLocked(target, announced, frameURL, frameOrigin string, atBound bool) [][]byte {
	if atBound || !g.docConfirmBlankProtectedLatchLocked(announced, frameURL, frameOrigin) {
		return nil
	}
	t := g.targets[target]
	if t == nil || t.gone {
		return nil
	}
	return g.markLocked(t)
}

func (g *guard) finishDocConfirmObserveLocked(target, path string, atBound bool) [][]byte {
	if !docConfirmTrackEnabled() || target == "" || path == "" || !g.docConfirmObserveTargetLiveLocked(target) {
		return nil
	}
	key := docConfirmObserveKey(target, path)
	if g.docObserveDone[key] {
		return nil
	}
	announced := ""
	if g.docObserveAnnounce != nil {
		announced = g.docObserveAnnounce[target]
	}
	frameURL, frameOrigin, ok := g.docConfirmObserveEvidenceLocked(target)
	if !ok && !atBound {
		return nil
	}
	override, verdict := g.docConfirmObserveFinishVerdictLocked(target, frameURL, frameOrigin, atBound)
	if override != "" {
		path = override
		key = docConfirmObserveKey(target, path)
		if g.docObserveDone[key] {
			return nil
		}
	}
	g.docConfirmObserveDiagLocked(target, path, announced, frameURL, frameOrigin, verdict)
	if g.docObserveDone == nil {
		g.docObserveDone = map[string]bool{}
	}
	g.docObserveDone[key] = true
	delete(g.docObserveWant, key)
	return g.docConfirmObserveLatchSendLocked(target, announced, frameURL, frameOrigin, atBound)
}

func (g *guard) noteExplicitDocConfirmObserveLocked(target string) [][]byte {
	return g.noteDocConfirmObserveLocked(target, "explicit")
}

func (g *guard) fallthroughDocConfirmObserveLocked(target string) {
	prefix := target + "|"
	if g.docObserveWant == nil {
		return
	}
	var keys []string
	for key := range g.docObserveWant {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		path := strings.TrimPrefix(key, prefix)
		_ = g.finishDocConfirmObserveLocked(target, path, true)
	}
}

func (g *guard) logDocConfirmObserveLocked(kind, announceClass, path, frameClass, originClass, typ string, created, opened, errorOrdinary bool, verdict string) {
	if !docConfirmObserveDiagEnabled() {
		return
	}
	if typ == "" {
		typ = "page"
	}
	line := "cdp: DIAG document-confirm " + kind +
		" class=" + announceClass +
		" path=" + path +
		" frame_class=" + frameClass +
		" origin_class=" + originClass +
		" provenance=" + docConfirmProvenanceLabel(created, opened, errorOrdinary) +
		" type=" + typ +
		" verdict=" + verdict
	g.logf("%s", line)
}

func (g *guard) docConfirmObserveAfterCommitLocked(nt *navTarget, before int) [][]byte {
	if nt == nil || nt.commits == before || !docConfirmTrackEnabled() {
		return nil
	}
	if !g.docConfirmObserveTargetLiveLocked(nt.target) {
		return nil
	}
	var keys []string
	prefix := nt.target + "|"
	for key := range g.docObserveWant {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	var send [][]byte
	for _, key := range keys {
		path := strings.TrimPrefix(key, prefix)
		send = append(send, g.finishDocConfirmObserveLocked(nt.target, path, false)...)
	}
	return send
}

func (g *guard) scheduleDocConfirmObserveLocked(target, path string) [][]byte {
	return g.noteDocConfirmObserveLocked(target, path)
}
