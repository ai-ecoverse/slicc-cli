package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

const cdpToken = "9f2c4e6a8b0d1f3e5a7c9e1b3d5f7a9c"

func tokenCDP(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + cdpToken + "/json/version":
			ws := "ws://" + r.Host + "/devtools/browser/" + cdpToken
			_ = json.NewEncoder(w).Encode(map[string]string{"Browser": "Quiet (WebKit)", "Protocol-Version": "1.3", "User-Agent": "Version/27.0 Safari/604.1", "webSocketDebuggerUrl": ws})
		case "/" + cdpToken + "/json/list":
			_, _ = w.Write([]byte(`[{"id":"a","type":"page","title":"t","url":"https://example.test/"}]`))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestBrowsersKeepsATokenInTheEndpointPathAndNeverPrintsIt(t *testing.T) {
	srv := tokenCDP(t)
	env := isolatedEnv(t)
	for _, u := range []string{srv.URL + "/" + cdpToken, srv.URL + "/" + cdpToken + "/", srv.URL + "/" + cdpToken + "/json/version"} {
		out, errOut, code := runWithEnv(t, env, "browsers", "--cdp", u)
		if code != 0 || !strings.Contains(out, "Quiet (WebKit)") {
			t.Fatalf("browsers --cdp: code %d, stdout %q, stderr %q", code, out, errOut)
		}
		if strings.Contains(out+errOut, cdpToken) {
			t.Fatalf("the table printed the token: %q %q", out, errOut)
		}
	}
	out, _, code := runWithEnv(t, env, "browsers", "--json", "--cdp", srv.URL+"/"+cdpToken)
	if code != 0 {
		t.Fatalf("browsers --json: code %d", code)
	}
	var r struct {
		Entries []struct {
			Endpoint struct {
				CDP string `json:"cdp"`
			} `json:"endpoint"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Entries) != 1 || !strings.HasSuffix(r.Entries[0].Endpoint.CDP, "/"+cdpToken) {
		t.Fatalf("--json lost the token, which a script needs to connect: %v %q", err, out)
	}
	_, errOut, code := runWithEnv(t, env, "browsers", "--cdp", srv.URL+"/"+cdpToken+"-wrong")
	if code != 0 || !strings.Contains(errOut, "403") || strings.Contains(errOut, cdpToken) {
		t.Fatalf("a refused token: code %d, stderr %q", code, errOut)
	}
}

func TestAttachNamesABrowserByItsBareProductAndHidesTheToken(t *testing.T) {
	srv := tokenCDP(t)
	cmd := exec.Command(binary, "attach", "--cdp", srv.URL+"/"+cdpToken, "--signal", "stdio", "--no-kernel", "--no-jerk")
	cmd.Env = append(isolatedEnv(t), "SLICC_DEBUG=1")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	lines := make(chan string)
	go func() {
		defer close(lines)
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			lines <- s.Text()
		}
	}()
	want := fmt.Sprintf("cdp: browser quiet is Quiet (WebKit) at %s/…", srv.URL)
	deadline := time.After(30 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("attach ended before %q", want)
			}
			if strings.Contains(line, cdpToken) {
				t.Fatalf("attach printed the token: %q", line)
			}
			if strings.Contains(line, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no line %q", want)
		}
	}
}

func TestAttachKeepsATokenOutOfItsConnectError(t *testing.T) {
	srv := tokenCDP(t)
	r := runBin(t, isolatedEnv(t), binary, "attach", "--cdp", srv.URL+"/"+cdpToken)
	if r.code != 1 || strings.Contains(r.stderr, cdpToken) || !strings.Contains(r.stderr, srv.Listener.Addr().String()) {
		t.Fatalf("%+v", r)
	}
}

func TestAttachConnectsThroughADiscoveryRouteInItsEndpoint(t *testing.T) {
	for _, route := range []string{"/json/version", "/json/version/", "/json/list", "/json"} {
		t.Run(strings.ReplaceAll(strings.Trim(route, "/"), "/", "-"), func(t *testing.T) {
			url := narrowBrowserUnder(t, "/"+cdpToken, true)
			lines := attachLines(t, isolatedEnv(t, "SLICC_DEBUG=1"), "--cdp", url+"/"+cdpToken+route)
			seeLineHiding(t, lines, "connected to "+url, cdpToken)
			seeLineHiding(t, lines, "cdp: browser narrow is", cdpToken)
		})
	}
}

func seeLineHiding(t *testing.T, lines <-chan string, want, secret string) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("attach ended before %q", want)
			}
			if strings.Contains(line, secret) {
				t.Fatalf("attach printed the token: %q", line)
			}
			if strings.Contains(line, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no line %q", want)
		}
	}
}

func TestDiscoverAsksTheSameRouteAsBrowserDiscovery(t *testing.T) {
	const escaped = "/" + cdpToken + "%2Fmore"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != escaped+"/json/version" {
			t.Errorf("asked for %s", r.URL.EscapedPath())
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/x"})
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, route := range []string{"", "/", "/json/version", "/json/version/", "/json/list", "/json", "/json/version?x=1"} {
		if ws, err := cdp.Discover(ctx, srv.URL+escaped+route); err != nil || !strings.HasSuffix(ws, "/devtools/browser/x") {
			t.Fatalf("%q: %q, %v", route, ws, err)
		}
	}
}

func TestABadEndpointKeepsItsTokenOutOfTheError(t *testing.T) {
	env := isolatedEnv(t)
	for _, url := range []string{
		"ftp://127.0.0.1:1/" + cdpToken,
		"http://127.0.0.1:1/" + cdpToken + "%zz",
		"http://example.invalid/" + cdpToken,
	} {
		for _, args := range [][]string{
			{"attach", "--no-kernel", "--no-jerk", "--cdp", url},
			{"attach", "--no-kernel", "--no-jerk", "--signal", "stdio", "--cdp", url},
			{"browsers", "--cdp", url},
			{"prompt", "--cdp", url, "hi"},
		} {
			if r := runBin(t, env, binary, args...); r.stdout+r.stderr == "" || strings.Contains(r.stdout+r.stderr, cdpToken) {
				t.Fatalf("%q: %+v", args, r)
			}
		}
	}
}
