package integration

func guardVerdictRefusesForged() bool { return true }

func guardNotRunRefusesForged() bool { return false }

func guardNotRunRefusesUnfinished() bool { return true }

func guardNotRunRefusesBadLegs() bool { return true }

func guardNotRunRefusesUnexpected() bool { return true }

func guardNotRunRefusesVanished() bool { return true }
