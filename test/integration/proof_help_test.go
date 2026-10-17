package integration

import (
	"strings"
	"testing"
)

func TestHelpSaysVersionExits(t *testing.T) {
	out, _, _ := slicc(t, "--help")
	if !strings.Contains(out, "print the version and exit") {
		t.Fatalf("--help:\n%s", out)
	}
}
