package screen

import (
	"encoding/base64"
	"encoding/binary"
	"net"
	"strconv"
	"strings"
)

const TeamID = "S8LB56P782"
const AppIdentifier = "ai.sliccy.mac"
const CLIIdentifier = "ai.sliccy.slicc"
const MinimumAppVersion = "0.1.0"

type Peer struct{ Token [8]uint32 }
type Verifier func(*net.UnixConn, Peer) error

func (p Peer) Encoded() string {
	var b [32]byte
	for i, v := range p.Token {
		binary.BigEndian.PutUint32(b[i*4:], v)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func ParsePeer(s string) (Peer, error) {
	var p Peer
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	if e != nil || len(b) != 32 {
		return p, ErrAccess
	}
	for i := range p.Token {
		p.Token[i] = binary.BigEndian.Uint32(b[i*4:])
	}
	if p.Encoded() != s {
		return Peer{}, ErrAccess
	}
	return p, nil
}
func CompareVersion(s, min string) bool {
	parse := func(s string) ([3]uint64, bool) {
		var out [3]uint64
		parts := strings.Split(s, ".")
		if len(parts) != 3 {
			return out, false
		}
		for i, v := range parts {
			if len(v) == 0 || (len(v) > 1 && v[0] == '0') {
				return out, false
			}
			for _, c := range v {
				if c < '0' || c > '9' {
					return out, false
				}
			}
			n, e := strconv.ParseUint(v, 10, 31)
			if e != nil {
				return out, false
			}
			out[i] = n
		}
		return out, true
	}
	a, ok := parse(s)
	if !ok {
		return false
	}
	b, ok := parse(min)
	if !ok {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}
