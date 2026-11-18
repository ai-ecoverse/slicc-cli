package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/coder/websocket"
)

func stockLinkFixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	html := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html>" + body))
	}
	mux.HandleFunc("/inner", func(w http.ResponseWriter, r *http.Request) {
		html(w, `<title>inner</title><style>body{margin:0}</style><button id="ibtn" style="position:fixed;left:0;top:0;width:150px;height:50px" onclick="window.hits+=1">Inner</button><input id="iin" style="position:fixed;left:0;top:60px"><script>window.hits=0</script>`)
	})
	mux.HandleFunc("/framed-same", func(w http.ResponseWriter, r *http.Request) {
		html(w, `<title>framed</title><iframe id="frame" src="/inner?k=framed&n=`+r.URL.Query().Get("n")+`"></iframe>`)
	})
	mux.HandleFunc("/nest", func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := strings.Cut(r.Host, ":")
		n := r.URL.Query().Get("n")
		box := "position:fixed;border:0;"
		html(w, `<title>nest</title><style>body{margin:0}</style>
<iframe id="ord" src="/inner?k=same&n=`+n+`" style="`+box+`left:0;top:40px;width:300px;height:120px"></iframe>
<iframe id="xo" src="http://127.0.0.1:`+port+`/inner?k=cross&n=`+n+`" style="`+box+`left:310px;top:40px;width:300px;height:120px"></iframe>
<iframe id="host" style="`+box+`left:0;top:170px;width:320px;height:300px"></iframe>
<script>
const doc = document.getElementById('host').contentDocument;
doc.body.style.margin = '0';
doc.body.innerHTML = '<iframe id="nsame" src="/inner?k=nested&n=`+n+`" style="position:absolute;border:0;left:0;top:0;width:300px;height:120px"></iframe>';
</script>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func stockLinkBridge(t *testing.T, ctx context.Context, env *protectEnv) string {
	t.Helper()
	var mu sync.Mutex
	var host *websocket.Conn
	clients := map[int]*websocket.Conn{}
	writers := map[int]*sync.Mutex{}
	next := 0
	writeHost := func(e playwrightEnvelope) error {
		mu.Lock()
		defer mu.Unlock()
		body, err := json.Marshal(e)
		if err != nil {
			return err
		}
		return host.Write(ctx, websocket.MessageText, body)
	}
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") }()
		conn.SetReadLimit(256 * 1024 * 1024)
		if r.URL.Path == "/host" {
			mu.Lock()
			host = conn
			mu.Unlock()
			for {
				_, raw, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var e playwrightEnvelope
				if err := json.Unmarshal(raw, &e); err != nil {
					return
				}
				mu.Lock()
				client, writer := clients[e.ID], writers[e.ID]
				mu.Unlock()
				if client == nil {
					continue
				}
				if e.Error != "" {
					_ = client.Close(websocket.StatusInternalError, e.Error)
					continue
				}
				writer.Lock()
				err = client.Write(ctx, websocket.MessageText, []byte(e.Message))
				writer.Unlock()
				if err != nil {
					mu.Lock()
					delete(clients, e.ID)
					delete(writers, e.ID)
					mu.Unlock()
				}
			}
		}
		mu.Lock()
		next++
		id := next
		clients[id] = conn
		writers[id] = &sync.Mutex{}
		mu.Unlock()
		defer func() {
			_ = writeHost(playwrightEnvelope{ID: id, Close: true})
			mu.Lock()
			delete(clients, id)
			delete(writers, id)
			mu.Unlock()
		}()
		if err := writeHost(playwrightEnvelope{ID: id, Open: true}); err != nil {
			return
		}
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if err := writeHost(playwrightEnvelope{ID: id, Message: string(raw)}); err != nil {
				return
			}
		}
	}))
	t.Cleanup(bridge.Close)
	var ready bool
	env.p.eval("linkTest.playwrightRelay("+jsArgs(strings.Replace(bridge.URL, "http", "ws", 1)+"/host", "chrome")+")", &ready)
	if !ready {
		t.Fatal("relay did not open")
	}
	return strings.Replace(bridge.URL, "http", "ws", 1) + "/cdp"
}

func openLoaded(t *testing.T, b *chromeProc, url, ready string) {
	t.Helper()
	p := b.openTarget(t, url)
	deadline := time.Now().Add(20 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var ok bool
		err := p.session.Evaluate(ctx, ready, cdp.EvaluateOptions{ReturnByValue: true}, &ok)
		cancel()
		if err == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not load: %v", url, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type stockLinkFrame struct {
	Found  bool     `json:"found"`
	Read   string   `json:"read"`
	Error  string   `json:"error"`
	Frames []string `json:"frames"`
}

type stockLinkPage struct {
	URL    string                    `json:"url"`
	Frames map[string]stockLinkFrame `json:"frames"`
}

type stockLinkConnect struct {
	OK                 bool                     `json:"ok"`
	MS                 int                      `json:"ms"`
	Error              string                   `json:"error"`
	TabsWithoutPage    []string                 `json:"tabsWithoutPage"`
	PagesNeverAttached []string                 `json:"pagesNeverAttached"`
	Pages              []string                 `json:"pages"`
	PagesError         string                   `json:"pagesError"`
	ProtectedVisible   []string                 `json:"protectedVisible"`
	Preexisting        map[string]stockLinkPage `json:"preexisting"`
}

type stockLinkInput struct {
	OK           bool   `json:"ok"`
	Error        string `json:"error"`
	Delivered    int    `json:"delivered"`
	ValueChanged bool   `json:"valueChanged"`
}

type stockLinkOrdinary struct {
	Found     bool   `json:"found"`
	Click     string `json:"click"`
	Type      string `json:"type"`
	Hits      *int   `json:"hits"`
	Value     string `json:"value"`
	ReadError string `json:"readError"`
}

type stockLinkResult struct {
	Connect   stockLinkConnect `json:"connect"`
	Reconnect stockLinkConnect `json:"reconnect"`
	Nested    *struct {
		Goto             string                       `json:"goto"`
		FramesSettled    bool                         `json:"framesSettled"`
		Embedded         any                          `json:"embedded"`
		ProtectedFrames  []string                     `json:"protectedFrames"`
		ExtensionFocused bool                         `json:"extensionFocused"`
		Inputs           map[string]stockLinkInput    `json:"inputs"`
		Ordinary         map[string]stockLinkOrdinary `json:"ordinary"`
		Control          struct {
			Mouse int    `json:"mouse"`
			Text  bool   `json:"text"`
			Error string `json:"error"`
		} `json:"control"`
	} `json:"nested"`
	Probes map[string]json.RawMessage `json:"probes"`
}

const stockShownRefusal = "slicc: refused: a browser extension's frame (chrome-extension://) is showing or focused in this tab, such as a password manager's autofill list; close it or reload the tab"

func checkStockConnect(t *testing.T, name string, c stockLinkConnect) {
	t.Helper()
	if !c.OK || c.MS >= 10000 {
		t.Errorf("%s: stock Puppeteer connect beside a protected tab: ok=%v after %d ms (%s); tabs whose page never attached: %v; pages never attached: %v", name, c.OK, c.MS, c.Error, c.TabsWithoutPage, c.PagesNeverAttached)
		return
	}
	if c.Pages == nil {
		t.Errorf("%s: browser.pages() failed: %s", name, c.PagesError)
	}
	if len(c.TabsWithoutPage) != 0 {
		t.Errorf("%s: tabs attached to the client whose page was never attached under the tab session nor destroyed: %v", name, c.TabsWithoutPage)
	}
	for _, p := range c.PagesNeverAttached {
		if !strings.HasSuffix(p, " SEVEN") && !strings.Contains(p, " SEVEN/") {
			t.Errorf("%s: an ordinary page announced with targetCreated was never attached nor destroyed: %s", name, p)
		}
	}
	if len(c.ProtectedVisible) != 0 {
		t.Errorf("%s: protected targets visible to the client: %v", name, c.ProtectedVisible)
	}
}

func checkStockPreexisting(t *testing.T, name string, c stockLinkConnect) {
	t.Helper()
	for kind, frames := range map[string][]string{"framed": {"framed"}, "nest": {"same", "cross", "nested"}} {
		p, ok := c.Preexisting[kind]
		if !ok {
			t.Errorf("%s: the %s page open before connect is missing from pages()", name, kind)
			continue
		}
		for _, k := range frames {
			if f := p.Frames[k]; !f.Found || f.Read != "Inner" {
				t.Errorf("%s: %s frame %s of a page open before connect: %+v", name, kind, k, f)
			}
		}
	}
}

func runStockLink(t *testing.T, env *protectEnv, mode string, pre func(ordinary string)) stockLinkResult {
	t.Helper()
	fixture := stockLinkFixture(t)
	ordinary := strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	upstream := stockLinkBridge(t, ctx, env)
	pre(ordinary)
	cmd := exec.CommandContext(ctx, requireNode(t), "testdata/stocklink-regression.mjs", upstream, env.b.endpoint, ordinary, fixture.URL, env.seven, mode, extensionFrameURL())
	out, err := cmd.CombinedOutput()
	var res stockLinkResult
	found := false
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "RESULT "); ok {
			found = json.Unmarshal([]byte(rest), &res) == nil
			t.Logf("RESULT %s", rest)
			continue
		}
		if line != "" {
			t.Log(line)
		}
	}
	if err != nil || !found {
		t.Fatalf("the stock Puppeteer script did not finish: %v", err)
	}
	return res
}

type stockLinkProbe struct {
	MS    int    `json:"ms"`
	Error string `json:"error"`
}

func checkStockProbes(t *testing.T, probes map[string]json.RawMessage) {
	t.Helper()
	for name, raw := range probes {
		var p struct {
			Listed bool   `json:"listed"`
			Attach string `json:"attach"`
			First  map[string]stockLinkProbe
			Second map[string]stockLinkProbe
			Worlds []struct {
				Frame string `json:"frame"`
				MS    int    `json:"ms"`
				Error string `json:"error"`
			} `json:"worlds"`
		}
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		if !p.Listed || p.Attach != "" {
			t.Errorf("probe %s: listed=%v attach=%s", name, p.Listed, p.Attach)
			continue
		}
		for when, set := range map[string]map[string]stockLinkProbe{"at attach": p.First, "after enable": p.Second} {
			for what, a := range set {
				if a.Error != "" || a.MS > 1000 {
					t.Errorf("probe %s %s: %s answered in %d ms: %s", name, when, what, a.MS, a.Error)
				}
			}
		}
		for _, w := range p.Worlds {
			if w.Error != "" || w.MS > 1000 {
				t.Errorf("probe %s: Page.createIsolatedWorld in ordinary child frame %s answered in %d ms: %s", name, w.Frame, w.MS, w.Error)
			}
		}
	}
}

func TestStockPuppeteerConnectsThroughLinkBesideHeldTabs(t *testing.T) {
	env := setupProtect(t)
	res := runStockLink(t, env, "connect", func(ordinary string) {
		_ = env.b.openTarget(t, env.seven+"/frame.html?held")
		openLoaded(t, env.b, ordinary+"/framed-same?pre=1", `document.readyState === 'complete' && document.getElementById('frame').contentDocument?.getElementById('ibtn') !== null`)
		openLoaded(t, env.b, ordinary+"/nest?pre=1", `document.readyState === 'complete' && document.getElementById('host').contentDocument.getElementById('nsame').contentDocument?.getElementById('ibtn') !== null`)
	})
	for name, c := range map[string]stockLinkConnect{"connect": res.Connect, "reconnect": res.Reconnect} {
		checkStockConnect(t, name, c)
		checkStockPreexisting(t, name, c)
	}
	checkStockProbes(t, res.Probes)
}

func TestStockPuppeteerInputThroughLinkStaysOutOfNestedExtensionFrame(t *testing.T) {
	env := setupExtensionFrameProtect(t)
	res := runStockLink(t, env, "nested", func(string) {})
	checkStockConnect(t, "connect", res.Connect)
	checkStockConnect(t, "reconnect", res.Reconnect)
	n := res.Nested
	if n == nil {
		t.Fatal("the nested-frame flow did not run")
	}
	if n.Goto != "ok" || !n.FramesSettled || n.Embedded != true {
		t.Fatalf("the nested-frame page: goto=%s, ordinary frames reached the client=%v, extension frame embedded=%v", n.Goto, n.FramesSettled, n.Embedded)
	}
	for _, k := range []string{"same", "cross", "nested"} {
		o := n.Ordinary[k]
		if !o.Found || o.Click != "ok" || o.Type != "ok" || o.Hits == nil || *o.Hits != 1 || o.Value != "ok" {
			t.Errorf("input into the ordinary %s frame: %s", k, fmt.Sprintf("%+v", o))
		}
	}
	if n.Control.Error != "" || n.Control.Mouse < 1 || !n.Control.Text {
		t.Fatalf("control: unguarded input did not reach the nested extension frame, so a zero count proves nothing: %+v", n.Control)
	}
	if !n.ExtensionFocused {
		t.Errorf("the observer could not focus the nested extension frame for the keyboard rows")
	}
	if len(n.ProtectedFrames) != 0 {
		t.Errorf("the extension frame is visible to the client: %v", n.ProtectedFrames)
	}
	if len(n.Inputs) < 8 {
		t.Errorf("only %d protected input rows ran", len(n.Inputs))
	}
	for name, in := range n.Inputs {
		if in.Delivered != 0 || in.ValueChanged {
			t.Errorf("%s reached the extension frame: delivered %d events, value changed %v (answer ok=%v %s)", name, in.Delivered, in.ValueChanged, in.OK, in.Error)
		}
		if strings.HasPrefix(name, "Target.attachToTarget") {
			if in.OK {
				t.Errorf("%s: the client attached to the extension frame", name)
			}
			continue
		}
		if in.OK || !strings.Contains(in.Error, stockShownRefusal) {
			t.Errorf("%s: want the shown-or-focused refusal, got ok=%v %q", name, in.OK, in.Error)
		}
	}
	checkStockProbes(t, res.Probes)
}
