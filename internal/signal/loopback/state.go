package loopback

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	identityFileName = "loopback-identity.json"
	peersFileName    = "loopback-peers.json"
	stateVersion     = 1
	certLifetime     = 20 * 365 * 24 * time.Hour
	certRenewSlop    = 24 * time.Hour
)

func DefaultDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "slicc"), nil
}

type Identity struct {
	Certificate webrtc.Certificate
	Fingerprint string
	Created     bool
	Port        int
	path        string
	pem         string
	createdAt   string
}

type identityFile struct {
	Version   int    `json:"version"`
	PEM       string `json:"pem"`
	Port      int    `json:"port,omitempty"`
	CreatedAt string `json:"createdAt"`
}

func badIdentity(path string) error {
	return fmt.Errorf("%s is not a loopback identity slicc can read; slicc leaves it alone, move it away to mint a new one (every paired seven then pairs again)", path)
}

func readIdentity(path string) (*Identity, error) {
	var f identityFile
	data, err := readShared(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(data, &f) != nil || f.Version != stateVersion {
		return nil, badIdentity(path)
	}
	id, err := identityFrom(path, f)
	if err != nil {
		return nil, badIdentity(path)
	}
	if !certUsable(f.PEM) {
		return nil, nil
	}
	return id, nil
}

func LoadIdentity(dir string) (*Identity, error) {
	path := filepath.Join(dir, identityFileName)
	unlock, err := lockFile(path + ".lock")
	if err != nil {
		return nil, err
	}
	defer unlock()
	if id, err := readIdentity(path); id != nil || err != nil {
		return id, err
	}
	pemData, err := newCertPEM()
	if err != nil {
		return nil, err
	}
	f := identityFile{Version: stateVersion, PEM: pemData, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := writePrivateJSON(path, f); err != nil {
		return nil, err
	}
	if _, err := OpenPeers(dir).RemoveAll(); err != nil {
		return nil, err
	}
	id, err := identityFrom(path, f)
	if err != nil {
		return nil, err
	}
	id.Created = true
	return id, nil
}

func identityFrom(path string, f identityFile) (*Identity, error) {
	cert, err := webrtc.CertificateFromPEM(f.PEM)
	if err != nil {
		return nil, fmt.Errorf("the loopback certificate: %w", err)
	}
	fps, err := cert.GetFingerprints()
	if err != nil {
		return nil, fmt.Errorf("the loopback certificate: %w", err)
	}
	fp := ""
	for _, f := range fps {
		if strings.EqualFold(f.Algorithm, "sha-256") {
			fp = NormalizeFingerprint(f.Value)
		}
	}
	if fp == "" {
		return nil, errors.New("the loopback certificate has no sha-256 fingerprint")
	}
	return &Identity{Certificate: *cert, Fingerprint: fp, Port: f.Port, path: path, pem: f.PEM, createdAt: f.CreatedAt}, nil
}

func (id *Identity) RememberPort(port int) error {
	if id.Port == port {
		return nil
	}
	unlock, err := lockFile(id.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	if cur, _ := readIdentity(id.path); cur == nil || cur.Fingerprint != id.Fingerprint {
		return nil
	}
	if err := writePrivateJSON(id.path, identityFile{Version: stateVersion, PEM: id.pem, Port: port, CreatedAt: id.createdAt}); err != nil {
		return err
	}
	id.Port = port
	return nil
}

func NormalizeFingerprint(v string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), ":", ""))
}

func ShortFingerprint(fp string) string {
	if len(fp) < 8 {
		return fp
	}
	return fp[:4] + "…" + fp[len(fp)-2:]
}

func Colons(fp string) string {
	var b strings.Builder
	for i := 0; i < len(fp); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(strings.ToUpper(fp[i:min(i+2, len(fp))]))
	}
	return b.String()
}

func certUsable(pemData string) bool {
	var cert *x509.Certificate
	hasKey := false
	rest := []byte(pemData)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch block.Type {
		case "CERTIFICATE":
			cert, _ = x509.ParseCertificate(block.Bytes)
		case "PRIVATE KEY":
			_, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			hasKey = err == nil
		}
	}
	return cert != nil && hasKey && time.Now().Add(certRenewSlop).Before(cert.NotAfter)
}

func newCertPEM() (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return "", err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "slicc"},
		Issuer:       pkix.Name{CommonName: "slicc"},
		NotBefore:    now.Add(-24 * time.Hour),
		NotAfter:     now.Add(certLifetime),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	_ = pem.Encode(&b, &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return b.String(), nil
}

func writePrivateJSON(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(name, path)
}
