//go:build !windows

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/lease"
)

func TestSignedLeaseBaselineR13WarmMatching(t *testing.T) {
	e := newLeaseBLEnv(t, 1)
	origin := "https://billing.example"
	exactRoot := origin + "/"
	hostA := origin + "/a"
	nav0 := len(e.pageTargets())

	rows := []struct {
		name, typed, forDur string
		allow               []map[string]string
		ttl                 int
		wantMatch           bool
	}{
		{"R13/origin-request-typed-origin-match", origin, "15m", []map[string]string{{"kind": "origin", "url": origin}}, 900, true},
		{"R13/exact-root-request-typed-root-match", exactRoot, "15m", []map[string]string{{"kind": "exact", "url": exactRoot}}, 900, true},
		{"R13/origin-request-typed-exact-root-mismatch", exactRoot, "15m", []map[string]string{{"kind": "origin", "url": origin}}, 900, false},
		{"R13/exact-root-request-typed-origin-mismatch", origin, "15m", []map[string]string{{"kind": "exact", "url": exactRoot}}, 900, false},
		{"R13/ttl-900s-equals-15m-match", origin, "15m", []map[string]string{{"kind": "origin", "url": origin}}, 900, true},
		{"R13/ttl-901s-mismatch", origin, "900s", []map[string]string{{"kind": "origin", "url": origin}}, 901, false},
		{"R13/exact-a-vs-typed-a-slash", hostA + "/", "15m", []map[string]string{{"kind": "exact", "url": hostA}}, 900, false},
	}

	for _, row := range rows {
		id := requestOn(t, e, "leaseTest", row.allow, row.ttl)
		before := e.status()
		p := startTTY(t, e.env, "lease", "review", "--to", "jkt:"+e.jkt, "--allow", row.typed, "--for", row.forDur)
		time.Sleep(2 * time.Second)
		screen := p.text()
		matched := strings.Contains(screen, "Type APPROVE") || strings.Contains(screen, "Select the browser")
		if matched {
			p.keys(t, "\n")
		}
		leaseBLDrainTTY(t, p, 8*time.Second)
		after := e.status()
		var list lease.Snapshot
		_ = json.Unmarshal([]byte(rawCtl(t, e.ctlPath(), ctl.OpLeaseList)), &list)
		pending := false
		for _, pnd := range list.Pending {
			if pnd.Request == id {
				pending = true
			}
		}
		nav1 := len(e.pageTargets())
		leaseBLLine(t, row.name, "want_match", row.wantMatch, "saw_approve_prompt", matched,
			"active_after", len(list.Active), "request_still_pending", pending,
			"nav_delta", nav1-nav0, "status_before", before, "status_after", after,
			"prompt_echo_has_kind", strings.Contains(screen, "origin:") || strings.Contains(screen, "exact:") || strings.Contains(screen, "path-prefix:"))
		if matched != row.wantMatch {
			t.Fatalf("%s: match=%t want=%t screen=%q", row.name, matched, row.wantMatch, screen)
		}
		if !row.wantMatch && (len(list.Active) != 0 || !pending || nav1 != nav0) {
			t.Fatalf("%s: mismatch must leave pending, no active, no nav; active=%d pending=%t nav=%d->%d", row.name, len(list.Active), pending, nav0, nav1)
		}
		e.cli("deny", id)
	}

	id1 := requestOn(t, e, "leaseTest", []map[string]string{{"kind": "origin", "url": origin}}, 900)
	var second struct{ Type, Request, Code string }
	e.p.eval("leaseTest.tryRequest([{kind:'exact',url:'"+hostA+"'}],{ttlSeconds:900})", &second)
	var list lease.Snapshot
	_ = json.Unmarshal([]byte(rawCtl(t, e.ctlPath(), ctl.OpLeaseList)), &list)
	leaseBLLine(t, "R13/two-pending-same-jkt-only-matching-shown", "first_pending", id1 != "", "second_type", second.Type, "second_code", second.Code,
		"pending_total", len(list.Pending), "same_jkt_second_accepted", second.Type == "pending")
	e.cli("deny", id1)
}

func leaseBLDrainTTY(t *testing.T, p *ttyProc, d time.Duration) {
	t.Helper()
	select {
	case err := <-p.done:
		_ = err
	case <-time.After(d):
		_ = p.cmd.Process.Kill()
		err := <-p.done
		_ = err
	}
}

func (e *leaseBLEnv) ctlPath() string {
	rows, err := ctl.ListLocal(context.Background())
	if err != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].Path
}

func TestSignedLeaseBaselineR13Seams(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ovOwner := hrOverlay(t, t.TempDir(), map[string]string{
		filepath.Join(root, "cmd/slicc/r13_seam_test.go"): filepath.Join(root, "test/integration/testdata/lease/r13_seam.go"),
	})
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", ovOwner, "-run", "^TestR13OwnerSeamMatches$", "./cmd/slicc")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	n := leaseBaselineEcho(t, out)
	if err != nil {
		t.Fatalf("owner seam: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestR13OwnerSeamMatches") {
		t.Fatalf("owner seam did not pass\n%s", out)
	}
	leaseBLLine(t, "R13-seam-owner-summary", "rows", n, "fail", 0)

	ovRule := hrOverlay(t, t.TempDir(), map[string]string{
		filepath.Join(root, "internal/lease/r13_rule_seam_test.go"): filepath.Join(root, "test/integration/testdata/lease/r13_rule_seam.go"),
	})
	cmd = exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", ovRule, "-run", "^TestLeaseBaselineR13RuleSeam$", "./internal/lease")
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	n = leaseBaselineEcho(t, out)
	if err != nil {
		t.Fatalf("rule seam: %v\n%s", err, out)
	}
	leaseBLLine(t, "R13-seam-rule-summary", "rows", n, "fail", 0)
}

func TestSignedLeaseBaselineR13ShellLine(t *testing.T) {
	e := newLeaseBLEnv(t, 1)
	scope := "https://billing.example/Alpha-1_b.c~d/e"
	id := requestOn(t, e, "leaseTest", []map[string]string{{"kind": "exact", "url": scope}}, 900)
	shells := []string{"sh"}
	if _, err := exec.LookPath("bash"); err == nil {
		shells = append(shells, "bash")
	}
	for _, sh := range shells {
		for _, quoted := range []bool{true, false} {
			allow := scope
			if quoted {
				allow = "'" + scope + "'"
			}
			line := fmt.Sprintf("%s lease review --to jkt:%s --allow %s --for 15m", binary, e.jkt, allow)
			cmd := exec.Command(sh, "-c", line)
			cmd.Env = e.env
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			_ = cmd.Run()
			errText := stderr.String()
			reason := "other"
			switch {
			case strings.Contains(errText, "review requires a local terminal"):
				reason = "review_requires_local_terminal"
			case strings.Contains(errText, "no matching proven request"):
				reason = "no_matching_proven_request"
			case strings.Contains(errText, "invalid or protected"):
				reason = "invalid_or_protected_rule"
			case strings.Contains(errText, "Type APPROVE"):
				reason = "reached_approve_prompt"
			}
			leaseBLLine(t, "R13/shell-line", "shell", sh, "quoted", quoted, "exit", cmd.ProcessState.ExitCode(),
				"exit_reason", reason,
				"stderr", strings.TrimSpace(errText),
				"stderr_has_no_matching_or_prompt", strings.Contains(errText, "no matching") || strings.Contains(errText, "APPROVE") || strings.Contains(errText, "Select"),
				"argv_allow_once", true,
				"measured", false)
		}
	}
	leaseBLLine(t, "R13/shell-line-zsh", "measured", false, "reason", "zsh_unmeasured_on_this_vehicle")
	e.cli("deny", id)
}

func requestOn(t *testing.T, e *leaseBLEnv, holder string, allow []map[string]string, ttl int) string {
	t.Helper()
	var r struct{ Type, Request string }
	params, _ := json.Marshal(allow)
	e.p.eval(holder+".request("+string(params)+",{ttlSeconds:"+fmt.Sprint(ttl)+"})", &r)
	if r.Type != "pending" || r.Request == "" {
		t.Fatalf("%s request: %+v", holder, r)
	}
	return r.Request
}

func TestSignedLeaseBaselineR13Mutants(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(root, "cmd/slicc/lease.go"))
	if err != nil {
		t.Fatal(err)
	}
	mutants := []struct {
		name, old, neu, failRow string
	}{
		{"ignores-kind", "!slices.Equal(rules, p.Allow)",
			"!(len(rules)==len(p.Allow) && func() bool { for i := range rules { if rules[i].URL != p.Allow[i].URL { return false } } ; return true }())",
			"R13/equal-text-exact-vs-path-prefix"},
		{"ignores-ttl", "return p.JKT == strings.TrimPrefix(f.to, \"jkt:\") && p.TTL == int(f.ttl/time.Second) && p.Parent == \"\"",
			"return p.JKT == strings.TrimPrefix(f.to, \"jkt:\") && p.Parent == \"\"",
			"R13/ttl-901s-mismatch"},
		{"ignores-jkt", "return p.JKT == strings.TrimPrefix(f.to, \"jkt:\") && p.TTL == int(f.ttl/time.Second) && p.Parent == \"\"",
			"return p.TTL == int(f.ttl/time.Second) && p.Parent == \"\"",
			"R13/jkt-mismatch"},
		{"canonical-url-text-only", "!slices.Equal(rules, p.Allow)",
			"!(len(rules)==len(p.Allow) && func() bool { for i := range rules { if rules[i].URL != p.Allow[i].URL { return false } } ; return true }())",
			"R13/equal-text-exact-vs-path-prefix"},
	}
	for _, m := range mutants {
		scratch := t.TempDir()
		mutated := strings.Replace(string(src), m.old, m.neu, 1)
		if mutated == string(src) {
			t.Fatalf("mutant %s: anchor not found", m.name)
		}
		path := filepath.Join(scratch, "lease.go")
		if err := os.WriteFile(path, []byte(mutated), 0o600); err != nil {
			t.Fatal(err)
		}
		ov := hrOverlay(t, scratch, map[string]string{
			filepath.Join(root, "cmd/slicc/lease.go"):         path,
			filepath.Join(root, "cmd/slicc/r13_seam_test.go"): filepath.Join(root, "test/integration/testdata/lease/r13_seam.go"),
		})
		ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
		cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-overlay", ov, "-run", "^TestR13OwnerSeamMatches$", "./cmd/slicc")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		cancel()
		text := string(out)
		if err == nil || strings.Contains(text, "[build failed]") || strings.Contains(text, "[setup failed]") {
			t.Fatalf("mutant %s: want red seam, got green or build fail\n%s", m.name, text)
		}
		if !strings.Contains(text, m.failRow) {
			t.Fatalf("mutant %s: failing row %s not named in output\n%s", m.name, m.failRow, text)
		}
		leaseBLLine(t, "R13-mutant", "name", m.name, "turned_red", true, "named_row", m.failRow)
	}
}
