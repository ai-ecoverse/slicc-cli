package cdp

func childDocStripEnabled() bool {
	return true
}

func childDocShadowRecurse() bool {
	return true
}

func childDocRefuseUnknown() bool {
	return false
}

func childDocCaptureSnapshotRefuse() bool {
	return true
}

func childDocGetSnapshotRefuse() bool {
	return true
}

func childDocImportedStrip() bool {
	return true
}

func childDocEventsStrip() bool {
	return true
}

func childDocChildInsertedStrip() bool {
	return true
}

func childDocDistributedWalk() bool {
	return true
}

func childDocTemplatePseudo() bool {
	return true
}

func childDocStringIndexStrict() bool {
	return true
}

func childDocStripBeforeHold() bool {
	return true
}

func childDocRecordPendingMethods() bool {
	return true
}

func childDocWalkCommands() bool {
	return true
}

var ChildDocTestBeforeWalk func()

func childDocBeforeWalkHook() {
	if ChildDocTestBeforeWalk != nil {
		ChildDocTestBeforeWalk()
	}
}
