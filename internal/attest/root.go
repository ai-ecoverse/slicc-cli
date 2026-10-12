package attest

import (
	"crypto/ecdsa"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

//go:embed trusted_root.json
var embeddedRoot []byte

const (
	RootEnv          = "SLICC_SIGSTORE_TRUSTED_ROOT"
	rootMediaType    = "application/vnd.dev.sigstore.trustedroot+json;version=0.1"
	ecdsaP256SHA256  = "PKIX_ECDSA_P256_SHA_256"
	defaultTlogStart = "2021-01-01T00:00:00Z"
)

type validity struct {
	start, end time.Time
}

func (v validity) covers(t time.Time) bool {
	return !t.Before(v.start) && (v.end.IsZero() || !t.After(v.end))
}

type tlog struct {
	baseURL string
	keyID   []byte
	key     *ecdsa.PublicKey
	valid   validity
}

type authority struct {
	root          *x509.Certificate
	intermediates []*x509.Certificate
	valid         validity
}

type TrustedRoot struct {
	tlogs       []tlog
	authorities []authority
}

type rawValidity struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type rawRoot struct {
	MediaType string `json:"mediaType"`
	Tlogs     []struct {
		BaseURL   string `json:"baseUrl"`
		PublicKey struct {
			RawBytes   []byte      `json:"rawBytes"`
			KeyDetails string      `json:"keyDetails"`
			ValidFor   rawValidity `json:"validFor"`
		} `json:"publicKey"`
		LogID struct {
			KeyID []byte `json:"keyId"`
		} `json:"logId"`
	} `json:"tlogs"`
	CertificateAuthorities []struct {
		CertChain struct {
			Certificates []struct {
				RawBytes []byte `json:"rawBytes"`
			} `json:"certificates"`
		} `json:"certChain"`
		ValidFor rawValidity `json:"validFor"`
	} `json:"certificateAuthorities"`
}

func parseValidity(v rawValidity) (validity, error) {
	start := v.Start
	if start == "" {
		start = defaultTlogStart
	}
	var out validity
	var err error
	if out.start, err = time.Parse(time.RFC3339Nano, start); err != nil {
		return out, err
	}
	if v.End != "" {
		if out.end, err = time.Parse(time.RFC3339Nano, v.End); err != nil {
			return out, err
		}
	}
	return out, nil
}

func ParseTrustedRoot(b []byte) (*TrustedRoot, error) {
	var raw rawRoot
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("attest: the trusted root doesn't parse: %w", err)
	}
	if raw.MediaType != rootMediaType {
		return nil, fmt.Errorf("attest: the trusted root has media type %q", raw.MediaType)
	}
	r := &TrustedRoot{}
	for _, t := range raw.Tlogs {
		if t.PublicKey.KeyDetails != ecdsaP256SHA256 {
			continue
		}
		pub, err := x509.ParsePKIXPublicKey(t.PublicKey.RawBytes)
		key, ok := pub.(*ecdsa.PublicKey)
		if err != nil || !ok {
			return nil, errors.New("attest: a transparency log key in the trusted root doesn't parse")
		}
		v, err := parseValidity(t.PublicKey.ValidFor)
		if err != nil {
			return nil, err
		}
		r.tlogs = append(r.tlogs, tlog{baseURL: t.BaseURL, keyID: t.LogID.KeyID, key: key, valid: v})
	}
	for _, ca := range raw.CertificateAuthorities {
		certs := ca.CertChain.Certificates
		if len(certs) == 0 {
			continue
		}
		var chain []*x509.Certificate
		for _, c := range certs {
			cert, err := x509.ParseCertificate(c.RawBytes)
			if err != nil {
				return nil, fmt.Errorf("attest: a certificate in the trusted root doesn't parse: %w", err)
			}
			chain = append(chain, cert)
		}
		v, err := parseValidity(ca.ValidFor)
		if err != nil {
			return nil, err
		}
		r.authorities = append(r.authorities, authority{root: chain[len(chain)-1], intermediates: chain[:len(chain)-1], valid: v})
	}
	if len(r.tlogs) == 0 || len(r.authorities) == 0 {
		return nil, errors.New("attest: the trusted root has no usable transparency log or certificate authority")
	}
	return r, nil
}

func DefaultRoot() (*TrustedRoot, error) {
	if path := os.Getenv(RootEnv); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return ParseTrustedRoot(b)
	}
	return ParseTrustedRoot(embeddedRoot)
}
