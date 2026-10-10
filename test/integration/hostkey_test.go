package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/sshd"
	"golang.org/x/crypto/ssh"
)

func TestHostKeyIsCreatedOnceAndKeptPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "slicc", "ssh_host_ed25519_key")
	first, err := sshd.LoadHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("type %s", first.PublicKey().Type())
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v err %v", info.Mode(), err)
		}
	}
	second, err := sshd.LoadHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.PublicKey().Marshal(), second.PublicKey().Marshal()) {
		t.Fatal("the host key changed between loads")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestHostKeyPermissionsAreTightened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	path := filepath.Join(t.TempDir(), "ssh_host_ed25519_key")
	if _, err := sshd.LoadHostKey(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sshd.LoadHostKey(path); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestHostKeyRejectsAGarbledFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh_host_ed25519_key")
	if err := os.WriteFile(path, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sshd.LoadHostKey(path); err == nil {
		t.Fatal("garbled key accepted")
	}
	if data, _ := os.ReadFile(path); string(data) != "not a key" {
		t.Fatal("garbled key was overwritten")
	}
}

func TestDefaultHostKeyLivesInTheUserConfigDir(t *testing.T) {
	home := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", home)
	case "darwin", "ios":
		t.Setenv("HOME", home)
	default:
		t.Setenv("XDG_CONFIG_HOME", home)
	}
	path, err := sshd.HostKeyPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, home) || filepath.Base(path) != "ssh_host_ed25519_key" || filepath.Base(filepath.Dir(path)) != "slicc" {
		t.Fatalf("path %q", path)
	}
	signer, err := sshd.DefaultHostKey()
	if err != nil {
		t.Fatal(err)
	}
	again, err := sshd.LoadHostKey(path)
	if err != nil || !bytes.Equal(signer.PublicKey().Marshal(), again.PublicKey().Marshal()) {
		t.Fatalf("default key not persisted at %s: %v", path, err)
	}
	line := sshd.AuthorizedKey(signer.PublicKey())
	parsed, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || len(rest) != 0 || !strings.HasPrefix(line, "ssh-ed25519 AAAA") || strings.ContainsAny(line, "\r\n") {
		t.Fatalf("authorized key %q: %v", line, err)
	}
	if !bytes.Equal(parsed.Marshal(), signer.PublicKey().Marshal()) {
		t.Fatal("authorized key does not round-trip")
	}
}
