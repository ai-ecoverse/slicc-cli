package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	capjerk "github.com/ai-ecoverse/slicc-cli/internal/caps/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

func TestWindowsArgs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"windows-args","private":true}`)
	npm := exec.Command("cmd", "/c", "npm", "install", "--no-audit", "--no-fund", "@ai-ecoverse/slicc-jerk")
	npm.Dir = root
	if out, err := npm.CombinedOutput(); err != nil {
		t.Fatalf("npm install: %v\n%s", err, out)
	}
	modules := filepath.Join(root, "node_modules")
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	exe := filepath.Join(modules, "@ai-ecoverse", "slicc-jerk-win32-"+arch, "bin", "jerk.exe")
	t.Setenv("PATH", filepath.Join(modules, ".bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	find := func() *jerk.Jerk {
		t.Helper()
		j, old := jerk.Find()
		if j == nil || j.Path != exe {
			t.Fatalf("found %+v, older %q, want %s", j, old, exe)
		}
		return j
	}
	find()
	if err := os.Rename(exe, exe+".npm"); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", exe, "./testdata/fakejerk").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	log := filepath.Join(root, "calls.log")
	t.Setenv("FAKEJERK_LOG", log)
	j := find()

	t.Run("values", func(t *testing.T) {
		marker := "windows-args.tmp"
		t.Cleanup(func() { _ = os.Remove(marker) })
		for _, v := range []string{"plain", "a&b", "a&&b", "a^b", "a^^b", "a|b", "100%", "a%b%c", "%USERNAME%", "%%", "x&cd.>" + marker} {
			_ = os.Remove(log)
			want := []string{"github", "create", "--via-link", "-p", "value=" + v}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			out := j.Run(ctx, "github", jerk.Call{Args: want})
			cancel()
			if got := loggedArgs(t, log); !slices.Equal(got, want) || out.Died || out.Result.Exit != 0 {
				t.Errorf("value %q arrived as %q, exit %d, died %v", v, got, out.Result.Exit, out.Died)
			}
		}
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("%s exists after the run", marker)
		}
	})

	t.Run("sibling", func(t *testing.T) {
		data := t.TempDir()
		marker := filepath.Join(t.TempDir(), "sibling.tmp")
		writeFile(t, filepath.Join(data, "jerk"), "not a program\n")
		writeFile(t, filepath.Join(data, "jerk.cmd"), "@echo jerk 9.9.9\r\n@echo ran>"+marker+"\r\n")
		t.Setenv("PATH", os.Getenv("SystemRoot")+`\System32`)
		if got, old := jerk.Find(data); got != nil {
			t.Errorf("found %+v, older %q", got, old)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Error("the file next to the candidate ran")
		}
	})

	t.Run("launcher", func(t *testing.T) {
		mustNotSkip(t)
		nodeBin := requireNode(t)
		tool := filepath.Join(modules, "@ai-ecoverse", "slicc-windows-"+runtime.GOARCH, "bin", "slicc.exe")
		if out, err := exec.Command("go", "build", "-o", tool, "./testdata/printpath").CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		script, err := os.ReadFile(filepath.Join("..", "..", "npm", "slicc.js"))
		if err != nil {
			t.Fatal(err)
		}
		launcher := filepath.Join(modules, "@ai-ecoverse", "slicc", "bin", "slicc.js")
		writeFile(t, launcher, string(script))
		r := runBin(t, os.Environ(), nodeBin, launcher)
		var got map[string]string
		if r.code != 0 || json.Unmarshal([]byte(r.stdout), &got) != nil {
			t.Fatalf("%+v", r)
		}
		if !strings.EqualFold(filepath.Ext(got["jerk"]), ".exe") {
			t.Errorf("the launcher's first jerk is %s", got["jerk"])
		}
	})

	t.Run("link", func(t *testing.T) {
		mustNotSkip(t)
		env := setupInProcess(t, policy.Local(), func(n *node.Node) {
			capjerk.Register(n, capjerk.Options{Jerk: j, Seal: true, CreateTimeout: 20 * time.Second})
		})
		for _, c := range []string{"%", "&", "|", "^", "<", ">", "!", `"`, "(", ")", "＆", "％", "＂", "｜", "é"} {
			v := "a" + c + "b"
			for _, open := range []map[string]any{
				{"op": "create", "provider": "github", "params": map[string]string{"value": v}},
				{"op": "create", "provider": "github", "scope": v},
				{"op": "unseal", "provider": "github", "lease_id": v},
			} {
				_ = os.Remove(log)
				r := env.p.jerk(t, open)
				if _, err := os.Stat(log); r.Reset != "EPROTO" || err == nil {
					t.Errorf("%v: reset %q (%s), started %v", open, r.Reset, r.Detail, err == nil)
				}
			}
		}
		_ = os.Remove(log)
		r := env.p.jerk(t, map[string]any{"op": "create", "provider": "github", "scope": "repo read", "params": map[string]string{"value": "a-b"}})
		want := []string{"github", "create", "--via-link", "--scope", "repo read", "-p", "value=a-b"}
		if got := loggedArgs(t, log); r.Reset != "" || !slices.Equal(got, want) {
			t.Errorf("plain values: reset %q, arrived as %q", r.Reset, got)
		}
	})
}

func mustNotSkip(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		if t.Skipped() {
			t.Error("this test has to run")
		}
	})
}

func loggedArgs(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var call struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &call); err != nil {
		t.Fatal(err)
	}
	return call.Args
}
