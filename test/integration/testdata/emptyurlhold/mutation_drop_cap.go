package cdp

func holdResumeRegardlessEnabled() bool { return true }

func holdResumeUnheldEnabled() bool { return false }

func holdResumeTwiceEnabled() bool { return false }

func holdResumeRefusedEnabled() bool { return false }

func holdResumeChangesProtectedFateEnabled() bool { return false }

func holdResumeCapEnabled() bool { return false }

func holdResumeCapLimit() int { return maxRefused }
