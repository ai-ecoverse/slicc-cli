package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestAJoinURLFileMustBePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes and symlinks")
	}
	env := configEnv(t)
	dir := t.TempDir()
	open := filepath.Join(dir, "open")
	if err := os.WriteFile(open, []byte("https://www.sliccy.ai/join/trayx.5e5e5e5e5e5e5e5e\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o640); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "private")
	if err := os.WriteFile(private, []byte("https://www.sliccy.ai/join/trayx.5e5e5e5e5e5e5e5e\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ path, want string }{
		{open, "slicc: " + open + " holds a secret, so others mustn't read it: chmod 600 " + open + "\n"},
		{link, "slicc: " + link + " isn't a regular file\n"},
		{dir, "slicc: " + dir + " isn't a regular file\n"},
	} {
		out, errOut, code := sliccStdin(t, env, "", "@"+c.path, "follow")
		if code != 2 || out != "" || errOut != c.want || strings.Contains(errOut, "5e5e5e5e") {
			t.Fatalf("@%s: code %d, stdout %q, stderr %q (want %q)", c.path, code, out, errOut, c.want)
		}
	}
}

func TestFollowFromStdinKeepsTheJoinURLOutOfItsCommandLine(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/<pid>/cmdline")
	}
	env := configEnv(t)
	h := newFakeHub(t)
	tr := h.addTray("follow")
	h.with(func() { tr.NoLeader = true })
	cmd := exec.Command(binary, "-", "follow")
	cmd.Env = env
	cmd.Stdin = strings.NewReader(h.joinURL(tr) + "\n")
	var stderr syncBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stderr.String(), "connecting to the tray hub") {
		if time.Now().After(deadline) {
			t.Fatalf("follow never dialed:\n%s", stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(cmd.Process.Pid) + "/cmdline")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00"); len(got) != 3 || got[1] != "-" || got[2] != "follow" {
		t.Fatalf("cmdline %q", got)
	}
	assertRedacted(t, string(cmdline)+stderr.String(), h, tr)
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
