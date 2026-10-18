package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const fakeGH = `import { appendFileSync, copyFileSync, readdirSync, readFileSync } from 'node:fs';
import { basename, join } from 'node:path';
const dir = process.env.FAKE_DIR;
const args = process.argv.slice(2);
appendFileSync(join(dir, 'gh.log'), JSON.stringify(args) + '\n');
const api = JSON.parse(readFileSync(join(dir, 'api.json'), 'utf8'));
if (args[0] === 'api') {
  const path = args.filter((a) => !a.startsWith('--'))[1].split('?')[0];
  if (!(path in api)) { console.error('HTTP 404: ' + path); process.exit(1); }
  console.log(JSON.stringify(args.includes('--slurp') ? [api[path]] : api[path]));
} else if (args[0] === 'release' && args[1] === 'download') {
  const to = args[args.indexOf('--dir') + 1];
  for (const f of readdirSync(join(dir, 'assets'))) copyFileSync(join(dir, 'assets', f), join(to, f));
} else if (args[0] === 'attestation') {
  if ((process.env.FAKE_ATTEST_FAIL ?? '') === basename(args[2])) { console.error('no attestation'); process.exit(1); }
} else if (args[0] === 'release' && args[1] === 'edit') {
  appendFileSync(join(dir, 'edits.log'), readFileSync(args[args.indexOf('--notes-file') + 1], 'utf8'));
} else { console.error('fake gh: ' + args.join(' ')); process.exit(1); }
`

const fakeNPM = `import { readFileSync } from 'node:fs';
import { join } from 'node:path';
const args = process.argv.slice(2);
if (args[0] === 'view') {
  const meta = JSON.parse(readFileSync(join(process.env.FAKE_DIR, 'npm.json'), 'utf8'))[args[1]];
  if (!meta) { console.error('npm error code E404'); process.exit(1); }
  if (typeof meta === 'string') { console.error('npm error code ' + meta); process.exit(1); }
  console.log(JSON.stringify(meta));
} else if (args[0] === 'install') {
} else if (args[0] === 'audit') {
  console.log('audited 1 package\n1 package has a verified registry signature\n1 package has a verified attestation');
} else { console.error('fake npm: ' + args.join(' ')); process.exit(1); }
`

const (
	verifyRepo    = "ai-ecoverse/slicc-cli"
	verifyVersion = "1.43.0"
	verifyCommit  = "1111111111111111111111111111111111111111"
)

type verifyFixture struct {
	dir       string
	assets    map[string][]byte
	api       map[string]any
	npm       map[string]any
	rebuild   map[string]any
	darwin    map[string]any
	tgz       []byte
	redirect  string
	env       []string
	badSRI    bool
	artifacts []map[string]any
	declared  string
	moreNPM   func(registry string) map[string]any
}

const verifyRunStart = "2026-01-01T10:00:00Z"

func verifyArtifact(id int, name, created string) map[string]any {
	return map[string]any{"id": id, "name": name, "digest": "sha256:" + sha256Hex([]byte(name)), "created_at": created}
}

func wasiTarball(t *testing.T, wasm []byte, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	pkg, _ := json.Marshal(map[string]any{"name": "@ai-ecoverse/wasi-slicc", "version": version})
	for _, f := range []struct {
		name string
		body []byte
	}{{"package/package.json", pkg}, {"package/bin/slicc.wasm", wasm}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newVerifyFixture(t *testing.T) *verifyFixture {
	f := &verifyFixture{dir: t.TempDir(), assets: map[string][]byte{}}
	for _, p := range []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64", "windows-arm64.exe", "windows-amd64.exe"} {
		f.assets["slicc-"+p] = []byte("fake-binary-" + p)
	}
	f.assets["slicc-wasip1-wasm.wasm"] = []byte("fake-wasm")
	f.assets["install.sh"] = []byte("#!/bin/sh\n")
	f.tgz = wasiTarball(t, f.assets["slicc-wasip1-wasm.wasm"], verifyVersion)
	f.redirect = "https://github.com/" + verifyRepo + "/releases/latest/download/install.sh"
	f.api = map[string]any{
		"repos/" + verifyRepo + "/git/ref/tags/v" + verifyVersion:  map[string]any{"object": map[string]any{"type": "commit", "sha": verifyCommit}},
		"repos/" + verifyRepo + "/compare/main..." + verifyCommit:  map[string]any{"status": "identical"},
		"repos/" + verifyRepo + "/releases/tags/v" + verifyVersion: map[string]any{"html_url": "https://example.invalid/release", "body": "## Changes\n"},
		"repos/" + verifyRepo + "/releases/latest":                 map[string]any{"tag_name": "v9.9.9"},
		"repos/" + verifyRepo + "/tags":                            []any{},
		"repos/" + verifyRepo + "/issues":                          []any{},
	}
	f.env = []string{"SLICC_NPM_PUBLISH=", "SLICCY_NEXT_PUBLISH=", "WASI_SLICC_PUBLISH=true"}
	f.api["repos/"+verifyRepo+"/actions/runs/1"] = map[string]any{"run_attempt": 1, "run_started_at": verifyRunStart}
	f.artifacts = []map[string]any{verifyArtifact(101, "cert-verify-rebuild", "2026-01-01T10:05:00Z"), verifyArtifact(102, "cert-verify-darwin", "2026-01-01T10:06:00Z")}
	f.declared = "cert-verify-rebuild=101:" + sha256Hex([]byte("cert-verify-rebuild")) + ", cert-verify-darwin=102:" + sha256Hex([]byte("cert-verify-darwin"))
	return f
}

func (f *verifyFixture) since(state, description string) {
	f.api["repos/"+verifyRepo+"/tags"] = []any{map[string]any{"name": "v1.42.9"}, map[string]any{"name": "v" + verifyVersion}}
	f.api["repos/"+verifyRepo+"/compare/v1.42.9...v"+verifyVersion] = map[string]any{"commits": []any{map[string]any{"commit": map[string]any{"message": "Merge pull request #5 from ai-ecoverse/x"}}}}
	f.api["repos/"+verifyRepo+"/pulls/5"] = map[string]any{"head": map[string]any{"sha": "fake-head-0005"}}
	statuses := []any{}
	if state != "" {
		statuses = append(statuses, map[string]any{"context": "cert-review", "state": state, "description": description})
	}
	f.api["repos/"+verifyRepo+"/commits/fake-head-0005/statuses"] = statuses
}

func (f *verifyFixture) sums() string {
	names := []string{}
	for n := range f.assets {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", sha256Hex(f.assets[n]), n)
	}
	return b.String()
}

func (f *verifyFixture) run(t *testing.T, mutate func(f *verifyFixture)) (int, string, map[string]any) {
	t.Helper()
	sums := f.sums()
	f.rebuild = map[string]any{"match": true, "differ": []string{}, "files": 7, "releaseSums": sha256Hex([]byte(sums))}
	f.darwin = map[string]any{"team": "FAKETEAM01", "binaries": []map[string]any{
		{"asset": "slicc-darwin-arm64", "sha256": sha256Hex(f.assets["slicc-darwin-arm64"]), "status": "PASS"},
		{"asset": "slicc-darwin-amd64", "sha256": sha256Hex(f.assets["slicc-darwin-amd64"]), "status": "PASS"},
	}}
	if mutate != nil {
		mutate(f)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slicc":
			http.Redirect(w, r, f.redirect, http.StatusFound)
		case "/wasi.tgz":
			_, _ = w.Write(f.tgz)
		case "/attestations/wasi":
			digest := sha512.Sum512(f.tgz)
			statement, _ := json.Marshal(map[string]any{
				"subject": []any{map[string]any{"name": "pkg:npm/%40ai-ecoverse/wasi-slicc@" + verifyVersion, "digest": map[string]any{"sha512": hex.EncodeToString(digest[:])}}},
				"predicate": map[string]any{
					"buildDefinition": map[string]any{
						"externalParameters":   map[string]any{"workflow": map[string]any{"repository": "https://github.com/" + verifyRepo, "path": ".github/workflows/release.yml", "ref": "refs/heads/main"}},
						"resolvedDependencies": []any{map[string]any{"digest": map[string]any{"gitCommit": verifyCommit}}},
					},
					"runDetails": map[string]any{"builder": map[string]any{"id": "https://github.com/actions/runner/github-hosted"}},
				},
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"attestations": []any{map[string]any{"predicateType": "https://slsa.dev/provenance/v1", "bundle": map[string]any{"dsseEnvelope": map[string]any{"payload": base64.StdEncoding.EncodeToString(statement)}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	if f.npm == nil {
		digest := sha512.Sum512(f.tgz)
		f.npm = map[string]any{"@ai-ecoverse/wasi-slicc@" + verifyVersion: map[string]any{
			"version": verifyVersion, "gitHead": verifyCommit,
			"dist": map[string]any{"tarball": srv.URL + "/wasi.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(digest[:]), "attestations": map[string]any{"url": srv.URL + "/attestations/wasi"}},
		}}
		if f.badSRI {
			f.npm["@ai-ecoverse/wasi-slicc@"+verifyVersion].(map[string]any)["dist"].(map[string]any)["integrity"] = "sha512-ZmFrZQ=="
		}
	}
	if f.moreNPM != nil {
		for k, v := range f.moreNPM(srv.URL) {
			f.npm[k] = v
		}
	}
	for _, d := range []string{"assets", "bin", "evidence", "tmp"} {
		if err := os.MkdirAll(filepath.Join(f.dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rel := []map[string]any{}
	for n, b := range f.assets {
		if err := os.WriteFile(filepath.Join(f.dir, "assets", n), b, 0o644); err != nil {
			t.Fatal(err)
		}
		rel = append(rel, map[string]any{"name": n})
	}
	if err := os.WriteFile(filepath.Join(f.dir, "assets", "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	rel = append(rel, map[string]any{"name": "SHA256SUMS"})
	tagged := f.api["repos/"+verifyRepo+"/releases/tags/v"+verifyVersion].(map[string]any)
	tagged["assets"] = rel
	f.api["repos/"+verifyRepo+"/actions/runs/1/artifacts"] = map[string]any{"total_count": len(f.artifacts), "artifacts": f.artifacts}
	writeJSON(t, filepath.Join(f.dir, "api.json"), f.api)
	writeJSON(t, filepath.Join(f.dir, "npm.json"), f.npm)
	if f.rebuild != nil {
		writeJSON(t, filepath.Join(f.dir, "evidence", "rebuild", "rebuild.json"), f.rebuild)
	}
	if f.darwin != nil {
		writeJSON(t, filepath.Join(f.dir, "evidence", "darwin", "darwin.json"), f.darwin)
	}
	node := requireNode(t)
	for name, src := range map[string]string{"gh": fakeGH, "npm": fakeNPM} {
		if err := os.WriteFile(filepath.Join(f.dir, name+".mjs"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		fakeTool(t, filepath.Join(f.dir, "bin"), name, fmt.Sprintf("exec %q %q \"$@\"\n", node, filepath.Join(f.dir, name+".mjs")))
	}
	out := filepath.Join(f.dir, "verify.json")
	_ = os.Remove(out)
	args := []string{certTools + "verify.mjs"}
	if f.declared != "" {
		args = append(args, "--artifacts", f.declared)
	}
	cmd := exec.Command(node, append(args, "--version", verifyVersion, "--run", "1", "--wait", "1", "--rebuild", filepath.Join(f.dir, "evidence", "rebuild", "rebuild.json"), "--darwin", filepath.Join(f.dir, "evidence", "darwin", "darwin.json"), "--installer", srv.URL+"/slicc", "--out", out, "--lines", filepath.Join(f.dir, "lines.json"))...)
	cmd.Env = append(os.Environ(), append([]string{"PATH=" + filepath.Join(f.dir, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"), "FAKE_DIR=" + f.dir, "CERT_NPM_REGISTRY=" + srv.URL, "RUNNER_TEMP=" + filepath.Join(f.dir, "tmp"), "GITHUB_STEP_SUMMARY="}, f.env...)...)
	msg, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	data, _ := os.ReadFile(out)
	_ = json.Unmarshal(data, &rec)
	return code, string(msg), rec
}

func TestCertVerifyCertifiesOnlyAReleaseThatVerifiesEverywhere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh and npm are shell shims; verify runs on Linux")
	}
	cases := []struct {
		name, state, want string
		mutate            func(f *verifyFixture)
	}{
		{"everything verifies", "success", "certified slicc-cli@1.43.0 binaries SHA256SUMS sha256=", nil},
		{"the tag isn't on main", "failure", "supply-chain alert: slicc-cli v1.43.0 doesn't verify: v1.43.0 (" + verifyCommit + ") is diverged of main", func(f *verifyFixture) {
			f.api["repos/"+verifyRepo+"/compare/main..."+verifyCommit] = map[string]any{"status": "diverged"}
		}},
		{"an extra asset", "failure", "supply-chain alert", func(f *verifyFixture) { f.assets["extra.bin"] = []byte("fake-extra") }},
		{"a missing asset", "failure", "supply-chain alert", func(f *verifyFixture) { delete(f.assets, "slicc-linux-arm64") }},
		{"an asset that isn't its SHA256SUMS line", "failure", "the release's slicc-linux-amd64 is", func(f *verifyFixture) { f.assets["slicc-linux-amd64"] = []byte("fake-swapped") }},
		{"an attestation that doesn't verify", "failure", "gh attestation verify slicc-windows-amd64.exe", func(f *verifyFixture) {
			f.env = append(f.env, "FAKE_ATTEST_FAIL=slicc-windows-amd64.exe")
		}},
		{"a darwin binary that fails its signature check", "failure", "slicc-darwin-arm64: its team isn't S8LB56P782", func(f *verifyFixture) {
			f.darwin["binaries"].([]map[string]any)[0]["status"] = "FAIL"
			f.darwin["binaries"].([]map[string]any)[0]["why"] = "its team isn't S8LB56P782"
		}},
		{"a darwin check of another binary", "error", "cert-ci: INCOMPLETE slicc-cli v1.43.0 (the darwin check read another slicc-darwin-amd64", func(f *verifyFixture) {
			f.darwin["binaries"].([]map[string]any)[1]["sha256"] = strings.Repeat("0", 64)
		}},
		{"no rebuild evidence", "error", "cert-ci: INCOMPLETE slicc-cli v1.43.0 (no rebuild from the tag", func(f *verifyFixture) { f.rebuild = nil }},
		{"not reproducible", "failure", "not reproducible: a git archive of v1.43.0 builds different slicc-linux-amd64", func(f *verifyFixture) {
			f.rebuild["match"] = false
			f.rebuild["differ"] = []string{"slicc-linux-amd64"}
		}},
		{"a wrong installer redirect", "failure", "/slicc answers 302 → https://example.invalid/install.sh", func(f *verifyFixture) { f.redirect = "https://example.invalid/install.sh" }},
		{"a tarball that isn't its integrity", "failure", "@ai-ecoverse/wasi-slicc: dist.integrity is sha512-ZmFrZQ==", func(f *verifyFixture) { f.badSRI = true }},
		{"notes that already certify another sha", "failure", "supply-chain alert: slicc-cli v1.43.0: the release notes already certify @ai-ecoverse/wasi-slicc@1.43.0 sha256=" + strings.Repeat("a", 64), func(f *verifyFixture) {
			f.api["repos/"+verifyRepo+"/releases/tags/v"+verifyVersion].(map[string]any)["body"] = "<!-- cert-verify -->\ncertified @ai-ecoverse/wasi-slicc@1.43.0 sha256=" + strings.Repeat("a", 64) + " (x)\n<!-- /cert-verify -->\n"
		}},
		{"a package its publish variable leaves out", "success", "not published: @ai-ecoverse/slicc", func(f *verifyFixture) {
			f.env = []string{"SLICC_NPM_PUBLISH=", "SLICCY_NEXT_PUBLISH=", "WASI_SLICC_PUBLISH="}
		}},
		{"a package of the set that isn't on npm is INCOMPLETE and named, and nothing is certified", "error", "cert-ci: INCOMPLETE slicc-cli v1.43.0 (sliccy@7.0.0-next.1.43.0 isn't on npm after 1 s; publish sliccy@7.0.0-next.1.43.0, then dispatch cert-verify with version=1.43.0)", func(f *verifyFixture) {
			f.env = []string{"SLICC_NPM_PUBLISH=", "SLICCY_NEXT_PUBLISH=true", "WASI_SLICC_PUBLISH=true"}
		}},
		{"a package that isn't on npm hides no alert in the rest of the release", "failure", "/slicc answers 302 → https://example.invalid/install.sh, not https://github.com/" + verifyRepo + "/releases/latest/download/install.sh; also missing: sliccy@7.0.0-next.1.43.0 isn't on npm after 1 s", func(f *verifyFixture) {
			f.env = []string{"SLICC_NPM_PUBLISH=", "SLICCY_NEXT_PUBLISH=true", "WASI_SLICC_PUBLISH=true"}
			f.redirect = "https://example.invalid/install.sh"
		}},
		{"npm failing on a package hides no alert in the rest of the release", "failure", "/slicc answers 302 → https://example.invalid/install.sh, not https://github.com/" + verifyRepo + "/releases/latest/download/install.sh; also missing: sliccy@7.0.0-next.1.43.0: a check didn't finish: npm error code E500", func(f *verifyFixture) {
			f.env = []string{"SLICC_NPM_PUBLISH=", "SLICCY_NEXT_PUBLISH=true", "WASI_SLICC_PUBLISH=true"}
			f.redirect = "https://example.invalid/install.sh"
			f.moreNPM = func(string) map[string]any { return map[string]any{"sliccy@7.0.0-next.1.43.0": "E500"} }
		}},
		{"a package npm lists without serving its tarball is INCOMPLETE, not unpublished", "error", "cert-ci: INCOMPLETE slicc-cli v1.43.0 (sliccy@7.0.0-next.1.43.0: npm lists it, but doesn't serve its tarball after 1 s; re-run the failed jobs)", func(f *verifyFixture) {
			f.env = []string{"SLICC_NPM_PUBLISH=", "SLICCY_NEXT_PUBLISH=true", "WASI_SLICC_PUBLISH=true"}
			f.moreNPM = func(registry string) map[string]any {
				return map[string]any{"sliccy@7.0.0-next.1.43.0": map[string]any{"version": "7.0.0-next.1.43.0", "dist": map[string]any{"tarball": registry + "/sliccy-unserved.tgz"}}}
			}
		}},
		{"a tarball on another host is an alert, even when it doesn't answer", "failure", "sliccy: the tarball is at http://127.0.0.1:9", func(f *verifyFixture) {
			f.env = []string{"SLICC_NPM_PUBLISH=", "SLICCY_NEXT_PUBLISH=true", "WASI_SLICC_PUBLISH=true"}
			f.moreNPM = func(string) map[string]any {
				return map[string]any{"sliccy@7.0.0-next.1.43.0": map[string]any{"version": "7.0.0-next.1.43.0", "dist": map[string]any{"tarball": "http://127.0.0.1:9/sliccy.tgz"}}}
			}
		}},
		{"every pull request since the last tag certified in the pull request", "success", "certified @ai-ecoverse/wasi-slicc@1.43.0 sha256=", func(f *verifyFixture) {
			f.since("success", "certified ai-ecoverse/slicc-cli PR #5 at fake-head-00")
		}},
		{"a pull request without a cert-review makes the lines verified, never certified", "success", "verified slicc-cli@1.43.0 binaries SHA256SUMS sha256=", func(f *verifyFixture) {
			f.since("", "")
		}},
		{"an artifact in the run that no producer declared", "error", "cert-ci: INCOMPLETE slicc-cli v1.43.0 (cert-verify-planted (artifact 103) is in the run, but no producer declared it", func(f *verifyFixture) {
			f.artifacts = append(f.artifacts, verifyArtifact(103, "cert-verify-planted", "2026-01-01T10:07:00Z"))
		}},
		{"an evidence artifact whose digest isn't its producer's", "error", "cert-verify-darwin's digest is sha256:" + sha256Hex([]byte("fake-forged")) + ", its producer said sha256:" + sha256Hex([]byte("cert-verify-darwin")), func(f *verifyFixture) {
			f.artifacts[1]["digest"] = "sha256:" + sha256Hex([]byte("fake-forged"))
		}},
		{"an evidence artifact under another id than its producer's", "error", "cert-verify-rebuild: its producer's artifact 101 isn't in the run", func(f *verifyFixture) {
			f.artifacts[0]["id"] = 104
		}},
		{"an earlier attempt's artifact is no evidence and no alarm", "success", "certified slicc-cli@1.43.0 binaries SHA256SUMS sha256=", func(f *verifyFixture) {
			f.artifacts = append(f.artifacts, verifyArtifact(99, "cert-record", "2026-01-01T09:59:59Z"))
		}},
		{"no declared artifacts", "error", "no producer declared its artifacts", func(f *verifyFixture) { f.declared = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newVerifyFixture(t)
			code, out, rec := f.run(t, c.mutate)
			if rec["state"] != c.state || !strings.Contains(out, c.want) {
				t.Fatalf("state %v, want %s with %q\n%s", rec["state"], c.state, c.want, out)
			}
			if (code == 0) != (c.state == "success") {
				t.Fatalf("exit %d for %s", code, c.state)
			}
			_, linesErr := os.Stat(filepath.Join(f.dir, "lines.json"))
			if (linesErr == nil) != (c.state == "success") {
				t.Fatalf("lines handed on: %v, for %s", linesErr == nil, c.state)
			}
			if edits, _ := os.ReadFile(filepath.Join(f.dir, "edits.log")); len(edits) != 0 {
				t.Fatal("verify never edits the notes")
			}
		})
	}
}

func TestCertVerifyWritesTheNotesOnceAndNeverOverAnotherSha(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell shim; the notes job runs on Linux")
	}
	node := requireNode(t)
	a, b, c := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	wasi := map[string]any{"prefix": "@ai-ecoverse/wasi-slicc@1.43.0", "sha256": a, "line": "certified @ai-ecoverse/wasi-slicc@1.43.0 sha256=" + a + " (covers 1.43.0)"}
	bins := map[string]any{"prefix": "slicc-cli@1.43.0 binaries SHA256SUMS", "sha256": b, "line": "certified slicc-cli@1.43.0 binaries SHA256SUMS sha256=" + b + " (reproducible)"}
	block := func(lines ...string) string {
		return "## Changes\n\n<!-- cert-verify -->\n" + strings.Join(lines, "\n") + "\n<!-- /cert-verify -->\n"
	}
	sliccy := "certified sliccy@7.0.0-next.1.43.0 sha256=" + c + " (covers 7.0.0-next.1.43.0)"
	cases := []struct {
		name, body, want, edit string
		lines                  []any
		code                   int
	}{
		{"an earlier verified line with the same sha counts as written", block(strings.Replace(wasi["line"].(string), "certified", "verified", 1), strings.Replace(bins["line"].(string), "certified", "verified", 1)), "unchanged", "", []any{wasi, bins}, 0},
		{"plain notes get the lines", "## Changes\n", "notes: wrote the 2 certified lines into v1.43.0's release notes", block(wasi["line"].(string), bins["line"].(string)), []any{wasi, bins}, 0},
		{"notes that carry them stay as they are", block(wasi["line"].(string), bins["line"].(string)), "notes: v1.43.0 already carries the 2 certified lines, unchanged", "", []any{wasi, bins}, 0},
		{"a line for a package outside this run's set is kept", block(sliccy, wasi["line"].(string)), "notes: wrote", block(sliccy, wasi["line"].(string), bins["line"].(string)), []any{wasi, bins}, 0},
		{"another sha is an alert, and the notes stay", block(strings.ReplaceAll(wasi["line"].(string), a, c)), "supply-chain alert: slicc-cli v1.43.0: the release notes already certify @ai-ecoverse/wasi-slicc@1.43.0 sha256=" + c, "", []any{wasi, bins}, 1},
		{"no lines", "## Changes\n", "cert-ci: INCOMPLETE slicc-cli v1.43.0: verify handed over no well-formed lines", "", []any{}, 1},
		{"a line that doesn't match its sha", "## Changes\n", "cert-ci: INCOMPLETE", "", []any{map[string]any{"prefix": wasi["prefix"], "sha256": c, "line": wasi["line"]}}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range []string{"bin", "tmp"} {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			writeJSON(t, filepath.Join(dir, "api.json"), map[string]any{"repos/" + verifyRepo + "/releases/tags/v" + verifyVersion: map[string]any{"body": tc.body}})
			if err := os.WriteFile(filepath.Join(dir, "gh.mjs"), []byte(fakeGH), 0o644); err != nil {
				t.Fatal(err)
			}
			fakeTool(t, filepath.Join(dir, "bin"), "gh", fmt.Sprintf("exec %q %q \"$@\"\n", node, filepath.Join(dir, "gh.mjs")))
			lines := writeJSON(t, filepath.Join(dir, "lines.json"), tc.lines)
			cmd := exec.Command(node, certTools+"notes.mjs", "--version", verifyVersion, "--lines", lines)
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_DIR="+dir, "RUNNER_TEMP="+filepath.Join(dir, "tmp"), "GITHUB_STEP_SUMMARY=")
			out, err := cmd.CombinedOutput()
			code := 0
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tc.code || !strings.Contains(string(out), tc.want) {
				t.Fatalf("exit %d, want %d with %q:\n%s", code, tc.code, tc.want, out)
			}
			edits, _ := os.ReadFile(filepath.Join(dir, "edits.log"))
			if string(edits) != tc.edit {
				t.Fatalf("the notes became:\n%s\nwant:\n%s", edits, tc.edit)
			}
		})
	}
}
