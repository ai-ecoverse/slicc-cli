package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func childDocRequireChrome(t *testing.T) {
	t.Helper()
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
}

func childDocFixture(t *testing.T) *httptest.Server {
	t.Helper()
	files := http.FileServer(http.Dir(filepath.Join("testdata", "link")))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/seven-host":
			_, _ = fmt.Fprint(w, `<!doctype html><body><script type="module" src="/page.js"></script>`)
		case "/seven-marked":
			_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>CD_CHILD_MARKER_a1b2 title</title></head><body><!-- CD_CHILD_MARKER_a1b2 comment --><p id="marked" data-mark="CD_CHILD_MARKER_a1b2">CD_CHILD_MARKER_a1b2 text</p></body></html>`)
		case "/ordinary":
			_, _ = fmt.Fprint(w, `<!doctype html><body><script type="module" src="/page.js"></script><p id="ordinary">ordinary</p>`)
		case "/ordinary-big":
			_, _ = fmt.Fprint(w, `<!doctype html><body><script type="module" src="/page.js"></script><div id="host"></div>`)
		default:
			w.Header().Del("Content-Type")
			files.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

var childDocMeasureRe = regexp.MustCompile(`child-doc admit measure target=\S+ framesProtected=(true|false) iframeProtected=(true|false) worlds=(\d+) protectedFrames=(\d+) navFrames=(\d+) reason=(\S+) getDocumentRefused=(true|false)`)

func childDocParseMeasure(lines string) (framesProtected, iframeProtected, getDocumentRefused bool, worlds, protectedFrames, navFrames int, reason string, ok bool) {
	m := childDocMeasureRe.FindStringSubmatch(lines)
	if m == nil {
		return false, false, false, 0, 0, 0, "", false
	}
	return m[1] == "true", m[2] == "true", m[7] == "true", atoiQuiet(m[3]), atoiQuiet(m[4]), atoiQuiet(m[5]), m[6], true
}

func atoiQuiet(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

type childDocChromeRow struct {
	name           string
	sameProcess    bool
	runtimeEnable  bool
	wantIframeProt bool
	wantWorlds     int
	wantProtFrames int
}

func TestCDPChildDocChromePierce(t *testing.T) {
	childDocRequireChrome(t)
	rows := []childDocChromeRow{
		{name: "site-isolated-no-runtime", sameProcess: false, runtimeEnable: false, wantIframeProt: true, wantWorlds: 0, wantProtFrames: 0},
		{name: "site-isolated-runtime", sameProcess: false, runtimeEnable: true, wantIframeProt: true, wantWorlds: 0, wantProtFrames: 0},
		{name: "same-process-no-runtime", sameProcess: true, runtimeEnable: false, wantIframeProt: false, wantWorlds: 1, wantProtFrames: 1},
		{name: "same-process-runtime", sameProcess: true, runtimeEnable: true, wantIframeProt: false, wantWorlds: 1, wantProtFrames: 1},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			childDocChromePierceRow(t, row)
		})
	}
}

func childDocChromePierceRow(t *testing.T, row childDocChromeRow) {
	t.Helper()
	fixture := childDocFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	seven := "http://seven.protected.test:" + port
	ordinary := "http://page.ordinary.test:" + port
	const marker = "CD_CHILD_MARKER_a1b2"
	args := []string{
		"--unsafely-treat-insecure-origin-as-secure=" + seven,
		"--host-resolver-rules=MAP *.test 127.0.0.1",
	}
	if row.sameProcess {
		args = append(args, "--disable-site-isolation-trials")
	} else {
		args = append(args, "--site-per-process")
	}
	b := launchBrowserOn(t, 0, args...)
	host, err := b.tryOpenPage(t, ordinary+"/ordinary")
	if err != nil {
		t.Fatalf("step=open-ordinary-host: %v", err)
	}
	linkHost, err := b.tryOpenPage(t, seven+"/seven-host")
	if err != nil {
		t.Fatalf("step=open-link-host: %v", err)
	}
	env := &linkEnv{t: t, b: b, origin: seven, p: linkHost}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", seven, "--name", "childdoc-chrome-"+row.name)
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step=linkTest.ready: browsers=%d", len(hello.Browsers))
	}
	browser := hello.Browsers[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	if err := host.session.Call(ctx, "Page.enable", nil, nil); err != nil {
		t.Fatalf("step=page-enable: %v", err)
	}
	if err := host.session.Call(ctx, "Runtime.enable", nil, nil); err != nil {
		t.Fatalf("step=runtime-enable-host: %v", err)
	}
	if err := host.session.Call(ctx, "DOM.enable", nil, nil); err != nil {
		t.Fatalf("step=dom-enable-host: %v", err)
	}
	var hrefOut struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut); err != nil || !strings.HasPrefix(hrefOut.Result.Value, ordinary) {
		t.Fatalf("step=confirm-ordinary: err=%v href=%q", err, hrefOut.Result.Value)
	}

	frameEvents := 0
	frameNavs := 0
	sub := b.client.Subscribe()
	defer sub.Close()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-sub.Events():
				if !ok {
					return
				}
				if ev.SessionID != host.session.ID {
					continue
				}
				switch ev.Method {
				case "Page.frameAttached":
					frameEvents++
				case "Page.frameNavigated":
					frameNavs++
				}
			}
		}
	}()

	inject := fmt.Sprintf(`(() => {
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
		t.Fatalf("step=inject-protected-child: err=%v value=%t", err, injectOut.Result.Value)
	}

	time.Sleep(1200 * time.Millisecond)

	var probe struct {
		Steps []struct {
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"steps"`
		GetDocumentOk         bool `json:"getDocumentOk"`
		GetDocumentRefused    bool `json:"getDocumentRefused"`
		Refused               bool `json:"refused"`
		DeliveredMarkerAbsent bool `json:"deliveredMarkerAbsent"`
		TreeNodeCount         int  `json:"treeNodeCount"`
		FrameOwners           int  `json:"frameOwners"`
		ContentDocuments      int  `json:"contentDocuments"`
		MarkerHits            int  `json:"markerHits"`
	}
	var probeRaw rawJSON
	env.p.evalWithin("linkTest.childDocPierce("+jsArgs(map[string]any{
		"browser": browser, "hostTarget": host.target, "marker": marker, "runtimeEnable": row.runtimeEnable, "callTimeoutMs": 30000,
	})+")", &probeRaw, 45*time.Second)
	if err := json.Unmarshal(probeRaw, &probe); err != nil {
		t.Fatalf("step=probe-parse: %v raw=%s", err, probeRaw)
	}
	for _, s := range probe.Steps {
		t.Logf("step=%s ok=%t err=%q", s.Name, s.OK, s.Error)
	}

	var rawDoc struct {
		Root json.RawMessage `json:"root"`
	}
	rawErr := host.session.Call(ctx, "DOM.getDocument", map[string]any{"depth": -1, "pierce": true}, &rawDoc)
	rawBlob := string(rawDoc.Root)
	rawMarkerPresent := rawErr == nil && strings.Contains(rawBlob, marker)
	rawContentDocs := strings.Count(rawBlob, `"contentDocument"`)
	t.Logf("ci-proof variant=%s sameProcess=%t runtimeEnable=%t rawMarkerPresent=%t rawContentDocuments=%d frameAttached=%d frameNavigated=%d",
		row.name, row.sameProcess, row.runtimeEnable, rawMarkerPresent, rawContentDocs, frameEvents, frameNavs)

	lines := guardLines(env.a)
	framesProt, iframeProt, measureRefused, worlds, protFrames, navFrames, reason, measureOK := childDocParseMeasure(lines)
	t.Logf("ci-proof measure framesProtected=%t iframeProtected=%t worlds=%d protectedFrames=%d navFrames=%d reason=%s getDocumentRefused=%t measureLogged=%t",
		framesProt, iframeProt, worlds, protFrames, navFrames, reason, measureRefused, measureOK)
	t.Logf("ci-proof delivered refused=%t getDocumentOk=%t deliveredMarkerAbsent=%t markerHits=%d treeNodeCount=%d frameOwners=%d contentDocuments=%d",
		probe.Refused, probe.GetDocumentOk, probe.DeliveredMarkerAbsent, probe.MarkerHits, probe.TreeNodeCount, probe.FrameOwners, probe.ContentDocuments)

	if !measureOK {
		t.Fatalf("step=measure: variant %s missing child-doc admit measure log", row.name)
	}
	if reason != "admit-refusal" {
		t.Fatalf("step=measure-reason: variant %s want admit-refusal (Chrome refuses at admit before the walk), got %q", row.name, reason)
	}
	if !probe.GetDocumentRefused || !measureRefused {
		t.Fatalf("step=measured-refusal: variant %s with protected child must refuse getDocument (probeRefused=%t measureRefused=%t framesProtected=%t getDocumentOk=%t); steps=%v",
			row.name, probe.GetDocumentRefused, measureRefused, framesProt, probe.GetDocumentOk, probe.Steps)
	}
	if !framesProt {
		t.Fatalf("step=frames-protected: variant %s measured refusal requires framesProtected=true", row.name)
	}
	if iframeProt != row.wantIframeProt {
		t.Fatalf("step=iframe-protected: variant %s want iframeProtected=%t, got %t", row.name, row.wantIframeProt, iframeProt)
	}
	if worlds != row.wantWorlds || protFrames != row.wantProtFrames {
		t.Fatalf("step=measure-shape: variant %s want worlds=%d protectedFrames=%d, got worlds=%d protectedFrames=%d",
			row.name, row.wantWorlds, row.wantProtFrames, worlds, protFrames)
	}
	if row.sameProcess && !rawMarkerPresent {
		t.Fatalf("step=raw-marker: same-process pierce control must show child marker (rawContentDocuments=%d)", rawContentDocs)
	}
	_ = hrExplicitClientClose(env)
}

func TestCDPChildDocChromeOrdinaryChildrenAdmitted(t *testing.T) {
	childDocRequireChrome(t)
	fixture := childDocFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	seven := "http://seven.protected.test:" + port
	ordinary := "http://page.ordinary.test:" + port
	const marker = "CD_CHILD_MARKER_a1b2"
	b := launchBrowserOn(t, 0,
		"--site-per-process",
		"--unsafely-treat-insecure-origin-as-secure="+seven,
		"--host-resolver-rules=MAP *.test 127.0.0.1",
	)
	host, err := b.tryOpenPage(t, ordinary+"/ordinary")
	if err != nil {
		t.Fatalf("step=open-ordinary-host: %v", err)
	}
	linkHost, err := b.tryOpenPage(t, seven+"/seven-host")
	if err != nil {
		t.Fatalf("step=open-link-host: %v", err)
	}
	env := &linkEnv{t: t, b: b, origin: seven, p: linkHost}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", seven, "--name", "childdoc-chrome-ordinary-children")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step=linkTest.ready: browsers=%d", len(hello.Browsers))
	}
	browser := hello.Browsers[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	for _, method := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		if err := host.session.Call(ctx, method, nil, nil); err != nil {
			t.Fatalf("step=%s: %v", method, err)
		}
	}
	inject := fmt.Sprintf(`(() => {
  const child = document.createElement('iframe');
  child.src = %q;
  document.body.appendChild(child);
  return true;
})()`, ordinary+"/ordinary")
	var injectOut struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
	}
	if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": inject, "returnByValue": true}, &injectOut); err != nil || !injectOut.Result.Value {
		t.Fatalf("step=inject-ordinary-child: err=%v value=%t", err, injectOut.Result.Value)
	}
	time.Sleep(1200 * time.Millisecond)

	var probe struct {
		Steps []struct {
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"steps"`
		GetDocumentOk         bool `json:"getDocumentOk"`
		GetDocumentRefused    bool `json:"getDocumentRefused"`
		Refused               bool `json:"refused"`
		DeliveredMarkerAbsent bool `json:"deliveredMarkerAbsent"`
		TreeNodeCount         int  `json:"treeNodeCount"`
		ContentDocuments      int  `json:"contentDocuments"`
		MarkerHits            int  `json:"markerHits"`
	}
	var probeRaw rawJSON
	env.p.evalWithin("linkTest.childDocPierce("+jsArgs(map[string]any{
		"browser": browser, "hostTarget": host.target, "marker": marker, "runtimeEnable": true, "callTimeoutMs": 30000,
	})+")", &probeRaw, 45*time.Second)
	if err := json.Unmarshal(probeRaw, &probe); err != nil {
		t.Fatalf("step=probe-parse: %v raw=%s", err, probeRaw)
	}
	for _, s := range probe.Steps {
		t.Logf("step=%s ok=%t err=%q", s.Name, s.OK, s.Error)
	}
	lines := guardLines(env.a)
	framesProt, _, measureRefused, _, _, _, reason, measureOK := childDocParseMeasure(lines)
	t.Logf("ci-proof ordinary-children measure framesProtected=%t getDocumentRefused=%t measureLogged=%t reason=%q getDocumentOk=%t treeNodeCount=%d",
		framesProt, measureRefused, measureOK, reason, probe.GetDocumentOk, probe.TreeNodeCount)
	if !probe.GetDocumentOk || measureRefused || probe.GetDocumentRefused {
		t.Fatalf("step=admission: ordinary children must admit (ok=%t measureRefused=%t probeRefused=%t framesProtected=%t); steps=%v",
			probe.GetDocumentOk, measureRefused, probe.GetDocumentRefused, framesProt, probe.Steps)
	}
	if probe.TreeNodeCount < 1 {
		t.Fatalf("step=tree-shape: want delivered ordinary tree; nodes=%d", probe.TreeNodeCount)
	}
	_ = hrExplicitClientClose(env)
}

func TestCDPChildDocChromeBigOrdinaryPage(t *testing.T) {
	childDocRequireChrome(t)
	fixture := childDocFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	seven := "http://seven.protected.test:" + port
	ordinary := "http://page.ordinary.test:" + port
	b := launchBrowserOn(t, 0,
		"--site-per-process",
		"--unsafely-treat-insecure-origin-as-secure="+seven,
		"--host-resolver-rules=MAP *.test 127.0.0.1",
	)
	host, err := b.tryOpenPage(t, ordinary+"/ordinary-big")
	if err != nil {
		t.Fatalf("step=open-ordinary-big: %v", err)
	}
	linkHost, err := b.tryOpenPage(t, seven+"/seven-host")
	if err != nil {
		t.Fatalf("step=open-link-host: %v", err)
	}
	env := &linkEnv{t: t, b: b, origin: seven, p: linkHost}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", seven, "--name", "childdoc-chrome-big")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step=linkTest.ready: browsers=%d", len(hello.Browsers))
	}
	browser := hello.Browsers[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)

	for _, method := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		if err := host.session.Call(ctx, method, nil, nil); err != nil {
			t.Fatalf("step=%s: %v", method, err)
		}
	}
	var hrefOut struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &hrefOut); err != nil || !strings.HasPrefix(hrefOut.Result.Value, ordinary) {
		t.Fatalf("step=confirm-ordinary-big: err=%v href=%q", err, hrefOut.Result.Value)
	}
	build := `(() => {
  const host = document.getElementById('host');
  let parent = host;
  for (let d = 0; d < 100; d++) {
    const wrap = document.createElement('div');
    wrap.dataset.depth = String(d);
    for (let i = 0; i < 200; i++) {
      const el = document.createElement('span');
      el.textContent = 'x';
      wrap.appendChild(el);
    }
    parent.appendChild(wrap);
    parent = wrap;
  }
  return document.getElementsByTagName('*').length;
})()`
	var built struct {
		Result struct {
			Value float64 `json:"value"`
		} `json:"result"`
	}
	if err := host.session.Call(ctx, "Runtime.evaluate", map[string]any{"expression": build, "returnByValue": true}, &built); err != nil || built.Result.Value < 20000 {
		t.Fatalf("step=build-big-dom: err=%v elements=%v", err, built.Result.Value)
	}
	t.Logf("ci-proof big-page elements=%v", built.Result.Value)

	rawCount := childDocCountPierceNodes(t, ctx, host)
	t.Logf("ci-proof big-page rawNodeCount=%d", rawCount)

	var probeRaw rawJSON
	env.p.evalWithin("linkTest.childDocPierce("+jsArgs(map[string]any{
		"browser": browser, "hostTarget": host.target, "callTimeoutMs": 120000,
	})+")", &probeRaw, 150*time.Second)
	var probe struct {
		GetDocumentOk      bool `json:"getDocumentOk"`
		GetDocumentRefused bool `json:"getDocumentRefused"`
		TreeNodeCount      int  `json:"treeNodeCount"`
	}
	if err := json.Unmarshal(probeRaw, &probe); err != nil {
		t.Fatalf("step=probe-parse: %v raw=%s", err, probeRaw)
	}
	t.Logf("ci-proof big-page guardedOk=%t refused=%t guardedNodeCount=%d rawNodeCount=%d",
		probe.GetDocumentOk, probe.GetDocumentRefused, probe.TreeNodeCount, rawCount)
	if !probe.GetDocumentOk || probe.GetDocumentRefused {
		t.Fatalf("step=big-page-admit: ordinary big pierce must admit; ok=%t refused=%t", probe.GetDocumentOk, probe.GetDocumentRefused)
	}
	if probe.TreeNodeCount != rawCount {
		t.Fatalf("step=big-page-parity: guarded node count %d != raw %d", probe.TreeNodeCount, rawCount)
	}
	_ = hrExplicitClientClose(env)
}

func TestCDPChildDocChromeFlattenedSameProcess(t *testing.T) {
	childDocRequireChrome(t)
	fixture := childDocFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	seven := "http://seven.protected.test:" + port
	ordinary := "http://page.ordinary.test:" + port
	const marker = "CD_CHILD_MARKER_a1b2"
	b := launchBrowserOn(t, 0,
		"--disable-site-isolation-trials",
		"--unsafely-treat-insecure-origin-as-secure="+seven,
		"--host-resolver-rules=MAP *.test 127.0.0.1",
	)
	host, err := b.tryOpenPage(t, ordinary+"/ordinary")
	if err != nil {
		t.Fatalf("step=open-ordinary-host: %v", err)
	}
	linkHost, err := b.tryOpenPage(t, seven+"/seven-host")
	if err != nil {
		t.Fatalf("step=open-link-host: %v", err)
	}
	env := &linkEnv{t: t, b: b, origin: seven, p: linkHost}
	env.a = startAttach(t, "--cdp", b.endpoint, "--origin", seven, "--name", "childdoc-chrome-flattened-same")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step=linkTest.ready: browsers=%d", len(hello.Browsers))
	}
	browser := hello.Browsers[0].ID
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)

	for _, method := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		if err := host.session.Call(ctx, method, nil, nil); err != nil {
			t.Fatalf("step=%s: %v", method, err)
		}
	}
	inject := fmt.Sprintf(`(() => {
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
		t.Fatalf("step=inject-protected-child: err=%v value=%t", err, injectOut.Result.Value)
	}
	time.Sleep(1200 * time.Millisecond)

	var rawFlat struct {
		Nodes json.RawMessage `json:"nodes"`
	}
	rawErr := host.session.Call(ctx, "DOM.getFlattenedDocument", map[string]any{"depth": -1, "pierce": true}, &rawFlat)
	rawBlob := string(rawFlat.Nodes)
	rawMarkerPresent := rawErr == nil && strings.Contains(rawBlob, marker)
	t.Logf("ci-proof flattened-same-process rawErr=%v rawMarkerPresent=%t rawNodesBytes=%d", rawErr, rawMarkerPresent, len(rawBlob))
	if rawErr != nil {
		t.Fatalf("step=raw-getFlattenedDocument: control call failed: %v", rawErr)
	}
	if !rawMarkerPresent {
		t.Fatalf("step=raw-marker: same-process flattened control must show child marker")
	}

	var probe struct {
		Steps []struct {
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"steps"`
		GetFlattenedOk        bool `json:"getFlattenedOk"`
		GetFlattenedRefused   bool `json:"getFlattenedRefused"`
		Refused               bool `json:"refused"`
		DeliveredMarkerAbsent bool `json:"deliveredMarkerAbsent"`
		NodeCount             int  `json:"nodeCount"`
		MarkerHits            int  `json:"markerHits"`
	}
	var probeRaw rawJSON
	env.p.evalWithin("linkTest.childDocFlattened("+jsArgs(map[string]any{
		"browser": browser, "hostTarget": host.target, "marker": marker, "runtimeEnable": true, "callTimeoutMs": 30000,
	})+")", &probeRaw, 45*time.Second)
	if err := json.Unmarshal(probeRaw, &probe); err != nil {
		t.Fatalf("step=probe-parse: %v raw=%s", err, probeRaw)
	}
	for _, s := range probe.Steps {
		t.Logf("step=%s ok=%t err=%q", s.Name, s.OK, s.Error)
	}
	lines := guardLines(env.a)
	framesProt, _, _, _, _, _, _, _ := childDocParseMeasure(lines)
	t.Logf("ci-proof flattened-same-process framesProtected=%t guardedOk=%t guardedRefused=%t markerHits=%d nodeCount=%d",
		framesProt, probe.GetFlattenedOk, probe.GetFlattenedRefused, probe.MarkerHits, probe.NodeCount)
	if !probe.GetFlattenedRefused || probe.GetFlattenedOk {
		t.Fatalf("step=assert-admit-refusal: framesProtected DOM.getFlattenedDocument must be refused at admit (ok=%t refused=%t framesProtected=%t markerHits=%d); steps=%v",
			probe.GetFlattenedOk, probe.GetFlattenedRefused, framesProt, probe.MarkerHits, probe.Steps)
	}
	_ = hrExplicitClientClose(env)
}

func childDocCountPierceNodes(t *testing.T, ctx context.Context, host *page) int {
	t.Helper()
	var doc struct {
		Root map[string]any `json:"root"`
	}
	if err := host.session.Call(ctx, "DOM.getDocument", map[string]any{"depth": -1, "pierce": true}, &doc); err != nil {
		t.Fatalf("step=raw-getDocument: %v", err)
	}
	var walk func(any) int
	walk = func(v any) int {
		m, ok := v.(map[string]any)
		if !ok {
			return 0
		}
		n := 1
		if c, ok := m["contentDocument"]; ok {
			n += walk(c)
		}
		if kids, ok := m["children"].([]any); ok {
			for _, k := range kids {
				n += walk(k)
			}
		}
		if roots, ok := m["shadowRoots"].([]any); ok {
			for _, r := range roots {
				n += walk(r)
			}
		}
		return n
	}
	return walk(doc.Root)
}
