package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func KeyPath(dir, vaultOrigin, sevenOrigin string) string {
	return filepath.Join(dir, "vault-"+hostPart(vaultOrigin)+"+"+hostPart(sevenOrigin)+".key")
}

func hostPart(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return "invalid"
	}
	h := u.Hostname()
	if p := u.Port(); p != "" && p != map[string]string{"https": "443", "http": "80"}[u.Scheme] {
		h += "_" + p
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, h)
}

func KeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:])[:32]
}

func LoadKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := b64.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(key) != KeySize {
		return nil, errors.New("vault: the key file holds no key")
	}
	return key, nil
}

func SaveKey(path string, key []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".vault-key-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteString(b64.EncodeToString(key) + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
