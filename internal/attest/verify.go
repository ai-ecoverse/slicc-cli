package attest

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/bits"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	Issuer              = "https://token.actions.githubusercontent.com"
	ProvenanceV1        = "https://slsa.dev/provenance/v1"
	inTotoPayloadType   = "application/vnd.in-toto+json"
	bundleV03           = "application/vnd.dev.sigstore.bundle.v0.3+json"
	bundleV03Alt        = "application/vnd.dev.sigstore.bundle+json;version=0.3"
	statementV1         = "https://in-toto.io/Statement/v1"
	statementV01        = "https://in-toto.io/Statement/v0.1"
	checkpointSigEmDash = "\u2014 "
)

var (
	oidIssuerV1 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
	oidIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
)

type Identity struct {
	Repo string
	Ref  string
}

func (id Identity) SAN() string {
	return "https://github.com/" + id.Repo + "/.github/workflows/release.yml@" + id.Ref
}

type Error struct {
	Step string
	Msg  string
}

func (e *Error) Error() string { return "attestation " + e.Step + ": " + e.Msg }

func fail(step, format string, args ...any) error {
	return &Error{Step: step, Msg: fmt.Sprintf(format, args...)}
}

type rawBundle struct {
	MediaType            string `json:"mediaType"`
	VerificationMaterial struct {
		Certificate *struct {
			RawBytes []byte `json:"rawBytes"`
		} `json:"certificate"`
		TlogEntries []tlogEntry `json:"tlogEntries"`
	} `json:"verificationMaterial"`
	DSSEEnvelope *struct {
		Payload     []byte `json:"payload"`
		PayloadType string `json:"payloadType"`
		Signatures  []struct {
			Sig []byte `json:"sig"`
		} `json:"signatures"`
	} `json:"dsseEnvelope"`
}

type tlogEntry struct {
	LogIndex string `json:"logIndex"`
	LogID    struct {
		KeyID []byte `json:"keyId"`
	} `json:"logId"`
	KindVersion struct {
		Kind    string `json:"kind"`
		Version string `json:"version"`
	} `json:"kindVersion"`
	IntegratedTime   string `json:"integratedTime"`
	InclusionPromise *struct {
		SignedEntryTimestamp []byte `json:"signedEntryTimestamp"`
	} `json:"inclusionPromise"`
	InclusionProof *struct {
		LogIndex   string   `json:"logIndex"`
		RootHash   []byte   `json:"rootHash"`
		TreeSize   string   `json:"treeSize"`
		Hashes     [][]byte `json:"hashes"`
		Checkpoint struct {
			Envelope string `json:"envelope"`
		} `json:"checkpoint"`
	} `json:"inclusionProof"`
	CanonicalizedBody []byte `json:"canonicalizedBody"`
}

type statement struct {
	Type          string `json:"_type"`
	PredicateType string `json:"predicateType"`
	Subject       []struct {
		Digest map[string]string `json:"digest"`
	} `json:"subject"`
}

func Verify(bundleJSON []byte, digest string, id Identity, root *TrustedRoot) error {
	var b rawBundle
	if err := json.Unmarshal(bundleJSON, &b); err != nil {
		return fail("bundle", "doesn't parse")
	}
	if b.MediaType != bundleV03 && b.MediaType != bundleV03Alt {
		return fail("bundle", "media type %q isn't a v0.3 Sigstore bundle", b.MediaType)
	}
	env := b.DSSEEnvelope
	if env == nil || b.VerificationMaterial.Certificate == nil || len(b.VerificationMaterial.TlogEntries) != 1 {
		return fail("bundle", "needs one DSSE envelope, one certificate and one transparency log entry")
	}
	if err := checkStatement(env.PayloadType, env.Payload, digest); err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(b.VerificationMaterial.Certificate.RawBytes)
	if err != nil {
		return fail("certificate", "doesn't parse")
	}
	if len(env.Signatures) != 1 {
		return fail("dsse", "needs exactly one signature, has %d", len(env.Signatures))
	}
	sig := env.Signatures[0].Sig
	if err := verifySignature(leaf.PublicKey, pae(env.PayloadType, env.Payload), sig); err != nil {
		return fail("dsse", "the signature doesn't verify with the certificate's key")
	}
	entry := b.VerificationMaterial.TlogEntries[0]
	integrated, err := strconv.ParseInt(entry.IntegratedTime, 10, 64)
	if err != nil || integrated <= 0 {
		return fail("tlog", "no integrated time")
	}
	at := time.Unix(integrated, 0)
	if err := checkChain(leaf, at, root); err != nil {
		return err
	}
	if err := checkIdentity(leaf, id); err != nil {
		return err
	}
	if err := checkBody(entry, env.Payload, sig, leaf); err != nil {
		return err
	}
	log, err := findLog(entry, at, root)
	if err != nil {
		return err
	}
	if err := checkSET(entry, integrated, log); err != nil {
		return err
	}
	return checkInclusion(entry, log)
}

func checkStatement(payloadType string, payload []byte, digest string) error {
	if payloadType != inTotoPayloadType {
		return fail("predicate", "payload type %q isn't in-toto", payloadType)
	}
	var st statement
	if err := json.Unmarshal(payload, &st); err != nil {
		return fail("predicate", "the in-toto statement doesn't parse")
	}
	if st.Type != statementV1 && st.Type != statementV01 {
		return fail("predicate", "statement type %q", st.Type)
	}
	if st.PredicateType != ProvenanceV1 {
		return fail("predicate", "predicate type %q isn't SLSA provenance v1", st.PredicateType)
	}
	want := strings.ToLower(digest)
	for _, s := range st.Subject {
		if strings.ToLower(s.Digest["sha256"]) == want && want != "" {
			return nil
		}
	}
	return fail("subject", "no subject has the file's SHA-256")
}

func pae(payloadType string, payload []byte) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "DSSEv1 %d %s %d ", len(payloadType), payloadType, len(payload))
	buf.Write(payload)
	return buf.Bytes()
}

func verifySignature(pub crypto.PublicKey, msg, sig []byte) error {
	key, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("not an ECDSA key")
	}
	var digest []byte
	switch key.Curve {
	case elliptic.P256():
		d := sha256.Sum256(msg)
		digest = d[:]
	case elliptic.P384():
		d := sha512.Sum384(msg)
		digest = d[:]
	default:
		return fmt.Errorf("unsupported curve")
	}
	if !ecdsa.VerifyASN1(key, digest, sig) {
		return fmt.Errorf("bad signature")
	}
	return nil
}

func checkChain(leaf *x509.Certificate, at time.Time, root *TrustedRoot) error {
	for _, ca := range root.authorities {
		if !ca.valid.covers(at) {
			continue
		}
		roots, inter := x509.NewCertPool(), x509.NewCertPool()
		roots.AddCert(ca.root)
		for _, c := range ca.intermediates {
			inter.AddCert(c)
		}
		_, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}})
		if err == nil {
			return nil
		}
	}
	return fail("certificate", "doesn't chain to a trusted Fulcio root at its integrated time %s", at.UTC().Format(time.RFC3339))
}

func checkIdentity(leaf *x509.Certificate, id Identity) error {
	issuer := ""
	for _, ext := range leaf.Extensions {
		switch {
		case ext.Id.Equal(oidIssuerV2):
			var s string
			if rest, err := asn1.Unmarshal(ext.Value, &s); err == nil && len(rest) == 0 {
				issuer = s
			}
		case ext.Id.Equal(oidIssuerV1) && issuer == "":
			issuer = string(ext.Value)
		}
	}
	if issuer != Issuer {
		return fail("identity", "issuer %q, want %s", issuer, Issuer)
	}
	if len(leaf.URIs) != 1 || len(leaf.EmailAddresses) != 0 || len(leaf.DNSNames) != 0 || len(leaf.IPAddresses) != 0 {
		return fail("identity", "the certificate needs exactly one URI subject alternative name")
	}
	if got := leaf.URIs[0].String(); got != id.SAN() {
		return fail("identity", "signed by %s, want %s", got, id.SAN())
	}
	return nil
}

type dsseBody struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		PayloadHash struct {
			Algorithm string `json:"algorithm"`
			Value     string `json:"value"`
		} `json:"payloadHash"`
		Signatures []struct {
			Signature string `json:"signature"`
			Verifier  string `json:"verifier"`
		} `json:"signatures"`
	} `json:"spec"`
}

func checkBody(entry tlogEntry, payload, sig []byte, leaf *x509.Certificate) error {
	if entry.KindVersion.Kind != "dsse" || entry.KindVersion.Version != "0.0.1" {
		return fail("tlog", "entry kind %s %s isn't dsse 0.0.1", entry.KindVersion.Kind, entry.KindVersion.Version)
	}
	var body dsseBody
	if err := json.Unmarshal(entry.CanonicalizedBody, &body); err != nil || body.Kind != "dsse" || body.APIVersion != "0.0.1" {
		return fail("tlog", "the entry body isn't a dsse 0.0.1 entry")
	}
	sum := sha256.Sum256(payload)
	if body.Spec.PayloadHash.Algorithm != "sha256" || body.Spec.PayloadHash.Value != hex.EncodeToString(sum[:]) {
		return fail("tlog", "the entry's payload hash isn't the envelope's")
	}
	if len(body.Spec.Signatures) != 1 || body.Spec.Signatures[0].Signature != base64.StdEncoding.EncodeToString(sig) {
		return fail("tlog", "the entry's signature isn't the envelope's")
	}
	pemBytes, err := base64.StdEncoding.DecodeString(body.Spec.Signatures[0].Verifier)
	block, _ := pem.Decode(pemBytes)
	if err != nil || block == nil || !bytes.Equal(block.Bytes, leaf.Raw) {
		return fail("tlog", "the entry's verifier isn't the bundle's certificate")
	}
	return nil
}

func findLog(entry tlogEntry, at time.Time, root *TrustedRoot) (tlog, error) {
	for _, l := range root.tlogs {
		if bytes.Equal(l.keyID, entry.LogID.KeyID) && l.valid.covers(at) {
			return l, nil
		}
	}
	return tlog{}, fail("tlog", "the entry's log isn't a trusted transparency log at its integrated time")
}

func checkSET(entry tlogEntry, integrated int64, log tlog) error {
	if entry.InclusionPromise == nil {
		return fail("set", "no signed entry timestamp")
	}
	index, err := strconv.ParseInt(entry.LogIndex, 10, 64)
	if err != nil || index < 0 {
		return fail("set", "no log index")
	}
	payload := fmt.Sprintf(`{"body":%q,"integratedTime":%d,"logID":%q,"logIndex":%d}`,
		base64.StdEncoding.EncodeToString(entry.CanonicalizedBody), integrated, hex.EncodeToString(log.keyID), index)
	d := sha256.Sum256([]byte(payload))
	if !ecdsa.VerifyASN1(log.key, d[:], entry.InclusionPromise.SignedEntryTimestamp) {
		return fail("set", "the signed entry timestamp doesn't verify with the log's key")
	}
	return nil
}

func hashChildren(l, r []byte) []byte {
	h := sha256.New()
	h.Write([]byte{1})
	h.Write(l)
	h.Write(r)
	return h.Sum(nil)
}

func rootFromProof(index, size uint64, leaf []byte, proof [][]byte) ([]byte, error) {
	if index >= size {
		return nil, fmt.Errorf("index %d is past the tree size %d", index, size)
	}
	inner := bits.Len64(index ^ (size - 1))
	border := bits.OnesCount64(index >> uint(inner))
	if len(proof) != inner+border {
		return nil, fmt.Errorf("the proof has %d hashes, want %d", len(proof), inner+border)
	}
	seed := leaf
	for i, h := range proof[:inner] {
		if len(h) != sha256.Size {
			return nil, fmt.Errorf("a proof hash has %d bytes", len(h))
		}
		if (index>>uint(i))&1 == 0 {
			seed = hashChildren(seed, h)
		} else {
			seed = hashChildren(h, seed)
		}
	}
	for _, h := range proof[inner:] {
		if len(h) != sha256.Size {
			return nil, fmt.Errorf("a proof hash has %d bytes", len(h))
		}
		seed = hashChildren(h, seed)
	}
	return seed, nil
}

func checkInclusion(entry tlogEntry, log tlog) error {
	p := entry.InclusionProof
	if p == nil {
		return fail("inclusion", "no inclusion proof")
	}
	index, err1 := strconv.ParseUint(p.LogIndex, 10, 64)
	size, err2 := strconv.ParseUint(p.TreeSize, 10, 64)
	if err1 != nil || err2 != nil {
		return fail("inclusion", "no log index or tree size")
	}
	leaf := sha256.Sum256(append([]byte{0}, entry.CanonicalizedBody...))
	got, err := rootFromProof(index, size, leaf[:], p.Hashes)
	if err != nil {
		return fail("inclusion", "%v", err)
	}
	if !bytes.Equal(got, p.RootHash) {
		return fail("inclusion", "the proof doesn't lead to the root hash")
	}
	return checkCheckpoint(p.Checkpoint.Envelope, size, p.RootHash, log)
}

func checkCheckpoint(note string, size uint64, rootHash []byte, log tlog) error {
	text, sigs, ok := strings.Cut(note, "\n\n")
	if !ok {
		return fail("checkpoint", "no signature block")
	}
	text += "\n"
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	host := ""
	if u, err := url.Parse(log.baseURL); err == nil {
		host = u.Host
	}
	if len(lines) < 3 || host == "" || (lines[0] != host && !strings.HasPrefix(lines[0], host+" - ")) {
		return fail("checkpoint", "it isn't a checkpoint of %s", log.baseURL)
	}
	if lines[1] != strconv.FormatUint(size, 10) || lines[2] != base64.StdEncoding.EncodeToString(rootHash) {
		return fail("checkpoint", "its tree size or root hash isn't the proof's")
	}
	d := sha256.Sum256([]byte(text))
	for _, line := range strings.Split(strings.TrimSuffix(sigs, "\n"), "\n") {
		rest, ok := strings.CutPrefix(line, checkpointSigEmDash)
		if !ok {
			continue
		}
		name, b64, ok := strings.Cut(rest, " ")
		raw, err := base64.StdEncoding.DecodeString(b64)
		if !ok || err != nil || name != host || len(raw) < 5 || !bytes.Equal(raw[:4], log.keyID[:4]) {
			continue
		}
		if ecdsa.VerifyASN1(log.key, d[:], raw[4:]) {
			return nil
		}
	}
	return fail("checkpoint", "no signature by %s verifies", host)
}
