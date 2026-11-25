package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func hsBuildSwitchesBinary(t *testing.T, mutantFile, version string) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("step=mutant-root: %v", err)
	}
	dir := t.TempDir()
	name := "slicc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(dir, name)
	replacePath := filepath.Join(dir, "replace.json")
	replacements := map[string]string{
		filepath.Join(root, "internal/caps/cdp/history_setup_switches.go"): filepath.Join(root, "test/integration/testdata/historysetup", mutantFile),
	}
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatalf("step=mutant-replace-marshal: %v", err)
	}
	if err := os.WriteFile(replacePath, data, 0600); err != nil {
		t.Fatalf("step=mutant-replace-write: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	buildReplace := "-" + "overlay"
	cmd := exec.CommandContext(ctx, "go", "build", buildReplace, replacePath, "-ldflags", "-X main.version="+version, "-o", out, "./cmd/slicc")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("step=mutant-build: %v\n%s", err, output)
	}
	t.Logf("step=mutant-build ok path=%s mutant=%s", out, mutantFile)
	return out
}

func hsBuildExemptionRemovedBinary(t *testing.T) string {
	t.Helper()
	return hsBuildSwitchesBinary(t, "mutation_exemption_removed.go", "1.2.3-test-setup-exemption-removed")
}

func hsBuildLatchRemovedBinary(t *testing.T) string {
	t.Helper()
	return hsBuildSwitchesBinary(t, "mutation_latch_removed.go", "1.2.3-test-setup-latch-removed")
}

type hsChromeSetupProbe struct {
	Steps []struct {
		Name  string `json:"name"`
		At    int64  `json:"at"`
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	} `json:"steps"`
	BypassOK              bool   `json:"bypassOk"`
	BypassError           string `json:"bypassError"`
	AutoAttachOK          bool   `json:"autoAttachOk"`
	AutoAttachError       string `json:"autoAttachError"`
	EvaluateRefused       bool   `json:"evaluateRefused"`
	GetDocumentOK         bool   `json:"getDocumentOk"`
	GetDocumentError      string `json:"getDocumentError"`
	GetDocumentReplyClean bool   `json:"getDocumentReplyClean"`
	DisableOK             bool   `json:"disableOk"`
	DisableError          string `json:"disableError"`
	WrongDepthRefused     bool   `json:"wrongDepthRefused"`
	ElapsedMs             int64  `json:"elapsedMs"`
}

type hsChromeAutoChildProbe struct {
	Steps []struct {
		Name  string `json:"name"`
		At    int64  `json:"at"`
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	} `json:"steps"`
	ChildID      string `json:"childId"`
	TargetEvents []struct {
		At                 int64  `json:"at"`
		Method             string `json:"method"`
		SessionPrefix      string `json:"sessionPrefix"`
		ChildSessionPrefix string `json:"childSessionPrefix"`
		TargetPrefix       string `json:"targetPrefix"`
		Type               string `json:"type"`
	} `json:"targetEvents"`
	AutoAttach struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		SessionID   string `json:"sessionId"`
		DeliveredAt int64  `json:"deliveredAt"`
	} `json:"autoAttach"`
	ChildEvaluate struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Value string `json:"value"`
	} `json:"childEvaluate"`
	ElapsedMs int64 `json:"elapsedMs"`
}

func historySetupChromeLockedOrdinaryAuto(t *testing.T) (*hrChromeLockedOrdinaryResult, hsChromeSetupProbe, hsChromeAutoChildProbe) {
	t.Helper()
	historyRestoreRequireChrome(t)
	fixture := historyRestoreChromeFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	seven := "http://seven.protected.test:" + port
	ordinary := "http://page.ordinary.test:" + port
	frameHost := "http://frame.other.test:" + port
	b := launchBrowserOn(t, 0,
		"--site-per-process",
		"--enable-features=BackForwardCache",
		"--disable-features=BackForwardCacheMemoryControls",
		"--unsafely-treat-insecure-origin-as-secure="+seven,
		"--host-resolver-rules=MAP *.test 127.0.0.1",
	)
	step := historyRestoreChromeStep(t)
	host, err := b.tryOpenPage(t, seven+"/seven-host")
	step("open-protected-host", err)
	linkHost, err := b.tryOpenPage(t, seven+"/seven-host")
	step("open-seven-link-host", err)
	env := &linkEnv{t: t, b: b, origin: seven, p: linkHost}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", seven, "--name", "history-setup-chrome-locked-ordinary-auto")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step=linkTest.ready: browsers count=%d", len(hello.Browsers))
	}
	browser := hello.Browsers[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	step("page-enable-host-unguarded", host.session.Call(ctx, "Page.enable", nil, nil))
	step("runtime-enable-host-unguarded", host.session.Call(ctx, "Runtime.enable", nil, nil))
	step("navigate-ordinary-unguarded", host.session.Call(ctx, "Page.navigate", map[string]any{"url": ordinary + "/ordinary"}, nil))
	loadDeadline := time.Now().Add(15 * time.Second)
	var hrefOut struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	for time.Now().Before(loadDeadline) {
		if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut); err == nil && strings.HasPrefix(hrefOut.Result.Value, ordinary) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut); err != nil || !strings.HasPrefix(hrefOut.Result.Value, ordinary) {
		t.Fatalf("step=unguarded-confirm-ordinary-href: href=%q err=%v", hrefOut.Result.Value, err)
	}
	t.Logf("step=unguarded-confirm-ordinary-href ok")

	baseOpts := map[string]any{
		"browser":       browser,
		"framePrefix":   frameHost,
		"lockSettleMs":  800,
		"findWaitMs":    15000,
		"attachWaitMs":  20000,
		"callTimeoutMs": 60000,
	}
	phaseAt := time.Now()
	cap := newHrChromeCapture(phaseAt)
	stopWatch := make(chan struct{})
	var watchWG sync.WaitGroup
	watchWG.Add(1)
	go func() {
		defer watchWG.Done()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWatch:
				return
			case <-ticker.C:
				cap.noteGuardLines(guardLines(env.a))
			}
		}
	}()

	logSteps := func(steps []struct {
		Name  string `json:"name"`
		At    int64  `json:"at"`
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}) {
		for _, s := range steps {
			if s.OK {
				t.Logf("step=%s ok at+%dms", s.Name, s.At)
			} else {
				t.Logf("step=%s refused at+%dms err=%q", s.Name, s.At, s.Error)
			}
		}
	}

	t.Logf("step=guarded-explicit-open hostTargetPrefix=%s", historyRestoreIDPrefix(host.target))
	var openRaw rawJSON
	env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitOpen("+jsArgs(map[string]any{
		"browser": browser, "hostTarget": host.target, "framePrefix": frameHost, "lockSettleMs": 800, "callTimeoutMs": 60000,
	})+")", &openRaw, 60*time.Second)
	var open struct {
		Steps []struct {
			Name  string `json:"name"`
			At    int64  `json:"at"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"steps"`
		HostAttached    bool     `json:"hostAttached"`
		HostAttachError string   `json:"hostAttachError"`
		BeforeIframes   []string `json:"beforeIframes"`
	}
	if err := json.Unmarshal(openRaw, &open); err != nil {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=guarded-explicit-open-parse: %v raw=%s", err, openRaw)
	}
	logSteps(open.Steps)
	if open.HostAttachError != "" {
		t.Logf("step=guarded-attach-host: refused after history-lock err=%q; falling back to pre-seven attach", open.HostAttachError)
		preHost, err := b.tryOpenPage(t, ordinary+"/ordinary")
		step("open-ordinary-pre-seven", err)
		host = preHost
		env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitOpen("+jsArgs(map[string]any{
			"browser": browser, "hostTarget": host.target, "framePrefix": frameHost, "lockSettleMs": 800, "callTimeoutMs": 60000,
		})+")", &openRaw, 60*time.Second)
		if err := json.Unmarshal(openRaw, &open); err != nil {
			close(stopWatch)
			watchWG.Wait()
			t.Fatalf("step=guarded-explicit-open-parse-retry: %v", err)
		}
		logSteps(open.Steps)
		if open.HostAttachError != "" {
			close(stopWatch)
			watchWG.Wait()
			t.Fatalf("step=guarded-attach-host: refused even on ordinary before seven err=%q", open.HostAttachError)
		}
		step("navigate-seven-after-pre-attach", host.session.Call(ctx, "Page.navigate", map[string]any{"url": seven + "/seven-host"}, nil))
		time.Sleep(500 * time.Millisecond)
		step("navigate-ordinary-after-seven", host.session.Call(ctx, "Page.navigate", map[string]any{"url": ordinary + "/ordinary"}, nil))
	}
	if !open.HostAttached && open.HostAttachError == "" {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=guarded-explicit-open: open did not attach host")
	}

	loadDeadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(loadDeadline) {
		if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut); err == nil && strings.HasPrefix(hrefOut.Result.Value, ordinary) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	var hist struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID  int    `json:"id"`
			URL string `json:"url"`
		} `json:"entries"`
	}
	if err := host.session.Call(ctx, "Page.getNavigationHistory", nil, &hist); err != nil {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=unguarded-getNavigationHistory: %v", err)
	}
	sevenEntry := 0
	for _, e := range hist.Entries {
		if strings.HasPrefix(e.URL, seven) {
			sevenEntry = e.ID
			break
		}
	}
	if sevenEntry == 0 {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=find-protected-history-entry: seven entry missing")
	}
	t.Logf("step=find-protected-history-entry id=%d", sevenEntry)

	var lockRaw rawJSON
	env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitLockProbe("+jsArgs(baseOpts)+")", &lockRaw, 30*time.Second)
	var lock struct {
		Steps []struct {
			Name  string `json:"name"`
			At    int64  `json:"at"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(lockRaw, &lock); err != nil {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=guarded-lock-engaged-parse: %v", err)
	}
	logSteps(lock.Steps)
	lockOK := false
	var lockErr string
	for _, s := range lock.Steps {
		if s.Name == "guarded-lock-engaged" {
			lockOK = s.OK
			lockErr = s.Error
			break
		}
	}
	if !lockOK {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=guarded-lock-engaged: want history-refused Runtime.evaluate; got ok=%t err=%q", lockOK, lockErr)
	}
	t.Logf("step=guarded-lock-engaged ok refused err=%q", lockErr)

	protectedExpr := fmt.Sprintf(`(() => { const f = document.createElement('iframe'); f.src = %q; document.body.appendChild(f); return true; })()`, seven+"/seven-host")
	var protectedInject struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": protectedExpr, "returnByValue": true}, &protectedInject); err != nil || !protectedInject.Result.Value {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=unguarded-inject-protected-iframe: err=%v value=%t", err, protectedInject.Result.Value)
	}
	t.Logf("step=unguarded-inject-protected-iframe ok")
	time.Sleep(400 * time.Millisecond)

	setupOpts := map[string]any{}
	for k, v := range baseOpts {
		setupOpts[k] = v
	}
	setupOpts["protectedPrefix"] = seven
	var setupRaw rawJSON
	env.p.evalWithin("linkTest.historySetupLockedOrdinarySetup("+jsArgs(setupOpts)+")", &setupRaw, 30*time.Second)
	var setup hsChromeSetupProbe
	if err := json.Unmarshal(setupRaw, &setup); err != nil {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=guarded-setup-parse: %v raw=%s", err, setupRaw)
	}
	logSteps(setup.Steps)

	expr := fmt.Sprintf(`(() => { const f = document.createElement('iframe'); f.src = %q; document.body.appendChild(f); return true; })()`, frameHost+"/inner")
	var injectOut struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &injectOut); err != nil || !injectOut.Result.Value {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=unguarded-inject-iframe: err=%v value=%t", err, injectOut.Result.Value)
	}
	t.Logf("step=unguarded-inject-iframe ok")

	before := append([]string(nil), open.BeforeIframes...)
	var childRaw rawJSON
	env.p.evalWithin("linkTest.historySetupLockedOrdinaryAutoChild("+jsArgs(map[string]any{
		"browser": browser, "framePrefix": frameHost, "beforeIframes": before, "findWaitMs": 15000, "attachWaitMs": 20000,
	})+")", &childRaw, 90*time.Second)
	close(stopWatch)
	watchWG.Wait()
	var child hsChromeAutoChildProbe
	if err := json.Unmarshal(childRaw, &child); err != nil {
		t.Fatalf("step=guarded-auto-child-parse: %v raw=%s", err, childRaw)
	}
	logSteps(child.Steps)
	for _, ev := range child.TargetEvents {
		t.Logf("target-event +%dms %s sess=%s child=%s target=%s type=%s", ev.At, ev.Method, ev.SessionPrefix, ev.ChildSessionPrefix, ev.TargetPrefix, ev.Type)
	}

	neutral := historyRestoreNeutralGuardLines(guardLines(env.a))
	stats := historyRestoreParseHoldStats(neutral)
	ordered := cap.snapshot()
	var holdAtMs, deliveredAtMs int64 = -1, -1
	for _, ev := range ordered {
		if ev.src != "guard" {
			continue
		}
		if holdAtMs < 0 && strings.Contains(ev.method, "holding a child of a history-locked page") {
			holdAtMs = ev.ms
		}
		if deliveredAtMs < 0 && strings.Contains(ev.method, "history hold delivered") {
			deliveredAtMs = ev.ms
		}
	}
	if holdAtMs >= 0 && deliveredAtMs >= holdAtMs {
		stats.holdMs = deliveredAtMs - holdAtMs
	}
	t.Logf("neutral guard line count after auto-attach setup: %d", len(neutral))
	for _, line := range neutral {
		t.Logf("guard: %s", line)
	}
	for _, row := range stats.readsPerHold {
		t.Logf("reads per held child: %s", row)
	}
	t.Logf("held=%d holdMs=%d deliveredAttachSeq=%d deliveredReadSeq=%d secondReads=%d deadlineHolds=%d", stats.held, stats.holdMs, stats.deliveredAttach, stats.deliveredRead, stats.secondReads, stats.deadlineHolds)
	historyRestoreLogOrdered(t, ordered)

	return &hrChromeLockedOrdinaryResult{
		stats:         stats,
		ordered:       ordered,
		childID:       child.ChildID,
		childEvalOK:   child.ChildEvaluate.OK,
		childEvalVal:  child.ChildEvaluate.Value,
		sevenEntry:    sevenEntry,
		host:          host,
		env:           env,
		b:             b,
		browser:       browser,
		frameHost:     frameHost,
		seven:         seven,
		ordinary:      ordinary,
		ctx:           ctx,
		holdAtMs:      holdAtMs,
		deliveredAtMs: deliveredAtMs,
	}, setup, child
}

func TestCDPHistorySetupChromeLockedOrdinaryAutoAttachHold(t *testing.T) {
	r, setup, child := historySetupChromeLockedOrdinaryAuto(t)
	for _, s := range setup.Steps {
		switch s.Name {
		case "guarded-setBypassServiceWorker", "guarded-setAutoAttach", "guarded-evaluate-history-refused", "guarded-getDocument-admitted", "guarded-getDocument-reply-clean", "guarded-dom-disable", "guarded-getDocument-wrong-depth-refused":
			if !s.OK {
				t.Fatalf("step=%s: %s", s.Name, s.Error)
			}
		}
	}
	if !setup.BypassOK {
		t.Fatalf("step=guarded-setBypassServiceWorker: refused err=%q", setup.BypassError)
	}
	t.Logf("step=guarded-setBypassServiceWorker ok")
	if !setup.AutoAttachOK {
		t.Fatalf("step=guarded-setAutoAttach: refused err=%q", setup.AutoAttachError)
	}
	t.Logf("step=guarded-setAutoAttach ok")
	if !setup.EvaluateRefused {
		t.Fatalf("step=content-stays-refused: evaluateRefused=%t", setup.EvaluateRefused)
	}
	if !setup.GetDocumentOK {
		t.Fatalf("step=guarded-getDocument-admitted: err=%q", setup.GetDocumentError)
	}
	if !setup.GetDocumentReplyClean {
		t.Fatalf("step=guarded-getDocument-reply-clean: probe reply carried a protected document")
	}
	if !setup.DisableOK {
		t.Fatalf("step=guarded-dom-disable: err=%q", setup.DisableError)
	}
	if !setup.WrongDepthRefused {
		t.Fatalf("step=guarded-getDocument-wrong-depth-refused: depth 0 was admitted")
	}
	t.Logf("step=bios-probe-sequence ok getDocument=%t replyClean=%t disable=%t evaluateRefused=%t wrongDepthRefused=%t", setup.GetDocumentOK, setup.GetDocumentReplyClean, setup.DisableOK, setup.EvaluateRefused, setup.WrongDepthRefused)

	for _, s := range child.Steps {
		switch s.Name {
		case "guarded-discover-iframe", "guarded-auto-attach-iframe", "guarded-child-evaluate":
			if !s.OK {
				t.Fatalf("step=%s: %s", s.Name, s.Error)
			}
		}
	}
	if child.ChildID == "" {
		t.Fatalf("step=guarded-discover-iframe: no child id")
	}
	if !child.AutoAttach.OK {
		t.Fatalf("step=guarded-auto-attach-iframe: %s", child.AutoAttach.Error)
	}
	if r.stats.held < 1 {
		t.Fatalf("step=held-child: held=%d want>=1 (auto-attach hold) — STOP: hidden/refused legitimate child", r.stats.held)
	}
	if r.stats.deliveredAttach == 0 {
		t.Fatalf("step=history-hold-delivered: want delivered after confirm; held=%d deadlineHolds=%d — STOP: hidden/refused legitimate child", r.stats.held, r.stats.deadlineHolds)
	}
	if r.stats.deliveredAttach > r.stats.deliveredRead {
		t.Fatalf("step=confirm-after-attach: attachSeq=%d readSeq=%d (want attachSeq<=readSeq)", r.stats.deliveredAttach, r.stats.deliveredRead)
	}
	if !child.ChildEvaluate.OK {
		t.Fatalf("step=guarded-child-evaluate: %s", child.ChildEvaluate.Error)
	}
	t.Logf("step=held-child ok held=%d attachSeq=%d readSeq=%d holdMs=%d secondReads=%d deadlineHolds=%d childEvalLen=%d", r.stats.held, r.stats.deliveredAttach, r.stats.deliveredRead, r.stats.holdMs, r.stats.secondReads, r.stats.deadlineHolds, len(child.ChildEvaluate.Value))

	t.Run("latch-refuses-after-blocked-protected-start", func(t *testing.T) {
		historySetupChromeLatchBlockedStart(t, r)
	})
	t.Run("bfcache-restore-refuses-setup", func(t *testing.T) {
		historySetupChromeRestoreRefusesSetup(t, r)
	})
}

func historySetupChromeProbeSetup(t *testing.T, r *hrChromeLockedOrdinaryResult) hsChromeSetupProbe {
	t.Helper()
	var setupRaw rawJSON
	r.env.p.evalWithin("linkTest.historySetupLockedOrdinarySetup("+jsArgs(map[string]any{
		"browser": r.browser, "framePrefix": r.frameHost, "lockSettleMs": 200, "callTimeoutMs": 15000,
	})+")", &setupRaw, 30*time.Second)
	var setup hsChromeSetupProbe
	if err := json.Unmarshal(setupRaw, &setup); err != nil {
		t.Fatalf("step=setup-parse: %v raw=%s", err, setupRaw)
	}
	for _, s := range setup.Steps {
		if s.OK {
			t.Logf("step=%s ok at+%dms", s.Name, s.At)
		} else {
			t.Logf("step=%s refused at+%dms err=%q", s.Name, s.At, s.Error)
		}
	}
	return setup
}

func historySetupChromeHrefClass(href, seven, ordinary string) string {
	switch {
	case strings.HasPrefix(href, seven):
		return "protected"
	case strings.HasPrefix(href, ordinary):
		return "ordinary"
	case strings.HasPrefix(href, "chrome-error:"):
		return "chrome-error"
	default:
		return "other"
	}
}

func historySetupChromeRequireHistoryText(t *testing.T, setup hsChromeSetupProbe) {
	t.Helper()
	if setup.BypassOK {
		t.Fatalf("step=setBypassServiceWorker: want HISTORY refusal; got ok")
	}
	if setup.AutoAttachOK {
		t.Fatalf("step=setAutoAttach: want HISTORY refusal; got ok")
	}
	for _, errText := range []string{setup.BypassError, setup.AutoAttachError} {
		if !strings.Contains(errText, "history may restore a protected document") {
			t.Fatalf("step=setup-refused: want HISTORY text; got %q", errText)
		}
		t.Logf("step=setup-refusal-text err=%q", errText)
	}
}

func historySetupChromeWaitConfirmSettled(t *testing.T, r *hrChromeLockedOrdinaryResult) {
	t.Helper()
	var waitRaw rawJSON
	r.env.p.evalWithin("linkTest.historySetupWaitConfirmSettled("+jsArgs(map[string]any{})+")", &waitRaw, 30*time.Second)
	var wait struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Steps []struct {
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
			At    int64  `json:"at"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(waitRaw, &wait); err != nil {
		t.Fatalf("step=guarded-wait-confirm-settled: parse: %v raw=%s", err, waitRaw)
	}
	for _, s := range wait.Steps {
		if s.OK {
			t.Logf("step=%s ok at+%dms", s.Name, s.At)
		} else {
			t.Logf("step=%s refused at+%dms err=%q", s.Name, s.At, s.Error)
		}
	}
	if !wait.OK {
		t.Fatalf("step=guarded-wait-confirm-settled: want Page.getNavigationHistory after confirm; err=%q", wait.Error)
	}
	t.Logf("step=guarded-wait-confirm-settled ok")
}

func historySetupChromeNavigateOrdinaryAfterBlocked(t *testing.T, r *hrChromeLockedOrdinaryResult) {
	t.Helper()
	historySetupChromeWaitConfirmSettled(t, r)
	step := historyRestoreChromeStep(t)
	ordURL := r.ordinary + "/ordinary"
	t.Logf("step=unguarded-navigate-ordinary-after-blocked urlLen=%d", len(ordURL))
	step("Page.navigate", r.host.session.Call(r.ctx, "Page.navigate", map[string]any{"url": ordURL}, nil))
	deadline := time.Now().Add(15 * time.Second)
	var hrefOut struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	for time.Now().Before(deadline) {
		_ = r.host.session.Call(r.ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut)
		if strings.HasPrefix(hrefOut.Result.Value, r.ordinary) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	class := historySetupChromeHrefClass(hrefOut.Result.Value, r.seven, r.ordinary)
	t.Logf("step=unguarded-ordinary-after-blocked hrefClass=%s hrefLen=%d", class, len(hrefOut.Result.Value))
	if !strings.HasPrefix(hrefOut.Result.Value, r.ordinary) {
		t.Fatalf("step=unguarded-ordinary-after-blocked: want ordinary href; hrefClass=%s href=%q", class, hrefOut.Result.Value)
	}
}

func historySetupChromeLatchBlockedStart(t *testing.T, r *hrChromeLockedOrdinaryResult) {
	t.Helper()
	var beforeHref struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	_ = r.host.session.Call(r.ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &beforeHref)
	if strings.HasPrefix(beforeHref.Result.Value, r.seven) {
		t.Fatalf("step=latch-blocked-start: tab already on protected href=%q", beforeHref.Result.Value)
	}
	step := historyRestoreChromeStep(t)
	navURL := r.seven + "/blocked-start"
	t.Logf("step=unguarded-navigate-protected urlLen=%d", len(navURL))
	step("Page.navigate", r.host.session.Call(r.ctx, "Page.navigate", map[string]any{"url": navURL}, nil))
	deadline := time.Now().Add(3 * time.Second)
	var hrefOut struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	blocked := false
	for time.Now().Before(deadline) {
		_ = r.host.session.Call(r.ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut)
		if !strings.HasPrefix(hrefOut.Result.Value, r.seven) {
			blocked = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !blocked {
		t.Skipf("step=latch-blocked-start: skipped — Chrome committed the protected navigation; href=%q", hrefOut.Result.Value)
	}
	t.Logf("step=unguarded-blocked-protected-start ok hrefClass=%s hrefLen=%d", historySetupChromeHrefClass(hrefOut.Result.Value, r.seven, r.ordinary), len(hrefOut.Result.Value))
	historySetupChromeNavigateOrdinaryAfterBlocked(t, r)
	setup := historySetupChromeProbeSetup(t, r)
	historySetupChromeRequireHistoryText(t, setup)
	t.Logf("step=latch-refuses-after-blocked-protected-start ok")
}

func historySetupChromeRestoreRefusesSetup(t *testing.T, r *hrChromeLockedOrdinaryResult) {
	t.Helper()
	if r.sevenEntry == 0 {
		t.Skip("step=bfcache-restore: skipped — no protected history entry id")
	}
	step := historyRestoreChromeStep(t)
	t.Logf("step=restore-navigateToHistoryEntry entryId=%d", r.sevenEntry)
	step("navigateToHistoryEntry", r.host.session.Call(r.ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": r.sevenEntry}, nil))
	deadline := time.Now().Add(5 * time.Second)
	var hrefOut struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	for time.Now().Before(deadline) {
		_ = r.host.session.Call(r.ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut)
		if strings.HasPrefix(hrefOut.Result.Value, r.seven) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.HasPrefix(hrefOut.Result.Value, r.seven) {
		t.Skipf("step=bfcache-restore: skipped — Chrome did not produce a protected commit/restore; href=%q", hrefOut.Result.Value)
	}
	t.Logf("step=unguarded-bfcache-restore-protected ok hrefLen=%d", len(hrefOut.Result.Value))
	setup := historySetupChromeProbeSetup(t, r)
	if setup.BypassOK || setup.AutoAttachOK {
		t.Fatalf("step=bfcache-restore-setup: want refused; bypassOK=%t autoOK=%t", setup.BypassOK, setup.AutoAttachOK)
	}
	t.Logf("step=bfcache-restore-setup refused bypass=%q auto=%q", setup.BypassError, setup.AutoAttachError)
	if st := hrExplicitClientClose(r.env); st != "" {
		t.Logf("step=guarded-client-close: %s", st)
	}
}

func hrExplicitClientClose(env *linkEnv) string {
	if env == nil || env.p == nil {
		return "no env"
	}
	var raw rawJSON
	env.p.evalWithin(`(async () => { const st = globalThis.__hrExplicit; if (!st?.client) return 'already-closed'; await st.client.close().catch(() => {}); st.client = null; return 'closed'; })()`, &raw, 15*time.Second)
	return strings.Trim(string(raw), `"`)
}

func TestCDPHistorySetupChromeLockedOrdinaryAutoAttachHoldMutant(t *testing.T) {
	historyRestoreRequireChrome(t)
	mutant := hsBuildExemptionRemovedBinary(t)
	t.Setenv("SLICC_TEST_ATTACH_BINARY", mutant)
	setup := historySetupChromeLockedOrdinarySetupOnly(t)
	if setup.AutoAttachOK {
		t.Fatalf("step=mutant-setAutoAttach: exemption-removed binary must refuse setAutoAttach with history text; got ok")
	}
	if !strings.Contains(setup.AutoAttachError, "history may restore a protected document") {
		t.Fatalf("step=mutant-setAutoAttach: want history refusal; got %q", setup.AutoAttachError)
	}
	t.Logf("step=mutant-setAutoAttach ok refused err=%q", setup.AutoAttachError)
	if setup.BypassOK {
		t.Fatalf("step=mutant-setBypassServiceWorker: exemption-removed binary must refuse bypass; got ok")
	}
	t.Logf("step=mutant-held-proof ok: tip admission would fail; bypass and setAutoAttach history-refused")
}

func TestCDPHistorySetupChromeLatchRemovedAdmitsAfterBlockedStart(t *testing.T) {
	historyRestoreRequireChrome(t)
	mutant := hsBuildLatchRemovedBinary(t)
	t.Setenv("SLICC_TEST_ATTACH_BINARY", mutant)
	r, setup, _ := historySetupChromeLockedOrdinaryAuto(t)
	if !setup.BypassOK || !setup.AutoAttachOK {
		t.Fatalf("step=latch-mutant-baseline: want admitted setup on locked ordinary; bypassOK=%t autoOK=%t", setup.BypassOK, setup.AutoAttachOK)
	}
	var beforeHref struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	_ = r.host.session.Call(r.ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &beforeHref)
	step := historyRestoreChromeStep(t)
	step("Page.navigate", r.host.session.Call(r.ctx, "Page.navigate", map[string]any{"url": r.seven + "/blocked-start"}, nil))
	deadline := time.Now().Add(3 * time.Second)
	var hrefOut struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	blocked := false
	for time.Now().Before(deadline) {
		_ = r.host.session.Call(r.ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut)
		if !strings.HasPrefix(hrefOut.Result.Value, r.seven) {
			blocked = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !blocked {
		t.Skipf("step=latch-mutant: skipped — Chrome committed the protected navigation; href=%q", hrefOut.Result.Value)
	}
	t.Logf("step=latch-mutant-blocked hrefClass=%s hrefLen=%d", historySetupChromeHrefClass(hrefOut.Result.Value, r.seven, r.ordinary), len(hrefOut.Result.Value))
	historySetupChromeNavigateOrdinaryAfterBlocked(t, r)
	after := historySetupChromeProbeSetup(t, r)
	if !after.BypassOK || !after.AutoAttachOK {
		t.Fatalf("step=latch-mutant-after-ordinary: latch-removed binary must admit; bypassOK=%t err=%q autoOK=%t err=%q", after.BypassOK, after.BypassError, after.AutoAttachOK, after.AutoAttachError)
	}
	t.Logf("step=latch-mutant-admits-after-ordinary ok (beforeHrefLen=%d)", len(beforeHref.Result.Value))
}

func historySetupChromeLockedOrdinarySetupOnly(t *testing.T) hsChromeSetupProbe {
	t.Helper()
	historyRestoreRequireChrome(t)
	fixture := historyRestoreChromeFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	seven := "http://seven.protected.test:" + port
	ordinary := "http://page.ordinary.test:" + port
	b := launchBrowserOn(t, 0,
		"--site-per-process",
		"--enable-features=BackForwardCache",
		"--disable-features=BackForwardCacheMemoryControls",
		"--unsafely-treat-insecure-origin-as-secure="+seven,
		"--host-resolver-rules=MAP *.test 127.0.0.1",
	)
	step := historyRestoreChromeStep(t)
	host, err := b.tryOpenPage(t, seven+"/seven-host")
	step("open-protected-host", err)
	linkHost, err := b.tryOpenPage(t, seven+"/seven-host")
	step("open-seven-link-host", err)
	env := &linkEnv{t: t, b: b, origin: seven, p: linkHost}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", seven, "--name", "history-setup-chrome-setup-only")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step=linkTest.ready: browsers count=%d", len(hello.Browsers))
	}
	browser := hello.Browsers[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	step("navigate-ordinary-unguarded", host.session.Call(ctx, "Page.navigate", map[string]any{"url": ordinary + "/ordinary"}, nil))
	time.Sleep(500 * time.Millisecond)
	var openRaw rawJSON
	env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitOpen("+jsArgs(map[string]any{
		"browser": browser, "hostTarget": host.target, "framePrefix": "http://frame.other.test", "lockSettleMs": 800, "callTimeoutMs": 60000,
	})+")", &openRaw, 60*time.Second)
	var open struct {
		HostAttached    bool   `json:"hostAttached"`
		HostAttachError string `json:"hostAttachError"`
		Steps           []struct {
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(openRaw, &open); err != nil {
		t.Fatalf("step=guarded-explicit-open-parse: %v", err)
	}
	if open.HostAttachError != "" {
		preHost, err := b.tryOpenPage(t, ordinary+"/ordinary")
		step("open-ordinary-pre-seven", err)
		host = preHost
		env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitOpen("+jsArgs(map[string]any{
			"browser": browser, "hostTarget": host.target, "framePrefix": "http://frame.other.test", "lockSettleMs": 800, "callTimeoutMs": 60000,
		})+")", &openRaw, 60*time.Second)
		if err := json.Unmarshal(openRaw, &open); err != nil {
			t.Fatalf("step=guarded-explicit-open-parse-retry: %v", err)
		}
		if open.HostAttachError != "" {
			t.Fatalf("step=guarded-attach-host: %s", open.HostAttachError)
		}
		step("navigate-seven-after-pre-attach", host.session.Call(ctx, "Page.navigate", map[string]any{"url": seven + "/seven-host"}, nil))
		time.Sleep(500 * time.Millisecond)
		step("navigate-ordinary-after-seven", host.session.Call(ctx, "Page.navigate", map[string]any{"url": ordinary + "/ordinary"}, nil))
	}
	var lockRaw rawJSON
	env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitLockProbe("+jsArgs(map[string]any{"lockSettleMs": 800})+")", &lockRaw, 30*time.Second)
	var lock struct {
		Steps []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"steps"`
	}
	_ = json.Unmarshal(lockRaw, &lock)
	lockOK := false
	for _, s := range lock.Steps {
		if s.Name == "guarded-lock-engaged" {
			lockOK = s.OK
		}
	}
	if !lockOK {
		t.Fatalf("step=guarded-lock-engaged: not engaged")
	}
	var setupRaw rawJSON
	env.p.evalWithin("linkTest.historySetupLockedOrdinarySetup("+jsArgs(map[string]any{"callTimeoutMs": 15000})+")", &setupRaw, 30*time.Second)
	var setup hsChromeSetupProbe
	if err := json.Unmarshal(setupRaw, &setup); err != nil {
		t.Fatalf("step=guarded-setup-parse: %v raw=%s", err, setupRaw)
	}
	for _, s := range setup.Steps {
		t.Logf("step=%s ok=%t err=%q", s.Name, s.OK, s.Error)
	}
	_ = hrExplicitClientClose(env)
	return setup
}

func TestCDPHistorySetupChromeProbeMarkers(t *testing.T) {
	historyRestoreRequireChrome(t)
	fixture := historyRestoreChromeFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	seven := "http://seven.protected.test:" + port
	ordinary := "http://page.ordinary.test:" + port
	const marker = "HS_B2_MARKER_7f3a9c"
	b := launchBrowserOn(t, 0,
		"--site-per-process",
		"--enable-features=BackForwardCache",
		"--disable-features=BackForwardCacheMemoryControls",
		"--unsafely-treat-insecure-origin-as-secure="+seven,
		"--host-resolver-rules=MAP *.test 127.0.0.1",
	)
	step := historyRestoreChromeStep(t)
	host, err := b.tryOpenPage(t, seven+"/seven-host")
	step("open-protected-host", err)
	linkHost, err := b.tryOpenPage(t, seven+"/seven-host")
	step("open-seven-link-host", err)
	env := &linkEnv{t: t, b: b, origin: seven, p: linkHost}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", seven, "--name", "history-setup-chrome-probe-markers")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step=linkTest.ready: browsers count=%d", len(hello.Browsers))
	}
	browser := hello.Browsers[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	step("navigate-ordinary-unguarded", host.session.Call(ctx, "Page.navigate", map[string]any{"url": ordinary + "/ordinary"}, nil))
	deadline := time.Now().Add(15 * time.Second)
	var hrefOut struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	for time.Now().Before(deadline) {
		if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut); err == nil && strings.HasPrefix(hrefOut.Result.Value, ordinary) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.HasPrefix(hrefOut.Result.Value, ordinary) {
		t.Fatalf("step=unguarded-confirm-ordinary-href: href=%q", hrefOut.Result.Value)
	}
	t.Logf("step=unguarded-confirm-ordinary-href ok")

	var openRaw rawJSON
	env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitOpen("+jsArgs(map[string]any{
		"browser": browser, "hostTarget": host.target, "framePrefix": "http://frame.other.test:" + port, "lockSettleMs": 800, "callTimeoutMs": 60000,
	})+")", &openRaw, 60*time.Second)
	var open struct {
		HostAttached    bool   `json:"hostAttached"`
		HostAttachError string `json:"hostAttachError"`
		Steps           []struct {
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(openRaw, &open); err != nil {
		t.Fatalf("step=guarded-explicit-open-parse: %v", err)
	}
	for _, s := range open.Steps {
		t.Logf("step=%s ok=%t err=%q", s.Name, s.OK, s.Error)
	}
	if open.HostAttachError != "" {
		preHost, err := b.tryOpenPage(t, ordinary+"/ordinary")
		step("open-ordinary-pre-seven", err)
		host = preHost
		env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitOpen("+jsArgs(map[string]any{
			"browser": browser, "hostTarget": host.target, "framePrefix": "http://frame.other.test:" + port, "lockSettleMs": 800, "callTimeoutMs": 60000,
		})+")", &openRaw, 60*time.Second)
		if err := json.Unmarshal(openRaw, &open); err != nil {
			t.Fatalf("step=guarded-explicit-open-parse-retry: %v", err)
		}
		if open.HostAttachError != "" {
			t.Fatalf("step=guarded-attach-host: %s", open.HostAttachError)
		}
		step("navigate-seven-after-pre-attach", host.session.Call(ctx, "Page.navigate", map[string]any{"url": seven + "/seven-host"}, nil))
		time.Sleep(500 * time.Millisecond)
		step("navigate-ordinary-after-seven", host.session.Call(ctx, "Page.navigate", map[string]any{"url": ordinary + "/ordinary"}, nil))
	}

	var lockRaw rawJSON
	env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitLockProbe("+jsArgs(map[string]any{
		"browser": browser, "lockSettleMs": 800, "callTimeoutMs": 15000,
	})+")", &lockRaw, 30*time.Second)
	var lock struct {
		Steps []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(lockRaw, &lock); err != nil {
		t.Fatalf("step=guarded-lock-parse: %v", err)
	}
	lockOK := false
	for _, s := range lock.Steps {
		if s.Name == "guarded-lock-engaged" {
			lockOK = s.OK
		}
	}
	if !lockOK {
		t.Fatalf("step=guarded-lock-engaged: not engaged")
	}
	t.Logf("step=guarded-lock-engaged ok")

	inject := fmt.Sprintf(`(() => {
  const blank = document.createElement('iframe');
  blank.srcdoc = '<p id="srcdoc">same-process</p>';
  document.body.appendChild(blank);
  const about = document.createElement('iframe');
  about.src = 'about:blank';
  document.body.appendChild(about);
  const marked = document.createElement('iframe');
  marked.src = %q;
  document.body.appendChild(marked);
  return true;
})()`, seven+"/seven-marked")
	var injectOut struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": inject, "returnByValue": true}, &injectOut); err != nil || !injectOut.Result.Value {
		t.Fatalf("step=unguarded-inject-frames: err=%v value=%t", err, injectOut.Result.Value)
	}
	t.Logf("step=unguarded-inject-frames ok srcdoc+about:blank+oopif-marked")
	time.Sleep(800 * time.Millisecond)

	var probeRaw rawJSON
	env.p.evalWithin("linkTest.historySetupProbeMarkers("+jsArgs(map[string]any{
		"browser": browser, "protectedPrefix": seven, "marker": marker, "callTimeoutMs": 30000,
	})+")", &probeRaw, 45*time.Second)
	var probe struct {
		Steps []struct {
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
			At    int64  `json:"at"`
		} `json:"steps"`
		BypassOK          bool   `json:"bypassOk"`
		AutoAttachOK      bool   `json:"autoAttachOk"`
		GetDocumentOK     bool   `json:"getDocumentOk"`
		GetDocumentError  string `json:"getDocumentError"`
		TargetTextRefusal bool   `json:"targetTextRefusal"`
		DisableOK         bool   `json:"disableOk"`
		DisableError      string `json:"disableError"`
		ReplyClean        bool   `json:"replyClean"`
		MarkerAbsent      bool   `json:"markerAbsent"`
		PathLeak          bool   `json:"pathLeak"`
		TreeNodeCount     int    `json:"treeNodeCount"`
		MarkerHits        int    `json:"markerHits"`
	}
	if err := json.Unmarshal(probeRaw, &probe); err != nil {
		t.Fatalf("step=probe-markers-parse: %v raw=%s", err, probeRaw)
	}
	rowsOK := 0
	for _, s := range probe.Steps {
		if s.OK {
			rowsOK++
			t.Logf("step=%s ok at+%dms", s.Name, s.At)
		} else {
			t.Logf("step=%s refused at+%dms err=%q", s.Name, s.At, s.Error)
		}
	}
	lines := guardLines(env.a)
	framesProt, iframeProt, measureRefused, worlds, protFrames, navFrames, reason, measureOK := childDocParseMeasure(lines)
	if !probe.BypassOK {
		t.Fatalf("step=bypass: want Network.setBypassServiceWorker ok")
	}
	if !probe.AutoAttachOK {
		t.Fatalf("step=autoAttach: want Target.setAutoAttach ok")
	}
	if !probe.GetDocumentOK {
		t.Fatalf("step=getDocument: want DOM.getDocument ok; err=%q targetTextRefusal=%t", probe.GetDocumentError, probe.TargetTextRefusal)
	}
	if !probe.ReplyClean {
		t.Fatalf("step=replyClean: probe reply must not carry a protected document path")
	}
	if probe.PathLeak {
		t.Fatalf("step=pathLeak: protected path must not leak in the probe reply")
	}
	if !probe.MarkerAbsent || probe.MarkerHits != 0 {
		t.Fatalf("step=marker-absent: marker must be absent; hits=%d absent=%t", probe.MarkerHits, probe.MarkerAbsent)
	}
	if probe.TreeNodeCount < 1 {
		t.Fatalf("step=treeNodeCount: want >= 1; got %d", probe.TreeNodeCount)
	}
	if !probe.DisableOK {
		t.Fatalf("step=disable: want DOM.disable ok; err=%q", probe.DisableError)
	}
	t.Logf("ci-proof outcome branch=admit-clean measureLogged=%t framesProtected=%t iframeProtected=%t worlds=%d protectedFrames=%d navFrames=%d reason=%s getDocumentRefused=%t disableOk=%t disableError=%q",
		measureOK, framesProt, iframeProt, worlds, protFrames, navFrames, reason, measureRefused, probe.DisableOK, probe.DisableError)
	t.Logf("ci-proof rowsRun=%d rowsOK=%d replyClean=%t markerAbsent=%t markerHits=%d treeNodeCount=%d pathLeak=%t bypass=%t autoAttach=%t getDocument=%t getDocumentError=%q targetTextRefusal=%t disable=%t",
		len(probe.Steps), rowsOK, probe.ReplyClean, probe.MarkerAbsent, probe.MarkerHits, probe.TreeNodeCount, probe.PathLeak, probe.BypassOK, probe.AutoAttachOK, probe.GetDocumentOK, probe.GetDocumentError, probe.TargetTextRefusal, probe.DisableOK)
	t.Logf("ci-proof pin=admit-clean")
	t.Logf("step=bios-probe-markers ok")
	_ = hrExplicitClientClose(env)
}
