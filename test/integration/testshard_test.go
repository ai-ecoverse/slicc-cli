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
	for _, platform := range []string{"darwin", "linux", "win32"} {
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
	for _, platform := range []string{"darwin", "linux", "win32", "nowhere"} {
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
				picks := shardSelector(t, node, strconv.Itoa(i)+"/"+strconv.Itoa(count), "SLICC_TEST_SHARD_PLATFORM="+platform)
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
	marker := filepath.Join(t.TempDir(), "flaked")
	flaky, code := runArgs([]string{"1/1", "--retry", "1", "-count=1", "-v", "."}, "FIXTURE_FLAKY="+marker, "SLICC_TEST_SHARD_PLATFORM=fixture")
	if got := strings.Join(ran(flaky), " "); code != 0 || got != "TestAlpha TestBravo TestCharlie TestCharlie/sub TestBravo" || !strings.Contains(flaky, "test-shard 1/1: retrying TestBravo (retry 1 of 1)") || !strings.Contains(flaky, "test-shard: PASS fixture 1/1 ran 3 tests; TestBravo passed on retry") || !strings.Contains(flaky, "::warning title=flaky tests::fixture 1/1: TestBravo failed, then passed on retry") {
		t.Fatalf("a test that fails once and passes on its retry: exit %d, ran %q\n%s", code, got, flaky)
	}
	still, code := runArgs([]string{"2/2", "--retry", "1", "-count=1", "-v", "."}, "FIXTURE_FAIL=1", "SLICC_TEST_SHARD_PLATFORM=fixture")
	if got := strings.Join(ran(still), " "); code != 1 || got != "TestBravo TestBravo" || !strings.Contains(still, "TestBravo failed again on retry 1") || !strings.Contains(still, "test-shard: FAIL fixture 2/2") {
		t.Fatalf("a test that fails on its retry too: exit %d, ran %q\n%s", code, got, still)
	}
	if _, code := run("2/2", "FIXTURE_FLAKY="+filepath.Join(t.TempDir(), "once")); code != 1 {
		t.Fatalf("without --retry a flaky test fails the shard: exit %d", code)
	}
	retimed, code := runArgs([]string{"1/1", "--retry", "1", "-count=1", "-timeout", "2s", "-v", "."}, "FIXTURE_STALL=1", "SLICC_TEST_SHARD_PLATFORM=fixture")
	if code == 0 || strings.Contains(retimed, "retrying") {
		t.Fatalf("a shard that hit its -timeout is not retried: exit %d\n%s", code, retimed)
	}
	if _, code := runArgs([]string{"1/1", "--retry", "x", "-v", "."}); code != 2 {
		t.Fatalf("--retry x: exit %d, want 2", code)
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

var guardVectorTests = map[string]bool{"TestCDPProtectVectorsPrompt": true, "TestCDPProtectVectorsLagging": true, "TestCDPProtectVectorsIgnoreChromeUITargets": true}

func TestOnlyTheFullMatrixRetriesAFailedTest(t *testing.T) {
	job := workflowJobs(t, readWorkflow(t, "ci.yml"))["test"].body
	run := strings.TrimPrefix(lineWith(t, job, "sh scripts/verdict.sh test-shard"), "run: ")
	limit := strings.TrimPrefix(lineWith(t, job, "timeout-minutes:"), "timeout-minutes: ")
	for _, full := range []any{nil, true} {
		ctx := ghCtx{"github.event_name": "push", "inputs.full": full, "needs.runner.outputs.os": "all"}
		legs, _ := ciMatrix(t, ctx)
		for _, leg := range legs {
			ctx["matrix.os"], ctx["matrix.shard"] = leg.os, leg.shard
			cmd := " " + ghString(ghTemplate(t, run, ctx)) + " "
			if got := strings.Contains(cmd, " --retry 1 "); got != (full == true) {
				t.Errorf("%s %s with full %v retries failed tests: %v; only the release's full matrix retries", leg.os, leg.shard, full, got)
			}
			if full != true {
				continue
			}
			m := regexp.MustCompile(` -timeout ([0-9]+)m `).FindStringSubmatch(cmd)
			job, err := strconv.Atoi(ghString(ghTemplate(t, limit, ctx)))
			if m == nil || err != nil {
				t.Fatalf("%s %s: no -timeout or timeout-minutes", leg.os, leg.shard)
			}
			if minutes, _ := strconv.Atoi(m[1]); job < 2*minutes+5 {
				t.Errorf("%s %s: timeout-minutes %d has no room for a retry of its -timeout %dm", leg.os, leg.shard, job, minutes)
			}
		}
	}
}

func TestEachCIShardFitsItsTimeout(t *testing.T) {
	node := requireNode(t)
	declared := declaredTests(t)
	times := shardTimes(t)
	platforms := map[string]string{"ubuntu-latest": "linux", "macos-latest": "darwin", "windows-latest": "win32"}
	job := workflowJobs(t, readWorkflow(t, "ci.yml"))["test"].body
	run := strings.TrimPrefix(lineWith(t, job, "sh scripts/verdict.sh test-shard"), "run: ")
	limit := strings.TrimPrefix(lineWith(t, job, "timeout-minutes:"), "timeout-minutes: ")
	for _, full := range []any{nil, true} {
		ctx := ghCtx{"github.event_name": "push", "inputs.full": full, "needs.runner.outputs.os": "all"}
		legs, _ := ciMatrix(t, ctx)
		for _, leg := range legs {
			ctx["matrix.os"], ctx["matrix.shard"] = leg.os, leg.shard
			m := regexp.MustCompile(` -timeout ([0-9]+)m `).FindStringSubmatch(ghString(ghTemplate(t, run, ctx)))
			if m == nil {
				t.Fatalf("%s %s has no -timeout in minutes", leg.os, leg.shard)
			}
			minutes, _ := strconv.Atoi(m[1])
			if job, err := strconv.Atoi(ghString(ghTemplate(t, limit, ctx))); err != nil || job < minutes+5 {
				t.Errorf("%s %s: its job's timeout-minutes %s leaves less than 5 minutes past its -timeout %dm for setup", leg.os, leg.shard, ghString(ghTemplate(t, limit, ctx)), minutes)
			}
			platform := platforms[leg.os]
			var known []float64
			for _, name := range declared {
				if s, ok := times[platform][name]; ok {
					known = append(known, s)
				}
			}
			sort.Float64s(known)
			guards := "SLICC_GUARD_VECTORS=1"
			if !leg.guards {
				guards = "SLICC_GUARD_VECTORS=0"
			}
			picks := shardSelector(t, node, leg.shard, "SLICC_TEST_SHARD_PLATFORM="+platform, guards)
			load := 0.0
			for _, name := range declared {
				if !picks(name) || (!leg.guards && guardVectorTests[name]) {
					continue
				}
				if s, ok := times[platform][name]; ok {
					load += s
				} else if len(known) > 0 {
					load += known[len(known)/2]
				}
			}
			t.Logf("%s %s: %.0f s of its -timeout %dm", leg.os, leg.shard, load, minutes)
			if budget := float64(minutes*60) * 3 / 4; load > budget {
				t.Errorf("%s %s: its tests took %.0f s by shard-times.json, more than three quarters of its -timeout %dm (%.0f s); add a shard", leg.os, leg.shard, load, minutes, budget)
			}
		}
	}
}
