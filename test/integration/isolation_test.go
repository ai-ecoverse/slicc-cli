package integration

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

var realEnv []string

func isolateProcess(root string) {
	realEnv = os.Environ()
	for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		if out, err := exec.Command("go", "env", key).Output(); err == nil {
			_ = os.Setenv(key, strings.TrimSpace(string(out)))
		}
	}
	set := map[string]string{
		"HOME":            root,
		"USERPROFILE":     root,
		"APPDATA":         filepath.Join(root, "Roaming"),
		"LOCALAPPDATA":    filepath.Join(root, "Local"),
		"XDG_CONFIG_HOME": filepath.Join(root, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(root, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(root, ".local", "share"),
	}
	for k, v := range set {
		_ = os.MkdirAll(v, 0o700)
		_ = os.Setenv(k, v)
	}
	_ = os.Setenv(browser.DiscoveryEnv, "isolated")
}

func withDiscovery(env []string, mode string) []string {
	return overrideEnv(env, map[string]string{browser.DiscoveryEnv: mode})
}

func realGetenv(key string) string {
	for _, kv := range realEnv {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func probedPortListener(t *testing.T) (net.Listener, int) {
	t.Helper()
	for _, port := range browser.DefaultProbePorts {
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			return ln, port
		}
	}
	if os.Getenv("CI") != "" {
		t.Fatal("every probed CDP port is taken")
	}
	t.Skip("every probed CDP port is taken on this machine")
	return nil, 0
}

func TestTheHarnessNeverReachesRealBrowsers(t *testing.T) {
	ln, port := probedPortListener(t)
	var hits atomic.Int32
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Browser":"Chrome/150.0.0.0","webSocketDebuggerUrl":"ws://127.0.0.1:`+strconv.Itoa(port)+`/devtools/browser/x"}`)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	if os.Getenv(browser.DiscoveryEnv) != "isolated" {
		t.Fatalf("%s is %q in the test process", browser.DiscoveryEnv, os.Getenv(browser.DiscoveryEnv))
	}

	cmd := exec.Command(binary, "--plain")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("bare slicc --plain kept running under the harness: %s", stderr.String())
	}
	if code := cmd.ProcessState.ExitCode(); code != 1 || !strings.Contains(stderr.String(), "no browser that can host seven") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}

	out, _, code := slicc(t, "browsers", "--json")
	var r report
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || len(r.Entries) != 0 {
		t.Fatalf("browsers --json under the harness: code %d, %s", code, out)
	}
	eps, _ := browser.DiscoverEndpoints(context.Background(), browser.Options{})
	if len(eps) != 0 || len(browser.DiscoverInstalled(context.Background(), browser.Options{})) != 0 {
		t.Fatalf("in-process discovery found %v", eps)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the fake browser on :%d got %d requests", port, n)
	}

	eps, _ = browser.DiscoverEndpoints(context.Background(), browser.Options{ProbePorts: []int{port}})
	if len(eps) != 1 || hits.Load() == 0 {
		t.Fatalf("an injected probe port is still probed: %v", eps)
	}
}
