package integration

import (
	"strings"
	"testing"
)

func TestWasiJoinURLFromStdin(t *testing.T) {
	runWasi(t, []wasiCase{
		{Name: "abort", Argv: []string{"slicc", "-", "abort"}, Stdin: ptr(wasiJoin + "\n"),
			Scenario: []map[string]any{opened(ev("aborted", true), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: aborted the run\n")
				checkOpen(t, onlyConn(t, r), ev("kind", "agent", "op", "abort"))
			}},
		{Name: "the rest of stdin is the prompt", Argv: []string{"slicc", "-", "prompt", "-"}, Stdin: ptr(wasiJoin + "\nhello there"),
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", promptRun("run"), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "Hi you.\n", "")
				checkOpen(t, onlyConn(t, r), ev("kind", "agent", "op", "prompt", "text", "hello there", "whenBusy", "followUp"))
			}},
		{Name: "empty stdin", Argv: []string{"slicc", "-", "abort"}, Stdin: ptr(""),
			check: func(t *testing.T, r wasiResult) { wantExit(t, r, 2, "", "slicc: no join URL on stdin\n") }},
		{Name: "a bare token on stdin", Argv: []string{"slicc", "-", "abort"}, Stdin: ptr("traytoken.5e5e5e5e5e5e5e5e\n"),
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 2, "", "slicc: not a tray join URL")
				if strings.Contains(r.Stdout+r.Stderr, "5e5e5e5e") || len(r.Log) != 0 {
					t.Fatalf("stdout %q, stderr %q, facade connections %d", r.Stdout, r.Stderr, len(r.Log))
				}
			}},
		{Name: "a file", Argv: []string{"slicc", "@/tmp/join", "abort"}, Files: map[string]string{"/tmp/join": "\n" + wasiJoin + "\n"},
			Scenario: []map[string]any{opened(ev("aborted", true), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: aborted the run\n")
				checkOpen(t, onlyConn(t, r), ev("kind", "agent", "op", "abort"))
			}},
		{Name: "a file without a join URL", Argv: []string{"slicc", "@/tmp/nojoin", "abort"}, Files: map[string]string{"/tmp/nojoin": "traytoken.5e5e5e5e5e5e5e5e\n"},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 2, "", "slicc: not a tray join URL")
				if strings.Contains(r.Stdout+r.Stderr, "5e5e5e5e") {
					t.Fatalf("stdout %q, stderr %q", r.Stdout, r.Stderr)
				}
			}},
	})
}
