package integration

import (
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/agentctl"
)

func renderAll(style agentctl.Style, events ...agentctl.Event) (string, string) {
	var out, errOut strings.Builder
	r := agentctl.NewRenderer(agentctl.RenderOptions{Out: &out, Err: &errOut, OutStyle: style, ErrStyle: style, Thinking: true, Users: true, Runs: true})
	for _, e := range events {
		r.Render(e)
	}
	r.Finish()
	return out.String(), errOut.String()
}

func renderEvents() []agentctl.Event {
	no := false
	return []agentctl.Event{
		{T: "user", ID: "u1", Text: "list\nfiles"},
		{T: "thinking", ID: "k1", Delta: "let me look"},
		{T: "text", ID: "a1", Delta: "Sure\x1b[2J."},
		{T: "tool", ID: "t1", Call: "c1", Name: "bash", Summary: "ls\n-la " + strings.Repeat("x", 300)},
		{T: "tool-result", ID: "r1", Call: "c1", OK: &no, Summary: "permission denied"},
		{T: "notice", ID: "n1", Text: "retrying"},
		{T: "error", ID: "e1", Message: "overloaded", Detail: "529"},
		{T: "text", ID: "a2", Delta: "Done."},
		{T: "run", State: "end", Reason: "stop"},
		{T: "something-new", ID: "x1"},
	}
}

func TestRendererPlain(t *testing.T) {
	out, errOut := renderAll(agentctl.Style{}, renderEvents()...)
	if out != "> list files\nSure.\n\nDone.\n" {
		t.Fatalf("stdout %q", out)
	}
	want := "thinking: let me look\ntool bash: ls -la " + strings.Repeat("x", 193) + "…\ntool bash failed: permission denied\nnotice: retrying\nerror: overloaded (529)\n"
	if errOut != want {
		t.Fatalf("stderr %q\nwant   %q", errOut, want)
	}
}

func TestRendererTTY(t *testing.T) {
	out, errOut := renderAll(agentctl.Style{TTY: true, Color: true}, renderEvents()...)
	if !strings.Contains(out, "\x1b[1m> list files\x1b[0m\n") || strings.Contains(out, "\x1b[2J") {
		t.Fatalf("stdout %q", out)
	}
	for _, want := range []string{"\x1b[2;3mlet me look\x1b[0m", "\x1b[1;36m⚙ bash\x1b[0m \x1b[2mls -la", "\x1b[31m  ✗ permission denied\x1b[0m", "\x1b[33m• retrying\x1b[0m", "\x1b[31m✗ overloaded\x1b[0m \x1b[2m529\x1b[0m"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr lacks %q:\n%q", want, errOut)
		}
	}
	out, errOut = renderAll(agentctl.Style{TTY: true}, renderEvents()...)
	if strings.Contains(out+errOut, "\x1b") || !strings.Contains(errOut, "⚙ bash ls -la") || !strings.Contains(errOut, "  ✗ permission denied") {
		t.Fatalf("NO_COLOR on a terminal: %q %q", out, errOut)
	}
}

func TestRendererBreaksTheLineOnASharedTerminal(t *testing.T) {
	var screen strings.Builder
	tty := agentctl.Style{TTY: true}
	r := agentctl.NewRenderer(agentctl.RenderOptions{Out: &screen, Err: &screen, OutStyle: tty, ErrStyle: tty})
	r.Render(agentctl.Event{T: "text", ID: "a1", Delta: "Looking"})
	r.Render(agentctl.Event{T: "tool", ID: "t1", Call: "c1", Name: "bash", Summary: "ls"})
	r.Render(agentctl.Event{T: "tool-result", ID: "r1", Call: "c1", Summary: "ok"})
	r.Finish()
	if got := screen.String(); got != "Looking\n⚙ bash ls\n  ↳ ok\n" {
		t.Fatalf("screen %q", got)
	}
}

func TestTrackerSkipsWhatWasShown(t *testing.T) {
	tr := agentctl.NewTracker()
	keep := func(e agentctl.Event) string {
		e, ok := tr.Filter(e)
		if !ok {
			return "-"
		}
		return e.Delta + e.T
	}
	if keep(agentctl.Event{T: "text", ID: "a", Delta: "héllo "}) != "héllo text" || keep(agentctl.Event{T: "tool", ID: "t"}) != "tool" {
		t.Fatal("first pass")
	}
	if tr.Since() != "t" {
		t.Fatalf("since %q", tr.Since())
	}
	tr.Reconnected()
	got := []string{
		keep(agentctl.Event{T: "text", ID: "a", Delta: "héllo world"}),
		keep(agentctl.Event{T: "tool", ID: "t"}),
		keep(agentctl.Event{T: "text", ID: "a", Delta: "!"}),
		keep(agentctl.Event{T: "run", State: "end"}),
	}
	if strings.Join(got, "|") != "worldtext|-|!text|run" {
		t.Fatalf("after reconnect %q", got)
	}
}
