package attest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"time"
)

var oidSCTList = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}

type sct struct {
	logID     []byte
	timestamp uint64
	ext       []byte
	hashAlg   byte
	sigAlg    byte
	sig       []byte
}

type reader struct {
	b   []byte
	bad bool
}

func (r *reader) take(n int) []byte {
	if r.bad || n < 0 || len(r.b) < n {
		r.bad = true
		return nil
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *reader) u8() byte {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (r *reader) u16() int {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return int(binary.BigEndian.Uint16(b))
}

func (r *reader) u64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func parseSCTs(leaf *x509.Certificate) ([]sct, error) {
	var raw []byte
	for _, ext := range leaf.Extensions {
		if ext.Id.Equal(oidSCTList) {
			if rest, err := asn1.Unmarshal(ext.Value, &raw); err != nil || len(rest) != 0 {
				return nil, errors.New("the SCT list doesn't parse")
			}
		}
	}
	if raw == nil {
		return nil, errors.New("no embedded SCT")
	}
	list := &reader{b: raw}
	body := &reader{b: list.take(list.u16())}
	if list.bad || len(list.b) != 0 {
		return nil, errors.New("the SCT list doesn't parse")
	}
	var out []sct
	for len(body.b) > 0 && !body.bad {
		one := &reader{b: body.take(body.u16())}
		var s sct
		if one.u8() != 0 {
			continue
		}
		s.logID = one.take(32)
		s.timestamp = one.u64()
		s.ext = one.take(one.u16())
		s.hashAlg, s.sigAlg = one.u8(), one.u8()
		s.sig = one.take(one.u16())
		if one.bad || len(one.b) != 0 {
			return nil, errors.New("an SCT doesn't parse")
		}
		out = append(out, s)
	}
	if body.bad {
		return nil, errors.New("the SCT list doesn't parse")
	}
	return out, nil
}

func precertTBS(leaf *x509.Certificate) ([]byte, error) {
	var tbs asn1.RawValue
	if rest, err := asn1.Unmarshal(leaf.RawTBSCertificate, &tbs); err != nil || len(rest) != 0 {
		return nil, errors.New("the certificate body doesn't parse")
	}
	var fields []byte
	found := false
	for rest := tbs.Bytes; len(rest) > 0; {
		var f asn1.RawValue
		var err error
		if rest, err = asn1.Unmarshal(rest, &f); err != nil {
			return nil, errors.New("the certificate body doesn't parse")
		}
		if f.Class != asn1.ClassContextSpecific || f.Tag != 3 {
			fields = append(fields, f.FullBytes...)
			continue
		}
		var exts asn1.RawValue
		if r, err := asn1.Unmarshal(f.Bytes, &exts); err != nil || len(r) != 0 {
			return nil, errors.New("the certificate extensions don't parse")
		}
		var kept []byte
		for er := exts.Bytes; len(er) > 0; {
			var e struct {
				Raw asn1.RawContent
				ID  asn1.ObjectIdentifier
			}
			var ev asn1.RawValue
			if er, err = asn1.Unmarshal(er, &ev); err != nil {
				return nil, errors.New("an extension doesn't parse")
			}
			if _, err := asn1.Unmarshal(ev.FullBytes, &e); err == nil && e.ID.Equal(oidSCTList) {
				found = true
				continue
			}
			kept = append(kept, ev.FullBytes...)
		}
		seq, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: kept})
		if err != nil {
			return nil, err
		}
		wrapped, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 3, IsCompound: true, Bytes: seq})
		if err != nil {
			return nil, err
		}
		fields = append(fields, wrapped...)
	}
	if !found {
		return nil, errors.New("no embedded SCT")
	}
	return asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: fields})
}

func checkSCT(leaf, issuer *x509.Certificate, root *TrustedRoot) error {
	scts, err := parseSCTs(leaf)
	if err != nil {
		return fail("sct", "%v", err)
	}
	tbs, err := precertTBS(leaf)
	if err != nil {
		return fail("sct", "%v", err)
	}
	issuerHash := sha256.Sum256(issuer.RawSubjectPublicKeyInfo)
	for _, s := range scts {
		if s.hashAlg != 4 || s.sigAlg != 3 {
			continue
		}
		at := time.UnixMilli(int64(s.timestamp))
		for _, l := range root.ctlogs {
			if !bytes.Equal(l.keyID, s.logID) || !l.valid.covers(at) {
				continue
			}
			var msg bytes.Buffer
			msg.Write([]byte{0, 0})
			_ = binary.Write(&msg, binary.BigEndian, s.timestamp)
			msg.Write([]byte{0, 1})
			msg.Write(issuerHash[:])
			msg.Write([]byte{byte(len(tbs) >> 16), byte(len(tbs) >> 8), byte(len(tbs))})
			msg.Write(tbs)
			_ = binary.Write(&msg, binary.BigEndian, uint16(len(s.ext)))
			msg.Write(s.ext)
			d := sha256.Sum256(msg.Bytes())
			if ecdsa.VerifyASN1(l.key, d[:], s.sig) {
				return nil
			}
		}
	}
	return fail("sct", "no embedded SCT verifies with a trusted certificate transparency log")
}
