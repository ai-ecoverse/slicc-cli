package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func promptRun(delivered string) []any {
	return []any{
		ev("t", "user", "id", "1", "text", "hello there", "delivered", delivered),
		ev("t", "thinking", "id", "a9.0", "delta", "pondering"),
		ev("t", "text", "id", "a9.1", "delta", "Hi"),
		ev("t", "text", "id", "a9.1", "delta", " you."),
		ev("t", "tool", "id", "a9.2", "call", "c1", "name", "bash", "summary", "ls -la"),
		ev("t", "tool-result", "id", "2", "call", "c1", "ok", true, "summary", "3 files"),
		ev("t", "notice", "id", "retry-1", "text", "retrying in 2s"),
		ev("t", "end", "reason", "stop"),
	}
}

func checkOpen(t *testing.T, c wasiConn, want map[string]any) {
	t.Helper()
	if c.First.Type != 1 || c.First.JSON.V != 1 || c.First.JSON.Join != wasiJoin {
		t.Fatalf("first record: type %d, v %d, join %q", c.First.Type, c.First.JSON.V, c.First.JSON.Join)
	}
	got, _ := json.Marshal(c.First.JSON.Open)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Fatalf("open %s, want %s", got, exp)
	}
}

func TestWasiPrompt(t *testing.T) {
	long := strings.Repeat("ünïcødé ", 13000)
	runWasi(t, []wasiCase{
		{Name: "short", Argv: []string{"slicc", wasiJoin, "prompt", "hello", "there"},
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", promptRun("run"), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "Hi you.\n", "tool bash: ls -la\n")
				for _, want := range []string{"tool bash ok: 3 files\n", "notice: retrying in 2s\n"} {
					if !strings.Contains(r.Stderr, want) {
						t.Fatalf("stderr lacks %q: %q", want, r.Stderr)
					}
				}
				if r.Stdout != "Hi you.\n" || strings.Contains(r.Stderr, "pondering") || strings.Contains(r.Stdout+r.Stderr, "\x1b[") {
					t.Fatalf("stdout %q stderr %q", r.Stdout, r.Stderr)
				}
				c := onlyConn(t, r)
				checkOpen(t, c, ev("kind", "agent", "op", "prompt", "text", "hello there", "whenBusy", "followUp"))
				if len(c.Records) != 1 || c.Records[0] != 3 || c.Bytes != 0 {
					t.Fatalf("records after the opening: %v (%d bytes)", c.Records, c.Bytes)
				}
			}},
		{Name: "steer and thinking", Argv: []string{"slicc", wasiJoin, "prompt", "--steer", "--thinking", "hello there"},
			Scenario: []map[string]any{opened(ev("delivered", "steer"), "send", promptRun("steer"), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "Hi you.\n", "thinking: pondering\n")
				checkOpen(t, onlyConn(t, r), ev("kind", "agent", "op", "prompt", "text", "hello there", "whenBusy", "steer", "thinking", true))
			}},
		{Name: "long with length", Argv: []string{"slicc", wasiJoin, "prompt", "-"}, Stdin: ptr(long + "\n"),
			Scenario: []map[string]any{opened(ev(), "awaitEnd", true, "send", []any{ev("t", "delivered", "delivered", "follow-up"), ev("t", "text", "id", "a1.0", "delta", "got it"), ev("t", "end", "reason", "stop")}, "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "got it\n", "")
				c := onlyConn(t, r)
				checkOpen(t, c, ev("kind", "agent", "op", "prompt", "length", float64(len(long)), "whenBusy", "followUp"))
				if c.Text != long || c.Bytes != len(long) || !c.Ended {
					t.Fatalf("the facade got %d bytes (want %d), ended %v", c.Bytes, len(long), c.Ended)
				}
				if n := len(c.Records); n < 3 || c.Records[n-1] != 3 {
					t.Fatalf("records %v: want several data records, then end", c.Records)
				}
			}},
		{Name: "reset while the upload stalls", Argv: []string{"slicc", wasiJoin, "prompt", "-"}, Stdin: ptr(strings.Repeat("z", 4<<20)),
			Scenario: []map[string]any{opened(ev(), "stall", "EACCES: unlock the vault to continue")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 3, "", "slicc prompt: unlock the vault to continue\n")
			}},
		{Name: "malformed record during the upload", Argv: []string{"slicc", wasiJoin, "prompt", "-"}, Stdin: ptr(strings.Repeat("z", 8<<20)),
			Scenario: []map[string]any{opened(ev(), "garbageLater", "\x09\x00\x00\x00\x00", "hold", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc prompt: ")
				if c := onlyConn(t, r); len(c.Controls) != 1 || c.Controls[0]["reset"] != "EPROTO" {
					t.Fatalf("slicc answered %v", c.Controls)
				}
			}},
		{Name: "json", Argv: []string{"slicc", wasiJoin, "prompt", "--json", "hello there"},
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", promptRun("run"), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, `{"delta":"Hi","id":"a9.1","t":"text"}`, "")
				lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
				if len(lines) != 8 || lines[7] != `{"reason":"stop","t":"end"}` || r.Stderr != "" {
					t.Fatalf("json lines %q, stderr %q", lines, r.Stderr)
				}
			}},
		{Name: "run failed", Argv: []string{"slicc", wasiJoin, "prompt", "go"},
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", []any{ev("t", "error", "id", "e1", "message", "model overloaded", "detail", "529"), ev("t", "end", "reason", "error", "message", "model overloaded")}, "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc prompt: the run failed: model overloaded\n")
			}},
		{Name: "aborted", Argv: []string{"slicc", wasiJoin, "prompt", "go"},
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", []any{ev("t", "end", "reason", "aborted")}, "end", true)},
			check:    func(t *testing.T, r wasiResult) { wantExit(t, r, 1, "", "slicc prompt: the run was aborted\n") }},
		{Name: "busy", Argv: []string{"slicc", wasiJoin, "prompt", "--reject", "go"},
			Scenario: []map[string]any{refused("EBUSY")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "seven is busy, so the prompt wasn't sent")
				checkOpen(t, onlyConn(t, r), ev("kind", "agent", "op", "prompt", "text", "go", "whenBusy", "reject"))
			}},
		{Name: "dropped", Argv: []string{"slicc", wasiJoin, "prompt", "go"},
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", []any{ev("t", "text", "id", "a1.0", "delta", "partial")}, "drop", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "partial", "slicc prompt: lost the link to seven; the run goes on there\n")
			}},
		{Name: "reset mid-stream", Argv: []string{"slicc", wasiJoin, "prompt", "go"},
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", []any{ev("t", "text", "id", "a1.0", "delta", "partial")}, "reset", "ECONNRESET: the link to that seven went down")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "partial", "slicc prompt: lost the link to seven; the run goes on there\n")
			}},
		{Name: "a later control that isn't a reset", Argv: []string{"slicc", wasiJoin, "prompt", "go"},
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", []any{ev("t", "text", "id", "a1.0", "delta", "partial")}, "control", ev("opened", ev()), "hold", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "partial", "slicc prompt: lost the link to seven; the run goes on there\n")
				if c := onlyConn(t, r); len(c.Controls) != 1 || c.Controls[0]["reset"] != "EPROTO" {
					t.Fatalf("slicc answered %v", c.Controls)
				}
			}},
		{Name: "terminal", Argv: []string{"slicc", wasiJoin, "prompt", "hello there"}, TTY: true,
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", promptRun("run"), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "Hi you.", "")
				if !strings.Contains(r.Stdout, "\x1b[") || !strings.Contains(r.Stdout, "⚙ bash") {
					t.Fatalf("no styling on a terminal: %q", r.Stdout)
				}
			}},
	})
}

func TestWasiWatch(t *testing.T) {
	runWasi(t, []wasiCase{
		{Name: "without since", Argv: []string{"slicc", wasiJoin, "watch"}, InterruptAfter: 1,
			Scenario: []map[string]any{opened(ev("head", "5"), "send", []any{ev("t", "user", "id", "6", "text", "hi", "delivered", "run"), ev("t", "text", "id", "a7.0", "delta", "Hello")}, "hold", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 130, "> hi\nHello", "slicc: watching seven\n")
				checkOpen(t, onlyConn(t, r), ev("kind", "agent", "op", "watch"))
			}},
		{Name: "resumes with since and a gap", Argv: []string{"slicc", wasiJoin, "watch", "--thinking"}, InterruptAfter: 2,
			Scenario: []map[string]any{
				opened(ev("head", "5"), "send", []any{ev("t", "user", "id", "6", "text", "hi", "delivered", "run"), ev("t", "text", "id", "a7.0", "delta", "Hel")}, "drop", true),
				opened(ev("head", "7", "gap", true), "send", []any{ev("t", "text", "id", "a7.0", "delta", "Hello"), ev("t", "text", "id", "a7.0", "delta", "!"), ev("t", "user", "id", "6", "text", "hi", "delivered", "run")}, "hold", true),
			},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 130, "", "notice: some events were missed\n")
				if r.Stdout != "> hi\nHello!" {
					t.Fatalf("stdout %q: want the replay without repeats", r.Stdout)
				}
				if len(r.Log) != 2 {
					t.Fatalf("%d facade connections, want 2", len(r.Log))
				}
				checkOpen(t, r.Log[0], ev("kind", "agent", "op", "watch", "thinking", true))
				checkOpen(t, r.Log[1], ev("kind", "agent", "op", "watch", "since", "a7.0", "thinking", true))
			}},
		{Name: "json", Argv: []string{"slicc", wasiJoin, "watch", "--json"}, InterruptAfter: 2,
			Scenario: []map[string]any{
				opened(ev("head", "5"), "send", []any{ev("t", "text", "id", "a7.0", "delta", "Hel")}, "reset", "ECONNRESET"),
				opened(ev("head", "7"), "send", []any{ev("t", "text", "id", "a7.0", "delta", "Hello")}, "hold", true),
			},
			check: func(t *testing.T, r wasiResult) {
				want := "{\"delta\":\"Hel\",\"id\":\"a7.0\",\"t\":\"text\"}\n{\"delta\":\"lo\",\"id\":\"a7.0\",\"t\":\"text\"}\n"
				if r.Status != 130 || r.Stdout != want {
					t.Fatalf("exit %d, stdout %q, want %q", r.Status, r.Stdout, want)
				}
			}},
		{Name: "tray unreachable, then up", Argv: []string{"slicc", wasiJoin, "watch"}, InterruptAfter: 2,
			Scenario: []map[string]any{
				refused("ENOTCONN: the tray can't be reached"),
				opened(ev("head", "1"), "send", []any{ev("t", "user", "id", "2", "text", "back", "delivered", "run")}, "hold", true),
			},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 130, "> back\n", "slicc: lost the link to seven; reconnecting\n")
			}},
		{Name: "refused", Argv: []string{"slicc", wasiJoin, "watch"},
			Scenario: []map[string]any{refused("EACCES: that seven doesn't allow remote control from this seven")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 3, "", "slicc watch: that seven doesn't allow remote control from this seven\n")
			}},
	})
}

func TestWasiLocalhostIsLoopback(t *testing.T) {
	runWasi(t, []wasiCase{
		{Name: "localhost mapped elsewhere", Argv: []string{"slicc", wasiJoin, "abort"}, LinkHost: "localhost",
			Hosts:    "203.0.113.5 localhost\n",
			Scenario: []map[string]any{opened(ev("aborted", true), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: aborted the run\n")
				checkOpen(t, onlyConn(t, r), ev("kind", "agent", "op", "abort"))
			}},
	})
}

func TestWasiHostModuleConnectsLocalhostAsLoopback(t *testing.T) {
	node := requireNode(t)
	pkg := wasiPackage(t, node)
	want := map[string]string{"localhost": "127.0.0.1", "127.0.0.1": "127.0.0.1", "127.3.4.5": "127.3.4.5", "LOCALHOST": "", "localhost.": "", "example.test": "", "10.0.0.1": ""}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	input, err := json.Marshal(map[string]any{"package": pkg, "hosts": names})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "testdata/wasi/hostmodule.mjs")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the host module harness: %v\n%s", err, stderr.String())
	}
	var results []struct {
		Name  string  `json:"name"`
		FD    int     `json:"fd"`
		Asked *string `json:"asked"`
	}
	if err := json.Unmarshal(out, &results); err != nil || len(results) != len(names) {
		t.Fatalf("the host module harness answered %q: %v", out, err)
	}
	for _, r := range results {
		asked := "nothing"
		if r.Asked != nil {
			asked = *r.Asked
		}
		switch w := want[r.Name]; {
		case w == "" && (r.Asked != nil || r.FD != -2):
			t.Errorf("connect(%q) = %d and asked ctx.net for %s, want EACCES (-2) without asking", r.Name, r.FD, asked)
		case w != "" && (asked != w || r.FD != 7):
			t.Errorf("connect(%q) = %d and asked ctx.net for %s, want %s", r.Name, r.FD, asked, w)
		}
	}
}

func TestWasiAbortAndRefusals(t *testing.T) {
	runWasi(t, []wasiCase{
		{Name: "abort", Argv: []string{"slicc", wasiJoin, "abort"},
			Scenario: []map[string]any{opened(ev("aborted", true), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: aborted the run\n")
				c := onlyConn(t, r)
				checkOpen(t, c, ev("kind", "agent", "op", "abort"))
				if !c.Ended {
					t.Fatal("abort didn't send its end")
				}
			}},
		{Name: "nothing running", Argv: []string{"slicc", wasiJoin, "abort"},
			Scenario: []map[string]any{opened(ev("aborted", false), "end", true)},
			check:    func(t *testing.T, r wasiResult) { wantExit(t, r, 0, "", "slicc: nothing was running\n") }},
		{Name: "vault locked", Argv: []string{"slicc", wasiJoin, "prompt", "hi"},
			Scenario: []map[string]any{refused("EACCES: unlock the vault to continue")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 3, "", "slicc prompt: unlock the vault to continue\n")
			}},
		{Name: "eacces without a message", Argv: []string{"slicc", wasiJoin, "abort"},
			Scenario: []map[string]any{refused("EACCES")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 3, "", "slicc abort: this seven doesn't allow remote control from this device")
			}},
		{Name: "tray unreachable", Argv: []string{"slicc", wasiJoin, "prompt", "hi"},
			Scenario: []map[string]any{refused("ENOTCONN: the tray can't be reached")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc prompt: the tray can't be reached (ENOTCONN)\n")
			}},
		{Name: "malformed answer", Argv: []string{"slicc", wasiJoin, "abort"},
			Scenario: []map[string]any{{"garbage": "\x09\x00\x00\x00\x00"}},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc abort: seven's link facade sent a malformed record: record type 9 (EPROTO)\n")
			}},
		{Name: "no facade url", Argv: []string{"slicc", wasiJoin, "prompt", "hi"}, Env: map[string]string{"SLICC_LINK_URL": ""},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc prompt: SLICC_LINK_URL isn't set, so this seven offers no link facade\n")
			}},
		{Name: "bad facade url", Argv: []string{"slicc", wasiJoin, "prompt", "hi"}, Env: map[string]string{"SLICC_LINK_URL": "ws://127.0.0.1:9/"},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc prompt: SLICC_LINK_URL isn't tcp://127.0.0.1:<port>\n")
			}},
		{Name: "nothing listening", Argv: []string{"slicc", wasiJoin, "abort"}, Env: map[string]string{"SLICC_LINK_URL": "tcp://127.0.0.1:1"},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc abort: can't reach seven's link facade: ")
			}},
		{Name: "no host module", Argv: []string{"slicc-bare", wasiJoin, "abort"},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc abort: this runtime has no slicc_host module")
			}},
	})
}

func TestWasiFacadeThatNeverAnswers(t *testing.T) {
	runWasi(t, []wasiCase{
		{Name: "prompt", Argv: []string{"slicc", wasiJoin, "prompt", "hi"}, Timeout: 100000,
			Scenario: []map[string]any{{"silent": 90000}},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc prompt: seven's link facade didn't answer within 1m0s\n")
			}},
	})
}

func TestWasiCommandsOutsideSeven(t *testing.T) {
	unavailable := func(command string) func(*testing.T, wasiResult) {
		return func(t *testing.T, r wasiResult) {
			if r.Status != 2 || r.Stderr != "slicc: "+command+" isn't available inside seven\n" || r.Stdout != "" || len(r.Log) != 0 {
				t.Fatalf("exit %d, stdout %q, stderr %q, %d facade connections", r.Status, r.Stdout, r.Stderr, len(r.Log))
			}
		}
	}
	runWasi(t, []wasiCase{
		{Name: "version", Argv: []string{"slicc", "--version"}, check: func(t *testing.T, r wasiResult) {
			if r.Status != 0 || r.Stdout != "slicc 1.2.3-wasi\n" {
				t.Fatalf("exit %d, %q", r.Status, r.Stdout)
			}
		}},
		{Name: "help", Argv: []string{"slicc", "--help"}, check: func(t *testing.T, r wasiResult) {
			wantExit(t, r, 0, "slicc <join-url> prompt <text>", "")
			for _, want := range []string{"slicc <join-url> watch", "slicc <join-url> abort", "slicc --version", "SLICC_LINK_URL"} {
				if !strings.Contains(r.Stdout, want) {
					t.Fatalf("help lacks %q:\n%s", want, r.Stdout)
				}
			}
			for _, banned := range []string{"attach", "follow", "--cdp", "launch", "browsers", "update", "install", "pair", "--mount"} {
				if strings.Contains(r.Stdout, banned) {
					t.Fatalf("help lists %q, which doesn't work inside seven:\n%s", banned, r.Stdout)
				}
			}
		}},
		{Name: "prompt help", Argv: []string{"slicc", wasiJoin, "prompt", "--help"}, check: func(t *testing.T, r wasiResult) {
			wantExit(t, r, 0, "usage: slicc <join-url> prompt [flags] <text>|@file|-", "")
			for _, banned := range []string{"--cdp", "--signal", "--origin", "--abort-on-interrupt", "tray hub"} {
				if strings.Contains(r.Stdout, banned) {
					t.Fatalf("prompt --help lists %q:\n%s", banned, r.Stdout)
				}
			}
		}},
		{Name: "watch help", Argv: []string{"slicc", wasiJoin, "watch", "-h"}, check: func(t *testing.T, r wasiResult) {
			wantExit(t, r, 0, "usage: slicc <join-url> watch", "")
			if strings.Contains(r.Stdout, "--cdp") || strings.Contains(r.Stdout, "--signal") {
				t.Fatalf("watch --help lists local flags:\n%s", r.Stdout)
			}
		}},
		{Name: "no arguments", Argv: []string{"slicc"}, check: func(t *testing.T, r wasiResult) { wantExit(t, r, 2, "", "usage:") }},
		{Name: "attach", Argv: []string{"slicc", "attach", "--cdp", "http://127.0.0.1:9333"}, check: unavailable("attach")},
		{Name: "browsers", Argv: []string{"slicc", "browsers"}, check: unavailable("browsers")},
		{Name: "default flags", Argv: []string{"slicc", "--profile", "x"}, check: unavailable("--profile")},
		{Name: "prompt without a join url", Argv: []string{"slicc", "prompt", "--cdp", "http://127.0.0.1:9333", "hi"}, check: unavailable("prompt")},
		{Name: "follow", Argv: []string{"slicc", wasiJoin, "follow", "--exit"}, check: unavailable("follow")},
		{Name: "no verb", Argv: []string{"slicc", wasiJoin}, check: func(t *testing.T, r wasiResult) {
			wantExit(t, r, 2, "", "slicc: a join URL must be followed by a command: prompt, watch or abort\n")
		}},
		{Name: "not a join url", Argv: []string{"slicc", "https://tray.example.test/nope", "prompt", "hi"}, check: func(t *testing.T, r wasiResult) {
			wantExit(t, r, 2, "", "slicc: not a tray join URL")
		}},
		{Name: "local flags", Argv: []string{"slicc", wasiJoin, "prompt", "--abort-on-interrupt", "hi"}, check: func(t *testing.T, r wasiResult) {
			wantExit(t, r, 2, "", "flag provided but not defined: -abort-on-interrupt")
		}},
	})
}

func TestWasiPackage(t *testing.T) {
	node := requireNode(t)
	pkg := wasiPackage(t, node)
	raw, err := os.ReadFile(filepath.Join(pkg, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name    string            `json:"name"`
		Version string            `json:"version"`
		Files   []string          `json:"files"`
		Engines map[string]string `json:"engines"`
		Slicc   json.RawMessage   `json:"slicc"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "@ai-ecoverse/wasi-slicc" || m.Version != "1.2.3-wasi" || strings.Join(m.Files, ",") != "README.md,LICENSE,bin,host" || m.Engines["slicc-kernel"] == "" {
		t.Fatalf("package.json: %s", raw)
	}
	var block bytes.Buffer
	if err := json.Compact(&block, m.Slicc); err != nil || block.String() != `{"abi":"wasi","commands":{"slicc":{"wasm":"bin/slicc.wasm","imports":"host/slicc-host.mjs"}}}` {
		t.Fatalf("slicc block: %s", m.Slicc)
	}
	wasm, err := os.Stat(filepath.Join(pkg, "bin", "slicc.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("slicc.wasm: %d bytes (%.1f MiB)", wasm.Size(), float64(wasm.Size())/(1<<20))
	readme, err := os.ReadFile(filepath.Join(pkg, "README.md"))
	if err != nil || !strings.Contains(string(readme), "SLICC_LINK_URL") || strings.Contains(string(readme), "## Inside seven") {
		t.Fatalf("README: %v\n%s", err, readme)
	}
	for _, f := range []string{"LICENSE", "host/slicc-host.mjs"} {
		if _, err := os.Stat(filepath.Join(pkg, f)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWasiBuildLeavesOutTheHostParts(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "../../cmd/slicc")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	deps := strings.Fields(string(out))
	for _, dep := range deps {
		for _, banned := range []string{"github.com/pion/", "internal/cdp", "internal/link", "internal/node", "internal/signal", "internal/sshd", "internal/caps", "internal/policy", "internal/browser", "internal/telemetry", "internal/update", "go-optel", "slicc-browsers", "golang.org/x/crypto/ssh"} {
			if strings.Contains(dep, banned) {
				t.Errorf("the wasip1 build links %s", dep)
			}
		}
	}
	if !strings.Contains(string(out), "internal/facade") || !strings.Contains(string(out), "internal/agentctl") {
		t.Fatalf("the wasip1 build lacks the facade or the agent client:\n%s", out)
	}
}
