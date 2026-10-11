package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func goMajor(t *testing.T, node, gomod, version string) (string, int) {
	t.Helper()
	args := []string{"../../scripts/go-major.mjs"}
	if gomod != "" {
		path := filepath.Join(t.TempDir(), "go.mod")
		if err := os.WriteFile(path, []byte(gomod), 0o644); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--go-mod", path)
	}
	out, err := exec.Command(node, append(args, version)...).CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func TestGoMajorGuardsTheModulePath(t *testing.T) {
	node := requireNode(t)
	v1 := "module github.com/ai-ecoverse/slicc-cli\n\ngo 1.26.5\n"
	v2 := "module github.com/ai-ecoverse/slicc-cli/v2\n\ngo 1.26.5\n"
	cases := []struct {
		gomod, version string
		code           int
		want           string
	}{
		{"", "1.26.0", 0, "fits module path github.com/ai-ecoverse/slicc-cli"},
		{"", "2.0.0", 1, "change the module path to github.com/ai-ecoverse/slicc-cli/v2 first"},
		{v1, "0.3.1", 0, "major 0"},
		{v1, "1.0.0", 0, "major 1"},
		{v1, "0.0.0-dry-run.7", 0, "major 0"},
		{v1, "2.0.0", 1, "only allows v0 or v1"},
		{v1, "10.1.0", 1, "slicc-cli/v10 first"},
		{v2, "2.4.0", 0, "fits module path github.com/ai-ecoverse/slicc-cli/v2"},
		{v2, "3.0.0", 1, "only allows v2; change the module path to github.com/ai-ecoverse/slicc-cli/v3 first"},
		{v2, "1.9.0", 1, "needs v2"},
		{v1, "1.2", 1, "not a semantic version"},
		{"module github.com/ai-ecoverse/slicc-cli/v1\n", "1.0.0", 1, "invalid major suffix"},
		{"go 1.26.5\n", "1.0.0", 1, "no module line"},
	}
	for _, c := range cases {
		out, code := goMajor(t, node, c.gomod, c.version)
		if code != c.code || !strings.Contains(out, c.want) {
			t.Fatalf("%q with %q: exit %d, %s", c.version, c.gomod, code, out)
		}
	}
}

func TestReleaseVerifiesTheGoMajor(t *testing.T) {
	data, err := os.ReadFile("../../.releaserc.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "node scripts/go-major.mjs '${nextRelease.version}'") {
		t.Fatalf(".releaserc.json does not run the major guard:\n%s", data)
	}
}
