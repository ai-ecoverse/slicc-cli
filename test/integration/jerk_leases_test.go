package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	capjerk "github.com/ai-ecoverse/slicc-cli/internal/caps/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/jerk"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
)

const leaseSentinel = "gho_secret"

type revokeNotice struct {
	Provider string   `json:"provider"`
	LeaseID  string   `json:"leaseId"`
	By       string   `json:"by"`
	Node     string   `json:"node"`
	Mode     string   `json:"mode"`
	Acted    bool     `json:"acted"`
	Keys     []string `json:"keys"`
}

type leaseCtl struct {
	t       *testing.T
	path    string
	answers []string
}

func (c *leaseCtl) call(req map[string]any) map[string]any {
	c.t.Helper()
	req["v"] = 1
	conn, err := net.DialTimeout("unix", c.path, 5*time.Second)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		c.t.Fatalf("%v: %v", req, err)
	}
	c.answers = append(c.answers, line)
	var out map[string]any
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		c.t.Fatalf("%v: %q", req, line)
	}
	return out
}

func credentialEvents(a *attachProc) []map[string]any {
	var out []map[string]any
	for _, line := range strings.Split(a.out.String(), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil && m["event"] == "credential" {
			out = append(out, m)
		}
	}
	return out
}

func waitCredential(t *testing.T, a *attachProc, n int) map[string]any {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if evs := credentialEvents(a); len(evs) >= n {
			return evs[n-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no credential event #%d on stdout:\n%s", n, a.out.String())
		}
	}
}

func pageNotices(p *page) []revokeNotice {
	var out []revokeNotice
	p.eval("linkTest.revoke.notices()", &out)
	return out
}

func waitNotices(t *testing.T, p *page, n int) []revokeNotice {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if got := pageNotices(p); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the page got %d notices, want %d", len(pageNotices(p)), n)
		}
	}
}

func listed(ans map[string]any) []string {
	var ids []string
	list, _ := ans["leases"].([]any)
	for _, l := range list {
		ids = append(ids, l.(map[string]any)["leaseId"].(string))
	}
	return ids
}

func normalizeAnswer(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	if list, ok := out["leases"].([]any); ok {
		norm := []any{}
		for _, l := range list {
			row := map[string]any{}
			for k, v := range l.(map[string]any) {
				row[k] = v
			}
			row["expiresAt"], row["sealedAt"] = "<time>", "<time>"
			norm = append(norm, row)
		}
		out["leases"] = norm
	}
	return out
}

func seal(t *testing.T, p *page, provider string) {
	t.Helper()
	if r := p.jerk(t, map[string]any{"op": "seal", "provider": provider}); r.Reset != "" || len(r.of("lease")) != 1 {
		t.Fatalf("seal %s: %+v", provider, r)
	}
}

func relink(e *linkEnv) {
	var st struct {
		Nonce string `json:"nonce"`
	}
	e.p.eval("linkTest.status()", &st)
	e.p.reload()
	if e.stdio {
		e.p.rehello()
	}
	e.waitUp(st.Nonce)
}

func TestJerkLeasesFromTheHost(t *testing.T) {
	log := filepath.Join(t.TempDir(), "jerk.log")
	e := setupLinkEnv(t, false, jerkEnv(fakeJerkDir(t), log), "--json")
	c := &leaseCtl{t: t, path: ctlSocket(t, e.a.cmd.Process.Pid)}
	var fx controlLog
	st, err := ctlStatus(c.path)
	if err != nil || !slices.Contains(st.Ops, ctl.OpLeases) || !slices.Contains(st.Ops, ctl.OpUnseal) || !slices.Contains(st.Ops, ctl.OpSealOff) || !slices.Contains(st.Ops, ctl.OpSealOn) {
		t.Fatalf("status %+v %v", st, err)
	}
	if ans := c.call(map[string]any{"op": "leases"}); ans["sealing"] != true || len(listed(ans)) != 0 {
		t.Fatalf("leases before a seal: %v", ans)
	}

	seal(t, e.p, "github")
	if ev := waitCredential(t, e.a, 1); ev["op"] != "seal" || ev["provider"] != "github" || ev["leaseId"] != "jerk-github-1" || ev["by"] != "seven" || ev["expiresAt"] == nil {
		t.Fatalf("seal event %v", ev)
	}
	fx.add(t, "seal github", normalizeEvent(t, mustJSON(waitCredential(t, e.a, 1))))
	seal(t, e.p, "minted")
	ans := c.call(map[string]any{"op": "leases"})
	if ids := listed(ans); !slices.Equal(ids, []string{"jerk-github-1", "jerk-minted-1"}) {
		t.Fatalf("leases %v", ans)
	}
	fx.add(t, "leases", normalizeAnswer(ans))

	if r := e.p.jerk(t, map[string]any{"op": "unseal", "provider": "github", "lease_id": "jerk-github-1"}); r.Reset != "" {
		t.Fatalf("seven's unseal %+v", r)
	}
	if ev := waitCredential(t, e.a, 3); ev["op"] != "unseal" || ev["by"] != "seven" || ev["leaseId"] != "jerk-github-1" || ev["upstream"] != false {
		t.Fatalf("unseal event %v", ev)
	}
	fx.add(t, "unseal by seven", normalizeEvent(t, mustJSON(waitCredential(t, e.a, 3))))
	if ids := listed(c.call(map[string]any{"op": "leases"})); !slices.Equal(ids, []string{"jerk-minted-1"}) {
		t.Fatalf("after seven's unseal: %v", ids)
	}

	for _, bad := range []struct {
		req  map[string]any
		want string
	}{
		{map[string]any{"op": "unseal"}, "EPROTO"},
		{map[string]any{"op": "unseal", "leaseId": "a b"}, "EPROTO"},
		{map[string]any{"op": "unseal", "leaseId": "jerk-github-1"}, "ENOENT"},
		{map[string]any{"op": "unseal", "leaseId": "jerk-nobody-1"}, "ENOENT"},
	} {
		if ans := c.call(bad.req); ans["error"] != bad.want || ans["ok"] == true {
			t.Fatalf("%v: %v, want %s", bad.req, ans, bad.want)
		}
	}

	ans = c.call(map[string]any{"op": "unseal", "leaseId": "jerk-minted-1"})
	if ans["ok"] != true || ans["revoked"] != true || ans["upstream"] != true {
		t.Fatalf("host unseal %v", ans)
	}
	fx.add(t, "unseal from the host", ans)
	if args, call := lastArgs(t, log); args != "minted drop --lease --via-link" || call["lease"] != "jerk-minted-1" {
		t.Fatalf("host unseal ran jerk %q %v", args, call)
	}
	if ev := waitCredential(t, e.a, 4); ev["op"] != "revoked" || ev["by"] != "host" || ev["leaseId"] != "jerk-minted-1" || ev["upstream"] != true {
		t.Fatalf("revoked event %v", ev)
	}
	fx.add(t, "revoked", normalizeEvent(t, mustJSON(waitCredential(t, e.a, 4))))
	var hello struct {
		Node string `json:"node"`
	}
	e.p.eval("linkTest.ready()", &hello)
	notices := waitNotices(t, e.p, 1)
	if n := notices[0]; n.Provider != "minted" || n.LeaseID != "jerk-minted-1" || n.By != "host" || !n.Acted || n.Node != hello.Node || !slices.Equal(n.Keys, []string{"by", "kind", "lease_id", "provider"}) {
		t.Fatalf("notice %+v (node %s)", n, hello.Node)
	}
	if len(pageNotices(e.p)) != 1 {
		t.Fatal("seven's own unseal sent a notice")
	}

	seal(t, e.p, "github")
	var bound map[string]any
	e.p.eval("linkTest.revoke.rebind('jerk-github-1', 'another-node')", &bound)
	if ans := c.call(map[string]any{"op": "unseal", "leaseId": "jerk-github-1"}); ans["ok"] != true || ans["upstream"] != false {
		t.Fatalf("host unseal of a read token %v", ans)
	}
	notices = waitNotices(t, e.p, 2)
	if n := notices[1]; n.LeaseID != "jerk-github-1" || n.Acted {
		t.Fatalf("a notice for another node's lease was acted on: %+v", n)
	}
	var kept map[string]any
	e.p.eval("linkTest.revoke.leases()", &kept)
	if kept["jerk-github-1"] == nil {
		t.Fatalf("the page forgot another node's lease: %v", kept)
	}

	seal(t, e.p, "gitlab")
	e.p.eval("linkTest.revoke.mode('reset')", nil)
	if ans := c.call(map[string]any{"op": "unseal", "leaseId": "jerk-gitlab-1"}); ans["ok"] != true {
		t.Fatalf("host unseal gitlab %v", ans)
	}
	notices = waitNotices(t, e.p, 3)
	if n := notices[2]; n.LeaseID != "jerk-gitlab-1" || n.Mode != "reset" || n.Acted {
		t.Fatalf("refused notice %+v", n)
	}
	e.a.waitStderr(t, "jerk: revoke notice gitlab:", 10*time.Second)
	before := len(credentialEvents(e.a))
	if r := e.p.jerk(t, map[string]any{"op": "unseal", "provider": "github", "lease_id": "jerk-gitlab-1"}); r.Reset != "" || r.last()["exit"] != 0.0 {
		t.Fatalf("unseal of gitlab's lease id as github %+v", r)
	}
	time.Sleep(300 * time.Millisecond)
	if got := len(credentialEvents(e.a)); got != before {
		t.Fatalf("an unseal under another provider printed a credential event: %v", credentialEvents(e.a)[before:])
	}
	e.p.eval("linkTest.revoke.mode('accept')", nil)
	relink(e)
	again := waitNotices(t, e.p, 6)[3:]
	byID := map[string]revokeNotice{}
	for _, n := range again {
		byID[n.LeaseID] = n
	}
	if n, ok := byID["jerk-gitlab-1"]; !ok || !n.Acted || len(byID) != 3 {
		t.Fatalf("the refused notice didn't go out on the next link: %+v", again)
	}

	seal(t, e.p, "dropfail")
	ans = c.call(map[string]any{"op": "unseal", "leaseId": "jerk-dropfail-1"})
	if ans["ok"] != false || ans["error"] != "EIO" || ans["exit"] != 4.0 {
		t.Fatalf("a failing drop %v", ans)
	}
	fx.add(t, "unseal that jerk fails", ans)
	if ids := listed(c.call(map[string]any{"op": "leases"})); !slices.Equal(ids, []string{"jerk-dropfail-1"}) {
		t.Fatalf("a failed drop left %v", ids)
	}

	ans = c.call(map[string]any{"op": "seal-off"})
	if ans["ok"] != true || ans["sealing"] != false {
		t.Fatalf("seal-off %v", ans)
	}
	fx.add(t, "seal-off", ans)
	r := e.p.jerk(t, map[string]any{"op": "seal", "provider": "github"})
	if r.Reset != "EACCES" || !strings.Contains(r.Detail, "the host turned off sealing") {
		t.Fatalf("seal after seal-off %+v", r)
	}
	fx.add(t, "seal after seal-off", map[string]any{"reset": r.Reset, "detail": r.Detail})
	if ids := listed(c.call(map[string]any{"op": "leases"})); !slices.Equal(ids, []string{"jerk-dropfail-1"}) {
		t.Fatalf("seal-off dropped a live lease: %v", ids)
	}
	relink(e)
	var h jerkHello
	e.p.eval("linkTest.ready()", &h)
	if slices.Contains(h.Caps, "jerk-seal") || !slices.Contains(h.Caps, "jerk") {
		t.Fatalf("HELLO after seal-off %+v", h)
	}
	if ans := c.call(map[string]any{"op": "leases"}); ans["sealing"] != false {
		t.Fatalf("leases after seal-off %v", ans)
	}
	ans = c.call(map[string]any{"op": "seal-on"})
	if ans["ok"] != true || ans["sealing"] != true {
		t.Fatalf("seal-on %v", ans)
	}
	fx.add(t, "seal-on", ans)
	relink(e)
	e.p.eval("linkTest.ready()", &h)
	if !slices.Contains(h.Caps, "jerk-seal") {
		t.Fatalf("HELLO after seal-on %+v", h)
	}
	seal(t, e.p, "github")

	for what, text := range map[string]string{"stdout": e.a.out.String(), "stderr": e.a.stderr.String(), "control answers": strings.Join(c.answers, "")} {
		if strings.Contains(text, leaseSentinel) {
			t.Fatalf("a credential reached %s:\n%s", what, text)
		}
	}
	nativeFixture(t, "credential.ndjson", fx.buf.Bytes())
	c.call(map[string]any{"op": "stop"})
	if err := e.a.cmd.Wait(); err != nil {
		t.Fatalf("attach after stop: %v", err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func ctlStatus(path string) (*ctl.Status, error) {
	var last error
	for range 20 {
		st, err := ctl.Query(context.Background(), path)
		if err == nil {
			return st, nil
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	return nil, last
}

func TestJerkRevokeNoticeTimesOutAndWaits(t *testing.T) {
	t.Setenv("PATH", fakeJerkDir(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	j, _ := jerk.Find()
	if j == nil {
		t.Fatal("no fake jerk")
	}
	var s *capjerk.Server
	env := setupInProcess(t, policy.Local(), func(n *node.Node) {
		s = capjerk.Register(n, capjerk.Options{Jerk: j, Seal: true, NoticeTimeout: 500 * time.Millisecond})
	})
	seal(t, env.p, "github")
	env.p.eval("linkTest.revoke.mode('silent')", nil)
	if r := s.Unseal(context.Background(), "jerk-github-1"); r.Code != "" {
		t.Fatalf("unseal %+v", r)
	}
	waitNotices(t, env.p, 1)
	env.a.waitStderr(t, "jerk: revoke notice github: ETIMEDOUT", 10*time.Second)
	env.p.eval("linkTest.revoke.mode('accept')", nil)
	relink(env)
	if n := waitNotices(t, env.p, 2)[1]; n.LeaseID != "jerk-github-1" || !n.Acted {
		t.Fatalf("the timed-out notice on the next link %+v", n)
	}
	if strings.Contains(env.a.stderr.String(), leaseSentinel) {
		t.Fatalf("a credential reached the log:\n%s", env.a.stderr.String())
	}
}

func envPath(env []string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "PATH") {
			return v
		}
	}
	return os.Getenv("PATH")
}

func TestDefaultJSONPrintsCredentialEvents(t *testing.T) {
	trayRuntime(t)
	env, _ := launchEnv(t)
	env, id := testChrome(t, env)
	dir := fakeJerkDir(t)
	env = overrideEnv(env, map[string]string{"PATH": dir + string(os.PathListSeparator) + envPath(env)})
	srv := pageServer(t)
	pageURL := srv.URL + "/"
	profile := "it-credential-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	f := startFlow(t, env, "--json", "--no-kernel", "--browser", id, "--profile", profile, "--headless", "--page", pageURL, "--name", "it-credential")
	f.next(t, "discovered")
	l := f.next(t, "launched")
	t.Cleanup(func() { _ = os.RemoveAll(l.ProfileDir) })
	f.next(t, "attached")
	f.next(t, "linked")
	p := dialPage(t, l.CDP, pageURL)
	p.waitFor("linkTest.status().up", 30*time.Second)
	seal(t, p, "minted")
	ev := f.next(t, "credential")
	if !strings.Contains(ev.raw, `"op":"seal"`) || !strings.Contains(ev.raw, `"leaseId":"jerk-minted-1"`) || !strings.Contains(ev.raw, `"by":"seven"`) {
		t.Fatalf("seal event %s", ev.raw)
	}
	c := &leaseCtl{t: t, path: ctlSocket(t, f.cmd.Process.Pid)}
	if ans := c.call(map[string]any{"op": "unseal", "leaseId": "jerk-minted-1"}); ans["ok"] != true || ans["upstream"] != true {
		t.Fatalf("host unseal %v", ans)
	}
	ev = f.next(t, "credential")
	if !strings.Contains(ev.raw, `"op":"revoked"`) || !strings.Contains(ev.raw, `"by":"host"`) || !strings.Contains(ev.raw, `"upstream":true`) {
		t.Fatalf("revoked event %s", ev.raw)
	}
	if n := waitNotices(t, p, 1)[0]; n.LeaseID != "jerk-minted-1" || !n.Acted {
		t.Fatalf("notice %+v", n)
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	f.rest(t)
	for _, e := range f.seen {
		if strings.Contains(e.raw, leaseSentinel) {
			t.Fatalf("a credential in an event: %s", e.raw)
		}
	}
	if strings.Contains(f.stderr.String(), leaseSentinel) {
		t.Fatal("a credential on stderr")
	}
}

func TestFollowJSONPrintsCredentialEvents(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	fp := startFollow(t, append(env, jerkEnv(fakeJerkDir(t), filepath.Join(t.TempDir(), "jerk.log"))...), h.joinURL(trays[0]), "--jerk-seal", "--json")
	p.waitUp(t, "")
	seal(t, p, "minted")
	fp.waitStderr(t, `"event":"credential","op":"seal","provider":"minted","leaseId":"jerk-minted-1"`, 15*time.Second)
	c := &leaseCtl{t: t, path: ctlSocket(t, fp.cmd.Process.Pid)}
	if ans := c.call(map[string]any{"op": "unseal", "leaseId": "jerk-minted-1"}); ans["ok"] != true || ans["upstream"] != true {
		t.Fatalf("host unseal %v", ans)
	}
	fp.waitStderr(t, `"op":"revoked"`, 15*time.Second)
	if n := waitNotices(t, p, 1)[0]; n.LeaseID != "jerk-minted-1" || !n.Acted {
		t.Fatalf("notice %+v", n)
	}
	c.call(map[string]any{"op": "stop"})
	if code := fp.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(fp.stderr.String(), leaseSentinel) {
		t.Fatalf("a credential in follow's output:\n%s", fp.stderr.String())
	}
}

func TestJerkConcurrentLinkAndHostUnseal(t *testing.T) {
	var fx controlLog
	for _, row := range []struct {
		name    string
		timeout bool
	}{
		{name: "completed while waiting"},
		{name: "wait deadline exhausted", timeout: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			var answer map[string]any
			if row.timeout {
				answer = waitingUnsealDeadline(t)
			} else {
				answer = concurrentUnseal(t)
			}
			fx.add(t, row.name, answer)
		})
	}
	if !t.Failed() {
		nativeFixture(t, "unseal-race.ndjson", fx.buf.Bytes())
	}
}

func concurrentUnseal(t *testing.T) map[string]any {
	t.Helper()
	first := make(chan chan bool, 2)
	stop := make(chan struct{})
	var drops atomic.Int32
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if drops.Add(1) > 2 {
			_, _ = w.Write([]byte(`{"message":"already dropped"}`))
			return
		}
		release := make(chan bool, 1)
		first <- release
		select {
		case ok := <-release:
			if !ok {
				w.WriteHeader(http.StatusConflict)
			}
			_, _ = w.Write([]byte(`{"message":"dropped","revoked":true}`))
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer func() { close(stop); gate.Close() }()
	log := filepath.Join(t.TempDir(), "jerk.log")
	e := setupLinkEnv(t, false, jerkEnv(fakeJerkDir(t), log, "FAKEJERK_DROP_GATE="+gate.URL), "--json")
	seal(t, e.p, "minted")
	waitCredential(t, e.a, 1)
	e.p.eval(`globalThis.unsealRace = linkTest.jerk({op:'unseal',provider:'minted',lease_id:'jerk-minted-1'}); true`, nil)
	var releaseLink chan bool
	select {
	case releaseLink = <-first:
	case <-time.After(15 * time.Second):
		t.Fatal("link drop never reached the gate")
	}
	c := &leaseCtl{t: t, path: ctlSocket(t, e.a.cmd.Process.Pid)}
	host := make(chan map[string]any, 1)
	go func() { host <- c.call(map[string]any{"op": "unseal", "leaseId": "jerk-minted-1"}) }()
	var answer map[string]any
	var releaseHost chan bool
	select {
	case releaseHost = <-first:
	case <-time.After(time.Second):
	}
	releaseLink <- true
	var result jerkRes
	e.p.eval("unsealRace", &result)
	if result.Reset != "" || result.last()["exit"] != 0.0 {
		t.Fatal("link unseal failed")
	}
	if releaseHost != nil {
		releaseHost <- false
	}
	select {
	case answer = <-host:
		if answer["ok"] != true || answer["revoked"] != true || answer["upstream"] != true {
			t.Errorf("host did not observe the completed link unseal: %v", answer)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("host unseal did not finish")
	}
	if len(listed(c.call(map[string]any{"op": "leases"}))) != 0 {
		t.Error("a dropped lease was restored")
	}
	if r := e.p.jerk(t, map[string]any{"op": "unseal", "provider": "minted", "lease_id": "jerk-minted-1"}); r.Reset != "" || r.last()["exit"] != 0.0 {
		t.Error("repeated link unseal did not retain the successful result")
	}
	if calls := jerkCalls(t, log); len(calls) != 2 {
		t.Errorf("jerk ran %d times, want one seal and one drop", len(calls))
	}
	if events := credentialEvents(e.a); len(events) != 2 || events[1]["op"] != "unseal" || events[1]["by"] != "seven" {
		t.Error("seven's unseal event was lost or duplicated")
	}
	return answer
}

func waitingUnsealDeadline(t *testing.T) map[string]any {
	t.Helper()
	entered, release := make(chan struct{}, 1), make(chan struct{})
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
			_, _ = w.Write([]byte(`{"message":"dropped","revoked":true}`))
		case <-r.Context().Done():
		}
	}))
	defer gate.Close()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	t.Setenv("FAKEJERK_DROP_GATE", gate.URL)
	log := filepath.Join(t.TempDir(), "jerk.log")
	t.Setenv("FAKEJERK_LOG", log)
	j := &jerk.Jerk{Path: filepath.Join(fakeJerkDir(t), exeName("jerk"))}
	var s *capjerk.Server
	events := make(chan node.Credential, 4)
	e := setupInProcess(t, policy.Local(), func(n *node.Node) {
		s = capjerk.Register(n, capjerk.Options{Jerk: j, Seal: true, Notify: func(c node.Credential) { events <- c }})
	})
	seal(t, e.p, "minted")
	e.p.eval(`globalThis.unsealRace = linkTest.jerk({op:'unseal',provider:'minted',lease_id:'jerk-minted-1'}); true`, nil)
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("link drop never reached the gate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	out := s.Unseal(ctx, "jerk-minted-1")
	if out.Code != "EBUSY" {
		t.Errorf("wait deadline result: %s", out.Code)
	}
	if len(s.Leases()) != 1 {
		t.Error("timed-out waiter removed the lease")
	}
	unblock()
	var result jerkRes
	e.p.eval("unsealRace", &result)
	if result.Reset != "" || result.last()["exit"] != 0.0 || len(s.Leases()) != 0 {
		t.Error("link unseal did not complete after the waiter timed out")
	}
	if len(jerkCalls(t, log)) != 2 || len(events) != 2 {
		t.Error("timed-out waiter duplicated a drop or lost an event")
	}
	return map[string]any{"v": 1, "ok": false, "error": out.Code}
}
