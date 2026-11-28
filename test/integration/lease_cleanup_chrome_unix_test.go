//go:build !windows

package integration

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func runLeaseCleanupChrome(t *testing.T, mode string) {
	t.Helper()
	childDocRequireChrome(t)
	var protected atomic.Value
	var hits, triggered atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, "protected.cleanup.test:") {
			hits.Add(1)
			_, _ = fmt.Fprint(w, "<p>protected fixture</p>")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/redirect":
			triggered.Add(1)
			http.Redirect(w, r, protected.Load().(string)+"/destination", http.StatusFound)
		case "/child":
			triggered.Add(1)
			_, _ = fmt.Fprintf(w, `<!doctype html><body><p>ordinary fixture</p><iframe src="%s/destination"></iframe>`, protected.Load().(string))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	parsed, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	site := "https://billing.cleanup.test:" + parsed.Port()
	vault := "https://protected.cleanup.test:" + parsed.Port()
	protected.Store(vault)
	digest := sha256.Sum256(srv.Certificate().RawSubjectPublicKeyInfo)
	chrome := launchBrowserOn(t, 0,
		"--ignore-certificate-errors-spki-list="+base64.StdEncoding.EncodeToString(digest[:]),
		"--host-resolver-rules=MAP *.cleanup.test 127.0.0.1",
		"--no-proxy-server",
	)
	out, err := leaseCleanupRun(t, leaseCleanupRoot(t), leaseCleanupCache(t), "^TestLeaseCleanupLive$", []string{
		"SLICC_CLEANUP_LIVE=" + mode,
		"SLICC_CLEANUP_ENDPOINT=" + chrome.endpoint,
		"SLICC_CLEANUP_SITE=" + site,
		"SLICC_CLEANUP_PROTECTED=" + vault,
	}, nil)
	leaves := leaseCleanupLeaves(t, out, err)
	if err != nil {
		t.Fatalf("live cleanup %s: %v\n%s", mode, err, out)
	}
	want := int32(1)
	expected := []string{"TestLeaseCleanupLive/R15aRedirectToProtectedHostViaErrorPageNotDetach"}
	if mode == "child" {
		want = 2
		expected = []string{"TestLeaseCleanupLive/R15bProtectedChildFrameDoesNotCloseTheParent/Baseline", "TestLeaseCleanupLive/R15bProtectedChildFrameDoesNotCloseTheParent/Cleanup"}
	}
	leaseCleanupInventory(t, expected, leaves)
	if hits.Load() != 0 || triggered.Load() != want {
		t.Fatalf("fixture reached %d protected requests and %d trigger requests", hits.Load(), triggered.Load())
	}
}
