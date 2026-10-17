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
		r := certTool(t, "", "outputs.mjs", "reproduce", "--release", filepath.Join(dir, "release"), "--rebuilt", filepath.Join(dir, rebuilt), "--out", out)
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

func libCall(t *testing.T, expr string) string {
	t.Helper()
	node := requireNode(t)
	lib, err := filepath.Abs(certTools + "lib.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("import(%q).then((l) => process.stdout.write(JSON.stringify(%s)))", "file://"+filepath.ToSlash(lib), expr)
	if runtime.GOOS == "windows" {
		script = fmt.Sprintf("import(%q).then((l) => process.stdout.write(JSON.stringify(%s)))", "file:///"+filepath.ToSlash(lib), expr)
	}
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return string(out)
}

func TestCertVerifyWritesTheCertifiedLinesIntoTheNotesOnce(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	lines := fmt.Sprintf(`[{prefix: '@ai-ecoverse/wasi-slicc@1.43.0', sha256: '%s', line: 'certified @ai-ecoverse/wasi-slicc@1.43.0 sha256=%s (covers 1.43.0)'}, {prefix: 'slicc-cli@1.43.0 binaries SHA256SUMS', sha256: '%s', line: 'certified slicc-cli@1.43.0 binaries SHA256SUMS sha256=%s (reproducible)'}]`, a, a, b, b)
	var written struct {
		Body      string `json:"body"`
		Unchanged bool   `json:"unchanged"`
		Conflict  string `json:"conflict"`
	}
	if err := json.Unmarshal([]byte(libCall(t, "l.releaseNotes('## Changes\\n\\n* fix: x\\n', "+lines+")")), &written); err != nil {
		t.Fatal(err)
	}
	want := "## Changes\n\n* fix: x\n\n<!-- cert-verify -->\ncertified @ai-ecoverse/wasi-slicc@1.43.0 sha256=" + a + " (covers 1.43.0)\ncertified slicc-cli@1.43.0 binaries SHA256SUMS sha256=" + b + " (reproducible)\n<!-- /cert-verify -->\n"
	if written.Body != want {
		t.Fatalf("notes:\n%s\nwant:\n%s", written.Body, want)
	}
	body, _ := json.Marshal(written.Body)
	var again struct {
		Unchanged bool `json:"unchanged"`
	}
	_ = json.Unmarshal([]byte(libCall(t, "l.releaseNotes("+string(body)+", "+lines+")")), &again)
	if !again.Unchanged {
		t.Fatal("notes that already carry every line stay unchanged")
	}
	other := strings.ReplaceAll(lines, "'"+a+"'", "'"+strings.Repeat("c", 64)+"'")
	var conflict struct {
		Conflict string `json:"conflict"`
	}
	_ = json.Unmarshal([]byte(libCall(t, "l.releaseNotes("+string(body)+", "+other+")")), &conflict)
	if conflict.Conflict != "the release notes already certify @ai-ecoverse/wasi-slicc@1.43.0 sha256="+a {
		t.Fatalf("another sha in the notes is a supply-chain alert, never overwritten: %q", conflict.Conflict)
	}
	stale := "x\n\n<!-- cert-verify -->\ncertified @ai-ecoverse/wasi-slicc@1.43.0 sha256=" + a + " (covers 1.43.0)\n<!-- /cert-verify -->\n"
	sb, _ := json.Marshal(stale)
	var replaced struct {
		Body string `json:"body"`
	}
	_ = json.Unmarshal([]byte(libCall(t, "l.releaseNotes("+string(sb)+", "+lines+")")), &replaced)
	if strings.Count(replaced.Body, "<!-- cert-verify -->") != 1 || !strings.Contains(replaced.Body, "binaries SHA256SUMS sha256="+b) {
		t.Fatalf("a partial block is replaced by the whole one: %s", replaced.Body)
	}
}

func TestCertVerifyChecksTheProvenanceOfEveryPackage(t *testing.T) {
	commit := strings.Repeat("1", 40)
	statement := func(subject, ref, builder, git string) string {
		return fmt.Sprintf(`{subject: [{name: '%s', digest: {sha512: 'fake-sha512'}}], predicate: {buildDefinition: {externalParameters: {workflow: {repository: 'https://github.com/ai-ecoverse/slicc-cli', path: '.github/workflows/release.yml', ref: '%s'}}, resolvedDependencies: [{digest: {gitCommit: '%s'}}]}, runDetails: {builder: {id: '%s'}}}}`, subject, ref, git, builder)
	}
	hosted := "https://github.com/actions/runner/github-hosted"
	args := func(name, version string) string {
		return fmt.Sprintf(`{repo: 'ai-ecoverse/slicc-cli', name: '%s', version: '%s', sha512: 'fake-sha512', commit: '%s'}`, name, version, commit)
	}
	cases := []struct {
		stmt, args, want string
	}{
		{statement("pkg:npm/%40ai-ecoverse/wasi-slicc@1.43.0", "refs/heads/main", hosted, commit), args("@ai-ecoverse/wasi-slicc", "1.43.0"), "[]"},
		{statement("pkg:npm/sliccy@7.0.0-next.1.43.0", "refs/heads/main", hosted, commit), args("sliccy", "7.0.0-next.1.43.0"), "[]"},
		{statement("pkg:npm/%40ai-ecoverse/slicc@1.43.0", "refs/heads/other", hosted, commit), args("@ai-ecoverse/slicc", "1.43.0"), "provenance ref is refs/heads/other"},
		{statement("pkg:npm/%40ai-ecoverse/slicc@1.43.0", "refs/heads/main", "https://example.invalid/self-hosted", commit), args("@ai-ecoverse/slicc", "1.43.0"), "provenance builder is https://example.invalid/self-hosted"},
		{statement("pkg:npm/%40ai-ecoverse/slicc@1.43.0", "refs/heads/main", hosted, strings.Repeat("2", 40)), args("@ai-ecoverse/slicc", "1.43.0"), "isn't the tag's commit"},
		{statement("pkg:npm/%40ai-ecoverse/slicc@1.42.0", "refs/heads/main", hosted, commit), args("@ai-ecoverse/slicc", "1.43.0"), "the provenance subject isn't this tarball's sha512"},
	}
	for _, c := range cases {
		got := libCall(t, "l.provenanceProblems("+c.stmt+", "+c.args+").problems")
		if c.want == "[]" && got != "[]" || c.want != "[]" && !strings.Contains(got, c.want) {
			t.Fatalf("problems %s, want %s", got, c.want)
		}
	}
}

func TestReleaseVerifiesWhatItPublishedCodeOnly(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	jobs := jobsOf(t, strings.ReplaceAll(string(raw), "\r\n", "\n"))
	gate := "if: ${{ !cancelled() && needs.release.result == 'success' && needs.release.outputs.version != '' && github.ref == 'refs/heads/main' }}"
	for name, runner := range map[string]string{"verify-rebuild": "${{ vars.CERT_RUNNER || 'ubuntu-latest' }}", "verify-darwin": "macos-latest", "verify": "${{ vars.CERT_LIGHT_RUNNER || 'ubuntu-latest' }}"} {
		body, ok := jobs[name]
		if !ok {
			t.Fatalf("release.yml has no %s job", name)
		}
		if !strings.Contains(body, "    runs-on: "+runner+"\n") || !strings.Contains(body, "    "+gate+"\n") {
			t.Errorf("%s: runs on %s, only after a release on main", name, runner)
		}
		if strings.Contains(body, "id-token") || strings.Contains(body, "secrets.") || strings.Contains(body, "claude") {
			t.Errorf("%s is code only, with no id-token and no secret", name)
		}
		if strings.Contains(body, "contents: write") != (name == "verify") {
			t.Errorf("%s: only verify writes, to edit the release notes", name)
		}
	}
	if !strings.Contains(jobs["verify"], "    name: release / verify on npm\n") || !strings.Contains(jobs["verify"], "    needs: [release, verify-rebuild, verify-darwin]\n") {
		t.Fatal("verify on npm reads the rebuild and darwin evidence")
	}
	if !strings.Contains(jobs["verify"], "tools/cert-review/verify.mjs") || !strings.Contains(jobs["verify-darwin"], "tools/cert-review/darwin.mjs") || !strings.Contains(jobs["verify-rebuild"], "git archive --format=tar \"v$VERSION\"") {
		t.Fatal("the verify jobs run the cert tools on the tag")
	}
	wf, err := os.ReadFile("../../.github/workflows/cert-review-pr.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wf), "\nenv:\n  CERT_CONTEXT: cert-review\n") {
		t.Fatal("the pull request check posts the required context cert-review")
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
	for _, tool := range []string{"pr.mjs", "record.mjs", "findings.mjs", "verify.mjs", "darwin.mjs", "outputs.mjs"} {
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
		{"release.yml", "verify-rebuild", "Every linux, windows and wasm asset and install.sh byte for byte against the release's SHA256SUMS"},
		{"release.yml", "verify-darwin", "codesign and Gatekeeper on the published darwin binaries"},
		{"release.yml", "verify", "The release on npm and GitHub is what release.yml built from the tag on main, and its notes carry the certified lines"},
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
