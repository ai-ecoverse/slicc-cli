package integration

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/sshd"
)

func TestFollowServesSSHWithARunner(t *testing.T) {
	requireBinary(t, "ssh")
	env, h, trays, p, _ := followSetup(t, 1)
	echo := serveTCP(t, func(c net.Conn) {
		_, _ = io.Copy(c, c)
		_ = c.(*net.TCPConn).CloseWrite()
	})
	fp := startFollow(t, env, h.joinURL(trays[0]), append(runnerArgs(), "--name", "box")...)
	fp.waitStderr(t, "slicc: ssh: runner ", 10*time.Second)
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.waitUp(t, "")
	n := &sshNode{p: p, relay: sshRelay(t, p)}
	p.eval("linkTest.hello()", &n.hello)
	if !strings.Contains(strings.Join(n.hello.Caps, ","), "ssh") || !strings.HasPrefix(n.hello.SSHHostKey, "ssh-ed25519 ") {
		t.Fatalf("HELLO %+v", n.hello)
	}
	signer, err := sshd.LoadHostKey(filepath.Join(configDir(t), "slicc", "ssh_host_ed25519_key"))
	if err != nil || sshd.AuthorizedKey(signer.PublicKey()) != n.hello.SSHHostKey {
		t.Fatalf("host key file does not match HELLO: %v", err)
	}
	f := n.fixture(t)
	out, errOut, code := systemSSH(t, f, n.hello.SSHHostKey, "followed stdin\n", "cat; exit 3")
	if code != 3 || strings.ReplaceAll(out, "\r", "") != "followed stdin\n" {
		t.Fatalf("code %d out %q stderr %q", code, out, errOut)
	}
	_, errOut, code = systemSSH(t, f, n.hello.SSHHostKey, "", "-W", echo)
	if code == 0 || !strings.Contains(errOut, "administratively prohibited") || !strings.Contains(errOut, "policy denies") {
		t.Fatalf("remote policy let ssh -W reach loopback: code %d stderr %q", code, errOut)
	}
	if _, errOut, code = systemSSH(t, f, n.hello.SSHHostKey, "", "-s", "sftp"); code == 0 || !strings.Contains(errOut, "subsystem request failed") {
		t.Fatalf("sftp with the helper runner: code %d stderr %q", code, errOut)
	}
}

func TestFollowWithoutRunnerRefusesSSH(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]))
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.waitUp(t, "")
	var res echoResult
	p.eval("linkTest.open({kind: 'ssh'})", &res)
	if res.Reset != "EOPNOTSUPP" {
		t.Fatalf("got %+v", res)
	}
	_, errOut, code := sliccEnv(t, env, h.joinURL(trays[0]), "follow", "--eval", "python3", "-i")
	if code != 2 || !strings.Contains(errOut, "--eval needs --runner") {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
}

func configDir(t *testing.T) string {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
