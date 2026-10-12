package integration

import (
	"bufio"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/vault"
)

type vaultRes map[string]any

func (r vaultRes) num(k string) float64 {
	f, _ := r[k].(float64)
	return f
}

func (r vaultRes) obj(k string) vaultRes {
	m, _ := r[k].(map[string]any)
	return vaultRes(m)
}

func (r vaultRes) str(k string) string {
	s, _ := r[k].(string)
	return s
}

func vaultServer(t *testing.T) *httptest.Server {
	t.Helper()
	files := http.FileServer(http.Dir(filepath.Join("testdata", "vault")))
	srv := httptest.NewServer(files)
	t.Cleanup(srv.Close)
	return srv
}

func (p *page) vaultCall(t *testing.T, op string, args map[string]any) vaultRes {
	t.Helper()
	var r vaultRes
	p.eval("linkTest.vault.call("+jsArgs(op, args)+")", &r)
	if e := r.str("error"); e != "" {
		t.Fatalf("vault %s: %s", op, e)
	}
	return r
}

func (p *page) waitVerified(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		st := p.vaultCall(t, "state", nil)
		if st["stored"] != nil {
			pr := p.vaultCall(t, "probe", nil)
			if pr["verified"] == true {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the vault never paired: %v", st)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func buildFakeJerk(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, exeName("jerk")), "./testdata/fakejerk")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatal(err)
	}
	return dir
}

type vaultEnv struct {
	*linkEnv
	home, vaultOrigin, jerkLog string
	port                       int
}

func setupVault(t *testing.T) *vaultEnv {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	jerkDir := buildFakeJerk(t)
	vs := vaultServer(t)
	_, vport := hostPort(t, strings.TrimPrefix(vs.URL, "http://"))
	e := &vaultEnv{home: tmp, vaultOrigin: "http://localhost:" + strconv.Itoa(vport), jerkLog: filepath.Join(tmp, "jerk.log")}
	env := []string{
		"HOME=" + tmp, "USERPROFILE=" + tmp, "APPDATA=" + filepath.Join(tmp, "Roaming"), "XDG_CONFIG_HOME=" + filepath.Join(tmp, ".config"),
		"PATH=" + jerkDir + string(os.PathListSeparator) + os.Getenv("PATH"), "FAKEJERK_LOG=" + e.jerkLog,
	}
	e.linkEnv = setupLinkEnv(t, false, env, "--vault-origin", e.vaultOrigin)
	var r vaultRes
	e.p.eval("linkTest.vault.embed("+jsArgs(e.vaultOrigin+"/index.html")+")", &r)
	if r.str("origin") != e.vaultOrigin {
		t.Fatalf("embed: %v", r)
	}
	e.p.waitVerified(t)
	st := e.p.vaultCall(t, "state", nil)
	e.port = int(st.obj("stored").num("port"))
	return e
}

func (e *vaultEnv) configDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(e.home, "Library", "Application Support", "slicc")
	case "windows":
		return filepath.Join(e.home, "Roaming", "slicc")
	}
	return filepath.Join(e.home, ".config", "slicc")
}

func TestVaultPairing(t *testing.T) {
	e := setupVault(t)
	t.Run("the pair message", e.testPairMessage)
	t.Run("the key file", e.testKeyFile)
	t.Run("the gate", e.testVaultGate)
	t.Run("fetch refusals", e.testVaultFetchRefusals)
	t.Run("jerk", e.testVaultJerk)
	t.Run("the port is protected", e.testVaultPortProtected)
	t.Run("the cdp capability can't reach the vault", e.testVaultProtected)
	t.Run("a reload keeps the key", e.testVaultReload)
}

func (e *vaultEnv) testVaultProtected(t *testing.T) {
	var r cdpOutcome
	pe := &protectEnv{linkEnv: e.linkEnv}
	pe.evalJSON(t, "linkTest.protect.create("+jsArgs("chrome", e.vaultOrigin+"/index.html")+")", &r)
	if !r.refused() {
		t.Fatalf("createTarget on the vault origin: %+v", r)
	}
}

func (e *vaultEnv) testPairMessage(t *testing.T) {
	st := e.p.vaultCall(t, "state", nil)
	pairs, _ := st["pairs"].([]any)
	if len(pairs) == 0 {
		t.Fatalf("no pair message: %v", st)
	}
	m := vaultRes(pairs[0].(map[string]any))
	if m.str("t") != "pair" || m.num("v") != 1 || m.str("seven") != e.origin || m.str("name") != "it-node" || m.str("node") == "" || len(m.str("nonce")) < 20 || m.num("port") != float64(e.port) || m["jerk"] != true || m["keyId"] != nil {
		t.Fatalf("pair message %v", m)
	}
	pr := e.p.vaultCall(t, "probe", nil).obj("body")
	if pr.num("vault") != 1 || pr.num("rawFetch") != 1 || pr.num("jerk") != 1 || pr.str("version") == "" || pr["requestBodyStreaming"] != false {
		t.Fatalf("probe %v", pr)
	}
	if !strings.Contains(e.a.stderr.String(), "vault: paired, listening on 127.0.0.1:"+strconv.Itoa(e.port)) {
		t.Fatalf("no pairing status:\n%s", e.a.stderr.String())
	}
}

func (e *vaultEnv) testKeyFile(t *testing.T) {
	_, sport := hostPort(t, strings.TrimPrefix(e.origin, "http://"))
	_, vport := hostPort(t, strings.TrimPrefix(e.vaultOrigin, "http://"))
	path := filepath.Join(e.configDir(), "vault-localhost_"+strconv.Itoa(vport)+"+127.0.0.1_"+strconv.Itoa(sport)+".key")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stored := e.p.vaultCall(t, "state", nil).obj("stored").str("key")
	if strings.TrimSpace(string(b)) != stored || len(stored) != 43 {
		t.Fatalf("key file %q, vault %q", b, stored)
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", fi.Mode())
	}
}

func (e *vaultEnv) listener(t *testing.T, method, path string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, "http://127.0.0.1:"+strconv.Itoa(e.port)+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res
}

func (e *vaultEnv) testVaultGate(t *testing.T) {
	key := e.p.vaultCall(t, "state", nil).obj("stored").str("key")
	vo := e.vaultOrigin
	for _, c := range []struct {
		name, method, path string
		headers            map[string]string
		status             int
		cors               bool
	}{
		{"another host", "POST", "/api/fetch-proxy", map[string]string{"Host": "evil.test:" + strconv.Itoa(e.port), "Origin": vo}, 403, false},
		{"another path", "POST", "/api/hostfs", map[string]string{"Origin": vo}, 404, false},
		{"seven's origin", "POST", "/api/fetch-proxy", map[string]string{"Origin": e.origin, "X-Bridge-Token": key}, 403, false},
		{"no origin", "POST", "/api/fetch-proxy", map[string]string{"X-Bridge-Token": key}, 403, false},
		{"GET", "GET", "/api/fetch-proxy", map[string]string{"Origin": vo, "X-Bridge-Token": key}, 405, true},
		{"no key", "POST", "/api/fetch-proxy", map[string]string{"Origin": vo, "X-Slicc-Raw-Request": "{}"}, 403, true},
		{"wrong key", "POST", "/api/jerk", map[string]string{"Origin": vo, "X-Bridge-Token": strings.Repeat("A", 43)}, 403, true},
		{"bad challenge", "POST", "/api/fetch-proxy", map[string]string{"Origin": vo, "X-Slicc-Raw-Probe": "1", "X-Slicc-Vault-Challenge": "short"}, 400, true},
		{"preflight", "OPTIONS", "/api/fetch-proxy", map[string]string{"Origin": vo, "Access-Control-Request-Method": "POST", "Access-Control-Request-Private-Network": "true"}, 204, true},
	} {
		res := e.listener(t, c.method, c.path, c.headers)
		if res.StatusCode != c.status || (res.Header.Get("Access-Control-Allow-Origin") == vo) != c.cors {
			t.Fatalf("%s: %d %v", c.name, res.StatusCode, res.Header)
		}
		if c.status != 204 && res.Header.Get("X-Proxy-Error") != "1" {
			t.Fatalf("%s: no X-Proxy-Error: %v", c.name, res.Header)
		}
		if c.name == "preflight" && (res.Header.Get("Access-Control-Allow-Private-Network") != "true" || !strings.Contains(res.Header.Get("Access-Control-Allow-Headers"), "X-Slicc-Vault-Challenge")) {
			t.Fatalf("preflight: %v", res.Header)
		}
	}
	res := e.listener(t, "POST", "/api/fetch-proxy", map[string]string{"Origin": vo, "X-Slicc-Raw-Probe": "1", "X-Slicc-Vault-Challenge": "AAAAAAAAAAAAAAAAAAAAAA"})
	if res.StatusCode != 200 {
		t.Fatalf("keyless probe: %d", res.StatusCode)
	}
}

func (e *vaultEnv) testVaultFetchRefusals(t *testing.T) {
	for _, c := range []struct {
		url    string
		status int
		want   string
	}{
		{"http://example.com/", 403, "https on port 443 only"},
		{"https://example.com:8443/", 403, "https on port 443 only"},
		{"https://no-such-host.invalid/p-secret-0451?q=secret-0451", 502, "fetch failed: "},
	} {
		r := e.p.vaultCall(t, "fetch", map[string]any{"req": map[string]any{"url": c.url, "method": "GET", "headers": [][2]string{{"Authorization", "Bearer secret-0451"}}}})
		if r.num("status") != float64(c.status) || !strings.Contains(r.str("text"), c.want) || strings.Contains(r.str("text"), "secret-0451") {
			t.Fatalf("%s: %v", c.url, r)
		}
	}
	r := e.p.vaultCall(t, "fetch", map[string]any{"req": map[string]any{"url": "not a url", "method": "GET", "headers": [][2]string{}}})
	if r.num("status") != 400 {
		t.Fatalf("malformed head: %v", r)
	}
	if strings.Contains(e.a.stderr.String(), "secret-0451") {
		t.Fatalf("the log shows the request:\n%s", e.a.stderr.String())
	}
}

func (e *vaultEnv) testVaultJerk(t *testing.T) {
	r := e.p.vaultCall(t, "jerk", map[string]any{"body": map[string]any{"op": "lease", "provider": "github", "duration": 900}})
	lease, result := r.obj("body").obj("lease"), r.obj("body").obj("result")
	if r.num("status") != 200 || lease.obj("credentials").str("GH_TOKEN") != "gho_fake_github" || lease.str("expires_at") == "" || lease.str("lease_id") != "jerk-github-1" {
		t.Fatalf("lease: %v", r)
	}
	if result.num("exit") != 0 || result.str("provider") != "github" || result.str("user") != "octocat" || result.str("expires_at") != lease.str("expires_at") || len(result["env"].([]any)) != 2 {
		t.Fatalf("lease result: %v", result)
	}
	r = e.p.vaultCall(t, "jerk", map[string]any{"body": map[string]any{"op": "drop", "provider": "github", "lease_id": "jerk-github-1"}})
	if r.num("status") != 200 || r.obj("body").obj("result").num("exit") != 0 || r.obj("body")["lease"] != nil || r.obj("body").obj("result").str("message") != "dropped jerk-github-1" {
		t.Fatalf("drop: %v", r)
	}
	for provider, exit := range map[string]float64{"broken": 3, "nolease": 1} {
		r = e.p.vaultCall(t, "jerk", map[string]any{"body": map[string]any{"op": "lease", "provider": provider}})
		if r.num("status") != 200 || r.obj("body")["lease"] != nil || r.obj("body").obj("result").num("exit") != exit {
			t.Fatalf("%s: %v", provider, r)
		}
	}
	for _, body := range []map[string]any{
		{"op": "lease", "provider": "Git/Hub"},
		{"op": "lease", "provider": "github", "duration": 0},
		{"op": "drop", "provider": "github"},
		{"op": "create", "provider": "github"},
	} {
		if r := e.p.vaultCall(t, "jerk", map[string]any{"body": body}); r.num("status") != 400 {
			t.Fatalf("%v: %v", body, r)
		}
	}
	if r := e.p.vaultCall(t, "jerk", map[string]any{"body": map[string]any{"op": "lease", "provider": "github"}, "key": ""}); r.num("status") != 403 {
		t.Fatalf("keyless jerk: %v", r)
	}
	f, err := os.Open(e.jerkLog)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var calls []map[string]any
	for sc := bufio.NewScanner(f); sc.Scan(); {
		var c map[string]any
		_ = json.Unmarshal(sc.Bytes(), &c)
		calls = append(calls, c)
	}
	if len(calls) < 2 || calls[0]["duration"] != "900" || calls[1]["lease"] != "jerk-github-1" {
		t.Fatalf("jerk calls %v", calls)
	}
	if strings.Contains(e.a.stderr.String(), "gho_fake") {
		t.Fatalf("the log shows the lease:\n%s", e.a.stderr.String())
	}
}

func (e *vaultEnv) testVaultPortProtected(t *testing.T) {
	var r vaultRes
	e.p.eval("linkTest.open("+jsArgs(map[string]any{"kind": "tcp", "host": "127.0.0.1", "port": e.port})+")", &r)
	if r.str("reset") != "EACCES" {
		t.Fatalf("tcp to the vault listener: %v", r)
	}
}

func (e *vaultEnv) testVaultReload(t *testing.T) {
	before := e.p.vaultCall(t, "state", nil).obj("stored").str("key")
	var r vaultRes
	e.p.eval("linkTest.vault.reload()", &r)
	deadline := time.Now().Add(20 * time.Second)
	for {
		st := e.p.vaultCall(t, "state", nil)
		pairs, _ := st["pairs"].([]any)
		if len(pairs) > 0 {
			m := vaultRes(pairs[0].(map[string]any))
			if m.str("keyId") == "" {
				t.Fatalf("a re-pair without keyId: %v", m)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pair after the reload: %v", st)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if e.p.waitVerified(t); e.p.vaultCall(t, "state", nil).obj("stored").str("key") != before {
		t.Fatal("the key changed")
	}
}

func TestVaultInProcess(t *testing.T) {
	up := credUpstream(t)
	roots := x509.NewCertPool()
	roots.AddCert(up.Certificate())
	vs := vaultServer(t)
	l, err := vault.Listen([]string{vs.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	l.Serve(vault.Config{Dialer: &mapDialer{routes: map[string]string{"example.com:443": up.Listener.Addr().String()}}, RootCAs: roots, Version: "test"})
	b := launchBrowser(t)
	srv := pageServer(t)
	p := b.openPage(t, srv.URL+"/index.html")
	logs := &syncBuffer{}
	logf := func(format string, args ...any) { _, _ = logs.Write([]byte(sprintf(format, args...) + "\n")) }
	pairer := vault.NewPairer(vault.PairOptions{Client: b.client, Listener: l, KeyDir: t.TempDir(), Seven: func(o string) bool { return o == srv.URL }, Node: "n1", Name: "it", Logf: logf, Status: logf})
	t.Cleanup(pairer.Close)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("pairer log:\n%s", logs.String())
		}
	})
	pairer.Target(cdp.TargetInfo{TargetID: p.target, Type: "page", URL: srv.URL + "/index.html"})
	var r vaultRes
	p.eval("linkTest.vault.embed("+jsArgs(vs.URL+"/index.html")+")", &r)
	p.waitVerified(t)
	t.Run("same-site frame pairs", func(t *testing.T) {
		st := p.vaultCall(t, "state", nil)
		if st.obj("stored").num("port") != float64(l.Port()) {
			t.Fatalf("state %v", st)
		}
	})
	t.Run("fetch injects nothing and relays", func(t *testing.T) {
		r := p.vaultCall(t, "fetch", map[string]any{"req": map[string]any{"url": "https://example.com/p?q=1", "method": "POST", "headers": [][2]string{{"Authorization", "Bearer v-0451"}}}, "body64": "aGk="})
		head := r.obj("head")
		if r.num("status") != 200 || head.num("status") != 200 || r.str("text") != "POST /p?q=1 Bearer v-0451 hi" || head.str("url") != "https://example.com/p?q=1" {
			t.Fatalf("fetch %v", r)
		}
		r = p.vaultCall(t, "fetch", map[string]any{"req": map[string]any{"url": "https://example.com/redirect", "method": "GET", "headers": [][2]string{}}})
		if r.obj("head").num("status") != 302 {
			t.Fatalf("redirect %v", r)
		}
	})
	t.Run("no jerk without host jerk", func(t *testing.T) {
		if pr := p.vaultCall(t, "probe", nil).obj("body"); pr["jerk"] != nil {
			t.Fatalf("probe %v", pr)
		}
		req, _ := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(l.Port())+"/api/jerk", strings.NewReader(`{"op":"lease","provider":"github"}`))
		req.Header.Set("Origin", vs.URL)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatalf("jerk without host jerk: %d", res.StatusCode)
		}
	})
	t.Run("replies from another context or nonce are ignored", func(t *testing.T) {
		before := p.vaultCall(t, "state", nil).obj("stored").str("key")
		for _, mode := range []string{"forge", "stale", "stale"} {
			p.vaultCall(t, "mode", map[string]any{"mode": mode})
			p.eval("linkTest.vault.reload()", &r)
			waitPairs(t, p)
			time.Sleep(300 * time.Millisecond)
		}
		if pr := p.vaultCall(t, "probe", nil); pr["verified"] != true || p.vaultCall(t, "state", nil).obj("stored").str("key") != before {
			t.Fatalf("a forged or stale reply changed the key: %v", pr)
		}
	})
	t.Run("a refusal is reported", func(t *testing.T) {
		p.vaultCall(t, "mode", map[string]any{"mode": "refuse"})
		p.eval("linkTest.vault.reload()", &r)
		waitPairs(t, p)
		deadline := time.Now().Add(10 * time.Second)
		for !strings.Contains(logs.String(), "vault: pairing refused: the test refuses") {
			if time.Now().After(deadline) {
				t.Fatalf("no refusal in the log:\n%s", logs.String())
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
}

func waitPairs(t *testing.T, p *page) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		st := p.vaultCall(t, "state", nil)
		if pairs, _ := st["pairs"].([]any); len(pairs) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pair message: %v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
