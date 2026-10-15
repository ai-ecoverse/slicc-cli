package integration

import (
	"slices"
	"strings"
	"testing"
)

const wasiLinkPort = 7400

func checkStamp(t *testing.T, c wasiConn, user string, root bool) {
	t.Helper()
	s := c.Stamp
	if s == nil || c.Facade != "link" {
		t.Fatalf("the page's hook got no stamp for the link facade: %+v", c)
	}
	if s.Users != "enforce" || s.User != user || (s.UID == 0) != root || s.PID <= 0 {
		t.Fatalf("stamp %+v, want users enforce, user %s, root %v", s, user, root)
	}
}

func TestWasiRealFacade(t *testing.T) {
	url := "tcp://127.0.0.1:7400"
	real := wasiReal{Port: wasiLinkPort, Users: []wasiUser{{Name: "cone", Groups: []string{"sudo"}}, {Name: "scoop-site", Groups: []string{"cone"}}}}
	runWasiReal(t, real, []wasiCase{
		{Name: "the kernel sets SLICC_LINK_URL", Argv: []string{"slicc-link-env"},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, url+"\n", "")
				if len(r.Log) != 0 {
					t.Fatalf("facade connections: %+v", r.Log)
				}
			}},
		{Name: "root prompts", Argv: []string{"slicc", wasiJoin, "prompt", "hello", "there"},
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", promptRun("run"), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "Hi you.\n", "tool bash: ls -la\n")
				c := onlyConn(t, r)
				checkStamp(t, c, "root", true)
				checkOpen(t, c, ev("kind", "agent", "op", "prompt", "text", "hello there", "whenBusy", "followUp"))
				if !c.Ended || !c.Closed {
					t.Fatalf("ended %v, closed %v", c.Ended, c.Closed)
				}
			}},
		{Name: "a cone in sudo uploads a long prompt", Argv: []string{"slicc", wasiJoin, "prompt", "-"}, User: "cone", Stdin: ptr(strings.Repeat("cone ", 40000)),
			Scenario: []map[string]any{opened(ev(), "awaitEnd", true, "send", []any{ev("t", "delivered", "delivered", "follow-up"), ev("t", "text", "id", "a1.0", "delta", "got it"), ev("t", "end", "reason", "stop")}, "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "got it\n", "")
				c := onlyConn(t, r)
				checkStamp(t, c, "cone", false)
				if !slices.Contains(c.Stamp.Groups, 27) {
					t.Fatalf("the cone's stamp has groups %v, without sudo", c.Stamp.Groups)
				}
				want := strings.Repeat("cone ", 40000)
				if c.Text != want || !c.Ended {
					t.Fatalf("the page got %d bytes (want %d), ended %v", c.Bytes, len(want), c.Ended)
				}
			}},
		{Name: "root aborts", Argv: []string{"slicc", wasiJoin, "abort"},
			Scenario: []map[string]any{opened(ev("aborted", true), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 0, "", "slicc: aborted the run\n")
				checkStamp(t, onlyConn(t, r), "root", true)
			}},
		{Name: "a scoop is refused", Argv: []string{"slicc", wasiJoin, "prompt", "hi"}, User: "scoop-site",
			Scenario: []map[string]any{opened(ev("delivered", "run"), "send", promptRun("run"), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 3, "", "slicc prompt: only root and members of sudo may follow a tray\n")
				c := onlyConn(t, r)
				checkStamp(t, c, "scoop-site", false)
				if slices.Contains(c.Stamp.Groups, 27) || len(c.Records) != 0 {
					t.Fatalf("stamp %+v, records %v", c.Stamp, c.Records)
				}
			}},
	})
}
