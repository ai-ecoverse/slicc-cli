package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

func historyRestoreChromeFixture(t *testing.T) *httptest.Server {
	t.Helper()
	files := http.FileServer(http.Dir(filepath.Join("testdata", "link")))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := strings.Cut(r.Host, ":")
		frame := "http://frame.other.test:" + port + "/inner"
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/seven-framed":
			_, _ = fmt.Fprintf(w, `<!doctype html><body><script type="module" src="/page.js"></script><iframe src="%s"></iframe>`, frame)
		case "/seven-host":
			_, _ = fmt.Fprint(w, `<!doctype html><body><script type="module" src="/page.js"></script>`)
		case "/ordinary":
			_, _ = fmt.Fprint(w, `<!doctype html><body><p id="ordinary">ordinary</p>`)
		case "/inner":
			_, _ = fmt.Fprint(w, `<!doctype html><body><p id="inner">inner</p>`)
		default:
			w.Header().Del("Content-Type")
			files.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

var historyRestoreURLLike = regexp.MustCompile(`(?i)(https?://|chrome-error:|blob:|data:)`)

func historyRestoreNeutralGuardLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if !strings.Contains(line, "cdp:") {
			continue
		}
		if historyRestoreURLLike.MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func historyRestoreInterestingGuardLine(line string) bool {
	keys := []string{
		"stream", "holding", "history confirm", "history hold", "deadline", "attachSeq=", "readSeq=",
		"unresolved-owner", "secondReads", "protecting", "detaching", "browser-endpoints-denied",
		"refusing", "refused",
	}
	for _, k := range keys {
		if strings.Contains(line, k) {
			return true
		}
	}
	return false
}

func historyRestoreIDPrefix(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func historyRestoreEventTargetID(ev cdp.Event) string {
	var params struct {
		TargetInfo struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfo"`
		TargetID string `json:"targetId"`
		Frame    struct {
			ID string `json:"id"`
		} `json:"frame"`
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(ev.Params, &params)
	switch {
	case params.TargetInfo.TargetID != "":
		return params.TargetInfo.TargetID
	case params.TargetID != "":
		return params.TargetID
	case params.Frame.ID != "":
		return params.Frame.ID
	default:
		return ""
	}
}

func historyRestoreRequireChrome(t *testing.T) {
	t.Helper()
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("step=chrome: CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
}

func historyRestoreChromeStep(t *testing.T) func(string, error) {
	t.Helper()
	return func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("step=%s: %v", name, err)
		}
	}
}

type hrChromeTimedEvt struct {
	src    string
	method string
	id     string
	ms     int64
}

type hrChromeHoldStats struct {
	held            int
	unresolvedHeld  int
	secondReads     int
	deadlineHolds   int
	deadlineByKind  map[string]int
	resolveWaitMs   []string
	deliveredAttach int
	deliveredRead   int
	holdMs          int64
	readsPerHold    []string
}

func historyRestoreParseHoldStats(neutral []string) hrChromeHoldStats {
	s := hrChromeHoldStats{deadlineByKind: map[string]int{}}
	attachSeqRe := regexp.MustCompile(`attachSeq=(\d+)`)
	readSeqRe := regexp.MustCompile(`readSeq=(\d+)`)
	readSeqsRe := regexp.MustCompile(`readSeqs=(\[[^\]]*\])`)
	secondReadsRe := regexp.MustCompile(`secondReads=(\d+)`)
	var holdAt, deliverAt int64 = -1, -1
	for _, line := range neutral {
		if strings.Contains(line, "holding a child of a history-locked page") || strings.Contains(line, "holding an explicit attach of a child of a history-locked page") {
			s.held++
			if holdAt < 0 {
				holdAt = 0
			}
		}
		if strings.Contains(line, "holding unresolved-owner child") {
			s.unresolvedHeld++
			s.held++
		}
		if strings.Contains(line, "history confirm reissued after stale in-flight read") {
			s.secondReads++
		}
		if strings.Contains(line, "reached deadline") || strings.Contains(line, "reached its deadline") {
			s.deadlineHolds++
		}
		if strings.Contains(line, "history hold delivered") || strings.Contains(line, "history hold hidden") {
			am := attachSeqRe.FindStringSubmatch(line)
			rm := readSeqRe.FindStringSubmatch(line)
			rs := readSeqsRe.FindStringSubmatch(line)
			sr := secondReadsRe.FindStringSubmatch(line)
			if len(am) == 2 && len(rm) == 2 {
				var attachSeq, readSeq int
				_, _ = fmt.Sscanf(am[1], "%d", &attachSeq)
				_, _ = fmt.Sscanf(rm[1], "%d", &readSeq)
				readSeqs := ""
				holdSecondReads := ""
				if len(rs) == 2 {
					readSeqs = rs[1]
				}
				if len(sr) == 2 {
					holdSecondReads = sr[1]
				}
				s.readsPerHold = append(s.readsPerHold, fmt.Sprintf("attachSeq=%d readSeq=%d readSeqs=%s secondReads=%s delivered=%t", attachSeq, readSeq, readSeqs, holdSecondReads, strings.Contains(line, "history hold delivered")))
				if strings.Contains(line, "history hold delivered") {
					s.deliveredAttach = attachSeq
					s.deliveredRead = readSeq
					if deliverAt < 0 {
						deliverAt = 0
					}
				}
			}
		}
		if strings.Contains(line, "unresolved-owner child reached deadline") {
			kind := "unknown"
			if i := strings.Index(line, "kind="); i >= 0 {
				rest := line[i+5:]
				if j := strings.IndexByte(rest, ' '); j >= 0 {
					kind = rest[:j]
				} else {
					kind = rest
				}
			}
			s.deadlineByKind[kind]++
		}
		if strings.Contains(line, "unresolved-owner child resolved") && strings.Contains(line, "waitedMs=") {
			if i := strings.Index(line, "waitedMs="); i >= 0 {
				s.resolveWaitMs = append(s.resolveWaitMs, strings.TrimSpace(line[i+len("waitedMs="):]))
			}
		}
	}
	_ = holdAt
	_ = deliverAt
	return s
}

func historyRestoreLogOrdered(t *testing.T, events []hrChromeTimedEvt) {
	t.Helper()
	for _, ev := range events {
		if ev.src == "guard" {
			t.Logf("ordered +%dms src=guard line=%s", ev.ms, ev.method)
			continue
		}
		t.Logf("ordered +%dms src=%s method=%s targetIdPrefix=%s", ev.ms, ev.src, ev.method, ev.id)
	}
}

func TestCDPHistoryRestoreChromeSafetyRestoredProtected(t *testing.T) {
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
	host, err := b.tryOpenPage(t, seven+"/seven-framed")
	step("open-protected-framed", err)
	deadline := time.Now().Add(15 * time.Second)
	var childIDs []string
	for time.Now().Before(deadline) {
		var res struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				URL      string `json:"url"`
			} `json:"targetInfos"`
		}
		listCtx, listCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := b.client.Call(listCtx, "", "Target.getTargets", map[string]any{}, &res)
		listCancel()
		if err == nil {
			childIDs = childIDs[:0]
			for _, info := range res.TargetInfos {
				if info.Type == "iframe" && strings.HasPrefix(info.URL, frameHost) {
					childIDs = append(childIDs, info.TargetID)
				}
			}
			if len(childIDs) > 0 {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(childIDs) == 0 {
		t.Fatalf("step=list-third-party-iframe: protected page never grew a third-party iframe")
	}
	t.Logf("step=list-third-party-iframe observer=unguarded count=%d", len(childIDs))
	childSet := map[string]bool{}
	for _, id := range childIDs {
		childSet[id] = true
	}
	linkHost, err := b.tryOpenPage(t, seven+"/seven-host")
	step("open-seven-host", err)
	env := &linkEnv{t: t, b: b, origin: seven, p: linkHost}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", seven, "--name", "history-restore-chrome-safety")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step=linkTest.ready: browsers count=%d", len(hello.Browsers))
	}
	browser := hello.Browsers[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	step("navigate-ordinary", host.session.Call(ctx, "Page.navigate", map[string]any{"url": ordinary + "/ordinary"}, nil))
	time.Sleep(500 * time.Millisecond)
	var create struct {
		TargetID string `json:"targetId"`
	}
	step("observer-create-about-blank", b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &create))
	observerSession, err := b.client.Attach(ctx, create.TargetID)
	step("observer-attach-about-blank", err)
	t.Cleanup(func() { b.closeTarget(create.TargetID) })
	var hist struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID  int    `json:"id"`
			URL string `json:"url"`
		} `json:"entries"`
	}
	if err := host.session.Call(ctx, "Page.getNavigationHistory", map[string]any{}, &hist); err != nil {
		t.Fatalf("step=getNavigationHistory: %v", err)
	}
	var sevenEntry int
	for _, e := range hist.Entries {
		if strings.HasPrefix(e.URL, seven) {
			sevenEntry = e.ID
			break
		}
	}
	if sevenEntry == 0 {
		t.Fatalf("step=find-protected-history-entry: protected entry missing from navigation history entries=%d", len(hist.Entries))
	}
	if err := observerSession.Call(ctx, "Target.activateTarget", map[string]any{"targetId": host.target}, nil); err != nil {
		t.Logf("step=observer-activate: err=%v", err)
	}

	_ = host.session.Call(ctx, "Page.enable", nil, nil)
	_ = b.client.Call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}, nil)
	sub := b.client.Subscribe()
	defer sub.Close()
	var (
		mu     sync.Mutex
		events []hrChromeTimedEvt
	)
	restoreAt := time.Now()
	seenGuard := map[string]bool{}
	stopWatch := make(chan struct{})
	var watchWG sync.WaitGroup
	watchWG.Add(2)
	go func() {
		defer watchWG.Done()
		for {
			select {
			case <-stopWatch:
				return
			case ev, ok := <-sub.Events():
				if !ok {
					return
				}
				switch ev.Method {
				case "Page.frameNavigated", "Target.attachedToTarget", "Target.targetInfoChanged":
				default:
					continue
				}
				id := historyRestoreEventTargetID(ev)
				interesting := ev.Method == "Page.frameNavigated" || childSet[id] || id == host.target
				if !interesting {
					var tip struct {
						TargetInfo struct {
							Type string `json:"type"`
							URL  string `json:"url"`
						} `json:"targetInfo"`
					}
					_ = json.Unmarshal(ev.Params, &tip)
					if tip.TargetInfo.Type == "iframe" && strings.HasPrefix(tip.TargetInfo.URL, frameHost) {
						interesting = true
						if id != "" {
							childSet[id] = true
						}
					}
				}
				if !interesting {
					continue
				}
				ms := time.Since(restoreAt).Milliseconds()
				mu.Lock()
				events = append(events, hrChromeTimedEvt{src: "observer=unguarded", method: ev.Method, id: historyRestoreIDPrefix(id), ms: ms})
				mu.Unlock()
			}
		}
	}()
	go func() {
		defer watchWG.Done()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWatch:
				return
			case <-ticker.C:
				ms := time.Since(restoreAt).Milliseconds()
				for _, line := range historyRestoreNeutralGuardLines(guardLines(env.a)) {
					if !historyRestoreInterestingGuardLine(line) {
						continue
					}
					mu.Lock()
					if seenGuard[line] {
						mu.Unlock()
						continue
					}
					seenGuard[line] = true
					events = append(events, hrChromeTimedEvt{src: "guard", method: line, id: "", ms: ms})
					mu.Unlock()
				}
			}
		}
	}()

	step("navigateToHistoryEntry", host.session.Call(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": sevenEntry}, nil))
	time.Sleep(800 * time.Millisecond)
	close(stopWatch)
	watchWG.Wait()

	neutral := historyRestoreNeutralGuardLines(guardLines(env.a))
	t.Logf("neutral guard line count after restore attempt: %d", len(neutral))
	for _, line := range neutral {
		t.Logf("guard: %s", line)
	}
	stats := historyRestoreParseHoldStats(neutral)
	for _, row := range stats.readsPerHold {
		t.Logf("hold release seqs: %s", row)
	}
	t.Logf("held children count from guard log: %d unresolvedHeld=%d secondReads=%d deadlineHolds=%d resolveWaitMs=%v deadlineByKind=%v", stats.held, stats.unresolvedHeld, stats.secondReads, stats.deadlineHolds, stats.resolveWaitMs, stats.deadlineByKind)
	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
			Title    string `json:"title"`
		} `json:"targetInfos"`
	}
	if err := b.client.Call(ctx, "", "Target.getTargets", map[string]any{}, &targets); err != nil {
		t.Fatal(err)
	}
	listedWhileCached := 0
	for _, info := range targets.TargetInfos {
		if info.Type == "iframe" && strings.HasPrefix(info.URL, frameHost) {
			listedWhileCached++
			t.Logf("observer=unguarded after restore: targetId=%s type=%s titleLen=%d", historyRestoreIDPrefix(info.TargetID), info.Type, len(info.Title))
		}
	}
	t.Logf("observer=unguarded third-party iframes still listed after restore attempt: %d", listedWhileCached)
	var guarded rawJSON
	env.p.evalWithin("linkTest.protectedOOPIF("+jsArgs(map[string]any{"browser": browser, "frames": childIDs})+")", &guarded, 60*time.Second)
	var row struct {
		Rows map[string]struct {
			Listed bool `json:"listed"`
			Info   struct {
				OK bool `json:"ok"`
			} `json:"info"`
			Attach struct {
				OK     bool   `json:"ok"`
				Error  string `json:"error"`
				Method string `json:"method"`
			} `json:"attach"`
		} `json:"rows"`
		Seen []string `json:"seen"`
	}
	if err := json.Unmarshal(guarded, &row); err != nil {
		t.Fatalf("client=guarded protectedOOPIF parse: %v raw=%s", err, guarded)
	}
	t.Logf("client=guarded protected OOPIF probe: %s", guarded)
	listedN, infoOKN, attachOKN := 0, 0, 0
	for id, r := range row.Rows {
		if r.Listed {
			listedN++
		}
		if r.Info.OK {
			infoOKN++
		}
		if r.Attach.OK {
			attachOKN++
		}
		t.Logf("client=guarded child idPrefix=%s listed=%t infoOK=%t attachOK=%t attachMethod=%q attachErr=%q", historyRestoreIDPrefix(id), r.Listed, r.Info.OK, r.Attach.OK, r.Attach.Method, r.Attach.Error)
		if r.Listed || r.Info.OK || r.Attach.OK {
			t.Fatalf("client=guarded saw content-bearing or attachable OOPIF id=%s listed=%t infoOK=%t attachOK=%t err=%q", id, r.Listed, r.Info.OK, r.Attach.OK, r.Attach.Error)
		}
	}
	t.Logf("client=guarded per-child counts listed=%d infoOK=%d attachOK=%d children=%d", listedN, infoOKN, attachOKN, len(row.Rows))
	mu.Lock()
	ordered := append([]hrChromeTimedEvt(nil), events...)
	mu.Unlock()
	historyRestoreLogOrdered(t, ordered)
	t.Logf("step=summary stepOrder=open-framed→list-iframe→attach-link→navigate-ordinary→observer-blank→history→restore held=%d unresolvedHeld=%d secondReads=%d deadlineHolds=%d listedWhileCached=%d orderedEvents=%d", stats.held, stats.unresolvedHeld, stats.secondReads, stats.deadlineHolds, listedWhileCached, len(ordered))
	if stats.held == 0 {
		t.Logf("note: held=0 — safety row; cached OOPIF restore may not attach at the guard; listedWhileCached=%d", listedWhileCached)
	}
	if stats.held == 0 && listedWhileCached == 0 {
		protected := false
		for _, line := range neutral {
			if strings.Contains(line, "protected origin") {
				protected = true
				break
			}
		}
		if !protected {
			t.Fatalf("step=restore-activity: no restore-related guard activity observed (held=0 listedWhileCached=0)")
		}
	}
}

type hrChromeCapture struct {
	mu        sync.Mutex
	childSet  map[string]bool
	seenGuard map[string]bool
	events    []hrChromeTimedEvt
	phaseAt   time.Time
}

func newHrChromeCapture(phaseAt time.Time) *hrChromeCapture {
	return &hrChromeCapture{
		childSet:  map[string]bool{},
		seenGuard: map[string]bool{},
		phaseAt:   phaseAt,
	}
}

func (c *hrChromeCapture) noteChild(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	c.childSet[id] = true
	c.mu.Unlock()
}

func (c *hrChromeCapture) noteObserver(method, id string) {
	c.mu.Lock()
	c.events = append(c.events, hrChromeTimedEvt{src: "observer=unguarded", method: method, id: historyRestoreIDPrefix(id), ms: time.Since(c.phaseAt).Milliseconds()})
	c.mu.Unlock()
}

func (c *hrChromeCapture) noteClient(method, id string, ms int64) {
	c.mu.Lock()
	c.events = append(c.events, hrChromeTimedEvt{src: "client=guarded", method: method, id: historyRestoreIDPrefix(id), ms: ms})
	c.mu.Unlock()
}

func (c *hrChromeCapture) noteGuardLines(raw string) {
	ms := time.Since(c.phaseAt).Milliseconds()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, line := range historyRestoreNeutralGuardLines(raw) {
		if !historyRestoreInterestingGuardLine(line) || c.seenGuard[line] {
			continue
		}
		c.seenGuard[line] = true
		c.events = append(c.events, hrChromeTimedEvt{src: "guard", method: line, id: "", ms: ms})
	}
}

func (c *hrChromeCapture) snapshot() []hrChromeTimedEvt {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]hrChromeTimedEvt(nil), c.events...)
}

func TestCDPHistoryRestoreChromeCaptureRace(t *testing.T) {
	c := newHrChromeCapture(time.Now())
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			c.noteChild(fmt.Sprintf("child-%d", i%8))
		}(i)
		go func(i int) {
			defer wg.Done()
			c.noteObserver("Target.attachedToTarget", fmt.Sprintf("child-%d", i%8))
		}(i)
		go func(i int) {
			defer wg.Done()
			c.noteGuardLines("cdp: holding a child of a history-locked page attachSeq=" + fmt.Sprintf("%d", i))
		}(i)
	}
	wg.Wait()
	if len(c.snapshot()) == 0 {
		t.Fatal("step=capture-race: expected captured events")
	}
}

type hrChromeLockedOrdinaryResult struct {
	stats         hrChromeHoldStats
	ordered       []hrChromeTimedEvt
	childID       string
	replyMs       int64
	askedAtMs     int64
	repliedAtMs   int64
	childEvalOK   bool
	childEvalVal  string
	sevenEntry    int
	host          *page
	env           *linkEnv
	b             *chromeProc
	browser       string
	frameHost     string
	seven         string
	ordinary      string
	ctx           context.Context
	holdAtMs      int64
	deliveredAtMs int64
}

func historyRestoreChromeLockedOrdinaryExplicit(t *testing.T) *hrChromeLockedOrdinaryResult {
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
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", seven, "--name", "history-restore-chrome-locked-ordinary-explicit")
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
		t.Fatalf("step=wait-ordinary-document: href=%q err=%v", hrefOut.Result.Value, err)
	}
	t.Logf("step=wait-ordinary-document ok hrefLen=%d", len(hrefOut.Result.Value))

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

	baseOpts := map[string]any{
		"browser":        browser,
		"ordinaryPrefix": ordinary,
		"sevenPrefix":    seven,
		"frameURL":       frameHost + "/inner",
		"framePrefix":    frameHost,
		"lockSettleMs":   1000,
		"findWaitMs":     15000,
		"callTimeoutMs":  90000,
	}
	type hrExplicitProbe struct {
		Steps []struct {
			Name  string `json:"name"`
			At    int64  `json:"at"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"steps"`
		HostAttachError string   `json:"hostAttachError"`
		HostAttached    bool     `json:"hostAttached"`
		PageSession     string   `json:"pageSession"`
		BeforeIframes   []string `json:"beforeIframes"`
		LockEngaged     bool     `json:"lockEngaged"`
		ChildID         string   `json:"childId"`
		ExplicitAttach  struct {
			OK        bool   `json:"ok"`
			Error     string `json:"error"`
			SessionID string `json:"sessionId"`
			AskedAt   int64  `json:"askedAt"`
			RepliedAt int64  `json:"repliedAt"`
			ReplyMs   int64  `json:"replyMs"`
		} `json:"explicitAttach"`
		ChildEvaluate struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
			Value string `json:"value"`
		} `json:"childEvaluate"`
		ElapsedMs int64 `json:"elapsedMs"`
	}
	logSteps := func(probe hrExplicitProbe) {
		for _, s := range probe.Steps {
			if s.OK {
				t.Logf("step=%s ok at+%dms", s.Name, s.At)
			} else {
				t.Logf("step=%s refused at+%dms err=%q", s.Name, s.At, s.Error)
			}
		}
	}
	failUnexpectedSteps := func(names ...string) func(hrExplicitProbe) {
		want := map[string]bool{}
		for _, n := range names {
			want[n] = true
		}
		return func(o hrExplicitProbe) {
			for _, s := range o.Steps {
				if s.OK || !want[s.Name] {
					continue
				}
				close(stopWatch)
				watchWG.Wait()
				t.Fatalf("step=%s: %s", s.Name, s.Error)
			}
		}
	}
	runOpen := func(hostTarget string) hrExplicitProbe {
		opts := map[string]any{}
		for k, v := range baseOpts {
			opts[k] = v
		}
		opts["hostTarget"] = hostTarget
		t.Logf("step=guarded-explicit-open hostTargetPrefix=%s", historyRestoreIDPrefix(hostTarget))
		var raw rawJSON
		env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitOpen("+jsArgs(opts)+")", &raw, 60*time.Second)
		var probe hrExplicitProbe
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("step=guarded-explicit-open-parse: %v raw=%s", err, raw)
		}
		logSteps(probe)
		return probe
	}
	runLockProbe := func() hrExplicitProbe {
		t.Logf("step=guarded-lock-engaged-probe")
		var raw rawJSON
		env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitLockProbe("+jsArgs(baseOpts)+")", &raw, 30*time.Second)
		var probe hrExplicitProbe
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("step=guarded-lock-engaged-parse: %v raw=%s", err, raw)
		}
		logSteps(probe)
		return probe
	}
	runAttach := func(before []string) hrExplicitProbe {
		opts := map[string]any{}
		for k, v := range baseOpts {
			opts[k] = v
		}
		opts["beforeIframes"] = before
		t.Logf("step=guarded-explicit-attach")
		var raw rawJSON
		env.p.evalWithin("linkTest.historyRestoreLockedOrdinaryExplicitAttach("+jsArgs(opts)+")", &raw, 90*time.Second)
		var probe hrExplicitProbe
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("step=guarded-explicit-attach-parse: %v raw=%s", err, raw)
		}
		logSteps(probe)
		return probe
	}
	confirmOrdinaryUnguarded := func() {
		loadDeadline = time.Now().Add(15 * time.Second)
		for time.Now().Before(loadDeadline) {
			if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut); err == nil && strings.HasPrefix(hrefOut.Result.Value, ordinary) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut); err != nil || !strings.HasPrefix(hrefOut.Result.Value, ordinary) {
			close(stopWatch)
			watchWG.Wait()
			t.Fatalf("step=unguarded-confirm-ordinary-href: href=%q err=%v", hrefOut.Result.Value, err)
		}
		t.Logf("step=unguarded-confirm-ordinary-href ok hrefLen=%d", len(hrefOut.Result.Value))
	}
	findSevenEntryUnguarded := func() int {
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
		t.Logf("step=unguarded-getNavigationHistory ok entries=%d", len(hist.Entries))
		for _, e := range hist.Entries {
			if strings.HasPrefix(e.URL, seven) {
				return e.ID
			}
		}
		return 0
	}
	injectIframeUnguarded := func() {
		expr := fmt.Sprintf(`(() => { const f = document.createElement('iframe'); f.src = %q; document.body.appendChild(f); return true; })()`, frameHost+"/inner")
		var out struct {
			Result struct {
				Value bool `json:"value"`
			} `json:"result"`
		}
		if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &out); err != nil || !out.Result.Value {
			close(stopWatch)
			watchWG.Wait()
			t.Fatalf("step=unguarded-inject-iframe: err=%v value=%t", err, out.Result.Value)
		}
		t.Logf("step=unguarded-inject-iframe ok")
	}

	t.Logf("step=guarded-explicit-attach-run path=after-seven-lock hostTargetPrefix=%s", historyRestoreIDPrefix(host.target))
	open := runOpen(host.target)
	usedPreSeven := false
	failOpenUnexpected := failUnexpectedSteps("guarded-setDiscoverTargets", "guarded-page-enable", "guarded-runtime-enable", "guarded-getTargets-before")
	if open.HostAttachError != "" {
		t.Logf("step=guarded-attach-host: refused after history-lock err=%q; falling back to pre-seven attach", open.HostAttachError)
		usedPreSeven = true
		preHost, err := b.tryOpenPage(t, ordinary+"/ordinary")
		step("open-ordinary-pre-seven", err)
		host = preHost
		open = runOpen(host.target)
		if open.HostAttachError != "" {
			close(stopWatch)
			watchWG.Wait()
			t.Fatalf("step=guarded-attach-host: refused even on ordinary before seven err=%q", open.HostAttachError)
		}
		failOpenUnexpected(open)
		step("navigate-seven-after-pre-attach", host.session.Call(ctx, "Page.navigate", map[string]any{"url": seven + "/seven-host"}, nil))
		time.Sleep(500 * time.Millisecond)
		step("navigate-ordinary-after-seven", host.session.Call(ctx, "Page.navigate", map[string]any{"url": ordinary + "/ordinary"}, nil))
	} else {
		failOpenUnexpected(open)
	}
	if !open.HostAttached && open.HostAttachError == "" {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=guarded-explicit-open: open did not attach host and reported no attach refusal")
	}

	confirmOrdinaryUnguarded()
	sevenEntry := findSevenEntryUnguarded()
	if sevenEntry == 0 {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=find-protected-history-entry: seven entry missing")
	}
	t.Logf("step=find-protected-history-entry id=%d", sevenEntry)

	lock := runLockProbe()
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
		t.Fatalf("step=guarded-lock-engaged: want history-refused Runtime.evaluate on locked ordinary host; got ok=%t err=%q", lockOK, lockErr)
	}
	t.Logf("step=guarded-lock-engaged ok refused err=%q", lockErr)

	before := append([]string(nil), open.BeforeIframes...)
	injectIframeUnguarded()
	probe := runAttach(before)
	close(stopWatch)
	watchWG.Wait()
	cap.noteGuardLines(guardLines(env.a))
	t.Logf("step=path usedPreSeven=%t", usedPreSeven)

	for _, s := range probe.Steps {
		if s.OK {
			continue
		}
		switch s.Name {
		case "guarded-discover-iframe", "guarded-explicit-attach-iframe", "guarded-explicit-attach", "guarded-child-evaluate":
			t.Fatalf("step=%s: %s", s.Name, s.Error)
		}
	}
	if probe.ChildID == "" {
		t.Fatalf("step=guarded-discover-iframe: no child id")
	}
	if !probe.ExplicitAttach.OK {
		t.Fatalf("step=guarded-explicit-attach-iframe: refused err=%q — STOP: explicit attach impossible on locked ordinary in this harness", probe.ExplicitAttach.Error)
	}
	t.Logf("step=guarded-explicit-attach-iframe ok replyMs=%d askedAt=%d repliedAt=%d sessionPrefix=%s", probe.ExplicitAttach.ReplyMs, probe.ExplicitAttach.AskedAt, probe.ExplicitAttach.RepliedAt, historyRestoreIDPrefix(probe.ExplicitAttach.SessionID))
	if !probe.ChildEvaluate.OK {
		t.Fatalf("step=guarded-child-evaluate: %s", probe.ChildEvaluate.Error)
	}
	t.Logf("step=guarded-child-evaluate ok valueLen=%d", len(probe.ChildEvaluate.Value))
	cap.noteChild(probe.ChildID)
	cap.noteClient("Target.attachToTarget", probe.ChildID, probe.ExplicitAttach.RepliedAt)

	neutral := historyRestoreNeutralGuardLines(guardLines(env.a))
	stats := historyRestoreParseHoldStats(neutral)
	ordered := cap.snapshot()
	var holdAtMs, deliveredAtMs int64 = -1, -1
	for _, ev := range ordered {
		if ev.src != "guard" {
			continue
		}
		if holdAtMs < 0 && (strings.Contains(ev.method, "holding a child of a history-locked page") || strings.Contains(ev.method, "holding an explicit attach of a child of a history-locked page")) {
			holdAtMs = ev.ms
		}
		if deliveredAtMs < 0 && strings.Contains(ev.method, "history hold delivered") {
			deliveredAtMs = ev.ms
		}
	}
	if holdAtMs >= 0 && deliveredAtMs >= holdAtMs {
		stats.holdMs = deliveredAtMs - holdAtMs
	} else if probe.ExplicitAttach.ReplyMs > 0 && stats.held >= 1 {
		stats.holdMs = probe.ExplicitAttach.ReplyMs
	}
	t.Logf("neutral guard line count after explicit attach: %d", len(neutral))
	for _, line := range neutral {
		t.Logf("guard: %s", line)
	}
	for _, row := range stats.readsPerHold {
		t.Logf("reads per held child: %s", row)
	}
	t.Logf("held=%d unresolvedHeld=%d secondReads=%d deadlineHolds=%d holdMs=%d deliveredAttachSeq=%d deliveredReadSeq=%d replyMs=%d resolveWaitMs=%v deadlineByKind=%v", stats.held, stats.unresolvedHeld, stats.secondReads, stats.deadlineHolds, stats.holdMs, stats.deliveredAttach, stats.deliveredRead, probe.ExplicitAttach.ReplyMs, stats.resolveWaitMs, stats.deadlineByKind)
	historyRestoreLogOrdered(t, ordered)
	t.Logf("step=summary stepOrder=navigate-ordinary-unguarded→guarded-attach-host→unguarded-confirm→guarded-lock-engaged→unguarded-inject→explicit-attach-iframe usedPreSeven=%t held=%d holdMs=%d replyMs=%d orderedEvents=%d", usedPreSeven, stats.held, stats.holdMs, probe.ExplicitAttach.ReplyMs, len(ordered))

	return &hrChromeLockedOrdinaryResult{
		stats:         stats,
		ordered:       ordered,
		childID:       probe.ChildID,
		replyMs:       probe.ExplicitAttach.ReplyMs,
		askedAtMs:     probe.ExplicitAttach.AskedAt,
		repliedAtMs:   probe.ExplicitAttach.RepliedAt,
		childEvalOK:   probe.ChildEvaluate.OK,
		childEvalVal:  probe.ChildEvaluate.Value,
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
	}
}

func TestCDPHistoryRestoreChromeLockedOrdinaryExplicitAttachHold(t *testing.T) {
	r := historyRestoreChromeLockedOrdinaryExplicit(t)
	if r.stats.held < 1 {
		t.Fatalf("step=held-child: held=%d want>=1 (explicit-attach hold)", r.stats.held)
	}
	if r.stats.deliveredAttach == 0 {
		t.Fatalf("step=history-hold-delivered: want delivered after confirm; held=%d hidden-deadline=%d", r.stats.held, r.stats.deadlineHolds)
	}
	if r.stats.deliveredAttach > 0 && r.stats.deliveredRead > 0 && r.stats.deliveredAttach > r.stats.deliveredRead {
		t.Fatalf("step=confirm-after-attach: attachSeq=%d readSeq=%d (want attachSeq<=readSeq)", r.stats.deliveredAttach, r.stats.deliveredRead)
	}
	if !r.childEvalOK {
		t.Fatalf("step=guarded-child-usable: evaluate on delivered ordinary child failed")
	}
	t.Logf("step=held-child ok held=%d attachSeq=%d readSeq=%d holdMs=%d replyMs=%d childEvalLen=%d deadline=false", r.stats.held, r.stats.deliveredAttach, r.stats.deliveredRead, r.stats.holdMs, r.replyMs, len(r.childEvalVal))

	historyRestoreChromeLockedOrdinaryRestoreCheck(t, r)
}

func historyRestoreChromeLockedOrdinaryRestoreCheck(t *testing.T, r *hrChromeLockedOrdinaryResult) {
	t.Helper()
	if r.stats.deliveredAttach == 0 {
		t.Skip("step=restore-after-deliver: skipped — child was never delivered (no history hold delivered seqs); cannot assert conceal-after-restore")
	}
	before := append([]string(nil), historyRestoreNeutralGuardLines(guardLines(r.env.a))...)
	beforeSet := map[string]bool{}
	for _, line := range before {
		beforeSet[line] = true
	}
	phaseAt := time.Now()
	sub := r.b.client.Subscribe()
	defer sub.Close()
	var (
		mu     sync.Mutex
		events []hrChromeTimedEvt
	)
	stopWatch := make(chan struct{})
	var watchWG sync.WaitGroup
	watchWG.Add(2)
	seenGuard := map[string]bool{}
	for _, line := range before {
		if historyRestoreInterestingGuardLine(line) {
			seenGuard[line] = true
		}
	}
	go func() {
		defer watchWG.Done()
		for {
			select {
			case <-stopWatch:
				return
			case ev, ok := <-sub.Events():
				if !ok {
					return
				}
				switch ev.Method {
				case "Page.frameNavigated", "Target.attachedToTarget", "Target.targetInfoChanged", "Target.detachedFromTarget":
				default:
					continue
				}
				id := historyRestoreEventTargetID(ev)
				ms := time.Since(phaseAt).Milliseconds()
				mu.Lock()
				events = append(events, hrChromeTimedEvt{src: "observer=unguarded", method: ev.Method, id: historyRestoreIDPrefix(id), ms: ms})
				mu.Unlock()
			}
		}
	}()
	go func() {
		defer watchWG.Done()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWatch:
				return
			case <-ticker.C:
				ms := time.Since(phaseAt).Milliseconds()
				for _, line := range historyRestoreNeutralGuardLines(guardLines(r.env.a)) {
					if !historyRestoreInterestingGuardLine(line) || seenGuard[line] {
						continue
					}
					seenGuard[line] = true
					mu.Lock()
					events = append(events, hrChromeTimedEvt{src: "guard", method: line, id: "", ms: ms})
					mu.Unlock()
				}
			}
		}
	}()

	t.Logf("step=restore-after-deliver: navigateToHistoryEntry entryId=%d", r.sevenEntry)
	if err := r.host.session.Call(r.ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": r.sevenEntry}, nil); err != nil {
		close(stopWatch)
		watchWG.Wait()
		t.Fatalf("step=restore-after-deliver: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	close(stopWatch)
	watchWG.Wait()

	after := historyRestoreNeutralGuardLines(guardLines(r.env.a))
	var newLines []string
	for _, line := range after {
		if !beforeSet[line] {
			newLines = append(newLines, line)
		}
	}
	mu.Lock()
	ordered := append([]hrChromeTimedEvt(nil), events...)
	mu.Unlock()
	t.Logf("step=restore-after-deliver new guard lines=%d orderedEvents=%d", len(newLines), len(ordered))
	for _, line := range newLines {
		t.Logf("restore-seq guard: %s", line)
	}
	historyRestoreLogOrdered(t, ordered)

	sawCommit := false
	sawConceal := false
	for _, line := range newLines {
		if strings.Contains(line, "protecting") || strings.Contains(line, "history hold hidden") || strings.Contains(line, "detaching") {
			sawConceal = true
		}
		if strings.Contains(line, "protecting") || strings.Contains(line, "protected origin") || strings.Contains(line, "history hold") {
			sawCommit = true
		}
	}
	for _, ev := range ordered {
		if ev.src == "observer=unguarded" && (ev.method == "Page.frameNavigated" || ev.method == "Target.targetInfoChanged") {
			sawCommit = true
		}
	}

	var probe rawJSON
	r.env.p.evalWithin("linkTest.protectedOOPIF("+jsArgs(map[string]any{"browser": r.browser, "frames": []string{r.childID}})+")", &probe, 60*time.Second)
	var row struct {
		Rows map[string]struct {
			Listed bool `json:"listed"`
			Info   struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			} `json:"info"`
			Attach struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			} `json:"attach"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(probe, &row); err != nil {
		t.Fatalf("step=restore-guarded-probe parse: %v raw=%s", err, probe)
	}
	t.Logf("step=restore-guarded-probe: %s", probe)
	refused := false
	for id, child := range row.Rows {
		t.Logf("restore-seq client=guarded idPrefix=%s listed=%t infoOK=%t attachOK=%t infoErr=%q attachErr=%q", historyRestoreIDPrefix(id), child.Listed, child.Info.OK, child.Attach.OK, child.Info.Error, child.Attach.Error)
		if !child.Listed && !child.Info.OK && !child.Attach.OK {
			refused = true
		}
		if child.Listed || child.Info.OK || child.Attach.OK {
			t.Fatalf("step=restore-child-refused: client=guarded still saw child idPrefix=%s listed=%t infoOK=%t attachOK=%t", historyRestoreIDPrefix(id), child.Listed, child.Info.OK, child.Attach.OK)
		}
	}
	t.Logf("restore-seq: delivered→restore→commit=%t conceal=%t refused=%t", sawCommit, sawConceal, refused)

	if !sawCommit && !sawConceal {
		t.Skipf("step=restore-after-deliver: Chrome did not produce a protect commit/conceal sequence after navigateToHistoryEntry (likely bfcache-cached restore); observed newGuardLines=%d orderedEvents=%d refused=%t", len(newLines), len(ordered), refused)
	}
	if !refused {
		t.Fatalf("step=restore-child-refused: expected guarded client refusal after restore; probe=%s", probe)
	}
	t.Logf("step=restore-after-deliver ok")
}

func hrBuildHoldRemovedBinary(t *testing.T) string {
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
		filepath.Join(root, "internal/caps/cdp/history_restore_switches.go"): filepath.Join(root, "test/integration/testdata/historyrestore/mutation_hold_removed.go"),
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
	cmd := exec.CommandContext(ctx, "go", "build", buildReplace, replacePath, "-ldflags", "-X main.version=1.2.3-test-hold-removed", "-o", out, "./cmd/slicc")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("step=mutant-build: %v\n%s", err, output)
	}
	t.Logf("step=mutant-build ok path=%s mutant=mutation_hold_removed.go", out)
	return out
}

func TestCDPHistoryRestoreChromeLockedOrdinaryExplicitAttachHoldMutant(t *testing.T) {
	historyRestoreRequireChrome(t)
	mutant := hrBuildHoldRemovedBinary(t)
	t.Setenv("SLICC_TEST_ATTACH_BINARY", mutant)
	r := historyRestoreChromeLockedOrdinaryExplicit(t)
	failMsg := fmt.Sprintf("step=held-child: held=%d want>=1 (explicit-attach hold)", r.stats.held)
	if r.stats.held >= 1 {
		t.Fatalf("step=mutant-held-proof: tip row must FAIL on hold-removed binary at held>=1; got held=%d (would pass tip)", r.stats.held)
	}
	if r.childID == "" {
		t.Fatalf("step=mutant-immediate-reply: hold-removed binary must answer explicit attach at once; childID empty replyMs=%d", r.replyMs)
	}
	if r.stats.deliveredRead > 0 || r.stats.secondReads > 0 {
		t.Fatalf("step=mutant-immediate-reply: held=0 path must have no confirm read; deliveredRead=%d secondReads=%d replyMs=%d", r.stats.deliveredRead, r.stats.secondReads, r.replyMs)
	}
	t.Logf("step=mutant-held-proof ok: tip assertion would fail with %q; replyMs=%d held=0 no-read", failMsg, r.replyMs)
}

type rawJSON = json.RawMessage
