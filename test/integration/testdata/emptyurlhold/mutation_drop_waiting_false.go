package cdp

func holdResumeRegardlessEnabled() bool { return false }

func holdResumeUnheldEnabled() bool { return false }

func holdResumeTwiceEnabled() bool { return false }

func holdResumeRefusedEnabled() bool { return false }

func holdResumeChangesProtectedFateEnabled() bool { return false }

func holdResumeCapEnabled() bool { return true }

func holdResumeCapLimit() int { return maxRefused }
