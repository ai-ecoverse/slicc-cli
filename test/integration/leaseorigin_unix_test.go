//go:build !windows

package integration

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

var (
	leaseOriginTray = protectOverlayRun{
		pkg:     "./internal/signal/tray",
		file:    "internal/signal/tray/leaseorigin_test.go",
		regress: "test/integration/testdata/leaseorigin/tray.go",
		extra:   map[string]string{"internal/signal/tray/leaseorigin_unix_test.go": "test/integration/testdata/leaseorigin/tray_unix.go"},
		run:     "^(TestLeaseFollowerStaysOnTheReviewedOrigin|TestOrdinaryFollowUnchanged|TestLeaseFollowerLatchOrdering)$",
		timeout: 5 * time.Minute,
		race:    true,
	}
	leaseOriginFollow = protectOverlayRun{
		pkg:     "./cmd/slicc",
		file:    "cmd/slicc/leaseorigin_test.go",
		regress: "test/integration/testdata/leaseorigin/follow.go",
		run:     "^(TestLeaseFollowerStaysOnTheReviewedOrigin|TestOrdinaryFollowUnchanged)$",
		timeout: 8 * time.Minute,
		race:    true,
	}
)

func TestLeaseFollowerOriginTables(t *testing.T) {
	for name, target := range map[string]protectOverlayRun{"Tray": leaseOriginTray, "Follow": leaseOriginFollow} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			output, err := protectOverlay(t, target, nil, nil)
			if err != nil || strings.Contains(output, "--- SKIP") {
				t.Fatalf("%s table: %v\n%s", name, err, output)
			}
		})
	}
}

type leaseOriginEdit struct{ from, to string }

func TestLeaseFollowerOriginCatchesMutants(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	const (
		follower = "internal/signal/tray/follower.go"
		session  = "internal/signal/tray/session.go"
		conn     = "internal/signal/tray/conn.go"
		client   = "internal/signal/tray/client.go"
		follow   = "cmd/slicc/follow.go"
		row      = "TestLeaseFollowerStaysOnTheReviewedOrigin/"
		ordinary = "TestOrdinaryFollowUnchanged/"
		latch    = "TestLeaseFollowerLatchOrdering/"
	)
	tr, fo := leaseOriginTray, leaseOriginFollow
	check := "\tif f.admit(next) != nil {\n\t\treturn f.refuse()\n\t}\n\tif stop != nil {"
	compare := "if err != nil || !sameOrigin(f.pin, u) || HubOrigin(next) != f.pinKey {"
	saveFirst := "\tif mustSave && f.cfg.State != nil {"
	saveAfter := "\tif !mustSave && f.cfg.State != nil && f.Refused() == nil {"
	for _, m := range []struct {
		name, file string
		target     protectOverlayRun
		edits      []leaseOriginEdit
		rows       []string
	}{
		{"ConfineRemoved", follower, tr, []leaseOriginEdit{{check, "\tif stop != nil {"}}, []string{row + "Location308Foreign", row + "PortOnly", row + "BootstrapSupersedeForeign", row + "AnswerSupersedeForeign"}},
		{"CheckAfterAdvance", follower, tr, []leaseOriginEdit{{check, "\tif stop != nil {"}, {"\t\tf.cfg.OnJoinURLChanged(next)\n\t}\n\treturn nil\n}", "\t\tf.cfg.OnJoinURLChanged(next)\n\t}\n\treturn f.admit(next)\n}"}}, []string{row + "Location308Foreign", row + "RejoinForeignRefused", row + "BootstrapSupersedeForeign", row + "AnswerSupersedeForeign"}},
		{"HostOnlyCompare", follower, tr, []leaseOriginEdit{{compare, "if err != nil || !strings.EqualFold(f.pin.Hostname(), u.Hostname()) {"}}, []string{row + "PortOnly"}},
		{"SchemeIgnored", follower, tr, []leaseOriginEdit{{compare, "if err != nil || !strings.EqualFold(f.pin.Hostname(), u.Hostname()) || effectivePort(f.pin) != effectivePort(u) {"}}, []string{row + "SchemeOnly", row + "HttpsToHttpSameHost"}},
		{"SingleComparator/SameOriginOnly", follower, tr, []leaseOriginEdit{{compare, "if err != nil || !sameOrigin(f.pin, u) {"}}, []string{row + "OriginSpellingsAgree", row + "RejoinForeignRefused"}},
		{"SingleComparator/HubOriginOnly", follower, tr, []leaseOriginEdit{{compare, "if _ = u; err != nil || HubOrigin(next) != f.pinKey {"}}, []string{row + "OriginSpellingsAgree"}},
		{"ZoneCaseFolded", follower, tr, []leaseOriginEdit{{"&& f.pin.Hostname() != u.Hostname() {", "&& !strings.EqualFold(f.pin.Hostname(), u.Hostname()) {"}}, []string{row + "ZoneIDSpelling", row + "RejoinForeignRefused"}},
		{"ExhaustionBeforeAdmit", follower, tr, []leaseOriginEdit{{"\tif f.admit(next) != nil {\n\t\treturn f.refuse()\n\t}\n\tif stop != nil {\n\t\treturn stop\n\t}\n", "\tif stop != nil {\n\t\treturn stop\n\t}\n\tif f.admit(next) != nil {\n\t\treturn f.refuse()\n\t}\n"}}, []string{row + "ForeignSixthHop"}},
		{"RestoredUsed", follower, tr, []leaseOriginEdit{{"if restored := cfg.State.Current(origin); f.admit(restored) == nil {", "if restored := cfg.State.Current(origin); restored != \"\" {"}}, []string{row + "RestoredForeignStateIgnored"}},
		{"PersistRefused", follower, tr, []leaseOriginEdit{{"func (f *Follower) advance(next string, mustSave bool, stop error) error {\n", "func (f *Follower) advance(next string, mustSave bool, stop error) error {\n\tif f.cfg.State != nil {\n\t\t_ = f.cfg.State.Save(f.origin, next)\n\t}\n"}}, []string{row + "Location308Foreign", row + "PortOnly"}},
		{"RejoinBypassesChoke", follower, tr, []leaseOriginEdit{{"\treturn f.advance(joinURL, true, nil)\n}", "\tf.mu.Lock()\n\tf.current = joinURL\n\tf.mu.Unlock()\n\tif f.cfg.OnJoinURLChanged != nil {\n\t\tf.cfg.OnJoinURLChanged(joinURL)\n\t}\n\treturn nil\n}"}}, []string{row + "RejoinForeignRefused"}},
		{"RejoinUntypedRefusal", follower, tr, []leaseOriginEdit{{"\t\tif f.admit(joinURL) != nil {\n\t\t\treturn f.refuse()\n\t\t}\n", ""}}, []string{row + "RejoinForeignRefused"}},
		{"SuccessorLoggedBeforeCheck/Dial", follower, tr, []leaseOriginEdit{{"\tif err := f.advance(next, false, stop); err != nil {\n\t\treturn err\n\t}\n\tf.logf(\"tray: superseded, following redirect %d/%d\", redirects+1, MaxRedirects)\n", "\tf.logf(\"tray: superseded, following redirect %d/%d\", redirects+1, MaxRedirects)\n\tif err := f.advance(next, false, stop); err != nil {\n\t\treturn err\n\t}\n"}}, []string{row + "Location308Foreign"}},
		{"SuccessorLoggedBeforeCheck/Bootstrap", session, tr, []leaseOriginEdit{{"\tif err := s.f.advance(next, false, nil); err != nil {\n\t\treturn err\n\t}\n\ts.f.logf(\"tray: superseded during the bootstrap\")\n", "\ts.f.logf(\"tray: superseded during the bootstrap\")\n\tif err := s.f.advance(next, false, nil); err != nil {\n\t\treturn err\n\t}\n"}}, []string{row + "BootstrapSupersedeForeign"}},
		{"OutboxFatalDropped/Session", session, tr, []leaseOriginEdit{{"\tif err := sleepCtx(ctx, s.done, s.f.wake, wait); err != nil {", "\tif err := sleepCtx(ctx, s.done, nil, wait); err != nil {"}}, []string{row + "RecvInRetrySleepGetsRefusal"}},
		{"OutboxFatalDropped/Settled", conn, tr, []leaseOriginEdit{{"\tcase <-c.s.f.wake:\n\t\treturn c.s.f.Refused()\n", ""}}, []string{row + "CandidateSupersedeForeign", row + "SettledRejoinWakes"}},
		{"DialReturnsSessionAfterRefusal", follower, tr, []leaseOriginEdit{{"\t\tif err := f.Refused(); err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t\tif resp.superseded {", "\t\tif resp.superseded {"}}, []string{row + "DialSignalAfterRefusal"}},
		{"RecvDeliversOfferAfterRefusal", session, tr, []leaseOriginEdit{{"\tif err := s.f.Refused(); err != nil {\n\t\treturn err\n\t}\n\tstatus := resp.Bootstrap", "\tstatus := resp.Bootstrap"}}, []string{row + "RecvOfferAfterRefusal"}},
		{"StickyRefusalRemoved", follower, tr, []leaseOriginEdit{{"\tf.mu.Lock()\n\tif f.refused.CompareAndSwap(nil, errForeignOrigin()) {\n\t\tclose(f.wake)\n\t}\n\tf.mu.Unlock()\n\treturn f.Refused()", "\treturn errForeignOrigin()"}}, []string{row + "StickyBlocksLaterAllowedMove", row + "CompetingNetworkErrorLoses", row + "AfterRefusalNoCandidateQueued"}},
		{"AdvanceIgnoresSticky", follower, tr, []leaseOriginEdit{{"\tif err := f.Refused(); err != nil {\n\t\treturn err\n\t}\n\tif f.admit(next) != nil {", "\tif f.admit(next) != nil {"}, {"\tf.mu.Lock()\n\tif err := f.Refused(); err != nil {\n\t\tf.mu.Unlock()\n\t\treturn err\n\t}\n\tf.current = next", "\tf.mu.Lock()\n\tf.current = next"}}, []string{row + "StickyBlocksLaterAllowedMove"}},
		{"RecvPrefersCompetingError", session, tr, []leaseOriginEdit{{"\t\treturn Event{}, s.f.refusedOr(err)\n", "\t\treturn Event{}, err\n"}}, []string{row + "RecvInRetrySleepGetsRefusal", row + "AnswerSupersedeForeign"}},
		{"CandidateQueuedAfterRefusal", session, tr, []leaseOriginEdit{{"\tif err := s.f.Refused(); err != nil {\n\t\ts.trickleMu.Unlock()\n\t\treturn err\n\t}\n", ""}}, []string{row + "AfterRefusalNoCandidateQueued", row + "CandidateRacingRefusedAnswer"}},
		{"SupervisorIgnoresRefused", follow, fo, []leaseOriginEdit{{"\t\tif refused := r.f.Refused(); refused != nil {\n\t\t\tcode, _ = r.failed(ctx, refused, false)\n\t\t\treturn code\n\t\t}\n", ""}}, []string{row + "SupervisorStopsOnRefusal/EvFailedFirst", row + "SupervisorStopsOnRefusal/EvDownFirst", row + "SupervisorStopsOnRefusal/EvDownAfterUp"}},
		{"AttachWaitIgnoresLatch", follower, tr, []leaseOriginEdit{{"\tfor {\n\t\tif err := f.Refused(); err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t\tresp, err := f.client.post(", "\tfor {\n\t\tresp, err := f.client.post("}, {"sleepCtx(ctx, nil, f.wake, wait)", "sleepCtx(ctx, nil, nil, wait)"}}, []string{row + "AttachWaitStopsOnLatch"}},
		{"ClientRetriesAfterRefusal", client, tr, []leaseOriginEdit{{"\t\tif err := c.halted(); err != nil {\n\t\t\treturn nil, err\n\t\t}\n", ""}}, []string{row + "CompetingNetworkErrorLoses"}},
		{"AdvancePublishesAfterRefusal", follower, tr, []leaseOriginEdit{{"\tf.mu.Lock()\n\tif err := f.Refused(); err != nil {\n\t\tf.mu.Unlock()\n\t\treturn err\n\t}\n\tf.current = next", "\tf.mu.Lock()\n\tf.current = next"}}, []string{latch + "RejoinSaveOverlapsRefusal"}},
		{"AdvanceCallsBackAfterRefusal", follower, tr, []leaseOriginEdit{{"if f.cfg.OnJoinURLChanged != nil && f.Refused() == nil {", "if f.cfg.OnJoinURLChanged != nil {"}}, []string{latch + "DialSaveOverlapsRefusal"}},
		{"RefusalDoesNotWake", follower, tr, []leaseOriginEdit{{"\tif f.refused.CompareAndSwap(nil, errForeignOrigin()) {\n\t\tclose(f.wake)\n\t}\n", "\tf.refused.CompareAndSwap(nil, errForeignOrigin())\n"}}, []string{row + "SettledRejoinWakes", row + "AttachWaitStopsOnLatch", row + "RecvInRetrySleepGetsRefusal"}},
		{"CandidateChecksBeforeLock", session, tr, []leaseOriginEdit{{"\ts.trickleMu.Lock()\n\tif err := s.f.Refused(); err != nil {\n\t\ts.trickleMu.Unlock()\n\t\treturn err\n\t}\n", "\tif err := s.f.Refused(); err != nil {\n\t\treturn err\n\t}\n\ts.trickleMu.Lock()\n"}}, []string{row + "CandidateRacingRefusedAnswer"}},
		{"BodyCarrierDisabled", client, tr, []leaseOriginEdit{{"\tif out.Result != nil && out.Result.Code == CodeSuperseded {", "\tif false {"}}, []string{row + "Manual409Foreign", row + "Manual409Same", row + "JSONSupersededForeign"}},
		{"OrdinaryAdvanceSaveFirst", follower, tr, []leaseOriginEdit{{saveFirst, "\tif f.cfg.State != nil {"}, {saveAfter, "\tif false {"}}, []string{ordinary + "DialPublishesBeforeSave"}},
		{"OrdinaryRejoinPublishFirst", follower, tr, []leaseOriginEdit{{saveFirst, "\tif false {"}, {saveAfter, "\tif f.cfg.State != nil {"}}, []string{ordinary + "RejoinSavesBeforePublish"}},
		{"OrdinaryPersistRemoved", follower, fo, []leaseOriginEdit{{saveAfter, "\tif false {"}}, []string{ordinary + "ForeignSuccessorStillFollowedAndRemembered", ordinary + "AgentLinkUnchanged"}},
		{"PinSetForGenericFollow", follow, fo, []leaseOriginEdit{{"\tif ff.leaseBrowser != \"\" {\n\t\tcfg.PinnedJoinURL = joinURL\n\t}", "\tcfg.PinnedJoinURL = joinURL"}}, []string{ordinary + "ForeignSuccessorStillFollowedAndRemembered"}},
		{"RefusalRetried", follow, fo, []leaseOriginEdit{{"\ttray.CodeSupersededForeignOrigin: \"the tray hub moved this link to another origin; it was not followed\",\n", ""}}, []string{row + "NoSuccessorURLInOutput"}},
	} {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			src := read(m.file)
			for _, e := range m.edits {
				if n := strings.Count(src, e.from); n != 1 {
					t.Fatalf("mutant %s anchor appears %d times in %s: %q", m.name, n, m.file, e.from)
				}
				src = strings.Replace(src, e.from, e.to, 1)
			}
			output, err := protectOverlay(t, m.target, nil, map[string]string{m.file: src})
			if err == nil {
				t.Fatalf("mutant %s passed every row\n%s", m.name, output)
			}
			for _, r := range m.rows {
				if !strings.Contains(output, "--- FAIL: "+r+" ") {
					t.Fatalf("mutant %s must fail %s\n%s", m.name, r, output)
				}
			}
		})
	}
	t.Run("AttachAfterRefusal", func(t *testing.T) {
		t.Parallel()
		files := map[string]string{}
		for file, e := range map[string]leaseOriginEdit{
			follower: {"\tfor {\n\t\tif err := f.Refused(); err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t\tresp, err := f.client.post(", "\tfor {\n\t\tresp, err := f.client.post("},
			client:   {"\t\tif err := c.halted(); err != nil {\n\t\t\treturn nil, err\n\t\t}\n", ""},
		} {
			src := read(file)
			if n := strings.Count(src, e.from); n != 1 {
				t.Fatalf("mutant AttachAfterRefusal anchor appears %d times in %s", n, file)
			}
			files[file] = strings.Replace(src, e.from, e.to, 1)
		}
		output, err := protectOverlay(t, fo, nil, files)
		if r := row + "SupervisorStopsOnRefusal/ServeNilFirst"; err == nil || !strings.Contains(output, "--- FAIL: "+r+" ") {
			t.Fatalf("mutant AttachAfterRefusal must fail %s\n%s", r, output)
		}
	})
}

func TestLeaseFollowerStaysOnTheReviewedOrigin(t *testing.T) {
	t.Run("RefusalIsTerminal", func(t *testing.T) {
		fx := newColdFixture(t, false)
		fx.endpoint = launchBrowser(t).endpoint
		plain, leased := joinStatePaths(t)
		foreign, foreignConns := countedHub(t)
		far := foreign.addTray("far")
		successor := foreign.joinURL(far)
		fx.hub.with(func() { fx.tray.SupersededBy = successor })
		cmd := startLeaseFollower(t, fx, fx.hub.joinURL(fx.tray))
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		var err error
		select {
		case err = <-exited:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatalf("the lease-only follower kept running after the refusal: %v", <-exited)
		}
		atExit := len(attachRequests(fx.hub, fx.tray.Token))
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || atExit != 1 {
			t.Fatalf("exit %v with %d reviewed attaches", err, atExit)
		}
		time.Sleep(3 * time.Second)
		log := cmd.Stderr.(*syncBuffer).String()
		if n := len(attachRequests(fx.hub, fx.tray.Token)); n != 1 || foreignConns.Load() != 0 || len(foreign.requestLog()) != 0 {
			t.Fatalf("reviewed attaches=%d foreign conns=%d\n%s", n, foreignConns.Load(), log)
		}
		if !strings.Contains(log, "TRAY_SUPERSEDED_FOREIGN_ORIGIN") {
			t.Fatalf("missing the refusal code:\n%s", log)
		}
		for _, secret := range []string{successor, far.Token, strings.TrimPrefix(foreign.srv.URL, "http://")} {
			if strings.Contains(log, secret) {
				t.Fatalf("the output names the refused successor:\n%s", log)
			}
		}
		mustAbsent(t, leased, "lease join state")
		mustAbsent(t, plain, "ordinary join state")
	})
}

func TestOrdinaryFollowUnchanged(t *testing.T) {
	t.Run("BinaryFollowsForeignSuccessor", func(t *testing.T) {
		fx := newColdFixture(t, false)
		plain, leased := joinStatePaths(t)
		foreign, _ := countedHub(t)
		far := foreign.addTray("far")
		foreign.with(func() { far.Expired = true })
		successor := foreign.joinURL(far)
		fx.hub.with(func() { fx.tray.SupersededBy = successor })
		typed := fx.hub.joinURL(fx.tray)
		cmd := exec.Command(binary, "-", "follow")
		cmd.Env = fx.env
		cmd.Stdin = strings.NewReader(typed + "\n")
		log := &syncBuffer{}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if t.Failed() {
				t.Log(log.String())
			}
		})
		waitAttach(t, foreign, far.Token)
		for deadline := time.Now().Add(30 * time.Second); tray.OpenState(plain).Current(typed) != successor; time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("ordinary follow did not remember the foreign successor")
			}
		}
		mustAbsent(t, leased, "lease join state")
	})
}
