package http

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	nethttp "net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
)

type ResponseHead struct {
	Status     int         `json:"status"`
	StatusText string      `json:"statusText"`
	Headers    [][2]string `json:"headers"`
	URL        string      `json:"url"`
}

var decodable = []string{"gzip", "x-gzip", "deflate", "br"}

func HeaderList(h nethttp.Header) [][2]string {
	names := make([]string, 0, len(h))
	byName := map[string][]string{}
	for k, vs := range h {
		lower := strings.ToLower(k)
		if _, ok := byName[lower]; !ok {
			names = append(names, lower)
		}
		byName[lower] = append(byName[lower], vs...)
	}
	sort.Strings(names)
	out := make([][2]string, 0, len(names))
	for _, name := range names {
		if name == "set-cookie" {
			continue
		}
		vs := make([]string, len(byName[name]))
		for i, v := range byName[name] {
			vs[i] = latin1String(v)
		}
		out = append(out, [2]string{name, strings.Join(vs, ", ")})
	}
	for _, v := range byName["set-cookie"] {
		out = append(out, [2]string{"set-cookie", latin1String(v)})
	}
	return out
}

func HasBody(method string, status int) bool {
	return !strings.EqualFold(method, "HEAD") && status/100 != 1 && status != 204 && status != 205 && status != 304
}

func joined(list [][2]string, name string) string {
	var vs []string
	for _, h := range list {
		if h[0] == name {
			vs = append(vs, h[1])
		}
	}
	return strings.Join(vs, ",")
}

func Codings(encoding string) ([]string, bool) {
	var codings []string
	for _, c := range strings.Split(encoding, ",") {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" || c == "identity" {
			continue
		}
		codings = append(codings, c)
	}
	if len(codings) == 0 {
		return nil, false
	}
	for _, c := range codings {
		if !contains(decodable, c) {
			return codings, false
		}
	}
	return codings, true
}

func DecodedPartial(status int, list [][2]string) bool {
	_, decoded := Codings(joined(list, "content-encoding"))
	return status == 206 && decoded
}

func ResponseHeaders(method string, status int, list [][2]string) ([][2]string, []string) {
	named := map[string]bool{}
	for _, t := range strings.Split(joined(list, "connection"), ",") {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			named[t] = true
		}
	}
	kept := make([][2]string, 0, len(list))
	for _, h := range list {
		if !contains(hopByHop, h[0]) && !named[h[0]] {
			kept = append(kept, h)
		}
	}
	if !HasBody(method, status) {
		return kept, nil
	}
	encoding := joined(list, "content-encoding")
	codings, decoded := Codings(encoding)
	identity := strings.ToLower(strings.TrimSpace(joined(kept, "content-encoding"))) == "identity"
	out := kept[:0:0]
	for _, h := range kept {
		switch {
		case h[0] == "content-length" && decoded:
		case h[0] == "content-encoding" && (decoded || identity):
		default:
			out = append(out, h)
		}
	}
	if !decoded {
		codings = nil
	}
	return out, codings
}

func StatusText(resp *nethttp.Response) string {
	code := strconv.Itoa(resp.StatusCode)
	text := strings.TrimPrefix(strings.TrimPrefix(resp.Status, code), " ")
	if resp.Status == "" {
		text = nethttp.StatusText(resp.StatusCode)
	}
	return latin1String(text)
}

func Decode(r io.Reader, codings []string) io.Reader {
	for i := len(codings) - 1; i >= 0; i-- {
		r = &lazy{src: bufio.NewReader(r), coding: codings[i]}
	}
	return r
}

type lazy struct {
	src    *bufio.Reader
	coding string
	r      io.Reader
	err    error
}

func (l *lazy) Read(p []byte) (int, error) {
	if l.r == nil && l.err == nil {
		l.r, l.err = l.open()
	}
	if l.err != nil {
		return 0, l.err
	}
	return l.r.Read(p)
}

func (l *lazy) open() (io.Reader, error) {
	head, err := l.src.Peek(2)
	if len(head) == 0 {
		if err == nil || errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}
	switch l.coding {
	case "gzip", "x-gzip":
		return gzip.NewReader(l.src)
	case "deflate":
		if len(head) == 2 && head[0]&0x0f == 8 && (uint16(head[0])<<8|uint16(head[1]))%31 == 0 {
			return zlib.NewReader(l.src)
		}
		return flate.NewReader(l.src), nil
	case "br":
		return brotli.NewReader(l.src), nil
	}
	return l.src, nil
}
