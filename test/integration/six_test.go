package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSixFlagsPointBackToSix(t *testing.T) {
	quoted := "'hi there'"
	if runtime.GOOS == "windows" {
		quoted = `"hi there"`
	}
	secret := "trayx.5e5e5e5e5e5e5e5e5e5e5e5e"
	cases := []struct {
		args []string
		flag string
		echo string
	}{
		{[]string{"-v"}, "-v", "-v"},
		{[]string{"--serve-only", "--cdp-port=9222"}, "--serve-only", "--serve-only --cdp-port=9222"},
		{[]string{"--cdp-port", "9222"}, "--cdp-port", "--cdp-port 9222"},
		{[]string{"--hosted"}, "--hosted", "--hosted"},
		{[]string{"--kill"}, "--kill", "--kill"},
		{[]string{"--install-cli", "--install-dir", "/opt/bin"}, "--install-cli", "--install-cli --install-dir /opt/bin"},
		{[]string{"--install-dir=/opt/bin"}, "--install-dir", "--install-dir=/opt/bin"},
		{[]string{"--computer-demo"}, "--computer-demo", "--computer-demo"},
		{[]string{"--log-level=debug", "--log-dir=/tmp/logs"}, "--log-level", "--log-level=debug --log-dir=/tmp/logs"},
		{[]string{"--log-dir=/tmp/logs"}, "--log-dir", "--log-dir=/tmp/logs"},
		{[]string{"--prompt", "hi there"}, "--prompt", "--prompt " + quoted},
		{[]string{"--prompt=hi"}, "--prompt", "--prompt=hi"},
		{[]string{"--env-file", "secrets.env"}, "--env-file", "--env-file secrets.env"},
		{[]string{"--lead", "https://www.sliccy.ai"}, "--lead", "--lead https://www.sliccy.ai"},
		{[]string{"--lead=https://www.sliccy.ai"}, "--lead", "--lead=https://www.sliccy.ai"},
		{[]string{"--join", "https://www.sliccy.ai/join/" + secret}, "--join", "--join <join-url>"},
		{[]string{"--join=https://www.sliccy.ai/join/" + secret}, "--join", "--join=<join-url>"},
		{[]string{"--electron", "slack"}, "--electron", "--electron slack"},
		{[]string{"--electron-app=/Applications/Slack.app"}, "--electron-app", "--electron-app=/Applications/Slack.app"},
		{[]string{"--cloud", "start", "--name", "box"}, "--cloud", "--cloud start --name box"},
		{[]string{"--browser", "chrome", "--yes", "--electron", "slack"}, "--electron", "--browser chrome --yes --electron slack"},
		{[]string{"--name", "box", "--prompt", "hi"}, "--prompt", "--name box --prompt hi"},
		{[]string{"frobnicate", "--electron", "slack"}, "--electron", "frobnicate --electron slack"},
	}
	for _, c := range cases {
		out, errOut, code := slicc(t, c.args...)
		want := "slicc: " + c.flag + " is a SLICC six option. This is the new SLICC (seven). For SLICC six, run: npx sliccy@6 " + c.echo + "\n"
		if code != 2 || out != "" || errOut != want {
			t.Fatalf("%v: code %d, stdout %q, stderr %q, want %q", c.args, code, out, errOut, want)
		}
		if strings.Contains(errOut, "5e5e5e5e") {
			t.Fatalf("%v: the join URL was echoed: %q", c.args, errOut)
		}
	}
}

func TestSevenFlagsAreNotSixFlags(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"version"}, {"-V"}, {"--bogus"}, {"--profile", "-v", "--bogus"}} {
		_, errOut, _ := slicc(t, args...)
		if strings.Contains(errOut, "is a SLICC six option") {
			t.Fatalf("%v: %q", args, errOut)
		}
	}
}

func TestSixMountFormFails(t *testing.T) {
	dir := t.TempDir()
	secret := "trayx.5e5e5e5e5e5e5e5e5e5e5e5e"
	cases := []struct {
		args []string
		prog string
		spec string
		hint string
	}{
		{[]string{"--yes", "--mount", dir + ":/workspace/proj"}, "slicc", dir + ":/workspace/proj", dir + ":proj"},
		{[]string{"--mount=" + dir + ":/mnt"}, "slicc", dir + ":/mnt", dir + ":mnt"},
		{[]string{"attach", "--cdp", "http://127.0.0.1:1", "--mount", dir + ":/workspace/proj:ro"}, "slicc attach", dir + ":/workspace/proj:ro", dir + ":proj:ro"},
		{[]string{"https://www.sliccy.ai/join/" + secret, "follow", "--mount", dir + ":/"}, "slicc follow", dir + ":/", dir},
	}
	for _, c := range cases {
		start := time.Now()
		out, errOut, code := slicc(t, c.args...)
		hint := c.hint
		if strings.ContainsAny(hint, ` \`) {
			hint = "'" + hint + "'"
			if runtime.GOOS == "windows" {
				hint = `"` + c.hint + `"`
			}
		}
		want := c.prog + ": --mount " + c.spec + " is SLICC six's form, <os-path>:<vfs-path>. This is the new SLICC (seven): use --mount <path>[:<name>][:ro], such as --mount " + hint + ", then mount the folder in seven.\n"
		if code != 2 || out != "" || errOut != want {
			t.Fatalf("%v: code %d, stdout %q, stderr %q, want %q", c.args, code, out, errOut, want)
		}
		if time.Since(start) > 10*time.Second {
			t.Fatalf("%v: took %s, so it ran", c.args, time.Since(start))
		}
	}
}

func TestSevenMountFormsAreNotSixs(t *testing.T) {
	dir := t.TempDir()
	colon := filepath.Join(t.TempDir(), "a:")
	if runtime.GOOS != "windows" {
		if err := os.MkdirAll(filepath.Join(colon, "b"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	specs := []string{dir, dir + ":proj", dir + ":proj:ro", dir + ":ro"}
	if runtime.GOOS != "windows" {
		specs = append(specs, filepath.Join(colon, "b"))
	}
	for _, spec := range specs {
		_, errOut, code := slicc(t, "attach", "--signal", "nope", "--mount", spec)
		if code != 2 || !strings.Contains(errOut, "--signal must be cdp or stdio") || strings.Contains(errOut, "six") {
			t.Fatalf("%s: code %d, stderr %q", spec, code, errOut)
		}
	}
}
