package browser

import (
	"sort"
	"strings"
)

var (
	TierSevenOpen = catalog.Rank.Tiers.SevenOpen
	TierEndpoint  = catalog.Rank.Tiers.Endpoint
	TierChrome    = catalog.Rank.Tiers.Chrome
	TierChromium  = catalog.Rank.Tiers.Chromium
	TierElectron  = catalog.Rank.Tiers.Electron
)

func installedTier(in Installed) int {
	switch {
	case in.Engine == EngineElectron:
		return TierElectron
	case in.ID == catalog.Rank.ChromeID:
		return TierChrome
	default:
		return TierChromium
	}
}

func endpointTier(ep Endpoint) int {
	if ep.Six {
		return TierSix
	}
	if ep.SevenOpen {
		return TierSevenOpen
	}
	return TierEndpoint
}

func EndpointID(ep Endpoint) string {
	return "cdp:" + endpointKey(ep.CDP)
}

func Rank(endpoints []Endpoint, installed []Installed) []Entry {
	eps := append([]Endpoint(nil), endpoints...)
	sortEndpoints(eps)
	var out []Entry
	for i := range eps {
		ep := eps[i]
		if major := ep.ChromiumMajor(); major > 0 && major < MinChromiumMajor {
			continue
		}
		out = append(out, Entry{ID: EndpointID(ep), Kind: KindEndpoint, Tier: endpointTier(ep), Name: ep.Name, Version: ep.Version, Endpoint: &ep})
	}
	ins := append([]Installed(nil), installed...)
	sortInstalled(ins)
	for i := range ins {
		in := ins[i]
		if !versionAllowed(in) {
			continue
		}
		out = append(out, Entry{ID: in.ID, Kind: KindInstalled, Tier: installedTier(in), Name: in.Name, Version: in.Version, Installed: &in})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tier < out[j].Tier })
	return out
}

func sortInstalled(ins []Installed) {
	sort.SliceStable(ins, func(i, j int) bool {
		a, b := ins[i], ins[j]
		if ta, tb := installedTier(a), installedTier(b); ta != tb {
			return ta < tb
		}
		if pa, pb := priority(a.ID), priority(b.ID); pa != pb {
			return pa < pb
		}
		if na, nb := strings.ToLower(a.Name), strings.ToLower(b.Name); na != nb {
			return na < nb
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Path < b.Path
	})
}

func versionAllowed(in Installed) bool {
	k, ok := knownByID(in.ID)
	if !ok || !k.chromium || in.Version == "" {
		return true
	}
	return majorOf(in.Version) == 0 || majorOf(in.Version) >= MinChromiumMajor
}

func majorOf(v string) int {
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func Best(entries []Entry) (Entry, bool) {
	if len(entries) == 0 {
		return Entry{}, false
	}
	return entries[0], true
}

func BestInstalled(entries []Entry) (Installed, bool) {
	for _, e := range entries {
		if e.Kind == KindInstalled && e.Installed.Engine == EngineChromium {
			return *e.Installed, true
		}
	}
	return Installed{}, false
}

func FindInstalled(installed []Installed, id string) (Installed, bool) {
	for _, in := range installed {
		if in.ID == id {
			return in, true
		}
	}
	lower := strings.ToLower(id)
	for _, in := range installed {
		if strings.ToLower(in.ID) == lower || strings.ToLower(in.Name) == lower {
			return in, true
		}
	}
	return Installed{}, false
}
