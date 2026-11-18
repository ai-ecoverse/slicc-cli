package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func preexistingTreeFixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	html := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html>" + body))
	}
	mux.HandleFunc("/inner", func(w http.ResponseWriter, r *http.Request) {
		html(w, `<title>inner</title><button id="ibtn">Inner</button>`)
	})
	mux.HandleFunc("/framed-same", func(w http.ResponseWriter, r *http.Request) {
		html(w, `<title>framed</title><iframe id="frame" src="/inner?k=framed&n=`+r.URL.Query().Get("n")+`"></iframe>`)
	})
	mux.HandleFunc("/nest", func(w http.ResponseWriter, r *http.Request) {
		n := r.URL.Query().Get("n")
		html(w, `<title>nest</title><iframe id="host"></iframe><script>
const doc = document.getElementById('host').contentDocument;
doc.body.innerHTML = '<iframe id="nsame" src="/inner?k=nested&n=`+n+`"></iframe>';
</script>`)
	})
	mux.HandleFunc("/framed-seven", func(w http.ResponseWriter, r *http.Request) {
		seven := r.URL.Query().Get("seven")
		html(w, `<title>framed-seven</title><iframe id="frame" src="`+seven+`/frame.html?n=`+r.URL.Query().Get("n")+`"></iframe>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type preexistingCmd struct {
	MS     int    `json:"ms"`
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	Value  any    `json:"value"`
	Frames []struct {
		ID       string `json:"id"`
		ParentID string `json:"parentId"`
		URL      string `json:"url"`
	} `json:"frames"`
}

type preexistingPipeline struct {
	Elapsed int            `json:"elapsed"`
	Info    preexistingCmd `json:"info"`
	Enable  preexistingCmd `json:"enable"`
	Tree    preexistingCmd `json:"tree"`
	Info2   preexistingCmd `json:"info2"`
}

type preexistingPage struct {
	Attached   bool                `json:"attached"`
	UnderTab   bool                `json:"underTab"`
	Waiting    bool                `json:"waiting"`
	URL        string              `json:"url"`
	ChildrenOK *bool               `json:"childrenOk"`
	Refused    bool                `json:"refused"`
	Error      string              `json:"error"`
	Pipeline   preexistingPipeline `json:"pipeline"`
	World      preexistingCmd      `json:"world"`
}

type preexistingResult struct {
	TabCount int `json:"tabCount"`
	Events   *struct {
		Methods  map[string]int `json:"methods"`
		Attached []any          `json:"attached"`
		Created  []any          `json:"created"`
	} `json:"events"`
	Puppeteer *struct {
		OK        bool     `json:"ok"`
		MS        int      `json:"ms"`
		Pages     int      `json:"pages"`
		URLs      []string `json:"urls"`
		HasFramed bool     `json:"hasFramed"`
		HasNest   bool     `json:"hasNest"`
		Error     string   `json:"error"`
	} `json:"puppeteer"`
	Steps []struct {
		Name  string `json:"name"`
		OK    bool   `json:"ok"`
		MS    int    `json:"ms"`
		Error string `json:"error"`
	} `json:"steps"`
	Pages map[string]preexistingPage `json:"pages"`
}

func checkBiosPipelineOK(t *testing.T, name string, p preexistingPage, wantChildren, wantWorld bool) {
	t.Helper()
	if !p.Attached {
		t.Errorf("%s: page open before connect never attached under tab flatten-auto-attach: %s", name, p.Error)
		return
	}
	if !p.UnderTab {
		t.Errorf("%s: page attach was not under a tab session (bios path)", name)
	}
	pipe := p.Pipeline
	for label, c := range map[string]preexistingCmd{
		"Target.getTargetInfo":   pipe.Info,
		"Page.enable":            pipe.Enable,
		"Page.getFrameTree":      pipe.Tree,
		"Target.getTargetInfo#2": pipe.Info2,
	} {
		if !c.OK || c.MS > 300 {
			t.Errorf("%s: pipelined %s: ok=%v after %d ms (%s)", name, label, c.OK, c.MS, c.Error)
		}
	}
	if wantChildren && (p.ChildrenOK == nil || !*p.ChildrenOK) {
		t.Errorf("%s: pipelined Page.getFrameTree did not contain the expected child frames: %+v", name, pipe.Tree.Frames)
	}
	if wantWorld && (!p.World.OK || p.World.MS > 300) {
		t.Errorf("%s: Page.createIsolatedWorld on /inner: ok=%v after %d ms (%s)", name, p.World.OK, p.World.MS, p.World.Error)
	}
}

func TestRawCDPFirstCommandsOnPreexistingFramedPages(t *testing.T) {
	env := setupProtectExtension(t)
	fixture := preexistingTreeFixture(t)
	ordinary := strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	upstream := stockLinkBridge(t, ctx, env)

	openLoaded(t, env.b, ordinary+"/framed-same?pre=1", `document.readyState === 'complete' && document.getElementById('frame').contentDocument?.getElementById('ibtn') !== null`)
	openLoaded(t, env.b, ordinary+"/nest?pre=1", `document.readyState === 'complete' && document.getElementById('host').contentDocument.getElementById('nsame').contentDocument?.getElementById('ibtn') !== null`)
	openLoaded(t, env.b, ordinary+"/framed-seven?pre=1&seven="+env.seven, `document.readyState === 'complete' && document.getElementById('frame') !== null`)
	_ = env.b.openTarget(t, "about:blank")

	cmd := exec.CommandContext(ctx, requireNode(t), "testdata/preexisting-tree.mjs", upstream, ordinary, env.seven, env.b.endpoint)
	out, err := cmd.CombinedOutput()
	var res preexistingResult
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
		t.Fatalf("the raw CDP preexisting-tree script did not finish: %v\n%s", err, out)
	}
	if res.TabCount < 1 {
		t.Errorf("bios path needs Target.targetCreated/attachedToTarget tab events after discover+auto-attach with filter [{}]; got tabCount=%d events=%+v", res.TabCount, res.Events)
	}

	checkBiosPipelineOK(t, "framed-same (extension content-script worlds)", res.Pages["framed"], true, true)
	checkBiosPipelineOK(t, "nest about:blank→same-origin (extension content-script worlds)", res.Pages["nest"], true, true)
	checkBiosPipelineOK(t, "unrelated about:blank", res.Pages["blank"], false, false)

	p := res.Pages["protected"]
	if !p.Attached {
		t.Errorf("protected: page with a seven frame open before connect never attached: %s", p.Error)
	} else {
		tree := p.Pipeline.Tree
		if tree.OK && !p.Refused {
			t.Errorf("protected: Page.getFrameTree on a page with a protected frame must stay refused or sanitized as today: ok=%v refused=%v error=%s frames=%+v", tree.OK, p.Refused, tree.Error, tree.Frames)
		}
		if tree.OK && p.Refused {
			t.Errorf("protected: inconsistent refused flag with ok tree: %+v", tree)
		}
		if !tree.OK && !strings.Contains(tree.Error, "seven's origin") && !strings.Contains(tree.Error, "refused") {
			t.Errorf("protected: unexpected getFrameTree failure: %s", tree.Error)
		}
	}

	if res.Puppeteer == nil {
		t.Fatal("stock Puppeteer connect beside the preexisting pages did not run")
	}
	if !res.Puppeteer.OK || res.Puppeteer.MS > 5000 {
		t.Errorf("stock Puppeteer connect+pages beside the extension-browser fixture: ok=%v after %d ms (%s)", res.Puppeteer.OK, res.Puppeteer.MS, res.Puppeteer.Error)
	}
	if res.Puppeteer.OK && (!res.Puppeteer.HasFramed || !res.Puppeteer.HasNest) {
		t.Errorf("stock Puppeteer pages() missed the preexisting framed pages: %+v", res.Puppeteer.URLs)
	}
}

func TestRawCDPFirstCommandsOnPlainPreexistingPagesStayFast(t *testing.T) {
	env := setupProtect(t)
	fixture := preexistingTreeFixture(t)
	ordinary := strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	upstream := stockLinkBridge(t, ctx, env)
	openLoaded(t, env.b, ordinary+"/framed-same?pre=1", `document.readyState === 'complete' && document.getElementById('frame').contentDocument?.getElementById('ibtn') !== null`)
	_ = env.b.openTarget(t, "about:blank")
	cmd := exec.CommandContext(ctx, requireNode(t), "testdata/preexisting-tree.mjs", upstream, ordinary, env.seven, "")
	out, err := cmd.CombinedOutput()
	var res preexistingResult
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
		t.Fatalf("the raw CDP preexisting-tree script did not finish: %v\n%s", err, out)
	}
	checkBiosPipelineOK(t, "plain framed-same (no extension worlds)", res.Pages["framed"], true, true)
	checkBiosPipelineOK(t, "plain about:blank", res.Pages["blank"], false, false)
}
