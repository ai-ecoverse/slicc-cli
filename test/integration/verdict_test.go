package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

type gate struct {
	name, script string
}

var gates = []gate{
	{"test-shard", "scripts/test-shard.mjs"},
	{"go-major", "scripts/go-major.mjs"},
	{"release-plan", "scripts/release-plan.mjs"},
	{"npm-publish", "scripts/npm-publish.mjs"},
	{"sliccy-next", "scripts/sliccy-next.mjs"},
	{"install-check", "test/integration/testdata/install/check.sh"},
	{"release-issue", "scripts/release-issue.mjs"},
}

type gateCall struct {
	file, gate, line string
}

func gateCalls(t *testing.T) []gateCall {
	t.Helper()
	var calls []gateCall
	add := func(file, text string) {
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\\\n", " ")
		for _, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- scripts/") || strings.HasPrefix(trimmed, "run: shellcheck ") {
				continue
			}
			for _, g := range gates {
				if strings.Contains(line, g.script) {
					calls = append(calls, gateCall{file, g.name, trimmed})
				}
			}
		}
	}
	for _, name := range []string{"ci.yml", "release.yml"} {
		raw, err := os.ReadFile("../../.github/workflows/" + name)
		if err != nil {
			t.Fatal(err)
		}
		add(name, string(raw))
	}
	raw, err := os.ReadFile("../../.releaserc.json")
	if err != nil {
		t.Fatal(err)
	}
	var rc struct {
		Plugins []any `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &rc); err != nil {
		t.Fatal(err)
	}
	for _, p := range rc.Plugins {
		if list, ok := p.([]any); ok && len(list) == 2 {
			if opts, ok := list[1].(map[string]any); ok {
				for _, key := range []string{"verifyReleaseCmd", "publishCmd"} {
					if cmd, ok := opts[key].(string); ok {
						for _, part := range strings.Split(cmd, "&&") {
							add(".releaserc.json", part)
						}
					}
				}
			}
		}
	}
	return calls
}

func requireSh(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("no sh on this Windows PATH")
		}
		t.Fatal(err)
	}
	return sh
}

func TestEveryGateCallerRequiresItsVerdict(t *testing.T) {
	calls := gateCalls(t)
	seen := map[string]int{}
	for _, c := range calls {
		seen[c.gate]++
		if !strings.Contains(c.line, "sh scripts/verdict.sh "+c.gate+" ") {
			t.Errorf("%s runs %s without requiring its %q verdict: %s", c.file, c.gate, c.gate+": PASS", c.line)
		}
	}
	for _, g := range gates {
		if seen[g.name] == 0 {
			t.Errorf("no workflow runs the %s gate", g.name)
		}
	}
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	release := strings.ReplaceAll(string(raw), "\r\n", "\n")
	fresh := workflowJobs(t, release)["fresh"].body
	for _, want := range []string{`echo "verdict=current" >> "$GITHUB_OUTPUT"`, `echo "verdict=stale" >> "$GITHUB_OUTPUT"`, "verdict: ${{ steps.head.outputs.verdict }}"} {
		if !strings.Contains(fresh, want) {
			t.Errorf("the fresh job lacks %q", want)
		}
	}
	if cond := workflowJobs(t, release)["build"].cond; !strings.Contains(cond, "(github.event_name != 'workflow_run' || needs.fresh.outputs.verdict == 'current' || needs.fresh.outputs.verdict == 'unknown')") {
		t.Errorf("a release after CI does not require the fresh job's verdict: %s", cond)
	}
}

func TestAGateThatExitsSilentlyFailsItsStep(t *testing.T) {
	sh := requireSh(t)
	requireNode(t)
	verdict, err := os.ReadFile("../../scripts/verdict.sh")
	if err != nil {
		t.Fatal(err)
	}
	expr := regexp.MustCompile(`\$\{\{[^}]*\}\}`)
	stubs := map[string]string{"silent": "", "fails": "echo \"%s: FAIL broken\"\nexit 3\n", "other": "echo \"other: PASS\"\n", "passes": "echo \"%s: PASS fine\"\n"}
	run := 0
	for _, c := range gateCalls(t) {
		at := strings.Index(c.line, "sh scripts/verdict.sh ")
		if at < 0 || strings.Contains(c.line, "docker run") {
			continue
		}
		cmd := expr.ReplaceAllString(c.line[at:], "x")
		cmd = strings.ReplaceAll(cmd, "${nextRelease.version}", "1.4.0")
		var script string
		for _, g := range gates {
			if g.name == c.gate {
				script = g.script
			}
		}
		for kind, body := range stubs {
			dir := t.TempDir()
			for path, data := range map[string]string{"scripts/verdict.sh": string(verdict), script: stub(script, strings.ReplaceAll(body, "%s", c.gate))} {
				if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(path)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, path), []byte(data), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			step := exec.Command(sh, "-c", cmd)
			step.Dir = dir
			out, err := step.CombinedOutput()
			code := 0
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			want := map[string]int{"silent": 1, "fails": 3, "other": 1, "passes": 0}[kind]
			if code != want || (kind == "silent" && !strings.Contains(string(out), "silence is not a pass")) {
				t.Errorf("%s, %s exits %s: step exit %d, want %d: %s\n%s", c.file, c.gate, kind, code, want, cmd, out)
			}
		}
		run++
	}
	if run < len(gates) {
		t.Fatalf("ran only %d gate steps", run)
	}
}

func stub(script, body string) string {
	if strings.HasSuffix(script, ".sh") {
		return body
	}
	var js strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		switch {
		case strings.HasPrefix(line, "echo "):
			js.WriteString("console.log(" + strings.TrimPrefix(line, "echo ") + ");\n")
		case strings.HasPrefix(line, "exit "):
			js.WriteString("process.exit(" + strings.TrimPrefix(line, "exit ") + ");\n")
		}
	}
	return js.String()
}

func TestVerdictPassesTheRealGates(t *testing.T) {
	sh := requireSh(t)
	node := requireNode(t)
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"go-major", node, "scripts/go-major.mjs", "1.4.0"}, 0, "go-major: PASS 1.4.0 (major 1) fits module path github.com/ai-ecoverse/slicc-cli"},
		{[]string{"go-major", node, "scripts/go-major.mjs", "2.0.0"}, 1, "go-major: FAIL release 2.0.0 is major 2"},
		{[]string{"test-shard", node, "scripts/test-shard.mjs", "1/1", "--print"}, 1, "verdict: test-shard exited 0 without a \"test-shard: PASS\" line"},
		{[]string{"go-major", node, "-e", ""}, 1, "silence is not a pass"},
		{[]string{"go-major"}, 2, "usage: verdict.sh"},
	}
	for _, c := range cases {
		cmd := exec.Command(sh, append([]string{"scripts/verdict.sh"}, c.args...)...)
		cmd.Dir = "../.."
		out, err := cmd.CombinedOutput()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != c.code || !strings.Contains(string(out), c.want) {
			t.Errorf("verdict.sh %v: exit %d, want %d and %q:\n%s", c.args, code, c.code, c.want, out)
		}
	}
}

func TestFreshPrintsAVerdictForEveryAnswer(t *testing.T) {
	sh := requireSh(t)
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	body := workflowJobs(t, strings.ReplaceAll(string(raw), "\r\n", "\n"))["fresh"].body
	at := strings.Index(body, "        run: |\n")
	if at < 0 {
		t.Fatalf("the fresh job has no run block:\n%s", body)
	}
	var script strings.Builder
	for _, line := range strings.Split(body[at+len("        run: |\n"):], "\n") {
		if !strings.HasPrefix(line, "          ") && strings.TrimSpace(line) != "" {
			break
		}
		script.WriteString(strings.TrimPrefix(line, "          ") + "\n")
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=fake", "-c", "user.email=fake@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "one")
	first := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "two")
	head := git("rev-parse", "HEAD")
	cases := []struct {
		repo, sha, verdict, line string
	}{
		{repo, head, "current", "fresh: PASS current: main is at " + head},
		{repo, first, "stale", "fresh: PASS stale: main is at " + head + ", past " + first},
		{filepath.Join(t.TempDir(), "missing"), head, "unknown", "fresh: PASS unknown (semantic-release decides)"},
	}
	for _, c := range cases {
		output := filepath.Join(t.TempDir(), "output")
		cmd := exec.Command(sh, "-c", script.String())
		cmd.Env = append(os.Environ(), "REPO="+c.repo, "SHA="+c.sha, "GITHUB_OUTPUT="+output)
		out, err := cmd.CombinedOutput()
		got, _ := os.ReadFile(output)
		if err != nil || !strings.Contains(string(out), c.line) || string(got) != "verdict="+c.verdict+"\n" {
			t.Errorf("fresh with %s at %s: %v, wrote %q:\n%s", c.repo, c.sha, err, got, out)
		}
	}
}
