package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	guarded "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

func leaseHopRequireChrome(t *testing.T) {
	t.Helper()
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("step=chrome: CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
}

func leaseHopPipeOverlay(t *testing.T, extras map[string]string, run string) ([]byte, error) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		filepath.Join(root, "internal/lease/hops_pipe_test.go"): filepath.Join(root, "test/integration/testdata/lease/hops_pipe.go"),
	}
	for k, v := range extras {
		replacements[k] = v
	}
	ov := hrOverlay(t, t.TempDir(), replacements)
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", ov, "-run", run, "./internal/lease")
	command.Dir = root
	env := append(os.Environ(),
		"SLICC_LEASE_HOP_ENDPOINT="+os.Getenv("SLICC_LEASE_HOP_ENDPOINT"),
		"SLICC_LEASE_HOP_DEST="+os.Getenv("SLICC_LEASE_HOP_DEST"),
	)
	if cache := os.Getenv("GOCACHE"); cache != "" {
		env = append(env, "GOCACHE="+cache)
	}
	command.Env = env
	return command.CombinedOutput()
}

func TestSignedLeaseHopPipeOverlay(t *testing.T) {
	out, err := leaseHopPipeOverlay(t, nil, "^TestLeasePipe")
	if err != nil {
		t.Fatalf("lease hop pipe: %v\n%s", err, out)
	}
}

func TestSignedLeaseHopPipeMutantCreateDest(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "internal/lease/browser.go")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	from := `if bs.c.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "background": false}, &p) != nil || p.TargetID == "" {
		return unsupported
	}
	t.target = p.TargetID
	s, e := bs.c.Attach(ctx, t.target)
	if e != nil || s.ID == "" {
		return unsupported
	}
	t.session = s.ID
	t.world = "slicc-" + id()
	t.changed = make(chan struct{})
	bs.mu.Lock()
	bs.tabs[s.ID] = t
	bs.mu.Unlock()
	for _, m := range []string{"Page.enable", "Runtime.enable"} {
		if s.Call(ctx, m, nil, nil) != nil {
			return unsupported
		}
	}
	if s.Call(ctx, "Page.navigate", map[string]any{"url": dest}, nil) != nil {
		return unsupported
	}
	t.lost = false
	return nil
}`
	to := `if bs.c.Call(ctx, "", "Target.createTarget", map[string]any{"url": dest, "background": false}, &p) != nil || p.TargetID == "" {
		return unsupported
	}
	t.target = p.TargetID
	s, e := bs.c.Attach(ctx, t.target)
	if e != nil || s.ID == "" {
		return unsupported
	}
	t.session = s.ID
	t.world = "slicc-" + id()
	t.changed = make(chan struct{})
	bs.mu.Lock()
	bs.tabs[s.ID] = t
	bs.mu.Unlock()
	for _, m := range []string{"Page.enable", "Runtime.enable"} {
		if s.Call(ctx, m, nil, nil) != nil {
			return unsupported
		}
	}
	t.lost = false
	return nil
}`
	if strings.Count(text, from) != 1 {
		t.Fatal("mutant anchor for create() must occur once in browser.go")
	}
	mutant := filepath.Join(t.TempDir(), "browser_create_dest.go")
	if err := os.WriteFile(mutant, []byte(strings.Replace(text, from, to, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := leaseHopPipeOverlay(t, map[string]string{src: mutant}, "^TestLeasePipeCreateArmsBeforeProtectedHop$")
	if err == nil {
		t.Fatalf("createTarget{url:dest} mutant must fail pipe row\n%s", out)
	}
	if !strings.Contains(string(out), "--- FAIL: TestLeasePipeCreateArmsBeforeProtectedHop") {
		t.Fatalf("mutant did not fail TestLeasePipeCreateArmsBeforeProtectedHop\n%s", out)
	}
}

func TestSignedLeaseProtectedHopsChrome(t *testing.T) {
	leaseHopRequireChrome(t)
	var protectedHits atomic.Int64
	var credentialHits atomic.Int64
	var firstHopStatus atomic.Int32
	var protectedDoc, credentialDoc string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if strings.Contains(host, "sliccy.ai") {
			protectedHits.Add(1)
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<p>PROTECTED</p>")
			return
		}
		if strings.Contains(host, "adobelogin.com") {
			credentialHits.Add(1)
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<p>SSO</p>")
			return
		}
		switch r.URL.Path {
		case "/ok":
			if r.Header.Get("Sec-Fetch-Dest") == "document" && r.Header.Get("Sec-Fetch-Mode") == "navigate" && (r.Header.Get("Sec-Fetch-Site") != "none" || r.Header.Get("Referer") != "") {
				firstHopStatus.Store(401)
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, "unauthorized")
				return
			}
			if r.Header.Get("Sec-Fetch-Dest") == "document" {
				firstHopStatus.Store(200)
			}
			fmt.Fprint(w, "<p>ALLOWED OK</p>")
		case "/redirect":
			http.Redirect(w, r, protectedDoc+"?n=lease-302", http.StatusFound)
		case "/meta":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<meta http-equiv="refresh" content="0;url=%s?n=lease-meta">`, protectedDoc)
		case "/script":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<script>location.assign(%q)</script>`, protectedDoc+"?n=lease-script")
		case "/sso":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<script>location.assign(%q)</script>`, credentialDoc)
		case "/favicon.ico":
			w.WriteHeader(http.StatusNoContent)
		default:
			fmt.Fprint(w, "<p>start</p>")
		}
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	su, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port := su.Port()
	protectedDoc = "https://seven.sliccy.ai:" + port + "/doc"
	credentialDoc = "https://ims-na1.adobelogin.com:" + port + "/ims/authorize"
	d := sha256.Sum256(srv.Certificate().RawSubjectPublicKeyInfo)
	pin := base64.StdEncoding.EncodeToString(d[:])
	owner := launchBrowserOn(t, 0,
		"--ignore-certificate-errors-spki-list="+pin,
		"--host-resolver-rules=MAP billing.example.test 127.0.0.1, MAP seven.sliccy.ai 127.0.0.1, MAP ims-na1.adobelogin.com 127.0.0.1",
		"--no-proxy-server",
	)
	site := strings.Replace(srv.URL, "127.0.0.1", "billing.example.test", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	rows := 0
	t.Run("control-fixture-reachable", func(t *testing.T) {
		rows++
		before := protectedHits.Load()
		var created struct{ TargetID string }
		if err := owner.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": protectedDoc + "?n=control", "background": false}, &created); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = owner.client.Call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": created.TargetID}, nil)
		})
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if protectedHits.Load() > before {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("control unguarded tab got 0 hits on %s (fixture unreachable; zero-hit assertions would be vacuous)", protectedDoc)
	})
	client, err := guarded.OpenLocal(ctx, owner.endpoint, []string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("first-goto-browser-initiated", func(t *testing.T) {
		rows++
		var created struct{ TargetID string }
		if err := client.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "background": false}, &created); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = client.Call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": created.TargetID}, nil)
		})
		s, err := client.Attach(ctx, created.TargetID)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Detach(context.Background()) }()
		for _, m := range []string{"Page.enable", "Runtime.enable"} {
			if err := s.Call(ctx, m, nil, nil); err != nil {
				t.Fatal(m, err)
			}
		}
		if err := s.Call(ctx, "Page.navigate", map[string]any{"url": site + "/ok"}, nil); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(8 * time.Second)
		var href, body string
		for time.Now().Before(deadline) {
			_ = s.Evaluate(ctx, "document.readyState==='complete'?document.body?document.body.innerText:location.href:''", cdp.EvaluateOptions{ReturnByValue: true}, &body)
			_ = s.Evaluate(ctx, "location.href", cdp.EvaluateOptions{ReturnByValue: true}, &href)
			if strings.Contains(body, "ALLOWED OK") || strings.Contains(body, "unauthorized") {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if st := firstHopStatus.Load(); st != 200 {
			t.Fatalf("first Page.navigate after about:blank got HTTP %d (want 200; 401 means Sec-Fetch-Site/Referer were not browser-initiated)", st)
		}
		if !strings.Contains(body, "ALLOWED OK") {
			t.Fatalf("first hop body=%q href=%q", body, href)
		}
	})
	for _, row := range []struct {
		name string
		path string
	}{
		{"302", "/redirect"},
		{"meta", "/meta"},
		{"script", "/script"},
	} {
		t.Run(row.name, func(t *testing.T) {
			rows++
			before := protectedHits.Load()
			var created struct{ TargetID string }
			if err := client.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "background": false}, &created); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = client.Call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": created.TargetID}, nil)
			})
			s, err := client.Attach(ctx, created.TargetID)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Detach(context.Background()) }()
			for _, m := range []string{"Page.enable", "Runtime.enable"} {
				if err := s.Call(ctx, m, nil, nil); err != nil {
					t.Fatal(m, err)
				}
			}
			if err := s.Call(ctx, "Page.navigate", map[string]any{"url": site + row.path}, nil); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				var href string
				_ = s.Evaluate(ctx, "location.href", cdp.EvaluateOptions{ReturnByValue: true}, &href)
				if strings.HasPrefix(href, "chrome-error:") || protectedHits.Load() != before {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if got := protectedHits.Load() - before; got != 0 {
				t.Fatalf("protected origin received %d document hits for %s", got, row.name)
			}
		})
	}
	t.Run("sso-credential", func(t *testing.T) {
		rows++
		beforeP, beforeC := protectedHits.Load(), credentialHits.Load()
		var created struct{ TargetID string }
		if err := client.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "background": false}, &created); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = client.Call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": created.TargetID}, nil)
		})
		s, err := client.Attach(ctx, created.TargetID)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Detach(context.Background()) }()
		for _, m := range []string{"Page.enable", "Runtime.enable"} {
			if err := s.Call(ctx, m, nil, nil); err != nil {
				t.Fatal(m, err)
			}
		}
		if err := s.Call(ctx, "Page.navigate", map[string]any{"url": site + "/sso"}, nil); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if credentialHits.Load() > beforeC {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if credentialHits.Load() <= beforeC {
			t.Fatal("credential SSO hop did not reach the fixture")
		}
		if protectedHits.Load() != beforeP {
			t.Fatalf("protected hits during SSO: %d", protectedHits.Load()-beforeP)
		}
	})
	t.Run("lease-create-302", func(t *testing.T) {
		rows++
		before := protectedHits.Load()
		t.Setenv("SLICC_LEASE_HOP_ENDPOINT", owner.endpoint)
		t.Setenv("SLICC_LEASE_HOP_DEST", site+"/redirect")
		out, err := leaseHopPipeOverlay(t, nil, "^TestLeaseChromeCreateRedirectHop$")
		if err != nil {
			t.Fatalf("lease create 302: %v\n%s", err, out)
		}
		if got := protectedHits.Load() - before; got != 0 {
			t.Fatalf("lease create 302: protected fixture hits=%d (want 0; on main createTarget{url:dest} lets the first hop complete before Fetch arms)", got)
		}
	})
	t.Logf("lease-chrome-summary test=TestSignedLeaseProtectedHopsChrome rows=%d protected_fixture_hits=%d credential_fixture_hits=%d first_hop_status=%d", rows, protectedHits.Load(), credentialHits.Load(), firstHopStatus.Load())
}
