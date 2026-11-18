package integration

import (
	"regexp"
	"strings"
	"testing"
)

func wasiApproval(id, kind string, detail map[string]any, extra ...any) map[string]any {
	return ev(append([]any{"id", id, "kind", kind, "requester", ev("kind", "agent", "name", "cone"), "agentName", "cone", "detail", detail, "tier", "user", "always", false, "stoppable", true, "createdAt", 1767268800000, "expiresAt", 1767268920000}, extra...)...)
}

func TestWasiApprovals(t *testing.T) {
	sudo := wasiApproval("apr-1", "sudo", ev("command", "apt-get install -y ripgrep"), "verdict", ev("level", 1, "reason", "routine"))
	tool := wasiApproval("apr-2", "guest-tool", ev("tool", "bash", "summary", "ls"), "judging", true)
	runWasi(t, []wasiCase{
		{Name: "once", Argv: []string{"slicc", wasiJoin, "approvals"},
			Scenario: []map[string]any{opened(ev("approvals", []any{sudo}), "end", true)},
			check: func(t *testing.T, r wasiResult) {
				if r.Status != 0 || !regexp.MustCompile(`^apr-1  sudo  cone  level 1  expired  apt-get install -y ripgrep\n$`).MatchString(r.Stdout) {
					t.Fatalf("exit %d, stdout %q, stderr %q", r.Status, r.Stdout, r.Stderr)
				}
				checkOpen(t, onlyConn(t, r), ev("kind", "agent", "op", "approvals"))
			}},
		{Name: "watch json", Argv: []string{"slicc", wasiJoin, "approvals", "--watch", "--json"}, InterruptAfter: 2,
			Scenario: []map[string]any{
				opened(ev("approvals", []any{sudo}), "send", []any{ev("t", "approvals", "approvals", []any{sudo, tool})}, "reset", "ECONNRESET"),
				opened(ev("approvals", []any{tool}), "hold", true),
			},
			check: func(t *testing.T, r wasiResult) {
				got := regexp.MustCompile(`"(receivedAt|retryAt)":"[^"]+"`).ReplaceAllString(r.Stdout, `"$1":"<t>"`)
				got = regexp.MustCompile(`"wait":[0-9]+`).ReplaceAllString(got, `"wait":<ms>`)
				lines := strings.Split(strings.TrimSpace(got), "\n")
				if r.Status != 130 || len(lines) != 4 || !strings.HasPrefix(lines[0], `{"schema":1,"event":"approvals","approvals":[{`) || strings.Count(lines[1], `"id":"apr-`) != 2 || lines[2] != `{"schema":1,"event":"reconnecting","attempt":1,"retryAt":"<t>","wait":<ms>}` || !strings.Contains(lines[3], `"id":"apr-2"`) || strings.Contains(got, `"t":`) {
					t.Fatalf("exit %d, stdout:\n%s", r.Status, got)
				}
				if len(r.Log) != 2 {
					t.Fatalf("%d facade connections, want 2", len(r.Log))
				}
				checkOpen(t, r.Log[1], ev("kind", "agent", "op", "approvals", "watch", true))
			}},
		{Name: "refused", Argv: []string{"slicc", wasiJoin, "approvals"},
			Scenario: []map[string]any{refused("EACCES: that seven doesn't allow remote control from this seven")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 3, "", "slicc approvals: that seven doesn't allow remote control from this seven\n")
			}},
		{Name: "older seven", Argv: []string{"slicc", wasiJoin, "approvals"},
			Scenario: []map[string]any{refused("EINVAL")},
			check: func(t *testing.T, r wasiResult) {
				wantExit(t, r, 1, "", "slicc approvals: this seven doesn't report its approvals; update it\n")
			}},
		{Name: "decide", Argv: []string{"slicc", wasiJoin, "decide", "apr-1", "once"},
			check: func(t *testing.T, r wasiResult) {
				if r.Status != 2 || r.Stderr != "slicc: decide isn't available inside seven\n" || len(r.Log) != 0 {
					t.Fatalf("exit %d, stderr %q, %d facade connections", r.Status, r.Stderr, len(r.Log))
				}
			}},
		{Name: "help", Argv: []string{"slicc", "--help"},
			check: func(t *testing.T, r wasiResult) {
				if r.Status != 0 || !strings.Contains(r.Stdout, "slicc <join-url> approvals") || strings.Contains(r.Stdout, "decide") {
					t.Fatalf("exit %d, stdout %q", r.Status, r.Stdout)
				}
			}},
	})
}
