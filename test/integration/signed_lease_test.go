package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/creack/pty"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type leaseSite struct {
	Sites            struct{ Billing, Receipt string }
	ChromiumArgs     []string
	OwnerStart       string
	BillingDocReset  func()
	BillingDocStatus func() (status int, site, referer string)
}

func signedSite(t *testing.T) leaseSite {
	t.Helper()
	var site leaseSite
	if path := os.Getenv("SLICC_TEST_LEASE_PAGE"); path != "" {
		cmd := exec.Command(requireNode(t), path)
		rd, e := cmd.StdoutPipe()
		if e != nil {
			t.Fatal(e)
		}
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		if e = json.NewDecoder(rd).Decode(&site); e != nil {
			t.Fatal(e)
		}
		return site
	}
	var lastStatus atomic.Int32
	var lastSite, lastReferer atomic.Value
	lastSite.Store("")
	lastReferer.Store("")
	var recordedDoc atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/r/1042":
			fmt.Fprint(w, "<p>Receipt 1042</p>")
		case "/widget":
			fmt.Fprint(w, "<p>PRIVATE WIDGET</p>")
		case "/favicon.ico":
			w.WriteHeader(http.StatusNoContent)
		default:
			if r.Header.Get("Sec-Fetch-Dest") != "document" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			secSite := r.Header.Get("Sec-Fetch-Site")
			referer := r.Header.Get("Referer")
			if recordedDoc.CompareAndSwap(false, true) {
				lastSite.Store(secSite)
				lastReferer.Store(referer)
				if secSite != "none" || referer != "" {
					lastStatus.Store(401)
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, "unauthorized")
					return
				}
				lastStatus.Store(200)
			}
			if lastStatus.Load() == 401 {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, "unauthorized")
				return
			}
			fmt.Fprint(w, `<h1>Invoice 1042 · paid</h1><iframe src="/widget"></iframe>`)
		}
	}))
	t.Cleanup(server.Close)
	d := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	site.ChromiumArgs = []string{"--ignore-certificate-errors-spki-list=" + base64.StdEncoding.EncodeToString(d[:]), "--host-resolver-rules=MAP receipt.example.test 127.0.0.1"}
	site.Sites.Billing = server.URL
	site.Sites.Receipt = strings.Replace(server.URL, "127.0.0.1", "receipt.example.test", 1)
	site.OwnerStart = server.URL + "/"
	site.BillingDocReset = func() {
		recordedDoc.Store(false)
		lastStatus.Store(0)
		lastSite.Store("")
		lastReferer.Store("")
	}
	site.BillingDocStatus = func() (int, string, string) {
		return int(lastStatus.Load()), lastSite.Load().(string), lastReferer.Load().(string)
	}
	return site
}
func TestSignedLeaseVectors(t *testing.T) {
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	ov := hrOverlay(t, t.TempDir(), map[string]string{filepath.Join(root, "internal/lease/fixture_test.go"): filepath.Join(root, "test/integration/testdata/lease/vectors.go")})
	c := exec.CommandContext(t.Context(), "go", "test", "-overlay", ov, "-count=1", "./internal/lease")
	c.Dir = root
	if out, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%v: %s", e, out)
	}
}

func TestSignedLeaseOwnerAndGuardedBrowser(t *testing.T) {
	site := signedSite(t)
	owner := launchBrowserOn(t, 0, append(site.ChromiumArgs, "--no-proxy-server")...)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var own struct{ TargetID string }
	if err := owner.client.Call(ctx, "", "Target.createTarget", map[string]string{"url": site.OwnerStart}, &own); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("SLICC_TEST_LEASE_PAGE") != "" {
		ss, e := owner.client.Attach(ctx, own.TargetID)
		if e != nil {
			t.Fatal(e)
		}
		var check struct {
			Cookies []struct {
				Name, SameSite   string
				Secure, HttpOnly bool
			}
		}
		for ctx.Err() == nil {
			if e = ss.Call(ctx, "Network.getCookies", map[string]any{"urls": []string{site.Sites.Billing}}, &check); e != nil {
				t.Fatal("fixture cookie check failed")
			}
			if len(check.Cookies) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if len(check.Cookies) != 1 || check.Cookies[0].Name != "__Host-fixture" || check.Cookies[0].SameSite != "Strict" || !check.Cookies[0].Secure || !check.Cookies[0].HttpOnly {
			t.Fatal("fixture must actually install the Strict, Secure, HttpOnly synthetic cookie")
		}
	}
	leader, p := launchWithPage(t, pageServer(t).URL+"/index.html?lease")
	if owner.port == leader.port {
		t.Fatal("expected independent browsers")
	}
	h := newFakeHub(t)
	tr := h.addTray("lease")
	_ = newTrayLeader(t, h, p, tr)
	if runtime.GOOS != "windows" {
		dir, e := os.MkdirTemp("/tmp", "slicc-lease-")
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		t.Setenv("TMPDIR", dir)
		t.Setenv("XDG_RUNTIME_DIR", dir)
	}
	env := append(configEnv(t), "SLICC_NO_TELEMETRY=1")
	cli := func(success bool, args ...string) string {
		t.Helper()
		cmd := exec.Command(binary, append([]string{"lease"}, args...)...)
		cmd.Env = env
		b, e := cmd.CombinedOutput()
		if (e == nil) != success {
			t.Fatalf("lease %s: %v: %s", args[0], e, b)
		}
		return string(b)
	}
	var pin, jkt string
	p.eval("leaseFingerprint()", &pin)
	p.eval("leaseTest.init()", &jkt)
	cli(true, "pair", "--link", "fixture", "--peer", pin)
	cli(false, "pair", "--link", "fixture", "--peer", strings.Repeat("ab", 32))
	cli(true, "pair", "--link", "unverified", "--peer", strings.Repeat("ab", 32))
	bad := exec.Command(binary, "-", "follow", "--lease-browser", owner.endpoint, "--name", "unverified")
	bad.Env = env
	bad.Stdin = strings.NewReader(h.joinURL(tr) + "\n")
	badlog := &syncBuffer{}
	bad.Stderr = badlog
	if e := bad.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if bad.ProcessState == nil {
			_ = bad.Process.Kill()
			_ = bad.Wait()
		}
	}()
	(&attachProc{cmd: bad, stderr: badlog}).waitStderr(t, "paired fingerprint mismatch", 20*time.Second)
	_ = bad.Process.Kill()
	_ = bad.Wait()
	if strings.Contains(badlog.String(), "linked to ") {
		t.Fatal("accepted wrong peer")
	}
	cmd := exec.Command(binary, "-", "follow", "--json", "--lease-browser", owner.endpoint, "--name", "fixture")
	cmd.Env = env
	cmd.Stdin = strings.NewReader(h.joinURL(tr) + "\n")
	log := &syncBuffer{}
	cmd.Stdout = log
	cmd.Stderr = log
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(log.String())
		}
	})
	p.waitUp(t, "")
	fp := &attachProc{cmd: cmd, stderr: log}
	fp.waitStderr(t, "linked to", 15*time.Second)
	var blocked map[string]any
	p.eval(`linkTest.open({kind:'cdp',browser:'local'})`, &blocked)
	if blocked["opened"] != nil || !strings.Contains(fmt.Sprint(blocked), "EOPNOTSUPP") {
		t.Fatalf("legacy OPEN: %v", blocked)
	}
	var req struct{ Type, Request string }
	request := func(kind, url string, extra any) {
		t.Helper()
		params := jsArgs([]any{map[string]string{"kind": kind, "url": url}}, extra)
		p.eval("leaseTest.request("+params+")", &req)
		if req.Type != "pending" || req.Request == "" {
			t.Fatalf("not pending: %v", req)
		}
	}
	request("origin", site.Sites.Billing, map[string]int{"ttlSeconds": 300})
	approve := []string{"--to", "jkt:" + jkt, "--allow", site.Sites.Billing, "--for", "5m"}
	cli(false, append([]string{"review"}, approve...)...)
	initial := cli(true, "status")
	if !strings.Contains(initial, req.Request) || !strings.Contains(initial, `"active":[]`) {
		t.Fatal("review issued before approval")
	}
	if runtime.GOOS == "windows" {
		cli(true, append([]string{"grant", "--no-input", "--browser", owner.endpoint, "--link", "fixture"}, approve...)...)
	} else {
		review := func(input string, ok bool) {
			t.Helper()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			c := exec.CommandContext(ctx, binary, append([]string{"lease", "review"}, approve...)...)
			c.Env = env
			tty, e := pty.Start(c)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = tty.Close() }()
			_, _ = io.WriteString(tty, input+"\n")
			b, _ := io.ReadAll(tty)
			e = c.Wait()
			if (e == nil) != ok || !strings.Contains(string(b), "Type APPROVE") {
				t.Fatalf("interactive review %v: %s", e, b)
			}
		}
		review("", false)
		if !strings.Contains(cli(true, "status"), req.Request) {
			t.Fatal("Enter consumed request")
		}
		review("APPROVE", true)
	}
	var l1, l2, ready string
	p.eval("leaseTest.grant()", &l1)
	p.eval("leaseTest.connect()", &ready)
	if ready != "ready" {
		t.Fatalf("connect %q", ready)
	}
	action := func(op string, args any) map[string]any {
		t.Helper()
		var reply map[string]any
		p.eval("leaseTest.action("+jsArgs(op, args)+")", &reply)
		return reply
	}
	if site.BillingDocReset != nil {
		site.BillingDocReset()
	}
	if v := action("goto", map[string]string{"url": site.Sites.Billing + "/"}); v["type"] != "result" {
		t.Fatalf("goto: %v", v)
	}
	var snap map[string]any
	for end := time.Now().Add(6 * time.Second); time.Now().Before(end); {
		snap = action("snapshot", map[string]any{})
		if strings.Contains(fmt.Sprint(snap), "Invoice 1042 · paid") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	s := fmt.Sprint(snap)
	if !strings.Contains(s, "Invoice 1042 · paid") || strings.Contains(s, "Support frame fixture only") || strings.Contains(s, "PRIVATE WIDGET") || strings.Contains(s, "unauthorized") {
		t.Fatalf("snapshot: %s", s)
	}
	if site.BillingDocStatus != nil {
		st, secSite, referer := site.BillingDocStatus()
		if st != 200 || secSite != "none" || referer != "" {
			t.Fatalf("first billing document HTTP status=%d Sec-Fetch-Site=%q Referer=%q (want 200, none, empty; 401 means a non-browser-initiated hop)", st, secSite, referer)
		}
		t.Logf("lease-chrome-summary test=TestSignedLeaseOwnerAndGuardedBrowser rows=1 billing_doc_status=%d sec_fetch_site=%s protected_fixture_hits=n/a snapshot=Invoice", st, secSite)
	} else {
		t.Logf("lease-chrome-summary test=TestSignedLeaseOwnerAndGuardedBrowser rows=1 billing_doc_status=external-fixture snapshot=Invoice")
	}
	url := site.Sites.Receipt + "/r/1042"
	if v := action("goto", map[string]string{"url": url}); v["reason"] != "E_SCOPE" {
		t.Fatal(v)
	}
	if v := action("click", map[string]string{"ref": "e1"}); v["reason"] != "E_UNSUPPORTED" {
		t.Fatal(v)
	}
	request("exact", url, map[string]string{"parent": l1})
	cli(true, "deny", req.Request)
	p.eval("leaseTest.grant()", &ready)
	if ready != "E_DENIED" {
		t.Fatal(ready)
	}
	request("exact", url, map[string]string{"parent": l1})
	cli(true, "grant", req.Request, "--add", url, "--keep-expiry")
	p.eval("leaseTest.grant()", &l2)
	if l1 == l2 {
		t.Fatal("replacement reused ID")
	}
	p.eval("leaseTest.connect(true)", &ready)
	if ready != "E_REVOKED" {
		t.Fatal("retired ID", ready)
	}
	p.eval("leaseTest.connect()", &ready)
	if ready != "ready" {
		t.Fatal(ready)
	}
	if v := action("goto", map[string]string{"url": url}); v["type"] != "result" {
		t.Fatal(v)
	}
	if v := action("snapshot", map[string]any{}); !strings.Contains(fmt.Sprint(v), "Receipt 1042") {
		t.Fatal(v)
	}
	targets, e := owner.client.Targets(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	task := ""
	for _, ti := range targets {
		if ti.URL == url && ti.Type == "page" {
			task = ti.TargetID
		}
	}
	if task == "" || task == own.TargetID {
		t.Fatal("expected exclusive receipt tab")
	}
	cli(true, "revoke", l2)
	p.eval("leaseTest.closed()", nil)
	p.eval("leaseTest.connect()", &ready)
	if ready != "E_REVOKED" {
		t.Fatal("revoked ID", ready)
	}
	for end := time.Now().Add(5 * time.Second); ; {
		targets, e := owner.client.Targets(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		found, kept := false, false
		for _, ti := range targets {
			found = found || ti.TargetID == task
			kept = kept || ti.TargetID == own.TargetID
		}
		if !kept {
			t.Fatal("removed preexisting tab")
		}
		if !found {
			break
		}
		if time.Now().After(end) {
			t.Fatal("did not remove leased tab")
		}
		time.Sleep(20 * time.Millisecond)
	}
	request("origin", site.Sites.Billing, map[string]int{"ttlSeconds": 4})
	cli(true, "grant", "--no-input", "--to", "jkt:"+jkt, "--allow", site.Sites.Billing, "--for", "4s", "--browser", owner.endpoint, "--link", "fixture")
	p.eval("leaseTest.grant()", nil)
	p.eval("leaseTest.connect()", &ready)
	if ready != "ready" {
		t.Fatal(ready)
	}
	p.evalWithin("leaseTest.closed()", nil, 5*time.Second)
	p.eval("leaseTest.connect()", &ready)
	if ready != "E_EXPIRED" {
		t.Fatal("expired grant", ready)
	}
}
