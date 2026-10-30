package integration

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	capjerk "github.com/ai-ecoverse/slicc-cli/internal/caps/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

type jerkRes struct {
	Opened map[string]any   `json:"opened"`
	Events []map[string]any `json:"events"`
	Reset  string           `json:"reset"`
	Detail string           `json:"detail"`
	Error  string           `json:"error"`
}

func (r jerkRes) last() map[string]any {
	if len(r.Events) == 0 {
		return nil
	}
	return r.Events[len(r.Events)-1]
}

func (r jerkRes) of(kind string) []map[string]any {
	var out []map[string]any
	for _, e := range r.Events {
		if e["event"] == kind {
			out = append(out, e)
		}
	}
	return out
}

func (p *page) jerk(t *testing.T, open map[string]any) jerkRes {
	t.Helper()
	var r jerkRes
	p.eval("linkTest.jerk("+jsArgs(open)+")", &r)
	if r.Error != "" {
		t.Fatalf("jerk %v: %s", open, r.Error)
	}
	return r
}

func fakeJerkDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, exeName("jerk")), "./testdata/fakejerk")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func jerkEnv(dir, log string, extra ...string) []string {
	return append([]string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"), "FAKEJERK_LOG=" + log}, extra...)
}

func jerkCalls(t *testing.T, log string) []map[string]any {
	t.Helper()
	f, err := os.Open(log)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var calls []map[string]any
	for sc := bufio.NewScanner(f); sc.Scan(); {
		var c map[string]any
		_ = json.Unmarshal(sc.Bytes(), &c)
		calls = append(calls, c)
	}
	return calls
}

func jerkRuns(t *testing.T, log string) int {
	t.Helper()
	if _, err := os.Stat(log); err != nil {
		return 0
	}
	return len(jerkCalls(t, log))
}

func lastArgv(t *testing.T, log string) ([]string, map[string]any) {
	t.Helper()
	calls := jerkCalls(t, log)
	if len(calls) == 0 {
		t.Fatal("jerk was never run")
	}
	c := calls[len(calls)-1]
	var args []string
	for _, a := range c["args"].([]any) {
		args = append(args, a.(string))
	}
	return args, c
}

func lastArgs(t *testing.T, log string) (string, map[string]any) {
	t.Helper()
	args, c := lastArgv(t, log)
	return strings.Join(args, " "), c
}

type jerkHello struct {
	Caps []string `json:"caps"`
	Jerk *struct {
		Version string `json:"version"`
	} `json:"jerk"`
}

func TestJerkOverTheLink(t *testing.T) {
	log := filepath.Join(t.TempDir(), "jerk.log")
	e := setupLinkEnv(t, false, jerkEnv(fakeJerkDir(t), log))
	t.Run("hello", func(t *testing.T) {
		var h jerkHello
		e.p.eval("linkTest.ready()", &h)
		if !slices.Contains(h.Caps, "jerk") || !slices.Contains(h.Caps, "jerk-seal") || h.Jerk == nil || h.Jerk.Version != "0.6.0" {
			t.Fatalf("HELLO %+v", h)
		}
		e.a.waitStderr(t, "slicc: jerk: offering slicc-jerk 0.6.0 (jerk, jerk-seal)", 5*time.Second)
	})
	t.Run("list, status and accounts keep only status fields", func(t *testing.T) { testJerkStatus(t, e.p, log) })
	t.Run("create", func(t *testing.T) { testJerkCreate(t, e, log) })
	t.Run("seal and unseal", func(t *testing.T) { testJerkSeal(t, e.p, log) })
	t.Run("refusals", func(t *testing.T) { testJerkRefusals(t, e.p) })
	t.Run("mcp:url ids", func(t *testing.T) { testJerkMCPURL(t, e, log) })
	t.Run("mcp:url refusals", func(t *testing.T) { testJerkMCPURLRefusals(t, e.p, log) })
	t.Run("nothing secret in the log", func(t *testing.T) {
		if strings.Contains(e.a.stderr.String(), "gho_secret") {
			t.Fatalf("slicc's log shows a token:\n%s", e.a.stderr.String())
		}
	})
}

func testJerkStatus(t *testing.T, p *page, log string) {
	r := p.jerk(t, map[string]any{"op": "list"})
	res := r.last()
	items, _ := res["items"].([]any)
	if r.Reset != "" || len(r.Events) != 1 || res["event"] != "result" || res["exit"] != 0.0 || len(items) != 1 {
		t.Fatalf("list %+v", r)
	}
	if row := items[0].(map[string]any); row["provider"] != "github" || row["name"] != "GitHub (gh)" || row["cli"] != true || row["token"] != nil {
		t.Fatalf("list row %v", row)
	}
	r = p.jerk(t, map[string]any{"op": "status", "provider": "github"})
	res = r.last()
	if res["user"] != "octocat" || res["provider"] != "github" || res["expires_at"] != "2030-01-01T00:00:00Z" || res["token"] != nil || res["raw"] != nil {
		t.Fatalf("status %+v", r)
	}
	if args, _ := lastArgs(t, log); args != "github get --json" {
		t.Fatalf("status ran jerk %s", args)
	}
	r = p.jerk(t, map[string]any{"op": "accounts", "provider": "github"})
	items, _ = r.last()["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "42" || items[0].(map[string]any)["token"] != nil {
		t.Fatalf("accounts %+v", r)
	}
	r = p.jerk(t, map[string]any{"op": "status", "provider": "broken"})
	if r.last()["exit"] != 3.0 || r.last()["message"] != "sign in with jerk broken create" {
		t.Fatalf("broken %+v", r)
	}
	for _, ev := range r.Events {
		b, _ := json.Marshal(ev)
		if strings.Contains(string(b), "gho_secret") {
			t.Fatalf("a token crossed the link: %s", b)
		}
	}
}

func testJerkCreate(t *testing.T, e *linkEnv, log string) {
	r := e.p.jerk(t, map[string]any{"op": "create", "provider": "github", "scope": "repo read:org", "params": map[string]string{"host": "github.example.com"}})
	progress := r.of("progress")
	if r.Reset != "" || len(progress) != 2 || progress[0]["message"] != "sign-in opened in Google Chrome" || r.last()["event"] != "result" || r.last()["user"] != "octocat" {
		t.Fatalf("create %+v", r)
	}
	args, _ := lastArgs(t, log)
	if want := "github create --via-link --seven-origin " + e.origin + " --scope repo read:org -p host=github.example.com"; args != want {
		t.Fatalf("create ran jerk %q, want %q", args, want)
	}
	var pair []jerkRes
	e.p.eval("linkTest.jerkPair("+jsArgs(map[string]any{"op": "create", "provider": "slow"}, map[string]any{"op": "create", "provider": "github"})+")", &pair)
	if len(pair) != 2 || pair[0].Reset != "" || pair[1].Reset != "EBUSY" {
		t.Fatalf("two creates at once: %+v", pair)
	}
	for _, bad := range []map[string]any{
		{"op": "create", "provider": "github", "params": map[string]string{"Host": "x"}},
		{"op": "create", "provider": "github", "params": map[string]string{"host": "a\nb"}},
		{"op": "create", "provider": "github", "scope": "repo\x00"},
	} {
		if r := e.p.jerk(t, bad); r.Reset != "EPROTO" {
			t.Fatalf("%v: %+v", bad, r)
		}
	}
}

func testJerkSeal(t *testing.T, p *page, log string) {
	r := p.jerk(t, map[string]any{"op": "seal", "provider": "github", "duration": 900})
	leases := r.of("lease")
	if r.Reset != "" || len(r.Events) != 2 || len(leases) != 1 || r.Events[0]["event"] != "lease" {
		t.Fatalf("seal %+v", r)
	}
	creds, _ := leases[0]["credentials"].(map[string]any)
	if creds["GH_TOKEN"] != "gho_secret_github" || leases[0]["expires_at"] == "" || leases[0]["lease_id"] != "jerk-github-1" {
		t.Fatalf("lease %v", leases[0])
	}
	res := r.last()
	env, _ := res["env"].([]any)
	if res["exit"] != 0.0 || len(env) != 2 || res["expires_at"] != leases[0]["expires_at"] || res["credentials"] != nil {
		t.Fatalf("seal result %v", res)
	}
	if args, c := lastArgs(t, log); args != "github lease --via-link" || c["duration"] != "900" {
		t.Fatalf("seal ran jerk %q %v", args, c)
	}
	if r := p.jerk(t, map[string]any{"op": "seal", "provider": "github"}); r.Reset != "" {
		t.Fatalf("seal without duration %+v", r)
	}
	if _, c := lastArgs(t, log); c["duration"] != "3600" {
		t.Fatalf("default duration %v", c)
	}
	for _, provider := range []string{"nolease", "noexpiry"} {
		r := p.jerk(t, map[string]any{"op": "seal", "provider": provider})
		if len(r.of("lease")) != 0 || r.last()["exit"] != 1.0 {
			t.Fatalf("%s: %+v", provider, r)
		}
	}
	r = p.jerk(t, map[string]any{"op": "unseal", "provider": "github", "lease_id": "jerk-github-1"})
	if r.Reset != "" || len(r.Events) != 1 || r.last()["message"] != "dropped jerk-github-1" {
		t.Fatalf("unseal %+v", r)
	}
	if args, c := lastArgs(t, log); args != "github drop --lease --via-link" || c["lease"] != "jerk-github-1" {
		t.Fatalf("unseal ran jerk %q %v", args, c)
	}
}

func testJerkRefusals(t *testing.T, p *page) {
	for _, c := range []struct {
		open map[string]any
		want string
	}{
		{map[string]any{"op": "frobnicate"}, "EPROTO"},
		{map[string]any{"op": "status"}, "EPROTO"},
		{map[string]any{"op": "status", "provider": "Git/Hub"}, "EPROTO"},
		{map[string]any{"op": "drop", "provider": "github"}, "EACCES"},
		{map[string]any{"op": "unseal", "provider": "github"}, "EPROTO"},
		{map[string]any{"op": "seal", "provider": "github", "duration": 0}, "EPROTO"},
		{map[string]any{"op": "seal", "provider": "github", "duration": 1.5}, "EPROTO"},
		{map[string]any{"op": "seal", "provider": "github", "duration": 86401}, "EPROTO"},
		{map[string]any{"op": "status", "provider": "dies"}, "ECONNRESET"},
	} {
		if r := p.jerk(t, c.open); r.Reset != c.want {
			t.Fatalf("%v: %+v, want %s", c.open, r, c.want)
		}
	}
}

const jerkMCPID = "mcp:url:http://127.0.0.1:8931/mcp/k3y"

func testJerkMCPURL(t *testing.T, e *linkEnv, log string) {
	r := e.p.jerk(t, map[string]any{"op": "create", "provider": jerkMCPID, "scope": "read", "params": map[string]string{"host": "x"}})
	if r.Reset != "" || r.last()["exit"] != 0.0 || r.last()["provider"] != jerkMCPID {
		t.Fatalf("create %+v", r)
	}
	if args, _ := lastArgv(t, log); !slices.Equal(args, []string{jerkMCPID, "create", "--via-link", "--seven-origin", e.origin, "--scope", "read", "-p", "host=x"}) {
		t.Fatalf("create ran jerk %q", args)
	}
	r = e.p.jerk(t, map[string]any{"op": "seal", "provider": jerkMCPID, "duration": 900})
	if r.Reset != "" || len(r.of("lease")) != 1 || r.last()["provider"] != jerkMCPID {
		t.Fatalf("seal %+v", r)
	}
	if args, c := lastArgv(t, log); !slices.Equal(args, []string{jerkMCPID, "lease", "--via-link"}) || c["duration"] != "900" {
		t.Fatalf("seal ran jerk %q %v", args, c)
	}
	r = e.p.jerk(t, map[string]any{"op": "unseal", "provider": jerkMCPID, "lease_id": "lease-1"})
	if r.Reset != "" || r.last()["message"] != "dropped lease-1" || r.last()["provider"] != jerkMCPID {
		t.Fatalf("unseal %+v", r)
	}
	if args, c := lastArgv(t, log); !slices.Equal(args, []string{jerkMCPID, "drop", "--lease", "--via-link"}) || c["lease"] != "lease-1" {
		t.Fatalf("unseal ran jerk %q %v", args, c)
	}
	base := "mcp:url:http://127.0.0.1:8931/"
	longest := base + strings.Repeat("a", 2048-len(base))
	if r := e.p.jerk(t, map[string]any{"op": "create", "provider": longest}); r.Reset != "" {
		t.Fatalf("a 2048-byte id: %+v", r)
	}
}

func testJerkMCPURLRefusals(t *testing.T, p *page, log string) {
	before := jerkRuns(t, log)
	base := "mcp:url:https://example.com/"
	for _, id := range []string{
		"mcp:bb",
		"mcp:url:",
		"mcp:url:https://EXAMPLE.com/",
		"mcp:url:https://example.com:443/",
		"mcp:url:https://example.com:0/",
		"mcp:url:https://example.com",
		"mcp:url:https://example.com/?a=1",
		"mcp:url:https://example.com/?",
		"mcp:url:https://example.com/#f",
		"mcp:url:https://example.com/#",
		"mcp:url:https://user:pw@example.com/",
		"mcp:url:http://example.com/",
		"mcp:url:https://example.com/a/../b",
		"mcp:url:https://example.com/a\\b",
		"mcp:url:https://example.com/a\nb",
		"mcp:url:https://example.com/\x7f",
		"mcp:url:https://example.com/ü",
		base + strings.Repeat("a", 2049-len(base)),
	} {
		for _, op := range []string{"create", "seal", "unseal"} {
			if r := p.jerk(t, map[string]any{"op": op, "provider": id, "lease_id": "x"}); r.Reset != "EPROTO" {
				t.Fatalf("%s %q: %+v", op, id, r)
			}
		}
	}
	for _, op := range []string{"status", "accounts"} {
		r := p.jerk(t, map[string]any{"op": op, "provider": jerkMCPID})
		if r.Reset != "EPROTO" || r.Detail != op+" needs a provider matching ^[a-z0-9][a-z0-9-]{0,63}$" {
			t.Fatalf("%s %+v", op, r)
		}
	}
	if after := jerkRuns(t, log); after != before {
		t.Fatalf("refused ids ran jerk %d times", after-before)
	}
}

func TestMCPURLsMatchJerk(t *testing.T) {
	for _, c := range []struct{ raw, canonical string }{
		{"https://example.com/", "https://example.com/"},
		{"https://example.com/mcp/", "https://example.com/mcp/"},
		{"https://example.com:8443/mcp", "https://example.com:8443/mcp"},
		{"http://localhost/", "http://localhost/"},
		{"http://localhost:3000/mcp", "http://localhost:3000/mcp"},
		{"http://127.0.0.1:8931/mcp", "http://127.0.0.1:8931/mcp"},
		{"http://[::1]:3000/", "http://[::1]:3000/"},
		{"https://[2001:db8::1]/", "https://[2001:db8::1]/"},
		{"https://1.2.3.4:8443/x", "https://1.2.3.4:8443/x"},
		{"https://example.com./", "https://example.com./"},
		{"https://xn--mnchen-3ya.de/mcp", "https://xn--mnchen-3ya.de/mcp"},
		{"https://example.com/a%2Fb", "https://example.com/a%2Fb"},
		{"https://example.com/a%2fb", "https://example.com/a%2fb"},
		{"https://example.com/%zz", "https://example.com/%zz"},
		{"https://example.com/a^b|c[d]", "https://example.com/a^b|c[d]"},
		{"https://example.com//a", "https://example.com//a"},
		{"https://example.com/~a!$&'()*+,;=:@", "https://example.com/~a!$&'()*+,;=:@"},
		{"https://example.com", "https://example.com/"},
		{"https://example.com:443/", "https://example.com/"},
		{"http://localhost:80/", "http://localhost/"},
		{"https://EXAMPLE.com/", "https://example.com/"},
		{"HTTPS://example.com/", "https://example.com/"},
		{"https://example.com:08443/", "https://example.com:8443/"},
		{"https://example.com/a/../b", "https://example.com/b"},
		{"https://example.com/a/%2e%2e/b", "https://example.com/b"},
		{"https://example.com/a/./b", "https://example.com/a/b"},
		{"https://example.com\\a", "https://example.com/a"},
		{"https://example.com/a\\b", "https://example.com/a/b"},
		{"https:/example.com/", "https://example.com/"},
		{"https:example.com/", "https://example.com/"},
		{"https://example.com/a b", "https://example.com/a%20b"},
		{"https://example.com/a\"b", "https://example.com/a%22b"},
		{"https://example.com/a<b>", "https://example.com/a%3Cb%3E"},
		{"https://example.com/a`b", "https://example.com/a%60b"},
		{"https://example.com/a{b}", "https://example.com/a%7Bb%7D"},
		{"https://example.com/ü", "https://example.com/%C3%BC"},
		{"https://münchen.de/", "https://xn--mnchen-3ya.de/"},
		{"https://ex%61mple.com/", "https://example.com/"},
		{"https://2130706433/", "https://127.0.0.1/"},
		{"https://0x7f.1/", "https://127.0.0.1/"},
		{"http://127.1/", "http://127.0.0.1/"},
		{"https://[::ffff:127.0.0.1]/", "https://[::ffff:7f00:1]/"},
		{"http://[0:0:0:0:0:0:0:1]/", "http://[::1]/"},
		{"https://@example.com/", "https://example.com/"},
		{"https://example.com:/", "https://example.com/"},
		{"https://example.com/?", ""},
		{"https://example.com/?a=1", ""},
		{"https://example.com/#", ""},
		{"https://example.com/#f", ""},
		{"https://user@example.com/", ""},
		{"https://user:pw@example.com/", ""},
		{"http://example.com/", ""},
		{"http://192.168.1.1/", ""},
		{"http://localhost./", ""},
		{"ftp://example.com/", ""},
		{"wss://example.com/", ""},
		{"file:///etc/passwd", ""},
		{"https://", ""},
		{"https://[::1/", ""},
		{"https://example.com:65536/", ""},
		{"https://a|b.com/", ""},
	} {
		_, ok := jerk.ParseMCPURL(jerk.MCPURLPrefix + c.raw)
		if ok != (c.raw == c.canonical) {
			t.Errorf("%q: admitted %v, jerk prints %q", c.raw, ok, c.canonical)
		}
		if c.canonical == "" {
			continue
		}
		if _, ok := jerk.ParseMCPURL(jerk.MCPURLPrefix + c.canonical); !ok {
			t.Errorf("jerk prints %q and slicc refuses it", c.canonical)
		}
	}
}

func TestJerkMCPURLPolicy(t *testing.T) {
	t.Setenv("PATH", fakeJerkDir(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	j, _ := jerk.Find()
	if j == nil {
		t.Fatal("no fake jerk")
	}
	useLog := func(t *testing.T) string {
		log := filepath.Join(t.TempDir(), "jerk.log")
		t.Setenv("FAKEJERK_LOG", log)
		return log
	}
	refused := func(t *testing.T, p *page, log string, ids ...string) {
		t.Helper()
		before := jerkRuns(t, log)
		for _, id := range ids {
			for _, op := range []string{"create", "seal", "unseal"} {
				r := p.jerk(t, map[string]any{"op": op, "provider": id, "lease_id": "x"})
				if r.Reset != "EACCES" || r.Detail == "" || strings.Contains(r.Detail, "/mcp") {
					t.Fatalf("%s %s: %+v", op, id, r)
				}
			}
		}
		if after := jerkRuns(t, log); after != before {
			t.Fatalf("refused ids ran jerk %d times", after-before)
		}
	}
	t.Run("remote", func(t *testing.T) {
		log := useLog(t)
		deny, err := policy.ParseRules([]string{"93.184.215.15:443"})
		if err != nil {
			t.Fatal(err)
		}
		pol := policy.Remote().With(nil, deny)
		dns := newFakeDNS(t)
		dns.set("public.slicc.test", "93.184.215.14", "2606:2800:21f:cb07:6820:80da:af6b:8b2c")
		dns.set("mixed.slicc.test", "93.184.215.14", "127.0.0.1")
		dns.set("private.slicc.test", "10.0.0.1")
		dns.set("pinned.slicc.test", "127.0.0.1")
		hosts, err := policy.ParseHosts(strings.NewReader("93.184.215.14 pinned.slicc.test localhost\n10.0.0.1 public.slicc.test\n"))
		if err != nil {
			t.Fatal(err)
		}
		env := setupInProcess(t, pol, func(n *node.Node) {
			capjerk.Register(n, capjerk.Options{Jerk: j, Seal: true, Resolver: &policy.Resolver{Policy: pol, Base: dns.resolver(), Hosts: hosts}})
		})
		for _, id := range []string{"mcp:url:https://public.slicc.test/mcp", "mcp:url:https://93.184.215.14:8443/mcp", "mcp:url:https://93.184.215.15:8443/mcp"} {
			if r := env.p.jerk(t, map[string]any{"op": "create", "provider": id}); r.Reset != "" {
				t.Fatalf("%s: %+v", id, r)
			}
			if args, _ := lastArgs(t, log); args != id+" create --via-link" {
				t.Fatalf("create ran jerk %s", args)
			}
		}
		refused(t, env.p, log,
			"mcp:url:http://127.0.0.1:8931/mcp",
			"mcp:url:http://localhost:8931/mcp",
			"mcp:url:http://[::1]:8931/mcp",
			"mcp:url:https://10.0.0.1/mcp",
			"mcp:url:https://192.168.1.1:8443/mcp",
			"mcp:url:https://169.254.169.254/mcp",
			"mcp:url:https://[::ffff:7f00:1]/mcp",
			"mcp:url:https://[64:ff9b::7f00:1]/mcp",
			"mcp:url:https://93.184.215.15/mcp",
			"mcp:url:https://private.slicc.test/mcp",
			"mcp:url:https://mixed.slicc.test/mcp",
			"mcp:url:https://pinned.slicc.test/mcp",
			"mcp:url:https://missing.slicc.test/mcp",
		)
	})
	t.Run("local", func(t *testing.T) {
		log := useLog(t)
		env := setupInProcess(t, policy.Local(9444), func(n *node.Node) {
			capjerk.Register(n, capjerk.Options{Jerk: j, Seal: true})
		})
		for _, open := range []map[string]any{
			{"op": "create", "provider": jerkMCPID},
			{"op": "seal", "provider": jerkMCPID},
			{"op": "unseal", "provider": jerkMCPID, "lease_id": "x"},
			{"op": "list", "provider": jerkMCPID},
		} {
			if r := env.p.jerk(t, open); r.Reset != "" {
				t.Fatalf("%v: %+v", open, r)
			}
		}
		if args, _ := lastArgs(t, log); args != "list --json" {
			t.Fatalf("list ran jerk %s", args)
		}
		logs := env.a.stderr.String()
		for _, op := range []string{"create", "seal", "unseal"} {
			if want := "jerk: " + op + " mcp:url:http://127.0.0.1: exit 0"; !strings.Contains(logs, want) {
				t.Fatalf("no %q in the log:\n%s", want, logs)
			}
		}
		if strings.Contains(logs, "k3y") || strings.Contains(logs, ":8931") {
			t.Fatalf("the log shows the URL:\n%s", logs)
		}
		refused(t, env.p, log, "mcp:url:http://127.0.0.1:9444/mcp", "mcp:url:https://169.254.169.254/mcp")
	})
	t.Run("no policy", func(t *testing.T) {
		log := useLog(t)
		env := setupInProcess(t, nil, func(n *node.Node) {
			capjerk.Register(n, capjerk.Options{Jerk: j, Seal: true})
		})
		refused(t, env.p, log, "mcp:url:https://93.184.215.14/mcp")
	})
}

func TestJerkOffOrMissing(t *testing.T) {
	dir := fakeJerkDir(t)
	log := filepath.Join(t.TempDir(), "jerk.log")
	for name, c := range map[string]struct {
		env  []string
		args []string
		says string
	}{
		"--no-jerk":      {jerkEnv(dir, log), []string{"--no-jerk"}, ""},
		"an old jerk":    {jerkEnv(dir, log, "FAKEJERK_VERSION=0.5.0"), nil, "slicc: jerk: host jerk 0.5.0 is older than 0.6.0; run slicc install jerk"},
		"no jerk at all": {nil, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			e := setupLinkEnv(t, false, c.env, c.args...)
			if c.says != "" {
				e.a.waitStderr(t, c.says, 10*time.Second)
			}
			var h jerkHello
			e.p.eval("linkTest.ready()", &h)
			if slices.Contains(h.Caps, "jerk") || slices.Contains(h.Caps, "jerk-seal") || h.Jerk != nil {
				t.Fatalf("HELLO %+v", h)
			}
			if r := e.p.jerk(t, map[string]any{"op": "list"}); r.Reset != "EOPNOTSUPP" {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestJerkLimitsInProcess(t *testing.T) {
	t.Setenv("PATH", fakeJerkDir(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	j, _ := jerk.Find()
	if j == nil {
		t.Fatal("no fake jerk")
	}
	env := setupInProcess(t, policy.Local(), func(n *node.Node) {
		capjerk.Register(n, capjerk.Options{Jerk: j, CreateTimeout: time.Second})
	})
	var h jerkHello
	env.p.eval("linkTest.ready()", &h)
	if !slices.Contains(h.Caps, "jerk") || slices.Contains(h.Caps, "jerk-seal") {
		t.Fatalf("HELLO %+v", h)
	}
	for _, op := range []string{"seal", "unseal"} {
		if r := env.p.jerk(t, map[string]any{"op": op, "provider": "github", "lease_id": "x"}); r.Reset != "EACCES" {
			t.Fatalf("%s without jerk-seal: %+v", op, r)
		}
	}
	if r := env.p.jerk(t, map[string]any{"op": "create", "provider": "slow"}); r.Reset != "ETIMEDOUT" {
		t.Fatalf("a create past its timeout: %+v", r)
	}
	start := time.Now()
	t.Cleanup(func() { time.Sleep(time.Until(start.Add(9 * time.Second))) })
	if r := env.p.jerk(t, map[string]any{"op": "status", "provider": "orphan"}); r.Reset != "" || r.last()["message"] != "left a child holding stdout" || time.Since(start) > 6*time.Second {
		t.Fatalf("a child holding jerk's stdout: %+v after %s", r, time.Since(start))
	}
	r := env.p.jerk(t, map[string]any{"op": "create", "provider": "github"})
	if r.Reset != "" || strings.Contains(r.last()["message"].(string), "--seven-origin") {
		t.Fatalf("create without a known origin: %+v", r)
	}
}

func TestFollowJerkFlags(t *testing.T) {
	dir := fakeJerkDir(t)
	log := filepath.Join(t.TempDir(), "jerk.log")
	for name, c := range map[string]struct {
		args       []string
		jerk, seal bool
	}{
		"no flag":     {nil, false, false},
		"--jerk":      {[]string{"--jerk"}, true, false},
		"--jerk-seal": {[]string{"--jerk-seal"}, true, true},
	} {
		t.Run(name, func(t *testing.T) {
			env, h, trays, p, _ := followSetup(t, 1)
			fp := startFollow(t, append(env, jerkEnv(dir, log)...), h.joinURL(trays[0]), c.args...)
			fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
			p.waitUp(t, "")
			var hello jerkHello
			p.eval("linkTest.ready()", &hello)
			if slices.Contains(hello.Caps, "jerk") != c.jerk || slices.Contains(hello.Caps, "jerk-seal") != c.seal || (hello.Jerk != nil) != c.jerk {
				t.Fatalf("HELLO %+v", hello)
			}
			r := p.jerk(t, map[string]any{"op": "seal", "provider": "github"})
			switch {
			case !c.jerk && r.Reset != "EOPNOTSUPP", c.jerk && !c.seal && r.Reset != "EACCES", c.seal && len(r.of("lease")) != 1:
				t.Fatalf("seal: %+v", r)
			}
			if c.jerk {
				res := p.jerk(t, map[string]any{"op": "create", "provider": "github"})
				if strings.Contains(res.last()["message"].(string), "--seven-origin") {
					t.Fatalf("a remote create named a seven origin: %+v", res)
				}
			}
		})
	}
}

func TestJerkFindSkipsAnOldJerk(t *testing.T) {
	onPath, data := t.TempDir(), t.TempDir()
	writeExe(t, filepath.Join(onPath, exeName("jerk")), buildJerk(t, "0.5.0"))
	t.Setenv("PATH", onPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	if j, old := jerk.Find(data); j != nil || old != "0.5.0" {
		t.Fatalf("only an old jerk: %+v, %q", j, old)
	}
	writeExe(t, filepath.Join(data, exeName("jerk")), buildJerk(t, "0.6.0"))
	if j, old := jerk.Find(data); j == nil || j.Version != "0.6.0" || j.Path != filepath.Join(data, exeName("jerk")) || old != "" {
		t.Fatalf("an old jerk on PATH and a new one in the data folder: %+v, %q", j, old)
	}
}

func writeExe(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFollowJerkWithOldJerk(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, append(env, jerkEnv(fakeJerkDir(t), filepath.Join(t.TempDir(), "jerk.log"), "FAKEJERK_VERSION=0.5.0")...), h.joinURL(trays[0]), "--jerk-seal")
	fp.waitStderr(t, "slicc: jerk: host jerk 0.5.0 is older than 0.6.0; run slicc install jerk", 10*time.Second)
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.waitUp(t, "")
	var hello jerkHello
	p.eval("linkTest.ready()", &hello)
	if slices.Contains(hello.Caps, "jerk") || slices.Contains(hello.Caps, "jerk-seal") || hello.Jerk != nil {
		t.Fatalf("HELLO %+v", hello)
	}
}

func TestFollowJerkWithoutJerk(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]), "--jerk")
	fp.waitStderr(t, "slicc: jerk: no slicc-jerk 0.6.0 or later on PATH or in slicc's data folder; not offering it", 10*time.Second)
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.waitUp(t, "")
	var hello jerkHello
	p.eval("linkTest.ready()", &hello)
	if slices.Contains(hello.Caps, "jerk") {
		t.Fatalf("HELLO %+v", hello)
	}
}
