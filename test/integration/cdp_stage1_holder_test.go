package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	capcdp "github.com/ai-ecoverse/slicc-cli/internal/caps/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/test/integration/testdata/stage1"
)

func Test675Stage1HolderDeathEndsDebuggerAndReleasesRealChrome(t *testing.T) {
	if os.Getenv("SLICC_675_STAGE1_DIAGNOSTIC") != "1" {
		t.Skip("draft candidate row")
	}
	continued := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/continued" {
			continued <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script>globalThis.stage1Effect=1;fetch('/continued');</script></body></html>`))
	}))
	defer server.Close()
	url := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	chrome := launchBrowser(t)
	registry := capcdp.NewRegistry(nil)
	entry, err := registry.Add(t.Context(), chrome.endpoint, false)
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := stage1.Open(t, registry.Handler(), entry.ID)
	client := stage1.NewClient(t, stream)
	created := stage1.Result(t, client.Call(t, "Target.createTarget", "", map[string]any{"url": "about:blank"}))
	id, _ := created["targetId"].(string)
	attached := stage1.Result(t, client.Call(t, "Target.attachToTarget", "", map[string]any{"targetId": id, "flatten": true}))
	session, _ := attached["sessionId"].(string)
	stage1.Result(t, client.Call(t, "Page.enable", session, nil))
	stage1.Result(t, client.Call(t, "Page.navigate", session, map[string]any{"url": url + "/seed"}))
	select {
	case <-continued:
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary predecessor did not execute")
	}
	stage1.Result(t, client.Call(t, "Debugger.enable", session, nil))
	stage1.Result(t, client.Call(t, "Debugger.setInstrumentationBreakpoint", session, map[string]any{"instrumentation": "beforeScriptExecution"}))
	stage1.Result(t, client.Call(t, "Page.reload", session, nil))
	pause := client.Paused(t)
	params, _ := pause["params"].(map[string]any)
	if params["reason"] == "instrumentation" {
		t.Fatal("instrumentation pause escaped")
	}
	select {
	case <-continued:
		t.Fatal("page code ran before holder death")
	default:
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	observer, err := cdp.Dial(ctx, chrome.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close() }()
	var observed struct {
		SessionID string `json:"sessionId"`
	}
	if err := observer.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": id, "flatten": true}, &observed); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-continued:
	case <-ctx.Done():
		t.Fatal("holder death left real Chrome paused past the rescue bound")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("rescue exceeded its bound")
	}
	var marker struct {
		Result struct {
			Value int `json:"value"`
		} `json:"result"`
	}
	if err := observer.Call(ctx, observed.SessionID, "Runtime.evaluate", map[string]any{"expression": "globalThis.stage1Effect", "returnByValue": true}, &marker); err != nil || marker.Result.Value != 1 {
		t.Fatal("raw observer did not see the page continue")
	}
	if err := observer.Call(ctx, observed.SessionID, "Page.reload", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-continued:
	case <-ctx.Done():
		t.Fatal("owned instrumentation survived holder death and paused the next reload")
	}
}
