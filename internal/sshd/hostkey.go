package sshd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/crypto/ssh"
)

const hostKeyFile = "ssh_host_ed25519_key"

func HostKeyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "slicc", hostKeyFile), nil
}

func DefaultHostKey() (ssh.Signer, error) {
	path, err := HostKeyPath()
	if err != nil {
		return nil, err
	}
	return LoadHostKey(path)
}

func LoadHostKey(path string) (ssh.Signer, error) {
	signer, err := readHostKey(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return signer, err
	}
	if err := createHostKey(path); err != nil {
		return nil, err
	}
	return readHostKey(path)
}

func AuthorizedKey(key ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func readHostKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := tightenMode(path); err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("sshd: host key %s: %w", path, err)
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		return nil, fmt.Errorf("sshd: host key %s is %s, not ed25519", path, signer.PublicKey().Type())
	}
	return signer, nil
}

func tightenMode(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	return os.Chmod(path, 0o600)
}

func createHostKey(path string) error {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(key, "slicc host key")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, hostKeyFile+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(pem.EncodeToMemory(block)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return os.Rename(tmp.Name(), path)
	}
	return nil
}
