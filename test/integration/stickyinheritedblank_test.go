package integration

import "testing"

func TestCDPStickyInheritedBlankBoundaryMeasurements(t *testing.T) {
	runStickyBoundaryMeasurements(t, "inherited-blank")
}
