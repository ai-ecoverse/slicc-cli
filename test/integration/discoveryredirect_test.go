package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

func TestCDPDiscoveryClientDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	protected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(http.StatusOK) }))
	t.Cleanup(protected.Close)
	ordinary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, protected.URL+"/protected", http.StatusFound)
	}))
	t.Cleanup(ordinary.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cdp.Discover(ctx, ordinary.URL); err == nil {
		t.Fatal("discovery accepted or followed redirect")
	}
	if hits.Load() != 0 {
		t.Fatalf("client-side CDP discovery sent redirected request: %d", hits.Load())
	}
}
