package cdp

func (g *guard) docLoaderCurrentLocked(target string) string {
	nt := g.navTargets[target]
	if nt != nil && nt.loaderID != "" {
		return nt.loaderID
	}
	return ""
}

func docLoaderReplaced(admitted, current string) bool {
	return docIdentityLoaderCompareEnabled() && admitted != current
}
