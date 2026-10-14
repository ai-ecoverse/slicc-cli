package integration

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseSignsOnlyDarwin(t *testing.T) {
	for _, file := range []string{"../../.github/workflows/release.yml", "../../scripts/package.sh", "../../README.md"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		data := strings.ToLower(string(raw))
		for _, banned := range []string{"azure", "trusted_signing", "jsign", "signtool", "setup-java"} {
			if strings.Contains(data, banned) {
				t.Errorf("%s mentions %q", file, banned)
			}
		}
	}
	raw, err := os.ReadFile("../../scripts/package.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "codesign --force --timestamp --options runtime") || !strings.Contains(string(raw), "xcrun notarytool submit") {
		t.Fatal("package.sh no longer signs and notarizes the darwin binaries")
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "Windows binaries are unsigned; SmartScreen may warn on first run.") {
		t.Fatal("README lacks the unsigned Windows note")
	}
}
