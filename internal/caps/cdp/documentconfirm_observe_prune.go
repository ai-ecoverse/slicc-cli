package cdp

import "strings"

func (g *guard) pruneDocConfirmObserveTargetLocked(target string) {
	if target == "" {
		return
	}
	delete(g.docObserveAnnounce, target)
	delete(g.docObserveTyp, target)
	delete(g.docObservePath, target)
	prefix := target + "|"
	for key := range g.docObserveDone {
		if key == target || strings.HasPrefix(key, prefix) {
			delete(g.docObserveDone, key)
		}
	}
	for key := range g.docObserveWant {
		if strings.HasPrefix(key, prefix) {
			delete(g.docObserveWant, key)
		}
	}
}
