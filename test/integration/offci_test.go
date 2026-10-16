package integration

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var browserGates = []string{"findChrome", "testChrome", "withDiscovery", "sixEnv"}

func browserTests(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	calls := map[string]map[string]bool{}
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil {
				name = "." + name
			}
			called := map[string]bool{}
			ast.Inspect(fd, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					called[x.Name] = true
				case *ast.SelectorExpr:
					called["."+x.Sel.Name] = true
				}
				return true
			})
			calls[name] = called
		}
	}
	marked := map[string]bool{}
	for _, g := range browserGates {
		marked[g] = true
	}
	for grew := true; grew; {
		grew = false
		for name, called := range calls {
			if marked[name] {
				continue
			}
			for c := range called {
				if marked[c] && c != name {
					marked[name], grew = true, true
					break
				}
			}
		}
	}
	var tests []string
	for name := range marked {
		if strings.HasPrefix(name, "Test") && name != "TestMain" && name != t.Name() {
			tests = append(tests, name)
		}
	}
	sort.Strings(tests)
	return tests
}

var installedBrowser = regexp.MustCompile(`(?i)(\.app/contents/macos/|chrome|chromium|msedge|brave|vivaldi|opera)`)

type browserWatch struct {
	root string
	mu   sync.Mutex
	hits map[string]bool
	hit  chan struct{}
	once sync.Once
	stop chan struct{}
	done chan struct{}
}

func watchInstalledBrowsers(root string) *browserWatch {
	w := &browserWatch{root: root, hits: map[string]bool{}, hit: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for {
			w.scan()
			select {
			case <-w.stop:
				w.scan()
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
	return w
}

func processLines() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if runtime.GOOS == "linux" {
		return procLines()
	}
	var out []byte
	if runtime.GOOS == "windows" {
		out, _ = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command",
			"Get-CimInstance Win32_Process | ForEach-Object { [string]$_.ProcessId + ' ' + $_.CommandLine }").Output()
	} else {
		out, _ = exec.CommandContext(ctx, "ps", "-A", "-ww", "-o", "pid=,args=").Output()
	}
	return strings.Split(string(bytes.ReplaceAll(out, []byte("\r"), nil)), "\n")
}

func procLines() []string {
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	var lines []string
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "cmdline"))
		if err != nil || len(b) == 0 {
			continue
		}
		lines = append(lines, filepath.Base(d)+" "+strings.TrimSpace(string(bytes.ReplaceAll(b, []byte{0}, []byte{' '}))))
	}
	return lines
}

func (w *browserWatch) scan() {
	root := strings.ToLower(filepath.ToSlash(w.root))
	for _, line := range processLines() {
		l := strings.ToLower(filepath.ToSlash(line))
		at := strings.Index(l, root)
		if at < 0 {
			continue
		}
		if exe := l[:at]; installedBrowser.MatchString(exe) {
			w.mu.Lock()
			w.hits[strings.TrimSpace(line)] = true
			w.mu.Unlock()
			w.once.Do(func() { close(w.hit) })
		}
	}
}

func (w *browserWatch) finish() []string {
	close(w.stop)
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	var hits []string
	for h := range w.hits {
		if len(h) > 300 {
			h = h[:300]
		}
		hits = append(hits, h)
	}
	sort.Strings(hits)
	return hits
}

func offCIEnv(root string) []string {
	var env []string
	for _, kv := range realEnv {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "CI", "SLICC_TEST_CHROME", "TMPDIR", "TMP", "TEMP":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "TMPDIR="+root, "TMP="+root, "TEMP="+root)
}

func TestOffCINoTestRunsAnInstalledBrowser(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("this test runs the browser tests as they would run off CI; it needs CI so that it never runs them on a developer machine by mistake")
	}
	tests := browserTests(t)
	if len(tests) < 20 || !slices.Contains(tests, "TestSixAttachOpensSevenAndLeavesTheProfileAsItWas") || !slices.Contains(tests, "TestPromptJSONPrintsTheRawEvents") || !slices.Contains(tests, "TestLaunchRealChromeAndCloseOnInterrupt") {
		t.Fatalf("the browser tests found are %v", tests)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "off-ci")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}

	canaryDir := filepath.Join(t.TempDir(), "Google Chrome Canary")
	canary := filepath.Join(canaryDir, exeName("chrome"))
	build := exec.Command("go", "build", "-o", canary, "./testdata/fakechrome")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		t.Fatal(err)
	}
	canaryProfile := filepath.Join(root, "canary-profile")
	if err := os.MkdirAll(canaryProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	w := watchInstalledBrowsers(root)
	probe := exec.Command(canary, "--remote-debugging-port=0", "--user-data-dir="+canaryProfile)
	if err := probe.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		w.mu.Lock()
		seen := len(w.hits)
		w.mu.Unlock()
		if seen > 0 {
			break
		}
		if time.Now().After(deadline) {
			_ = probe.Process.Kill()
			_ = probe.Wait()
			w.finish()
			t.Fatal("the watch never saw a browser outside the run's temp dir that uses a profile inside it")
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = probe.Process.Kill()
	_ = probe.Wait()
	if hits := w.finish(); len(hits) != 1 || !strings.Contains(hits[0], "Google Chrome Canary") {
		t.Fatalf("the canary was seen as %q", hits)
	}

	w = watchInstalledBrowsers(root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	go func() {
		select {
		case <-w.hit:
			time.Sleep(2 * time.Second)
			cancel()
		case <-ctx.Done():
		}
	}()
	run := exec.CommandContext(ctx, os.Args[0], "-test.run", "^("+strings.Join(tests, "|")+")$", "-test.count=1", "-test.v", "-test.timeout=4m")
	run.Env = offCIEnv(root)
	out, err := run.CombinedOutput()
	hits := w.finish()
	if len(hits) > 20 {
		hits = append(hits[:20], "…")
	}
	if len(hits) > 0 {
		t.Errorf("off CI, the browser tests ran an installed browser:\n  %s", strings.Join(hits, "\n  "))
	}
	if err != nil {
		t.Errorf("the off-CI run of %d tests failed (%v):\n%s", len(tests), err, tail(string(out), 80))
	}
	skips := strings.Count(string(out), "--- SKIP: ")
	if skips == 0 || !strings.Contains(string(out), harnessChromeNotice) || !strings.Contains(string(out), testChromeNotice) {
		t.Errorf("off CI, %d tests skipped, without both notices:\n%s", skips, tail(string(out), 80))
	}
	t.Logf("off CI: %d browser tests, %d skipped", len(tests), skips)
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append([]string{"… " + strconv.Itoa(len(lines)-n) + " lines …"}, lines[len(lines)-n:]...)
	}
	return strings.Join(lines, "\n")
}
