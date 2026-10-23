package integration

import "testing"

func TestCDPStickyErrorBoundaryMeasurements(t *testing.T) {
	runStickyBoundaryMeasurements(t, "error")
}
