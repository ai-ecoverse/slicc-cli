package integration

import "testing"

func TestCDPStickyHistoryBoundaryMeasurements(t *testing.T) {
	runStickyBoundaryMeasurements(t, "history")
}
