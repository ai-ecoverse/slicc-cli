package ws

import (
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/textproto"
	"net/url"
	"strings"

	httpcap "github.com/ai-ecoverse/slicc-cli/internal/caps/http"
)

type Open struct {
	URL       *url.URL
	Protocols []string
	Header    nethttp.Header
}

type protoError string

func (e protoError) Error() string { return string(e) }

var dropped = []string{
	"host", "connection", "upgrade", "accept-encoding",
	"sec-websocket-key", "sec-websocket-version", "sec-websocket-extensions", "sec-websocket-protocol",
}

func ParseOpen(raw json.RawMessage) (Open, error) {
	var o struct {
		URL       *string     `json:"url"`
		Protocols []*string   `json:"protocols"`
		Headers   [][]*string `json:"headers"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return Open{}, protoError("malformed ws OPEN")
	}
	if o.URL == nil {
		return Open{}, protoError("ws OPEN needs a url")
	}
	u, err := ParseURL(*o.URL)
	if err != nil {
		return Open{}, err
	}
	protocols, err := Protocols(o.Protocols)
	if err != nil {
		return Open{}, err
	}
	pairs := make([][2]string, 0, len(o.Headers))
	for _, p := range o.Headers {
		if len(p) != 2 || p[0] == nil || p[1] == nil {
			return Open{}, protoError("a header is not a [name, value] pair")
		}
		pairs = append(pairs, [2]string{*p[0], *p[1]})
	}
	header, err := Header(pairs)
	if err != nil {
		return Open{}, err
	}
	return Open{URL: u, Protocols: protocols, Header: header}, nil
}

func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, protoError("url does not parse")
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return nil, protoError("url is not ws or wss")
	}
	if u.Host == "" || u.Hostname() == "" || u.Opaque != "" {
		return nil, protoError("url has no host")
	}
	if u.User != nil {
		return nil, protoError("url includes credentials")
	}
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

func Protocols(list []*string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, p := range list {
		if p == nil || !isToken(*p) {
			return nil, protoError("a subprotocol is not a token")
		}
		for _, q := range out {
			if q == *p {
				return nil, protoError(fmt.Sprintf("subprotocol %s is listed twice", *p))
			}
		}
		out = append(out, *p)
	}
	return out, nil
}

func Header(pairs [][2]string) (nethttp.Header, error) {
	list, err := httpcap.UpstreamHeaders(pairs)
	if err != nil {
		return nil, protoError(err.Error())
	}
	h := nethttp.Header{}
	for _, e := range list {
		if contains(dropped, e.Name) {
			continue
		}
		h[textproto.CanonicalMIMEHeaderKey(e.Name)] = []string{latin1Bytes(e.Value)}
	}
	if _, ok := h["User-Agent"]; !ok {
		h["User-Agent"] = []string{""}
	}
	return h, nil
}

func Name(u *url.URL) string {
	return u.Scheme + "://" + u.Host + u.EscapedPath()
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func latin1Bytes(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return string(b)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
