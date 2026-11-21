package cdp

import (
	"strings"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

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
	case strings.Contains(lower, "new-tab-page"):
		return "chrome://new-tab-page"
	case strings.Contains(lower, "newtab"):
		return "chrome://newtab"
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
	s := protect.Scheme(url)
	if s == "http" || s == "https" || s == "data" {
		return "ordinary"
	}
	if protect.Inheriting(url) {
		return "ordinary"
	}
	return "other"
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

func (g *guard) docConfirmObserveWouldVerdictLocked(frameURL, frameOrigin string) string {
	if frameURL == "" && frameOrigin == "" {
		return "admit"
	}
	url := frameURL
	if url == "" {
		return "admit"
	}
	if g.o != nil && g.o.Loaded(url) {
		return "refuse-latch"
	}
	lowerProt := strings.ToLower(url)
	if !chromeErrorDocument(url) && (strings.HasPrefix(lowerProt, "view-source:") || strings.HasPrefix(lowerProt, "blob:") || strings.HasPrefix(lowerProt, "filesystem:")) {
		return "refuse-latch"
	}
	lower := strings.ToLower(url)
	blank := lower == "about:blank" || strings.HasPrefix(lower, "about:blank")
	if blank {
		switch docConfirmOriginClass(frameOrigin, g.o) {
		case "protected":
			return "refuse-latch"
		case "opaque", "ordinary":
			return "admit"
		default:
			return "refuse-nonlatch"
		}
	}
	if chromeErrorDocument(url) {
		return "admit"
	}
	if protect.Unparseable(url) {
		return "refuse-nonlatch"
	}
	if strings.HasPrefix(lower, "view-source:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "filesystem:") {
		return "refuse-nonlatch"
	}
	if protect.Inheriting(url) {
		return "admit"
	}
	s := protect.Scheme(url)
	if s == "http" || s == "https" || s == "data" {
		return "admit"
	}
	return "refuse-nonlatch"
}

func (g *guard) docConfirmObserveFilterEventLocked(session, target string) bool {
	return false
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

func (g *guard) noteDocConfirmObserveLocked(target, path string) {
	if !docConfirmObserveEnabled() || target == "" {
		return
	}
	if g.docObserveDone[target] {
		return
	}
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
	g.tryLogDocConfirmObserveLocked(target, false)
}

func (g *guard) tryLogDocConfirmObserveLocked(target string, atBound bool) {
	if !docConfirmObserveEnabled() || target == "" || g.docObserveDone[target] {
		return
	}
	path := ""
	if g.docObservePath != nil {
		path = g.docObservePath[target]
	}
	if path == "" {
		return
	}
	announced := ""
	if g.docObserveAnnounce != nil {
		announced = g.docObserveAnnounce[target]
	}
	frameURL := ""
	frameOrigin := ""
	nt := g.navTargets[target]
	switch {
	case nt != nil && nt.commits > 0:
		frameURL = nt.documentURL
		frameOrigin = nt.securityOrigin
	case nt != nil && nt.errorRecorded && nt.documentURL != "":
		frameURL = nt.documentURL
		frameOrigin = nt.securityOrigin
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
	case !atBound:
		return
	}
	announceClass := docConfirmURLClass(announced)
	frameClass := docConfirmURLClass(frameURL)
	originClass := docConfirmOriginClass(frameOrigin, g.o)
	verdict := g.docConfirmObserveWouldVerdictLocked(frameURL, frameOrigin)
	if atBound && (nt == nil || nt.commits == 0) {
		verdict = "admit"
	}
	if chromeErrorDocument(frameURL) && g.errorProofOfLocked(target) != errorProofNone {
		path = "G"
		verdict = "admit"
	}
	kind := "would-refuse"
	if verdict == "admit" {
		kind = "would-admit"
	}
	opened := g.opened[target]
	errorOrdinary := g.errorProofOfLocked(target) == errorProofOrdinary
	g.logDocConfirmObserveLocked(kind, announceClass, path, frameClass, originClass, false, opened, errorOrdinary, verdict)
	if g.docObserveDone == nil {
		g.docObserveDone = map[string]bool{}
	}
	g.docObserveDone[target] = true
}

func (g *guard) fallthroughDocConfirmObserveLocked(target string) {
	g.tryLogDocConfirmObserveLocked(target, true)
}

func (g *guard) logDocConfirmObserveLocked(kind, announceClass, path, frameClass, originClass string, created, opened, errorOrdinary bool, verdict string) {
	line := "cdp: DIAG document-confirm " + kind +
		" class=" + announceClass +
		" path=" + path +
		" frame_class=" + frameClass +
		" origin_class=" + originClass +
		" provenance=" + docConfirmProvenanceLabel(created, opened, errorOrdinary) +
		" verdict=" + verdict
	g.logf("%s", line)
	if g.docObserveLog == nil {
		g.docObserveLog = []string{}
	}
	g.docObserveLog = append(g.docObserveLog, line)
}

func (g *guard) docConfirmObserveAfterCommitLocked(nt *navTarget, before int) {
	if nt == nil || nt.commits == before {
		return
	}
	g.tryLogDocConfirmObserveLocked(nt.target, false)
}

//nolint:unparam
func (g *guard) scheduleDocConfirmObserveLocked(target, clientSession, path string) [][]byte {
	_ = clientSession
	g.noteDocConfirmObserveLocked(target, path)
	return nil
}
