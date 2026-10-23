package integration

import "testing"

func TestCDPStickyBFCacheBoundaryMeasurements(t *testing.T) {
	runStickyBoundaryMeasurements(t, "bfcache")
}
