package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func declaredTests(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range files {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") && fn.Name.Name != "TestMain" {
				names = append(names, fn.Name.Name)
			}
		}
	}
	return names
}

func shardSelector(t *testing.T, node, spec string) func(string) bool {
	t.Helper()
	out, err := exec.Command(node, "../../scripts/test-shard.mjs", spec, "--print").Output()
	if err != nil {
		t.Fatalf("test-shard %s: %v", spec, err)
	}
	lines := strings.Fields(string(out))
	if len(lines) == 0 {
		return func(string) bool { return true }
	}
	if len(lines) != 2 {
		t.Fatalf("test-shard %s printed %q", spec, out)
	}
	re := regexp.MustCompile(lines[1])
	switch lines[0] {
	case "-run":
		return re.MatchString
	case "-skip":
		return func(name string) bool { return !re.MatchString(name) }
	}
	t.Fatalf("test-shard %s printed flag %q", spec, lines[0])
	return nil
}

func TestTestShardsPartitionTheSuite(t *testing.T) {
	node := requireNode(t)
	names := append(declaredTests(t), "TestNotYetWritten")
	if len(names) < 100 {
		t.Fatalf("found only %d tests", len(names))
	}
	for _, count := range []int{1, 2, 3} {
		sizes := make([]int, count)
		owner := map[string]int{}
		for i := 1; i <= count; i++ {
			picks := shardSelector(t, node, strconv.Itoa(i)+"/"+strconv.Itoa(count))
			for _, name := range names {
				if !picks(name) {
					continue
				}
				if prev, dup := owner[name]; dup {
					t.Fatalf("%s is in shards %d and %d of %d", name, prev, i, count)
				}
				owner[name] = i
				sizes[i-1]++
			}
		}
		for _, name := range names {
			if owner[name] == 0 {
				t.Fatalf("%s is in no shard of %d", name, count)
			}
		}
		if owner["TestNotYetWritten"] != count {
			t.Fatalf("a test the script does not know ran in shard %d of %d", owner["TestNotYetWritten"], count)
		}
		for _, n := range sizes {
			if n < sizes[0]-1 || n > sizes[0]+1 {
				t.Fatalf("unbalanced shards of %d: %v", count, sizes)
			}
		}
	}
	for _, bad := range []string{"0/2", "3/2", "1/0", "two", ""} {
		err := exec.Command(node, "../../scripts/test-shard.mjs", bad, "--print").Run()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
			t.Fatalf("shard %q: %v", bad, err)
		}
	}
}

func TestTestShardRunsGoTestOnItsShard(t *testing.T) {
	node := requireNode(t)
	script, err := filepath.Abs("../../scripts/test-shard.mjs")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := "package fixture\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n" +
		"func TestAlpha(t *testing.T) {}\n\nfunc TestBravo(t *testing.T) {\n\tif os.Getenv(\"FIXTURE_FAIL\") != \"\" {\n\t\tt.Fatal(\"bravo failed\")\n\t}\n}\n\n" +
		"func TestCharlie(t *testing.T) {\n\tt.Run(\"sub\", func(t *testing.T) {})\n}\n"
	for name, body := range map[string]string{"go.mod": "module fixture\n\ngo 1.26\n", "fixture_test.go": src} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(spec string, extra ...string) (string, int) {
		cmd := exec.Command(node, script, spec, "-count=1", "-v", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), append([]string{"SLICC_TEST_SHARD_SUITE=" + dir}, extra...)...)
		out, err := cmd.CombinedOutput()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	ran := func(out string) []string {
		var names []string
		for _, m := range regexp.MustCompile(`(?m)^=== RUN   (\S+)$`).FindAllStringSubmatch(out, -1) {
			names = append(names, m[1])
		}
		return names
	}
	first, code := run("1/2")
	if got := strings.Join(ran(first), " "); code != 0 || got != "TestAlpha TestCharlie TestCharlie/sub" {
		t.Fatalf("shard 1/2: exit %d, ran %q\n%s", code, got, first)
	}
	second, code := run("2/2")
	if got := strings.Join(ran(second), " "); code != 0 || got != "TestBravo" {
		t.Fatalf("shard 2/2: exit %d, ran %q\n%s", code, got, second)
	}
	failed, code := run("2/2", "FIXTURE_FAIL=1")
	if code != 1 || !strings.Contains(failed, "bravo failed") {
		t.Fatalf("a failing shard: exit %d\n%s", code, failed)
	}
}
