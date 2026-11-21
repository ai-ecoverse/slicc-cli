package integration

func guardVerdictRefusesForged() bool { return true }

func guardNotRunRefusesForged() bool { return true }

func guardNotRunRefusesUnfinished() bool { return true }

func guardNotRunRefusesBadLegs() bool { return false }

func guardNotRunRefusesUnexpected() bool { return true }

func guardNotRunRefusesVanished() bool { return true }
