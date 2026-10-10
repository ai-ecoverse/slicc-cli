package tray

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	certFileName  = "tray-certs.json"
	certLifetime  = 20 * 365 * 24 * time.Hour
	certRenewSlop = 24 * time.Hour
	certCommon    = "slicc"
)

type Certs struct {
	path string
	mu   sync.Mutex
}

type certFile struct {
	Version int         `json:"version"`
	Hubs    []certEntry `json:"hubs"`
}

type certEntry struct {
	Origin    string `json:"origin"`
	PEM       string `json:"pem"`
	Leader    string `json:"leader,omitempty"`
	CreatedAt string `json:"createdAt"`
}

func DefaultCertsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, stateDirName, certFileName), nil
}

func OpenDefaultCerts() (*Certs, error) {
	path, err := DefaultCertsPath()
	if err != nil {
		return nil, err
	}
	return OpenCerts(path), nil
}

func OpenCerts(path string) *Certs { return &Certs{path: path} }

func (c *Certs) Path() string { return c.path }

func HubOrigin(joinURL string) string {
	u, err := ParseJoinURL(joinURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + ":" + effectivePort(u)
}

func (c *Certs) Load(origin string) (pemData string, created bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.load()
	for _, e := range f.Hubs {
		if e.Origin == origin && certUsable(e.PEM) {
			return e.PEM, false, nil
		}
	}
	pemData, err = newCertPEM()
	if err != nil {
		return "", false, err
	}
	hubs := []certEntry{{Origin: origin, PEM: pemData, CreatedAt: time.Now().UTC().Format(time.RFC3339)}}
	for _, e := range f.Hubs {
		if e.Origin != origin {
			hubs = append(hubs, e)
		}
	}
	if err := writePrivateJSON(c.path, certFile{Version: stateVersion, Hubs: hubs}); err != nil {
		return "", false, err
	}
	return pemData, true, nil
}

func (c *Certs) Leader(origin string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.load().Hubs {
		if e.Origin == origin {
			return e.Leader
		}
	}
	return ""
}

func (c *Certs) SetLeader(origin, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.load()
	for i, e := range f.Hubs {
		if e.Origin == origin {
			if e.Leader == name {
				return nil
			}
			f.Hubs[i].Leader = name
			return writePrivateJSON(c.path, f)
		}
	}
	return nil
}

func (c *Certs) Delete(origin string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.load()
	hubs := f.Hubs[:0]
	for _, e := range f.Hubs {
		if e.Origin != origin {
			hubs = append(hubs, e)
		}
	}
	if len(hubs) == len(f.Hubs) {
		return nil
	}
	return writePrivateJSON(c.path, certFile{Version: stateVersion, Hubs: hubs})
}

func (c *Certs) load() certFile {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return certFile{Version: stateVersion}
	}
	var f certFile
	if json.Unmarshal(data, &f) != nil || f.Version != stateVersion {
		return certFile{Version: stateVersion}
	}
	return f
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
		Subject:      pkix.Name{CommonName: certCommon},
		Issuer:       pkix.Name{CommonName: certCommon},
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
