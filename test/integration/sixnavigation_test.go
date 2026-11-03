package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

func TestSixSignallingRechecksPathAfterNavigation(t *testing.T) {
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP www.sliccy.ai ~NOTFOUND, MAP seven.sliccy.ai ~NOTFOUND")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var target struct {
		ID string `json:"targetId"`
	}
	if err := b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &target); err != nil {
		t.Fatal(err)
	}
	s, err := b.client.Attach(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	sub := b.client.Subscribe()
	defer sub.Close()
	if err := s.Call(ctx, "Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*"}}}, nil); err != nil {
		t.Fatal(err)
	}
	body := base64.StdEncoding.EncodeToString([]byte(`<script>globalThis.hellos = []; globalThis.sliccLinkReceive = (m) => { if (m.t === 'hello') hellos.push(m); };</script>`))
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case ev, ok := <-sub.Events():
				if !ok {
					done <- b.client.Err()
					return
				}
				if ev.SessionID != s.ID || ev.Method != "Fetch.requestPaused" {
					continue
				}
				var p struct {
					ID string `json:"requestId"`
				}
				if err := json.Unmarshal(ev.Params, &p); err != nil {
					done <- err
					return
				}
				if err := s.Call(ctx, "Fetch.fulfillRequest", map[string]any{"requestId": p.ID, "responseCode": 200, "responseHeaders": []map[string]string{{"name": "Content-Type", "value": "text/html"}}, "body": body}, nil); err != nil {
					done <- err
					return
				}
			case <-ctx.Done():
				done <- nil
				return
			}
		}
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	navigate := func(path string) {
		t.Helper()
		if err := s.Call(ctx, "Page.navigate", map[string]any{"url": "https://www.sliccy.ai" + path}, nil); err != nil {
			t.Fatal(err)
		}
	}
	navigate("/7/")
	waitTabs(t, b, flow.SixPage, 1)
	v, err := getVersion(b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := url.Parse(fmt.Sprint(v["webSocketDebuggerUrl"]))
	h := newFakeHome(t)
	dir := filepath.Join(sixRootIn(h), "browser-coding-agent-chrome")
	makeSixTree(t, dir, fmt.Sprintf("%d\n%s", b.port, ws.Path))
	holdSixLock(t, dir, b.cmd.Process.Pid)
	f := startFlow(t, sixEnv(h, "isolated"), "--plain", "--profile", "six", "--cdp", b.endpoint, "--no-kernel")
	waitLeft := func(want int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for strings.Count(f.stderr.String(), "the seven tab left seven") < want {
			if time.Now().After(deadline) {
				t.Fatalf("no left-seven notice %d", want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitHellos := func(want int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var count int
			err := s.Evaluate(ctx, "document.readyState === 'complete' ? globalThis.hellos.length : -1", cdp.EvaluateOptions{ReturnByValue: true}, &count)
			if err == nil && count == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("hellos = %d, want %d: %v", count, want, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitHellos(1)
	for i, path := range []string{"/", "/os/", "/settings", "/7/back"} {
		navigate(path)
		if strings.HasPrefix(path, "/7/") || strings.HasPrefix(path, "/os/") {
			waitHellos(1)
			continue
		}
		waitLeft(i/2 + 1)
		waitHellos(0)
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}
