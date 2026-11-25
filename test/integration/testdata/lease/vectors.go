package lease

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func vectorFile(t *testing.T, name string, v any) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join("../../test/integration/testdata/lease", name))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, v); e != nil {
		t.Fatal(e)
	}
}

type fixtureStream struct {
	Open                                      open
	Opened                                    struct{ Nonce string }
	ProofRecordHex, TranscriptHex, BindingHex string
}

func TestLeaseVectors(t *testing.T) {
	var f struct {
		JWT                  string
		Claims               claims
		IssuerJwk, HolderJwk jwk
		Context              struct {
			Now    int64
			Active []string
		}
		RequestStream, DataStream fixtureStream
		Expansion                 struct {
			Open  open
			Grant struct{ JWT string }
		}
		JwtCases []struct {
			ID, JWT  string
			JWK      json.RawMessage
			Now      int64
			Active   []string
			Accepted bool
		}
		FramingCases []struct {
			ID, Hex string
			Records []json.RawMessage
		}
	}
	vectorFile(t, "lease-v1.json", &f)
	pub, jkt := f.HolderJwk.key()
	issuer, _ := f.IssuerJwk.key()
	b := Broker{public: verifyKey{issuer}, issuer: f.Claims.Iss, audience: f.Claims.Aud, epoch: f.Claims.Epoch}
	for _, c := range f.JwtCases {
		t.Run(c.ID, func(t *testing.T) {
			jwt := c.JWT
			if jwt == "" {
				jwt = f.JWT
			}
			j := f.HolderJwk
			if c.JWK != nil {
				j = jwk{}
				if !strict(c.JWK, &j) {
					if c.Accepted {
						t.Fatal("holder rejected")
					}
					return
				}
			}
			_, thumb := j.key()
			cl, e := b.verify(jwt, thumb)
			now := c.Now
			if now == 0 {
				now = f.Context.Now
			}
			ids := c.Active
			if ids == nil {
				ids = f.Context.Active
			}
			b.active = map[string]*active{}
			if slices.Contains(ids, f.Claims.ID) {
				b.active[f.Claims.ID] = &active{claims: f.Claims, ctx: context.Background()}
			}
			if e == nil {
				_, e = b.currentAt(cl, time.Unix(now, 0))
			}
			if (e == nil) != c.Accepted {
				t.Fatalf("accepted=%v want %v", e == nil, c.Accepted)
			}
		})
	}
	for _, s := range []fixtureStream{f.RequestStream, f.DataStream} {
		binding := decode(jkt, 32)
		if s.Open.Mode == "data" {
			d := sha256.Sum256([]byte(f.JWT))
			binding = d[:]
		}
		transcript := proof(s.Open.Mode, decode(s.Opened.Nonce, 32), binding)
		record, _ := hex.DecodeString(s.ProofRecordHex)
		var p struct {
			Type string `json:"type"`
			Sig  string `json:"sig"`
		}
		if hex.EncodeToString(binding) != s.BindingHex || hex.EncodeToString(transcript) != s.TranscriptHex || !read(bytes.NewReader(record), &p) || p.Type != "proof" || !ed25519.Verify(pub, transcript, decode(p.Sig, 64)) {
			t.Fatal(s.Open.Mode, "proof mismatch")
		}
		if ed25519.Verify(pub, proof(s.Open.Mode, random(32), binding), decode(p.Sig, 64)) {
			t.Fatal("proof reused")
		}
	}
	next, e := b.verify(f.Expansion.Grant.JWT, jkt)
	if e != nil || next.Exp != f.Claims.Exp || next.ID == f.Claims.ID || !slices.Equal(next.Allow, append(slices.Clone(f.Claims.Allow), f.Expansion.Open.Allow...)) {
		t.Fatal("replacement vector")
	}
	for _, c := range f.FramingCases {
		t.Run(c.ID, func(t *testing.T) {
			data, _ := hex.DecodeString(c.Hex)
			rd := bytes.NewReader(data)
			if c.Records == nil {
				var v any
				if read(rd, &v) {
					t.Fatal("accepted invalid record")
				}
				return
			}
			for _, want := range c.Records {
				var v json.RawMessage
				if !read(rd, &v) {
					t.Fatal("invalid record")
				}
				var a, z any
				_ = json.Unmarshal(v, &a)
				_ = json.Unmarshal(want, &z)
				p, _ := json.Marshal(a)
				q, _ := json.Marshal(z)
				if !bytes.Equal(p, q) {
					t.Fatalf("mismatch %s", c.ID)
				}
			}
		})
	}
}
func TestScopeVectors(t *testing.T) {
	var f struct {
		InvalidRules []json.RawMessage
		URLCases     []struct {
			ID    string
			Allow []Rule
			URL   string
			Match bool
		}
	}
	vectorFile(t, "lease-scope-v1.json", &f)
	for _, raw := range f.InvalidRules {
		var r Rule
		if strict(raw, &r) && r.Valid(nil) {
			t.Fatalf("invalid rule %s", raw)
		}
	}
	for _, c := range f.URLCases {
		if (ValidRules(c.Allow, nil) && matches(c.Allow, c.URL, nil)) != c.Match {
			t.Error(c.ID)
		}
	}
}
