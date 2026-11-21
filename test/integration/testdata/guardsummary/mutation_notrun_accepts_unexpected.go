package integration

func guardVerdictRefusesForged() bool { return true }

func guardNotRunRefusesForged() bool { return true }

func guardNotRunRefusesUnfinished() bool { return true }

func guardNotRunRefusesBadLegs() bool { return true }

func guardNotRunRefusesUnexpected() bool { return false }

func guardNotRunRefusesVanished() bool { return true }
