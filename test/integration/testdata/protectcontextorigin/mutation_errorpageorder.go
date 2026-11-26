package protect

import "strings"

func AboutLookalike(origin string) bool {
	return strings.HasPrefix(strings.ToLower(origin), "about:") && origin != "about://"
}

func inheritedLookalike(raw string, ev Evidence) bool {
	return Inheriting(raw) && AboutLookalike(ev.ContextOrigin)
}

func (o *Origins) TargetWith(raw, typ string, previous bool, opener Opener, ev Evidence) bool {
	if HiddenType(typ) {
		return true
	}
	if inheritedLookalike(raw, ev) {
		return true
	}
	if ErrorPage(raw, ev) {
		return previous
	}
	return o.Target(raw, previous, opener)
}
