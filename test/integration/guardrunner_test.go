package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	sliccbrowsers "github.com/ai-ecoverse/slicc-browsers"
)

const guardSetVersion = "1.7.12"

func guardSettleScale() int {
	switch runtime.GOOS {
	case "linux":
		return 1
	case "darwin":
		return 2
	}
	return 3
}

func guardSlots() int {
	return min(10, max(4, 2*runtime.NumCPU()))
}

func guardPackage(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "node_modules", "@ai-ecoverse", "slicc-browsers"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("@ai-ecoverse/slicc-browsers is missing; run npm ci")
		}
		t.Skip("@ai-ecoverse/slicc-browsers is not installed; run npm ci")
	}
	var pkg struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(b, &pkg)
	if pkg.Version != guardSetVersion {
		t.Fatalf("node_modules has @ai-ecoverse/slicc-browsers@%s, want %s; run npm ci", pkg.Version, guardSetVersion)
	}
	b, err = os.ReadFile(filepath.Join(dir, "guard-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var npm, gomod struct {
		Vectors []struct {
			ID string `json:"id"`
		} `json:"vectors"`
	}
	_ = json.Unmarshal(b, &npm)
	_ = json.Unmarshal(sliccbrowsers.GuardVectorsJSON(), &gomod)
	if len(npm.Vectors) == 0 || fmt.Sprint(npm.Vectors) != fmt.Sprint(gomod.Vectors) {
		t.Fatalf("the npm and Go pins of slicc-browsers carry different guard vectors")
	}
	return dir
}

type guardFixture struct {
	port int
	log  int
}

func startGuardFixture(t *testing.T) guardFixture {
	t.Helper()
	cmd := exec.Command("node", filepath.Join("testdata", "guard", "fixture.mjs"))
	cmd.Env = realEnv
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		reap(t, cmd, "the guard fixture")
	})
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(stdout).ReadString('\n')
		line <- s
	}()
	var f struct {
		Fixture int `json:"fixture"`
		Log     int `json:"log"`
	}
	select {
	case s := <-line:
		if err := json.Unmarshal([]byte(s), &f); err != nil || f.Fixture == 0 || f.Log == 0 {
			t.Fatalf("the guard fixture didn't start: %q %s", s, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the guard fixture didn't start: %s", stderr.String())
	}
	return guardFixture{port: f.Fixture, log: f.Log}
}

func guardPageServer(t *testing.T, pkg string, fixturePort int) *httptest.Server {
	t.Helper()
	root := filepath.Join("testdata", "link")
	files := http.FileServer(http.Dir(root))
	vendor := http.StripPrefix("/vendor/slicc-browsers/", http.FileServer(http.Dir(pkg)))
	upstream := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(fixturePort)}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(upstream)
		r.Out.Host = r.In.Host
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/vendor/slicc-browsers/") {
			if strings.HasSuffix(r.URL.Path, ".mjs") {
				w.Header().Set("Content-Type", "text/javascript")
			}
			vendor.ServeHTTP(w, r)
			return
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path.Clean(r.URL.Path)), "index.html")); r.URL.Path == "/" && err == nil {
			files.ServeHTTP(w, r)
			return
		}
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(path.Clean(r.URL.Path)))); err == nil && !st.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type guardRow struct {
	ID        string          `json:"id"`
	Target    string          `json:"target"`
	Variant   string          `json:"variant"`
	Transport string          `json:"transport"`
	Via       string          `json:"via"`
	Control   string          `json:"control"`
	Guarded   string          `json:"guarded"`
	Live      string          `json:"live"`
	Reason    string          `json:"reason"`
	Why       json.RawMessage `json:"why"`
	Errors    []string        `json:"errors"`
	Observed  json.RawMessage `json:"observations"`
}

type guardSummary struct {
	Set             string          `json:"set"`
	Pass            bool            `json:"pass"`
	Line            string          `json:"line"`
	Results         []guardRow      `json:"results"`
	Missing         json.RawMessage `json:"missing"`
	NotRun          []string        `json:"notRun"`
	ObserverSkipped int             `json:"observerSkipped"`
	Counts          json.RawMessage `json:"counts"`
}

const (
	guardRowTimeout = 3 * time.Minute
	guardStall      = guardRowTimeout + time.Minute
)

type guardCall struct {
	Method  string `json:"method"`
	Session string `json:"session"`
	AgeMS   int    `json:"ageMs"`
}

type guardInflight struct {
	ID      string                 `json:"id"`
	Target  string                 `json:"target"`
	Variant string                 `json:"variant"`
	Leg     string                 `json:"leg"`
	Phase   string                 `json:"phase"`
	Step    *int                   `json:"step"`
	MS      int                    `json:"ms"`
	Clients map[string][]guardCall `json:"clients"`
}

func (r guardInflight) String() string {
	out := fmt.Sprintf("%s %s %s running %s", r.ID, r.Target, r.Variant, time.Duration(r.MS)*time.Millisecond)
	if r.Leg != "" {
		out += fmt.Sprintf(", %s leg, %s", r.Leg, r.Phase)
		if r.Step != nil {
			out += fmt.Sprintf(" step %d", *r.Step)
		}
	}
	names := make([]string, 0, len(r.Clients))
	for name := range r.Clients {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		calls := r.Clients[name]
		if len(calls) == 0 {
			out += fmt.Sprintf("; %s: no pending call", name)
			continue
		}
		for _, c := range calls {
			out += fmt.Sprintf("; %s: %s (session %q) pending for %s", name, c.Method, c.Session, time.Duration(c.AgeMS)*time.Millisecond)
		}
	}
	return out
}

type guardProgress struct {
	Done     bool            `json:"done"`
	Error    string          `json:"error"`
	Rows     []guardRow      `json:"rows"`
	Inflight []guardInflight `json:"inflight"`
	Summary  *guardSummary   `json:"summary"`
}

func guardSHA() string {
	out, err := exec.Command("git", "-C", filepath.Join("..", ".."), "rev-parse", "HEAD").Output()
	if err == nil && len(strings.TrimSpace(string(out))) >= 7 {
		return strings.TrimSpace(string(out))[:7]
	}
	if s := os.Getenv("GITHUB_SHA"); len(s) >= 7 {
		return s[:7]
	}
	return "unknown"
}

func TestCDPProtectVectorsPrompt(t *testing.T) {
	runGuardVectors(t, "prompt")
}

func TestCDPProtectVectorsLagging(t *testing.T) {
	runGuardVectors(t, "lagging")
}

type guardRun struct {
	variant    string
	slots      int
	only       string
	inject     string
	rowTimeout time.Duration
}

func runGuardVectors(t *testing.T, variant string) {
	sum := runGuard(t, guardRun{variant: variant, slots: guardSlots(), rowTimeout: guardRowTimeout})
	checkGuardSummary(t, sum)
	fmt.Println(sum.Line)
	t.Log(sum.Line)
}

func runGuard(t *testing.T, run guardRun) *guardSummary {
	t.Helper()
	if os.Getenv("SLICC_GUARD_VECTORS") == "0" {
		t.Skip("the guard vectors run on Linux for pull requests and on every OS on main")
	}
	pkg := guardPackage(t)
	fix := startGuardFixture(t)
	srv := guardPageServer(t, pkg, fix.port)
	fp := strconv.Itoa(fix.port)
	seven := "http://seven.guard.test:" + fp
	targets := map[string]string{
		"seven":              seven,
		"vault":              "http://vault.sliccy.ai:" + fp,
		"vault-x":            "http://vault-x.sliccy.ai:" + fp,
		"apex":               "http://sliccy.ai:" + fp,
		"own":                srv.URL,
		"client":             srv.URL,
		"ordinary":           "http://ordinary.guard.test:" + fp,
		"ordinary-same-site": "http://page.guard.test:" + fp,
		"cross":              "http://cross.test:" + fp,
	}
	n := run.slots
	rules := "--host-resolver-rules=MAP *.sliccy.ai 127.0.0.1,MAP sliccy.ai 127.0.0.1,MAP *.test 127.0.0.1"
	chromes := make([]*chromeProc, n)
	for i := range chromes {
		chromes[i] = launchBrowserOn(t, 0, rules, "--remote-allow-origins="+srv.URL)
	}
	args := []string{"--cdp", chromes[0].endpoint}
	for _, b := range chromes[1:] {
		args = append(args, "--cdp-extra", b.endpoint)
	}
	args = append(args, "--origin", srv.URL, "--origin", seven, "--name", "it-node")
	env := &linkEnv{t: t, b: chromes[0], origin: srv.URL}
	env.a = startAttach(t, args...)
	env.p = chromes[0].openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != n {
		t.Fatalf("the link lists %d browsers, want %d: %+v", len(hello.Browsers), n, hello.Browsers)
	}
	type slot struct {
		ID string `json:"id"`
		WS string `json:"ws"`
	}
	slots := make([]slot, n)
	for i, b := range chromes {
		v, err := getVersion(b.endpoint)
		if err != nil {
			t.Fatal(err)
		}
		ws, _ := v["webSocketDebuggerUrl"].(string)
		slots[i] = slot{ID: hello.Browsers[i].ID, WS: ws}
	}
	sha := guardSHA()
	opts := map[string]any{
		"browsers":     slots,
		"targets":      targets,
		"capabilities": map[string]bool{"autoAttach": true, "browserSession": true, "browserContexts": true},
		"variants":     []string{run.variant},
		"parallel":     n,
		"log":          "http://127.0.0.1:" + strconv.Itoa(fix.log),
		"sha":          sha,
		"rowTimeoutMs": run.rowTimeout.Milliseconds(),
		"only":         run.only,
		"inject":       run.inject,
		"settleScale":  guardSettleScale(),
	}
	var started struct {
		Started bool `json:"started"`
	}
	env.p.eval("linkTest.guard.start("+jsArgs(opts)+")", &started)
	if !started.Started {
		t.Fatal("the guard run didn't start")
	}
	begin := time.Now()
	var sum *guardSummary
	seen := 0
	said := begin
	moved := begin
	for deadline := begin.Add(11 * time.Minute); sum == nil; time.Sleep(2 * time.Second) {
		var p guardProgress
		env.p.eval("linkTest.guard.progress("+strconv.Itoa(seen)+")", &p)
		seen += len(p.Rows)
		if len(p.Rows) > 0 {
			moved = time.Now()
		}
		if sum == nil && !p.Done && time.Since(moved) > guardStall {
			for _, r := range p.Inflight {
				t.Logf("in flight: %s", r)
			}
			t.Fatalf("the guard run stalled: no row finished for %s, with %d rows done and %d in flight", guardStall, seen, len(p.Inflight))
		}
		if time.Since(said) > 30*time.Second {
			said = time.Now()
			t.Logf("%d rows run after %s", seen, time.Since(begin).Round(time.Second))
		}
		if p.Error != "" {
			t.Fatalf("the guard run failed: %s", p.Error)
		}
		if p.Done {
			sum = p.Summary
		}
		if sum == nil && time.Now().After(deadline) {
			t.Fatalf("the guard run didn't finish in time; %d rows done", seen)
		}
	}
	t.Logf("%d rows in %s with %d slots on %s", len(sum.Results), time.Since(begin).Round(time.Second), n, runtime.GOOS)
	return sum
}

func TestCDPProtectVectorsIgnoreChromeUITargets(t *testing.T) {
	const id = "C3.redirect-popup-stays-out"
	sum := runGuard(t, guardRun{variant: "prompt", slots: 2, only: id, inject: "browser_ui", rowTimeout: 45 * time.Second})
	if len(sum.Results) == 0 {
		t.Fatalf("no rows for %s", id)
	}
	for _, r := range sum.Results {
		if r.ID != id {
			t.Errorf("the run has a row for %s, want only %s", r.ID, id)
		}
		if r.Control != "pass" || r.Guarded != "pass" || r.Live != "pass" {
			t.Errorf("%s %s: control %s, guarded %s, live %s\n  why %s\n  errors %v", r.ID, r.Target, r.Control, r.Guarded, r.Live, r.Why, r.Errors)
		}
	}
}

func checkGuardSummary(t *testing.T, sum *guardSummary) {
	t.Helper()
	if sum.Set != "@ai-ecoverse/slicc-browsers@"+guardSetVersion {
		t.Errorf("the run used %s, want @ai-ecoverse/slicc-browsers@%s", sum.Set, guardSetVersion)
	}
	if string(sum.Missing) != "[]" {
		t.Errorf("rows missing from the run: %s", sum.Missing)
	}
	if sum.ObserverSkipped != 0 {
		t.Errorf("%d rows skipped for want of an unguarded observer", sum.ObserverSkipped)
	}
	for _, r := range sum.Results {
		name := r.ID + " " + r.Target + " " + r.Variant
		switch {
		case r.Control == "not-run":
			if r.Guarded != "not-run" || r.Live != "not-run" {
				t.Errorf("%s: a text row ran: %+v", name, r)
			}
		case r.Control == "skip":
			if !strings.HasPrefix(r.Reason, "not-applicable") {
				t.Errorf("%s: skipped with %q", name, r.Reason)
			} else {
				t.Logf("%s: skip %s", name, r.Reason)
			}
		case r.Control != "pass" || r.Guarded != "pass" || r.Live != "pass":
			t.Errorf("%s: control %s, guarded %s, live %s\n  why %s\n  errors %v\n  observed %s", name, r.Control, r.Guarded, r.Live, r.Why, r.Errors, r.Observed)
		}
	}
	t.Logf("counts %s; not run %v", sum.Counts, sum.NotRun)
	passed := map[string]int{}
	for _, r := range sum.Results {
		if r.Control == "pass" && r.Guarded == "pass" && r.Live == "pass" {
			passed[r.ID]++
		}
	}
	var set struct {
		Vectors []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"vectors"`
	}
	_ = json.Unmarshal(sliccbrowsers.GuardVectorsJSON(), &set)
	var ran []string
	for _, v := range set.Vectors {
		if v.Kind != "cdp" {
			continue
		}
		if passed[v.ID] == 0 {
			t.Errorf("%s: no row of the vector passed", v.ID)
			continue
		}
		ran = append(ran, fmt.Sprintf("%s=%d", v.ID, passed[v.ID]))
	}
	t.Logf("passed rows per vector: %s", strings.Join(ran, " "))
	if !sum.Pass {
		t.Errorf("the guard set did not pass: %s", sum.Line)
	}
}
