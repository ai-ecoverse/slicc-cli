package integration

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
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

func shardSelector(t *testing.T, node, spec string, env ...string) func(string) bool {
	t.Helper()
	cmd := exec.Command(node, "../../scripts/test-shard.mjs", spec, "--print")
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
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

func shardTimes(t *testing.T) map[string]map[string]float64 {
	t.Helper()
	raw, err := os.ReadFile("shard-times.json")
	if err != nil {
		t.Fatal(err)
	}
	var times map[string]map[string]float64
	if err := json.Unmarshal(raw, &times); err != nil {
		t.Fatalf("shard-times.json: %v", err)
	}
	return times
}

func TestShardTimesCoverEachCIPlatform(t *testing.T) {
	times := shardTimes(t)
	declared := map[string]bool{}
	for _, name := range declaredTests(t) {
		declared[name] = true
	}
	for _, platform := range []string{"linux"} {
		known := 0
		for name := range times[platform] {
			if declared[name] {
				known++
			}
		}
		if known < len(declared)/2 {
			t.Errorf("shard-times.json knows %d of %d tests on %s; refresh it with scripts/shard-times.mjs", known, len(declared), platform)
		}
	}
}

func TestTestShardsPartitionTheSuite(t *testing.T) {
	node := requireNode(t)
	declared := declaredTests(t)
	names := append(append([]string{}, declared...), "TestNotYetWritten")
	if len(names) < 100 {
		t.Fatalf("found only %d tests", len(names))
	}
	times := shardTimes(t)
	for _, platform := range []string{"linux", "nowhere"} {
		weight := map[string]float64{}
		var known []float64
		for _, name := range declared {
			if s, ok := times[platform][name]; ok {
				known = append(known, s)
			}
		}
		sort.Float64s(known)
		fallback := 1.0
		if len(known) > 0 {
			fallback = known[len(known)/2]
		}
		heaviest := 0.0
		for _, name := range declared {
			s, ok := times[platform][name]
			if !ok {
				s = fallback
			}
			weight[name] = s
			heaviest = max(heaviest, s)
		}
		for _, count := range []int{1, 2, 3} {
			loads := make([]float64, count)
			owner := map[string]int{}
			for i := 1; i <= count; i++ {
				picks := shardSelector(t, node, strconv.Itoa(i)+"/"+strconv.Itoa(count), "SLICC_TEST_SHARD_PLATFORM="+platform, "SLICC_GUARD_VECTORS=1")
				for _, name := range names {
					if !picks(name) {
						continue
					}
					if prev, dup := owner[name]; dup {
						t.Fatalf("%s: %s is in shards %d and %d of %d", platform, name, prev, i, count)
					}
					owner[name] = i
					loads[i-1] += weight[name]
				}
			}
			for _, name := range names {
				if owner[name] == 0 {
					t.Fatalf("%s: %s is in no shard of %d", platform, name, count)
				}
			}
			if owner["TestNotYetWritten"] != count {
				t.Fatalf("%s: a test the script does not know ran in shard %d of %d", platform, owner["TestNotYetWritten"], count)
			}
			lo, hi := slices.Min(loads), slices.Max(loads)
			if hi-lo > heaviest+0.001 {
				t.Fatalf("%s: unbalanced shards of %d: %v s, heaviest test %v s", platform, count, loads, heaviest)
			}
		}
	}
	for _, bad := range []string{"0/2", "3/2", "1/0", "two", "", "ci/x"} {
		err := exec.Command(node, "../../scripts/test-shard.mjs", bad, "--print").Run()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
			t.Fatalf("shard %q: %v", bad, err)
		}
	}
	for shard, want := range map[string]string{"0": "1/3", "2": "3/3"} {
		ci := shardSelector(t, node, "ci/3", "SLICC_TEST_SHARD="+shard, "SLICC_TEST_SHARD_PLATFORM=linux")
		plain := shardSelector(t, node, want, "SLICC_TEST_SHARD_PLATFORM=linux")
		for _, name := range names {
			if ci(name) != plain(name) {
				t.Fatalf("ci/3 on the pipeline's shard %s picks %s differently from %s", shard, name, want)
			}
		}
	}
	for _, bad := range []string{"3", "-1", "x"} {
		cmd := exec.Command(node, "../../scripts/test-shard.mjs", "ci/3", "--print")
		cmd.Env = append(os.Environ(), "SLICC_TEST_SHARD="+bad)
		if exit, ok := cmd.Run().(*exec.ExitError); !ok || exit.ExitCode() != 2 {
			t.Fatalf("ci/3 on the pipeline's shard %q: want exit 2", bad)
		}
	}
	cmd := exec.Command(node, "../../scripts/test-shard.mjs", "ci/3", "--print")
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "SLICC_TEST_SHARD=") })
	if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("ci/3 off the pipeline runs every test: %q, %v", out, err)
	}
}

func TestTestShardRunsGoTestOnItsShard(t *testing.T) {
	node := requireNode(t)
	script, err := filepath.Abs("../../scripts/test-shard.mjs")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := "package fixture\n\nimport (\n\t\"os\"\n\t\"testing\"\n\t\"time\"\n)\n\n" +
		"func TestAlpha(t *testing.T) {}\n\nfunc TestBravo(t *testing.T) {\n\tif os.Getenv(\"FIXTURE_FAIL\") != \"\" {\n\t\tt.Fatal(\"bravo failed\")\n\t}\n\tif f := os.Getenv(\"FIXTURE_FLAKY\"); f != \"\" {\n\t\tif _, err := os.Stat(f); err != nil {\n\t\t\t_ = os.WriteFile(f, nil, 0o644)\n\t\t\tt.Fatal(\"bravo flaked\")\n\t\t}\n\t}\n\tif os.Getenv(\"FIXTURE_STALL\") != \"\" {\n\t\ttime.Sleep(time.Minute)\n\t}\n}\n\n" +
		"func TestCharlie(t *testing.T) {\n\tt.Run(\"sub\", func(t *testing.T) {})\n}\n"
	for name, body := range map[string]string{"go.mod": "module fixture\n\ngo 1.26\n", "fixture_test.go": src} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runArgs := func(args []string, extra ...string) (string, int) {
		cmd := exec.Command(node, append([]string{script}, args...)...)
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
	run := func(spec string, extra ...string) (string, int) {
		return runArgs([]string{spec, "-count=1", "-v", "."}, extra...)
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
	report := filepath.Join(t.TempDir(), "0.xml")
	preload := filepath.Join(t.TempDir(), "shard.cjs")
	if err := os.WriteFile(preload, []byte("process.env.SLICC_PRELOADED = '1';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pipeline := []string{"SLICC_TEST_SHARD=1", "SLICC_TEST_JUNIT=" + report, "SLICC_TEST_FILES=", "SLICC_TEST_NODE_OPTIONS= --max-old-space-size=64 ", "NODE_OPTIONS=--require " + preload, "FIXTURE_FAIL=1"}
	piped, code := runArgs([]string{"ci/2", "-count=1", "-v", "."}, pipeline...)
	if got := strings.Join(ran(piped), " "); code != 1 || got != "TestBravo" {
		t.Fatalf("ci/2 on the pipeline's shard 1: exit %d, ran %q\n%s", code, got, piped)
	}
	xml, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("no JUnit report: %v\n%s", err, piped)
	}
	if !strings.Contains(string(xml), `<testcase name="TestBravo" classname="test/integration" file="test/integration" time="0"><failure message="TestBravo failed"/></testcase>`) || strings.Count(string(xml), "<testcase ") != 1 {
		t.Fatalf("the JUnit report of a failed shard:\n%s", xml)
	}
	envSrc := "package fixture\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestEnv(t *testing.T) {\n\tif os.Getenv(\"NODE_OPTIONS\") != \"--max-old-space-size=64\" || os.Getenv(\"SLICC_TEST_SHARD\") != \"\" || os.Getenv(\"SLICC_TEST_JUNIT\") != \"\" {\n\t\tt.Fatalf(\"NODE_OPTIONS=%q\", os.Getenv(\"NODE_OPTIONS\"))\n\t}\n}\n\nfunc TestSkipped(t *testing.T) { t.Skip(\"skipped\") }\n"
	envDir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module fixture\n\ngo 1.26\n", "env_test.go": envSrc} {
		if err := os.WriteFile(filepath.Join(envDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(node, script, "ci/1", "-count=1", "-v", ".")
	cmd.Dir = envDir
	cmd.Env = append(os.Environ(), "SLICC_TEST_SHARD_SUITE="+envDir, "SLICC_TEST_SHARD=0", "SLICC_TEST_JUNIT="+report, "SLICC_TEST_NODE_OPTIONS= --max-old-space-size=64 ", "NODE_OPTIONS=--require "+preload)
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "test-shard: PASS") {
		t.Fatalf("go test gets the pipeline's own NODE_OPTIONS back and no SLICC_TEST_ variables: %v\n%s", err, out)
	}
	if xml, _ := os.ReadFile(report); !strings.Contains(string(xml), `tests="2" failures="0" skipped="1"`) || !strings.Contains(string(xml), `<testcase name="TestSkipped" classname="test/integration" file="test/integration" time="0"><skipped/></testcase>`) {
		t.Fatalf("the JUnit report of a passing shard:\n%s", xml)
	}
	stalled, code := runArgs([]string{"1/1", "-count=1", "-timeout", "2s", "-v", "."}, "FIXTURE_STALL=1", "SLICC_TEST_SHARD_PLATFORM=fixture")
	if want := "test-shard: fixture 1/1 hit its -timeout 2s with TestBravo for "; code == 0 || !strings.Contains(stalled, want) || !strings.Contains(stalled, "the 1 finished tests took 0 s") || !strings.Contains(stalled, "the slowest finished: TestAlpha 0 s") {
		t.Fatalf("a shard that hit its -timeout: exit %d, want %q\n%s", code, want, stalled)
	}
	timed := filepath.Join(t.TempDir(), "times.json")
	if err := os.WriteFile(timed, []byte(`{"fixture": {"TestAlpha": 30, "TestBravo": 1, "TestCharlie": 2}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	weighted := []string{"SLICC_TEST_SHARD_TIMES=" + timed, "SLICC_TEST_SHARD_PLATFORM=fixture"}
	heavy, code := run("1/2", weighted...)
	if got := strings.Join(ran(heavy), " "); code != 0 || got != "TestAlpha" || !strings.Contains(heavy, "test-shard 1/2: 1 tests, about 30 s by the fixture times") {
		t.Fatalf("weighted shard 1/2: exit %d, ran %q\n%s", code, got, heavy)
	}
	light, code := run("2/2", weighted...)
	if got := strings.Join(ran(light), " "); code != 0 || got != "TestBravo TestCharlie TestCharlie/sub" {
		t.Fatalf("weighted shard 2/2: exit %d, ran %q\n%s", code, got, light)
	}
}

func TestShardTimesReadsGoTestLogs(t *testing.T) {
	node := requireNode(t)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	out := write("times.json", `{"win32": {"TestOld": 4}, "darwin": {"TestGone": 9, "TestDelta": 5}}`)
	one := write("one.log", "=== RUN   TestAlpha\n--- PASS: TestAlpha (12.34s)\n    --- PASS: TestAlpha/sub (12.00s)\n2026-10-15T06:41:39.2245670Z --- FAIL: TestBravo (0.71s)\r\n")
	two := write("two.log", "--- SKIP: TestAlpha (0.00s)\n--- SKIP: TestCharlie (0.00s)\n--- SKIP: TestDelta (0.00s)\nok  \tfixture\t13.1s\n")
	cmd := exec.Command(node, "../../scripts/shard-times.mjs", "--out", out, "darwin="+one, "darwin="+two)
	if b, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(b), "darwin: 4 tests, 18 s") {
		t.Fatalf("shard-times: %v\n%s", err, b)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"darwin\": {\n    \"TestAlpha\": 12.3,\n    \"TestBravo\": 0.7,\n    \"TestCharlie\": 0,\n    \"TestDelta\": 5\n  },\n  \"win32\": {\n    \"TestOld\": 4\n  }\n}\n"
	if string(raw) != want {
		t.Fatalf("shard-times wrote\n%s\nwant\n%s", raw, want)
	}
	empty := write("empty.log", "ok  \tfixture\t1.0s\n")
	for _, args := range [][]string{{"--out", out, "linux=" + empty}, {"--out", out}, {"--run", "1", "linux=" + one}, {"--bogus"}} {
		err := exec.Command(node, append([]string{"../../scripts/shard-times.mjs"}, args...)...).Run()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
			t.Fatalf("shard-times %v: %v", args, err)
		}
	}
}

func TestEachCIShardFitsItsTimeout(t *testing.T) {
	node := requireNode(t)
	declared := declaredTests(t)
	times := shardTimes(t)["linux"]
	shards, err := strconv.Atoi(ciInput(t, "shards"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := strconv.Atoi(ciInput(t, "timeout-minutes"))
	if err != nil {
		t.Fatal(err)
	}
	test := npmScript(t, "test")
	m := regexp.MustCompile(`^node scripts/test-shard\.mjs ci/([0-9]+) .* -timeout ([0-9]+)m `).FindStringSubmatch(test)
	if m == nil || m[1] != strconv.Itoa(shards) {
		t.Fatalf("npm test is %q; it splits the suite into ci.yml's %d shards with a -timeout in minutes", test, shards)
	}
	minutes, _ := strconv.Atoi(m[2])
	if job < minutes+5 {
		t.Errorf("timeout-minutes %d leaves less than 5 minutes past the -timeout %dm for setup", job, minutes)
	}
	var known []float64
	for _, name := range declared {
		if s, ok := times[name]; ok {
			known = append(known, s)
		}
	}
	sort.Float64s(known)
	for i := 1; i <= shards; i++ {
		spec := strconv.Itoa(i) + "/" + strconv.Itoa(shards)
		picks := shardSelector(t, node, spec, "SLICC_TEST_SHARD_PLATFORM=linux")
		load := 0.0
		for _, name := range declared {
			if !picks(name) {
				continue
			}
			if s, ok := times[name]; ok {
				load += s
			} else if len(known) > 0 {
				load += known[len(known)/2]
			}
		}
		t.Logf("shard %s: %.0f s of its -timeout %dm", spec, load, minutes)
		if budget := float64(minutes*60) * 3 / 4; load > budget {
			t.Errorf("shard %s: its tests took %.0f s by shard-times.json, more than three quarters of its -timeout %dm (%.0f s); add a shard", spec, load, minutes, budget)
		}
	}
}
