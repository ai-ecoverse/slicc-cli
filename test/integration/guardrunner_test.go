package integration

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
)

const (
	guardNPMPackage = "@ai-ecoverse/slicc-browsers"
	guardGoModule   = "github.com/ai-ecoverse/slicc-browsers"
)

var (
	guardExactVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	guardSettleToken  = regexp.MustCompile(`(?i)settle:\s*no request for .+ within \d+ms`)
)

func guardSetVersion(t testing.TB) string {
	t.Helper()
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatalf("package.json: %v", err)
	}
	npm := pkg.DevDependencies[guardNPMPackage]
	if !guardExactVersion.MatchString(npm) {
		t.Fatalf("package.json pins %s at %q, want an exact x.y.z version", guardNPMPackage, npm)
	}
	b, err = os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	var gomod []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "require "))
		if len(f) >= 2 && f[0] == guardGoModule {
			gomod = append(gomod, f[1])
		}
	}
	if len(gomod) != 1 {
		t.Fatalf("go.mod requires %s %d times (%v), want once", guardGoModule, len(gomod), gomod)
	}
	if gomod[0] != "v"+npm {
		t.Fatalf("package.json pins %s@%s but go.mod requires %s %s; move both pins to the same release", guardNPMPackage, npm, guardGoModule, gomod[0])
	}
	return npm
}

func guardSettleScale() int {
	switch runtime.GOOS {
	case "linux":
		return 1
	case "darwin":
		return 2
	}
	return 3
}

func guardSlots() int {
	return min(10, max(4, 2*runtime.NumCPU()))
}

func guardPackage(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "node_modules", "@ai-ecoverse", "slicc-browsers"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("@ai-ecoverse/slicc-browsers is missing; run npm ci")
		}
		t.Skip("@ai-ecoverse/slicc-browsers is not installed; run npm ci")
	}
	var pkg struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(b, &pkg)
	if want := guardSetVersion(t); pkg.Version != want {
		t.Fatalf("node_modules has %s@%s, but package.json pins %s; run npm ci", guardNPMPackage, pkg.Version, want)
	}
	b, err = os.ReadFile(filepath.Join(dir, "guard-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var npm, gomod struct {
		Vectors []struct {
			ID string `json:"id"`
		} `json:"vectors"`
	}
	_ = json.Unmarshal(b, &npm)
	_ = json.Unmarshal(sliccbrowsers.GuardVectorsJSON(), &gomod)
	if len(npm.Vectors) == 0 || fmt.Sprint(npm.Vectors) != fmt.Sprint(gomod.Vectors) {
		t.Fatalf("the npm and Go pins of slicc-browsers carry different guard vectors")
	}
	return dir
}

type guardFixture struct {
	port int
	log  int
}

type idpTLS struct {
	port string
	spki string
}

func idpTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ims-na1.adobelogin.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{
			"ims-na1.adobelogin.com",
			"ims-na1-stg1.adobelogin.com",
			"adobelogin.com",
			"adobe.com",
			"www.adobe.com",
			"localhost",
		},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func certSPKI(t *testing.T, leaf tls.Certificate) string {
	t.Helper()
	parsed, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(parsed.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pub)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func fixtureProxy(fixturePort int) http.Handler {
	upstream := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(fixturePort)}
	return &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(upstream)
		r.Out.Host = r.In.Host
	}}
}

func startIdPTLS(t *testing.T, fixturePort int, leaf tls.Certificate) idpTLS {
	t.Helper()
	srv := httptest.NewUnstartedServer(fixtureProxy(fixturePort))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{leaf}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil || u.Port() == "" {
		t.Fatalf("IdP TLS URL %q: %v", srv.URL, err)
	}
	return idpTLS{port: u.Port(), spki: certSPKI(t, leaf)}
}

type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

type onceListener struct {
	addr net.Addr
	c    net.Conn
	once sync.Once
}

func (l *onceListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = l.c })
	if c == nil {
		return nil, net.ErrClosed
	}
	return c, nil
}

func (l *onceListener) Close() error { return nil }

func (l *onceListener) Addr() net.Addr { return l.addr }

func startDualFrontDoor(t *testing.T, fixturePort int, leaf tls.Certificate) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	handler := fixtureProxy(fixturePort)
	httpSrv := &http.Server{Handler: handler}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{leaf}, MinVersion: tls.VersionTLS12}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveDualFrontDoor(c, httpSrv, tlsCfg)
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil || port == "" {
		t.Fatalf("dual front door addr %q: %v", ln.Addr(), err)
	}
	return port
}

func serveDualFrontDoor(c net.Conn, httpSrv *http.Server, tlsCfg *tls.Config) {
	br := bufio.NewReader(c)
	peek, err := br.Peek(1)
	pc := &peekConn{Conn: c, r: br}
	if err != nil {
		_ = c.Close()
		return
	}
	conn := net.Conn(pc)
	if peek[0] == 0x16 {
		tc := tls.Server(pc, tlsCfg)
		if err := tc.Handshake(); err != nil {
			_ = tc.Close()
			return
		}
		conn = tc
	}
	_ = httpSrv.Serve(&onceListener{addr: c.LocalAddr(), c: conn})
}

func stageCredentialExportProbeNotes(t *testing.T, src string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "credential-export.mjs")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	noteHook := "const note = (label, evidence) => notes.push(`${label}: ${JSON.stringify(evidence)}`);\n"
	noteInject := "const note = (label, evidence) => notes.push(`${label}: ${JSON.stringify(evidence)}`);\n  globalThis.__credProbeNote = note;\n"
	if !strings.Contains(text, noteHook) {
		t.Fatal("credential-export.mjs note hook shape changed; update probe notes")
	}
	text = strings.Replace(text, noteHook, noteInject, 1)
	topNeedle := "const topSession = attach.result?.sessionId;\n  if (!topSession) return { ok: false, storageKey: null, reportedKey: null, topId };\n"
	topInject := topNeedle + `  if (typeof globalThis.__credProbeNote === 'function') {
    const info = await outcome(reader.send('Target.getTargetInfo', { targetId: topId }));
    const got = String(info.result?.targetInfo?.url ?? '');
    const asked = String(topUrl);
    globalThis.__credProbeNote('diag.topTarget', {
      askedScheme: asked.startsWith('https:') ? 'https' : asked.startsWith('http:') ? 'http' : 'other',
      askedHost: asked.replace(/^https?:\/\//, '').split('/')[0].split(':')[0],
      gotScheme: got.startsWith('https:') ? 'https' : got.startsWith('http:') ? 'http' : got.startsWith('chrome-error:') ? 'chrome-error' : 'other',
      gotHost: got.replace(/^https?:\/\//, '').split('/')[0].split(':')[0],
      title: String(info.result?.targetInfo?.title ?? '').slice(0, 80),
    });
  }
`
	if !strings.Contains(text, topNeedle) {
		t.Fatal("credential-export.mjs plantPartitionedInTop shape changed; update probe notes")
	}
	text = strings.Replace(text, topNeedle, topInject, 1)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func startGuardFixture(t *testing.T) guardFixture {
	t.Helper()
	cmd := exec.Command("node", filepath.Join("testdata", "guard", "fixture.mjs"))
	cmd.Env = realEnv
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		reap(t, cmd, "the guard fixture")
	})
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(stdout).ReadString('\n')
		line <- s
	}()
	var f struct {
		Fixture int `json:"fixture"`
		Log     int `json:"log"`
	}
	select {
	case s := <-line:
		if err := json.Unmarshal([]byte(s), &f); err != nil || f.Fixture == 0 || f.Log == 0 {
			t.Fatalf("the guard fixture didn't start: %q %s", s, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the guard fixture didn't start: %s", stderr.String())
	}
	return guardFixture{port: f.Fixture, log: f.Log}
}

func guardPageServer(t *testing.T, pkg string, fixturePort int) *httptest.Server {
	t.Helper()
	root := filepath.Join("testdata", "link")
	files := http.FileServer(http.Dir(root))
	vendor := http.StripPrefix("/vendor/slicc-browsers/", http.FileServer(http.Dir(pkg)))
	upstream := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(fixturePort)}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(upstream)
		r.Out.Host = r.In.Host
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/vendor/slicc-browsers/") {
			if strings.HasSuffix(r.URL.Path, ".mjs") {
				w.Header().Set("Content-Type", "text/javascript")
			}
			vendor.ServeHTTP(w, r)
			return
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path.Clean(r.URL.Path)), "index.html")); r.URL.Path == "/" && err == nil {
			files.ServeHTTP(w, r)
			return
		}
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(path.Clean(r.URL.Path)))); err == nil && !st.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type guardRow struct {
	ID          string          `json:"id"`
	Target      string          `json:"target"`
	Variant     string          `json:"variant"`
	Transport   string          `json:"transport"`
	Via         string          `json:"via"`
	Control     string          `json:"control"`
	Guarded     string          `json:"guarded"`
	Live        string          `json:"live"`
	Reason      string          `json:"reason"`
	Why         json.RawMessage `json:"why"`
	Notes       []string        `json:"notes"`
	Errors      []string        `json:"errors"`
	Observed    json.RawMessage `json:"observations"`
	Diagnostics json.RawMessage `json:"diagnostics,omitempty"`
}

type guardSummary struct {
	Set             string          `json:"set"`
	Pass            bool            `json:"pass"`
	Results         []guardRow      `json:"results"`
	Missing         json.RawMessage `json:"missing"`
	NotRun          []string        `json:"notRun"`
	ObserverSkipped int             `json:"observerSkipped"`
	Counts          json.RawMessage `json:"counts"`
}

const (
	guardRowTimeout = 3 * time.Minute
	guardStall      = 7 * time.Minute
)

type guardCall struct {
	Method  string `json:"method"`
	Session string `json:"session"`
	AgeMS   int    `json:"ageMs"`
}

type guardInflight struct {
	ID      string                 `json:"id"`
	Target  string                 `json:"target"`
	Variant string                 `json:"variant"`
	Leg     string                 `json:"leg"`
	Phase   string                 `json:"phase"`
	Step    *int                   `json:"step"`
	MS      int                    `json:"ms"`
	Clients map[string][]guardCall `json:"clients"`
}

func (r guardInflight) String() string {
	out := fmt.Sprintf("%s %s %s running %s", r.ID, r.Target, r.Variant, time.Duration(r.MS)*time.Millisecond)
	if r.Leg != "" {
		out += fmt.Sprintf(", %s leg, %s", r.Leg, r.Phase)
		if r.Step != nil {
			out += fmt.Sprintf(" step %d", *r.Step)
		}
	}
	names := make([]string, 0, len(r.Clients))
	for name := range r.Clients {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		calls := r.Clients[name]
		if len(calls) == 0 {
			out += fmt.Sprintf("; %s: no pending call", name)
			continue
		}
		for _, c := range calls {
			out += fmt.Sprintf("; %s: %s (session %q) pending for %s", name, c.Method, c.Session, time.Duration(c.AgeMS)*time.Millisecond)
		}
	}
	return out
}

type guardProgress struct {
	Done     bool            `json:"done"`
	Error    string          `json:"error"`
	Rows     []guardRow      `json:"rows"`
	Inflight []guardInflight `json:"inflight"`
	Summary  *guardSummary   `json:"summary"`
}

func guardSHA() string {
	out, err := exec.Command("git", "-C", filepath.Join("..", ".."), "rev-parse", "HEAD").Output()
	if err == nil && len(strings.TrimSpace(string(out))) >= 7 {
		return strings.TrimSpace(string(out))[:7]
	}
	if s := os.Getenv("GITHUB_SHA"); len(s) >= 7 {
		return s[:7]
	}
	return "unknown"
}

type guardVector struct {
	ID      string     `json:"id"`
	Kind    string     `json:"kind"`
	Targets []string   `json:"targets"`
	Skip    *guardSkip `json:"skip"`
}

type guardSkip struct {
	Target string `json:"target"`
	Reason string `json:"reason"`
}

const guardC9MacReason = "not-applicable: on macOS the C9 controls hang inside Chrome (#344)"

var guardWaivers = map[string]string{
	"C3.tab-attach": "guarded settle of window.open under main empty-URL hold; runner folds that into control=fail (#260); stock Playwright/Puppeteer popup through the link guard passes",
}

func waivedGuardRow(r guardRow) (string, bool) {
	reason, ok := guardWaivers[r.ID]
	if !ok {
		return "", false
	}
	switch r.ID {
	case "C3.tab-attach":
		if r.Guarded != "fail" || (r.Control != "fail" && r.Control != "incomplete") {
			return "", false
		}
		if !guardedSettleFailure(r) {
			return "", false
		}
		return reason, true
	default:
		return "", false
	}
}

func guardedSettleFailure(r guardRow) bool {
	return guardSettleToken.MatchString(string(r.Why) + " " + strings.Join(r.Errors, " "))
}

func logTabIframeLeakEvidence(t testing.TB, r guardRow) {
	t.Helper()
	var obs struct {
		Control map[string]any `json:"control"`
		Guarded map[string]any `json:"guarded"`
	}
	_ = json.Unmarshal(r.Observed, &obs)
	var diag struct {
		IframeLeak map[string]any `json:"iframeLeak"`
	}
	_ = json.Unmarshal(r.Diagnostics, &diag)
	leak, _ := json.Marshal(diag.IframeLeak)
	t.Logf("C3.tab-iframe-leak verdict: id=%s target=%s variant=%s control=%s guarded=%s live=%s control.iframeAttached=%v guarded.iframeAttached=%v iframeLeak=%s observed=%s",
		r.ID, r.Target, r.Variant, r.Control, r.Guarded, r.Live, obs.Control["iframeAttached"], obs.Guarded["iframeAttached"], leak, r.Observed)
}

type tabIframeLeakEvidence struct {
	IframeAttached bool  `json:"iframeAttached"`
	FrameScrubbed  bool  `json:"frameScrubbed"`
	AttachRefused  bool  `json:"attachRefused"`
	Frames         []any `json:"frames"`
	Attached       []any `json:"attached"`
	Listings       []any `json:"listings"`
	Sessions       []any `json:"sessions"`
	AttachAttempts []any `json:"attachAttempts"`
}

func tabIframeLeakFromDiagnostics(raw json.RawMessage) (tabIframeLeakEvidence, error) {
	var wrap struct {
		IframeLeak tabIframeLeakEvidence `json:"iframeLeak"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return tabIframeLeakEvidence{}, err
	}
	return wrap.IframeLeak, nil
}

func tabIframeLeakObserverMissesFrame(diagnostics json.RawMessage) bool {
	ev, err := tabIframeLeakFromDiagnostics(diagnostics)
	return err == nil && len(ev.Frames) == 0
}

func tabIframeLeakGuardedHidesFrame(observed, diagnostics json.RawMessage) error {
	var sides map[string]map[string]any
	if err := json.Unmarshal(observed, &sides); err != nil {
		return err
	}
	controlAttached, _ := sides["control"]["iframeAttached"].(bool)
	guardedAttached, _ := sides["guarded"]["iframeAttached"].(bool)
	if !controlAttached {
		return fmt.Errorf("control must see the cross-site frame attach: iframeAttached=%v", controlAttached)
	}
	if guardedAttached {
		return fmt.Errorf("guarded stock iframeAttached must be false: %v", guardedAttached)
	}
	ev, err := tabIframeLeakFromDiagnostics(diagnostics)
	if err != nil {
		return err
	}
	if len(ev.Frames) == 0 {
		return fmt.Errorf("unguarded observer never saw the framed target (incomplete)")
	}
	if ev.IframeAttached || len(ev.Attached) != 0 || len(ev.Sessions) != 0 {
		return fmt.Errorf("guarded attached or opened a session for the framed target: attached=%v sessions=%v", ev.Attached, ev.Sessions)
	}
	if len(ev.Listings) != 0 && !ev.FrameScrubbed {
		return fmt.Errorf("guarded listings must show only the bare origin: scrubbed=%v listings=%v", ev.FrameScrubbed, ev.Listings)
	}
	if !ev.AttachRefused || len(ev.AttachAttempts) == 0 {
		return fmt.Errorf("Target.attachToTarget must be refused: refused=%v attempts=%v", ev.AttachRefused, ev.AttachAttempts)
	}
	return nil
}

func guardVectorSet() []guardVector {
	var set struct {
		Vectors []guardVector `json:"vectors"`
	}
	_ = json.Unmarshal(sliccbrowsers.GuardVectorsJSON(), &set)
	return set.Vectors
}

func guardSkipsOn(goos string, vectors []guardVector) map[string]guardSkip {
	skips := map[string]guardSkip{}
	if goos != "darwin" {
		return skips
	}
	for _, v := range vectors {
		if v.Kind == "cdp" && strings.HasPrefix(v.ID, "C9.") && len(v.Targets) == 1 && v.Skip == nil {
			skips[v.ID] = guardSkip{Target: v.Targets[0], Reason: guardC9MacReason}
		}
	}
	return skips
}

var guardSkips = guardSkipsOn(runtime.GOOS, guardVectorSet())

func TestCDPGuardVectorsSkipC9OnlyOnMacOS(t *testing.T) {
	vectors := guardVectorSet()
	c9 := 0
	for _, goos := range []string{"darwin", "linux", "windows"} {
		skips := guardSkipsOn(goos, vectors)
		mac := goos == "darwin"
		for _, v := range vectors {
			if v.Kind != "cdp" {
				continue
			}
			want := mac && strings.HasPrefix(v.ID, "C9.")
			if want {
				c9++
				if len(v.Targets) != 1 || v.Skip != nil {
					t.Errorf("%s: targets %v and skip %+v can't take a per-vector skip", v.ID, v.Targets, v.Skip)
				}
			}
			skip, got := skips[v.ID]
			if got != want {
				t.Errorf("%s: %s skipped %v, want %v", goos, v.ID, got, want)
			}
			if got && (skip.Target != v.Targets[0] || skip.Reason != guardC9MacReason) {
				t.Errorf("%s: %s skip %+v", goos, v.ID, skip)
			}
		}
	}
	if c9 == 0 {
		t.Error("the guard set has no C9 vector")
	}
}

type guardRun struct {
	variant        string
	slots          int
	only           string
	inject         string
	rowTimeout     time.Duration
	errorPageVault bool
}

func runGuardVectors(t *testing.T, variant string) {
	sum := runGuard(t, guardRun{variant: variant, slots: guardSlots(), rowTimeout: guardRowTimeout})
	if path, err := writeGuardReport(filepath.Join("..", "..", "artifacts", "guard"), variant, sum); err != nil {
		t.Logf("the guard report wasn't written: %v", err)
	} else {
		t.Logf("guard report: %s", path)
	}
	checkGuardSummary(t, sum)
	t.Logf("%s %s: counts=%s", sum.Set, variant, sum.Counts)
}

func writeGuardReport(dir, variant string, sum *guardSummary) (string, error) {
	b, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "guard-report-"+variant+".json")
	return path, os.WriteFile(path, append(b, '\n'), 0o644)
}

func runGuard(t *testing.T, run guardRun) *guardSummary {
	t.Helper()
	if len(guardSkips) > 0 {
		t.Logf("skipping %d C9 vectors: %s", len(guardSkips), guardC9MacReason)
	}
	pkg := stageCredentialExportProbeNotes(t, guardPackage(t))
	ext := filepath.Join("..", "..", "node_modules", "@ai-ecoverse", "slicc-shared-web", "harness", "extension")
	if _, err := os.Stat(filepath.Join(ext, "manifest.json")); err != nil {
		t.Fatalf("slicc-shared-web's harness extension is missing; run npm ci: %v", err)
	}
	ext = stage1GuardFixture(t, ext)
	fix := startGuardFixture(t)
	leaf := idpTLSCertificate(t)
	front := startDualFrontDoor(t, fix.port, leaf)
	srv := guardPageServer(t, pkg, fix.port)
	idp := startIdPTLS(t, fix.port, leaf)
	fp := front
	seven := "http://seven.guard.test:" + fp
	sevenOopif := "http://seven.oopif.test:" + fp
	idpOrigin := "https://ims-na1.adobelogin.com:" + idp.port
	idpStgOrigin := "https://ims-na1-stg1.adobelogin.com:" + idp.port
	targets := map[string]string{
		"seven":              seven,
		"sevenOopif":         sevenOopif,
		"vault":              "http://vault.sliccy.ai:" + fp,
		"vault-x":            "http://vault-x.sliccy.ai:" + fp,
		"apex":               "http://sliccy.ai:" + fp,
		"own":                srv.URL,
		"client":             srv.URL,
		"ordinary":           "http://ordinary.guard.test:" + fp,
		"ordinary-same-site": "http://page.guard.test:" + fp,
		"cross":              "http://cross.test:" + fp,
		"idp":                idpOrigin,
		"idp-stg":            idpStgOrigin,
	}
	n := run.slots
	rules := "--host-resolver-rules=" + strings.Join([]string{
		"MAP *.sliccy.ai 127.0.0.1",
		"MAP sliccy.ai 127.0.0.1",
		"MAP *.test 127.0.0.1",
		"MAP ims-na1.adobelogin.com 127.0.0.1",
		"MAP ims-na1-stg1.adobelogin.com 127.0.0.1",
		"MAP *.adobelogin.com 127.0.0.1",
		"MAP adobelogin.com 127.0.0.1",
		"MAP adobe.com 127.0.0.1",
		"MAP *.adobe.com 127.0.0.1",
	}, ",")
	secure := "--unsafely-treat-insecure-origin-as-secure=" + strings.Join([]string{targets["ordinary"], targets["seven"], targets["vault"], targets["sevenOopif"]}, ",")
	spki := "--ignore-certificate-errors-spki-list=" + idp.spki
	chromes := make([]*chromeProc, n)
	for i := range chromes {
		chromes[i] = launchExtensionBrowser(t, ext, rules, "--remote-allow-origins="+srv.URL, secure, spki, "--ignore-certificate-errors")
	}
	gates := make([]*guardGate, n)
	for i, b := range chromes {
		gates[i] = newGuardGate(t, b.endpoint, srv.URL)
	}
	args := []string{"--cdp", gates[0].srv.URL}
	for _, g := range gates[1:] {
		args = append(args, "--cdp-extra", g.srv.URL)
	}
	args = append(args, "--origin", srv.URL, "--origin", seven, "--origin", sevenOopif, "--name", "it-node")
	env := &linkEnv{t: t, b: chromes[0], origin: srv.URL}
	env.a = startAttach(t, args...)
	env.p = chromes[0].openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != n {
		t.Fatalf("the link lists %d browsers, want %d: %+v", len(hello.Browsers), n, hello.Browsers)
	}
	type slot struct {
		ID   string `json:"id"`
		WS   string `json:"ws"`
		Gate string `json:"gate"`
	}
	slots := make([]slot, n)
	for i := range chromes {
		slots[i] = slot{ID: hello.Browsers[i].ID, WS: strings.Replace(gates[i].srv.URL, "http", "ws", 1) + "/browser", Gate: gates[i].srv.URL + "/gate"}
	}
	sha := guardSHA()
	if run.errorPageVault {
		targets["seven"] = targets["vault"]
	}
	opts := map[string]any{
		"browsers":       slots,
		"targets":        targets,
		"capabilities":   map[string]bool{"autoAttach": true, "iframeAutoAttach": true, "errorFrameClass": true, "browserSession": true, "browserContexts": true, "chromeUrlsAttach": true, "clientExit": true, "discovery": true, "extensionWorld": true, "extensionFrame": true, "inboundDelay": true, "forwardAudit": true, "stockSetup": true, "closeProtected": true, "secureOrigin": true, "signInHold": false, "requestInterception": true, "fetchInterception": true, "legacyInterception": true},
		"variants":       []string{run.variant},
		"parallel":       n,
		"log":            "http://127.0.0.1:" + strconv.Itoa(fix.log),
		"sha":            sha,
		"rowTimeoutMs":   run.rowTimeout.Milliseconds(),
		"only":           run.only,
		"skips":          guardSkips,
		"inject":         run.inject,
		"errorPageVault": run.errorPageVault,
		"settleScale":    guardSettleScale(),
	}
	var started struct {
		Started bool `json:"started"`
	}
	env.p.eval("linkTest.guard.start("+jsArgs(opts)+")", &started)
	if !started.Started {
		t.Fatal("the guard run didn't start")
	}
	begin := time.Now()
	var sum *guardSummary
	seen := 0
	said := begin
	moved := begin
	for deadline := begin.Add(11 * time.Minute); sum == nil; time.Sleep(2 * time.Second) {
		var p guardProgress
		env.p.eval("linkTest.guard.progress("+strconv.Itoa(seen)+")", &p)
		seen += len(p.Rows)
		if len(p.Rows) > 0 {
			moved = time.Now()
		}
		if sum == nil && !p.Done && time.Since(moved) > guardStall {
			for _, r := range p.Inflight {
				t.Logf("in flight: %s", r)
			}
			t.Fatalf("the guard run stalled: no row finished for %s, with %d rows done and %d in flight", guardStall, seen, len(p.Inflight))
		}
		if time.Since(said) > 30*time.Second {
			said = time.Now()
			t.Logf("%d rows run after %s", seen, time.Since(begin).Round(time.Second))
		}
		if p.Error != "" {
			t.Fatalf("the guard run failed: %s", p.Error)
		}
		if p.Done {
			sum = p.Summary
		}
		if sum == nil && time.Now().After(deadline) {
			t.Fatalf("the guard run didn't finish in time; %d rows done", seen)
		}
	}
	t.Logf("%d rows in %s with %d slots on %s", len(sum.Results), time.Since(begin).Round(time.Second), n, runtime.GOOS)
	return sum
}

func TestCDPProtectVectorsIgnoreChromeUITargets(t *testing.T) {
	const id = "C3.redirect-popup-stays-out"
	sum := runGuard(t, guardRun{variant: "prompt", slots: 2, only: id, inject: "browser_ui", rowTimeout: 45 * time.Second})
	if len(sum.Results) == 0 {
		t.Fatalf("no rows for %s", id)
	}
	for _, r := range sum.Results {
		if r.ID != id {
			t.Errorf("the run has a row for %s, want only %s", r.ID, id)
		}
		if r.Control != "pass" || r.Guarded != "pass" || r.Live != "pass" {
			t.Errorf("%s %s: control %s, guarded %s, live %s\n  why %s\n  errors %v", r.ID, r.Target, r.Control, r.Guarded, r.Live, r.Why, r.Errors)
		}
	}
}

func checkGuardSummary(t testing.TB, sum *guardSummary) {
	t.Helper()
	if want := guardNPMPackage + "@" + guardSetVersion(t); sum.Set != want {
		t.Errorf("the run used %s, want %s", sum.Set, want)
	}
	if string(sum.Missing) != "[]" {
		t.Errorf("rows missing from the run: %s", sum.Missing)
	}
	if sum.ObserverSkipped != 0 {
		t.Errorf("%d rows skipped for want of an unguarded observer", sum.ObserverSkipped)
	}
	for _, r := range sum.Results {
		name := r.ID + " " + r.Target + " " + r.Variant
		if skip, ok := guardSkips[r.ID]; ok {
			if r.Control != "skip" || r.Guarded != "skip" || r.Live != "skip" || r.Reason != skip.Reason || r.Target != skip.Target {
				t.Errorf("%s: the run skips it with %q, but it reported control %s, guarded %s, live %s, reason %q", name, skip.Reason, r.Control, r.Guarded, r.Live, r.Reason)
			}
			continue
		}
		if r.ID == "C9.page-drivable" {
			t.Logf("%s: mouse transport evidence=%s", name, r.Diagnostics)
			checkMouseEvidence(t, r)
		}
		if r.ID == "C3.tab-iframe-leak" {
			logTabIframeLeakEvidence(t, r)
		}
		if len(r.Errors) != 0 {
			if reason, ok := waivedGuardRow(r); ok {
				t.Logf("%s: WAIVED (%s): unexpected errors kept: %v", name, reason, r.Errors)
			} else {
				t.Errorf("%s: unexpected errors: %v", name, r.Errors)
			}
		}
		switch {
		case r.Control == "not-run":
			if r.Guarded != "not-run" || r.Live != "not-run" {
				t.Errorf("%s: a text row ran: %+v", name, r)
			}
		case r.Control == "skip":
			if guardSkipClass(r) == guardSkipUnexplained {
				t.Errorf("%s: inconsistent or unexplained skip: %+v", name, r)
				continue
			}
			t.Logf("%s: skip %s", name, r.Reason)
		case r.Control != "pass" || r.Guarded != "pass" || r.Live != "pass":
			if reason, ok := waivedGuardRow(r); ok {
				t.Logf("%s: WAIVED (%s): control %s, guarded %s, live %s\n  why %s\n  errors %v\n  observed %s", name, reason, r.Control, r.Guarded, r.Live, r.Why, r.Errors, r.Observed)
				continue
			}
			if r.ID == "C7.credential-export" {
				for _, n := range r.Notes {
					if strings.Contains(n, "diag.pageGetCookies") || strings.Contains(n, "diag.topTarget") || strings.Contains(n, "partitioned:") || strings.Contains(n, "result:") {
						t.Logf("%s: %s", name, n)
					}
				}
			}
			t.Errorf("%s: control %s, guarded %s, live %s\n  why %s\n  errors %v\n  observed %s\n  notes %v", name, r.Control, r.Guarded, r.Live, r.Why, r.Errors, r.Observed, r.Notes)
		}
	}
	t.Logf("counts %s; not run %v", sum.Counts, sum.NotRun)
	tally := tallyGuardRows(sum.Results)
	if !tally.any() {
		t.Error("no applicable guard vector passed; an all-skip run is not evidence")
	}
	var set struct {
		Vectors []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"vectors"`
	}
	_ = json.Unmarshal(sliccbrowsers.GuardVectorsJSON(), &set)
	ranIDs := map[string]bool{}
	for _, r := range sum.Results {
		ranIDs[r.ID] = true
	}
	var ran []string
	for _, v := range set.Vectors {
		if _, skipped := guardSkips[v.ID]; v.Kind != "cdp" || skipped {
			continue
		}
		if !ranIDs[v.ID] {
			continue
		}
		if !tally.proven(v.ID) {
			t.Errorf("%s: no row of the vector passed", v.ID)
			continue
		}
		ran = append(ran, fmt.Sprintf("%s=%d", v.ID, tally.passed[v.ID]))
	}
	t.Logf("passed rows per vector: %s", strings.Join(ran, " "))
	if !sum.Pass {
		onlyWaived := true
		for _, r := range sum.Results {
			if r.Control == "pass" && r.Guarded == "pass" && r.Live == "pass" {
				continue
			}
			if r.Control == "skip" || r.Control == "not-run" {
				continue
			}
			if _, ok := waivedGuardRow(r); !ok {
				onlyWaived = false
				break
			}
		}
		if !onlyWaived {
			t.Errorf("the guard set did not pass: counts=%s", sum.Counts)
		} else {
			t.Logf("guard set Pass=false only from waived rows: counts=%s", sum.Counts)
		}
	}
}
