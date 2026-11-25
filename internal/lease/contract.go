package lease

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"filippo.io/edwards25519"
	"github.com/ai-ecoverse/slicc-cli/internal/link"
	"github.com/ai-ecoverse/slicc-cli/internal/protect"
	jose "github.com/go-jose/go-jose/v4"
	strictjson "github.com/go-jose/go-jose/v4/json"
	whatwg "github.com/nationallibraryofnorway/whatwg-url/url"
)

const Cap = link.LeaseCap

const alg jose.SignatureAlgorithm = "Ed25519"
const typ = "slicc-cdp-lease+jwt"
const MaxTTL = 86400

var enc = base64.RawURLEncoding.Strict()

type refusal string

func (r refusal) Error() string { return string(r) }

const (
	invalid     refusal = "E_INVALID"
	denied      refusal = "E_SCOPE"
	retired     refusal = "E_REVOKED"
	expired     refusal = "E_EXPIRED"
	unsupported refusal = "E_UNSUPPORTED"
	busy        refusal = "E_BUSY"
	rejected    refusal = "E_DENIED"
)

func strict(raw []byte, v any) bool {
	var before, after any
	if len(raw) == 0 || !utf8.Valid(raw) || strictjson.Unmarshal(raw, &before) != nil || before == nil || json.Unmarshal(raw, v) != nil {
		return false
	}
	b, e := json.Marshal(v)
	return e == nil && strictjson.Unmarshal(b, &after) == nil && reflect.DeepEqual(before, after)
}
func random(n int) []byte { b := make([]byte, n); _, _ = rand.Read(b); return b }
func id() string          { return enc.EncodeToString(random(18)) }
func decode(s string, n int) []byte {
	if len(s) != enc.EncodedLen(n) {
		return nil
	}
	b, e := enc.DecodeString(s)
	if e != nil || len(b) != n {
		return nil
	}
	return b
}
func ValidJKT(s string) bool { return decode(s, 32) != nil }

type Rule struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

func safeURL(raw string, o *protect.Origins) (*whatwg.Url, bool) {
	if len(raw) > 2048 || len(raw) < 8 || !strings.EqualFold(raw[:8], "https://") || strings.ContainsAny(raw, "\\\t\r\n ") || o.Loaded(raw) || o.Kept(raw) || protect.CredentialURL(raw) {
		return nil, false
	}
	authority := strings.FieldsFunc(raw[8:], func(r rune) bool { return r == '/' || r == '?' || r == '#' })
	if len(authority) == 0 || strings.ContainsAny(authority[0], "@*%") || !strings.HasPrefix(raw[8:], authority[0]) {
		return nil, false
	}
	if _, e := url.PathUnescape(raw); e != nil {
		return nil, false
	}
	for _, r := range raw {
		if r < 32 || r == 127 {
			return nil, false
		}
	}
	w, e := whatwg.Parse(raw)
	if e != nil || w.Username() != "" || w.Password() != "" || w.Host() == "" {
		return nil, false
	}
	return w, true
}
func ambiguous(raw string) bool {
	path := strings.SplitN(strings.SplitN(raw, "?", 2)[0], "#", 2)[0]
	path = strings.ToLower(path)
	if strings.Contains(path, "%2f") || strings.Contains(path, "%5c") {
		return true
	}
	for _, seg := range strings.Split(path, "/") {
		seg = strings.ReplaceAll(seg, "%2e", ".")
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}
func (r Rule) Valid(o *protect.Origins) bool {
	if strings.Contains(r.URL, "*") || r.Kind != "origin" && ambiguous(r.URL) {
		return false
	}
	u, ok := safeURL(r.URL, o)
	if !ok {
		return false
	}
	origin := "https://" + u.Host()
	switch r.Kind {
	case "origin":
		return r.URL == origin
	case "exact":
		return r.URL == u.Href(false)
	case "path-prefix":
		return r.URL == origin+u.Pathname() && r.URL == u.Href(false) && (u.Pathname() == "/" || !strings.HasSuffix(r.URL, "/"))
	}
	return false
}
func matches(rules []Rule, raw string, o *protect.Origins) bool {
	u, ok := safeURL(raw, o)
	if !ok {
		return false
	}
	origin := "https://" + u.Host()
	path := origin + u.Pathname()
	return slices.ContainsFunc(rules, func(r Rule) bool {
		switch r.Kind {
		case "origin":
			return r.URL == origin
		case "exact":
			return !ambiguous(raw) && r.URL == u.Href(false)
		case "path-prefix":
			return !ambiguous(raw) && (r.URL == path || strings.HasPrefix(path, strings.TrimSuffix(r.URL, "/")+"/"))
		}
		return false
	})
}
func canonical(rules []Rule, raw string, o *protect.Origins) (string, bool) {
	u, ok := safeURL(raw, o)
	if !ok || !matches(rules, raw, o) {
		return "", false
	}
	href := u.Href(false)
	return href, matches(rules, href, o)
}
func ValidRules(rules []Rule, o *protect.Origins) bool {
	if len(rules) == 0 || len(rules) > 8 {
		return false
	}
	for i, r := range rules {
		if !r.Valid(o) || slices.Contains(rules[:i], r) {
			return false
		}
	}
	return true
}

type jwk struct {
	Crv string `json:"crv"`
	Kty string `json:"kty"`
	X   string `json:"x"`
}

func (j jwk) key() (ed25519.PublicKey, string) {
	p := decode(j.X, 32)
	if j.Crv != "Ed25519" || j.Kty != "OKP" || p == nil {
		return nil, ""
	}
	point, e := new(edwards25519.Point).SetBytes(p)
	if e != nil || !bytes.Equal(point.Bytes(), p) || new(edwards25519.Point).MultByCofactor(point).Equal(edwards25519.NewIdentityPoint()) == 1 {
		return nil, ""
	}
	b, _ := json.Marshal(j)
	h := sha256.Sum256(b)
	return p, enc.EncodeToString(h[:])
}

type signingKey struct{ ed25519.PrivateKey }

func (k signingKey) Public() *jose.JSONWebKey        { return nil }
func (k signingKey) Algs() []jose.SignatureAlgorithm { return []jose.SignatureAlgorithm{alg} }
func (k signingKey) SignPayload(b []byte, a jose.SignatureAlgorithm) ([]byte, error) {
	if a != alg {
		return nil, invalid
	}
	return ed25519.Sign(k.PrivateKey, b), nil
}

type verifyKey struct{ ed25519.PublicKey }

func (k verifyKey) VerifyPayload(b, s []byte, a jose.SignatureAlgorithm) error {
	if a != alg || !ed25519.Verify(k.PublicKey, b, s) {
		return invalid
	}
	return nil
}

type confirmation struct {
	JKT string `json:"jkt"`
}
type claims struct {
	Iss   string       `json:"iss"`
	Aud   string       `json:"aud"`
	ID    string       `json:"jti"`
	Exp   int64        `json:"exp"`
	CNF   confirmation `json:"cnf"`
	Epoch string       `json:"browser_session"`
	Allow []Rule       `json:"allow"`
}

func (b *Broker) sign(c claims) (string, error) {
	raw, e := json.Marshal(c)
	if e != nil {
		return "", invalid
	}
	j, e := b.signer.Sign(raw)
	if e != nil {
		return "", invalid
	}
	return j.CompactSerialize()
}
func (b *Broker) verify(jwt, jkt string) (claims, error) {
	var c claims
	p := strings.Split(jwt, ".")
	if len(p) != 3 || len(jwt) > 16384 {
		return c, invalid
	}
	for _, s := range p {
		raw, e := enc.DecodeString(s)
		if e != nil || enc.EncodeToString(raw) != s {
			return c, invalid
		}
	}
	h, e := enc.DecodeString(p[0])
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if e != nil || !strict(h, &header) || header.Alg != string(alg) || header.Typ != typ {
		return c, invalid
	}
	j, e := jose.ParseSignedCompact(jwt, []jose.SignatureAlgorithm{alg})
	if e != nil {
		return c, invalid
	}
	raw, e := j.Verify(b.public)
	if e != nil || !strict(raw, &c) || c.Iss != b.issuer || c.Aud != b.audience || c.Epoch != b.epoch || c.CNF.JKT != jkt || !ValidRules(c.Allow, b.origins) {
		return c, invalid
	}
	return c, nil
}
func proof(mode string, nonce, binding []byte) []byte {
	return bytes.Join([][]byte{[]byte("slicc-cdp-lease-v1:" + mode), append([]byte{0}, nonce...), binding}, nil)
}
