package cdp

func holdResumeRegardlessEnabled() bool { return true }

func holdResumeUnheldEnabled() bool { return false }

func holdResumeTwiceEnabled() bool { return false }

func holdResumeRefusedEnabled() bool { return true }

func holdResumeChangesProtectedFateEnabled() bool { return false }

func holdResumeCapEnabled() bool { return true }

func holdResumeCapLimit() int { return maxRefused }
