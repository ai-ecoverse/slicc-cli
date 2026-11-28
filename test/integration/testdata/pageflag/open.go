//go:build !wasip1

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/caps/kernelin"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
)

const (
	pageFlagChildEnv  = "SLICC_PAGEFLAG_CHILD"
	pageFlagRootEnv   = "SLICC_PAGEFLAG_ROOT"
	pageFlagNoCtlDir  = 8
	pageFlagTermBound = 10 * time.Second
)

type pageFlagChildResult struct {
	code    int
	pid     int
	port    string
	ended   string
	stderr  string
	home    []string
	runtime []string
}

func pageFlagRanUntilEnded(t *testing.T, r pageFlagChildResult, args []string) {
	t.Helper()
	if strings.Contains(r.stderr, "is on a protected host and is not a seven page") {
		t.Fatalf("slicc %q refused: exit %d, ended %q, stderr %q", args, r.code, r.ended, r.stderr)
	}
	four := "slicc: listening on 127.0.0.1:" + r.port
	lines := strings.Split(r.stderr, "\n")
	if !slices.Contains(lines, four) && !slices.Contains(lines, four+" and [::1]:"+r.port) {
		t.Fatalf("slicc %q: exit %d, ended %q, stderr %q; want %q", args, r.code, r.ended, r.stderr, four+" and [::1]:"+r.port)
	}
	if r.ended == "" || (r.code != 0 && r.code != -1) {
		t.Fatalf("slicc %q: exit %d, ended %q, stderr %q; want it to run until the fixture ended it, then exit 0 on SIGTERM", args, r.code, r.ended, r.stderr)
	}
	if r.code == -1 {
		t.Logf("slicc %q ended by the fixture (%s) with a signal exit", args, r.ended)
	}
}

func pageFlagRoot(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if runtime.GOOS != "windows" && len(base) > 40 {
		base = "/tmp"
	}
	root, err := os.MkdirTemp(base, "pf")
	if err != nil {
		t.Fatal(err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func pageFlagFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func pageFlagOwnControlFiles(r pageFlagChildResult) bool {
	own := []string{strconv.Itoa(r.pid) + ".sock", strconv.Itoa(r.pid) + ".sock.lock"}
	for _, f := range r.runtime {
		parts := strings.Split(f, "/")
		if len(parts) != 2 || !slices.Contains(own, parts[1]) {
			return false
		}
	}
	return true
}

func pageFlagClosedPorts(t *testing.T) (loopback, cdp string, release func()) {
	t.Helper()
	a, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		_ = a.Close()
		t.Fatal(err)
	}
	loopback = strconv.Itoa(a.Addr().(*net.TCPAddr).Port)
	cdp = strconv.Itoa(b.Addr().(*net.TCPAddr).Port)
	release = func() {
		_ = a.Close()
		_ = b.Close()
	}
	return loopback, cdp, release
}

func pageFlagChild(t *testing.T, args ...string) pageFlagChildResult {
	t.Helper()
	return pageFlagSpawn(t, pageFlagSpawnOptions{}, args...)
}

type pageFlagSpawnOptions struct {
	pickBefore bool
	killAfter  time.Duration
}

func pageFlagSpawn(t *testing.T, opts pageFlagSpawnOptions, args ...string) pageFlagChildResult {
	t.Helper()
	if opts.pickBefore {
		release := originFlagEnsurePickFallback(t)
		t.Cleanup(release)
	}
	loopback, cdpPort, releasePorts := pageFlagClosedPorts(t)
	cdp := "http://127.0.0.1:" + cdpPort
	args = append([]string(nil), args...)
	for i := range args {
		if args[i] == "http://127.0.0.1:1" {
			args[i] = cdp
		}
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--port" && args[i+1] == "0" {
			args[i+1] = loopback
		}
	}
	root := pageFlagRoot(t)
	home, run := filepath.Join(root, "h"), filepath.Join(root, "r")
	for _, d := range []string{home, run} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^Test(Page|Origin)FlagRefusalHappensBeforeListen$")
	cmd.Env = append(os.Environ(),
		pageFlagChildEnv+"="+string(raw),
		pageFlagRootEnv+"="+root,
		"HOME="+home,
		"USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"APPDATA="+filepath.Join(home, "appdata"),
		"LOCALAPPDATA="+filepath.Join(home, "localappdata"),
		"XDG_RUNTIME_DIR="+run,
		"TMPDIR="+run,
		"TMP="+run,
		"TEMP="+run,
		"SLICC_DISCOVERY=isolated",
		loopbackPortsEnv+"="+loopback+"-"+loopback,
		openCommandEnv+"="+filepath.Join(home, "no-browser"),
		"SLICC_DEBUG=",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	releasePorts()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	r := pageFlagChildResult{pid: cmd.Process.Pid, port: loopback}
	limit := 2 * time.Minute
	deadline := time.After(limit)
	var kill <-chan time.Time
	if opts.killAfter > 0 {
		kill = time.After(opts.killAfter)
	}
	var werr error
	timedOut := false
wait:
	for {
		select {
		case werr = <-exited:
			break wait
		case <-kill:
			r.ended = "SIGTERM"
			if cmd.Process.Signal(syscall.SIGTERM) != nil {
				r.ended = "kill"
				_ = cmd.Process.Kill()
				werr = <-exited
				break wait
			}
			select {
			case werr = <-exited:
			case <-time.After(pageFlagTermBound):
				r.ended = "kill"
				_ = cmd.Process.Kill()
				werr = <-exited
			}
			break wait
		case <-deadline:
			timedOut = true
			_ = cmd.Process.Kill()
			<-exited
			break wait
		}
	}
	r.code = cmd.ProcessState.ExitCode()
	r.stderr = stderr.String()
	if timedOut {
		t.Fatalf("slicc %q still running after %s:\n%s", args, limit, r.stderr)
	}
	if werr != nil && r.code == -1 && r.ended == "" {
		t.Fatalf("slicc %q: %v\n%s", args, werr, r.stderr)
	}
	if r.code == pageFlagNoCtlDir {
		t.Fatalf("slicc %q: the control directory would not be under %s", args, root)
	}
	r.home = pageFlagFiles(t, home)
	r.runtime = pageFlagFiles(t, run)
	return r
}

func TestPageFlagRefusalHappensBeforeListen(t *testing.T) {
	if raw := os.Getenv(pageFlagChildEnv); raw != "" {
		originFlagChildMain(raw)
		return
	}
	for _, c := range []struct {
		name, want string
		args       []string
	}{
		{"Open", "slicc open: --page https://lease-pilot.sliccy.ai is on a protected host and is not a seven page\n", []string{"open", "--page", "https://lease-pilot.sliccy.ai/", "--no-kernel", "--no-jerk"}},
		{"Pair", "slicc pair: --page https://vault.sliccy.ai is on a protected host and is not a seven page\n", []string{"pair", "--page", "https://vault.sliccy.ai/", "--no-kernel", "--no-jerk"}},
		{"DefaultBrowser", "slicc open: --page https://lease-pilot.sliccy.ai is on a protected host and is not a seven page\n", []string{"--default-browser", "--page", "https://LEASE-PILOT.sliccy.ai/", "--no-kernel", "--no-jerk"}},
		{"DefaultMode", "slicc: --page https://vault-main.sliccy.ai is on a protected host and is not a seven page\n", []string{"--plain", "--no-fallback", "--page", "https://vault-main.sliccy.ai/", "--no-kernel", "--no-jerk"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := pageFlagChild(t, c.args...)
			if r.code != 2 || r.stderr != c.want || len(r.home) != 0 || len(r.runtime) != 0 {
				t.Fatalf("slicc %q: exit %d, home %q, runtime %q, stderr %q; want exit 2, no files and %q", c.args, r.code, r.home, r.runtime, r.stderr, c.want)
			}
		})
	}
	t.Run("SevenListens", func(t *testing.T) {
		args := []string{"open", "--page", "https://seven.sliccy.ai/", "--no-kernel", "--no-jerk"}
		r := pageFlagSpawn(t, pageFlagSpawnOptions{killAfter: 3 * time.Second}, args...)
		pageFlagRanUntilEnded(t, r, args)
		identity := slices.ContainsFunc(r.home, func(f string) bool { return strings.HasSuffix(f, "/slicc/loopback-identity.json") })
		if !identity || !pageFlagOwnControlFiles(r) {
			t.Fatalf("slicc open with seven's page: home %q, runtime %q, exit %d, stderr %q; want an identity and only its own control files", r.home, r.runtime, r.code, r.stderr)
		}
	})
}

const originFlagExtID = "abcdefghijklmnopqrstuvwxyzabcdef"

func originFlagRefusal(origin string) string {
	return "--origin " + origin + " is on a protected host and is not a seven page"
}

func originFlagChildMain(raw string) {
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil || len(args) == 0 {
		os.Exit(9)
	}
	root := os.Getenv(pageFlagRootEnv)
	if dir, err := ctl.Dir(); root == "" || err != nil || !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		os.Exit(pageFlagNoCtlDir)
	}
	switch args[0] {
	case "open", "pair":
		os.Exit(runOpen(args[0], args[1:], os.Stdin, os.Stdout, os.Stderr))
	case "attach":
		os.Exit(attach(args[1:], os.Stdin, os.Stdout, os.Stderr))
	case "cdp":
		os.Exit(runCDP("", args[1:], agentIO{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}))
	case "prompt":
		os.Exit(runAgent("prompt", "", args[1:], agentIO{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}))
	default:
		os.Exit(runDefault(args, os.Stdout, os.Stderr))
	}
}

var originFlagKernelPickMu sync.Mutex

func originFlagHoldDefaultKernel(t *testing.T) (release func()) {
	t.Helper()
	addr := net.JoinHostPort(kernelin.BindHost, strconv.Itoa(kernelin.DefaultPort))
	ln, err := net.Listen("tcp4", addr)
	if err != nil && runtime.GOOS == "darwin" {
		wild := net.JoinHostPort(kernelin.WildHost, strconv.Itoa(kernelin.DefaultPort))
		ln, err = net.Listen("tcp4", wild)
	}
	if err != nil {
		return func() {}
	}
	return func() { _ = ln.Close() }
}

func originFlagEnsurePickFallback(t *testing.T) (release func()) {
	t.Helper()
	originFlagKernelPickMu.Lock()
	held := originFlagHoldDefaultKernel(t)
	var msgs []string
	_ = kernelin.Pick(kernelin.DefaultPort, func(format string, args ...any) {
		msgs = append(msgs, fmt.Sprintf(format, args...))
	})
	if len(msgs) == 0 {
		held()
		originFlagKernelPickMu.Unlock()
		t.Fatalf("precondition: kernelin.Pick(%d) printed no fallback diagnostic (DefaultPort is free and bindable; cannot observe Pick-before-check)", kernelin.DefaultPort)
	}
	return func() {
		held()
		originFlagKernelPickMu.Unlock()
	}
}

func originFlagChild(t *testing.T, args ...string) pageFlagChildResult {
	t.Helper()
	return pageFlagSpawn(t, pageFlagSpawnOptions{}, args...)
}

func TestOriginFlagRefusalHappensBeforeListen(t *testing.T) {
	if raw := os.Getenv(pageFlagChildEnv); raw != "" {
		originFlagChildMain(raw)
		return
	}
	cdp := "http://127.0.0.1:1"
	refuse := func(prefix, origin string) string {
		return prefix + originFlagRefusal(origin) + "\n"
	}
	for _, c := range []struct {
		name, want string
		code       int
		args       []string
		pick       bool
	}{
		{"AttachPilot", refuse("slicc: ", "https://lease-pilot.sliccy.ai"), 2, []string{"attach", "--cdp", cdp, "--origin", "https://lease-pilot.sliccy.ai", "--no-kernel"}, false},
		{"AttachVault", refuse("slicc: ", "https://vault.sliccy.ai"), 2, []string{"attach", "--cdp", cdp, "--origin", "https://vault.sliccy.ai", "--no-kernel"}, false},
		{"AttachDeeper", refuse("slicc: ", "https://a.lease-pilot.sliccy.ai"), 2, []string{"attach", "--cdp", cdp, "--origin", "https://a.lease-pilot.sliccy.ai", "--no-kernel"}, false},
		{"AttachApex", refuse("slicc: ", "https://sliccy.ai"), 2, []string{"attach", "--cdp", cdp, "--origin", "https://sliccy.ai", "--no-kernel"}, false},
		{"AttachPilotWS", refuse("slicc: ", "ws://lease-pilot.sliccy.ai"), 2, []string{"attach", "--cdp", cdp, "--origin", "ws://lease-pilot.sliccy.ai", "--no-kernel"}, false},
		{"AttachBeforePick", refuse("slicc: ", "https://lease-pilot.sliccy.ai"), 2, []string{"attach", "--cdp", cdp, "--origin", "https://lease-pilot.sliccy.ai"}, true},
		{"CdpPilot", refuse("slicc cdp: ", "https://lease-pilot.sliccy.ai"), 2, []string{"cdp", "--cdp", cdp, "--origin", "https://lease-pilot.sliccy.ai", "--port", "0"}, false},
		{"CdpVault", refuse("slicc cdp: ", "https://vault.sliccy.ai"), 2, []string{"cdp", "--cdp", cdp, "--origin", "https://vault.sliccy.ai", "--port", "0"}, false},
		{"CdpDeeper", refuse("slicc cdp: ", "https://a.lease-pilot.sliccy.ai"), 2, []string{"cdp", "--cdp", cdp, "--origin", "https://a.lease-pilot.sliccy.ai", "--port", "0"}, false},
		{"CdpApex", refuse("slicc cdp: ", "https://sliccy.ai"), 2, []string{"cdp", "--cdp", cdp, "--origin", "https://sliccy.ai", "--port", "0"}, false},
		{"CdpVaultWSS", refuse("slicc cdp: ", "wss://vault.sliccy.ai"), 2, []string{"cdp", "--cdp", cdp, "--origin", "wss://vault.sliccy.ai", "--port", "0"}, false},
		{"AgentPilot", refuse("slicc prompt: ", "https://lease-pilot.sliccy.ai"), 1, []string{"prompt", "--cdp", cdp, "--origin", "https://lease-pilot.sliccy.ai", "hi"}, false},
		{"AgentVault", refuse("slicc prompt: ", "https://vault.sliccy.ai"), 1, []string{"prompt", "--cdp", cdp, "--origin", "https://vault.sliccy.ai", "hi"}, false},
		{"AgentDeeper", refuse("slicc prompt: ", "https://a.lease-pilot.sliccy.ai"), 1, []string{"prompt", "--cdp", cdp, "--origin", "https://a.lease-pilot.sliccy.ai", "hi"}, false},
		{"AgentApex", refuse("slicc prompt: ", "https://sliccy.ai"), 1, []string{"prompt", "--cdp", cdp, "--origin", "https://sliccy.ai", "hi"}, false},
		{"AgentDeeperFTP", refuse("slicc prompt: ", "ftp://a.lease-pilot.sliccy.ai"), 1, []string{"prompt", "--cdp", cdp, "--origin", "ftp://a.lease-pilot.sliccy.ai", "hi"}, false},
		{"OpenPilot", refuse("slicc open: ", "https://lease-pilot.sliccy.ai"), 2, []string{"open", "--page", "https://seven.sliccy.ai/", "--origin", "https://lease-pilot.sliccy.ai", "--no-kernel", "--no-jerk"}, false},
		{"OpenVault", refuse("slicc open: ", "https://vault.sliccy.ai"), 2, []string{"open", "--page", "https://seven.sliccy.ai/", "--origin", "https://vault.sliccy.ai", "--no-kernel", "--no-jerk"}, false},
		{"OpenDeeper", refuse("slicc open: ", "https://a.lease-pilot.sliccy.ai"), 2, []string{"open", "--page", "https://seven.sliccy.ai/", "--origin", "https://a.lease-pilot.sliccy.ai", "--no-kernel", "--no-jerk"}, false},
		{"OpenApex", refuse("slicc open: ", "https://sliccy.ai"), 2, []string{"open", "--page", "https://seven.sliccy.ai/", "--origin", "https://sliccy.ai", "--no-kernel", "--no-jerk"}, false},
		{"OpenApexFTP", refuse("slicc open: ", "ftp://sliccy.ai"), 2, []string{"open", "--page", "https://seven.sliccy.ai/", "--origin", "ftp://sliccy.ai", "--no-kernel", "--no-jerk"}, false},
		{"DefaultPilot", refuse("slicc: ", "https://lease-pilot.sliccy.ai"), 2, []string{"--plain", "--no-fallback", "--page", "https://seven.sliccy.ai/", "--origin", "https://lease-pilot.sliccy.ai", "--no-kernel", "--no-jerk"}, false},
		{"DefaultVault", refuse("slicc: ", "https://vault.sliccy.ai"), 2, []string{"--plain", "--no-fallback", "--page", "https://seven.sliccy.ai/", "--origin", "https://vault.sliccy.ai", "--no-kernel", "--no-jerk"}, false},
		{"DefaultDeeper", refuse("slicc: ", "https://a.lease-pilot.sliccy.ai"), 2, []string{"--plain", "--no-fallback", "--page", "https://seven.sliccy.ai/", "--origin", "https://a.lease-pilot.sliccy.ai", "--no-kernel", "--no-jerk"}, false},
		{"DefaultApex", refuse("slicc: ", "https://sliccy.ai"), 2, []string{"--plain", "--no-fallback", "--page", "https://seven.sliccy.ai/", "--origin", "https://sliccy.ai", "--no-kernel", "--no-jerk"}, false},
		{"DefaultPilotWS", refuse("slicc: ", "ws://lease-pilot.sliccy.ai"), 2, []string{"--plain", "--no-fallback", "--page", "https://seven.sliccy.ai/", "--origin", "ws://lease-pilot.sliccy.ai", "--no-kernel", "--no-jerk"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !c.pick {
				t.Parallel()
			}
			var r pageFlagChildResult
			if c.pick {
				r = pageFlagSpawn(t, pageFlagSpawnOptions{pickBefore: true}, c.args...)
			} else {
				r = originFlagChild(t, c.args...)
			}
			if r.code != c.code || r.stderr != c.want || len(r.home) != 0 || len(r.runtime) != 0 {
				t.Fatalf("slicc %q: exit %d, home %q, runtime %q, stderr %q; want exit %d, no files and %q", c.args, r.code, r.home, r.runtime, r.stderr, c.code, c.want)
			}
			if c.pick && (strings.Contains(r.stderr, "kernel services use port") || strings.Contains(r.stderr, "cannot listen on")) {
				t.Fatalf("slicc %q ran kernelin.Pick before the origin check: %q", c.args, r.stderr)
			}
		})
	}
}

func TestOriginFlagStillAcceptsCommands(t *testing.T) {
	if raw := os.Getenv(pageFlagChildEnv); raw != "" {
		originFlagChildMain(raw)
		return
	}
	cdp := "http://127.0.0.1:1"
	for _, c := range []struct {
		name string
		args []string
		open bool
		code int
	}{
		{"AttachSeven", []string{"attach", "--cdp", cdp, "--origin", "https://seven.sliccy.ai", "--no-kernel"}, false, 1},
		{"AttachNonSliccy", []string{"attach", "--cdp", cdp, "--origin", "https://example.com", "--no-kernel"}, false, 1},
		{"AttachChromeExtension", []string{"attach", "--cdp", cdp, "--origin", "chrome-extension://" + originFlagExtID, "--no-kernel"}, false, 1},
		{"AttachWSNonZone", []string{"attach", "--cdp", cdp, "--origin", "ws://example.com", "--no-kernel"}, false, 1},
		{"CdpSeven", []string{"cdp", "--cdp", cdp, "--origin", "https://seven.sliccy.ai", "--port", "0"}, false, 1},
		{"CdpLocalhost", []string{"cdp", "--cdp", cdp, "--origin", "http://localhost:3000", "--port", "0"}, false, 1},
		{"AgentWWW", []string{"prompt", "--cdp", cdp, "--origin", "https://www.sliccy.ai", "hi"}, false, 1},
		{"AgentFile", []string{"prompt", "--cdp", cdp, "--origin", "file://localhost", "hi"}, false, 1},
		{"AgentDevtools", []string{"prompt", "--cdp", cdp, "--origin", "devtools://devtools", "hi"}, false, 1},
		{"OpenSevenOrigin", []string{"open", "--page", "https://seven.sliccy.ai/", "--origin", "https://seven.sliccy.ai", "--no-kernel", "--no-jerk"}, true, 0},
		{"OpenNonSliccy", []string{"open", "--page", "https://seven.sliccy.ai/", "--origin", "https://example.com", "--no-kernel", "--no-jerk"}, true, 0},
		{"OpenChromeExtension", []string{"open", "--page", "https://seven.sliccy.ai/", "--origin", "chrome-extension://" + originFlagExtID, "--no-kernel", "--no-jerk"}, true, 0},
		{"DefaultWSNonZone", []string{"--plain", "--no-fallback", "--page", "https://seven.sliccy.ai/", "--origin", "ws://example.com", "--no-kernel", "--no-jerk"}, false, 1},
		{"DefaultLocalhost", []string{"--plain", "--no-fallback", "--page", "https://seven.sliccy.ai/", "--origin", "http://localhost:3000", "--no-kernel", "--no-jerk"}, false, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if c.open {
				pageFlagRanUntilEnded(t, pageFlagSpawn(t, pageFlagSpawnOptions{killAfter: 3 * time.Second}, c.args...), c.args)
				return
			}
			r := originFlagChild(t, c.args...)
			if strings.Contains(r.stderr, "is on a protected host and is not a seven page") {
				t.Fatalf("slicc %q refused a control origin: exit %d stderr %q", c.args, r.code, r.stderr)
			}
			if r.code != c.code {
				t.Fatalf("slicc %q: exit %d stderr %q; want exit %d after accepting the origin", c.args, r.code, r.stderr, c.code)
			}
			if strings.HasPrefix(c.name, "Cdp") && !strings.Contains(r.stderr, "cdp: discover") {
				t.Fatalf("slicc %q: exit %d stderr %q; want cdp: discover for the absent endpoint", c.args, r.code, r.stderr)
			}
		})
	}
}
