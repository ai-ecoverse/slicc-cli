package flow

import (
	"errors"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

const FallbackNotice = "No SLICC browser or debugging endpoint found, so seven opens in your default browser for pairing. Use --profile or --cdp to choose."

const KindPair browser.Kind = "default-browser"

const PairID = "default-browser"

var errPairAlone = errors.New("your default browser links without cdp, so it can't go with other browsers")

func Fallback(o browser.Options, entries []browser.Entry) bool {
	for _, e := range entries {
		switch {
		case IsPair(e) || e.Kind == browser.KindSix:
		case e.Kind == browser.KindEndpoint:
			return false
		case e.Kind == browser.KindInstalled && e.Installed != nil && e.Installed.Engine != browser.EngineElectron && len(Profiles(o, *e.Installed)) > 0:
			return false
		}
	}
	return true
}

func IsPair(e browser.Entry) bool { return e.Kind == KindPair }

func PairEntry() browser.Entry {
	return browser.Entry{ID: PairID, Kind: KindPair, Tier: browser.TierElectron, Name: "Default browser"}
}

func WithPair(o browser.Options, entries []browser.Entry) []browser.Entry {
	pair := PairEntry()
	if Fallback(o, entries) {
		pair.Tier = 0
		return append([]browser.Entry{pair}, entries...)
	}
	for i, e := range entries {
		if e.Tier >= browser.TierElectron {
			return append(append(entries[:i:i], pair), entries[i:]...)
		}
	}
	return append(entries, pair)
}
