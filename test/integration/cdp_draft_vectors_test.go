package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
)

func TestCDPDraftVectorPinsNameTheSameBytes(t *testing.T) {
	pkg := guardPackage(t)
	content, err := os.ReadFile(filepath.Join(pkg, "guard-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, sliccbrowsers.GuardVectorsJSON()) {
		t.Fatal("npm and Go dependencies have different guard vector bytes")
	}
}
