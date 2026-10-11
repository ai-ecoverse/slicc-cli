package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

var corpusNeeds = []string{
	sliccbrowsers.NeedUserDataDirs, sliccbrowsers.NeedProfiles, sliccbrowsers.NeedFlag, sliccbrowsers.NeedProcess,
	sliccbrowsers.NeedProbe, sliccbrowsers.NeedOrigins, sliccbrowsers.NeedInstalled,
}

func TestCorpus(t *testing.T) {
	if os.Getenv(browser.DiscoveryEnv) != "isolated" {
		t.Fatalf("%s must be isolated for the corpus", browser.DiscoveryEnv)
	}
	cases, err := sliccbrowsers.Corpus()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("empty corpus")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if !c.Runs(runtime.GOOS, corpusNeeds...) {
				t.Skipf("not for %s", runtime.GOOS)
			}
			runCorpusCase(t, c)
		})
	}
}

func runCorpusCase(t *testing.T, c sliccbrowsers.Case) {
	home := t.TempDir()
	base := sliccbrowsers.Layout{OS: runtime.GOOS, Home: home}
	env := c.Env(base)
	getenv := func(k string) string { return env[k] }
	l := sliccbrowsers.Layout{OS: runtime.GOOS, Home: home, Getenv: getenv}
	for path, body := range c.Files(l) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	table := []byte(c.PSOutput(l, os.Getpid()))
	if runtime.GOOS == "windows" {
		table = []byte(c.CIMOutput(l, os.Getpid()))
	}
	o := browser.Options{
		Home: home, Getenv: getenv, CDPURLs: c.CDP(l), Origins: c.Input.Origins,
		ProbePorts: c.Input.ProbePorts, NoProbe: len(c.Input.ProbePorts) == 0, Timeout: 2 * time.Second,
		Processes: func(context.Context) ([]byte, error) { return table, nil },
		Transport: c.Transport(), Dial: c.DialContext,
	}
	if o.ProbePorts == nil {
		o.ProbePorts = []int{}
	}
	eps, _ := browser.DiscoverEndpoints(context.Background(), o)
	var installed []browser.Installed
	if len(c.Input.Installed) > 0 {
		if err := json.Unmarshal(c.Input.Installed, &installed); err != nil {
			t.Fatal(err)
		}
	}
	report := browser.NewReport(browser.Rank(eps, installed))
	if out := os.Getenv("SLICC_CORPUS_WRITE"); out != "" {
		writeCorpusExpected(t, out, c.Name, report, l)
		return
	}
	got, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	want, err := c.ExpectedJSON(l)
	if err != nil {
		t.Fatal(err)
	}
	if g, w := canonicalJSON(t, got), canonicalJSON(t, want); g != w {
		t.Errorf("got\n%s\nwant\n%s", g, w)
	}
}

func canonicalJSON(t *testing.T, b []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	return string(out)
}

func writeCorpusExpected(t *testing.T, dir, name string, r browser.Report, l sliccbrowsers.Layout) {
	tokens := []string{"sevenProfiles", "home"}
	for _, b := range sliccbrowsers.Default().Browsers {
		tokens = append(tokens, "userData:"+b.ID)
	}
	for _, e := range r.Entries {
		if e.Endpoint != nil {
			e.Endpoint.UserDataDir = tokenize(e.Endpoint.UserDataDir, l, tokens)
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
	if err := os.WriteFile(filepath.Join(dir, name, "expected.json"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tokenize(x string, l sliccbrowsers.Layout, tokens []string) string {
	best, bestVal := "", ""
	for _, tok := range tokens {
		val, ok := l.Token(tok)
		if !ok || val == "" || len(val) <= len(bestVal) || !strings.HasPrefix(x, val) {
			continue
		}
		if rest := x[len(val):]; rest == "" || rest[0] == '/' || rest[0] == '\\' {
			best, bestVal = tok, val
		}
	}
	if best == "" {
		return x
	}
	return "{" + best + "}" + filepath.ToSlash(x[len(bestVal):])
}

func TestTheProcessScanIsFakedOrOff(t *testing.T) {
	t.Setenv("FAKE_ELECTRON_MODE", "close")
	in := buildFakeElectron(t, "fakescan")
	b := launchFake(t, in)
	defer closeAndCheck(t, b)
	port := portOf(t, b.CDP)
	has := func(eps []browser.Endpoint) *browser.Endpoint {
		for i := range eps {
			if eps[i].CDP == b.CDP {
				return &eps[i]
			}
		}
		return nil
	}
	home := t.TempDir()
	eps, _ := browser.DiscoverEndpoints(context.Background(), browser.Options{Home: home})
	if ep := has(eps); ep != nil {
		t.Fatalf("isolated discovery scanned the real process list: %+v", ep)
	}
	out, _, code := slicc(t, "browsers", "--json")
	if code != 0 || strings.Contains(out, b.CDP) {
		t.Fatalf("slicc browsers scanned the real process list: %d %s", code, out)
	}
	cmd := `"` + in.Executable + `" --remote-debugging-port=` + port + ` --user-data-dir=` + home
	table := []byte(fmt.Sprintf("%d %s\n", b.PID, cmd))
	if runtime.GOOS == "windows" {
		table, _ = json.Marshal([]map[string]any{{"ProcessId": b.PID, "CommandLine": cmd}})
	}
	o := browser.Options{Home: home, Processes: func(context.Context) ([]byte, error) { return table, nil }}
	eps, _ = browser.DiscoverEndpoints(context.Background(), o)
	ep := has(eps)
	if ep == nil || ep.Source != browser.SourceProcess || ep.UserDataDir != home || ep.WebSocketURL != b.WebSocketURL {
		t.Fatalf("the faked process scan: %+v in %+v", ep, eps)
	}
}
