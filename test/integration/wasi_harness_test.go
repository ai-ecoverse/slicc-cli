package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const wasiJoin = "https://tray.example.test/join/tok.00112233445566778899"

type wasiFirst struct {
	Type int `json:"type"`
	JSON struct {
		V    int            `json:"v"`
		Join string         `json:"join"`
		Open map[string]any `json:"open"`
	} `json:"json"`
	Keys []string `json:"-"`
}

func (f *wasiFirst) UnmarshalJSON(b []byte) error {
	type plain wasiFirst
	if err := json.Unmarshal(b, (*plain)(f)); err != nil {
		return err
	}
	var raw struct {
		JSON map[string]json.RawMessage `json:"json"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	f.Keys = f.Keys[:0]
	for k := range raw.JSON {
		f.Keys = append(f.Keys, k)
	}
	return nil
}

type wasiStamp struct {
	PID    int    `json:"pid"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	User   string `json:"user"`
	Groups []int  `json:"groups"`
	Users  string `json:"users"`
}

type wasiConn struct {
	Stamp    *wasiStamp       `json:"stamp"`
	Facade   string           `json:"facade"`
	First    *wasiFirst       `json:"first"`
	Records  []int            `json:"records"`
	Controls []map[string]any `json:"controls"`
	Bytes    int              `json:"bytes"`
	Ended    bool             `json:"ended"`
	Closed   bool             `json:"closed"`
	Text     string           `json:"text"`
}

type wasiResult struct {
	Name        string     `json:"name"`
	Status      int        `json:"status"`
	Interrupted bool       `json:"interrupted"`
	Stdout      string     `json:"stdout"`
	Stderr      string     `json:"stderr"`
	Log         []wasiConn `json:"log"`
	Error       string     `json:"error"`
}

type wasiCase struct {
	Name           string            `json:"name"`
	Argv           []string          `json:"argv"`
	Env            map[string]string `json:"env,omitempty"`
	LinkHost       string            `json:"linkHost,omitempty"`
	Hosts          string            `json:"hosts,omitempty"`
	Files          map[string]string `json:"files,omitempty"`
	User           string            `json:"user,omitempty"`
	Stdin          *string           `json:"stdin,omitempty"`
	TTY            bool              `json:"tty,omitempty"`
	Scenario       []map[string]any  `json:"scenario,omitempty"`
	InterruptAfter int               `json:"interruptAfter,omitempty"`
	Timeout        int               `json:"timeout,omitempty"`
	check          func(t *testing.T, r wasiResult)
}

var wasiBuild = struct {
	once sync.Once
	dir  string
	err  error
	out  []byte
}{}

func wasiPackage(t *testing.T, node string) string {
	t.Helper()
	wasiBuild.once.Do(func() {
		dir, err := os.MkdirTemp("", "slicc-wasi-it-")
		if err != nil {
			wasiBuild.err = err
			return
		}
		from := filepath.Join(dir, "release")
		if err := os.MkdirAll(from, 0o755); err != nil {
			wasiBuild.err = err
			return
		}
		build := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version=1.2.3-wasi", "-o", filepath.Join(from, "slicc-wasip1-wasm.wasm"), "../../cmd/slicc")
		build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
		if wasiBuild.out, wasiBuild.err = build.CombinedOutput(); wasiBuild.err != nil {
			return
		}
		stage := exec.Command(node, "../../scripts/npm-stage.mjs", "--version", "1.2.3-wasi", "--from", from, "--out", filepath.Join(dir, "npm"), "--wasi-only")
		wasiBuild.out, wasiBuild.err = stage.CombinedOutput()
		wasiBuild.dir = dir
	})
	if wasiBuild.err != nil {
		t.Fatalf("building slicc.wasm: %v\n%s", wasiBuild.err, wasiBuild.out)
	}
	return filepath.Join(wasiBuild.dir, "npm", "wasi-slicc")
}

func requireKernel(t *testing.T) string {
	t.Helper()
	node := requireNode(t)
	if _, err := os.Stat("../../node_modules/@ai-ecoverse/slicc-kernel/package.json"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("@ai-ecoverse/slicc-kernel is missing; run npm ci")
		}
		t.Skip("@ai-ecoverse/slicc-kernel is not installed; run npm ci")
	}
	return node
}

type wasiUser struct {
	Name   string   `json:"name"`
	Groups []string `json:"groups,omitempty"`
}

type wasiReal struct {
	Port  int        `json:"port"`
	Users []wasiUser `json:"users"`
}

func runWasi(t *testing.T, cases []wasiCase) {
	t.Helper()
	runWasiOn(t, nil, cases)
}

func runWasiReal(t *testing.T, real wasiReal, cases []wasiCase) {
	t.Helper()
	runWasiOn(t, &real, cases)
}

func runWasiOn(t *testing.T, real *wasiReal, cases []wasiCase) {
	t.Helper()
	node := requireKernel(t)
	pkg := wasiPackage(t, node)
	facade, err := filepath.Abs("testdata/wasi/fakefacade")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"package": pkg, "facade": facade, "real": real, "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "testdata/wasi/run.mjs")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the kernel harness: %v\n%s", err, stderr.String())
	}
	var results []wasiResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("the kernel harness answered %q: %v\n%s", out, err, stderr.String())
	}
	if len(results) != len(cases) {
		t.Fatalf("%d results for %d cases", len(results), len(cases))
	}
	for i, c := range cases {
		r := results[i]
		t.Run(c.Name, func(t *testing.T) {
			if r.Error != "" {
				t.Fatalf("harness: %s", r.Error)
			}
			if strings.Contains(r.Stdout+r.Stderr, "tok.00112233445566778899") {
				t.Fatalf("the output shows the join URL:\n%s\n%s", r.Stdout, r.Stderr)
			}
			c.check(t, r)
		})
	}
}

func wantExit(t *testing.T, r wasiResult, code int, stdout, stderr string) {
	t.Helper()
	if r.Status != code || !strings.Contains(r.Stdout, stdout) || !strings.Contains(r.Stderr, stderr) {
		t.Fatalf("exit %d (want %d)\nstdout %q (want %q)\nstderr %q (want %q)", r.Status, code, r.Stdout, stdout, r.Stderr, stderr)
	}
}

func onlyConn(t *testing.T, r wasiResult) wasiConn {
	t.Helper()
	if len(r.Log) != 1 || r.Log[0].First == nil {
		t.Fatalf("facade connections: %+v (exit %d, stdout %q, stderr %q)", r.Log, r.Status, r.Stdout, r.Stderr)
	}
	return r.Log[0]
}

func ev(fields ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(fields); i += 2 {
		m[fields[i].(string)] = fields[i+1]
	}
	return m
}

func opened(payload map[string]any, more ...any) map[string]any {
	s := ev(more...)
	s["answer"] = map[string]any{"opened": payload}
	return s
}

func refused(reason string) map[string]any {
	return map[string]any{"answer": map[string]any{"reset": reason}}
}

func ptr(s string) *string { return &s }
