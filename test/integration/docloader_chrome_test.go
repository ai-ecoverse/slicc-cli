package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type docLoaderWire struct {
	seq     int
	at      time.Time
	in      bool
	id      int64
	method  string
	session string
	raw     []byte
}

type docLoaderTap struct {
	mu    sync.Mutex
	wires []docLoaderWire
}

func (d *docLoaderTap) add(w docLoaderWire) {
	d.mu.Lock()
	defer d.mu.Unlock()
	w.seq = len(d.wires)
	w.at = time.Now()
	d.wires = append(d.wires, w)
}

func (d *docLoaderTap) snapshot() []docLoaderWire {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]docLoaderWire(nil), d.wires...)
}

func docLoaderTopLoader(w docLoaderWire, target string) (string, bool) {
	if w.in {
		return "", false
	}
	var m struct {
		Method string `json:"method"`
		Params struct {
			Frame struct {
				ID       string `json:"id"`
				ParentID string `json:"parentId"`
				LoaderID string `json:"loaderId"`
			} `json:"frame"`
		} `json:"params"`
		Result struct {
			FrameTree struct {
				Frame struct {
					ID       string `json:"id"`
					LoaderID string `json:"loaderId"`
				} `json:"frame"`
			} `json:"frameTree"`
		} `json:"result"`
	}
	if json.Unmarshal(w.raw, &m) != nil {
		return "", false
	}
	if m.Method == "Page.frameNavigated" && m.Params.Frame.ID == target && m.Params.Frame.ParentID == "" {
		return m.Params.Frame.LoaderID, true
	}
	if m.Result.FrameTree.Frame.ID == target {
		return m.Result.FrameTree.Frame.LoaderID, true
	}
	return "", false
}

type docLoaderProof struct {
	admitted, tracker, source string
	infoAnswered              bool
}

func docLoaderReadProof(wires []docLoaderWire, target, reader, client, marker, method string) (docLoaderProof, bool) {
	var p docLoaderProof
	evalIn, evalOut := -1, -1
	var evalID int64
	var evalSession string
	if method == "" {
		method = "Runtime.evaluate"
	}
	for i, w := range wires {
		if evalIn < 0 && w.in && w.method == method && w.session == client {
			if method == "Runtime.evaluate" && !strings.Contains(string(w.raw), marker) {
				continue
			}
			evalIn, evalID, evalSession = i, w.id, w.session
			continue
		}
		if evalIn >= 0 && !w.in && w.id == evalID && w.session == evalSession && w.method == "" {
			evalOut = i
			break
		}
	}
	if evalIn < 0 || evalOut < 0 {
		return p, false
	}
	for _, w := range wires[:evalIn] {
		if l, ok := docLoaderTopLoader(w, target); ok && l != "" && w.session != reader {
			p.admitted = l
		}
	}
	settleAt := wires[evalOut].at.Add(docLoaderBound)
	var infoCmd *docLoaderWire
	for i := evalOut; i < len(wires) && infoCmd == nil; i++ {
		w := wires[i]
		if w.in && w.method == "Target.getTargetInfo" && strings.Contains(string(w.raw), target) {
			infoCmd = &wires[i]
		}
	}
	if infoCmd != nil {
		for _, w := range wires[infoCmd.seq:] {
			if !w.in && w.id == infoCmd.id && w.session == infoCmd.session && w.method == "" {
				p.infoAnswered = true
				settleAt = w.at
				break
			}
		}
	}
	for _, w := range wires {
		if w.at.After(settleAt) {
			break
		}
		if l, ok := docLoaderTopLoader(w, target); ok && l != "" && w.session != reader {
			p.tracker = l
		}
	}
	p.source = "unknown"
	if p.tracker != "" {
		p.source = "tracker"
	}
	return p, true
}

const docLoaderBound = time.Second

func (p docLoaderProof) current() string {
	if p.source == "tracker" {
		return p.tracker
	}
	return ""
}

func TestCDPDocLoaderRacesRealReload(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("step launch: CI Chrome required for the document loader row")
		}
		t.Skip(harnessChromeNotice)
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, "<!doctype html><title>doc loader</title><body>doc loader %d", time.Now().UnixNano())
	}))
	t.Cleanup(fixture.Close)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	page := "http://page.ordinary.test:" + port + "/reload"
	srv := pageServer(t)
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.test 127.0.0.1")
	gate := newGuardGate(t, b.endpoint, srv.URL)
	tap := &docLoaderTap{}
	gate.observe = func(c gateCommand) {
		tap.add(docLoaderWire{in: true, id: c.ID, method: c.Method, session: c.Session, raw: append([]byte(nil), c.Params...)})
	}
	gate.transform = func(data []byte) []byte {
		var m struct {
			ID      *int64 `json:"id"`
			Method  string `json:"method"`
			Session string `json:"sessionId"`
		}
		if json.Unmarshal(data, &m) == nil {
			w := docLoaderWire{method: m.Method, session: m.Session, raw: append([]byte(nil), data...)}
			if m.ID != nil {
				w.id = *m.ID
			}
			tap.add(w)
		}
		return data
	}
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--name", "doc-loader-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step hello: browsers %+v", hello.Browsers)
	}
	ws := strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser"
	for _, c := range []struct {
		name, method string
		busyMs       int
	}{
		{"covered-dom-getdocument", "DOM.getDocument", 0},
		{"evaluate-chrome-nav-error", "Runtime.evaluate", 600},
	} {
		t.Run(c.name, func(t *testing.T) {
			tap.mu.Lock()
			tap.wires = nil
			tap.mu.Unlock()
			opts := map[string]any{
				"browser": hello.Browsers[0].ID, "ws": ws, "page": page,
				"busyMs": c.busyMs, "reloadAfterMs": 150, "method": c.method,
			}
			var raw json.RawMessage
			env.p.evalWithin("linkTest.docLoader("+jsArgs(opts)+")", &raw, 120*time.Second)
			var row struct {
				Steps         []json.RawMessage `json:"steps"`
				TargetID      string            `json:"targetId"`
				ReaderSession string            `json:"readerSession"`
				ClientSession string            `json:"clientSession"`
				Control       bool              `json:"control"`
				Before        string            `json:"before"`
				RacedOK       bool              `json:"racedOk"`
				RacedValue    string            `json:"racedValue"`
				RacedError    string            `json:"racedError"`
				ReaderLoaders []string          `json:"readerLoaders"`
				After         string            `json:"after"`
				Usable        bool              `json:"usable"`
				Method        string            `json:"method"`
			}
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatalf("step result: %v\n%s", err, raw)
			}
			for _, s := range row.Steps {
				t.Logf("step %s", s)
			}
			lines := guardLines(env.a)
			dropLine := ""
			for _, line := range strings.Split(lines, "\n") {
				if strings.Contains(line, "target="+row.TargetID) && strings.Contains(line, "a held reply's document was replaced") {
					dropLine = line
				}
			}
			marker := "docloader-race"
			proof, seen := docLoaderReadProof(tap.snapshot(), row.TargetID, row.ReaderSession, row.ClientSession, marker, c.method)
			outcome := "unexpected"
			switch {
			case !row.RacedOK && row.RacedError == "Execution context was destroyed.":
				outcome = "dropped"
			case !row.RacedOK && row.RacedError == "Inspected target navigated or closed":
				outcome = "preempted"
			case c.method == "Runtime.evaluate" && row.RacedOK && strings.HasPrefix(row.RacedValue, "docloader-race "):
				outcome = "delivered"
			case c.method != "Runtime.evaluate" && row.RacedOK && row.RacedValue != "":
				outcome = "delivered"
			}
			proofLines := []string{
				fmt.Sprintf("ci-proof doc-loader case=%s method=%s outcome=%s raced-ok=%t value=%q error=%q before=%s after=%s usable=%t", c.name, c.method, outcome, row.RacedOK, row.RacedValue, row.RacedError, row.Before, row.After, row.Usable),
				fmt.Sprintf("ci-proof doc-loader case=%s wire-seen=%t admitted=%q settle=%q info-answered=%t reader-loaders=%v", c.name, seen, proof.admitted, proof.current(), proof.infoAnswered, row.ReaderLoaders),
				fmt.Sprintf("ci-proof doc-loader case=%s guard-drop=%q", c.name, dropLine),
			}
			for _, l := range proofLines {
				t.Log(l)
			}
			fail := func(format string, args ...any) {
				t.Fatalf(format+"\n%s\n%s\nguard:\n%s", append(args, strings.Join(proofLines, "\n"), raw, lines)...)
			}
			replaced := proof.admitted != "" && proof.current() != "" && proof.admitted != proof.current()
			named := map[string]bool{"delivered": true, "dropped": true, "preempted": true}
			switch {
			case !row.Control:
				fail("step control: the client could not evaluate on the ordinary page before the race")
			case len(row.ReaderLoaders) == 0:
				fail("step reload: the observer saw no new document commit")
			case !seen:
				fail("step wire: the read and its reply were not seen on the node's connection")
			case proof.admitted == "":
				fail("step admission: the tracker had no loader after the warm navigation")
			case !named[outcome]:
				fail("step outcome: raced read outcome %q is not delivered, dropped or preempted", outcome)
			case c.method == "Runtime.evaluate" && outcome != "preempted":
				fail("step evaluate-pin: Runtime.evaluate must observe Chrome's Inspected target navigated or closed (got %s)", outcome)
			case strings.Contains(row.RacedError, "slicc: refused"):
				fail("step no-cut: the raced read was refused instead of dropped with the retriable text")
			case !row.Usable:
				fail("step usable: the client session could not evaluate after the race, so the node cut it")
			case dropLine != "" && outcome != "dropped":
				fail("step drop: the node logged a replaced document but the client did not get the retriable text")
			case outcome == "delivered" && proof.current() != "" && replaced:
				fail("step delivered: the node delivered a reply although the loader at admission and the tracker's at settle differ")
			}
		})
	}
}

type docLoaderKeepRow struct {
	Steps         []json.RawMessage `json:"steps"`
	Mode          string            `json:"mode"`
	TargetID      string            `json:"targetId"`
	ReaderSession string            `json:"readerSession"`
	ClientSession string            `json:"clientSession"`
	Control       bool              `json:"control"`
	FirstLoader   string            `json:"firstLoader"`
	LastLoader    string            `json:"lastLoader"`
	History       struct {
		Used           bool   `json:"used"`
		RestoreType    string `json:"restoreType"`
		RestoredLoader string `json:"restoredLoader"`
		SecondLoader   string `json:"secondLoader"`
		Back           string `json:"back"`
	} `json:"history"`
	Before          string   `json:"before"`
	RacedOK         bool     `json:"racedOk"`
	RacedValue      string   `json:"racedValue"`
	RacedError      string   `json:"racedError"`
	ReaderTopLoader []string `json:"readerTopLoaders"`
	ChildNavs       int      `json:"childNavs"`
	SameDocNavs     int      `json:"sameDocNavs"`
	After           string   `json:"after"`
	Usable          bool     `json:"usable"`
}

func TestCDPDocLoaderKeepsUnreplacedDocuments(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("step launch: CI Chrome required for the document loader keep rows")
		}
		t.Skip(harnessChromeNotice)
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case r.URL.Path == "/frame":
			w.Header().Set("Cache-Control", "no-store")
			_, _ = fmt.Fprint(w, `<!doctype html><title>doc loader frame</title><body>top<iframe src="/child?n=1"></iframe>`)
		case r.URL.Path == "/child":
			w.Header().Set("Cache-Control", "no-store")
			_, _ = fmt.Fprintf(w, "<!doctype html><title>child</title><body>child %s", r.URL.RawQuery)
		case strings.HasPrefix(r.URL.Path, "/history"):
			_, _ = fmt.Fprintf(w, "<!doctype html><title>doc loader %s</title><body>%s", r.URL.Path, r.URL.Path)
		default:
			w.Header().Set("Cache-Control", "no-store")
			_, _ = fmt.Fprintf(w, "<!doctype html><title>doc loader</title><body>doc loader %d", time.Now().UnixNano())
		}
	}))
	t.Cleanup(fixture.Close)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	base := "http://page.ordinary.test:" + port
	srv := pageServer(t)
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.test 127.0.0.1")
	gate := newGuardGate(t, b.endpoint, srv.URL)
	tap := &docLoaderTap{}
	gate.observe = func(c gateCommand) {
		tap.add(docLoaderWire{in: true, id: c.ID, method: c.Method, session: c.Session, raw: append([]byte(nil), c.Params...)})
	}
	gate.transform = func(data []byte) []byte {
		var m struct {
			ID      *int64 `json:"id"`
			Method  string `json:"method"`
			Session string `json:"sessionId"`
		}
		if json.Unmarshal(data, &m) == nil {
			w := docLoaderWire{method: m.Method, session: m.Session, raw: append([]byte(nil), data...)}
			if m.ID != nil {
				w.id = *m.ID
			}
			tap.add(w)
		}
		return data
	}
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--name", "doc-loader-keep-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatalf("step hello: browsers %+v", hello.Browsers)
	}
	ws := strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser"
	for _, c := range []struct {
		name, mode, page string
	}{
		{"same-document-pushstate", "pushState", base + "/same"},
		{"same-document-hash", "hash", base + "/hash"},
		{"iframe-only", "iframe", base + "/frame"},
		{"history-back", "history", base + "/history-first"},
		{"same-document-pushstate-known", "pushState-known", base + "/same-known"},
		{"iframe-only-known", "iframe-known", base + "/frame"},
	} {
		t.Run(c.name, func(t *testing.T) {
			opts := map[string]any{
				"browser": hello.Browsers[0].ID, "ws": ws, "mode": c.mode, "page": c.page,
				"child": base + "/child?n=1", "child2": base + "/child?n=2", "second": base + "/history-second",
			}
			var raw json.RawMessage
			env.p.evalWithin("linkTest.docLoaderKeep("+jsArgs(opts)+")", &raw, 120*time.Second)
			var row docLoaderKeepRow
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatalf("step result: %v\n%s", err, raw)
			}
			for _, s := range row.Steps {
				t.Logf("step %s", s)
			}
			docLoaderKeepJudge(t, c.name, row, tap, guardLines(env.a), raw)
		})
	}
}

func docLoaderKeepJudge(t *testing.T, name string, row docLoaderKeepRow, tap *docLoaderTap, lines string, raw json.RawMessage) {
	t.Helper()
	marker := "docloader-" + row.Mode
	dropLine := ""
	for _, line := range strings.Split(lines, "\n") {
		if strings.Contains(line, "target="+row.TargetID) && strings.Contains(line, "a held reply's document was replaced") {
			dropLine = line
		}
	}
	proof, seen := docLoaderReadProof(tap.snapshot(), row.TargetID, row.ReaderSession, row.ClientSession, marker, "Runtime.evaluate")
	replaced := len(row.ReaderTopLoader) > 0 || (row.Before != "" && row.After != "" && row.Before != row.After)
	outcome := "unexpected"
	switch {
	case !row.RacedOK && row.RacedError == "Execution context was destroyed.":
		outcome = "dropped"
	case !row.RacedOK && strings.Contains(row.RacedError, "slicc: refused"):
		outcome = "refused"
	case row.RacedOK && row.RacedValue == marker+" "+row.Before:
		outcome = "delivered"
	case row.RacedOK && strings.HasPrefix(row.RacedValue, marker+" "):
		outcome = "delivered-other-document"
	}
	dropped := outcome == "dropped" || dropLine != ""
	proofLines := []string{
		fmt.Sprintf("ci-proof doc-loader case=%s outcome=%s replaced=%t admitted=%q settle=%q raced-ok=%t value=%q error=%q before=%s after=%s usable=%t", name, outcome, replaced, proof.admitted, proof.current(), row.RacedOK, row.RacedValue, row.RacedError, row.Before, row.After, row.Usable),
		fmt.Sprintf("ci-proof doc-loader case=%s wire-seen=%t info-answered=%t tracker=%q first-loader=%q last-loader=%q top-commits=%v child-navs=%d same-doc-navs=%d", name, seen, proof.infoAnswered, proof.tracker, row.FirstLoader, row.LastLoader, row.ReaderTopLoader, row.ChildNavs, row.SameDocNavs),
		fmt.Sprintf("ci-proof doc-loader case=%s history used=%t back=%q restore-type=%q second-loader=%q restored-loader=%q", name, row.History.Used, row.History.Back, row.History.RestoreType, row.History.SecondLoader, row.History.RestoredLoader),
		fmt.Sprintf("ci-proof doc-loader case=%s guard-drop=%q", name, dropLine),
	}
	for _, l := range proofLines {
		t.Log(l)
	}
	fail := func(format string, args ...any) {
		t.Fatalf(format+"\n%s\n%s\nguard:\n%s", append(args, strings.Join(proofLines, "\n"), raw, lines)...)
	}
	loadersDiffer := seen && proof.admitted != "" && proof.current() != "" && proof.admitted != proof.current()
	switch {
	case strings.HasSuffix(row.Mode, "-known") && proof.admitted == "":
		fail("step admission: known-admission row had an empty loader at the read")
	case dropped && !replaced:
		fail("ci-proof doc-loader FALSE-DROP case=%s: the read was dropped while the top document was not replaced (outcome=%s guard-drop=%t admitted=%q settle=%q)", name, outcome, dropLine != "", proof.admitted, proof.current())
	case !row.Control:
		fail("step control: the client could not evaluate on the ordinary page before the read")
	case strings.HasPrefix(row.Mode, "pushState") && row.SameDocNavs < 1, row.Mode == "hash" && row.SameDocNavs < 1:
		fail("step navigation: the observer saw no same-document navigation")
	case strings.HasPrefix(row.Mode, "iframe") && row.ChildNavs < 1:
		fail("step navigation: the observer saw no child frame commit")
	case row.Mode == "history" && (row.History.Back != "ok" || row.History.RestoreType == ""):
		fail("step navigation: the history step did not commit the earlier page back")
	case row.Mode != "history" && replaced:
		fail("step replaced: the top document was replaced although only a same-document or child navigation ran")
	case row.Mode != "history" && loadersDiffer:
		fail("step loaders: the top-frame loader at admission and at settle differ without a document replacement")
	case row.Mode == "history" && loadersDiffer && !replaced:
		fail("ci-proof doc-loader FALSE-DROP case=%s: the loaders differ although the restored document was not replaced during the read", name)
	case !seen:
		fail("step wire: the read and its reply were not seen on the node's connection")
	case outcome != "delivered":
		fail("step delivered: the read on an unreplaced document was not delivered (outcome %s)", outcome)
	case !row.Usable:
		fail("step usable: the client session could not evaluate after the read")
	}
}
