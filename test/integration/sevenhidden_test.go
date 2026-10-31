package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func narrowBrowser(t *testing.T, browserSession bool) string {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		ws := "ws" + strings.TrimPrefix(srv.URL, "http") + "/devtools/browser/x"
		_ = json.NewEncoder(w).Encode(map[string]string{"Browser": "Narrow/1.0", "Protocol-Version": "1.3", "webSocketDebuggerUrl": ws})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("/devtools/browser/x", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		for {
			_, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var m struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(data, &m) != nil {
				return
			}
			out := map[string]any{"id": m.ID, "result": map[string]any{}}
			switch m.Method {
			case "Target.getTargets":
				out["result"] = map[string]any{"targetInfos": []any{}}
			case "Target.createTarget":
				delete(out, "result")
				out["error"] = map[string]any{"code": -32000, "message": "This target is out of reach"}
			case "Target.attachToBrowserTarget":
				if browserSession {
					out["result"] = map[string]any{"sessionId": "browser"}
					break
				}
				delete(out, "result")
				out["error"] = map[string]any{"code": -32601, "message": "'Target.attachToBrowserTarget' wasn't found"}
			}
			b, _ := json.Marshal(out)
			if c.Write(context.Background(), websocket.MessageText, b) != nil {
				return
			}
		}
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func attachLines(t *testing.T, env []string, args ...string) <-chan string {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"attach", "--no-kernel", "--no-jerk"}, args...)...)
	cmd.Env = env
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			lines <- s.Text()
		}
	}()
	return lines
}

func seeLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("attach ended before %q", want)
			}
			if strings.Contains(line, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no line %q", want)
		}
	}
}

func TestAttachSaysWhatToDoWhenNoSevenTabEverShows(t *testing.T) {
	lines := attachLines(t, isolatedEnv(t, "SLICC_SEVEN_QUIET=100ms"), "--cdp", narrowBrowser(t, true))
	seeLine(t, lines, "waiting for a seven tab")
	seeLine(t, lines, "still no seven tab")
}

func TestABrowserWithoutABrowserSessionIsNotOffered(t *testing.T) {
	url := narrowBrowser(t, false)
	lines := attachLines(t, isolatedEnv(t, "SLICC_DEBUG=1"), "--cdp", url)
	seeLine(t, lines, "seven can't drive this browser through slicc")
}

func TestTheDefaultFlowExplainsABrowserThatWontOpenSeven(t *testing.T) {
	url := narrowBrowser(t, true)
	id := "cdp:" + strings.TrimPrefix(url, "http://")
	r := runBin(t, isolatedEnv(t), binary, "--cdp", url, "--browser", id, "--plain", "--yes", "--no-kernel", "--no-jerk")
	if r.code == 0 || !strings.Contains(r.stderr+r.stdout, "link it through seven's Link a device") {
		t.Fatalf("%+v", r)
	}
}
