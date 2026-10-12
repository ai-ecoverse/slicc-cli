package integration

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/sshd"
	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
)

type sshNode struct {
	p     *page
	a     *attachProc
	home  string
	relay string
	hello struct {
		Caps       []string `json:"caps"`
		SSHHostKey string   `json:"sshHostKey"`
	}
}

func homeEnv(home string) []string {
	return []string{
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"AppData=" + filepath.Join(home, "AppData", "Roaming"), "SLICC_NO_UPDATE_CHECK=1", "SLICC_NO_TELEMETRY=1",
	}
}

func startSSHNode(t *testing.T, b *chromeProc, srv *httptest.Server, args ...string) *sshNode {
	t.Helper()
	home := t.TempDir()
	return startSSHNodeEnv(t, b, srv, home, homeEnv(home), args...)
}

func startSSHNodeEnv(t *testing.T, b *chromeProc, srv *httptest.Server, home string, env []string, args ...string) *sshNode {
	t.Helper()
	n := &sshNode{home: home}
	n.a = startAttachEnv(t, true, env, append([]string{"--signal", "stdio", "--name", "it-ssh"}, args...)...)
	n.p = b.openPage(t, srv.URL+"/index.html")
	relayStdio(t, n.p, n.a)
	n.p.waitFor("linkTest.status().up", 30*time.Second)
	n.p.eval("linkTest.hello()", &n.hello)
	n.relay = sshRelay(t, n.p)
	return n
}

func sshRelay(t *testing.T, p *page) string {
	t.Helper()
	var mu sync.Mutex
	waiting := map[string]chan wsConn{}
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ch := waiting[r.URL.Path]
		mu.Unlock()
		if ch == nil {
			http.NotFound(w, r)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c.SetReadLimit(-1)
		done := make(chan struct{})
		ch <- wsConn{c: c, done: done}
		<-done
	}))
	t.Cleanup(ws.CloseClientConnections)
	t.Cleanup(ws.Close)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	next := 0
	go func() {
		for {
			tcp, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			next++
			path := "/" + strconv.Itoa(next)
			ch := make(chan wsConn, 1)
			waiting[path] = ch
			mu.Unlock()
			go bridgeOne(p, tcp, "ws"+strings.TrimPrefix(ws.URL, "http")+path, ch)
		}
	}()
	return ln.Addr().String()
}

type wsConn struct {
	c    *websocket.Conn
	done chan struct{}
}

func bridgeOne(p *page, tcp net.Conn, url string, ch chan wsConn) {
	defer func() { _ = tcp.Close() }()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var res map[string]any
		_ = p.session.Evaluate(ctx, "linkTest.bridge({kind: 'ssh'}, "+jsArgs(url)+")", cdp.EvaluateOptions{AwaitPromise: true, ReturnByValue: true}, &res)
		if res["opened"] == nil {
			close(ch)
		}
	}()
	var w wsConn
	select {
	case w = <-ch:
	case <-time.After(30 * time.Second):
	}
	if w.c == nil {
		return
	}
	defer close(w.done)
	conn := websocket.NetConn(context.Background(), w.c, websocket.MessageBinary)
	defer func() { _ = conn.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(conn, tcp); done <- struct{}{} }()
	go func() { _, _ = io.Copy(tcp, conn); done <- struct{}{} }()
	<-done
}

func (n *sshNode) fixture(t *testing.T) *sshFixture {
	t.Helper()
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(n.hello.SSHHostKey))
	if err != nil {
		t.Fatalf("HELLO sshHostKey %q: %v", n.hello.SSHHostKey, err)
	}
	return &sshFixture{addr: n.relay, key: key}
}

func runnerArgs(extra ...string) []string {
	return append(append([]string{"--runner"}, helperRunner()...), append([]string{"--"}, extra...)...)
}

func hostKeyFile(t *testing.T, home string) string {
	t.Helper()
	var matches []string
	_ = filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Name() == "ssh_host_ed25519_key" {
			matches = append(matches, path)
		}
		return nil
	})
	if len(matches) != 1 {
		t.Fatalf("host key files under %s: %v", home, matches)
	}
	return matches[0]
}

func TestLinkSSH(t *testing.T) {
	requireBinary(t, "ssh")
	b := launchBrowser(t)
	srv := pageServer(t)
	echo := serveTCP(t, func(c net.Conn) {
		_, _ = io.Copy(c, c)
		_ = c.(*net.TCPConn).CloseWrite()
	})
	_, echoPort := hostPort(t, echo)
	on := startSSHNode(t, b, srv, runnerArgs("--sftp")...)
	off := startSSHNode(t, b, srv, runnerArgs("--deny", "127.0.0.1/32:"+strconv.Itoa(echoPort))...)
	none := startSSHNode(t, b, srv)
	t.Run("HELLO carries the host key ssh verifies", func(t *testing.T) { testLinkSSHHello(t, on) })
	t.Run("exec, exit codes and stdin", func(t *testing.T) { testLinkSSHExec(t, on) })
	t.Run("pty", func(t *testing.T) { testLinkSSHPTY(t, on) })
	t.Run("sftp on", func(t *testing.T) { testLinkSFTP(t, on, true) })
	t.Run("sftp off", func(t *testing.T) { testLinkSFTP(t, off, false) })
	t.Run("direct-tcpip allowed by policy", func(t *testing.T) { testLinkForward(t, on, echo, true) })
	t.Run("direct-tcpip denied by policy", func(t *testing.T) { testLinkForward(t, off, echo, false) })
	t.Run("no runner", func(t *testing.T) { testLinkNoRunner(t, none) })
}

func testLinkSSHHello(t *testing.T, n *sshNode) {
	n.a.waitStderr(t, "slicc: ssh: runner ", 5*time.Second)
	found := false
	for _, c := range n.hello.Caps {
		found = found || c == "ssh"
	}
	if !found {
		t.Fatalf("caps %v", n.hello.Caps)
	}
	signer, err := sshd.LoadHostKey(hostKeyFile(t, n.home))
	if err != nil {
		t.Fatal(err)
	}
	if got := sshd.AuthorizedKey(signer.PublicKey()); got != n.hello.SSHHostKey {
		t.Fatalf("HELLO %q, key file %q", n.hello.SSHHostKey, got)
	}
	f := n.fixture(t)
	out, errOut, code := systemSSH(t, f, n.hello.SSHHostKey, "", "echo over the link")
	if code != 0 || strings.ReplaceAll(out, "\r", "") != "over the link\n" {
		t.Fatalf("code %d out %q stderr %q", code, out, errOut)
	}
	other, err := sshd.LoadHostKey(filepath.Join(t.TempDir(), "other"))
	if err != nil {
		t.Fatal(err)
	}
	_, errOut, code = systemSSH(t, f, sshd.AuthorizedKey(other.PublicKey()), "", "echo should not run")
	if code != 255 || !strings.Contains(strings.ToLower(errOut), "host key") {
		t.Fatalf("wrong key: code %d stderr %q", code, errOut)
	}
}

func testLinkSSHExec(t *testing.T, n *sshNode) {
	f := n.fixture(t)
	if _, errOut, code := systemSSH(t, f, n.hello.SSHHostKey, "", "stderr to the side; exit 9"); code != 9 || !strings.Contains(errOut, "to the side") {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
	input := strings.Repeat("stdin through the link\n", 20000)
	out, errOut, code := systemSSH(t, f, n.hello.SSHHostKey, input, "cat")
	if code != 0 || strings.ReplaceAll(out, "\r", "") != input {
		t.Fatalf("code %d, %d bytes back of %d, stderr %q", code, len(out), len(input), errOut)
	}
}

func testLinkSSHPTY(t *testing.T, n *sshNode) {
	out, errOut, code := systemSSH(t, n.fixture(t), n.hello.SSHHostKey, "", "-tt", "tty; size; exit 6")
	text := ansi.ReplaceAllString(out, "")
	if code != 6 || !strings.Contains(text, "tty=true") || !strings.Contains(text, "size=") {
		t.Fatalf("code %d out %q stderr %q", code, out, errOut)
	}
}

func testLinkSFTP(t *testing.T, n *sshNode, on bool) {
	sftpBin := requireBinary(t, "sftp")
	host, port, _ := net.SplitHostPort(n.relay)
	work := t.TempDir()
	known := filepath.Join(work, "known_hosts")
	if err := os.WriteFile(known, []byte("["+host+"]:"+port+" "+n.hello.SSHHostKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "up.txt"), []byte("up through the link\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(n.home, "down.txt"), []byte("down through the link\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sftpBin, "-F", "none", "-b", "-",
		"-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+known, "-o", "GlobalKnownHostsFile="+known,
		"-o", "BatchMode=yes", "-P", port, "anyone@"+host)
	cmd.Dir = work
	cmd.Stdin = strings.NewReader("put up.txt\nget down.txt\n")
	out, err := cmd.CombinedOutput()
	if !on {
		if err == nil || !strings.Contains(strings.ToLower(string(out)), "subsystem") {
			t.Fatalf("sftp worked with sftp off: %v\n%s", err, out)
		}
		return
	}
	if err != nil {
		t.Fatalf("sftp: %v\n%s", err, out)
	}
	if got, err := os.ReadFile(filepath.Join(n.home, "up.txt")); err != nil || string(got) != "up through the link\n" {
		t.Fatalf("put: %q %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(work, "down.txt")); err != nil || string(got) != "down through the link\n" {
		t.Fatalf("get: %q %v", got, err)
	}
}

func testLinkForward(t *testing.T, n *sshNode, echo string, allowed bool) {
	out, errOut, code := systemSSH(t, n.fixture(t), n.hello.SSHHostKey, "forwarded bytes\n", "-W", echo)
	if allowed {
		if code != 0 || out != "forwarded bytes\n" {
			t.Fatalf("code %d out %q stderr %q", code, out, errOut)
		}
		return
	}
	if code == 0 || !strings.Contains(errOut, "administratively prohibited") || !strings.Contains(errOut, "policy denies") {
		t.Fatalf("code %d out %q stderr %q", code, out, errOut)
	}
}

func testLinkNoRunner(t *testing.T, n *sshNode) {
	for _, c := range n.hello.Caps {
		if c == "ssh" {
			t.Fatalf("caps %v", n.hello.Caps)
		}
	}
	if n.hello.SSHHostKey != "" {
		t.Fatalf("sshHostKey %q without a runner", n.hello.SSHHostKey)
	}
	var r echoResult
	n.p.eval("linkTest.open({kind: 'ssh'})", &r)
	if r.Reset != "EOPNOTSUPP" {
		t.Fatalf("got %+v", r)
	}
}

func TestAttachComputeFlagErrors(t *testing.T) {
	cases := map[string][]string{
		"--shell needs --runner":                {"--signal", "stdio", "--shell", "sh"},
		"--runner needs a command":              {"--signal", "stdio", "--runner"},
		"--runner needs a command, for example": {"--signal", "stdio", "--runner", "--"},
		"separate words":                        {"--signal", "stdio", "--runner=bash -c"},
		"--runner given twice":                  {"--signal", "stdio", "--runner", "sh", "-c", "--", "--runner", "bash", "-c"},
		"exclude each other":                    {"--signal", "stdio", "--runner", "sh", "-c", "--", "--sftp", "--no-sftp"},
		"--eval-quiet needs --eval":             {"--signal", "stdio", "--runner", "sh", "-c", "--", "--eval-quiet", "1s"},
		"--sftp needs --runner":                 {"--signal", "stdio", "--sftp"},
	}
	for want, args := range cases {
		_, errOut, code := slicc(t, append([]string{"attach"}, args...)...)
		if code != 2 || !strings.Contains(errOut, want) {
			t.Errorf("%v: code %d, stderr %q", args, code, errOut)
		}
	}
	out, _, code := slicc(t, "attach", "--help")
	if code != 0 || !strings.Contains(out, "--runner <argv…>") || !strings.Contains(out, "--no-sftp") {
		t.Fatalf("help: %d %q", code, out)
	}
}
