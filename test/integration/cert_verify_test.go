package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeTool(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

type darwinBinary struct {
	Asset   string `json:"asset"`
	Status  string `json:"status"`
	Why     string `json:"why"`
	Answers []struct {
		Output string `json:"output"`
	} `json:"answers"`
}

func darwinCheck(t *testing.T, codesign, spctl string) []darwinBinary {
	t.Helper()
	node := requireNode(t)
	work := t.TempDir()
	bin := filepath.Join(work, "bin")
	assets := filepath.Join(work, "assets")
	for _, d := range []string{bin, assets} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []string{"slicc-darwin-arm64", "slicc-darwin-amd64"} {
		if err := os.WriteFile(filepath.Join(assets, a), []byte("fake-binary-"+a), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeTool(t, bin, "codesign", codesign)
	fakeTool(t, bin, "spctl", spctl)
	out := filepath.Join(work, "evidence", "darwin.json")
	cmd := exec.Command(node, certTools+"darwin.mjs", "--dir", assets, "--out", out, "--team", "FAKETEAM01", "--backoff", "0")
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "COUNT="+filepath.Join(work, "count"))
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("darwin.mjs: %v\n%s", err, msg)
	}
	var got struct {
		Binaries []darwinBinary `json:"binaries"`
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Binaries) != 2 {
		t.Fatalf("want both darwin binaries: %s", data)
	}
	return got.Binaries
}

func TestCertVerifyPassesADarwinBinaryOnlyOnANotarizedAnswer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fakes are shell scripts; the darwin check runs on macOS")
	}
	good := `case "$1" in
-dv) printf 'Identifier=ai.sliccy.slicc\nCodeDirectory v=20500 flags=0x10000(runtime)\nAuthority=Developer ID Application: Fake (FAKETEAM01)\nTeamIdentifier=FAKETEAM01\n' >&2 ;;
esac
exit 0
`
	notarized := `echo "$3: accepted" >&2; echo "source=Notarized Developer ID" >&2; exit 0
`
	unnotarized := `echo "$4: rejected" >&2; echo "source=Unnotarized Developer ID" >&2; exit 3
`
	second := `n=$(cat "$COUNT" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$COUNT"
if [ $((n % 2)) -eq 1 ]; then echo "rejected" >&2; echo "source=Unnotarized Developer ID" >&2; exit 3; fi
echo "accepted" >&2; echo "source=Notarized Developer ID" >&2; exit 0
`
	otherTeam := strings.ReplaceAll(good, "FAKETEAM01", "OTHERTEAM9")
	cases := []struct {
		name, codesign, spctl, status, why string
		answers                            int
	}{
		{"signed and notarized", good, notarized, "PASS", "", 1},
		{"Gatekeeper says Unnotarized first, then Notarized", good, second, "PASS", "", 2},
		{"never notarized", good, unnotarized, "FAIL", "never said Notarized Developer ID in 3 tries: Unnotarized Developer ID / Unnotarized Developer ID / Unnotarized Developer ID", 3},
		{"another team", otherTeam, notarized, "FAIL", "its team isn't FAKETEAM01", 1},
		{"another team, and spctl killed", otherTeam, "kill -9 $$\n", "FAIL", "its team isn't FAKETEAM01", 3},
		{"codesign killed", "kill -9 $$\n", notarized, "INCOMPLETE", "codesign did not finish", 1},
		{"spctl killed", good, "kill -9 $$\n", "INCOMPLETE", "never said Notarized", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, b := range darwinCheck(t, c.codesign, c.spctl) {
				if b.Status != c.status || !strings.Contains(b.Why, c.why) || len(b.Answers) != c.answers {
					t.Fatalf("%s: %s (%s) after %d answers, want %s with %q after %d", b.Asset, b.Status, b.Why, len(b.Answers), c.status, c.why, c.answers)
				}
			}
		})
	}
}

func TestCertVerifyReproducesEverythingButTheSignedDarwinBinaries(t *testing.T) {
	dir := t.TempDir()
	line := func(sha, name string) string { return fmt.Sprintf("%s  %s\n", strings.Repeat(sha, 64), name) }
	release := line("a", "slicc-darwin-arm64") + line("b", "slicc-linux-amd64") + line("c", "slicc-wasip1-wasm.wasm") + line("d", "install.sh")
	rebuilt := line("e", "slicc-darwin-arm64") + line("b", "slicc-linux-amd64") + line("c", "slicc-wasip1-wasm.wasm") + line("d", "install.sh")
	broken := line("e", "slicc-darwin-arm64") + line("f", "slicc-linux-amd64") + line("c", "slicc-wasip1-wasm.wasm") + line("d", "install.sh")
	for name, body := range map[string]string{"release": release, "rebuilt": rebuilt, "broken": broken} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	check := func(rebuilt string) map[string]any {
		out := filepath.Join(dir, "rebuild.json")
		r := certTool(t, "", "outputs.mjs", "reproduce", "--release", filepath.Join(dir, "release"), "--tree", dir, "--rebuilt", filepath.Join(dir, rebuilt), "--out", out)
		if r.code != 0 {
			t.Fatalf("reproduce:\n%s", r.out)
		}
		var got map[string]any
		data, _ := os.ReadFile(out)
		_ = json.Unmarshal(data, &got)
		return got
	}
	if got := check("rebuilt"); got["match"] != true || got["files"] != float64(3) || fmt.Sprint(got["signed"]) != "[slicc-darwin-arm64]" {
		t.Fatalf("only the signed darwin binary may differ: %v", got)
	}
	if got := check("broken"); got["match"] != false || fmt.Sprint(got["differ"]) != "[slicc-linux-amd64]" {
		t.Fatalf("a linux binary that differs is not reproducible: %v", got)
	}
}

func TestCertVerifyRunsCodeOnlyAfterTheReleaseOutsideItsConcurrency(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/cert-verify.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.Contains(text, "  workflow_run:\n    workflows: [Release]\n    types: [completed]\n    branches: [main]\n") || !strings.Contains(text, "  group: cert-verify-${{ github.event.workflow_run.head_sha || inputs.version }}\n") {
		t.Fatal("cert-verify follows each Release run in a concurrency group of its own, never release-main")
	}
	jobs := jobsOf(t, text)
	runners := map[string]string{"tag": "${{ vars.CERT_LIGHT_RUNNER || 'ubuntu-latest' }}", "rebuild": "${{ vars.CERT_RUNNER || 'ubuntu-latest' }}", "darwin": "macos-latest", "verify": "${{ vars.CERT_LIGHT_RUNNER || 'ubuntu-latest' }}", "notes": "ubuntu-latest"}
	if len(jobs) != len(runners) {
		t.Fatalf("jobs %v", jobs)
	}
	for name, runner := range runners {
		body := jobs[name]
		if !strings.Contains(body, "    runs-on: "+runner+"\n") {
			t.Errorf("%s must run on %s", name, runner)
		}
		if strings.Contains(body, "id-token") || strings.Contains(body, "secrets.") || strings.Contains(body, "claude") {
			t.Errorf("%s is code only, with no id-token and no secret", name)
		}
		if strings.Contains(body, "contents: write") != (name == "notes") {
			t.Errorf("%s: only the notes job writes, on a hosted runner", name)
		}
		if name != "tag" && !strings.Contains(body, "needs.tag.outputs.version != ''") {
			t.Errorf("%s runs only for a release", name)
		}
	}
	_, cond, _ := strings.Cut(jobs["tag"], "    if: >-\n")
	cond, _, _ = strings.Cut(cond, "\n    runs-on:")
	want := "${{ github.ref == 'refs/heads/main' && (github.event_name == 'workflow_dispatch' || github.event.workflow_run.head_repository.full_name == github.repository && (github.event.workflow_run.event == 'workflow_run' || github.event.workflow_run.event == 'workflow_dispatch')) }}"
	if strings.Join(strings.Fields(cond), " ") != want {
		t.Fatalf("which release follows every completed Release run on main of this repository, whatever its conclusion:\n%s", cond)
	}
	if strings.Contains(jobs["rebuild"], "GH_TOKEN") || strings.Contains(jobs["rebuild"], "gh ") {
		t.Fatal("the pool runner that rebuilds gets no token")
	}
	if !strings.Contains(jobs["verify"], "    needs: [tag, rebuild, darwin]\n") || !strings.Contains(jobs["notes"], "needs.verify.result == 'success'") || !strings.Contains(jobs["notes"], "tools/cert-review/notes.mjs") || strings.Contains(jobs["verify"], "notes.mjs") {
		t.Fatal("verify reads the rebuild and darwin evidence, read-only; only the notes job edits, after it")
	}
	if !strings.Contains(jobs["verify"], "    name: release / verify on npm\n") {
		t.Fatal("the job is release / verify on npm")
	}
	release, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(release), "cert-review") {
		t.Fatal("release.yml stays plain semantic-release")
	}
	wf, err := os.ReadFile("../../.github/workflows/cert-review-pr.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ReplaceAll(string(wf), "\r\n", "\n"), "\nenv:\n  CERT_CONTEXT: cert-review\n") {
		t.Fatal("the pull request check posts the required context cert-review")
	}
}

func TestCertVerifyIsRedForAReleaseRunThatTaggedAndFailed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the workflow steps are bash")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	server := t.TempDir()
	repo := filepath.Join(server, "ai-ecoverse", "slicc-cli.git")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	commit := func(msg string) string {
		git("commit", "-q", "--allow-empty", "-m", msg)
		return git("rev-parse", "HEAD")
	}
	published := commit("published 1.44.3")
	git("tag", "v1.44.3")
	partial := commit("published 1.44.4, then failed")
	git("tag", "-a", "-m", "v1.44.4", "v1.44.4")
	untagged := commit("failed before tagging")
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed")
	}
	step := stepRun(t, "cert-verify.yml", "tag", "tag")
	const api = "repos/ai-ecoverse/slicc-cli/"
	release := func(published string) map[string]any {
		return map[string]any{api + "releases/tags/v1.44.4": map[string]any{"published_at": published}}
	}
	withJobs := func(fixture map[string]any, attempt string, jobs ...string) map[string]any {
		list := []any{}
		for i := 0; i+1 < len(jobs); i += 2 {
			list = append(list, map[string]any{"name": jobs[i], "conclusion": jobs[i+1]})
		}
		fixture[api+"actions/runs/20/attempts/"+attempt+"/jobs?per_page=100"] = map[string]any{"total_count": len(list), "jobs": list}
		return fixture
	}
	during, before := "2026-10-18T03:10:42Z", "2026-10-18T02:00:00Z"
	red := " It is not verified or certified; find out why."
	for _, c := range []struct {
		name, sha, conclusion, input, version, says string
		api                                         map[string]any
		fails, asks                                 bool
	}{
		{"a successful release is verified", published, "success", "", "1.44.3", "release: 1.44.3\n", nil, false, false},
		{"a release run that tagged and then failed is red, naming the release, the run and the failed job", partial, "failure", "", "", "v1.44.4 is tagged on " + partial + ", but the Release run that tagged it ended failure in release: https://github.invalid/run/20." + red, withJobs(release(during), "1", "Still the head of main", "success", "release", "failure", "sliccy-next", "skipped", "smoke (macos-latest, slicc-darwin-arm64)", "skipped"), true, true},
		{"failed smoke tests after tagging are red, naming them", partial, "failure", "", "", "ended failure in smoke (windows-latest, slicc-windows-amd64.exe),install-smoke (alpine-wget):", withJobs(release(during), "1", "release", "success", "smoke (windows-latest, slicc-windows-amd64.exe)", "failure", "install-smoke (alpine-wget)", "timed_out"), true, true},
		{"a run cancelled after tagging is red", partial, "cancelled", "", "", "ended cancelled in sliccy-next:", withJobs(release(during), "1", "release", "success", "sliccy-next", "cancelled"), true, true},
		{"a tag with no GitHub release after a failed run is red", partial, "failure", "", "", "the Release run that tagged it ended failure in release:", withJobs(map[string]any{}, "1", "release", "failure"), true, true},
		{"a failed run whose jobs can't be read is still red", partial, "failure", "", "", "the Release run that tagged it ended failure: https://github.invalid/run/20." + red, release(during), true, true},
		{"a re-run attempt that failed after tagging is red, with that attempt's jobs", partial, "failure", "", "", "the Release run that tagged it ended failure in smoke (windows-latest, slicc-windows-amd64.exe):", withJobs(release(during), "2", "release", "success", "smoke (windows-latest, slicc-windows-amd64.exe)", "failure"), true, true},
		{"a queued run cancelled before its release job is quiet and verifies nothing", partial, "cancelled", "", "", "release: this Release run on " + partial + " ended cancelled before its release job ran, so it didn't make v1.44.4; nothing to verify", withJobs(release(during), "1", "Still the head of main", "success", "full / ci", "cancelled"), false, true},
		{"a run whose gate failed and skipped its release job is quiet", partial, "failure", "", "", "ended failure before its release job ran", withJobs(release(during), "1", "full / ci", "failure", "release", "skipped"), false, true},
		{"a failed run on a commit released before it is quiet and verifies nothing", partial, "failure", "", "", "release: v1.44.4 was released on " + partial + " before this Release run, which ended failure; nothing to verify", release(before), false, true},
		{"a failed run that tagged nothing is quiet", untagged, "failure", "", "", "release: none on " + untagged + "; its Release run ended failure and tagged nothing", nil, false, false},
		{"a cert-verify dispatch verifies the version asked for", "", "", "1.44.3", "1.44.3", "release: 1.44.3\n", nil, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin := t.TempDir()
			fixtures := filepath.Join(bin, "api")
			if err := os.MkdirAll(fixtures, 0o755); err != nil {
				t.Fatal(err)
			}
			for path, v := range c.api {
				writeJSON(t, filepath.Join(fixtures, strings.NewReplacer("/", "_", "?", "_", "&", "_", "=", "_").Replace(path)), v)
			}
			attempt := "1"
			for path := range c.api {
				if strings.Contains(path, "/attempts/2/") {
					attempt = "2"
				}
			}
			log := filepath.Join(bin, "gh.log")
			fakeTool(t, bin, "gh", `printf '%s\n' "$*" >> "$GH_LOG"
path=""; filter="."
while [ $# -gt 0 ]; do case "$1" in repos/*) path=$1 ;; --jq) filter=$2; shift ;; esac; shift; done
f="$API/$(printf '%s' "$path" | tr '/?&=' '____')"
[ -f "$f" ] || { echo "HTTP 404: $path" >&2; exit 1; }
exec jq -r "$filter" "$f"
`)
			r := runSilently(t, step, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_LOG="+log, "API="+fixtures,
				"GITHUB_SERVER_URL=file://"+server, "SHA="+c.sha, "CONCLUSION="+c.conclusion, "STARTED=2026-10-18T03:00:00Z", "RUN=20", "ATTEMPT="+attempt, "RUN_URL=https://github.invalid/run/20", "INPUT="+c.input)
			asked, _ := os.ReadFile(log)
			if (r.code != 0) != c.fails || r.outputs["version"] != c.version || !strings.Contains(r.out, c.says) {
				t.Fatalf("exit %d, version %q, want failing %v and %q saying %q:\n%s", r.code, r.outputs["version"], c.fails, c.version, c.says, r.out)
			}
			if (len(asked) != 0) != c.asks {
				t.Fatalf("GitHub is asked only about a tagged commit whose Release run didn't succeed: %q", asked)
			}
		})
	}
}

func stepRun(t *testing.T, workflow, job, step string) string {
	t.Helper()
	raw, err := os.ReadFile("../../.github/workflows/" + workflow)
	if err != nil {
		t.Fatal(err)
	}
	body, ok := jobsOf(t, strings.ReplaceAll(string(raw), "\r\n", "\n"))[job]
	if !ok {
		t.Fatalf("%s has no job %s", workflow, job)
	}
	steps := strings.Split(body, "\n      - ")
	for _, s := range steps {
		if !strings.Contains(s, "id: "+step+"\n") && !strings.HasPrefix(s, "name: "+step+"\n") && !strings.Contains(s, "\n        name: "+step+"\n") {
			continue
		}
		_, after, ok := strings.Cut(s, "        run: |\n")
		if !ok {
			t.Fatalf("%s/%s has no run block", job, step)
		}
		lines := []string{}
		for _, l := range strings.Split(after, "\n") {
			if l != "" && !strings.HasPrefix(l, "          ") {
				break
			}
			lines = append(lines, strings.TrimPrefix(l, "          "))
		}
		return strings.Join(lines, "\n")
	}
	t.Fatalf("%s has no step %s in %s", workflow, step, job)
	return ""
}

type silentRun struct {
	code    int
	out     string
	outputs map[string]string
	curl    string
}

func runSilently(t *testing.T, script string, env ...string) silentRun {
	t.Helper()
	work := t.TempDir()
	tools := filepath.Join(work, "tools", "cert-review")
	bin := filepath.Join(work, "bin")
	for _, d := range []string{tools, bin, filepath.Join(work, "tmp")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"pr.mjs", "record.mjs", "findings.mjs", "gather.mjs", "verify.mjs", "darwin.mjs", "outputs.mjs", "notes.mjs"} {
		if err := os.WriteFile(filepath.Join(tools, tool), []byte("process.exit(0)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	curlLog := filepath.Join(work, "curl.log")
	fakeTool(t, bin, "curl", `printf '%s\n' "$@" >> "$CURL_LOG"; exit 0
`)
	fakeTool(t, bin, "gh", "exit 0\n")
	gho := filepath.Join(work, "github_output")
	cmd := exec.Command("bash", "-e", "-c", script)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), append([]string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "CURL_LOG=" + curlLog,
		"GITHUB_OUTPUT=" + gho, "GITHUB_STEP_SUMMARY=" + filepath.Join(work, "summary"), "RUNNER_TEMP=" + filepath.Join(work, "tmp"),
		"GITHUB_API_URL=https://api.github.invalid", "GITHUB_SERVER_URL=https://github.invalid", "GITHUB_REPOSITORY=ai-ecoverse/slicc-cli",
		"GITHUB_RUN_ID=1", "GH_REPO=ai-ecoverse/slicc-cli", "RUN_URL=https://github.invalid/run/1", "PR=7", "HEAD_SHA=fake-head-0007", "VERSION=1.43.0", "CERT_CONTEXT=cert-review", "GH_TOKEN=fake-value-0001",
	}, env...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]string{}
	data, _ := os.ReadFile(gho)
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			outputs[k] = v
		}
	}
	curl, _ := os.ReadFile(curlLog)
	return silentRun{code, string(out), outputs, string(curl)}
}

func TestACertToolThatExitsSilentlyNeverPasses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the workflow steps are bash")
	}
	requireNode(t)
	for _, c := range []struct{ workflow, job, step string }{
		{"cert-review-pr.yml", "plan", "plan"},
		{"cert-review-pr.yml", "classify", "classify"},
		{"cert-review-pr.yml", "group", "lookup"},
		{"cert-verify.yml", "rebuild", "Every linux, windows and wasm asset and install.sh byte for byte against the release's SHA256SUMS"},
		{"cert-verify.yml", "darwin", "codesign and Gatekeeper on the published darwin binaries"},
		{"cert-verify.yml", "verify", "The release on npm and GitHub is what release.yml built from the tag on main"},
		{"cert-verify.yml", "notes", "Only the notes edit, from verify's lines"},
	} {
		t.Run(c.job+"/"+c.step, func(t *testing.T) {
			if r := runSilently(t, stepRun(t, c.workflow, c.job, c.step)); r.code == 0 {
				t.Fatalf("a silent tool passed the step:\n%s", r.out)
			}
		})
	}
	t.Run("record", func(t *testing.T) {
		r := runSilently(t, stepRun(t, "cert-review-pr.yml", "record", "record"))
		if r.outputs["conclusion"] != "error" || !strings.Contains(r.outputs["line"], "INCOMPLETE #7: record printed no verdict") {
			t.Fatalf("a silent record must be INCOMPLETE: %v\n%s", r.outputs, r.out)
		}
	})
	t.Run("report", func(t *testing.T) {
		r := runSilently(t, stepRun(t, "cert-review-pr.yml", "report", "The verdict as the status on the pull request's head, and in a comment"), "CONCLUSION=success", "LINE=")
		if r.code == 0 || !strings.Contains(r.curl, `"state":"error"`) || strings.Contains(r.curl, `"state":"success"`) {
			t.Fatalf("success without a certified line must post error and fail: exit %d, curl %s\n%s", r.code, r.curl, r.out)
		}
	})
}

func TestRenovatePullRequestsAskForTheirCertReview(t *testing.T) {
	raw, err := os.ReadFile("../../renovate.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Labels) != 0 || strings.Contains(string(raw), "merge-ok") {
		t.Fatalf("Renovate's pull requests carry no merge-ok: they aren't drafts, so a dev pin's cert-review runs by itself: %v", cfg.Labels)
	}
}
