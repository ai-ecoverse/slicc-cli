package integration

import "testing"

func TestCDPProtectVectorsPrompt(t *testing.T) {
	runGuardVectors(t, "prompt")
}
