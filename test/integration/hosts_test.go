package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hostsFile = "# test names, no public DNS\n" +
	"127.0.0.1 ref.link.test Other.Link.Test.\n" +
	"\n" +
	"::1\tref.link.test   # IPv6 too\n"

func writeHosts(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAttachHostsFile(t *testing.T) {
	env := setupLink(t, false, "--hosts", writeHosts(t, hostsFile))
	up := newUpstream(t)
	env.wantAddresses(t, "ref.link.test", 0, "127.0.0.1", "::1")
	env.wantAddresses(t, "REF.link.test.", 4, "127.0.0.1")
	env.wantAddresses(t, "ref.link.test", 6, "::1")
	env.wantAddresses(t, "other.link.test", 0, "127.0.0.1")
	env.wantAddresses(t, "other.link.test", 6)
	if r := env.resolve(t, "localhost", 4, true); !strings.Contains(strings.Join(r.Addresses, " "), "127.0.0.1") {
		t.Fatalf("a name outside the file did not fall through: %+v", r)
	}
	_, port := hostPort(t, env.echo)
	for _, shared := range []bool{false, true} {
		var r echoResult
		env.p.eval("linkTest.echo("+jsArgs("ref.link.test", port, 100000, shared)+")", &r)
		if !r.OK || r.Opened.RemoteAddr != env.echo {
			t.Fatalf("tcp by name (shared=%v): %+v", shared, r)
		}
	}
	r := env.get(t, "http://ref.link.test:"+portOf(t, up.srv.URL)+"/target")
	if r.Status != 200 || r.body(t) != "followed" {
		t.Fatalf("http by name: %+v", r)
	}
	echo := rawEcho(t)
	_, echoPort := hostPort(t, echo)
	h := parseEchoed(t, env.get(t, "http://Other.link.test:"+jsArgs(echoPort)+"/").Body64)
	if got := h.headers["host"]; len(got) != 1 || got[0] != "Other.link.test:"+jsArgs(echoPort) {
		t.Fatalf("Host %v", got)
	}
}

func TestAttachHostsFileUnderPolicy(t *testing.T) {
	env := setupLink(t, false, "--hosts", writeHosts(t, hostsFile), "--deny", "127.0.0.0/8", "--deny", "::1/128")
	up := newUpstream(t)
	env.wantAddresses(t, "ref.link.test", 0)
	_, port := hostPort(t, env.echo)
	var r echoResult
	env.p.eval("linkTest.echo("+jsArgs("ref.link.test", port, 10)+")", &r)
	if r.Reset != "EACCES" {
		t.Fatalf("tcp by name: %+v", r)
	}
	res := env.http(t, map[string]any{"url": "http://ref.link.test:" + portOf(t, up.srv.URL) + "/target", "method": "GET", "headers": [][2]string{}}, false)
	if res.Reset != "EACCES" || up.targetHits.Load() != 0 {
		t.Fatalf("http by name: %+v", res)
	}
}

func TestHostsFileErrors(t *testing.T) {
	for _, c := range []struct{ content, want string }{
		{"127.0.0.1 ok.link.test\nnot-an-ip bad.link.test\n", "hosts file line 2"},
		{"# only\n10.0.0.1\n", "hosts file line 2"},
	} {
		for _, cmd := range [][]string{{"attach", "--signal", "stdio"}, {"https://www.sliccy.ai/join/abc.def", "follow"}} {
			_, errOut, code := slicc(t, append(cmd, "--hosts", writeHosts(t, c.content))...)
			if code != 2 || !strings.Contains(errOut, c.want) {
				t.Fatalf("%v: code %d, stderr %q", cmd, code, errOut)
			}
		}
	}
	_, errOut, code := slicc(t, "attach", "--signal", "stdio", "--hosts", filepath.Join(t.TempDir(), "missing"))
	if code != 2 || !strings.Contains(errOut, "hosts file") {
		t.Fatalf("missing file: code %d, stderr %q", code, errOut)
	}
	out, _, _ := slicc(t, "attach", "--help")
	if strings.Contains(out, "--hosts") {
		t.Fatal("--hosts is in attach --help")
	}
}
