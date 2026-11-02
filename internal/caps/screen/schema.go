package screen

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

const (
	Cap         = "screen"
	PageCap     = "screen-state"
	HeaderBytes = 4096
	FrameBytes  = 2 << 20
	MaxPixels   = 4000000
	LongEdge    = 2560
	LeaseMillis = 1500
)

var ErrProtocol = errors.New("EPROTO")
var ErrSize = errors.New("EMSGSIZE")
var ErrStale = errors.New("ESTALE")
var ErrAccess = errors.New("EACCES")
var ErrAudit = errors.New("EIO: screen audit unavailable")

type Counter string

func (c Counter) Value() (uint64, error) {
	s := string(c)
	if len(s) == 0 || len(s) > 20 || (len(s) > 1 && s[0] == '0') {
		return 0, ErrProtocol
	}
	for _, v := range s {
		if v < '0' || v > '9' {
			return 0, ErrProtocol
		}
	}
	n, e := strconv.ParseUint(s, 10, 64)
	if e != nil {
		return 0, ErrProtocol
	}
	return n, nil
}
func (c Counter) Next() (Counter, error) {
	n, e := c.Value()
	if e != nil || n == ^uint64(0) {
		return "", ErrStale
	}
	return Counter(strconv.FormatUint(n+1, 10)), nil
}
func ID(n int) string { return base64.RawURLEncoding.EncodeToString(randomBytes(n)) }
func randomBytes(n int) []byte {
	p := make([]byte, n)
	_, _ = rand.Read(p)
	return p
}
func validID(s string, n int) bool {
	p, e := base64.RawURLEncoding.Strict().DecodeString(s)
	return e == nil && len(p) == n && base64.RawURLEncoding.EncodeToString(p) == s
}

type Binding struct {
	Link            string  `json:"link"`
	Session         string  `json:"session"`
	Source          string  `json:"source"`
	GrantEpoch      Counter `json:"grantEpoch"`
	SourceEpoch     Counter `json:"sourceEpoch"`
	ProtectionEpoch Counter `json:"protectionEpoch"`
}

func (b Binding) Valid() bool {
	if !validID(b.Link, 16) || !validID(b.Session, 16) || !validID(b.Source, 16) {
		return false
	}
	for _, c := range []Counter{b.GrantEpoch, b.SourceEpoch, b.ProtectionEpoch} {
		if n, e := c.Value(); e != nil || n == 0 {
			return false
		}
	}
	return true
}

type Limits struct {
	HeaderBytes    int `json:"headerBytes"`
	FrameBytes     int `json:"frameBytes"`
	LongEdge       int `json:"longEdge"`
	Pixels         int `json:"pixels"`
	DefaultFPS     int `json:"defaultFps"`
	MaxFPS         int `json:"maxFps"`
	BytesPerSecond int `json:"bytesPerSecond"`
}

func DefaultLimits() Limits {
	return Limits{HeaderBytes, FrameBytes, LongEdge, MaxPixels, 5, 10, 8 << 20}
}
func (l Limits) Valid() bool {
	d := DefaultLimits()
	return l.HeaderBytes > 0 && l.HeaderBytes <= d.HeaderBytes && l.FrameBytes > 0 && l.FrameBytes <= d.FrameBytes && l.LongEdge > 0 && l.LongEdge <= d.LongEdge && l.Pixels > 0 && l.Pixels <= d.Pixels && l.DefaultFPS > 0 && l.DefaultFPS <= l.MaxFPS && l.MaxFPS <= d.MaxFPS && l.BytesPerSecond > 0 && l.BytesPerSecond <= d.BytesPerSecond
}

type Support struct {
	V      int      `json:"v"`
	Scopes []string `json:"scopes"`
	Codec  string   `json:"codec"`
	Limits Limits   `json:"limits"`
}

func supported(scopes []string) Support { return Support{1, scopes, "jpeg", DefaultLimits()} }

type Frame struct {
	Type       string  `json:"type"`
	V          int     `json:"v"`
	Binding    Binding `json:"binding"`
	Seq        Counter `json:"frameSeq"`
	Captured   Counter `json:"capturedMonoMs"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Codec      string  `json:"codec"`
	Launch     string  `json:"launch,omitempty"`
	Connection string  `json:"connection,omitempty"`
}

type Open struct {
	Kind      string  `json:"kind"`
	V         int     `json:"v"`
	Binding   Binding `json:"binding"`
	MaxFPS    int     `json:"maxFps,omitempty"`
	MaxWidth  int     `json:"maxWidth,omitempty"`
	MaxHeight int     `json:"maxHeight,omitempty"`
}

type Stop struct {
	Kind    string  `json:"kind"`
	V       int     `json:"v"`
	Link    string  `json:"link"`
	Session *string `json:"session"`
}

func Strict(data []byte, v any, limit int) error {
	if len(data) > limit {
		return ErrSize
	}
	if !utf8.Valid(data) {
		return ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if e := walk(d, 0); e != nil {
		return ErrProtocol
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrProtocol
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return ErrProtocol
	}
	return nil
}
func walk(d *json.Decoder, depth int) error {
	if depth > 8 {
		return ErrProtocol
	}
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return ErrProtocol
			}
			seen[s] = true
			if e = walk(d, depth+1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return ErrProtocol
		}
	case '[':
		for d.More() {
			if e = walk(d, depth+1); e != nil {
				return e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	return nil
}

type Features struct {
	V           int      `json:"v"`
	Available   bool     `json:"available"`
	Transport   string   `json:"transport,omitempty"`
	Peer        string   `json:"peer,omitempty"`
	Lanes       []string `json:"lanes,omitempty"`
	Kinds       []string `json:"kinds,omitempty"`
	PageCap     string   `json:"pageCap,omitempty"`
	DynamicCaps bool     `json:"dynamicCaps,omitempty"`
	Codecs      []string `json:"codecs,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	Limits      *Limits  `json:"limits,omitempty"`
}

func Capabilities() Features {
	f := Features{V: 1, Available: NativeAvailable()}
	if f.Available {
		l := DefaultLimits()
		f.Transport = "private-child-unix-v1"
		f.Peer = "audit-token-designated-requirement"
		f.Lanes = []string{"safety", "frames"}
		f.Kinds = []string{"screen", "screen-stop", "screen-state"}
		f.PageCap = PageCap
		f.DynamicCaps = true
		f.Codecs = []string{"jpeg"}
		f.Scopes = []string{"window", "display"}
		f.Limits = &l
	}
	return f
}

func (s *Stop) UnmarshalJSON(p []byte) error {
	type alias Stop
	var v alias
	d := json.NewDecoder(bytes.NewReader(p))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil {
		return ErrProtocol
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(p, &fields) != nil {
		return ErrProtocol
	}
	if _, ok := fields["session"]; !ok {
		return ErrProtocol
	}
	*s = Stop(v)
	return nil
}
