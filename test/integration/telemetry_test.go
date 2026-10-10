package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type beacon struct {
	Checkpoint string `json:"checkpoint"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Referer    string `json:"referer"`
	raw        string
}

type collector struct {
	*httptest.Server
	mu      sync.Mutex
	beacons []beacon
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{}
	c.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var b beacon
		_ = json.Unmarshal(body, &b)
		b.raw = string(body)
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/.rum/") {
			c.mu.Lock()
			c.beacons = append(c.beacons, b)
			c.mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	c.Start()
	t.Cleanup(c.Close)
	return c
}

func (c *collector) env() []string {
	return []string{"OPTEL_RATE=on", "SLICC_TELEMETRY_URL=" + c.URL + "/"}
}

func (c *collector) got() []beacon {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]beacon(nil), c.beacons...)
}

func (c *collector) find(checkpoint string) *beacon {
	for _, b := range c.got() {
		if b.Checkpoint == checkpoint {
			return &b
		}
	}
	return nil
}

func TestTelemetrySendsAnEnterBeacon(t *testing.T) {
	gh := newFakeGitHub(t, "v1.0.0", nil)
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	r := runBin(t, gh.env(t, col.env()...), bin, "update", "--check")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	enter := col.find("enter")
	if enter == nil || enter.Source != "update" || !strings.Contains(enter.Referer, "slicc-cli") {
		t.Fatalf("beacons: %+v", col.got())
	}
	if col.find("error") != nil {
		t.Fatalf("unexpected error beacon: %+v", col.got())
	}
}

func TestTelemetryReportsUpdateErrors(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", nil, withStatus(http.StatusBadGateway))
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	r := runBin(t, gh.env(t, col.env()...), bin, "update")
	if r.code != 1 {
		t.Fatalf("%+v", r)
	}
	failure := col.find("error")
	if failure == nil || failure.Source != "update" || !strings.Contains(failure.Target, "HTTP 502") {
		t.Fatalf("beacons: %+v", col.got())
	}
}

func TestTelemetryNeverSendsTypedArguments(t *testing.T) {
	gh := newFakeGitHub(t, "v1.0.0", nil)
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	runBin(t, gh.env(t, col.env()...), bin, "frobnicate-s3cr3t", "--token=s3cr3t")
	enter := col.find("enter")
	if enter == nil || enter.Source != "unknown" {
		t.Fatalf("beacons: %+v", col.got())
	}
	for _, b := range col.got() {
		if strings.Contains(b.raw, "s3cr3t") {
			t.Fatalf("leaked an argument: %s", b.raw)
		}
	}
}

func TestTelemetryOptOutAndDevBuildsSendNothing(t *testing.T) {
	gh := newFakeGitHub(t, "v1.0.0", nil)
	col := newCollector(t)
	release := buildVersion(t, "1.0.0", t.TempDir())
	runs := []struct {
		name string
		bin  string
		env  []string
		args []string
	}{
		{"opt-out", release, []string{"SLICC_NO_TELEMETRY=1"}, []string{"update", "--check"}},
		{"dev build", binary, nil, []string{"update", "--check"}},
		{"version", release, nil, []string{"--version"}},
		{"help", release, nil, []string{"--help"}},
	}
	for _, run := range runs {
		runBin(t, gh.env(t, append(col.env(), run.env...)...), run.bin, run.args...)
		if got := col.got(); len(got) != 0 {
			t.Fatalf("%s sent %d beacons: %+v", run.name, len(got), got)
		}
	}
}
