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
	Value  string `json:"value"`
	Frames []struct {
		ID       string `json:"id"`
		ParentID string `json:"parentId"`
		URL      string `json:"url"`
	} `json:"frames"`
}

type preexistingFirst struct {
	Info     preexistingCmd `json:"info"`
	Tree     preexistingCmd `json:"tree"`
	History  preexistingCmd `json:"history"`
	Resource preexistingCmd `json:"resource"`
}

type preexistingPage struct {
	Attached              bool             `json:"attached"`
	UnderTab              bool             `json:"underTab"`
	Waiting               bool             `json:"waiting"`
	URL                   string           `json:"url"`
	ChildrenOK            *bool            `json:"childrenOk"`
	AfterResumeChildrenOK *bool            `json:"afterResumeChildrenOk"`
	Refused               bool             `json:"refused"`
	Error                 string           `json:"error"`
	First                 preexistingFirst `json:"first"`
	AfterResume           preexistingFirst `json:"afterResume"`
}

type preexistingResult struct {
	TabCount int `json:"tabCount"`
	Steps    []struct {
		Name  string `json:"name"`
		OK    bool   `json:"ok"`
		MS    int    `json:"ms"`
		Error string `json:"error"`
	} `json:"steps"`
	Pages map[string]preexistingPage `json:"pages"`
}

func checkCommandsOK(t *testing.T, name, when string, first preexistingFirst, children *bool, wantChildren bool) {
	t.Helper()
	for label, c := range map[string]preexistingCmd{
		"Target.getTargetInfo":      first.Info,
		"Page.getFrameTree":         first.Tree,
		"Page.getNavigationHistory": first.History,
		"Page.getResourceTree":      first.Resource,
	} {
		if !c.OK || c.MS > 300 {
			t.Errorf("%s %s: %s on the page session: ok=%v after %d ms (%s)", name, when, label, c.OK, c.MS, c.Error)
		}
	}
	if wantChildren && (children == nil || !*children) {
		t.Errorf("%s %s: Page.getFrameTree did not contain the expected child frames: %+v", name, when, first.Tree.Frames)
	}
}

func checkFirstOK(t *testing.T, name string, p preexistingPage, wantChildren bool) {
	t.Helper()
	if !p.Attached {
		t.Errorf("%s: page open before connect never attached under tab flatten-auto-attach: %s", name, p.Error)
		return
	}
	if !p.UnderTab {
		t.Errorf("%s: page attach was not under a tab session (bios path)", name)
	}
	checkCommandsOK(t, name, "at attach", p.First, p.ChildrenOK, wantChildren)
	checkCommandsOK(t, name, "after runIfWaitingForDebugger", p.AfterResume, p.AfterResumeChildrenOK, wantChildren)
}

func TestRawCDPFirstCommandsOnPreexistingFramedPages(t *testing.T) {
	env := setupProtect(t)
	fixture := preexistingTreeFixture(t)
	ordinary := strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	upstream := stockLinkBridge(t, ctx, env)

	openLoaded(t, env.b, ordinary+"/framed-same?pre=1", `document.readyState === 'complete' && document.getElementById('frame').contentDocument?.getElementById('ibtn') !== null`)
	openLoaded(t, env.b, ordinary+"/nest?pre=1", `document.readyState === 'complete' && document.getElementById('host').contentDocument.getElementById('nsame').contentDocument?.getElementById('ibtn') !== null`)
	openLoaded(t, env.b, ordinary+"/framed-seven?pre=1&seven="+env.seven, `document.readyState === 'complete' && document.getElementById('frame') !== null`)
	_ = env.b.openTarget(t, "about:blank")

	cmd := exec.CommandContext(ctx, requireNode(t), "testdata/preexisting-tree.mjs", upstream, ordinary, env.seven)
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
		t.Errorf("bios path needs Chrome tab targets (Target.getTargets type=tab); got tabCount=%d", res.TabCount)
	}

	checkFirstOK(t, "framed-same", res.Pages["framed"], true)
	checkFirstOK(t, "nest about:blank→same-origin", res.Pages["nest"], true)
	checkFirstOK(t, "unrelated about:blank", res.Pages["blank"], false)

	p := res.Pages["protected"]
	if !p.Attached {
		t.Errorf("protected: page with a seven frame open before connect never attached: %s", p.Error)
	} else {
		tree := p.First.Tree
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
}
