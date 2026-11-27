//go:build !windows

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
)

type leaseBLEnd struct {
	Type string `json:"type"`
	Code string `json:"code"`
}

func (e *leaseBLEnv) collect(d time.Duration) leaseBLEnd {
	e.t.Helper()
	var end leaseBLEnd
	e.p.evalWithin("leaseTest.collect()", &end, d)
	return end
}

func (e *leaseBLEnv) exp() int64 {
	e.t.Helper()
	var c struct {
		Exp int64 `json:"exp"`
	}
	e.p.eval("leaseTest.claims()", &c)
	return c.Exp
}

func TestSignedLeaseBaselineRevokeHolder(t *testing.T) {
	e := newLeaseBLEnv(t, 1)
	for _, op := range []string{"goto", "snapshot"} {
		id := e.grant(60)
		n := "r1" + op
		if op == "goto" {
			if v := e.action("goto", map[string]string{"url": e.site.srv.URL + "/fast?n=" + n}); v["type"] != "result" {
				t.Fatalf("setup goto: %v", v)
			}
		} else {
			if v := e.action("goto", map[string]string{"url": e.site.srv.URL + "/burst?ms=700&n=" + n}); v["type"] != "result" {
				t.Fatalf("setup goto: %v", v)
			}
			time.Sleep(700 * time.Millisecond)
		}
		tab := e.leasedTab("n=" + n)
		before := e.status()
		var arrived time.Time
		if op == "goto" {
			e.dispatch("goto", map[string]string{"url": e.site.srv.URL + "/slow?n=" + n})
			arrived = e.site.wait("arrived:"+n, 8*time.Second)
			time.Sleep(200 * time.Millisecond)
		} else {
			e.dispatch("snapshot", map[string]any{})
			time.Sleep(50 * time.Millisecond)
		}
		h0, t0 := e.now(), time.Now()
		e.cli("revoke", id)
		t1 := time.Now()
		h1 := e.now()
		during := e.status()
		end := e.collect(15 * time.Second)
		t2 := time.Now()
		gone := e.tabGone(tab, 6*time.Second)
		abort := e.site.wait("aborted:"+n, 2*time.Second)
		e.site.open(n)
		time.Sleep(300 * time.Millisecond)
		fs := e.frames()
		resAfter0, failAfter0, reset, resetT, _ := leaseBLFrames(fs, h0)
		resAfter1, _, _, _, _ := leaseBLFrames(fs, h1)
		again := e.connect("leaseTest")
		leaseBLLine(t, "R1-"+op+"-holder", "follower_pid", e.follow.Process.Pid, "holder_browser_pid", e.holder.cmd.Process.Pid, "owner_browser_pid", e.owner.cmd.Process.Pid,
			"nav_arrived_ms", leaseBLAt(arrived), "revoke_cli_start_ms", leaseBLAt(t0), "revoke_cli_ms", leaseBLSince(t0, t1),
			"holder_reset_after_revoke_start_ms", fmt.Sprintf("%.1f", resetT-h0), "holder_reset_code", reset, "holder_collect_returned_after_ms", leaseBLSince(t0, t2), "holder_collect", end.Type+":"+end.Code,
			"holder_results_after_revoke_start", resAfter0, "holder_errors_after_revoke_start", failAfter0, "holder_results_after_revoke_return", resAfter1,
			"tab_gone_after_ms", leaseBLSince(t0, gone), "nav_request_aborted_after_ms", leaseBLSince(t0, abort), "late_nav_served", !e.site.at("released:"+n).IsZero(),
			"reconnect_same_jwt", again, "status_before", before, "status_after_revoke", during, "status_end", e.status())
		if op == "goto" && (resAfter0 != 0 || arrived.IsZero()) {
			t.Fatal("a result reached the holder after the owner revoked an in-flight goto")
		}
		if again == "ready" {
			t.Fatal("a revoked lease reconnected")
		}
		if op == "goto" {
			t1 := time.Now()
			var req struct{ Type, Request, Code string }
			e.p.eval("leaseTest.tryRequest("+jsArgs([]any{map[string]string{"kind": "origin", "url": e.site.srv.URL}}, map[string]int{"ttlSeconds": 60})+")", &req)
			pendingStatus := e.status()
			deny := "none"
			if req.Type == "pending" {
				e.cli("deny", req.Request)
				var d string
				e.p.eval("leaseTest.tryGrant()", &d)
				deny = d
			}
			leaseBLLine(t, "R8-holder", "after_revoke_reconnect", again, "new_request", req.Type+req.Code, "status_with_new_request", pendingStatus, "owner_deny_holder_saw", deny,
				"checked_ms", leaseBLSince(t1, time.Now()), "status_end", e.status(), "close_admissions_op", "none")
		}
	}
}

func TestSignedLeaseBaselineExpiryHolder(t *testing.T) {
	e := newLeaseBLEnv(t, 1)
	_ = e.grant(4)
	exp := e.exp()
	if v := e.action("goto", map[string]string{"url": e.site.srv.URL + "/fast?n=r2"}); v["type"] != "result" {
		t.Fatalf("setup goto: %v", v)
	}
	tab := e.leasedTab("n=r2")
	e.dispatch("goto", map[string]string{"url": e.site.srv.URL + "/slow?n=r2"})
	arrived := e.site.wait("arrived:r2", 3*time.Second)
	during := e.status()
	end := e.collect(15 * time.Second)
	back := time.Now()
	gone := e.tabGone(tab, 6*time.Second)
	abort := e.site.wait("aborted:r2", 2*time.Second)
	e.site.open("r2")
	fs := e.frames()
	res, fails, reset, _, resetW := leaseBLFrames(fs, 0)
	after := e.connect("leaseTest")
	expAt := time.Unix(exp, 0)
	leaseBLLine(t, "R2-goto-holder", "follower_pid", e.follow.Process.Pid, "ttl_s", 4, "exp_unix", exp, "nav_arrived_before_exp", !arrived.IsZero() && arrived.Before(expAt),
		"holder_reset_wall_minus_exp_ms", fmt.Sprintf("%.1f", resetW-float64(exp*1000)), "holder_reset_code", reset, "holder_collect", end.Type+":"+end.Code,
		"collect_returned_minus_exp_ms", leaseBLSince(expAt, back), "holder_results", res, "holder_errors", fails,
		"tab_gone_minus_exp_ms", leaseBLSince(expAt, gone), "nav_aborted_minus_exp_ms", leaseBLSince(expAt, abort),
		"reconnect_after_exp", after, "status_in_term", during, "status_after_exp", e.status())
	if res != 1 || after == "ready" {
		t.Fatal("an expired lease delivered or reconnected")
	}
}

func TestSignedLeaseBaselineStall(t *testing.T) {
	e := newLeaseBLEnv(t, 1)
	_ = e.grant(300)
	if v := e.action("goto", map[string]string{"url": e.site.srv.URL + "/fast?n=r3"}); v["type"] != "result" {
		t.Fatalf("setup goto: %v", v)
	}
	tab := e.leasedTab("n=r3")
	e.dispatch("goto", map[string]string{"url": e.site.srv.URL + "/slow?n=r3a"})
	e.site.wait("arrived:r3a", 5*time.Second)
	h0 := e.now()
	logMark := len(e.log.String())
	s0 := time.Now()
	e.stopHolder()
	time.Sleep(300 * time.Millisecond)
	e.site.open("r3a")
	committed := e.site.wait("released:r3a", 3*time.Second)
	time.Sleep(2 * time.Second)
	stalled := e.status()
	tabDuring := e.pageTargets()[tab]
	e.contHolder()
	c0 := time.Now()
	end := e.collect(20 * time.Second)
	resumed := time.Now()
	fs := e.frames()
	res, fails, reset, _, _ := leaseBLFrames(fs, h0)
	lostShort := strings.Contains(e.log.String()[logMark:], "link-lost")
	leaseBLLine(t, "R3-short-stall", "holder_browser_pid", e.holder.cmd.Process.Pid, "follower_pid", e.follow.Process.Pid, "stall_ms", leaseBLSince(s0, c0),
		"nav_committed_during_stall", !committed.IsZero(), "tab_during_stall", tabDuring, "status_during_stall", stalled, "link_lost_logged", lostShort,
		"holder_result_after_resume_ms", leaseBLSince(c0, resumed), "holder_read", end.Type+":"+end.Code, "holder_results", res, "holder_errors", fails, "holder_reset", reset)
	snap := e.action("snapshot", map[string]any{})
	leaseBLLine(t, "R3-after-short-stall", "snapshot_type", snap["type"], "snapshot_has_slow_text", strings.Contains(fmt.Sprint(snap), "Baseline slow r3a"), "status", e.status())
	pend := e.request("otherHolder", 60)
	withPending := e.status()
	e.dispatch("goto", map[string]string{"url": e.site.srv.URL + "/slow?n=r3b"})
	e.site.wait("arrived:r3b", 5*time.Second)
	nonce := e.p.status().Nonce
	e.h.with(func() { e.h.hang = true })
	logMark = len(e.log.String())
	s1 := time.Now()
	e.stopHolder()
	lost := e.waitLog("link-lost", logMark, 60*time.Second)
	afterLoss := e.status()
	tabAfterLoss := e.pageTargets()[tab]
	abortAfterLoss := !e.site.at("aborted:r3b").IsZero()
	e.site.open("r3b")
	released := e.site.wait("released:r3b", 3*time.Second)
	time.Sleep(time.Second)
	tabLate := e.pageTargets()[tab]
	e.contHolder()
	c1 := time.Now()
	e.h.with(func() { e.h.hang = false })
	relinked := time.Time{}
	resume := "not-attempted"
	var snap2 map[string]any
	if !lost.IsZero() {
		e.p.waitUp(t, nonce)
		relinked = time.Now()
		resume = e.connect("leaseTest")
		if resume == "ready" {
			snap2 = e.action("snapshot", map[string]any{})
		}
	}
	var pg string
	e.p.eval("otherHolder.tryGrant()", &pg)
	leaseBLLine(t, "R3-long-stall", "stall_until_link_lost_ms", leaseBLSince(s1, lost), "link_lost_observed", !lost.IsZero(), "pending_request_id_len", len(pend), "status_with_pending_before", withPending, "status_after_link_lost", afterLoss,
		"tab_after_link_lost", tabAfterLoss, "nav_request_aborted_by_link_loss", abortAfterLoss, "late_nav_committed_while_unlinked", !released.IsZero(), "tab_after_late_commit", tabLate)
	leaseBLLine(t, "R4-link-drop", "method", "holder-process-group-SIGSTOP-plus-hub-unreachable", "link_lost_observed", !lost.IsZero(), "link_lost_after_ms", leaseBLSince(s1, lost), "status_after_link_lost", afterLoss,
		"pending_request_holder_saw", pg, "relink_after_resume_ms", leaseBLSince(c1, relinked), "resume_same_jwt", resume,
		"snapshot_after_resume", fmt.Sprint(snap2["type"]), "snapshot_has_work_done_while_unlinked", strings.Contains(fmt.Sprint(snap2), "Baseline slow r3b"), "status_end", e.status(),
		"stall_kept_link", lost.IsZero())
	_ = nonce
}

func leaseBLFileFacts(path, needle string) string {
	st, err := os.Stat(path)
	if err != nil {
		return "present:false"
	}
	b, _ := os.ReadFile(path)
	var f struct {
		Version int `json:"version"`
		Trays   []struct {
			Origin  string `json:"origin"`
			JoinURL string `json:"joinUrl"`
		} `json:"trays"`
	}
	_ = json.Unmarshal(b, &f)
	keyed := len(f.Trays) > 0 && strings.HasPrefix(f.Trays[0].Origin, "sha256:")
	dir, _ := os.Stat(filepath.Dir(path))
	return fmt.Sprintf("present:true,mode:%o,dir_mode:%o,bytes:%d,entries:%d,keyed_sha256:%t,contains_typed_join:%t", st.Mode().Perm(), dir.Mode().Perm(), len(b), len(f.Trays), keyed, needle != "" && bytes.Contains(b, []byte(needle)))
}

func (e *leaseBLEnv) hubCount(token string) int {
	n := 0
	for _, r := range e.h.requestLog() {
		if r.Token == token {
			n++
		}
	}
	return n
}

func TestSignedLeaseBaselineRestart(t *testing.T) {
	e := newLeaseBLEnv(t, 1)
	cfg := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "slicc")
	stateLease := filepath.Join(cfg, "tray-join.json.lease")
	certsLease := filepath.Join(cfg, "tray-certs.json.lease")
	leaseBLLine(t, "R11-lease-after-first-attach", "state", leaseBLFileFacts(stateLease, e.join()), "certs", leaseBLFileFacts(certsLease, ""),
		"plain_state", leaseBLFileFacts(filepath.Join(cfg, "tray-join.json"), e.join()), "plain_certs", leaseBLFileFacts(filepath.Join(cfg, "tray-certs.json"), ""), "typed_join_len", len(e.join()))
	_ = e.grant(300)
	if v := e.action("goto", map[string]string{"url": e.site.srv.URL + "/fast?n=r5"}); v["type"] != "result" {
		t.Fatalf("setup goto: %v", v)
	}
	tab := e.leasedTab("n=r5")
	_ = e.request("otherHolder", 60)
	before := e.status()
	nonce := e.p.status().Nonce
	oldPid := e.follow.Process.Pid
	hubBefore := e.hubCount(e.trays[0].Token)
	k0 := time.Now()
	_ = e.follow.Process.Kill()
	<-e.done
	afterKill := e.status()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	sockets, _ := ctl.ListLocal(ctx)
	cancel()
	_, orphan := e.pageTargets()[tab]
	var down time.Time
	for end := time.Now().Add(40 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if !e.p.status().Up {
			down = time.Now()
			break
		}
	}
	e.start(nonce)
	restarted := time.Now()
	resume := e.connect("leaseTest")
	var pg string
	e.p.eval("otherHolder.tryGrant()", &pg)
	_, orphanAfter := e.pageTargets()[tab]
	leaseBLLine(t, "R5-restart", "old_follower_pid", oldPid, "new_follower_pid", e.follow.Process.Pid, "status_before_kill", before, "status_after_kill", afterKill,
		"ctl_sockets_after_kill", len(sockets), "orphan_tab_after_kill", orphan, "holder_link_down_after_kill_ms", leaseBLSince(k0, down), "relinked_after_kill_ms", leaseBLSince(k0, restarted),
		"old_jwt_on_new_broker", resume, "old_pending_holder_saw", pg, "status_after_restart", e.status(), "orphan_tab_after_restart", orphanAfter, "page_targets_after_restart", len(e.pageTargets()))
	leaseBLLine(t, "R11-lease-restart", "state", leaseBLFileFacts(stateLease, e.join()), "hub_requests_typed_before_restart", hubBefore, "hub_requests_typed_total", e.hubCount(e.trays[0].Token))
	if resume == "ready" {
		t.Fatal("a JWT from a killed broker validated on its successor")
	}
	_ = e.follow.Process.Kill()
	<-e.done
	nonce = e.p.status().Nonce
	cmd := exec.Command(binary, "-", "follow", "--json")
	cmd.Env = e.env
	cmd.Stdin = strings.NewReader(e.join() + "\n")
	log := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	linked := (&attachProc{cmd: cmd, stderr: log}).waitStderrOK("linked to", 30*time.Second)
	leaseBLLine(t, "R11-ordinary-control", "linked", linked, "state", leaseBLFileFacts(filepath.Join(cfg, "tray-join.json"), e.join()), "certs", leaseBLFileFacts(filepath.Join(cfg, "tray-certs.json"), ""),
		"lease_state_unchanged", leaseBLFileFacts(stateLease, e.join()))
	_ = nonce
}

func (a *attachProc) waitStderrOK(want string, timeout time.Duration) bool {
	for end := time.Now().Add(timeout); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if strings.Contains(a.stderr.String(), want) {
			return true
		}
	}
	return false
}

func TestSignedLeaseBaselinePeers(t *testing.T) {
	e := newLeaseBLEnv(t, 2)
	_ = e.grant(300)
	if v := e.action("goto", map[string]string{"url": e.site.srv.URL + "/fast?n=r6"}); v["type"] != "result" {
		t.Fatalf("setup goto: %v", v)
	}
	for _, how := range []string{"endData", "resetData"} {
		h0 := e.now()
		t0 := time.Now()
		e.p.eval("leaseTest."+how+"()", nil)
		end := e.collect(10 * time.Second)
		_, _, reset, resetT, _ := leaseBLFrames(e.frames(), h0)
		st := e.status()
		again := e.connect("leaseTest")
		snap := e.action("snapshot", map[string]any{})
		leaseBLLine(t, "R6-holder-"+how, "holder_saw", end.Type+":"+end.Code, "broker_reset", reset, "broker_reset_after_ms", fmt.Sprintf("%.1f", resetT-h0), "elapsed_ms", leaseBLSince(t0, time.Now()),
			"status_after_detach", st, "reconnect_same_jwt", again, "snapshot_after_reconnect", snap["type"])
	}
	nonce := e.p.status().Nonce
	t0 := time.Now()
	e.p.eval("linkTest.drop()", nil)
	afterDrop := e.status()
	e.p.waitUp(t, nonce)
	relinked := time.Now()
	again := e.connect("leaseTest")
	leaseBLLine(t, "R6-holder-link-drop", "status_after_drop", afterDrop, "relink_ms", leaseBLSince(t0, relinked), "reconnect_same_jwt", again, "status", e.status())
	docs := e.site.documents()
	tabs := len(e.pageTargets())
	var token string
	e.p.eval("leaseTest.token()", &token)
	e.p.eval("otherHolder.adopt("+jsArgs(token)+")", nil)
	cross := e.connect("otherHolder")
	var both []string
	e.p.eval("Promise.all([leaseTest.connect(), otherHolder.connect()])", &both)
	snap := e.action("snapshot", map[string]any{})
	leaseBLLine(t, "R7peer-cross-job-jkt", "other_jkt_with_first_jwt", cross, "simultaneous_valid_and_other", strings.Join(both, "+"), "valid_snapshot_after", snap["type"],
		"site_documents_delta", e.site.documents()-docs, "owner_tabs_delta", len(e.pageTargets())-tabs, "status", e.status())
	if cross == "ready" {
		t.Fatal("a holder with another key used the first job's lease")
	}
	cfg := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "slicc")
	stateLease := filepath.Join(cfg, "tray-join.json.lease")
	rotated := e.h.joinURL(e.trays[1])
	e.h.with(func() { e.trays[0].SupersededBy = rotated })
	nonce = e.p.status().Nonce
	t0 = time.Now()
	e.p.eval("linkTest.drop()", nil)
	e.p.waitUp(t, nonce)
	relinked = time.Now()
	rot := e.connect("leaseTest")
	snapRot := e.action("snapshot", map[string]any{})
	e.p.eval("otherHolder.adopt("+jsArgs(token)+")", nil)
	crossRot := e.connect("otherHolder")
	leaseBLLine(t, "R10-same-origin-rotation-live", "relink_ms", leaseBLSince(t0, relinked), "hub_requests_old_token", e.hubCount(e.trays[0].Token), "hub_requests_rotated_token", e.hubCount(e.trays[1].Token),
		"state", leaseBLFileFacts(stateLease, rotated), "pinned_holder_same_jwt", rot, "snapshot", snapRot["type"], "other_jkt_same_jwt_after_rotation", crossRot, "status", e.status())
	if crossRot == "ready" {
		t.Fatal("a token rotation let another holder key use the lease")
	}
	_, p2 := launchWithPage(t, pageServer(t).URL+"/index.html?lease")
	var pin2 string
	p2.eval("leaseFingerprint()", &pin2)
	var pin1 string
	e.p.eval("leaseFingerprint()", &pin1)
	l2 := newTrayLeader(t, e.h, p2, e.trays...)
	_ = l2
	logMark := len(e.log.String())
	nonce = e.p.status().Nonce
	dials0 := len(e.h.requestLog())
	t0 = time.Now()
	e.p.eval("linkTest.drop()", nil)
	mismatch := e.waitLog("paired fingerprint mismatch", logMark, 30*time.Second)
	time.Sleep(4 * time.Second)
	during := e.status()
	mismatches := strings.Count(e.log.String()[logMark:], "paired fingerprint mismatch")
	var p2status struct {
		Up, HelloReceived                                  bool
		InboundControlRecords, InboundControlBytes, Hellos int
		Signaling, Ice, Connection, LinkError, Nonce       string
	}
	p2.eval("linkTest.status()", &p2status)
	p2.eval("otherHolder.init()", nil)
	p2.eval("otherHolder.adopt("+jsArgs(token)+")", nil)
	var p2try string
	p2.evalWithin("Promise.race([otherHolder.connect().then(v=>String(v)), new Promise(r=>setTimeout(()=>r('timeout'),5000))]).catch(e=>String(e))", &p2try, 15*time.Second)
	p2.eval("linkTest.status()", &p2status)
	e.h.with(func() {
		for _, tr := range e.trays {
			tr.OnJoin = e.leader.onJoin
			tr.OnFollower = e.leader.onFollower
		}
	})
	e.p.waitUp(t, nonce)
	back := time.Now()
	valid := e.connect("leaseTest")
	sentBeforeCheck := "unmeasured"
	switch {
	case p2status.InboundControlRecords > 0 || p2status.HelloReceived:
		sentBeforeCheck = "yes"
	case mismatches > 0:
		sentBeforeCheck = "no"
	}
	leaseBLLine(t, "R6-dtls-mismatch-pre-pin", "code_pin_after_Ready", true, "code_sendHello_on_control_open", true, "code_Ready_is_peer_HELLO", true,
		"sent_before_the_check", sentBeforeCheck, "peer_inbound_control_records", p2status.InboundControlRecords, "peer_inbound_control_bytes", p2status.InboundControlBytes,
		"peer_hello_received", p2status.HelloReceived, "mismatch_logged_count", mismatches, "file_lines", "leasefollow.go:128-147,link.go:252,275-285,295-314")
	leaseBLLine(t, "R7peer-different-dtls-leaf", "leaf_differs", pin1 != pin2, "first_mismatch_after_ms", leaseBLSince(t0, mismatch), "mismatch_logged_count", mismatches, "hub_requests_during", len(e.h.requestLog())-dials0,
		"status_during", during, "other_page_link_up", p2status.Up, "other_page_connect_with_stolen_jwt", p2try, "valid_holder_relinked_after_ms", leaseBLSince(t0, back), "valid_holder_same_jwt", valid, "status_end", e.status(),
		"peer_inbound_control_records", p2status.InboundControlRecords, "peer_inbound_control_bytes", p2status.InboundControlBytes, "sent_before_the_check", sentBeforeCheck)
	if p2try == "ready" {
		t.Fatal("a peer with an unpinned DTLS leaf reached the broker")
	}
	h0 := e.now()
	t0 = time.Now()
	_ = e.follow.Process.Signal(os.Interrupt)
	end := e.collect(15 * time.Second)
	_, _, reset, resetT, _ := leaseBLFrames(e.frames(), h0)
	select {
	case <-e.done:
	case <-time.After(15 * time.Second):
	}
	exited := time.Now()
	leaseBLLine(t, "R6-owner-stops-follower", "holder_saw", end.Type+":"+end.Code, "holder_reset", reset, "holder_reset_after_ms", fmt.Sprintf("%.1f", resetT-h0), "follower_exit_after_ms", leaseBLSince(t0, exited),
		"owner_tabs_left", len(e.pageTargets()), "status_after", e.status())
}
