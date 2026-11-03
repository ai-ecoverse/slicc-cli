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
	testSixNavigation(t, "document")
}

func TestSixSignallingRechecksSameDocumentPath(t *testing.T) {
	for _, method := range []string{"pushState", "replaceState", "hash"} {
		t.Run(method, func(t *testing.T) { testSixNavigation(t, method) })
	}
}

func testSixNavigation(t *testing.T, method string) {
	t.Helper()
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
	f := startFlow(t, append(sixEnv(h, "isolated"), "SLICC_DEBUG=1"), "--plain", "--profile", "six", "--cdp", b.endpoint, "--no-kernel")
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
	if method != "document" {
		testSixRoutes(t, ctx, s, method, waitHellos, waitLeft, f.stderr.String)
	} else {
		for i, path := range []string{"/", "/os/", "/settings", "/7/back"} {
			navigate(path)
			if strings.HasPrefix(path, "/7/") || strings.HasPrefix(path, "/os/") {
				waitHellos(1)
				continue
			}
			waitLeft(i/2 + 1)
			waitHellos(0)
		}
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func testSixRoutes(t *testing.T, ctx context.Context, s *cdp.Session, method string, waitHellos, waitLeft func(int), logs func() string) {
	t.Helper()
	eval := func(expr string, out any) {
		t.Helper()
		if err := s.Evaluate(ctx, expr, cdp.EvaluateOptions{ReturnByValue: true, AwaitPromise: true}, out); err != nil {
			t.Fatal(err)
		}
	}
	route := func(path string) {
		t.Helper()
		encoded, _ := json.Marshal(path)
		operation := method
		if method == "hash" {
			operation = "pushState"
		}
		eval("history."+operation+"({}, '', "+string(encoded)+")", nil)
		if method == "hash" {
			eval("location.hash = 'route'", nil)
		}
	}
	settle := func(want int) {
		t.Helper()
		eval("new Promise(resolve => setTimeout(resolve, 200))", nil)
		waitHellos(want)
	}

	probe := func(allowed bool) {
		t.Helper()
		marker := "signal: candidate from the page"
		if !allowed {
			marker = "signal: ignoring sliccLinkSend from context"
		}
		before := strings.Count(logs(), marker)
		eval("sliccLinkSend(JSON.stringify({t: 'candidate', nonce: hellos.at(-1).nonce, candidate: null}))", nil)
		deadline := time.Now().Add(5 * time.Second)
		for strings.Count(logs(), marker) <= before {
			if time.Now().After(deadline) {
				t.Fatalf("no signalling probe result: %s", marker)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	var first string
	eval("hellos[0].nonce", &first)
	route("/os/within")
	settle(1)
	probe(true)
	eval("globalThis.child = document.createElement('iframe'); child.src = '/7/child'; document.body.append(child); new Promise(resolve => child.onload = resolve)", nil)
	eval("child.contentWindow.history.replaceState({}, '', '/settings#child')", nil)
	settle(1)
	probe(true)
	for i, path := range []string{"/settings", "/"} {
		route(path)
		waitLeft(i + 1)
		settle(i + 1)
		probe(false)
		if method == "hash" {
			eval("location.hash = 'still-outside'", nil)
			settle(i + 1)
			probe(false)
		}
		allowed := "/7/return"
		if i == 1 {
			allowed = "/os/return"
		}
		route(allowed)
		waitHellos(i + 2)
		probe(true)
		var nonce string
		eval(fmt.Sprintf("hellos[%d].nonce", i+1), &nonce)
		if nonce == first {
			t.Fatal("return reused the old nonce")
		}
		first = nonce
	}
}
