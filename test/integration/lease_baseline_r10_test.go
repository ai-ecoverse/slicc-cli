//go:build !windows

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

func leaseBLOriginKey(joinURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(joinURL)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func leaseBLSeedState(t *testing.T, path, typed, current string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"version": 1,
		"trays": []map[string]string{{
			"origin":    leaseBLOriginKey(typed),
			"joinUrl":   current,
			"updatedAt": time.Now().UTC().Format(time.RFC3339),
		}},
	}
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func leaseBLSeedOldFormat(t *testing.T, path, current string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(map[string]any{"joinUrl": current}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func leaseBLHubCounts(h *fakeHub, trays ...*fakeTray) string {
	var parts []string
	for i, tr := range trays {
		parts = append(parts, "t"+strconv.Itoa(i)+":"+strconv.Itoa(len(attachRequests(h, tr.Token))))
	}
	return strings.Join(parts, ",")
}

func leaseBLDialErr(t *testing.T, cfg tray.Config) (code string, redirected bool, current string) {
	t.Helper()
	var last string
	prev := cfg.OnJoinURLChanged
	cfg.OnJoinURLChanged = func(u string) {
		last = u
		if prev != nil {
			prev(u)
		}
	}
	f, err := tray.NewFollower(cfg)
	if err != nil {
		return "new-follower", false, ""
	}
	if cfg.State != nil {
		current = cfg.State.Current(cfg.JoinURL)
	} else {
		current = cfg.JoinURL
	}
	s, err := f.Dial(testCtx(t))
	if last != "" {
		current = last
	} else if cfg.State != nil {
		current = cfg.State.Current(cfg.JoinURL)
	}
	if err != nil {
		return tray.ErrorCode(err), f.Redirected(), current
	}
	t.Cleanup(func() { _ = s.Close() })
	return "nil", f.Redirected(), current
}

func newFakeHubTLS(t *testing.T) *fakeHub {
	t.Helper()
	h := &fakeHub{t: t, trays: map[string]*fakeTray{}}
	h.srv = httptest.NewTLSServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func TestSignedLeaseBaselineR10RestoredState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	statePath := filepath.Join(home, "slicc", "tray-join.json.lease")

	h := newFakeHub(t)
	foreign := newFakeHub(t)
	same := h.addTray("same")
	live := h.addTray("live")
	foreignTray := foreign.addTray("foreign")

	typed := h.joinURL(same)
	cases := []struct {
		name, seed string
		old        bool
	}{
		{"same-origin-successor", h.joinURL(live), false},
		{"foreign-origin", foreign.joinURL(foreignTray), false},
		{"different-port", foreign.joinURL(foreignTray), false},
		{"old-format-same", h.joinURL(live), true},
	}
	for _, c := range cases {
		_ = os.Remove(statePath)
		if c.old {
			leaseBLSeedOldFormat(t, statePath, c.seed)
		} else {
			leaseBLSeedState(t, statePath, typed, c.seed)
		}
		before := leaseBLFileFacts(statePath, c.seed)
		state := tray.OpenState(statePath)
		restored := state.Current(typed)
		reqs0 := len(h.requestLog()) + len(foreign.requestLog())
		cfg := fastConfig(typed, nil)
		cfg.State = state
		code, redirected, current := leaseBLDialErr(t, cfg)
		reqs1 := len(h.requestLog()) + len(foreign.requestLog())
		seedHost := "none"
		if u, err := url.Parse(c.seed); err == nil {
			seedHost = u.Hostname() + ":" + u.Port()
		}
		requestedSeed := "no"
		for _, r := range append(h.requestLog(), foreign.requestLog()...) {
			if strings.Contains(c.seed, r.Token) {
				requestedSeed = "yes"
				break
			}
		}
		after := leaseBLFileFacts(statePath, "")
		leaseBLLine(t, "R10-restored-state", "case", c.name, "old_format", c.old, "restored_equals_seed", restored == c.seed,
			"seed_host", seedHost, "requested_seed_url", requestedSeed, "dial_err", code, "redirected", redirected,
			"hub_requests_delta", reqs1-reqs0, "state_before", before, "state_after", after, "current_changed", current != restored)
	}

	tlsHub := newFakeHubTLS(t)
	tlsTray := tlsHub.addTray("scheme")
	_ = os.Remove(statePath)
	leaseBLSeedState(t, statePath, typed, tlsHub.joinURL(tlsTray))
	state := tray.OpenState(statePath)
	cfg := fastConfig(typed, nil)
	cfg.State = state
	cfg.HTTPClient = tlsHub.srv.Client()
	code, _, _ := leaseBLDialErr(t, cfg)
	requested := "no"
	for _, r := range tlsHub.requestLog() {
		if r.Token == tlsTray.Token {
			requested = "yes"
			break
		}
	}
	leaseBLLine(t, "R10-restored-state", "case", "different-scheme", "old_format", false, "restored_equals_seed", state.Current(typed) == tlsHub.joinURL(tlsTray) || requested == "yes",
		"seed_host", "tls", "requested_seed_url", requested, "dial_err", code, "redirected", false,
		"hub_requests_delta", len(tlsHub.requestLog()), "state_before", "seeded", "state_after", leaseBLFileFacts(statePath, ""), "current_changed", false)
}

func TestSignedLeaseBaselineR10Carriers(t *testing.T) {
	h := newFakeHub(t)
	foreign := newFakeHub(t)
	for _, carrier := range []string{"308-location", "link-only", "409-manual", "json-body"} {
		for _, dest := range []string{"same", "foreign"} {
			a := h.addTray(carrier + "-" + dest)
			b := h.addTray(carrier + "-" + dest + "-b")
			fb := foreign.addTray(carrier + "-" + dest)
			successor := h.joinURL(b)
			if dest == "foreign" {
				successor = foreign.joinURL(fb)
			}
			h.with(func() {
				a.SupersededBy = successor
				a.LinkOnly = carrier == "link-only"
				a.LegacySupersede = carrier == "409-manual"
				a.JSONOnly = carrier == "json-body"
			})
			join := h.joinURL(a)
			if carrier == "409-manual" {
				u, _ := url.Parse(join)
				q := u.Query()
				q.Set("redirect", "manual")
				u.RawQuery = q.Encode()
				join = u.String()
			}
			state := tray.OpenState(filepath.Join(t.TempDir(), "slicc", "tray-join.json.lease"))
			cfg := fastConfig(join, nil)
			cfg.State = state
			code, redirected, current := leaseBLDialErr(t, cfg)
			persisted := state.Current(h.joinURL(a))
			leaseBLLine(t, "R10-carrier", "carrier", carrier, "dest", dest, "dial_err", code, "redirected", redirected,
				"advanced", current == successor, "persisted_final", persisted == successor || persisted == current,
				"hub_a", len(attachRequests(h, a.Token)), "hub_b", len(attachRequests(h, b.Token)), "hub_foreign", len(attachRequests(foreign, fb.Token)))
		}
	}
}

func TestSignedLeaseBaselineR10MultiHop(t *testing.T) {
	h := newFakeHub(t)
	foreign := newFakeHub(t)

	same := supersedeChain(h, 3)
	state := tray.OpenState(filepath.Join(t.TempDir(), "slicc", "tray-join.json.lease"))
	cfg := fastConfig(h.joinURL(same[0]), nil)
	cfg.State = state
	code, redirected, current := leaseBLDialErr(t, cfg)
	leaseBLLine(t, "R10-multi-hop", "case", "same-origin-chain", "hops", 3, "dial_err", code, "redirected", redirected,
		"final", current == h.joinURL(same[3]), "persisted", state.Current(h.joinURL(same[0])) == h.joinURL(same[3]),
		"counts", leaseBLHubCounts(h, same...))

	mid := h.addTray("mid0")
	midF := foreign.addTray("midf")
	midEnd := h.addTray("mid1")
	h.with(func() { mid.SupersededBy = foreign.joinURL(midF) })
	foreign.with(func() { midF.SupersededBy = h.joinURL(midEnd) })
	state = tray.OpenState(filepath.Join(t.TempDir(), "slicc", "mid.lease"))
	cfg = fastConfig(h.joinURL(mid), nil)
	cfg.State = state
	code, redirected, current = leaseBLDialErr(t, cfg)
	leaseBLLine(t, "R10-multi-hop", "case", "foreign-mid-chain", "hops", 2, "dial_err", code, "redirected", redirected,
		"final", current == h.joinURL(midEnd), "persisted", state.Current(h.joinURL(mid)) == h.joinURL(midEnd),
		"counts", "h:"+strconv.Itoa(len(attachRequests(h, mid.Token)))+",f:"+strconv.Itoa(len(attachRequests(foreign, midF.Token)))+",e:"+strconv.Itoa(len(attachRequests(h, midEnd.Token))))

	end0 := h.addTray("end0")
	endF := foreign.addTray("endf")
	h.with(func() { end0.SupersededBy = foreign.joinURL(endF) })
	state = tray.OpenState(filepath.Join(t.TempDir(), "slicc", "end.lease"))
	cfg = fastConfig(h.joinURL(end0), nil)
	cfg.State = state
	code, redirected, current = leaseBLDialErr(t, cfg)
	leaseBLLine(t, "R10-multi-hop", "case", "foreign-at-end", "hops", 1, "dial_err", code, "redirected", redirected,
		"final", current == foreign.joinURL(endF), "persisted", state.Current(h.joinURL(end0)) == foreign.joinURL(endF),
		"counts", "h:"+strconv.Itoa(len(attachRequests(h, end0.Token)))+",f:"+strconv.Itoa(len(attachRequests(foreign, endF.Token))))

	five := supersedeChain(h, tray.MaxRedirects)
	code, redirected, _ = leaseBLDialErr(t, fastConfig(h.joinURL(five[0]), nil))
	leaseBLLine(t, "R10-multi-hop", "case", "five-hops-ok", "hops", tray.MaxRedirects, "dial_err", code, "redirected", redirected,
		"final", code == "nil", "persisted", "n/a", "counts", leaseBLHubCounts(h, five...))

	six := supersedeChain(h, tray.MaxRedirects+1)
	state = tray.OpenState(filepath.Join(t.TempDir(), "slicc", "six.lease"))
	cfg = fastConfig(h.joinURL(six[0]), nil)
	cfg.State = state
	code, redirected, current = leaseBLDialErr(t, cfg)
	leaseBLLine(t, "R10-multi-hop", "case", "six-hops-exhausted", "hops", tray.MaxRedirects+1, "dial_err", code, "redirected", redirected,
		"final", current == h.joinURL(six[tray.MaxRedirects]), "persisted", state.Current(h.joinURL(six[0])) == h.joinURL(six[tray.MaxRedirects]),
		"counts", leaseBLHubCounts(h, six...))

	a, b := h.addTray("loop-a"), h.addTray("loop-b")
	h.with(func() {
		a.SupersededBy = h.joinURL(b)
		b.SupersededBy = h.joinURL(a)
	})
	state = tray.OpenState(filepath.Join(t.TempDir(), "slicc", "loop.lease"))
	before := leaseBLFileFacts(state.Path(), "")
	cfg = fastConfig(h.joinURL(a), nil)
	cfg.State = state
	code, _, _ = leaseBLDialErr(t, cfg)
	leaseBLLine(t, "R10-multi-hop", "case", "same-origin-loop", "hops", tray.MaxRedirects, "dial_err", code, "redirected", true,
		"final", code == tray.CodeSupersedeExhausted, "persisted", leaseBLFileFacts(state.Path(), ""),
		"counts", "a:"+strconv.Itoa(len(attachRequests(h, a.Token)))+",b:"+strconv.Itoa(len(attachRequests(h, b.Token))),
		"state_before_empty", before)
}

func TestSignedLeaseBaselineR10PortScheme(t *testing.T) {
	h := newFakeHub(t)
	h2 := newFakeHub(t)
	tls := newFakeHubTLS(t)
	a := h.addTray("port-a")
	b := h2.addTray("port-b")
	h.with(func() { a.SupersededBy = h2.joinURL(b) })
	code, redirected, current := leaseBLDialErr(t, fastConfig(h.joinURL(a), nil))
	leaseBLLine(t, "R10-port-scheme", "case", "port-only", "dial_err", code, "redirected", redirected,
		"advanced_to_successor", current == h2.joinURL(b), "hub0", len(attachRequests(h, a.Token)), "hub1", len(attachRequests(h2, b.Token)))

	s0 := h.addTray("scheme-a")
	s1 := tls.addTray("scheme-b")
	h.with(func() { s0.SupersededBy = tls.joinURL(s1) })
	cfg := fastConfig(h.joinURL(s0), nil)
	tr := tls.srv.Client().Transport
	cfg.HTTPClient = &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	code, redirected, current = leaseBLDialErr(t, cfg)
	leaseBLLine(t, "R10-port-scheme", "case", "scheme-only", "dial_err", code, "redirected", redirected,
		"advanced_to_successor", strings.HasPrefix(current, "https://"), "hub0", len(attachRequests(h, s0.Token)), "hub1", len(attachRequests(tls, s1.Token)))
}

func TestSignedLeaseBaselineR10BadRedirect(t *testing.T) {
	h := newFakeHub(t)
	bare := h.addTray("bare")
	h.with(func() { bare.BareRedirect = true })
	code, _, _ := leaseBLDialErr(t, fastConfig(h.joinURL(bare), nil))
	leaseBLLine(t, "R10-bad-redirect", "case", "3xx-no-location", "dial_err", code, "requests_to_location", 0)

	bad := h.addTray("badloc")
	h.with(func() { bad.PlainRedirect = "https://example.test/not-a-join" })
	code, _, _ = leaseBLDialErr(t, fastConfig(h.joinURL(bad), nil))
	followed := "no"
	for _, r := range h.requestLog() {
		if strings.Contains(r.Token, "not-a-join") {
			followed = "yes"
		}
	}
	leaseBLLine(t, "R10-bad-redirect", "case", "3xx-location-not-join", "dial_err", code, "requests_to_location", followed)

	miss := h.addTray("miss")
	h.with(func() { miss.SupersedeMissing = true })
	code, _, _ = leaseBLDialErr(t, fastConfig(h.joinURL(miss), nil))
	leaseBLLine(t, "R10-bad-redirect", "case", "supersede-missing-join", "dial_err", code, "requests_to_location", 0)
}

func TestSignedLeaseBaselineR10ColdResumeOrdinary(t *testing.T) {
	env := configEnv(t)
	h := newFakeHub(t)
	trays := supersedeChain(h, 2)
	out, errOut, code := sliccEnv(t, env, h.joinURL(trays[0]), "follow", "--signal-only")
	path, err := tray.DefaultStatePath()
	if err != nil {
		t.Fatal(err)
	}
	leaseBLLine(t, "R10-ordinary-cold", "path", "signal-only", "exit", code, "remembered", strings.Contains(out, "superseded"),
		"stderr_redacted_or_empty", !strings.Contains(errOut+out, trays[0].Token), "persisted_final", tray.OpenState(path).Current(h.joinURL(trays[0])) == h.joinURL(trays[2]),
		"hub_counts", leaseBLHubCounts(h, trays...), "state", leaseBLFileFacts(path, h.joinURL(trays[2])))

	_, _, code2 := sliccEnv(t, env, h.joinURL(trays[0]), "follow", "--signal-only")
	leaseBLLine(t, "R10-ordinary-resume", "path", "signal-only", "exit", code2, "first_token_attaches", len(attachRequests(h, trays[0].Token)),
		"final_attaches", len(attachRequests(h, trays[2].Token)), "walked_start_again", len(attachRequests(h, trays[0].Token)) > 1)

	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	leaseEnv := append(configEnv(t), "XDG_CONFIG_HOME="+home, "SLICC_NO_TELEMETRY=1", "SLICC_NO_UPDATE_CHECK=1")
	leasePath := filepath.Join(home, "slicc", "tray-join.json.lease")
	a := h.addTray("lease-cold")
	b := h.addTray("lease-cold-b")
	h.with(func() { a.SupersededBy = h.joinURL(b) })
	owner := launchBrowser(t)
	_, page := launchWithPage(t, pageServer(t).URL+"/index.html?lease")
	_ = newTrayLeader(t, h, page, a, b)
	cmd := exec.Command(binary, "-", "follow", "--json", "--lease-browser", owner.endpoint, "--name", "r10cold")
	cmd.Env = leaseEnv
	cmd.Stdin = strings.NewReader(h.joinURL(a) + "\n")
	log := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	linked := (&attachProc{cmd: cmd, stderr: log}).waitStderrOK("linked to", 30*time.Second)
	leaseBLLine(t, "R10-lease-cold", "linked", linked, "state", leaseBLFileFacts(leasePath, ""),
		"hub_counts", "a:"+strconv.Itoa(len(attachRequests(h, a.Token)))+",b:"+strconv.Itoa(len(attachRequests(h, b.Token))),
		"persisted_final", tray.OpenState(leasePath).Current(h.joinURL(a)) == h.joinURL(b))
}
