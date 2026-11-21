package cdp

func (g *guard) docConfirmObserveTargetLiveLocked(target string) bool {
	t := g.targets[target]
	return t != nil && !t.gone
}
