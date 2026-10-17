package integration

import (
	"os"
	"strings"
	"testing"
)

type ciRun struct {
	event, ref, sha string
}

func (r ciRun) value(name string) (string, bool) {
	switch name {
	case "github.event_name":
		return r.event, true
	case "github.ref":
		return r.ref, true
	case "github.sha":
		return r.sha, true
	}
	return "", false
}

func evalWorkflowExpr(t *testing.T, expr string, run ciRun) string {
	t.Helper()
	or := strings.Split(expr, "||")
	for i, alt := range or {
		val := ""
		for _, part := range strings.Split(alt, "&&") {
			val = evalWorkflowCompare(t, strings.TrimSpace(part), run)
			if val == "" || val == "false" {
				break
			}
		}
		if (val != "" && val != "false") || i == len(or)-1 {
			return val
		}
	}
	return ""
}

func evalWorkflowCompare(t *testing.T, expr string, run ciRun) string {
	t.Helper()
	for _, op := range []string{"!=", "=="} {
		if l, r, ok := strings.Cut(expr, op); ok {
			eq := evalWorkflowOperand(t, strings.TrimSpace(l), run) == evalWorkflowOperand(t, strings.TrimSpace(r), run)
			if eq == (op == "==") {
				return "true"
			}
			return "false"
		}
	}
	return evalWorkflowOperand(t, expr, run)
}

func evalWorkflowOperand(t *testing.T, expr string, run ciRun) string {
	t.Helper()
	if len(expr) >= 2 && expr[0] == '\'' && expr[len(expr)-1] == '\'' {
		return expr[1 : len(expr)-1]
	}
	if expr == "true" || expr == "false" {
		return expr
	}
	if v, ok := run.value(expr); ok {
		return v
	}
	t.Fatalf("ci.yml's concurrency uses %q, which this test can't evaluate", expr)
	return ""
}

func evalWorkflowTemplate(t *testing.T, tmpl string, run ciRun) string {
	t.Helper()
	out := ""
	for {
		start := strings.Index(tmpl, "${{")
		if start < 0 {
			return out + tmpl
		}
		end := strings.Index(tmpl[start:], "}}")
		if end < 0 {
			t.Fatalf("unterminated expression in %q", tmpl)
		}
		out += tmpl[:start] + evalWorkflowExpr(t, strings.TrimSpace(tmpl[start+3:start+end]), run)
		tmpl = tmpl[start+end+2:]
	}
}

func TestCIConcurrencyKeepsOnePushRunOnMain(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	ci := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if strings.Count(ci, "concurrency:") != 1 {
		t.Fatal("ci.yml must have exactly one concurrency block, the workflow's")
	}
	at := strings.Index(ci, "\nconcurrency:\n")
	if at < 0 {
		t.Fatal("ci.yml has no workflow-level concurrency block")
	}
	group, cancel := "", ""
	for _, line := range strings.Split(ci[at+len("\nconcurrency:\n"):], "\n") {
		if !strings.HasPrefix(line, "  ") {
			break
		}
		k, v, _ := strings.Cut(strings.TrimSpace(line), ":")
		switch k {
		case "group":
			group = strings.TrimSpace(v)
		case "cancel-in-progress":
			cancel = strings.TrimSpace(v)
		}
	}
	if group == "" || cancel == "" {
		t.Fatalf("ci.yml's concurrency needs group and cancel-in-progress (group %q, cancel %q)", group, cancel)
	}
	eval := func(r ciRun) (string, bool) {
		return evalWorkflowTemplate(t, group, r), evalWorkflowTemplate(t, cancel, r) == "true"
	}

	pushA, cancelA := eval(ciRun{"push", "refs/heads/main", "1111111111111111111111111111111111111111"})
	pushB, cancelB := eval(ciRun{"push", "refs/heads/main", "2222222222222222222222222222222222222222"})
	if pushA != pushB {
		t.Errorf("two pushes to main get groups %q and %q; they must share one, so a newer push replaces the pending run", pushA, pushB)
	}
	if cancelA || cancelB {
		t.Error("a push to main sets cancel-in-progress; the running push run must finish so a release can happen under a steady stream of merges")
	}

	prA, prCancel := eval(ciRun{"pull_request", "refs/pull/7/merge", "3333333333333333333333333333333333333333"})
	prB, _ := eval(ciRun{"pull_request", "refs/pull/7/merge", "4444444444444444444444444444444444444444"})
	prOther, _ := eval(ciRun{"pull_request", "refs/pull/8/merge", "4444444444444444444444444444444444444444"})
	if prA != prB || !prCancel {
		t.Errorf("a pull request's newer push must cancel its older run (groups %q, %q; cancel %v)", prA, prB, prCancel)
	}
	if prA == prOther || prA == pushA {
		t.Errorf("a pull request's group %q must be its own (other PR %q, push %q)", prA, prOther, pushA)
	}

	mgA, mgCancel := eval(ciRun{"merge_group", "refs/heads/gh-readonly-queue/main/pr-7-1111111111111111111111111111111111111111", "5555555555555555555555555555555555555555"})
	mgB, mgCancelB := eval(ciRun{"merge_group", "refs/heads/gh-readonly-queue/main/pr-8-5555555555555555555555555555555555555555", "6666666666666666666666666666666666666666"})
	if mgCancel || mgCancelB {
		t.Error("a merge-group run sets cancel-in-progress; merge-queue runs must never be cancelled")
	}
	if mgA == mgB || mgA == pushA || mgA == prA {
		t.Errorf("each merge group needs its own concurrency group, or a queued group's run is replaced (groups %q, %q; push %q; PR %q)", mgA, mgB, pushA, prA)
	}
}
