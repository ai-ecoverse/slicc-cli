package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	goModule      = "github.com/ai-ecoverse/slicc-cli"
	goTestVersion = "v1.99.0"
	goInstallCmd  = "go install github.com/ai-ecoverse/slicc-cli/cmd/slicc@latest"
)

var (
	goInstallOnce sync.Once
	goInstallBin  string
	goInstallErr  string
)

func fileURL(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

func moduleFiles(root string) ([]string, error) {
	files := []string{"go.mod", "go.sum", "LICENSE"}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err == nil {
				files = append(files, filepath.ToSlash(rel))
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func writeModuleZip(root, dest, prefix string) error {
	files, err := moduleFiles(root)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		w, err := zw.Create(prefix + name)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(dest, buf.Bytes(), 0o644)
}

func fakeModuleProxy(root, dir, version string) error {
	at := filepath.Join(dir, filepath.FromSlash(goModule), "@v")
	if err := os.MkdirAll(at, 0o755); err != nil {
		return err
	}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return err
	}
	files := map[string]string{
		"list":            version + "\n",
		version + ".info": `{"Version":"` + version + `","Time":"2025-01-01T00:00:00Z"}`,
		version + ".mod":  string(gomod),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(at, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return writeModuleZip(root, filepath.Join(at, version+".zip"), goModule+"@"+version+"/")
}

func goEnv(key string) (string, error) {
	out, err := exec.Command("go", "env", key).Output()
	return strings.TrimSpace(string(out)), err
}

func goInstallEnv(work string) ([]string, error) {
	cache, err := goEnv("GOMODCACHE")
	if err != nil {
		return nil, err
	}
	env := []string{}
	for _, kv := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if strings.HasPrefix(key, "GO") || key == "CGO_ENABLED" {
			continue
		}
		env = append(env, kv)
	}
	proxies := []string{fileURL(filepath.Join(work, "proxy")), fileURL(filepath.Join(cache, "cache", "download")), "https://proxy.golang.org"}
	return append(env,
		"GOENV=off",
		"GOPROXY="+strings.Join(proxies, ","),
		"GOSUMDB=off",
		"GOFLAGS=-modcacherw",
		"GOMODCACHE="+filepath.Join(work, "mod"),
		"GOPATH="+filepath.Join(work, "gopath"),
		"GOBIN="+filepath.Join(work, "bin"),
		"CGO_ENABLED=0",
	), nil
}

func goInstall(work string) (string, string) {
	root, err := filepath.Abs("../..")
	if err != nil {
		return "", err.Error()
	}
	if err := fakeModuleProxy(root, filepath.Join(work, "proxy"), goTestVersion); err != nil {
		return "", "fake module proxy: " + err.Error()
	}
	env, err := goInstallEnv(work)
	if err != nil {
		return "", "go env: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "install", "-trimpath", goModule+"/cmd/slicc@"+goTestVersion)
	cmd.Dir = work
	cmd.Env = env
	if msg, err := cmd.CombinedOutput(); err != nil {
		return "", "go install: " + err.Error() + "\n" + string(msg)
	}
	return filepath.Join(work, "bin", exeName("slicc")), ""
}

func goInstalled(t *testing.T) string {
	t.Helper()
	goInstallOnce.Do(func() {
		work := filepath.Join(filepath.Dir(binary), "goinstall")
		if err := os.MkdirAll(work, 0o755); err != nil {
			goInstallErr = err.Error()
			return
		}
		goInstallBin, goInstallErr = goInstall(work)
	})
	if goInstallErr != "" {
		t.Fatal(goInstallErr)
	}
	return goInstallBin
}

func copyBinary(t *testing.T, from, dir string) string {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(dir, exeName("slicc"))
	if err := os.WriteFile(to, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return to
}

func TestGoInstallReportsTheModuleVersion(t *testing.T) {
	bin := goInstalled(t)
	r := runBin(t, isolatedEnv(t), bin, "--version")
	if r.code != 0 || r.stdout != "slicc 1.99.0\n" {
		t.Fatalf("%+v", r)
	}
	out, err := exec.Command("go", "version", "-m", bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "mod\t"+goModule+"\t"+goTestVersion+"\th1:") {
		t.Fatalf("build info:\n%s", out)
	}
}

func TestUpdateIsDisabledForGoInstalls(t *testing.T) {
	gh := newFakeGitHub(t, "v1.100.0", newRelease(t, "1.100.0"))
	bin := copyBinary(t, goInstalled(t), t.TempDir())
	for _, args := range [][]string{{"update"}, {"update", "--check"}} {
		r := runBin(t, gh.env(t), bin, args...)
		if r.code != 1 || !strings.Contains(r.stderr, "installed with go install; update it with `"+goInstallCmd+"`") {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	if n := gh.requests.Load(); n != 0 {
		t.Fatalf("%d requests to GitHub", n)
	}
	if got := versionOf(t, bin); got != "slicc 1.99.0" {
		t.Fatalf("binary changed: %q", got)
	}
}

func TestUpdateNoticeForGoInstallsNamesGoInstall(t *testing.T) {
	gh := newFakeGitHub(t, "v1.100.0", nil)
	bin := goInstalled(t)
	env := gh.env(t)
	runBin(t, env, bin, "no-such-command")
	r := runBin(t, env, bin, "no-such-command")
	if !strings.Contains(r.stderr, "slicc 1.100.0 is available (you have 1.99.0); run `"+goInstallCmd+"`") {
		t.Fatalf("%+v", r)
	}
}

func TestGoInstallSendsTelemetryAsARelease(t *testing.T) {
	gh := newFakeGitHub(t, "v1.99.0", nil)
	col := newCollector(t)
	r := runBin(t, gh.env(t, col.env()...), goInstalled(t), "update")
	if r.code != 1 {
		t.Fatalf("%+v", r)
	}
	if enter := col.find("enter"); enter == nil || enter.Source != "update" {
		t.Fatalf("beacons: %+v", col.got())
	}
}

func TestABuildWithoutALinkedVersionIsADevelopmentBuild(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, exeName("slicc"))
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "../../cmd/slicc")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if msg, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, msg)
	}
	if got := versionOf(t, bin); got != "slicc dev" {
		t.Fatalf("--version: %q", got)
	}
	gh := newFakeGitHub(t, "v9.0.0", nil)
	r := runBin(t, gh.env(t), bin, "update")
	if r.code != 1 || !strings.Contains(r.stderr, "refusing to replace a development build (dev)") {
		t.Fatalf("%+v", r)
	}
}

func TestUpdateNamesTheNPMCommand(t *testing.T) {
	built := buildVersion(t, "1.0.0", t.TempDir())
	root := t.TempDir()
	platform := filepath.Join("@ai-ecoverse", "slicc-"+target(), "bin")
	npx := filepath.Join(root, "_npx", "abc", "node_modules")
	if err := os.MkdirAll(filepath.Join(npx, "sliccy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(npx, "sliccy", "package.json"), []byte(`{"name":"sliccy"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		dir, launcher, want string
	}{
		{t.TempDir(), "npm", "installed with npm; update it with `npm install -g @ai-ecoverse/slicc@latest`, or run the latest with `npx @ai-ecoverse/slicc@latest`"},
		{filepath.Join(root, "lib", "node_modules", "@ai-ecoverse", "slicc", "node_modules", platform), "", "`npm install -g @ai-ecoverse/slicc@latest`"},
		{filepath.Join(root, "lib", "node_modules", "sliccy", "node_modules", platform), "npm", "`npm install -g sliccy@next`, or run the latest with `npx sliccy@next`"},
		{filepath.Join(npx, platform), "npm", "installed with npm (npx); run `npx sliccy@next` for the latest"},
		{filepath.Join(root, "_npx", "def", "node_modules", platform), "npm", "installed with npm (npx); run `npx @ai-ecoverse/slicc@latest` for the latest"},
	}
	gh := newFakeGitHub(t, "v1.1.0", nil)
	for _, c := range cases {
		bin := copyBinary(t, built, c.dir)
		r := runBin(t, gh.env(t, "SLICC_LAUNCHER="+c.launcher), bin, "update")
		if r.code != 1 || !strings.Contains(r.stderr, c.want) {
			t.Fatalf("%s: %+v", c.dir, r)
		}
	}
	if n := gh.requests.Load(); n != 0 {
		t.Fatalf("%d requests to GitHub", n)
	}
}
