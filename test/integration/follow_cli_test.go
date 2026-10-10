package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

func configEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	vars := map[string]string{
		"HOME":            home,
		"USERPROFILE":     home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"AppData":         filepath.Join(home, "AppData", "Roaming"),
		"APPDATA":         filepath.Join(home, "AppData", "Roaming"),
	}
	for k, v := range vars {
		t.Setenv(k, v)
	}
	env := os.Environ()
	return env
}

func sliccEnv(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = env
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

func TestFollowSignalOnlyPrintsTheOfferAndICEServers(t *testing.T) {
	env := configEnv(t)
	h := newFakeHub(t)
	tr := h.addTray("cli")
	h.with(func() { tr.WaitAttaches = 1 })
	out, errOut, code := sliccEnv(t, env, h.joinURL(tr), "follow", "--signal-only")
	if code != 0 {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{
		"attached: bootstrap attempt 1\n",
		"offer: " + tray.SummarizeSDP(testOfferSDP) + "\n",
		"1 media (application), 1 candidates, fingerprint sha-256 AB:CD:EF:01",
		"ice servers: 2\n",
		"  stun:stun.example.test:3478\n",
		"  turn:turn.example.test:3478?transport=udp turns:turn.example.test:5349?transport=tcp (with credentials)\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout lacks %q:\n%s", want, out)
		}
	}
	all := out + errOut
	assertRedacted(t, all, h, tr)
	for _, secret := range []string{"turn-user-secret", "turn-credential-secret", "ufragsecret", "icepasswordsecretvalue", "192.0.2.10", tr.TrayID} {
		if strings.Contains(all, secret) {
			t.Fatalf("output leaks %q", secret)
		}
	}
	h.with(func() {
		if len(tr.answers) != 0 {
			t.Error("signal-only answered the offer")
		}
	})
	path, err := tray.DefaultStatePath()
	if err != nil {
		t.Fatal(err)
	}
	if tray.OpenState(path).Current(h.joinURL(tr)) != h.joinURL(tr) {
		t.Fatal("state not written")
	}
	if _, ok := tray.OpenState(path).Last(); !ok {
		t.Fatal("state has no last URL")
	}
}

func TestFollowSignalOnlyFollowsAndRemembersSupersede(t *testing.T) {
	env := configEnv(t)
	h := newFakeHub(t)
	trays := supersedeChain(h, 2)
	out, errOut, code := sliccEnv(t, env, h.joinURL(trays[0]), "follow", "--signal-only")
	if code != 0 || !strings.Contains(out, "join url: superseded, the replacement is remembered\n") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	assertRedacted(t, out+errOut, h, trays...)
	path, err := tray.DefaultStatePath()
	if err != nil {
		t.Fatal(err)
	}
	if tray.OpenState(path).Current(h.joinURL(trays[0])) != h.joinURL(trays[2]) {
		t.Fatal("state does not hold the replacement")
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("state mode %v %v", fi, err)
		}
		di, err := os.Stat(filepath.Dir(path))
		if err != nil || di.Mode().Perm() != 0o700 {
			t.Fatalf("state dir mode %v %v", di, err)
		}
	}
	if _, _, code := sliccEnv(t, env, h.joinURL(trays[0]), "follow", "--signal-only"); code != 0 {
		t.Fatalf("second run code %d", code)
	}
	if len(attachRequests(h, trays[0].Token)) != 1 || len(attachRequests(h, trays[2].Token)) != 2 {
		t.Fatal("second run walked the chain again")
	}
}

func TestFollowSignalOnlyErrorsAreRedacted(t *testing.T) {
	env := configEnv(t)
	h := newFakeHub(t)
	exhausted := supersedeChain(h, tray.MaxRedirects+1)
	expired := h.addTray("expired")
	hanging := h.addTray("hang")
	h.with(func() { expired.Expired = true })
	cases := []struct {
		joinURL string
		want    string
		hang    bool
		args    []string
	}{
		{joinURL: h.srv.URL + "/join/unknown.00112233445566778899", want: "INVALID_JOIN_CAPABILITY"},
		{joinURL: h.joinURL(expired), want: "TRAY_EXPIRED"},
		{joinURL: h.joinURL(exhausted[0]), want: "TRAY_SUPERSEDED_CHAIN_EXHAUSTED"},
		{joinURL: httptestClosedURL(t) + "/join/closed.00112233445566778899", want: "NETWORK"},
		{joinURL: h.joinURL(hanging), want: "timed out waiting for an offer", hang: true, args: []string{"--timeout", "300ms"}},
	}
	for _, c := range cases {
		h.with(func() { h.hang = c.hang })
		out, errOut, code := sliccEnv(t, env, append([]string{c.joinURL, "follow", "--signal-only"}, c.args...)...)
		if code != 1 || !strings.Contains(errOut, c.want) {
			t.Fatalf("%s: code %d, stdout %q, stderr %q", c.want, code, out, errOut)
		}
		assertNoSecret(t, out+errOut, c.joinURL)
		for _, tr := range exhausted {
			assertNoSecret(t, out+errOut, h.joinURL(tr))
		}
	}
}

func TestJoinURLArgumentsAreNeverEchoed(t *testing.T) {
	env := configEnv(t)
	secret := "trayx.5e5e5e5e5e5e5e5e5e5e5e5e"
	cases := [][]string{
		{"https://www.sliccy.ai/join/" + secret},
		{"https://www.sliccy.ai/join/" + secret, "frobnicate"},
		{"http://www.sliccy.ai/join/" + secret, "follow", "--signal-only"},
		{"https://www.sliccy.ai/nojoin/" + secret, "follow"},
		{"https://www.sliccy.ai/join/" + secret, "follow"},
		{"https://www.sliccy.ai/join/" + secret, "follow", "--bogus"},
		{"https://www.sliccy.ai/join/" + secret, "follow", "--signal-only", "extra"},
	}
	for _, args := range cases {
		out, errOut, code := sliccEnv(t, env, args...)
		if code != 2 || out != "" || errOut == "" {
			t.Fatalf("%v: code %d, stdout %q, stderr %q", args[1:], code, out, errOut)
		}
		if strings.Contains(errOut, "5e5e5e5e") || strings.Contains(errOut, "trayx") {
			t.Fatalf("%v: stderr leaks the token: %q", args[1:], errOut)
		}
	}
}

func TestHelpDoesNotAdvertiseTheDiagnostic(t *testing.T) {
	out, _, _ := slicc(t, "--help")
	if strings.Contains(out, "signal-only") || strings.Contains(out, "follow") {
		t.Fatalf("help mentions follow: %q", out)
	}
}
