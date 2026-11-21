package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	vsloopTree   = "main"
	vsloopRowID  = "C8.close-protected.view-source"
	vsloopBudget = 170 * time.Second
)

type vsloopLine struct {
	at   time.Duration
	text string
}

type vsloopRec struct {
	mu    sync.Mutex
	start time.Time
	gate  []vsloopLine
	guard []vsloopLine
	tail  []byte
}

func (r *vsloopRec) since() time.Duration { return time.Since(r.start) }

func (r *vsloopRec) Write(p []byte) (int, error) {
	at := r.since()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tail = append(r.tail, p...)
	for {
		i := bytes.IndexByte(r.tail, '\n')
		if i < 0 {
			break
		}
		r.guard = append(r.guard, vsloopLine{at: at, text: string(r.tail[:i])})
		r.tail = r.tail[i+1:]
	}
	return len(p), nil
}

func vsloopClip(data []byte) string {
	if len(data) <= 4096 {
		return string(data)
	}
	return string(data[:2048]) + " ... " + string(data[len(data)-768:])
}

var vsloopUpKeep = [][]byte{
	[]byte(`"method":"Target.`),
	[]byte(`"method":"Page.frameNavigated"`),
	[]byte(`"method":"Page.frameStartedNavigating"`),
	[]byte(`"method":"Page.frameRequestedNavigation"`),
	[]byte(`"method":"Inspector.detached"`),
	[]byte(`"result":{"sessionId"`),
	[]byte(`"result":{"targetInfo"`),
	[]byte(`"error":{`),
}

func (r *vsloopRec) up(data []byte) []byte {
	for _, k := range vsloopUpKeep {
		if bytes.Contains(data, k) {
			at := r.since()
			s := "< " + vsloopClip(data)
			r.mu.Lock()
			r.gate = append(r.gate, vsloopLine{at: at, text: s})
			r.mu.Unlock()
			break
		}
	}
	return data
}

func (r *vsloopRec) down(m gateCommand) {
	at := r.since()
	params := string(m.Params)
	if len(params) > 2048 {
		params = params[:2048] + "..."
	}
	if params == "" {
		params = "null"
	}
	s := fmt.Sprintf(`> {"id":%d,"method":%q,"sessionId":%q,"params":%s}`, m.ID, m.Method, m.Session, params)
	r.mu.Lock()
	r.gate = append(r.gate, vsloopLine{at: at, text: s})
	r.mu.Unlock()
}

func (r *vsloopRec) snapshot() (gate, guard []vsloopLine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]vsloopLine(nil), r.gate...), append([]vsloopLine(nil), r.guard...)
}

func vsloopAttach(t *testing.T, rec *vsloopRec, args ...string) *attachProc {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"attach"}, args...)...)
	cmd.Env = append(os.Environ(), "SLICC_DEBUG=1")
	a := &attachProc{cmd: cmd, stderr: &syncBuffer{}, out: &syncBuffer{}}
	cmd.Stderr = &vsloopTee{a: a.stderr, b: rec}
	cmd.Stdout = a.out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reap(t, cmd, "slicc attach") })
	return a
}

type vsloopTee struct {
	a *syncBuffer
	b *vsloopRec
}

func (w *vsloopTee) Write(p []byte) (int, error) {
	_, _ = w.b.Write(p)
	return w.a.Write(p)
}

const vsloopShapeAttachOld = "async function attachClient(client, targetId) {\n  const attached = await outcome(client.send('Target.attachToTarget', { targetId, flatten: true }));\n  const session = attached.result?.sessionId;\n  if (session) {\n    await outcome(client.send('Page.enable', {}, session));\n    await outcome(client.send('Runtime.enable', {}, session));\n  }\n  return { session, attachRefused: refused(attached) };\n}\n"

const vsloopShapeAttachNew = `const vsloopRecs = new Map();
const vsloopClip = (s) => (s.length > 500 ? s.slice(0, 500) + '...' : s);
const vsloopTimed = (arr) => {
  if (!Array.isArray(arr)) return null;
  if (!arr.__vsloopTimes) {
    const times = new Array(arr.length).fill(0);
    Object.defineProperty(arr, '__vsloopTimes', { value: times });
    Object.defineProperty(arr, 'push', {
      value: function (...items) {
        for (let i = 0; i < items.length; i++) times.push(Date.now());
        return Array.prototype.push.apply(this, items);
      },
      writable: true,
      configurable: true,
    });
  }
  return arr.__vsloopTimes;
};
const vsloopOut = (o) =>
  o.ok
    ? { ok: true, sessionId: o.result?.sessionId ?? '' }
    : { ok: false, message: String(o.error?.message ?? o.error), code: o.error?.code ?? null };
function vsloopFlush(rec) {
  const mt = rec.messages?.__vsloopTimes ?? [];
  const st = rec.sent?.__vsloopTimes ?? [];
  const keys = [rec.targetId, rec.session].filter(Boolean);
  const recv = [];
  for (let i = rec.m0; i < (rec.messages?.length ?? 0) && recv.length < 40; i++) {
    const text = String(rec.messages[i]);
    let hit = keys.some((k) => text.includes(k));
    if (!hit && rec.wireId != null) {
      try {
        const m = JSON.parse(text);
        hit = m.id === rec.wireId && !m.sessionId;
      } catch {}
    }
    if (hit) recv.push({ at: mt[i] ?? 0, text: vsloopClip(text) });
  }
  const sent = [];
  for (let i = rec.s0; i < (rec.sent?.length ?? 0) && sent.length < 40; i++) {
    const m = rec.sent[i];
    sent.push({ at: st[i] ?? 0, id: m?.id ?? null, method: m?.method ?? '', sessionId: m?.sessionId ?? '', params: vsloopClip(JSON.stringify(m?.params ?? {})) });
  }
  rec.notes.push('vsloop.client: ' + JSON.stringify({ targetId: rec.targetId, t0: rec.t0, t1: rec.t1, wireId: rec.wireId, attach: rec.attach, session: rec.session, steps: rec.steps, close: rec.close, recv, sent }));
}
async function attachClient(client, targetId, notes) {
  vsloopTimed(client.messages);
  vsloopTimed(client.sent);
  const rec = { targetId, notes, messages: client.messages, sent: client.sent, m0: client.messages?.length ?? 0, s0: client.sent?.length ?? 0, steps: [] };
  rec.t0 = Date.now();
  const attached = await outcome(client.send('Target.attachToTarget', { targetId, flatten: true }));
  rec.t1 = Date.now();
  rec.attach = vsloopOut(attached);
  rec.wireId = client.sent?.[rec.s0]?.id ?? null;
  vsloopRecs.set(targetId, rec);
  const session = attached.result?.sessionId;
  rec.session = session ?? '';
  if (session) {
    const pe = await outcome(client.send('Page.enable', {}, session));
    rec.steps.push({ method: 'Page.enable', at: Date.now(), ...vsloopOut(pe) });
    const re = await outcome(client.send('Runtime.enable', {}, session));
    rec.steps.push({ method: 'Runtime.enable', at: Date.now(), ...vsloopOut(re) });
  }
  return { session, attachRefused: refused(attached) };
}
`

const vsloopShapeCloseOld = "  result.closed = gone;\n  result.closeRefused = refused(close) && !gone;\n}\n"

const vsloopShapeCloseNew = `  const rec = vsloopRecs.get(targetId);
  if (rec?.notes) {
    vsloopRecs.delete(targetId);
    rec.close = { ...vsloopOut(close), gone, at: Date.now() };
    vsloopFlush(rec);
  }
  result.closed = gone;
  result.closeRefused = refused(close) && !gone;
}
`

const vsloopShapeCallOld = "const { session, attachRefused } = await attachClient(client, targetId);"

func vsloopStageShape(t *testing.T, pkg string) {
	t.Helper()
	path := filepath.Join(pkg, "close-protected-shape.mjs")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, needle := range []string{vsloopShapeAttachOld, vsloopShapeCloseOld, vsloopShapeCallOld} {
		if strings.Count(text, needle) != 1 {
			t.Fatalf("close-protected-shape.mjs changed shape; the client recorder can't be staged: %q", needle[:min(60, len(needle))])
		}
	}
	text = strings.Replace(text, vsloopShapeAttachOld, vsloopShapeAttachNew, 1)
	text = strings.Replace(text, vsloopShapeCloseOld, vsloopShapeCloseNew, 1)
	text = strings.Replace(text, vsloopShapeCallOld, "const { session, attachRefused } = await attachClient(client, targetId, notes);", 1)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func vsloopStageVectors(t *testing.T, pkg, target string, n, offset int) {
	t.Helper()
	path := filepath.Join(pkg, "guard-vectors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var set map[string]json.RawMessage
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	var vectors []map[string]any
	if err := json.Unmarshal(set["vectors"], &vectors); err != nil {
		t.Fatal(err)
	}
	var row []byte
	for _, v := range vectors {
		if v["id"] == vsloopRowID {
			row, _ = json.Marshal(v)
		}
	}
	if row == nil {
		t.Fatalf("the guard set has no %s", vsloopRowID)
	}
	copies := make([]map[string]any, n)
	for i := range copies {
		var v map[string]any
		_ = json.Unmarshal(row, &v)
		v["id"] = fmt.Sprintf("%s#%03d", vsloopRowID, offset+i+1)
		v["targets"] = []string{target}
		copies[i] = v
	}
	set["vectors"], _ = json.Marshal(copies)
	out, _ := json.Marshal(set)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

type vsloopRun struct {
	target   string
	variants []string
	n        int
	offset   int
	part     int
}

func vsloopExecute(t *testing.T, run vsloopRun) ([]guardRow, *vsloopRec, bool, time.Duration) {
	t.Helper()
	pkg := stageCredentialExportProbeNotes(t, guardPackage(t))
	vsloopStageVectors(t, pkg, run.target, run.n, run.offset)
	vsloopStageShape(t, pkg)
	ext := filepath.Join("..", "..", "node_modules", "@ai-ecoverse", "slicc-shared-web", "harness", "extension")
	if _, err := os.Stat(filepath.Join(ext, "manifest.json")); err != nil {
		t.Fatalf("slicc-shared-web's harness extension is missing; run npm ci: %v", err)
	}
	ext = stage1GuardFixture(t, ext)
	fix := startGuardFixture(t)
	leaf := idpTLSCertificate(t)
	front := startDualFrontDoor(t, fix.port, leaf)
	srv := guardPageServer(t, pkg, fix.port)
	idp := startIdPTLS(t, fix.port, leaf)
	fp := front
	seven := "http://seven.guard.test:" + fp
	sevenOopif := "http://seven.oopif.test:" + fp
	targets := map[string]string{
		"seven":              seven,
		"sevenOopif":         sevenOopif,
		"vault":              "http://vault.sliccy.ai:" + fp,
		"vault-x":            "http://vault-x.sliccy.ai:" + fp,
		"apex":               "http://sliccy.ai:" + fp,
		"own":                srv.URL,
		"client":             srv.URL,
		"ordinary":           "http://ordinary.guard.test:" + fp,
		"ordinary-same-site": "http://page.guard.test:" + fp,
		"cross":              "http://cross.test:" + fp,
		"idp":                "https://ims-na1.adobelogin.com:" + idp.port,
		"idp-stg":            "https://ims-na1-stg1.adobelogin.com:" + idp.port,
	}
	n := guardSlots()
	rules := "--host-resolver-rules=" + strings.Join([]string{
		"MAP *.sliccy.ai 127.0.0.1",
		"MAP sliccy.ai 127.0.0.1",
		"MAP *.test 127.0.0.1",
		"MAP ims-na1.adobelogin.com 127.0.0.1",
		"MAP ims-na1-stg1.adobelogin.com 127.0.0.1",
		"MAP *.adobelogin.com 127.0.0.1",
		"MAP adobelogin.com 127.0.0.1",
		"MAP adobe.com 127.0.0.1",
		"MAP *.adobe.com 127.0.0.1",
	}, ",")
	secure := "--unsafely-treat-insecure-origin-as-secure=" + strings.Join([]string{targets["ordinary"], targets["seven"], targets["vault"], targets["sevenOopif"]}, ",")
	spki := "--ignore-certificate-errors-spki-list=" + idp.spki
	chromes := make([]*chromeProc, n)
	for i := range chromes {
		chromes[i] = launchExtensionBrowser(t, ext, rules, "--remote-allow-origins="+srv.URL, secure, spki, "--ignore-certificate-errors")
	}
	rec := &vsloopRec{start: time.Now()}
	gates := make([]*guardGate, n)
	for i, b := range chromes {
		gates[i] = newGuardGate(t, b.endpoint, srv.URL)
		gates[i].transform = rec.up
		gates[i].observe = rec.down
	}
	args := []string{"--cdp", gates[0].srv.URL}
	for _, g := range gates[1:] {
		args = append(args, "--cdp-extra", g.srv.URL)
	}
	args = append(args, "--origin", srv.URL, "--origin", seven, "--origin", sevenOopif, "--name", "it-node")
	env := &linkEnv{t: t, b: chromes[0], origin: srv.URL}
	env.a = vsloopAttach(t, rec, args...)
	env.p = chromes[0].openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != n {
		t.Fatalf("the link lists %d browsers, want %d: %+v", len(hello.Browsers), n, hello.Browsers)
	}
	type slot struct {
		ID   string `json:"id"`
		WS   string `json:"ws"`
		Gate string `json:"gate"`
	}
	slots := make([]slot, n)
	for i := range chromes {
		slots[i] = slot{ID: hello.Browsers[i].ID, WS: strings.Replace(gates[i].srv.URL, "http", "ws", 1) + "/browser", Gate: gates[i].srv.URL + "/gate"}
	}
	opts := map[string]any{
		"browsers":       slots,
		"targets":        targets,
		"capabilities":   guardCapabilities(),
		"variants":       run.variants,
		"parallel":       n,
		"log":            "http://127.0.0.1:" + strconv.Itoa(fix.log),
		"sha":            guardSHA(),
		"rowTimeoutMs":   int64(60000),
		"only":           "",
		"skips":          map[string]guardSkip{},
		"inject":         "",
		"errorPageVault": false,
		"settleScale":    guardSettleScale(),
	}
	var started struct {
		Started bool `json:"started"`
	}
	env.p.eval("linkTest.guard.start("+jsArgs(opts)+")", &started)
	if !started.Started {
		t.Fatal("the guard run didn't start")
	}
	want := run.n * len(run.variants)
	begin := time.Now()
	var rows []guardRow
	said := begin
	complete := false
	for deadline := begin.Add(vsloopBudget); ; time.Sleep(2 * time.Second) {
		var p guardProgress
		env.p.eval("linkTest.guard.progress("+strconv.Itoa(len(rows))+")", &p)
		rows = append(rows, p.Rows...)
		if time.Since(said) > 30*time.Second {
			said = time.Now()
			t.Logf("%d of %d rows after %s", len(rows), want, time.Since(begin).Round(time.Second))
		}
		if p.Error != "" {
			if len(rows) < want {
				t.Fatalf("the guard run failed after %d of %d rows: %s", len(rows), want, p.Error)
			}
			t.Logf("the guard run reported an error after all rows: %s", p.Error)
		}
		if p.Done || len(rows) >= want {
			complete = len(rows) >= want
			break
		}
		if time.Now().After(deadline) {
			for _, r := range p.Inflight {
				t.Logf("in flight at the budget: %s", r)
			}
			break
		}
	}
	took := time.Since(begin)
	t.Logf("%d of %d rows in %s with %d slots on %s", len(rows), want, took.Round(time.Second), n, runtime.GOOS)
	return rows, rec, complete, took
}

var vsloopNoteRe = regexp.MustCompile(`^guarded view-source: (\{.*\})$`)

func vsloopGuardedTarget(r guardRow) (string, string) {
	for _, note := range r.Notes {
		m := vsloopNoteRe.FindStringSubmatch(note)
		if m == nil {
			continue
		}
		var v struct {
			TargetID string `json:"targetId"`
			URL      string `json:"url"`
		}
		if json.Unmarshal([]byte(m[1]), &v) == nil {
			return v.TargetID, v.URL
		}
	}
	return "", ""
}

type vsloopMsg struct {
	ID        int64           `json:"id"`
	Method    string          `json:"method"`
	SessionID string          `json:"sessionId"`
	Params    json.RawMessage `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     json.RawMessage `json:"error"`
}

type vsloopInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	OpenerID string `json:"openerId"`
	Attached bool   `json:"attached"`
}

type vsloopTimeline struct {
	Target   string   `json:"target"`
	Pattern  string   `json:"pattern"`
	Shape    string   `json:"shape"`
	Path     string   `json:"path"`
	Marks    []string `json:"marks"`
	gate     []vsloopLine
	guard    []vsloopLine
	window   []vsloopLine
	sessions []string
	reached  []string
	Client   string `json:"client"`
	Decision string `json:"decision"`
}

func vsloopParse(line string) (bool, vsloopMsg, bool) {
	if len(line) < 2 {
		return false, vsloopMsg{}, false
	}
	up := line[0] == '<'
	body := line[2:]
	var m vsloopMsg
	if strings.Contains(body, " ... ") {
		return up, m, false
	}
	return up, m, json.Unmarshal([]byte(body), &m) == nil
}

var vsloopTargetRe = regexp.MustCompile(`target=([0-9A-F]{32})`)
var vsloopSessionRe = regexp.MustCompile(`session=([0-9A-F]{32})`)

type vsloopOutcome struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"sessionId,omitempty"`
	Message   string `json:"message,omitempty"`
	Code      *int   `json:"code,omitempty"`
	Method    string `json:"method,omitempty"`
	At        int64  `json:"at,omitempty"`
	Gone      *bool  `json:"gone,omitempty"`
}

type vsloopClient struct {
	TargetID string          `json:"targetId"`
	T0       int64           `json:"t0"`
	T1       int64           `json:"t1"`
	WireID   *int64          `json:"wireId"`
	Attach   vsloopOutcome   `json:"attach"`
	Session  string          `json:"session"`
	Steps    []vsloopOutcome `json:"steps"`
	Close    *vsloopOutcome  `json:"close"`
	Recv     []struct {
		At   int64  `json:"at"`
		Text string `json:"text"`
	} `json:"recv"`
	Sent []struct {
		At        int64  `json:"at"`
		ID        *int64 `json:"id"`
		Method    string `json:"method"`
		SessionID string `json:"sessionId"`
		Params    string `json:"params"`
	} `json:"sent"`
}

func vsloopClientOf(r guardRow) *vsloopClient {
	for _, note := range r.Notes {
		rest, ok := strings.CutPrefix(note, "guarded vsloop.client: ")
		if !ok {
			continue
		}
		var c vsloopClient
		if json.Unmarshal([]byte(rest), &c) == nil {
			return &c
		}
	}
	return nil
}

var vsloopHexRe = regexp.MustCompile(`[0-9A-F]{32}`)
var vsloopNumRe = regexp.MustCompile(`[0-9]+`)
var vsloopSidRe = regexp.MustCompile(`"sessionId":"([0-9A-F]{32})"`)

func vsloopNorm(msg string) string {
	return vsloopNumRe.ReplaceAllString(vsloopHexRe.ReplaceAllString(msg, "<id>"), "N")
}

func vsloopClientClass(c *vsloopClient) string {
	switch {
	case c == nil:
		return "unrecorded"
	case c.Attach.OK && c.Attach.SessionID != "":
		return "session"
	case c.Attach.OK:
		return "ok-without-session"
	case strings.HasPrefix(c.Attach.Message, "slicc: refused:"):
		return "refused(" + vsloopNorm(c.Attach.Message) + ")"
	default:
		return "error(" + vsloopNorm(c.Attach.Message) + ")"
	}
}

func vsloopBuild(target string, gate, guard []vsloopLine, start time.Time, cl *vsloopClient) vsloopTimeline {
	tl := vsloopTimeline{Target: target}
	if target == "" {
		tl.Pattern = "no guarded target in the notes"
		return tl
	}
	sessions := map[string]bool{}
	for _, l := range gate {
		if !strings.Contains(l.text, target) {
			continue
		}
		up, m, ok := vsloopParse(l.text)
		if !ok || !up || m.Method != "Target.attachedToTarget" {
			continue
		}
		var p struct {
			SessionID  string     `json:"sessionId"`
			TargetInfo vsloopInfo `json:"targetInfo"`
		}
		if json.Unmarshal(m.Params, &p) == nil && p.TargetInfo.TargetID == target && p.SessionID != "" {
			sessions[p.SessionID] = true
		}
	}
	for _, l := range guard {
		if !strings.Contains(l.text, "target="+target) {
			continue
		}
		for _, s := range vsloopSessionRe.FindAllStringSubmatch(l.text, -1) {
			sessions[s[1]] = true
		}
	}
	for s := range sessions {
		tl.sessions = append(tl.sessions, s)
	}
	sort.Strings(tl.sessions)
	hit := func(text string) bool {
		if strings.Contains(text, target) {
			return true
		}
		for s := range sessions {
			if strings.Contains(text, s) {
				return true
			}
		}
		return false
	}
	var first, last time.Duration = -1, 0
	for _, l := range gate {
		if hit(l.text) {
			tl.gate = append(tl.gate, l)
			if first < 0 {
				first = l.at
			}
			last = l.at
		}
	}
	for _, l := range guard {
		if hit(l.text) {
			tl.guard = append(tl.guard, l)
			if first < 0 {
				first = l.at
			}
			last = max(last, l.at)
		}
	}
	if first < 0 {
		tl.Pattern = "no gate or guard record for the guarded target"
		return tl
	}
	wall := func(ms int64) time.Duration { return time.UnixMilli(ms).Sub(start) }
	lo, hi := first-100*time.Millisecond, last+100*time.Millisecond
	if cl != nil && cl.T0 > 0 {
		lo = wall(cl.T0) - 20*time.Millisecond
		hi = wall(cl.T1) + 20*time.Millisecond
		if cl.Close != nil && cl.Close.At > 0 {
			hi = wall(cl.Close.At) + 20*time.Millisecond
		}
	}
	for _, l := range guard {
		if l.at < lo || l.at > hi {
			continue
		}
		if vsloopTargetRe.MatchString(l.text) {
			continue
		}
		for _, k := range []string{"attach", "confirm", "hold", "refused", "detach", "release"} {
			if strings.Contains(l.text, k) {
				tl.window = append(tl.window, l)
				break
			}
		}
	}
	zero := first
	navAt := time.Duration(-1)
	for _, l := range tl.gate {
		up, m, ok := vsloopParse(l.text)
		if ok && !up && m.Method == "Page.navigate" && strings.Contains(string(m.Params), "view-source:") {
			navAt = l.at
			zero = l.at
			break
		}
	}
	rel := func(at time.Duration) string {
		return fmt.Sprintf("%+dms", (at - zero).Milliseconds())
	}
	mark := func(at time.Duration, what string) { tl.Marks = append(tl.Marks, rel(at)+" "+what) }
	if navAt >= 0 {
		mark(navAt, "observer Page.navigate view-source")
	}
	attachCmds := 0
	var sliccAttachAt, vsInfoAt, askAt, answerAt, enableAt, detachAt time.Duration = -1, -1, -1, -1, -1, -1
	answerURL := ""
	var sliccSession, enableSession string
	lastURL := ""
	attachedEvents := 0
	for _, l := range tl.gate {
		up, m, ok := vsloopParse(l.text)
		if !ok {
			continue
		}
		switch {
		case !up && m.Method == "Target.attachToTarget" && strings.Contains(string(m.Params), target):
			attachCmds++
			if navAt >= 0 && l.at >= navAt && sliccAttachAt < 0 {
				sliccAttachAt = l.at
				mark(l.at, fmt.Sprintf("Target.attachToTarget #%d (after navigate: the client's, via slicc)", attachCmds))
			} else {
				mark(l.at, fmt.Sprintf("Target.attachToTarget #%d", attachCmds))
			}
		case !up && m.Method == "Target.getTargetInfo" && strings.Contains(string(m.Params), target):
			if askAt < 0 && sliccAttachAt >= 0 && l.at >= sliccAttachAt && m.SessionID != "" {
				askAt = l.at
			}
			mark(l.at, "> Target.getTargetInfo (session "+m.SessionID+")")
		case !up && m.Method == "Target.detachFromTarget":
			if detachAt < 0 && navAt >= 0 && l.at > navAt {
				for s := range sessions {
					if strings.Contains(string(m.Params), s) {
						detachAt = l.at
					}
				}
			}
			mark(l.at, "> Target.detachFromTarget "+string(m.Params))
		case !up && m.Method == "Target.closeTarget":
			mark(l.at, "> Target.closeTarget")
		case !up && (m.Method == "Page.enable" || m.Method == "Runtime.enable" || m.Method == "Runtime.runIfWaitingForDebugger" || m.Method == "Runtime.evaluate"):
			if m.Method == "Page.enable" && navAt >= 0 && l.at > navAt && sessions[m.SessionID] && enableAt < 0 {
				enableAt = l.at
				enableSession = m.SessionID
			}
			mark(l.at, "> "+m.Method+" on "+m.SessionID)
		case up && m.Method == "Target.attachedToTarget":
			var p struct {
				SessionID  string     `json:"sessionId"`
				TargetInfo vsloopInfo `json:"targetInfo"`
				Waiting    bool       `json:"waitingForDebugger"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.TargetInfo.TargetID != target {
				continue
			}
			attachedEvents++
			if sliccAttachAt >= 0 && l.at >= sliccAttachAt && sliccSession == "" {
				sliccSession = p.SessionID
			}
			mark(l.at, fmt.Sprintf("< Target.attachedToTarget session=%s url=%q waiting=%t", p.SessionID, p.TargetInfo.URL, p.Waiting))
		case up && (m.Method == "Target.targetInfoChanged" || m.Method == "Target.targetCreated"):
			var p struct {
				TargetInfo vsloopInfo `json:"targetInfo"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.TargetInfo.TargetID != target || p.TargetInfo.URL == lastURL {
				continue
			}
			lastURL = p.TargetInfo.URL
			if vsInfoAt < 0 && strings.HasPrefix(p.TargetInfo.URL, "view-source:") {
				vsInfoAt = l.at
			}
			mark(l.at, fmt.Sprintf("< %s url=%q attached=%t", m.Method, p.TargetInfo.URL, p.TargetInfo.Attached))
		case up && m.Method == "Target.detachedFromTarget":
			mark(l.at, "< Target.detachedFromTarget "+string(m.Params))
		case up && m.Method == "" && len(m.Result) > 0 && strings.Contains(string(m.Result), `"targetInfo"`):
			var res struct {
				TargetInfo vsloopInfo `json:"targetInfo"`
			}
			_ = json.Unmarshal(m.Result, &res)
			if res.TargetInfo.TargetID != target {
				continue
			}
			if askAt >= 0 && answerAt < 0 && l.at >= askAt {
				answerAt = l.at
				answerURL = res.TargetInfo.URL
			}
			mark(l.at, fmt.Sprintf("< getTargetInfo answer url=%q", res.TargetInfo.URL))
		case up && m.Method == "" && len(m.Result) > 0 && strings.Contains(string(m.Result), `"sessionId"`):
			mark(l.at, "< attach answer "+string(m.Result))
		}
	}
	for _, l := range tl.guard {
		text := strings.TrimPrefix(l.text, "slicc debug: ")
		tl.Marks = append(tl.Marks, rel(l.at)+" guard: "+text)
	}
	for _, l := range tl.window {
		tl.Marks = append(tl.Marks, rel(l.at)+" guard (no target id; another slot may have logged it): "+strings.TrimPrefix(l.text, "slicc debug: "))
	}
	reached := map[string]bool{}
	if cl != nil {
		if cl.T0 > 0 {
			mark(wall(cl.T0), "client: sends Target.attachToTarget")
		}
		if cl.T1 > 0 {
			mark(wall(cl.T1), "client: attach reply "+vsloopClientClass(cl)+" sessionId="+strconv.Quote(cl.Attach.SessionID)+" message="+strconv.Quote(cl.Attach.Message))
		}
		for _, st := range cl.Steps {
			mark(wall(st.At), fmt.Sprintf("client: %s reply ok=%t message=%q", st.Method, st.OK, st.Message))
		}
		if cl.Close != nil {
			gone := false
			if cl.Close.Gone != nil {
				gone = *cl.Close.Gone
			}
			mark(wall(cl.Close.At), fmt.Sprintf("client: closeTarget measured ok=%t message=%q gone=%t", cl.Close.OK, cl.Close.Message, gone))
		}
		for _, m := range cl.Recv {
			mark(wall(m.At), "client received: "+m.Text)
			for _, sm := range vsloopSidRe.FindAllStringSubmatch(m.Text, -1) {
				reached[sm[1]] = true
			}
		}
		for _, m := range cl.Sent {
			if m.Method == "Target.attachToTarget" {
				continue
			}
			mark(wall(m.At), fmt.Sprintf("client sent: %s session=%q %s", m.Method, m.SessionID, m.Params))
		}
		if cl.Attach.SessionID != "" {
			reached[cl.Attach.SessionID] = true
		}
	}
	for s := range reached {
		tl.reached = append(tl.reached, s)
	}
	sort.Strings(tl.reached)
	sort.SliceStable(tl.Marks, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimSuffix(strings.Fields(tl.Marks[i])[0], "ms"))
		b, _ := strconv.Atoi(strings.TrimSuffix(strings.Fields(tl.Marks[j])[0], "ms"))
		return a < b
	})
	guardAt := func(substr string) time.Duration {
		for _, l := range tl.guard {
			if strings.Contains(l.text, substr) {
				return l.at
			}
		}
		return -1
	}
	protectAt := guardAt("protecting target=" + target)
	movedAt := guardAt("moved onto a protected origin; detaching its sessions target=" + target)
	switch {
	case sliccAttachAt < 0:
		tl.Path = "no-attach-after-navigate"
	case askAt >= 0:
		tl.Path = "doc-confirm-ask"
	default:
		tl.Path = "no-doc-confirm-ask"
	}
	opt := func(at time.Duration) string {
		if at < 0 {
			return "never"
		}
		return rel(at)
	}
	answerClass := "none"
	switch {
	case answerAt < 0:
	case strings.HasPrefix(answerURL, "view-source:"):
		answerClass = "view-source"
	case answerURL == "about:blank":
		answerClass = "about:blank"
	case answerURL == "":
		answerClass = "empty"
	default:
		answerClass = "other"
	}
	order := "?"
	switch {
	case vsInfoAt < 0:
		order = "no view-source targetInfo seen"
	case sliccAttachAt >= 0 && vsInfoAt < sliccAttachAt:
		order = "view-source targetInfo before the slicc attach"
	case sliccAttachAt >= 0 && (enableAt < 0 || vsInfoAt < enableAt):
		order = "view-source targetInfo after the slicc attach, before the client's Page.enable"
	case enableAt >= 0:
		order = "view-source targetInfo after the client's Page.enable"
	}
	tl.Client = vsloopClientClass(cl)
	sliccReached := sliccSession != "" && slices.Contains(tl.reached, sliccSession)
	switch {
	case sliccAttachAt < 0 && strings.HasPrefix(tl.Client, "refused"):
		tl.Decision = "refused-without-forwarding"
	case sliccAttachAt < 0:
		tl.Decision = "not-forwarded,client-" + tl.Client
	case askAt >= 0:
		tl.Decision = "forwarded,doc-confirm-held,client-" + tl.Client
	default:
		tl.Decision = "forwarded,no-ask,client-" + tl.Client
	}
	tl.Shape = fmt.Sprintf("path=%s client=%s decision=%s sliccSessionReachedClient=%t answer=%s order=%q", tl.Path, tl.Client, tl.Decision, sliccReached, answerClass, order)
	tl.Pattern = fmt.Sprintf("%s attachCmds=%d attachedEvents=%d vsInfo=%s attachAfterNavigate=%s (session %s) ask=%s answer=%s clientPageEnable=%s (session %s) protecting=%s moved=%s detach=%s",
		tl.Shape, attachCmds, attachedEvents, opt(vsInfoAt), opt(sliccAttachAt), sliccSession, opt(askAt), opt(answerAt), opt(enableAt), enableSession, opt(protectAt), opt(movedAt), opt(detachAt))
	return tl
}

type vsloopEntry struct {
	N            int             `json:"n"`
	Target       string          `json:"target"`
	Variant      string          `json:"variant"`
	Control      string          `json:"control"`
	Guarded      string          `json:"guarded"`
	Live         string          `json:"live"`
	Why          json.RawMessage `json:"why,omitempty"`
	Observations json.RawMessage `json:"observations,omitempty"`
	Errors       []string        `json:"errors,omitempty"`
	TargetID     string          `json:"targetId,omitempty"`
	Pattern      string          `json:"pattern,omitempty"`
	Shape        string          `json:"shape,omitempty"`
	Path         string          `json:"path,omitempty"`
	Client       string          `json:"client,omitempty"`
	Decision     string          `json:"decision,omitempty"`
	AttachReply  string          `json:"attachReply,omitempty"`
	Reached      []string        `json:"sessionsSeenByClient,omitempty"`
	Log          string          `json:"log,omitempty"`
}

func vsloopN(id string) int {
	i := strings.LastIndexByte(id, '#')
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(id[i+1:])
	return n
}

func vsloopReport(t *testing.T, run vsloopRun) {
	rows, rec, complete, took := vsloopExecute(t, run)
	gate, guard := rec.snapshot()
	dir := filepath.Join("..", "..", "artifacts", "guard")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("mkdir %s: %v", dir, err)
	}
	type tally struct{ n, pass, fail, incomplete, other int }
	tallies := map[string]*tally{}
	var entries []vsloopEntry
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Variant != rows[j].Variant {
			return rows[i].Variant < rows[j].Variant
		}
		return vsloopN(rows[i].ID) < vsloopN(rows[j].ID)
	})
	for _, r := range rows {
		key := r.Target + " " + r.Variant
		if tallies[key] == nil {
			tallies[key] = &tally{}
		}
		tl := tallies[key]
		tl.n++
		e := vsloopEntry{N: vsloopN(r.ID), Target: r.Target, Variant: r.Variant, Control: r.Control, Guarded: r.Guarded, Live: r.Live, Errors: r.Errors}
		pass := r.Control == "pass" && r.Guarded == "pass" && r.Live == "pass"
		target, _ := vsloopGuardedTarget(r)
		e.TargetID = target
		cl := vsloopClientOf(r)
		timeline := vsloopBuild(target, gate, guard, rec.start, cl)
		e.Path = timeline.Path
		e.Client = timeline.Client
		e.Decision = timeline.Decision
		e.Reached = timeline.reached
		if cl != nil {
			e.AttachReply = cl.Attach.Message
			if cl.Attach.SessionID != "" {
				e.AttachReply = "sessionId " + cl.Attach.SessionID
			}
		}
		e.Pattern = timeline.Pattern
		e.Shape = timeline.Shape
		switch {
		case pass:
			tl.pass++
		case r.Control == "incomplete" || r.Guarded == "incomplete" || r.Live == "incomplete":
			tl.incomplete++
		case r.Guarded == "fail":
			tl.fail++
		default:
			tl.other++
		}
		if !pass {
			e.Why = r.Why
			e.Observations = r.Observed
			name := fmt.Sprintf("vsloop-%s-%s-%s-%d.log", vsloopTree, r.Target, r.Variant, e.N)
			e.Log = name
			var b strings.Builder
			row, _ := json.MarshalIndent(r, "", "  ")
			fmt.Fprintf(&b, "tree %s row %s target %s variant %s n %d\ncontrol %s guarded %s live %s\nwhy %s\nobservations %s\nguarded target %s sessions %v\nsessions seen by the client %v\nclient attach reply %s\npattern %s\n\n== timeline (ms from the observer's view-source navigate) ==\n", vsloopTree, r.ID, r.Target, r.Variant, e.N, r.Control, r.Guarded, r.Live, r.Why, r.Observed, target, timeline.sessions, timeline.reached, e.AttachReply, timeline.Pattern)
			for _, m := range timeline.Marks {
				b.WriteString(m + "\n")
			}
			b.WriteString("\n== guard debug lines for the target and its sessions (s since the run began) ==\n")
			for _, l := range timeline.guard {
				fmt.Fprintf(&b, "%9.3f %s\n", l.at.Seconds(), l.text)
			}
			b.WriteString("\n== guard debug lines in the window without a target id ==\n")
			for _, l := range timeline.window {
				fmt.Fprintf(&b, "%9.3f %s\n", l.at.Seconds(), l.text)
			}
			b.WriteString("\n== gate records for the target and its sessions ==\n")
			for _, l := range timeline.gate {
				fmt.Fprintf(&b, "%9.3f %s\n", l.at.Seconds(), l.text)
			}
			if cl != nil {
				raw, _ := json.MarshalIndent(cl, "", "  ")
				b.WriteString("\n== client record (epoch ms) ==\n")
				b.Write(raw)
				b.WriteString("\n")
			}
			b.WriteString("\n== row ==\n")
			b.Write(row)
			b.WriteString("\n")
			if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
				t.Logf("write %s: %v", name, err)
			}
			t.Logf("VSLOOP-FAIL tree=%s target=%s variant=%s n=%d control=%s guarded=%s live=%s why=%s observations=%s log=%s pattern: %s", vsloopTree, r.Target, r.Variant, e.N, r.Control, r.Guarded, r.Live, r.Why, r.Observed, name, timeline.Pattern)
		}
		entries = append(entries, e)
	}
	patterns := map[string]int{}
	paths := map[string]int{}
	for _, e := range entries {
		verdict := "FAIL"
		if e.Control == "pass" && e.Guarded == "pass" && e.Live == "pass" {
			verdict = "pass"
		}
		patterns[e.Target+" "+e.Variant+" "+verdict+": "+e.Shape]++
		paths[fmt.Sprintf("target=%s variant=%s verdict=%s path=%s client=%s", e.Target, e.Variant, verdict, e.Path, e.Client)]++
	}
	pkeys := make([]string, 0, len(paths))
	for k := range paths {
		pkeys = append(pkeys, k)
	}
	sort.Strings(pkeys)
	for _, k := range pkeys {
		t.Logf("VSLOOP-PATH tree=%s part=%d count=%d %s", vsloopTree, run.part, paths[k], k)
	}
	keys := make([]string, 0, len(patterns))
	for k := range patterns {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("VSLOOP-SHAPE tree=%s %d x %s", vsloopTree, patterns[k], k)
	}
	summary := map[string]any{"tree": vsloopTree, "sha": guardSHA(), "row": vsloopRowID, "target": run.target, "variants": run.variants, "n": run.n, "complete": complete, "seconds": took.Seconds(), "slots": guardSlots(), "part": run.part, "offset": run.offset, "rows": entries, "shapes": patterns, "paths": paths}
	out, _ := json.MarshalIndent(summary, "", "  ")
	name := fmt.Sprintf("vsloop-%s-%s-%s-part%d-summary.json", vsloopTree, run.target, strings.Join(run.variants, "+"), run.part)
	if err := os.WriteFile(filepath.Join(dir, name), out, 0o644); err != nil {
		t.Logf("write %s: %v", name, err)
	}
	tkeys := make([]string, 0, len(tallies))
	for k := range tallies {
		tkeys = append(tkeys, k)
	}
	sort.Strings(tkeys)
	for _, k := range tkeys {
		tl := tallies[k]
		parts := strings.Fields(k)
		failures := tl.n - tl.pass
		t.Logf("VSLOOP-RATE tree=%s part=%d sha=%s target=%s variant=%s N=%d pass=%d guardedFail=%d incomplete=%d other=%d rate=%d/%d (%.1f%%) complete=%t took=%s", vsloopTree, run.part, guardSHA(), parts[0], parts[1], tl.n, tl.pass, tl.fail, tl.incomplete, tl.other, failures, tl.n, 100*float64(failures)/float64(max(1, tl.n)), complete, took.Round(time.Second))
	}
	if len(rows) == 0 {
		t.Fatal("the repeated row run reported no rows")
	}
}

func TestDiagRepeatGuardRowVaultPromptPart1(t *testing.T) {
	vsloopReport(t, vsloopRun{target: "vault", variants: []string{"prompt"}, n: 167, offset: 0, part: 1})
}

func TestDiagRepeatGuardRowVaultPromptPart2(t *testing.T) {
	vsloopReport(t, vsloopRun{target: "vault", variants: []string{"prompt"}, n: 167, offset: 167, part: 2})
}

func TestDiagRepeatGuardRowVaultPromptPart3(t *testing.T) {
	vsloopReport(t, vsloopRun{target: "vault", variants: []string{"prompt"}, n: 166, offset: 334, part: 3})
}

func TestDiagRepeatGuardRowVaultLaggingPart1(t *testing.T) {
	vsloopReport(t, vsloopRun{target: "vault", variants: []string{"lagging"}, n: 167, offset: 0, part: 1})
}

func TestDiagRepeatGuardRowVaultLaggingPart2(t *testing.T) {
	vsloopReport(t, vsloopRun{target: "vault", variants: []string{"lagging"}, n: 167, offset: 167, part: 2})
}

func TestDiagRepeatGuardRowVaultLaggingPart3(t *testing.T) {
	vsloopReport(t, vsloopRun{target: "vault", variants: []string{"lagging"}, n: 166, offset: 334, part: 3})
}
