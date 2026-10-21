package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

const darwinThrottledPriority = 4

func TestHarnessChromeKeepsAHiddenTabOutOfMacOSBackgroundBand(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only macOS throttles a hidden tab's renderer to the background band")
	}
	b := launchBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>tab</title>"))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	hidden := openTab(ctx, t, b, srv.URL+"/hidden")
	openTab(ctx, t, b, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)+"/front")
	waitEval(ctx, t, hidden, "document.visibilityState === 'hidden'")
	time.Sleep(3 * time.Second)
	before := rendererCPU(ctx, t, b)
	var done bool
	if err := hidden.Evaluate(ctx, "(() => { const end = performance.now() + 2000; while (performance.now() < end); return true; })()", cdp.EvaluateOptions{ReturnByValue: true}, &done); err != nil || !done {
		t.Fatalf("busy the hidden tab: %v", err)
	}
	after := rendererCPU(ctx, t, b)
	busiest, most := 0, 0.0
	for pid, c := range after {
		if d := c - before[pid]; d > most {
			busiest, most = pid, d
		}
	}
	if busiest == 0 {
		t.Fatalf("no renderer ran the hidden tab's script: %v then %v", before, after)
	}
	time.Sleep(2 * time.Second)
	pri := priorities(after)
	if p, ok := pri[busiest]; !ok || p <= darwinThrottledPriority {
		t.Errorf("the hidden tab's renderer %d idles at priority %d (renderers %v); macOS starves that band under load, and a link page in it loses its link", busiest, p, pri)
	}
}

func openTab(ctx context.Context, t *testing.T, b *chromeProc, url string) *cdp.Session {
	t.Helper()
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": url}, &res); err != nil {
		t.Fatalf("Target.createTarget %s: %v", url, err)
	}
	s, err := b.client.Attach(ctx, res.TargetID)
	if err != nil {
		t.Fatalf("attach to %s: %v", url, err)
	}
	waitEval(ctx, t, s, "document.readyState === 'complete'")
	return s
}

func waitEval(ctx context.Context, t *testing.T, s *cdp.Session, expr string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var ok bool
		if err := s.Evaluate(ctx, expr, cdp.EvaluateOptions{ReturnByValue: true}, &ok); err == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never held", expr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func rendererCPU(ctx context.Context, t *testing.T, b *chromeProc) map[int]float64 {
	t.Helper()
	var info struct {
		ProcessInfo []struct {
			Type    string  `json:"type"`
			ID      int     `json:"id"`
			CPUTime float64 `json:"cpuTime"`
		} `json:"processInfo"`
	}
	if err := b.client.Call(ctx, "", "SystemInfo.getProcessInfo", nil, &info); err != nil {
		t.Fatalf("SystemInfo.getProcessInfo: %v", err)
	}
	out := map[int]float64{}
	for _, p := range info.ProcessInfo {
		if p.Type == "renderer" {
			out[p.ID] = p.CPUTime
		}
	}
	return out
}

func priorities(pids map[int]float64) map[int]int {
	out := map[int]int{}
	for pid := range pids {
		b, err := exec.Command("ps", "-o", "pri=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			continue
		}
		if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			out[pid] = p
		}
	}
	return out
}
