package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/sshd"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type sshFixture struct {
	addr string
	key  ssh.PublicKey
	srv  *sshd.Server
}

func startSSHD(t *testing.T, cfg sshd.Config) *sshFixture {
	t.Helper()
	if cfg.HostKey == nil {
		signer, err := sshd.LoadHostKey(filepath.Join(t.TempDir(), "ssh_host_ed25519_key"))
		if err != nil {
			t.Fatal(err)
		}
		cfg.HostKey = signer
	}
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	srv, err := sshd.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = srv.ServeConn(c) }()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		_ = srv.Close()
	})
	return &sshFixture{addr: ln.Addr().String(), key: srv.HostKey(), srv: srv}
}

func startRunnerSSHD(t *testing.T) *sshFixture {
	return startSSHD(t, sshd.Config{Runner: helperRunner()})
}

func (f *sshFixture) client(t *testing.T) *ssh.Client {
	t.Helper()
	c, err := ssh.Dial("tcp", f.addr, &ssh.ClientConfig{
		User:            "whoever",
		HostKeyCallback: ssh.FixedHostKey(f.key),
		Timeout:         10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (f *sshFixture) session(t *testing.T) *ssh.Session {
	t.Helper()
	s, err := f.client(t).NewSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ee *ssh.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("not an exit: %v", err)
	}
	return ee.ExitStatus()
}

func TestSSHExecRunsTheRunnerWithTheCommand(t *testing.T) {
	f := startRunnerSSHD(t)
	s := f.session(t)
	var stdout, stderr bytes.Buffer
	s.Stdout, s.Stderr = &stdout, &stderr
	err := s.Run("echo hello world; stderr oops; exit 7")
	if code := exitCode(t, err); code != 7 {
		t.Fatalf("exit %d", code)
	}
	if stdout.String() != "hello world\n" || stderr.String() != "oops\n" {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestSSHExecExitZeroAndWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	f := startSSHD(t, sshd.Config{Runner: helperRunner(), Dir: dir})
	out, err := f.session(t).Output("pwd")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	want, _ := filepath.EvalSymlinks(dir)
	if got != want {
		t.Fatalf("pwd %q, want %q", got, want)
	}
}

func TestSSHExecStreamsStdin(t *testing.T) {
	f := startRunnerSSHD(t)
	s := f.session(t)
	data := make([]byte, 1<<20)
	_, _ = rand.Read(data)
	s.Stdin = bytes.NewReader(data)
	out, err := s.Output("cat")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("got %d bytes back, sent %d", len(out), len(data))
	}
}

func TestSSHEnvRequestsReachTheCommand(t *testing.T) {
	f := startRunnerSSHD(t)
	s := f.session(t)
	if err := s.Setenv("SLICC_SSHD_TEST", "from-the-client"); err != nil {
		t.Fatal(err)
	}
	out, err := s.Output("env SLICC_SSHD_TEST")
	if err != nil || string(out) != "from-the-client\n" {
		t.Fatalf("out %q err %v", out, err)
	}
}

func TestSSHExecWithoutPTYHasNoTerminal(t *testing.T) {
	f := startRunnerSSHD(t)
	out, err := f.session(t).Output("tty")
	if err != nil || string(out) != "tty=false\n" {
		t.Fatalf("out %q err %v", out, err)
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[=>]`)

type screen struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *screen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansi.ReplaceAllString(s.buf.String(), "")
}

func (s *screen) waitFor(t *testing.T, want string, poke func()) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.text(), want) {
			return
		}
		if poke != nil {
			poke()
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no %q in %q", want, s.text())
}

func TestSSHShellWithPTYFollowsWindowChanges(t *testing.T) {
	f := startRunnerSSHD(t)
	s := f.session(t)
	if err := s.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	stdin, err := s.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out := &screen{}
	s.Stdout = out
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "ready", nil)
	ask := func() { _, _ = io.WriteString(stdin, "size\r") }
	out.waitFor(t, "size=80x24", ask)
	if err := s.WindowChange(40, 132); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "size=132x40", ask)
	_, _ = io.WriteString(stdin, "tty\r")
	out.waitFor(t, "tty=true", nil)
	_, _ = io.WriteString(stdin, "exit 5\r")
	if code := exitCode(t, s.Wait()); code != 5 {
		t.Fatalf("exit %d", code)
	}
}

func TestSSHExecWithPTY(t *testing.T) {
	f := startRunnerSSHD(t)
	s := f.session(t)
	if err := s.RequestPty("xterm", 30, 100, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	out := &screen{}
	s.Stdout = out
	if err := s.Run("tty; size; exit 4"); exitCode(t, err) != 4 {
		t.Fatalf("run: %v", err)
	}
	if text := out.text(); !strings.Contains(text, "tty=true") || !strings.Contains(text, "size=100x30") {
		t.Fatalf("output %q", text)
	}
}

type slowWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	first bool
}

func (w *slowWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.first {
		w.first = true
		time.Sleep(2 * time.Second)
	}
	return w.buf.Write(p)
}

func TestSSHPTYOutputSurvivesASlowReader(t *testing.T) {
	f := startRunnerSSHD(t)
	s := f.session(t)
	if err := s.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	out := &slowWriter{}
	s.Stdout = out
	const size = 3 << 20
	if err := s.Run("blob " + strconv.Itoa(size)); err != nil {
		t.Fatal(err)
	}
	text := ansi.ReplaceAllString(out.buf.String(), "")
	count := strings.Count(text, "x")
	exact := runtime.GOOS != "windows" && count == size
	repainted := runtime.GOOS == "windows" && count >= size
	if (!exact && !repainted) || !strings.HasSuffix(strings.TrimSpace(text), "END") {
		t.Fatalf("got %d x and tail %q", count, text[max(0, len(text)-20):])
	}
}

func TestSSHSignalStopsTheCommand(t *testing.T) {
	f := startRunnerSSHD(t)
	s := f.session(t)
	if err := s.Start("echo started; sleep 60"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := s.Signal(ssh.SIGINT); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the command survived SIGINT")
	}
	var ee *ssh.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("wait: %v", err)
	}
	if runtime.GOOS == "windows" {
		if ee.ExitStatus() == 0 {
			t.Fatalf("exit %d", ee.ExitStatus())
		}
		return
	}
	if ee.Signal() != "INT" {
		t.Fatalf("signal %q, status %d", ee.Signal(), ee.ExitStatus())
	}
}

func TestSSHExitSignalFromAShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals")
	}
	f := startSSHD(t, sshd.Config{Runner: []string{"sh", "-c"}})
	err := f.session(t).Run("kill -TERM $$")
	var ee *ssh.ExitError
	if !errors.As(err, &ee) || ee.Signal() != "TERM" {
		t.Fatalf("got %v", err)
	}
	out, err := f.session(t).Output("printf '%s' \"$((6 * 7))\"; exit 0")
	if err != nil || string(out) != "42" {
		t.Fatalf("out %q err %v", out, err)
	}
}

func TestSSHShellIsRefusedWhenTheRunnerHasNoInteractiveForm(t *testing.T) {
	exe := helperRunner()[0]
	f := startSSHD(t, sshd.Config{Runner: []string{exe, helperFlag, "--no-shell"}})
	s := f.session(t)
	var stderr bytes.Buffer
	s.Stderr = &stderr
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	if code := exitCode(t, s.Wait()); code != 1 || !strings.Contains(stderr.String(), "no interactive shell") {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
}

func TestSSHStartFailureIsReported(t *testing.T) {
	f := startSSHD(t, sshd.Config{Runner: []string{filepath.Join(t.TempDir(), "missing-runner"), "-c"}})
	s := f.session(t)
	var stderr bytes.Buffer
	s.Stderr = &stderr
	if code := exitCode(t, s.Run("true")); code != 127 || !strings.Contains(stderr.String(), "cannot start") {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
}

func TestShellForDerivesTheInteractiveForm(t *testing.T) {
	cases := map[string]string{
		"bash -c":                    "bash -l",
		"/bin/zsh -c":                "/bin/zsh -l",
		"docker exec -i box sh -c":   "docker exec -i box sh -l",
		"python3 -c":                 "python3",
		"cmd.exe /c":                 "cmd.exe",
		"pwsh -NoProfile -Command":   "pwsh -NoProfile",
		"ssh other-box":              "",
		"bash":                       "",
		"C:\\Git\\bin\\bash.exe -c":  "C:\\Git\\bin\\bash.exe -l",
		"node -e":                    "",
		"powershell.exe -NoLogo -c":  "powershell.exe -NoLogo",
		"/usr/local/bin/fish --no-c": "",
	}
	for runner, want := range cases {
		got := strings.Join(sshd.ShellFor(strings.Fields(runner)), " ")
		if got != want {
			t.Errorf("ShellFor(%q) = %q, want %q", runner, got, want)
		}
	}
}

func TestSSHNoRunnerRefusesEverySession(t *testing.T) {
	f := startSSHD(t, sshd.Config{})
	_, err := f.client(t).NewSession()
	if err == nil || !strings.Contains(err.Error(), "administratively prohibited") || !strings.Contains(err.Error(), sshd.ErrNoRunner.Error()) {
		t.Fatalf("got %v", err)
	}
}

func TestSSHSFTPPutAndGet(t *testing.T) {
	dir := t.TempDir()
	f := startSSHD(t, sshd.Config{Runner: helperRunner(), Dir: dir})
	client, err := sftp.NewClient(f.client(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	data := make([]byte, 300*1024)
	_, _ = rand.Read(data)
	w, err := client.Create("upload.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "upload.bin"))
	if err != nil || !bytes.Equal(onDisk, data) {
		t.Fatalf("put: %d bytes, err %v", len(onDisk), err)
	}
	if err := os.WriteFile(filepath.Join(dir, "download.txt"), []byte("from the host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := client.Open("download.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || string(got) != "from the host\n" {
		t.Fatalf("get %q err %v", got, err)
	}
}

func TestSSHSFTPCanBeTurnedOff(t *testing.T) {
	f := startSSHD(t, sshd.Config{Runner: helperRunner(), NoSFTP: true})
	if _, err := sftp.NewClient(f.client(t)); err == nil {
		t.Fatal("sftp worked with NoSFTP")
	}
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func TestSSHDirectTCPIPIsRefusedByDefault(t *testing.T) {
	target := echoServer(t)
	f := startRunnerSSHD(t)
	_, err := f.client(t).Dial("tcp", target)
	if err == nil || !strings.Contains(err.Error(), "administratively prohibited") {
		t.Fatalf("got %v", err)
	}
}

type recordingDialer struct {
	mu    sync.Mutex
	addrs []string
}

func (d *recordingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, network+" "+address)
	d.mu.Unlock()
	var nd net.Dialer
	return nd.DialContext(ctx, network, address)
}

func TestSSHDirectTCPIPUsesTheInjectedDialer(t *testing.T) {
	target := echoServer(t)
	dialer := &recordingDialer{}
	f := startSSHD(t, sshd.Config{Runner: helperRunner(), Dialer: dialer})
	conn, err := f.client(t).Dial("tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "ping over the link"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len("ping over the link"))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping over the link" {
		t.Fatalf("read %q err %v", buf, err)
	}
	dialer.mu.Lock()
	addrs := append([]string{}, dialer.addrs...)
	dialer.mu.Unlock()
	if len(addrs) != 1 || addrs[0] != "tcp "+target {
		t.Fatalf("dials %v", addrs)
	}
	closed := startSSHD(t, sshd.Config{Runner: helperRunner(), Dialer: dialer})
	_, err = closed.client(t).Dial("tcp", "127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "connect failed") {
		t.Fatalf("closed port: %v", err)
	}
}

func TestSSHDirectTCPIPGoesThroughThePolicyDialer(t *testing.T) {
	target := echoServer(t)
	f := startSSHD(t, sshd.Config{Runner: helperRunner(), Dialer: policy.NewDialer(policy.Remote())})
	_, err := f.client(t).Dial("tcp", target)
	if err == nil || !strings.Contains(err.Error(), "administratively prohibited") || !strings.Contains(err.Error(), "policy denies") {
		t.Fatalf("remote default: %v", err)
	}
	g := startSSHD(t, sshd.Config{Runner: helperRunner(), Dialer: policy.NewDialer(policy.Local())})
	conn, err := g.client(t).Dial("tcp", target)
	if err != nil {
		t.Fatalf("local default: %v", err)
	}
	_ = conn.Close()
}

func evalSession(t *testing.T, f *sshFixture, input string) (string, string, int) {
	t.Helper()
	ch, reqs, err := f.client(t).OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	status := make(chan int, 1)
	go func() {
		for req := range reqs {
			var msg struct{ Status uint32 }
			if req.Type == "exit-status" && ssh.Unmarshal(req.Payload, &msg) == nil {
				status <- int(msg.Status)
			}
		}
		close(status)
	}()
	ok, err := ch.SendRequest("subsystem", true, ssh.Marshal(struct{ Name string }{sshd.EvalSubsystem}))
	if err != nil || !ok {
		t.Fatalf("subsystem: %v %v", ok, err)
	}
	_, _ = io.WriteString(ch, input)
	_ = ch.CloseWrite()
	var stderr bytes.Buffer
	errDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stderr, ch.Stderr())
		close(errDone)
	}()
	stdout, _ := io.ReadAll(ch)
	<-errDone
	code, ok := <-status
	if !ok {
		t.Fatal("no exit-status")
	}
	return string(stdout), stderr.String(), code
}

func TestSSHEvalSubsystemKeepsOneREPLAcrossSessions(t *testing.T) {
	f := startSSHD(t, sshd.Config{Runner: helperRunner(), Eval: helperREPLArgv(), EvalQuiet: 300 * time.Millisecond})
	if out, errOut, code := evalSession(t, f, "set answer 42\n"); code != 0 || out != "" || errOut != "" {
		t.Fatalf("set: %q %q %d", out, errOut, code)
	}
	if out, errOut, code := evalSession(t, f, "get answer\nbogus\n"); code != 0 || out != "answer=42\n" || errOut != "repl: bogus?\n" {
		t.Fatalf("get: %q %q %d", out, errOut, code)
	}
	if _, errOut, code := evalSession(t, f, "quit\n"); code != 1 || !strings.Contains(errOut, "exited with code 3") {
		t.Fatalf("quit: %q %d", errOut, code)
	}
	if out, _, code := evalSession(t, f, "get answer\n"); code != 0 || out != "answer=\n" {
		t.Fatalf("restart: %q %d", out, code)
	}
}

func TestSSHEvalSubsystemWithoutAREPL(t *testing.T) {
	f := startRunnerSSHD(t)
	_, errOut, code := evalSession(t, f, "1\n")
	if code != 1 || !strings.Contains(errOut, "no REPL") {
		t.Fatalf("%q %d", errOut, code)
	}
}

func TestSSHUnknownSubsystemIsRefused(t *testing.T) {
	f := startRunnerSSHD(t)
	if err := f.session(t).RequestSubsystem("nope"); err == nil {
		t.Fatal("unknown subsystem accepted")
	}
}
