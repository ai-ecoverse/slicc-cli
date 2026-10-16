package integration

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func runIn(t *testing.T, dir string, env []string, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("(%s) %s %s: %v\n%s", dir, name, strings.Join(args, " "), err, out)
	}
	return out
}

func untar(t *testing.T, data []byte, to string) {
	t.Helper()
	r := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(to, filepath.FromSlash(h.Name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, body, os.FileMode(h.Mode).Perm()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestReleaseBuildIsReproducibleFromAnyCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/package.sh runs on macOS and Linux only")
	}
	for _, tool := range []string{"git", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	tagged := filepath.Join(work, "tagged")
	archived := filepath.Join(work, "archived")
	if err := os.MkdirAll(tagged, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"go.mod", "go.sum", "install.sh", "cmd", "internal", "scripts"} {
		from := filepath.Join(root, p)
		info, err := os.Stat(from)
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			copyTree(t, from, filepath.Join(tagged, p))
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tagged, p), data, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	gitEnv := []string{"GIT_AUTHOR_NAME=slicc test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=slicc test", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
	runIn(t, tagged, gitEnv, "git", "init", "-q", "-b", "main")
	runIn(t, tagged, gitEnv, "git", "add", "-A")
	runIn(t, tagged, gitEnv, "git", "commit", "-q", "-m", "fix: the release commit")
	if err := os.MkdirAll(archived, 0o755); err != nil {
		t.Fatal(err)
	}
	untar(t, runIn(t, tagged, gitEnv, "git", "archive", "--format=tar", "HEAD"), archived)
	runIn(t, tagged, gitEnv, "git", "tag", "v99.0.0")

	env := []string{"SLICC_SIGN=0", "RUNNER_TEMP=" + work}
	sums := map[string][]byte{}
	for _, tree := range []string{tagged, archived} {
		start := time.Now()
		runIn(t, tree, env, "bash", "scripts/package.sh", "1.2.3")
		t.Logf("%s built in %s", filepath.Base(tree), time.Since(start).Round(time.Second))
		data, err := os.ReadFile(filepath.Join(tree, "artifacts", "release", "SHA256SUMS"))
		if err != nil {
			t.Fatal(err)
		}
		sums[tree] = data
	}
	lines := strings.Split(strings.TrimSpace(string(sums[tagged])), "\n")
	if len(lines) != 8 {
		t.Fatalf("SHA256SUMS has %d lines, want 8:\n%s", len(lines), sums[tagged])
	}
	if !bytes.Equal(sums[tagged], sums[archived]) {
		t.Fatalf("a checkout tagged after the build and a git archive of it build different bytes:\ntagged checkout:\n%s\ngit archive:\n%s", sums[tagged], sums[archived])
	}
	for _, line := range lines {
		name := strings.TrimPrefix(strings.Fields(line)[1], "*")
		a, err := os.ReadFile(filepath.Join(tagged, "artifacts", "release", name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(archived, "artifacts", "release", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("%s differs between the two builds", name)
		}
	}
	host := "slicc-" + runtime.GOOS + "-" + runtime.GOARCH
	out := runIn(t, archived, nil, filepath.Join(archived, "artifacts", "release", host), "--version")
	if strings.TrimSpace(string(out)) != "slicc 1.2.3" {
		t.Fatalf("%s --version: %q, want slicc 1.2.3", host, out)
	}
}
