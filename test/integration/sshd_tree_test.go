package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/sshd"
	"golang.org/x/crypto/ssh"
)

func heartbeat(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func waitBeating(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	first := ""
	for time.Now().Before(deadline) {
		now := heartbeat(path)
		if first == "" {
			first = now
		} else if now != "" && now != first {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the grandchild never started beating at %s", path)
}

func waitStopped(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		before := heartbeat(path)
		time.Sleep(time.Second)
		if heartbeat(path) == before {
			return
		}
	}
	t.Fatalf("the grandchild is still beating at %s", path)
}

func startTree(t *testing.T, f *sshFixture) (*ssh.Client, *ssh.Session, string) {
	t.Helper()
	beat := filepath.Join(t.TempDir(), "beat")
	client := f.client(t)
	s, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start("spawn " + beat + "; sleep 120"); err != nil {
		t.Fatal(err)
	}
	waitBeating(t, beat)
	return client, s, beat
}

func waitDone(t *testing.T, s *ssh.Session) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(20 * time.Second):
		t.Fatal("the session did not end")
		return nil
	}
}

func TestSSHKillEndsTheWholeProcessTree(t *testing.T) {
	f := startRunnerSSHD(t)
	_, s, beat := startTree(t, f)
	if err := s.Signal(ssh.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, s); err == nil {
		t.Fatal("a killed command exited 0")
	}
	waitStopped(t, beat)
}

func TestSSHDisconnectEndsTheWholeProcessTree(t *testing.T) {
	f := startRunnerSSHD(t)
	client, _, beat := startTree(t, f)
	_ = client.Close()
	waitStopped(t, beat)
}

func TestSSHServerCloseEndsRunningSessions(t *testing.T) {
	f := startRunnerSSHD(t)
	client, s, beat := startTree(t, f)
	_ = f.srv.Close()
	_ = waitDone(t, s)
	waitStopped(t, beat)
	if _, err := client.NewSession(); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("new session after Close: %v", err)
	}
}

func TestSSHSessionEndEndsLeftoverProcessesOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("job objects are Windows only; on Unix a background job outlives the session")
	}
	f := startRunnerSSHD(t)
	beat := filepath.Join(t.TempDir(), "beat")
	s := f.session(t)
	if err := s.Start("spawn " + beat); err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, s); err != nil {
		t.Fatal(err)
	}
	waitStopped(t, beat)
}

func TestSFTPDefaultIsOnlyForPlainShells(t *testing.T) {
	cases := map[string]bool{
		"bash -c":                           true,
		"/bin/sh -c":                        true,
		"/usr/bin/zsh -c":                   true,
		"fish -c":                           true,
		"pwsh -Command":                     true,
		"powershell.exe -NoLogo -c":         true,
		"C:\\Windows\\System32\\cmd.exe /c": true,
		"CMD.EXE /c":                        true,
		"docker exec -i box sh -c":          false,
		"ssh other-box":                     false,
		"python3 -c":                        false,
		"dash -c":                           false,
		"":                                  false,
	}
	for runner, want := range cases {
		if got := sshd.SFTPDefault(strings.Fields(runner)); got != want {
			t.Errorf("SFTPDefault(%q) = %v, want %v", runner, got, want)
		}
	}
}
