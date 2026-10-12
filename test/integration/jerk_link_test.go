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

func lastArgs(t *testing.T, log string) (string, map[string]any) {
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
		if !slices.Contains(h.Caps, "jerk") || !slices.Contains(h.Caps, "jerk-seal") || h.Jerk == nil || h.Jerk.Version != "0.3.1" {
			t.Fatalf("HELLO %+v", h)
		}
		e.a.waitStderr(t, "slicc: jerk: offering slicc-jerk 0.3.1 (jerk, jerk-seal)", 5*time.Second)
	})
	t.Run("list, status and accounts keep only status fields", func(t *testing.T) { testJerkStatus(t, e.p, log) })
	t.Run("create", func(t *testing.T) { testJerkCreate(t, e, log) })
	t.Run("seal and unseal", func(t *testing.T) { testJerkSeal(t, e.p, log) })
	t.Run("refusals", func(t *testing.T) { testJerkRefusals(t, e.p) })
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

func TestJerkOffOrMissing(t *testing.T) {
	dir := fakeJerkDir(t)
	log := filepath.Join(t.TempDir(), "jerk.log")
	for name, c := range map[string]struct {
		env  []string
		args []string
	}{
		"--no-jerk":      {jerkEnv(dir, log), []string{"--no-jerk"}},
		"an old jerk":    {jerkEnv(dir, log, "FAKEJERK_VERSION=0.2.9"), nil},
		"no jerk at all": {nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			e := setupLinkEnv(t, false, c.env, c.args...)
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
	j := jerk.Find()
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
	if r := env.p.jerk(t, map[string]any{"op": "status", "provider": "orphan"}); r.Reset != "" || r.last()["message"] != "left a child holding stdout" || time.Since(start) > 10*time.Second {
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

func TestFollowJerkWithoutJerk(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, env, h.joinURL(trays[0]), "--jerk")
	fp.waitStderr(t, "slicc: jerk: no slicc-jerk 0.3.0 or later on PATH or in slicc's data folder; not offering it", 10*time.Second)
	fp.waitStderr(t, "slicc: linked to link test page", 60*time.Second)
	p.waitUp(t, "")
	var hello jerkHello
	p.eval("linkTest.ready()", &hello)
	if slices.Contains(hello.Caps, "jerk") {
		t.Fatalf("HELLO %+v", hello)
	}
}
