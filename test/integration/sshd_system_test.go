package integration

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/sshd"
)

func requireBinary(t *testing.T, name string) string {
	t.Helper()
	bin, err := exec.LookPath(name)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("no %s binary in CI: %v", name, err)
		}
		t.Skipf("no %s binary", name)
	}
	return bin
}

func systemSSH(t *testing.T, f *sshFixture, knownHost string, stdin string, args ...string) (string, string, int) {
	t.Helper()
	bin := requireBinary(t, "ssh")
	host, port, _ := net.SplitHostPort(f.addr)
	dir := t.TempDir()
	known := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(known, []byte("["+host+"]:"+port+" "+knownHost+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{
		"-F", "none",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + known,
		"-o", "GlobalKnownHostsFile=" + known,
		"-o", "BatchMode=yes",
		"-o", "UpdateHostKeys=no",
		"-o", "ConnectTimeout=10",
		"-p", port,
		"-l", "anyone",
		host,
	}
	cmd := exec.Command(bin, append(base, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("ssh hung: %s", stderr.String())
	}
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

func TestSystemSSHVerifiesTheExposedHostKey(t *testing.T) {
	f := startRunnerSSHD(t)
	key := sshd.AuthorizedKey(f.key)
	out, errOut, code := systemSSH(t, f, key, "", "echo hi from slicc")
	if code != 0 || strings.ReplaceAll(out, "\r", "") != "hi from slicc\n" {
		t.Fatalf("code %d out %q stderr %q", code, out, errOut)
	}
	other := startRunnerSSHD(t)
	_, errOut, code = systemSSH(t, f, sshd.AuthorizedKey(other.key), "", "echo should not run")
	if code != 255 || !strings.Contains(strings.ToLower(errOut), "host key") {
		t.Fatalf("wrong key: code %d stderr %q", code, errOut)
	}
}

func TestSystemSSHExitCodesAndStdin(t *testing.T) {
	f := startRunnerSSHD(t)
	key := sshd.AuthorizedKey(f.key)
	if _, errOut, code := systemSSH(t, f, key, "", "exit 9"); code != 9 {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
	out, errOut, code := systemSSH(t, f, key, "piped through ssh\n", "cat")
	if code != 0 || strings.ReplaceAll(out, "\r", "") != "piped through ssh\n" {
		t.Fatalf("code %d out %q stderr %q", code, out, errOut)
	}
}

func TestSystemSSHWithAForcedPTY(t *testing.T) {
	f := startRunnerSSHD(t)
	out, errOut, code := systemSSH(t, f, sshd.AuthorizedKey(f.key), "", "-tt", "tty; exit 6")
	if code != 6 || !strings.Contains(ansi.ReplaceAllString(out, ""), "tty=true") {
		t.Fatalf("code %d out %q stderr %q", code, out, errOut)
	}
}

func TestSystemSSHSubsystemSFTP(t *testing.T) {
	sftpBin := requireBinary(t, "sftp")
	dir := t.TempDir()
	f := startSSHD(t, sshd.Config{Runner: helperRunner(), Dir: dir})
	if err := os.WriteFile(filepath.Join(dir, "remote.txt"), []byte("remote file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(f.addr)
	work := t.TempDir()
	known := filepath.Join(work, "known_hosts")
	if err := os.WriteFile(known, []byte("["+host+"]:"+port+" "+sshd.AuthorizedKey(f.key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "local.txt"), []byte("local file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sftpBin, "-F", "none", "-b", "-",
		"-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+known, "-o", "GlobalKnownHostsFile="+known,
		"-o", "BatchMode=yes", "-P", port, "anyone@"+host)
	cmd.Dir = work
	cmd.Stdin = strings.NewReader("put local.txt\nget remote.txt\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sftp: %v\n%s", err, out)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "local.txt")); err != nil || string(got) != "local file\n" {
		t.Fatalf("put: %q %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(work, "remote.txt")); err != nil || string(got) != "remote file\n" {
		t.Fatalf("get: %q %v", got, err)
	}
}
