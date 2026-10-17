package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

func sliccStdin(t *testing.T, env []string, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

func TestFollowTakesTheJoinURLFromStdinOrAFile(t *testing.T) {
	env := configEnv(t)
	h := newFakeHub(t)
	tr := h.addTray("cli")
	h.with(func() { tr.WaitAttaches = 1 })
	file := filepath.Join(t.TempDir(), "join")
	if err := os.WriteFile(file, []byte("\n  "+h.joinURL(tr)+"  \r\nnot this line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		stdin string
		arg   string
	}{
		{"stdin", "\n" + h.joinURL(tr) + "\n", "-"},
		{"stdin without a newline", h.joinURL(tr), "-"},
		{"file", "", "@" + file},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := sliccStdin(t, env, c.stdin, c.arg, "follow", "--signal-only")
			if code != 0 || !strings.Contains(out, "offer: "+tray.SummarizeSDP(testOfferSDP)+"\n") {
				t.Fatalf("code %d\nstdout %q\nstderr %q", code, out, errOut)
			}
			assertRedacted(t, out+errOut, h, tr)
		})
	}
}

func TestJoinURLFromStdinOrAFileIsNeverEchoed(t *testing.T) {
	env := configEnv(t)
	dir := t.TempDir()
	secret := "https://www.sliccy.ai/nojoin/trayx.5e5e5e5e5e5e5e5e"
	bad := filepath.Join(dir, "bad")
	empty := filepath.Join(dir, "empty")
	big := filepath.Join(dir, "big")
	for path, data := range map[string]string{bad: secret + "\n", empty: "\n \n", big: strings.Repeat("5e5e5e5e", 9000)} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, stdin, arg, want string
	}{
		{"empty stdin", "", "-", "slicc: no join URL on stdin\n"},
		{"blank stdin", "\n\n  \n", "-", "slicc: no join URL on stdin\n"},
		{"not a join URL on stdin", secret + "\n", "-", "slicc: not a tray join URL"},
		{"not a join URL in a file", "", "@" + bad, "slicc: not a tray join URL"},
		{"an empty file", "", "@" + empty, "slicc: no join URL in " + empty + "\n"},
		{"a big file", "", "@" + big, "slicc: " + big + " is too big to hold a join URL\n"},
		{"a missing file", "", "@" + filepath.Join(dir, "missing"), "slicc: can't read the join URL: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := sliccStdin(t, env, c.stdin, c.arg, "follow")
			if code != 2 || out != "" || !strings.Contains(errOut, c.want) {
				t.Fatalf("code %d, stdout %q, stderr %q (want %q)", code, out, errOut, c.want)
			}
			if strings.Contains(errOut, "5e5e5e5e") || strings.Contains(errOut, "trayx") {
				t.Fatalf("stderr leaks the input: %q", errOut)
			}
		})
	}
}

func TestAgentVerbsTakeTheJoinURLFromStdin(t *testing.T) {
	env := configEnv(t)
	out, errOut, code := sliccStdin(t, env, "https://www.sliccy.ai/join/trayx.5e5e5e5e5e5e5e5e\n", "-", "watch", "--help")
	if code != 0 || !strings.Contains(out, "usage: slicc <join-url> watch") || strings.Contains(out+errOut, "5e5e5e5e") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	out, _, _ = sliccStdin(t, env, "https://www.sliccy.ai/join/trayx.5e5e5e5e5e5e5e5e\n", "-", "follow", "--help")
	if !strings.Contains(out, "slicc - follow [flags]") {
		t.Fatalf("follow --help: %q", out)
	}
}
