package cdp

func holdResumeRegardlessEnabled() bool { return true }

func holdResumeUnheldEnabled() bool { return false }

func holdResumeTwiceEnabled() bool { return true }

func holdResumeRefusedEnabled() bool { return false }

func holdResumeChangesProtectedFateEnabled() bool { return false }

func holdResumeCapEnabled() bool { return true }

func holdResumeCapLimit() int { return maxRefused }
