package integration

import (
	"encoding/base64"
	wire "encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

type resolveResult struct {
	Addresses []string `json:"addresses"`
	TTL       int      `json:"ttl"`
	Extra     int      `json:"extra"`
	Shared    bool     `json:"shared"`
	Reset     string   `json:"reset"`
	Detail    string   `json:"detail"`
	Error     string   `json:"error"`
}

func (e *linkEnv) resolve(t *testing.T, name string, family int, shared bool) resolveResult {
	t.Helper()
	var r resolveResult
	e.p.eval("linkTest.resolve("+jsArgs(name, family, shared)+")", &r)
	if r.Error != "" {
		t.Fatalf("resolve %s: %s", name, r.Error)
	}
	return r
}

func (e *linkEnv) wantAddresses(t *testing.T, name string, family int, want ...string) {
	t.Helper()
	for _, shared := range []bool{true, false} {
		r := e.resolve(t, name, family, shared)
		if r.Reset != "" || r.TTL != 30 || r.Extra != 0 || r.Shared != shared || r.Addresses == nil {
			t.Fatalf("resolve %s/%d (shared=%v): %+v", name, family, shared, r)
		}
		got := slices.Clone(r.Addresses)
		slices.Sort(got)
		w := slices.Clone(want)
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Fatalf("resolve %s/%d: got %v, want %v", name, family, r.Addresses, want)
		}
	}
}

type httpResult struct {
	Status     int         `json:"status"`
	StatusText string      `json:"statusText"`
	Headers    [][2]string `json:"headers"`
	URL        string      `json:"url"`
	Shared     bool        `json:"shared"`
	Bytes      int         `json:"bytes"`
	SHA256     string      `json:"sha256"`
	Body64     *string     `json:"body64"`
	Reset      string      `json:"reset"`
	Detail     string      `json:"detail"`
	Error      string      `json:"error"`
}

func (r httpResult) body(t *testing.T) string {
	t.Helper()
	if r.Body64 == nil {
		t.Fatalf("no body kept: %+v", r)
	}
	b, err := base64.StdEncoding.DecodeString(*r.Body64)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r httpResult) header(name string) (string, bool) {
	for _, h := range r.Headers {
		if h[0] == name {
			return h[1], true
		}
	}
	return "", false
}

func (e *linkEnv) http(t *testing.T, req map[string]any, shared bool) httpResult {
	t.Helper()
	var r httpResult
	e.p.eval("linkTest.http("+jsArgs(req, shared)+")", &r)
	if r.Error != "" {
		t.Fatalf("http %v: %s", req["url"], r.Error)
	}
	return r
}

func (e *linkEnv) get(t *testing.T, url string, headers ...[2]string) httpResult {
	t.Helper()
	if headers == nil {
		headers = [][2]string{}
	}
	r := e.http(t, map[string]any{"url": url, "method": "GET", "headers": headers}, false)
	if r.Reset != "" {
		t.Fatalf("GET %s: reset %s %s", url, r.Reset, r.Detail)
	}
	return r
}

func TestLinkResolveAndHTTP(t *testing.T) {
	env := setupLink(t, false)
	up := newUpstream(t)
	t.Run("hello caps", env.testKindCaps)
	t.Run("resolve", env.testResolve)
	t.Run("http request headers", func(t *testing.T) { env.testRequestHeaders(t) })
	t.Run("http ranged requests", func(t *testing.T) { env.testRanged(t, up) })
	t.Run("http response headers", env.testResponseHeaders)
	t.Run("http codings", func(t *testing.T) { env.testCodings(t, up) })
	t.Run("http bodiless", func(t *testing.T) { env.testBodiless(t, up) })
	t.Run("http encoded partial", func(t *testing.T) { env.testEncodedPartial(t, up) })
	t.Run("http no redirect follow", func(t *testing.T) { env.testRedirect(t, up) })
	t.Run("http request bodies", func(t *testing.T) { env.testRequestBodies(t, up) })
	t.Run("http large bodies", func(t *testing.T) { env.testLargeBodies(t, up) })
	t.Run("http errors", func(t *testing.T) { env.testHTTPErrors(t, up) })
}

func (e *linkEnv) testKindCaps(t *testing.T) {
	var h struct {
		Caps []string `json:"caps"`
	}
	e.p.eval("linkTest.ready()", &h)
	if !slices.Equal(h.Caps, []string{"net", "http"}) {
		t.Fatalf("caps %v", h.Caps)
	}
}

func (e *linkEnv) testResolve(t *testing.T) {
	e.wantAddresses(t, "127.0.0.1", 0, "127.0.0.1")
	e.wantAddresses(t, "127.0.0.1", 4, "127.0.0.1")
	e.wantAddresses(t, "127.0.0.1", 6)
	e.wantAddresses(t, "::1", 6, "::1")
	e.wantAddresses(t, "[::1]", 0, "::1")
	e.wantAddresses(t, "::ffff:127.0.0.1", 4, "127.0.0.1")
	e.wantAddresses(t, "93.184.215.14", 0, "93.184.215.14")
	e.wantAddresses(t, "169.254.169.254", 0)
	e.wantAddresses(t, "fe80::1", 6)
	e.wantAddresses(t, "0.0.0.0", 0)
	e.wantAddresses(t, "no-such-host.invalid", 0)
	r := e.resolve(t, "localhost", 4, true)
	if !slices.Contains(r.Addresses, "127.0.0.1") {
		t.Fatalf("localhost: %+v", r)
	}
	for _, open := range []string{`{kind:'resolve'}`, `{kind:'resolve',name:'localhost',family:5}`, `{kind:'resolve',name:'',family:0}`} {
		for _, shared := range []bool{true, false} {
			var res echoResult
			e.p.eval("linkTest.open("+open+", "+fmt.Sprint(shared)+")", &res)
			if res.Reset != "EPROTO" {
				t.Fatalf("%s: %+v", open, res)
			}
		}
	}
}

func (e *linkEnv) testRequestHeaders(t *testing.T) {
	echo := "http://" + rawEcho(t)
	headers := [][2]string{
		{"Connection", "X-Hop, close"}, {"X-Hop", "1"}, {"Keep-Alive", "timeout=5"}, {"Proxy-Connection", "keep-alive"},
		{"TE", "trailers"}, {"Trailer", "X-T"}, {"Transfer-Encoding", "chunked"}, {"Upgrade", "websocket"},
		{"Host", "evil.example"}, {"Content-Length", "99"}, {"Accept-Encoding", "identity"}, {"Expect", "100-continue"},
		{"Proxy-Authorization", "Basic c2VjcmV0"}, {"X-Multi", "a"}, {"x-multi", "b"}, {"Cookie", "a=1"}, {"cookie", "b=2"},
		{"User-Agent", "curl/8.22.0"}, {"X-Latin", "caf\u00e9"}, {"X-Space", "  padded\t"}, {"Authorization", "Bearer t"},
	}
	for _, shared := range []bool{false, true} {
		r := e.http(t, map[string]any{"url": echo + "/path?q=1#frag", "method": "get", "headers": headers}, shared)
		if r.Reset != "" || r.Status != 200 || r.Shared != shared || r.URL != echo+"/path?q=1#frag" {
			t.Fatalf("shared=%v: %+v", shared, r)
		}
		h := parseEchoed(t, r.Body64)
		if h.line != "GET /path?q=1 HTTP/1.1" {
			t.Fatalf("request line %q", h.line)
		}
		for _, gone := range []string{"connection", "x-hop", "keep-alive", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade", "content-length", "expect", "proxy-authorization"} {
			if v, ok := h.headers[gone]; ok {
				t.Fatalf("%s was sent: %v", gone, v)
			}
		}
		want := map[string]string{
			"host":            strings.TrimPrefix(echo, "http://"),
			"x-multi":         "a, b",
			"cookie":          "a=1; b=2",
			"accept-encoding": "gzip, deflate, br",
			"user-agent":      "curl/8.22.0",
			"x-latin":         "caf\xe9",
			"x-space":         "padded",
			"authorization":   "Bearer t",
		}
		for name, v := range want {
			if got := h.headers[name]; len(got) != 1 || got[0] != v {
				t.Fatalf("%s: got %q, want %q (all %v)", name, got, v, h.headers)
			}
		}
	}
	r := e.get(t, echo+"/")
	if _, ok := parseEchoed(t, r.Body64).headers["user-agent"]; ok {
		t.Fatal("a User-Agent was added")
	}
}

func (e *linkEnv) testRanged(t *testing.T, up *upstream) {
	echo := "http://" + rawEcho(t)
	for _, h := range [][2]string{{"Range", "bytes=0-3"}, {"If-Range", `"x"`}, {"range", "bytes=1-"}} {
		r := e.get(t, echo+"/", h)
		if v, ok := parseEchoed(t, r.Body64).headers["accept-encoding"]; ok {
			t.Fatalf("%s: Accept-Encoding %v was sent", h[0], v)
		}
	}
	r := e.get(t, echo+"/", [2]string{"Connection", "range"}, [2]string{"Range", "bytes=0-3"})
	if v := parseEchoed(t, r.Body64).headers["accept-encoding"]; len(v) != 1 || v[0] != "gzip, deflate, br" {
		t.Fatalf("Range named by Connection: Accept-Encoding %v", v)
	}
	r = e.get(t, up.srv.URL+"/range", [2]string{"Range", "bytes=2-5"})
	cr, _ := r.header("content-range")
	seen, _ := r.header("x-seen-accept-encoding")
	if r.Status != 206 || r.body(t) != "2345" || cr != "bytes 2-5/16" || seen != "" {
		t.Fatalf("ranged: %+v", r)
	}
}

func (e *linkEnv) testResponseHeaders(t *testing.T) {
	addr := rawResponse(t, "HTTP/1.1 299 Fine Thing\r\n"+
		"Set-Cookie: a=1\r\n"+
		"X-B: 1\r\n"+
		"Connection: x-drop, keep-alive\r\n"+
		"X-Drop: gone\r\n"+
		"Keep-Alive: timeout=5\r\n"+
		"Proxy-Connection: keep-alive\r\n"+
		"Upgrade: foo\r\n"+
		"Set-Cookie: b=2, c=3\r\n"+
		"X-A: 1\r\n"+
		"X-A: 2\r\n"+
		"X-Latin: caf\xe9\r\n"+
		"Content-Length: 4\r\n\r\nbody")
	r := e.get(t, "http://"+addr+"/x")
	want := [][2]string{{"content-length", "4"}, {"x-a", "1, 2"}, {"x-b", "1"}, {"x-latin", "caf\u00e9"}, {"set-cookie", "a=1"}, {"set-cookie", "b=2, c=3"}}
	if r.Status != 299 || r.StatusText != "Fine Thing" || r.body(t) != "body" || r.URL != "http://"+addr+"/x" {
		t.Fatalf("response %+v", r)
	}
	if !slices.Equal(r.Headers, want) {
		t.Fatalf("headers %q, want %q", r.Headers, want)
	}
}

func (e *linkEnv) testCodings(t *testing.T, up *upstream) {
	plain := repeatPlain()
	for _, enc := range []string{"gzip", "x-gzip", "deflate", "deflate-raw", "br", "gzip,br", "br,deflate-raw", "deflate,gzip,br"} {
		for _, cl := range []string{"0", "1"} {
			r := e.get(t, up.srv.URL+"/coded?enc="+enc+"&cl="+cl)
			if r.Status != 200 || r.body(t) != plain {
				t.Fatalf("%s: %+v", enc, r)
			}
			for _, gone := range []string{"content-encoding", "content-length"} {
				if v, ok := r.header(gone); ok {
					t.Fatalf("%s (cl=%s): %s %q kept", enc, cl, gone, v)
				}
			}
		}
	}
	r := e.get(t, up.srv.URL+"/coded?enc=gzip&ce="+"GZip,%20identity&cl=1")
	if _, ok := r.header("content-encoding"); ok || r.body(t) != plain {
		t.Fatalf("gzip, identity: %+v", r)
	}
	r = e.get(t, up.srv.URL+"/coded?enc=gzip,zstd&cl=1&raw=1")
	ce, _ := r.header("content-encoding")
	cl, hasCL := r.header("content-length")
	raw, _ := r.header("x-raw-sha256")
	if ce != "gzip, zstd" || !hasCL || cl != fmt.Sprint(r.Bytes) || r.SHA256 != raw {
		t.Fatalf("undecodable stack: %+v", r)
	}
	r = e.get(t, up.srv.URL+"/coded?ce=identity&cl=1")
	if _, ok := r.header("content-encoding"); ok || r.body(t) != plain {
		t.Fatalf("identity: %+v", r)
	}
	if cl, _ := r.header("content-length"); cl != fmt.Sprint(len(plain)) {
		t.Fatalf("identity: content-length %q", cl)
	}
	r = e.get(t, up.srv.URL+"/coded?enc=gzip&empty=1")
	if _, ok := r.header("content-encoding"); ok || r.Bytes != 0 || r.Status != 200 {
		t.Fatalf("empty gzip: %+v", r)
	}
}

func (e *linkEnv) testBodiless(t *testing.T, up *upstream) {
	r := e.http(t, map[string]any{"url": up.srv.URL + "/coded?enc=gzip&cl=1", "method": "HEAD", "headers": [][2]string{}}, false)
	ce, _ := r.header("content-encoding")
	cl, _ := r.header("content-length")
	if r.Status != 200 || r.Bytes != 0 || ce != "gzip" || cl == "" || cl == "0" {
		t.Fatalf("HEAD: %+v", r)
	}
	for _, status := range []string{"204 No Content", "205 Reset Content", "304 Not Modified"} {
		addr := rawResponse(t, "HTTP/1.1 "+status+"\r\nContent-Encoding: gzip\r\nContent-Length: 20\r\nETag: \"e\"\r\n\r\n")
		r := e.get(t, "http://"+addr+"/")
		ce, _ := r.header("content-encoding")
		cl, _ := r.header("content-length")
		if r.Bytes != 0 || ce != "gzip" || (cl != "20" && !strings.HasPrefix(status, "204")) {
			t.Fatalf("%s: %+v", status, r)
		}
	}
}

func (e *linkEnv) testEncodedPartial(t *testing.T, up *upstream) {
	for _, shared := range []bool{false, true} {
		r := e.http(t, map[string]any{"url": up.srv.URL + "/partial", "method": "GET", "headers": [][2]string{{"Range", "bytes=0-3"}}}, shared)
		if r.Reset != "EPROTO" {
			t.Fatalf("encoded 206: %+v", r)
		}
	}
}

func (e *linkEnv) testRedirect(t *testing.T, up *upstream) {
	r := e.get(t, up.srv.URL+"/redirect")
	loc, _ := r.header("location")
	if r.Status != 302 || loc != "/target" || up.targetHits.Load() != 0 {
		t.Fatalf("redirect: %+v, target hits %d", r, up.targetHits.Load())
	}
}

type uploadResult struct {
	Method        string `json:"method"`
	Bytes         int    `json:"bytes"`
	SHA256        string `json:"sha256"`
	ContentLength int64  `json:"contentLength"`
	Chunked       bool   `json:"chunked"`
	Error         string `json:"error"`
}

func (e *linkEnv) upload(t *testing.T, req map[string]any, shared bool) uploadResult {
	t.Helper()
	r := e.http(t, req, shared)
	if r.Reset != "" || r.Status != 200 {
		t.Fatalf("upload: %+v", r)
	}
	var u uploadResult
	if err := json.Unmarshal([]byte(r.body(t)), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *linkEnv) testRequestBodies(t *testing.T, up *upstream) {
	small := base64.StdEncoding.EncodeToString([]byte("hello"))
	u := e.upload(t, map[string]any{"url": up.srv.URL + "/upload", "method": "post", "headers": [][2]string{}, "body64": small}, true)
	if u.Method != "POST" || u.Bytes != 5 || u.ContentLength != 5 || u.Chunked {
		t.Fatalf("small POST: %+v", u)
	}
	u = e.upload(t, map[string]any{"url": up.srv.URL + "/upload", "method": "GET", "headers": [][2]string{}, "body64": small}, false)
	if u.Bytes != 0 || u.ContentLength != 0 {
		t.Fatalf("GET with a body: %+v", u)
	}
	u = e.upload(t, map[string]any{"url": up.srv.URL + "/upload", "method": "PATCH", "headers": [][2]string{}, "body64": ""}, false)
	if u.Method != "PATCH" || u.Bytes != 0 || u.Chunked {
		t.Fatalf("empty PATCH: %+v", u)
	}
	size := 5<<20 + 3
	u = e.upload(t, map[string]any{"url": up.srv.URL + "/upload", "method": "PUT", "headers": [][2]string{{"Content-Length", fmt.Sprint(size)}}, "prng": map[string]any{"size": size, "seed": 7}}, false)
	if u.Bytes != size || u.ContentLength != int64(size) || u.Chunked || u.SHA256 != xorshiftSum(size, 7) {
		t.Fatalf("PUT with Content-Length: %+v", u)
	}
}

func (e *linkEnv) testLargeBodies(t *testing.T, up *upstream) {
	size := 260<<20 + 1
	u := e.upload(t, map[string]any{"url": up.srv.URL + "/upload", "method": "POST", "headers": [][2]string{}, "prng": map[string]any{"size": size, "seed": 99}}, false)
	if u.Bytes != size || !u.Chunked || u.SHA256 != xorshiftSum(size, 99) || u.Error != "" {
		t.Fatalf("large POST: %+v", u)
	}
	var r httpResult
	e.p.eval("linkTest.http("+jsArgs(map[string]any{"url": up.srv.URL + "/big", "method": "GET", "headers": [][2]string{}}, false, 0)+")", &r)
	if r.Status != 200 || r.Bytes != len(up.big) || r.SHA256 != up.bigSum {
		t.Fatalf("large GET: %+v", r)
	}
}

func headBytes(n uint32, payload string) string {
	b := wire.BigEndian.AppendUint32(nil, n)
	return base64.StdEncoding.EncodeToString(append(b, payload...))
}

func (e *linkEnv) testHTTPErrors(t *testing.T, up *upstream) {
	closed := "http://" + e.closed + "/"
	cases := []struct {
		name   string
		req    map[string]any
		code   string
		detail string
	}{
		{"refused", map[string]any{"url": closed, "method": "GET", "headers": [][2]string{}}, "ECONNREFUSED", "fetch failed: "},
		{"not found", map[string]any{"url": "http://no-such-host.invalid/", "method": "GET", "headers": [][2]string{}}, "ENOTFOUND", "fetch failed: "},
		{"link-local", map[string]any{"url": "http://169.254.169.254/latest/meta-data/", "method": "GET", "headers": [][2]string{}}, "EACCES", "fetch failed: "},
		{"debugging port", map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%d/json/version", e.b.port), "method": "GET", "headers": [][2]string{}}, "EACCES", "policy"},
		{"untrusted TLS", map[string]any{"url": up.tls.URL + "/target", "method": "GET", "headers": [][2]string{}}, "ECONNREFUSED", "certificate"},
		{"ftp url", map[string]any{"url": "ftp://127.0.0.1/", "method": "GET", "headers": [][2]string{}}, "EPROTO", ""},
		{"credentials", map[string]any{"url": "http://u:p@127.0.0.1/", "method": "GET", "headers": [][2]string{}}, "EPROTO", ""},
		{"bad url", map[string]any{"url": "not a url", "method": "GET", "headers": [][2]string{}}, "EPROTO", ""},
		{"bad method", map[string]any{"url": up.srv.URL, "method": "GE T", "headers": [][2]string{}}, "EPROTO", ""},
		{"CONNECT", map[string]any{"url": up.srv.URL, "method": "connect", "headers": [][2]string{}}, "EPROTO", ""},
		{"bad pair", map[string]any{"url": up.srv.URL, "method": "GET", "headers": [][]string{{"a"}}}, "EPROTO", ""},
		{"bad header name", map[string]any{"url": up.srv.URL, "method": "GET", "headers": [][2]string{{"a b", "1"}}}, "EPROTO", ""},
		{"newline in value", map[string]any{"url": up.srv.URL, "method": "GET", "headers": [][2]string{{"X-A", "1\r\nX-B: 2"}}}, "EPROTO", ""},
		{"past latin-1", map[string]any{"url": up.srv.URL, "method": "GET", "headers": [][2]string{{"X-A", "\u0100"}}}, "EPROTO", ""},
		{"null headers", map[string]any{"url": up.srv.URL, "method": "GET", "headers": nil}, "EPROTO", ""},
		{"not JSON", map[string]any{"head64": headBytes(8, "not json")}, "EPROTO", ""},
		{"huge head", map[string]any{"head64": headBytes(0xffffffff, "")}, "EPROTO", ""},
		{"short head", map[string]any{"head64": headBytes(100, "{}")}, "EPROTO", ""},
	}
	for _, c := range cases {
		for _, shared := range []bool{false, true} {
			r := e.http(t, c.req, shared)
			if r.Reset != c.code || !strings.Contains(r.Detail, c.detail) {
				t.Fatalf("%s (shared=%v): %+v, want %s with %q", c.name, shared, r, c.code, c.detail)
			}
		}
	}
}

func followExitKinds(t *testing.T, p *page) {
	t.Helper()
	up := newUpstream(t)
	env := &linkEnv{t: t, p: p}
	env.wantAddresses(t, "127.0.0.1", 0, "127.0.0.1")
	env.wantAddresses(t, "10.0.0.1", 0)
	env.wantAddresses(t, "127.0.0.2", 4)
	env.wantAddresses(t, "ref.link.test", 0, "127.0.0.1")
	r := env.get(t, "http://ref.link.test:"+portOf(t, up.srv.URL)+"/target")
	if r.Status != 200 || r.body(t) != "followed" {
		t.Fatalf("follow http by hosts name: %+v", r)
	}
	r = env.get(t, up.srv.URL+"/coded?enc=gzip&cl=1")
	if r.Status != 200 || r.body(t) != repeatPlain() {
		t.Fatalf("follow http: %+v", r)
	}
	r = env.http(t, map[string]any{"url": "http://127.0.0.2:" + portOf(t, up.srv.URL) + "/", "method": "GET", "headers": [][2]string{}}, true)
	if r.Reset != "EACCES" {
		t.Fatalf("follow http outside the allow rule: %+v", r)
	}
}
