package integration

func guardVerdictRefusesForged() bool { return true }

func guardNotRunRefusesForged() bool { return true }

func guardNotRunRefusesUnfinished() bool { return false }

func guardNotRunRefusesBadLegs() bool { return true }

func guardNotRunRefusesUnexpected() bool { return true }

func guardNotRunRefusesVanished() bool { return true }
