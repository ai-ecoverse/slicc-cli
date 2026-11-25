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
	Sites        struct{ Billing, Receipt string }
	ChromiumArgs []string
	OwnerStart   string
	submitted    chan string
	posts        *atomic.Int32
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
	site.submitted = make(chan string, 4)
	site.posts = new(atomic.Int32)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/r/1042":
			fmt.Fprint(w, "<p>Receipt 1042</p>")
		case "/widget":
			fmt.Fprint(w, "<p>PRIVATE WIDGET</p><form method=post action=/submit><label>Note<input name=note></label><button>Save</button></form>")
		case "/submit":
			if r.Method == http.MethodPost {
				site.posts.Add(1)
				body, err := io.ReadAll(io.LimitReader(r.Body, 1024))
				if err != nil {
					http.Error(w, "bad fixture POST", 400)
					return
				}
				select {
				case site.submitted <- string(body):
				default:
				}
				fmt.Fprint(w, "<p>Fixture note saved</p>")
			}
		default:
			fmt.Fprintf(w, `<h1>Invoice 1042 · paid</h1><form method="post" action="/submit"><label>Note<input name="note"></label><button>Save</button></form><iframe src="https://receipt.example.test:%s/widget"></iframe>`, strings.Split(r.Host, ":")[1])
		}
	}))
	t.Cleanup(server.Close)
	d := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	site.ChromiumArgs = []string{"--ignore-certificate-errors-spki-list=" + base64.StdEncoding.EncodeToString(d[:]), "--host-resolver-rules=MAP receipt.example.test 127.0.0.1"}
	site.Sites.Billing = server.URL
	site.Sites.Receipt = strings.Replace(server.URL, "127.0.0.1", "receipt.example.test", 1)
	site.OwnerStart = server.URL + "/"
	return site
}
func TestSignedLeaseVectors(t *testing.T) {
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	ov := hrOverlay(t, t.TempDir(), map[string]string{filepath.Join(root, "internal/lease/form_cancel_test.go"): filepath.Join(root, "test/integration/testdata/lease/form_cancel.go"), filepath.Join(root, "internal/lease/fixture_test.go"): filepath.Join(root, "test/integration/testdata/lease/vectors.go"), filepath.Join(root, "internal/caps/cdp/form_guard_test.go"): filepath.Join(root, "test/integration/testdata/lease/form_guard.go")})
	c := exec.CommandContext(t.Context(), "go", "test", "-overlay", ov, "-count=1", "./internal/lease", "./internal/caps/cdp")
	c.Dir = root
	if out, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%v: %s", e, out)
	}
}

func TestSignedLeaseOwnerAndGuardedBrowser(t *testing.T) { runSignedLease(t, false) }
func TestSignedLeaseOrdinaryForm(t *testing.T)           { runSignedLease(t, true) }
func runSignedLease(t *testing.T, forms bool) {
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
	followArgs := []string{"-", "follow", "--json", "--lease-browser", owner.endpoint, "--name", "fixture"}
	if forms {
		followArgs = append(followArgs, "--lease-ordinary-forms")
	}
	cmd := exec.Command(binary, followArgs...)
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
	if forms {
		cli(false, "grant", "--no-input", "--browser", owner.endpoint, "--link", "fixture", "--ordinary-form", site.Sites.Receipt+"/submit", "--to", "jkt:"+jkt, "--allow", site.Sites.Billing, "--for", "5m")
		approve = append(approve, "--ordinary-form", site.Sites.Billing+"/submit")
	}
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
			if forms && (!strings.Contains(string(b), "one submit-button action") || !strings.Contains(string(b), site.Sites.Billing+"/submit") || !strings.Contains(string(b), "Type APPROVE FORM")) {
				t.Fatal("write consequences not shown")
			}
			if (e == nil) != ok || !strings.Contains(string(b), "Type APPROVE") {
				t.Fatalf("interactive review %v: %s", e, b)
			}
		}
		review("", false)
		if !strings.Contains(cli(true, "status"), req.Request) {
			t.Fatal("Enter consumed request")
		}
		if forms {
			review("APPROVE", false)
			review("APPROVE FORM", true)
		} else {
			review("APPROVE", true)
		}
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
	if !strings.Contains(s, "Invoice 1042 · paid") || strings.Contains(s, "Support frame fixture only") || strings.Contains(s, "PRIVATE WIDGET") {
		t.Fatalf("snapshot: %s", s)
	}
	url := site.Sites.Receipt + "/r/1042"
	if v := action("goto", map[string]string{"url": url}); v["reason"] != "E_SCOPE" {
		t.Fatal(v)
	}
	if forms {
		exerciseLeaseForm(t, owner, own.TargetID, site, action)
	} else if v := action("click", map[string]string{"ref": "e1"}); v["reason"] != "E_UNSUPPORTED" {
		t.Fatal(v)
	}
	request("exact", url, map[string]string{"parent": l1})
	cli(true, "deny", req.Request)
	p.eval("leaseTest.grant()", &ready)
	if ready != "E_DENIED" {
		t.Fatal(ready)
	}
	request("exact", url, map[string]string{"parent": l1})
	if forms {
		cli(false, "grant", req.Request, "--add", url, "--keep-expiry", "--ordinary-form", site.Sites.Billing+"/submit")
	}
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
	if forms {
		if v := action("fill", map[string]string{"ref": "e1", "value": "test"}); v["reason"] != "E_UNSUPPORTED" {
			t.Fatal("replacement inherited writes", v)
		}
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
	if forms {
		if r := action("click", map[string]string{"ref": "e1"}); r["reason"] != "E_UNSUPPORTED" {
			t.Fatal("legacy approve unexpectedly admitted writes", r)
		}
	}
	p.evalWithin("leaseTest.closed()", nil, 5*time.Second)
	p.eval("leaseTest.connect()", &ready)
	if ready != "E_EXPIRED" {
		t.Fatal("expired grant", ready)
	}
	if forms && site.posts != nil && site.posts.Load() != 1 {
		t.Fatalf("fixture observed %d POSTs, want exactly one", site.posts.Load())
	}
}
