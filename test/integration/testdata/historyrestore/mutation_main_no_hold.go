package cdp

func historyRestoreConfirmAtNavCommit() bool         { return false }
func historyRestoreDetachOldChildren() bool          { return false }
func historyRestoreHoldChildren() bool               { return false }
func historyRestoreHoldWorkers() bool                { return false }
func historyRestoreHoldExplicitAttach() bool         { return false }
func historyRestoreIdentityCompare() bool            { return false }
func historyRestoreDeadlineHides() bool              { return false }
func historyRestoreWaitForConfirm() bool             { return false }
func historyRestoreRequireHistoryLocked() bool       { return false }
func historyRestoreIdentityFromRead() bool           { return true }
func historyRestoreIdentityRestampOnRead() bool      { return true }
func historyRestoreEmptySnapHides() bool             { return false }
func historyRestoreFirstProtectedWithChildren() bool { return false }
func historyRestoreHoldUnresolvedOwner() bool        { return false }

func historyRestoreUnresolvedNeedsLockedStream() bool { return true }

func historyRestoreOwnOwnerUndecidedNeedsLockedStream() bool { return true }
func historyRestoreAnswerDetachReply() bool                  { return false }
func historyRestorePinConcernPage() bool                     { return false }
func historyRestoreUnresolvedToLocked() bool                 { return false }
func historyRestoreUnresolvedToOrdinary() bool               { return false }
func historyRestoreUnresolvedAwaitDecide() bool              { return false }
func historyRestoreUnresolvedDeadlineHides() bool            { return false }
func historyRestoreUnresolvedOrdinaryBypass() bool           { return false }
func historyRestoreConfirmFreshness() bool                   { return false }
func historyRestoreRejectDoneConfirm() bool                  { return false }
func historyRestoreRejectStaleInFlight() bool                { return false }

func historyRestoreStaleDeliveryHides() bool { return false }

func historyRestoreReissueOnlyIfAcceptable() bool { return false }
