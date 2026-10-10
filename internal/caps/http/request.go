package http

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
)

const (
	MaxHead        = 1 << 20
	AcceptEncoding = "gzip, deflate, br"
)

type RequestHead struct {
	URL     string      `json:"url"`
	Method  string      `json:"method"`
	Headers [][2]string `json:"headers"`
}

var hopByHop = []string{"connection", "keep-alive", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade"}

var requestSkip = append([]string{"host", "content-length", "accept-encoding", "expect", "proxy-authorization"}, hopByHop...)

var normalizedMethods = []string{"DELETE", "GET", "HEAD", "OPTIONS", "POST", "PUT"}

var forbiddenMethods = []string{"CONNECT", "TRACE", "TRACK"}

type protoError string

func (e protoError) Error() string { return string(e) }

func ReadHead(r io.Reader) (RequestHead, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return RequestHead{}, headReadError(err)
	}
	n := binary.BigEndian.Uint32(size[:])
	if n > MaxHead {
		return RequestHead{}, protoError(fmt.Sprintf("request head of %d bytes is over %d", n, MaxHead))
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return RequestHead{}, headReadError(err)
	}
	return DecodeHead(buf)
}

func headReadError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return protoError("the request ended inside its head")
	}
	return err
}

func DecodeHead(b []byte) (RequestHead, error) {
	var raw struct {
		URL     *string      `json:"url"`
		Method  *string      `json:"method"`
		Headers *[][]*string `json:"headers"`
	}
	if err := json.Unmarshal(b, &raw); err != nil || raw.URL == nil || raw.Method == nil || raw.Headers == nil {
		return RequestHead{}, protoError("malformed request head")
	}
	h := RequestHead{URL: *raw.URL, Method: *raw.Method, Headers: make([][2]string, 0, len(*raw.Headers))}
	if !isToken(h.Method) {
		return RequestHead{}, protoError("malformed request head: method is not a token")
	}
	for _, pair := range *raw.Headers {
		if len(pair) != 2 || pair[0] == nil || pair[1] == nil {
			return RequestHead{}, protoError("malformed request head: a header is not a [name, value] pair")
		}
		h.Headers = append(h.Headers, [2]string{*pair[0], *pair[1]})
	}
	return h, nil
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

func Method(m string) (string, error) {
	if !isToken(m) {
		return "", protoError("method is not a token")
	}
	for _, f := range forbiddenMethods {
		if strings.EqualFold(m, f) {
			return "", protoError("method " + f + " is not allowed")
		}
	}
	for _, n := range normalizedMethods {
		if strings.EqualFold(m, n) {
			return n, nil
		}
	}
	return m, nil
}

func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, protoError("url does not parse")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, protoError("url is not http or https")
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

type Header struct {
	Name  string
	Value string
}

func UpstreamHeaders(headers [][2]string) ([]Header, error) {
	named := connectionTokens(headers)
	var out []Header
	index := map[string]int{}
	for _, h := range headers {
		name, value := h[0], h[1]
		if !isToken(name) {
			return nil, protoError(fmt.Sprintf("header name %q is not a token", name))
		}
		lower := strings.ToLower(name)
		if contains(requestSkip, lower) || named[lower] {
			continue
		}
		if i, ok := index[lower]; ok {
			sep := ", "
			if lower == "cookie" {
				sep = "; "
			}
			out[i].Value += sep + value
			continue
		}
		index[lower] = len(out)
		out = append(out, Header{Name: lower, Value: value})
	}
	for i := range out {
		out[i].Value = trimHTTPSpace(out[i].Value)
		if !validValue(out[i].Value) {
			return nil, protoError(fmt.Sprintf("header %s has an invalid value", out[i].Name))
		}
	}
	_, hasRange := index["range"]
	_, hasIfRange := index["if-range"]
	if !hasRange && !hasIfRange {
		out = append(out, Header{Name: "accept-encoding", Value: AcceptEncoding})
	}
	return out, nil
}

func DeclaredLength(headers [][2]string) int64 {
	var found string
	for _, h := range headers {
		if !strings.EqualFold(h[0], "content-length") {
			continue
		}
		v := trimHTTPSpace(h[1])
		if found != "" && v != found {
			return -1
		}
		found = v
	}
	if found == "" || strings.TrimLeft(found, "0123456789") != "" {
		return -1
	}
	n, err := strconv.ParseInt(found, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func connectionTokens(headers [][2]string) map[string]bool {
	tokens := map[string]bool{}
	for _, h := range headers {
		if !strings.EqualFold(h[0], "connection") {
			continue
		}
		for _, t := range strings.Split(h[1], ",") {
			if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
				tokens[t] = true
			}
		}
	}
	return tokens
}

func trimHTTPSpace(s string) string {
	return strings.Trim(s, " \t\r\n")
}

func validValue(s string) bool {
	for _, r := range s {
		if r > 0xff || r == 0x7f || (r < 0x20 && r != '\t') {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func latin1Bytes(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return string(b)
}

func latin1String(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		sb.WriteRune(rune(s[i]))
	}
	return sb.String()
}

func BuildRequest(head RequestHead) (*nethttp.Request, error) {
	method, err := Method(head.Method)
	if err != nil {
		return nil, err
	}
	u, err := ParseURL(head.URL)
	if err != nil {
		return nil, err
	}
	headers, err := UpstreamHeaders(head.Headers)
	if err != nil {
		return nil, err
	}
	req := &nethttp.Request{
		Method:     method,
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     nethttp.Header{"User-Agent": {""}},
		Host:       u.Host,
	}
	for _, h := range headers {
		req.Header[textproto.CanonicalMIMEHeaderKey(h.Name)] = []string{latin1Bytes(h.Value)}
	}
	return req, nil
}
