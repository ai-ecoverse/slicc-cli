package cdp

func historyRestoreConfirmAtNavCommit() bool         { return true }
func historyRestoreDetachOldChildren() bool          { return true }
func historyRestoreHoldChildren() bool               { return true }
func historyRestoreHoldWorkers() bool                { return true }
func historyRestoreHoldExplicitAttach() bool         { return true }
func historyRestoreIdentityCompare() bool            { return true }
func historyRestoreDeadlineHides() bool              { return true }
func historyRestoreWaitForConfirm() bool             { return true }
func historyRestoreRequireHistoryLocked() bool       { return true }
func historyRestoreIdentityFromRead() bool           { return true }
func historyRestoreIdentityRestampOnRead() bool      { return true }
func historyRestoreEmptySnapHides() bool             { return true }
func historyRestoreFirstProtectedWithChildren() bool { return true }
func historyRestoreHoldUnresolvedOwner() bool        { return true }

func historyRestoreUnresolvedNeedsLockedStream() bool { return true }

func historyRestoreOwnOwnerUndecidedNeedsLockedStream() bool { return true }
func historyRestoreAnswerDetachReply() bool                  { return true }
func historyRestorePinConcernPage() bool                     { return false }
func historyRestoreUnresolvedToLocked() bool                 { return true }
func historyRestoreUnresolvedToOrdinary() bool               { return true }
func historyRestoreUnresolvedAwaitDecide() bool              { return true }
func historyRestoreUnresolvedDeadlineHides() bool            { return true }
func historyRestoreUnresolvedOrdinaryBypass() bool           { return false }
func historyRestoreConfirmFreshness() bool                   { return true }
func historyRestoreRejectDoneConfirm() bool                  { return true }
func historyRestoreRejectStaleInFlight() bool                { return true }

func historyRestoreStaleDeliveryHides() bool { return true }

func historyRestoreReissueOnlyIfAcceptable() bool { return false }
