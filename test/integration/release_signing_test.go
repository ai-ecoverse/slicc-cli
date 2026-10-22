//go:build !windows

package integration

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeRcodesign = `#!/bin/sh
echo "$*" >> "$RCODESIGN_LOG"
case "$1" in
  sign)
    for last; do :; done
    printf 'signed' >> "$last"
    ;;
  encode-app-store-connect-api-key)
    printf '{}' > "$3"
    ;;
  notary-submit)
    for last; do :; done
    case "$last" in
      *.zip) unzip -l "$last" >> "$RCODESIGN_LOG" ;;
      *) echo "Error: do not know how to notarize $last" >&2; exit 1 ;;
    esac
    [ -z "${RCODESIGN_NOTARY_FAIL:-}" ] || { echo "notarization rejected" >&2; exit 4; }
    ;;
esac
`

func signingTree(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	for _, p := range []string{"go.mod", "go.sum", "install.sh", "cmd", "internal", "scripts"} {
		from := filepath.Join(root, p)
		info, err := os.Stat(from)
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			copyTree(t, from, filepath.Join(tree, p))
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tree, p), data, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	return tree
}

func appleSecrets(t *testing.T) []string {
	t.Helper()
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("openssl is required for the signing test")
		}
		t.Skip("openssl is not installed")
	}
	dir := t.TempDir()
	runIn(t, dir, nil, openssl, "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "key.pem", "-out", "cert.pem", "-days", "1", "-subj", "/CN=slicc test")
	runIn(t, dir, nil, openssl, "pkcs12", "-export", "-in", "cert.pem", "-inkey", "key.pem", "-out", "cert.p12", "-passout", "pass:secret")
	p12, err := os.ReadFile(filepath.Join(dir, "cert.p12"))
	if err != nil {
		t.Fatal(err)
	}
	return []string{
		"APPLE_CERTIFICATE_BASE64=" + base64.StdEncoding.EncodeToString(p12),
		"APPLE_DEVELOPER_CERTIFICATE_PASSWORD=secret",
		"APPLE_API_KEY_ID=KEY123",
		"APPLE_API_KEY_ISSUER_ID=00000000-0000-0000-0000-000000000000",
		"APPLE_API_KEY_P8_BASE64=" + base64.StdEncoding.EncodeToString([]byte("not a real key")),
	}
}

func packageRun(t *testing.T, tree, temp string, env []string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", "scripts/package.sh", "1.2.3")
	cmd.Dir = tree
	cmd.Env = append(os.Environ(), append([]string{"RUNNER_TEMP=" + temp}, env...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func TestTheReleaseSignsAndNotarizesBothDarwinBinariesBeforeItsChecksums(t *testing.T) {
	secrets := appleSecrets(t)
	tree := signingTree(t)
	tools := t.TempDir()
	tool := filepath.Join(tools, "rcodesign")
	if err := os.WriteFile(tool, []byte(fakeRcodesign), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(tools, "log")
	temp := t.TempDir()
	env := append([]string{"SLICC_SIGN=required", "RCODESIGN=" + tool, "RCODESIGN_LOG=" + log}, secrets...)
	if out, code := packageRun(t, tree, temp, env); code != 0 {
		t.Fatalf("package.sh with every Apple secret: exit %d\n%s", code, out)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	log2 := string(raw)
	for _, bin := range []string{"slicc-darwin-arm64", "slicc-darwin-amd64"} {
		if !strings.Contains(log2, " "+bin+"\n") {
			t.Errorf("the notarized zip lacks %s:\n%s", bin, log2)
		}
	}
	var calls []string
	for _, line := range strings.Split(strings.TrimSpace(log2), "\n") {
		if f := strings.Fields(line); len(f) > 0 && strings.Contains("sign verify notary-submit encode-app-store-connect-api-key", f[0]) && !strings.HasPrefix(line, " ") {
			calls = append(calls, line)
		}
	}
	release := filepath.Join(tree, "artifacts", "release")
	var want []string
	for _, bin := range []string{"slicc-darwin-arm64", "slicc-darwin-amd64"} {
		path := filepath.Join("artifacts", "release", bin)
		want = append(want, "sign --pem-file * --binary-identifier ai.sliccy.slicc --code-signature-flags runtime --for-notarization "+path, "verify "+path)
	}
	want = append(want, "notary-submit --api-key-file * --wait --max-wait-seconds 1800 * notarize.zip")
	if len(calls) != 1+len(want) || !strings.HasPrefix(calls[0], "encode-app-store-connect-api-key -o ") || !strings.Contains(calls[0], " 00000000-0000-0000-0000-000000000000 KEY123 ") {
		t.Fatalf("rcodesign calls:\n%s", raw)
	}
	for i, w := range want {
		got := calls[i+1]
		parts := strings.Split(w, " * ")
		ok := got == w
		if len(parts) > 1 {
			ok = strings.HasPrefix(got, parts[0]+" ") && strings.HasSuffix(got, parts[len(parts)-1])
			for _, p := range parts[1 : len(parts)-1] {
				ok = ok && strings.Contains(got, " "+p+" ")
			}
		}
		if !ok {
			t.Fatalf("rcodesign call %d is %q, want %q", i+2, got, w)
		}
	}
	sums, err := os.ReadFile(filepath.Join(release, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{"slicc-darwin-arm64", "slicc-darwin-amd64", "slicc-linux-amd64"} {
		data, err := os.ReadFile(filepath.Join(release, bin))
		if err != nil {
			t.Fatal(err)
		}
		if signed := strings.HasSuffix(string(data), "signed"); signed != strings.HasPrefix(bin, "slicc-darwin") {
			t.Errorf("%s signed: %v", bin, signed)
		}
		sum := sha256.Sum256(data)
		if !strings.Contains(string(sums), hex.EncodeToString(sum[:])+"  "+bin+"\n") {
			t.Errorf("SHA256SUMS doesn't hash %s as it ships:\n%s", bin, sums)
		}
	}
	if left, _ := os.ReadDir(temp); len(left) != 0 {
		t.Errorf("package.sh left %v in RUNNER_TEMP, keys and certificates among them", left)
	}

	if out, code := packageRun(t, tree, t.TempDir(), append(env, "RCODESIGN_NOTARY_FAIL=1")); code == 0 || !strings.Contains(out, "notarization rejected") {
		t.Fatalf("a rejected notarization must fail the build: exit %d\n%s", code, out)
	}
	for _, missing := range []string{"APPLE_CERTIFICATE_BASE64", "APPLE_API_KEY_P8_BASE64"} {
		var partial []string
		for _, kv := range env {
			if !strings.HasPrefix(kv, missing+"=") {
				partial = append(partial, kv)
			}
		}
		partial = append(partial, missing+"=")
		out, code := packageRun(t, tree, t.TempDir(), partial)
		if code == 0 || !strings.Contains(out, "::error::signing and notarizing the darwin binaries needs "+missing) {
			t.Fatalf("SLICC_SIGN=required without %s: exit %d\n%s", missing, code, out)
		}
		out, code = packageRun(t, tree, t.TempDir(), append(partial, "SLICC_SIGN="))
		if code != 0 || !strings.Contains(out, "::warning::no "+missing+"; the darwin binaries stay unsigned") {
			t.Fatalf("an unsigned build without %s: exit %d\n%s", missing, code, out)
		}
	}
	if !strings.Contains(readRepo(t, "scripts/release-build.sh"), `SLICC_SIGN="${SLICC_SIGN:-required}" bash scripts/package.sh "$version"`) {
		t.Fatal("release-build.sh doesn't require signing")
	}
}
