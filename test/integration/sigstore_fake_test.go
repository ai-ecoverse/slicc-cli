package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	wire "encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeSigstore struct {
	root, inter       *x509.Certificate
	rootKey, interKey *ecdsa.PrivateKey
	rekor, ct         *ecdsa.PrivateKey
	logID, ctID       []byte
	rootPath          string
}

type bundleOptions struct {
	issuer    string
	san       string
	predicate string
	notAfter  time.Duration
	sct       string
	config    string
}

func newFakeSigstore(t *testing.T) *fakeSigstore {
	t.Helper()
	f := &fakeSigstore{}
	var err error
	f.rootKey, _ = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	f.interKey, _ = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	f.rekor, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	f.ct, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now()
	ca := func(cn string, pub, signer *ecdsa.PrivateKey, parent *x509.Certificate) *x509.Certificate {
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{Organization: []string{"sigstore.test"}, CommonName: cn},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
			KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		}
		if parent == nil {
			parent = tmpl
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &pub.PublicKey, signer)
		if err != nil {
			t.Fatal(err)
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	f.root = ca("test-root", f.rootKey, f.rootKey, nil)
	f.inter = ca("test-intermediate", f.interKey, f.rootKey, f.root)
	pub, err := x509.MarshalPKIXPublicKey(&f.rekor.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	id := sha256.Sum256(pub)
	f.logID = id[:]
	ctPub, err := x509.MarshalPKIXPublicKey(&f.ct.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	ctID := sha256.Sum256(ctPub)
	f.ctID = ctID[:]
	root := map[string]any{
		"mediaType": "application/vnd.dev.sigstore.trustedroot+json;version=0.1",
		"tlogs": []any{map[string]any{
			"baseUrl": "https://rekor.sigstore.dev", "hashAlgorithm": "SHA2_256",
			"publicKey": map[string]any{"rawBytes": pub, "keyDetails": "PKIX_ECDSA_P256_SHA_256", "validFor": map[string]string{"start": now.Add(-time.Hour).UTC().Format(time.RFC3339)}},
			"logId":     map[string]any{"keyId": f.logID},
		}},
		"ctlogs": []any{map[string]any{
			"baseUrl": "https://ctfe.sigstore.test", "hashAlgorithm": "SHA2_256",
			"publicKey": map[string]any{"rawBytes": ctPub, "keyDetails": "PKIX_ECDSA_P256_SHA_256", "validFor": map[string]string{"start": now.Add(-time.Hour).UTC().Format(time.RFC3339)}},
			"logId":     map[string]any{"keyId": f.ctID},
		}},
		"certificateAuthorities": []any{map[string]any{
			"subject":   map[string]string{"organization": "sigstore.test", "commonName": "test-root"},
			"uri":       "https://fulcio.sigstore.test",
			"certChain": map[string]any{"certificates": []any{map[string]any{"rawBytes": f.inter.Raw}, map[string]any{"rawBytes": f.root.Raw}}},
			"validFor":  map[string]string{"start": now.Add(-time.Hour).UTC().Format(time.RFC3339)},
		}},
	}
	b, _ := json.Marshal(root)
	f.rootPath = filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(f.rootPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeSigstore) env() string { return "SLICC_SIGSTORE_TRUSTED_ROOT=" + f.rootPath }

func releaseSAN(repo string) string {
	return "https://github.com/" + repo + "/.github/workflows/release.yml@refs/heads/main"
}

const pipelineSAN = "https://github.com/ai-ecoverse/slicc-shared-web/.github/workflows/pipeline.yml@refs/tags/v2.8.0"

func callerConfig(repo string) string {
	return "https://github.com/" + repo + "/.github/workflows/ci.yml@refs/heads/main"
}

func pipelineBundle(repo string) bundleOptions {
	return bundleOptions{san: pipelineSAN, config: callerConfig(repo)}
}

func leafHash(b []byte) []byte {
	h := sha256.Sum256(append([]byte{0}, b...))
	return h[:]
}

func nodeHash(l, r []byte) []byte {
	h := sha256.Sum256(append(append([]byte{1}, l...), r...))
	return h[:]
}

func (f *fakeSigstore) bundle(t *testing.T, digest string, o bundleOptions) []byte {
	t.Helper()
	if o.issuer == "" {
		o.issuer = "https://token.actions.githubusercontent.com"
	}
	if o.predicate == "" {
		o.predicate = "https://slsa.dev/provenance/v1"
	}
	if o.notAfter == 0 {
		o.notAfter = 10 * time.Minute
	}
	now := time.Now()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	issuer, _ := asn1.MarshalWithParams(o.issuer, "utf8")
	san, _ := url.Parse(o.san)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(o.notAfter),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}, URIs: []*url.URL{san},
		ExtraExtensions: []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}, Value: issuer}},
	}
	if o.config != "" {
		config, _ := asn1.MarshalWithParams(o.config, "utf8")
		tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, pkix.Extension{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 18}, Value: config})
	}
	der := f.withSCT(t, tmpl, &key.PublicKey, o.sct)
	payload, _ := json.Marshal(map[string]any{
		"_type": "https://in-toto.io/Statement/v1", "predicateType": o.predicate,
		"subject":   []any{map[string]any{"name": "asset", "digest": map[string]string{"sha256": digest}}},
		"predicate": map[string]any{},
	})
	payloadType := "application/vnd.in-toto+json"
	pae := fmt.Sprintf("DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload)
	d := sha256.Sum256([]byte(pae))
	sig, _ := ecdsa.SignASN1(rand.Reader, key, d[:])
	ph := sha256.Sum256(payload)
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	body, _ := json.Marshal(map[string]any{"apiVersion": "0.0.1", "kind": "dsse", "spec": map[string]any{
		"envelopeHash": map[string]string{"algorithm": "sha256", "value": hex.EncodeToString(d[:])},
		"payloadHash":  map[string]string{"algorithm": "sha256", "value": hex.EncodeToString(ph[:])},
		"signatures":   []any{map[string]string{"signature": base64.StdEncoding.EncodeToString(sig), "verifier": base64.StdEncoding.EncodeToString(leafPEM)}},
	}})
	integrated, logIndex := now.Unix(), int64(4242)
	set := fmt.Sprintf(`{"body":%q,"integratedTime":%d,"logID":%q,"logIndex":%d}`, base64.StdEncoding.EncodeToString(body), integrated, hex.EncodeToString(f.logID), logIndex)
	sd := sha256.Sum256([]byte(set))
	setSig, _ := ecdsa.SignASN1(rand.Reader, f.rekor, sd[:])
	l0, l2 := leafHash([]byte("left")), leafHash([]byte("right"))
	rootHash := nodeHash(nodeHash(l0, leafHash(body)), l2)
	note := fmt.Sprintf("rekor.sigstore.dev - 77\n3\n%s\n", base64.StdEncoding.EncodeToString(rootHash))
	nd := sha256.Sum256([]byte(note))
	noteSig, _ := ecdsa.SignASN1(rand.Reader, f.rekor, nd[:])
	note += "\n\u2014 rekor.sigstore.dev " + base64.StdEncoding.EncodeToString(append(append([]byte{}, f.logID[:4]...), noteSig...)) + "\n"
	b, _ := json.Marshal(map[string]any{
		"mediaType": "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{
			"certificate": map[string]any{"rawBytes": der},
			"tlogEntries": []any{map[string]any{
				"logIndex": fmt.Sprint(logIndex), "logId": map[string]any{"keyId": f.logID},
				"kindVersion": map[string]string{"kind": "dsse", "version": "0.0.1"}, "integratedTime": fmt.Sprint(integrated),
				"inclusionPromise": map[string]any{"signedEntryTimestamp": setSig},
				"inclusionProof": map[string]any{"logIndex": "1", "rootHash": rootHash, "treeSize": "3", "hashes": []any{l0, l2},
					"checkpoint": map[string]string{"envelope": note}},
				"canonicalizedBody": body,
			}},
		},
		"dsseEnvelope": map[string]any{"payload": payload, "payloadType": payloadType, "signatures": []any{map[string]any{"sig": sig}}},
	})
	return b
}

func attestationList(bundles ...[]byte) []byte {
	list := []any{}
	for _, b := range bundles {
		list = append(list, map[string]any{"bundle": json.RawMessage(b)})
	}
	out, _ := json.Marshal(map[string]any{"attestations": list})
	return out
}

func (f *fakeSigstore) withSCT(t *testing.T, tmpl *x509.Certificate, pub *ecdsa.PublicKey, mode string) []byte {
	t.Helper()
	pre, err := x509.CreateCertificate(rand.Reader, tmpl, f.inter, pub, f.interKey)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "none" {
		return pre
	}
	cert, _ := x509.ParseCertificate(pre)
	tbs := cert.RawTBSCertificate
	ts := uint64(time.Now().UnixMilli())
	issuer := sha256.Sum256(f.inter.RawSubjectPublicKeyInfo)
	msg := []byte{0, 0}
	msg = wire.BigEndian.AppendUint64(msg, ts)
	msg = append(msg, 0, 1)
	msg = append(msg, issuer[:]...)
	msg = append(msg, byte(len(tbs)>>16), byte(len(tbs)>>8), byte(len(tbs)))
	msg = append(msg, tbs...)
	msg = append(msg, 0, 0)
	signer := f.ct
	if mode == "wrong-key" {
		signer, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	d := sha256.Sum256(msg)
	sig, _ := ecdsa.SignASN1(rand.Reader, signer, d[:])
	one := append([]byte{0}, f.ctID...)
	one = wire.BigEndian.AppendUint64(one, ts)
	one = append(one, 0, 0, 4, 3)
	one = wire.BigEndian.AppendUint16(one, uint16(len(sig)))
	one = append(one, sig...)
	list := wire.BigEndian.AppendUint16(nil, uint16(len(one)))
	list = append(list, one...)
	list = append(wire.BigEndian.AppendUint16(nil, uint16(len(list))), list...)
	value, _ := asn1.Marshal(list)
	final := *tmpl
	final.ExtraExtensions = append(append([]pkix.Extension{}, tmpl.ExtraExtensions...), pkix.Extension{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}, Value: value})
	der, err := x509.CreateCertificate(rand.Reader, &final, f.inter, pub, f.interKey)
	if err != nil {
		t.Fatal(err)
	}
	return der
}
