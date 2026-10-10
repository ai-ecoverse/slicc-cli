package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func requireNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node is required for the npm launcher tests")
		}
		t.Skip("node is not installed")
	}
	return node
}

func target() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func stage(t *testing.T, node, bin string, extra ...string) string {
	t.Helper()
	from, out := t.TempDir(), t.TempDir()
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, assetName()), data, 0o755); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"../../scripts/npm-stage.mjs", "--version", "1.2.3-test", "--from", from, "--out", out, "--only", target()}, extra...)
	if msg, err := exec.Command(node, args...).CombinedOutput(); err != nil {
		t.Fatalf("npm-stage: %v\n%s", err, msg)
	}
	return out
}

func install(t *testing.T, staged, modules string, withPlatform bool) {
	t.Helper()
	if err := os.CopyFS(filepath.Join(modules, "@ai-ecoverse", "slicc"), os.DirFS(filepath.Join(staged, "slicc"))); err != nil {
		t.Fatal(err)
	}
	if withPlatform {
		if err := os.CopyFS(filepath.Join(modules, "@ai-ecoverse", "slicc-"+target()), os.DirFS(filepath.Join(staged, "slicc-"+target()))); err != nil {
			t.Fatal(err)
		}
	}
}

func launcher(t *testing.T, node, modules string, env []string, args ...string) result {
	t.Helper()
	return runBin(t, env, node, append([]string{filepath.Join(modules, "@ai-ecoverse", "slicc", "bin", "slicc.js")}, args...)...)
}

func TestNPMPackagesDescribeThePlatform(t *testing.T) {
	node := requireNode(t)
	staged := stage(t, node, binary)
	var main, platform map[string]any
	for path, into := range map[string]*map[string]any{
		filepath.Join(staged, "slicc", "package.json"):           &main,
		filepath.Join(staged, "slicc-"+target(), "package.json"): &platform,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}
	deps, _ := main["optionalDependencies"].(map[string]any)
	if main["name"] != "@ai-ecoverse/slicc" || deps["@ai-ecoverse/slicc-"+target()] != "1.2.3-test" {
		t.Fatalf("main package: %v", main)
	}
	if bin, _ := main["bin"].(map[string]any); bin["slicc"] != "bin/slicc.js" {
		t.Fatalf("main bin: %v", main["bin"])
	}
	npmOS := map[string]string{"darwin": "darwin", "linux": "linux", "windows": "win32"}[runtime.GOOS]
	cpu := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if platform["name"] != "@ai-ecoverse/slicc-"+target() || platform["os"].([]any)[0] != npmOS || platform["cpu"].([]any)[0] != cpu {
		t.Fatalf("platform package: %v", platform)
	}
}

func TestNPMLauncherRunsTheBinary(t *testing.T) {
	node := requireNode(t)
	modules := filepath.Join(t.TempDir(), "node_modules")
	install(t, stage(t, node, binary), modules, true)
	r := launcher(t, node, modules, isolatedEnv(t), "--version")
	if r.code != 0 || r.stdout != "slicc 1.2.3-test\n" {
		t.Fatalf("%+v", r)
	}
	r = launcher(t, node, modules, isolatedEnv(t), "frobnicate", "--x")
	if r.code != 2 || !strings.Contains(r.stderr, `unknown command "frobnicate"`) {
		t.Fatalf("%+v", r)
	}
}

func TestNPMLauncherMarksTheInstallAsNPM(t *testing.T) {
	node := requireNode(t)
	lib := filepath.Join(t.TempDir(), "lib")
	install(t, stage(t, node, buildVersion(t, "1.0.0", t.TempDir())), lib, true)
	gh := newFakeGitHub(t, "v1.1.0", nil)
	r := launcher(t, node, lib, gh.env(t, "NODE_PATH="+lib), "update")
	if r.code != 1 || !strings.Contains(r.stderr, "installed with npm") {
		t.Fatalf("%+v", r)
	}
	if n := gh.requests.Load(); n != 0 {
		t.Fatalf("%d requests to GitHub", n)
	}
}

func TestNPMLauncherExplainsAMissingPlatformPackage(t *testing.T) {
	node := requireNode(t)
	modules := filepath.Join(t.TempDir(), "node_modules")
	install(t, stage(t, node, binary), modules, false)
	r := launcher(t, node, modules, isolatedEnv(t), "--version")
	if r.code != 1 || !strings.Contains(r.stderr, "@ai-ecoverse/slicc-"+target()+" is not installed") {
		t.Fatalf("%+v", r)
	}
}

func TestNPMLauncherCanBeAliased(t *testing.T) {
	node := requireNode(t)
	staged := stage(t, node, binary, "--name", "sliccy")
	data, err := os.ReadFile(filepath.Join(staged, "slicc", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Name string            `json:"name"`
		Bin  map[string]string `json:"bin"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "sliccy" || pkg.Bin["sliccy"] != "bin/slicc.js" || len(pkg.Bin) != 1 {
		t.Fatalf("%+v", pkg)
	}
	modules := filepath.Join(t.TempDir(), "node_modules")
	install(t, staged, modules, true)
	if r := launcher(t, node, modules, isolatedEnv(t), "--version"); r.code != 0 || r.stdout != "slicc 1.2.3-test\n" {
		t.Fatalf("%+v", r)
	}
}

const fakeSignalBinary = `#!/bin/sh
if [ "$2" = trap ]; then
  for sig in TERM INT HUP; do
    trap "echo $sig > '$1.sig'; exit 7" $sig
  done
  echo ready > "$1"
  sleep 30 &
  wait
  exit 1
fi
echo ready > "$1"
exec sleep 30
`

func startFake(t *testing.T, node, modules string, args ...string) (*exec.Cmd, string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(node, append([]string{filepath.Join(modules, "@ai-ecoverse", "slicc", "bin", "slicc.js"), marker}, args...)...)
	cmd.Env = isolatedEnv(t)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("the fake binary never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	return cmd, marker
}

func TestNPMLauncherForwardsSignals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX signals to forward")
	}
	node := requireNode(t)
	fake := filepath.Join(t.TempDir(), "slicc")
	if err := os.WriteFile(fake, []byte(fakeSignalBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	modules := filepath.Join(t.TempDir(), "node_modules")
	install(t, stage(t, node, fake), modules, true)
	names := map[syscall.Signal]string{syscall.SIGTERM: "TERM", syscall.SIGINT: "INT", syscall.SIGHUP: "HUP"}
	for sig, name := range names {
		cmd, marker := startFake(t, node, modules, "trap")
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		got, _ := os.ReadFile(marker + ".sig")
		if strings.TrimSpace(string(got)) != name || cmd.ProcessState.ExitCode() != 7 {
			t.Fatalf("%v: child saw %q, launcher ended with %v", sig, got, cmd.ProcessState)
		}
		cmd, _ = startFake(t, node, modules)
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		status, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !status.Signaled() || status.Signal() != sig {
			t.Fatalf("%v: launcher ended with %v", sig, cmd.ProcessState)
		}
	}
}
