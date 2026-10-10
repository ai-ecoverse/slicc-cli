package integration

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	wire "encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

const plainText = "hello coded world, "

type upstream struct {
	srv        *httptest.Server
	tls        *httptest.Server
	h2         *httptest.Server
	big        []byte
	bigSum     string
	targetHits atomic.Int32
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{big: make([]byte, 32<<20)}
	_, _ = rand.Read(u.big)
	sum := sha256.Sum256(u.big)
	u.bigSum = hex.EncodeToString(sum[:])
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", u.upload)
	mux.HandleFunc("/coded", u.coded)
	mux.HandleFunc("/range", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Accept-Encoding", r.Header.Get("Accept-Encoding"))
		http.ServeContent(w, r, "range.txt", time.Unix(1700000000, 0), strings.NewReader("0123456789abcdef"))
	})
	mux.HandleFunc("/partial", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Range", "bytes 0-3/16")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(encode(t, []byte("0123"), "gzip"))
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/target", http.StatusFound)
	})
	mux.HandleFunc("/target", func(w http.ResponseWriter, r *http.Request) {
		u.targetHits.Add(1)
		_, _ = io.WriteString(w, "followed")
	})
	mux.HandleFunc("/proto", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(encode(t, []byte(r.Proto+" "+r.Header.Get("Cookie")), "gzip"))
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(u.big)))
		_, _ = w.Write(u.big)
	})
	u.srv = httptest.NewServer(mux)
	u.tls = httptest.NewTLSServer(mux)
	u.h2 = httptest.NewUnstartedServer(mux)
	u.h2.EnableHTTP2 = true
	u.h2.StartTLS()
	t.Cleanup(u.srv.Close)
	t.Cleanup(u.tls.Close)
	t.Cleanup(u.h2.Close)
	return u
}

func (u *upstream) upload(w http.ResponseWriter, r *http.Request) {
	h := sha256.New()
	n, err := io.Copy(h, r.Body)
	chunked := len(r.TransferEncoding) > 0 && r.TransferEncoding[0] == "chunked"
	_, _ = fmt.Fprintf(w, `{"method":%q,"bytes":%d,"sha256":%q,"contentLength":%d,"chunked":%v,"error":%q}`,
		r.Method, n, hex.EncodeToString(h.Sum(nil)), r.ContentLength, chunked, errString(err))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (u *upstream) coded(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	body := []byte(strings.Repeat(plainText, 200))
	if q.Get("empty") == "1" {
		body = nil
	}
	var names []string
	for _, c := range strings.Split(q.Get("enc"), ",") {
		if c == "" {
			continue
		}
		if len(body) > 0 {
			body = encode(nil, body, c)
		}
		names = append(names, strings.TrimSuffix(c, "-raw"))
	}
	ce := strings.Join(names, ", ")
	if q.Has("ce") {
		ce = q.Get("ce")
	}
	w.Header().Set("Content-Type", "text/plain")
	if ce != "" {
		w.Header().Set("Content-Encoding", ce)
	}
	if q.Get("cl") == "1" {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	}
	if q.Get("raw") == "1" {
		w.Header().Set("X-Raw-Sha256", sha256Hex(body))
	}
	_, _ = w.Write(body)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func encode(t *testing.T, data []byte, coding string) []byte {
	var buf bytes.Buffer
	var w io.WriteCloser
	switch coding {
	case "gzip", "x-gzip":
		w = gzip.NewWriter(&buf)
	case "deflate":
		w = zlib.NewWriter(&buf)
	case "deflate-raw":
		w, _ = flate.NewWriter(&buf, flate.DefaultCompression)
	case "br":
		w = brotli.NewWriter(&buf)
	default:
		return append([]byte(coding+":"), data...)
	}
	if _, err := w.Write(data); err != nil && t != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	return buf.Bytes()
}

func readRequestHead(c net.Conn) []byte {
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(c)
	var head []byte
	for {
		line, err := br.ReadBytes('\n')
		head = append(head, line...)
		if err != nil || string(line) == "\r\n" {
			return head
		}
	}
}

func rawEcho(t *testing.T) string {
	return serveTCP(t, func(c net.Conn) {
		head := readRequestHead(c)
		_, _ = fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(head))
		_, _ = c.Write(head)
	})
}

func rawResponse(t *testing.T, response string) string {
	return serveTCP(t, func(c net.Conn) {
		readRequestHead(c)
		_, _ = io.WriteString(c, response)
	})
}

type echoedHead struct {
	line    string
	headers map[string][]string
}

func parseEchoed(t *testing.T, body64 *string) echoedHead {
	t.Helper()
	if body64 == nil {
		t.Fatal("no body")
	}
	raw, err := base64.StdEncoding.DecodeString(*body64)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\r\n\r\n"), "\r\n")
	h := echoedHead{line: lines[0], headers: map[string][]string{}}
	for _, l := range lines[1:] {
		name, value, _ := strings.Cut(l, ":")
		h.headers[strings.ToLower(name)] = append(h.headers[strings.ToLower(name)], strings.TrimSpace(value))
	}
	return h
}

func xorshiftSum(size int, seed uint32) string {
	h := sha256.New()
	block := make([]byte, 65536)
	x := seed
	for left := size; left > 0; left -= len(block) {
		for i := 0; i < len(block); i += 4 {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			wire.LittleEndian.PutUint32(block[i:], x)
		}
		h.Write(block[:min(left, len(block))])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func repeatPlain() string { return strings.Repeat(plainText, 200) }

func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(rawURL, "http://"), "https://"))
	if err != nil {
		t.Fatal(err)
	}
	return port
}
