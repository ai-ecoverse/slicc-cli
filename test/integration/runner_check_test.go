package integration

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerQuotedAsOneWordIsRefusedBeforeLinking(t *testing.T) {
	env := configEnv(t)
	h := newFakeHub(t)
	tr := h.addTray("cli")
	join := h.joinURL(tr)
	want := "slicc: --runner \"bash -c\": no program \"bash -c\"; did you mean --runner bash -c?\n"
	for _, args := range [][]string{
		{join, "follow", "--runner", "bash -c"},
		{join, "follow", "--exit", "--runner", "bash -c", "--", "--name", "box"},
		{"attach", "--signal", "stdio", "--runner", "bash -c"},
		{"--plain", "--runner", "bash -c"},
	} {
		out, errOut, code := sliccEnv(t, env, args...)
		if code != 2 || errOut != want || out != "" {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	_, errOut, code := sliccEnv(t, env, join, "follow", "--runner", "docker exec", "-i", "box", "sh", "-c")
	if code != 2 || !strings.Contains(errOut, "did you mean --runner docker exec -i box sh -c?") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
	if n := len(h.requestLog()); n != 0 {
		t.Fatalf("%d requests to the hub", n)
	}
}

func TestRunnerNotFoundIsRefusedBeforeLinking(t *testing.T) {
	env := configEnv(t)
	h := newFakeHub(t)
	tr := h.addTray("cli")
	join := h.joinURL(tr)
	missing := filepath.Join(t.TempDir(), "missing-runner")
	for _, prog := range []string{"slicc-no-such-runner", missing, "./slicc-no-such-runner"} {
		for _, args := range [][]string{
			{join, "follow", "--runner", prog, "-c"},
			{"attach", "--signal", "stdio", "--runner", prog, "-c"},
			{"--plain", "--runner", prog, "-c"},
		} {
			out, errOut, code := sliccEnv(t, env, args...)
			if code != 2 || errOut != "slicc: --runner "+prog+": program not found\n" || out != "" {
				t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
			}
		}
	}
	if n := len(h.requestLog()); n != 0 {
		t.Fatalf("%d requests to the hub", n)
	}
}

func TestValidRunnerPassesTheStartupCheck(t *testing.T) {
	env := configEnv(t)
	h := newFakeHub(t)
	tr := h.addTray("cli")
	h.with(func() { tr.WaitAttaches = 1 })
	out, errOut, code := sliccEnv(t, env, append([]string{h.joinURL(tr), "follow"}, runnerArgs("--signal-only")...)...)
	if code != 0 || !strings.Contains(out, "offer: ") || strings.Contains(errOut, "--runner") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
}
