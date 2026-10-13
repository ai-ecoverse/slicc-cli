package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		"XDG_STATE_HOME":  filepath.Join(root, ".local", "state"),
	}
	if runtime.GOOS == "windows" {
		delete(set, "USERPROFILE")
		delete(set, "LOCALAPPDATA")
	}
	for k, v := range set {
		_ = os.MkdirAll(v, 0o700)
		_ = os.Setenv(k, v)
	}
	_ = os.Unsetenv("SLICC_USER_DATA_DIR")
	_ = os.Setenv(browser.DiscoveryEnv, "isolated")
	_ = os.Setenv("SLICC_OPEN_COMMAND", filepath.Join(root, "no-browser-opener"))
	_ = os.Setenv("SLICC_LOOPBACK_PORTS", harnessLoopbackPorts())
}

func harnessLoopbackPorts() string {
	for try := 0; try < 50; try++ {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			break
		}
		base := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		if base+9 <= 65535 {
			return fmt.Sprintf("%d-%d", base, base+9)
		}
	}
	return "47117-47126"
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

func TestTheHarnessNeverReachesRealBrowsers(t *testing.T) {
	if os.Getenv(browser.DiscoveryEnv) != "isolated" {
		t.Fatalf("%s is %q in the test process", browser.DiscoveryEnv, os.Getenv(browser.DiscoveryEnv))
	}
	var hits atomic.Int32
	var port int
	port = guard.lend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Browser":"Chrome/150.0.0.0","webSocketDebuggerUrl":"ws://127.0.0.1:`+strconv.Itoa(port)+`/devtools/browser/x"}`)
	}))

	cmd := exec.Command(binary, "--plain", "--no-fallback")
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
	if code := cmd.ProcessState.ExitCode(); code != 1 || !strings.Contains(stderr.String(), "no browser to control: run `slicc open` to link seven in your everyday browser") {
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
