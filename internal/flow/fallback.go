package flow

import "github.com/ai-ecoverse/slicc-cli/internal/browser"

const FallbackNotice = "No SLICC browser or debugging endpoint found, so seven opens in your default browser for pairing. Use --profile or --cdp to choose."

func Fallback(o browser.Options, entries []browser.Entry) bool {
	for _, e := range entries {
		switch {
		case IsSix(e) || e.Kind == browser.KindSix:
		case e.Kind == browser.KindEndpoint:
			return false
		case e.Kind == browser.KindInstalled && e.Installed != nil && e.Installed.Engine != browser.EngineElectron && len(Profiles(o, *e.Installed)) > 0:
			return false
		}
	}
	return true
}
