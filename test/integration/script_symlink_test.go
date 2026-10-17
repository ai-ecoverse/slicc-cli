package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func scriptRoutes(t *testing.T, script string) map[string]string {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	linkedRepo := filepath.Join(dir, "repo")
	symlinkOrSkip(t, repo, linkedRepo)
	linkedScript := filepath.Join(dir, "bin", filepath.Base(script))
	symlinkOrSkip(t, filepath.Join(repo, "scripts", script), linkedScript)
	return map[string]string{
		"real path":        filepath.Join(repo, "scripts", script),
		"symlinked repo":   filepath.Join(linkedRepo, "scripts", script),
		"symlinked script": linkedScript,
	}
}

func runScript(t *testing.T, node, dir, path string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(node, append([]string{path}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

func TestScriptsRunThroughSymlinks(t *testing.T) {
	node := requireNode(t)
	t.Run("go-major", func(t *testing.T) {
		for route, path := range scriptRoutes(t, "go-major.mjs") {
			_, stderr, code := runScript(t, node, "", path, nil, "2.0.0")
			if code != 1 || !strings.Contains(stderr, "change the module path to github.com/ai-ecoverse/slicc-cli/v2 first") {
				t.Errorf("%s: go-major let 2.0.0 through: exit %d, %q", route, code, stderr)
			}
			stdout, _, code := runScript(t, node, "", path, nil, "1.4.0")
			if code != 0 || !strings.Contains(stdout, "fits module path") {
				t.Errorf("%s: go-major on 1.4.0: exit %d, %q", route, code, stdout)
			}
		}
	})
	t.Run("test-shard", func(t *testing.T) {
		for route, path := range scriptRoutes(t, "test-shard.mjs") {
			stdout, stderr, code := runScript(t, node, "", path, nil, "2/2", "--print")
			if lines := strings.Fields(stdout); code != 0 || len(lines) != 2 || lines[0] != "-skip" || !strings.Contains(lines[1], "Test") {
				t.Errorf("%s: test-shard 2/2 --print: exit %d, %q %q", route, code, stdout, stderr)
			}
		}
	})
	t.Run("shard-times", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "linux.log")
		if err := os.WriteFile(log, []byte("--- PASS: TestAlpha (2.50s)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for route, path := range scriptRoutes(t, "shard-times.mjs") {
			out := filepath.Join(t.TempDir(), "times.json")
			stdout, stderr, code := runScript(t, node, "", path, nil, "--out", out, "linux="+log)
			raw, err := os.ReadFile(out)
			if code != 0 || !strings.Contains(stdout, "linux: 1 tests, 3 s") || err != nil || !strings.Contains(string(raw), `"TestAlpha": 2.5`) {
				t.Errorf("%s: shard-times: exit %d, %q %q, wrote %q (%v)", route, code, stdout, stderr, raw, err)
			}
		}
	})
}

func TestTestShardRefusesAnEmptyShard(t *testing.T) {
	node := requireNode(t)
	script, err := filepath.Abs("../../scripts/test-shard.mjs")
	if err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	two := t.TempDir()
	src := "package fixture\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) {}\n\nfunc TestBravo(t *testing.T) {}\n"
	for path, body := range map[string]string{
		filepath.Join(empty, "go.mod"):        "module fixture\n\ngo 1.26\n",
		filepath.Join(two, "go.mod"):          "module fixture\n\ngo 1.26\n",
		filepath.Join(two, "fixture_test.go"): src,
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		suite string
		args  []string
	}{
		{empty, []string{"1/1", "--print"}},
		{empty, []string{"1/1", "-count=1", "."}},
		{empty, []string{"2/2", "--print"}},
		{two, []string{"3/3", "--print"}},
		{two, []string{"3/3", "-count=1", "."}},
	}
	for _, c := range cases {
		stdout, stderr, code := runScript(t, node, c.suite, script, []string{"SLICC_TEST_SHARD_SUITE=" + c.suite}, c.args...)
		if code != 1 || !strings.Contains(stderr, "no tests in") || stdout != "" {
			t.Errorf("an empty shard %v: exit %d, %q %q", c.args, code, stdout, stderr)
		}
	}
	for _, spec := range []string{"1/3", "2/3"} {
		stdout, _, code := runScript(t, node, two, script, []string{"SLICC_TEST_SHARD_SUITE=" + two}, spec, "--print")
		if code != 0 || !strings.HasPrefix(stdout, "-run\n") {
			t.Errorf("shard %s of two tests: exit %d, %q", spec, code, stdout)
		}
	}
	stdout, stderr, code := runScript(t, node, two, script, []string{"SLICC_TEST_SHARD_SUITE=" + two}, "1/1", "-count=1", "-v", ".")
	if code != 0 || !strings.Contains(stdout, "test-shard: PASS ") || !strings.HasSuffix(stdout, " 1/1 ran 2 tests\n") {
		t.Errorf("a shard of two tests: exit %d, %q %q", code, stdout, stderr)
	}
	stdout, stderr, code = runScript(t, node, two, script, []string{"SLICC_TEST_SHARD_SUITE=" + two}, "1/1", "-count=1", "-v", "-skip", ".", ".")
	if code != 1 || !strings.Contains(stderr, "go test ran no tests of the 2 selected") || strings.Contains(stdout, "PASS fixture") || strings.Contains(stdout, "test-shard: PASS") {
		t.Errorf("a shard whose go test runs nothing: exit %d, %q %q", code, stdout, stderr)
	}
}
