package integration

import "testing"

func TestCDPProtectVectorsLagging(t *testing.T) {
	runGuardVectors(t, "lagging")
}
