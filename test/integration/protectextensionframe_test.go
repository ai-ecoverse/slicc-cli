package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const harnessExtensionID = "ankmbodmgimjemnojmeddfcdlbocolob"

func extensionFrameURL() string {
	return "chrome-extension://" + harnessExtensionID + "/frame.html"
}

func setupExtensionFrameProtect(t *testing.T) *protectEnv {
	t.Helper()
	ext := filepath.Join("..", "..", "node_modules", "@ai-ecoverse", "slicc-shared-web", "harness", "extension")
	return setupProtectOn(t, launchExtensionBrowser(t, ext), true)
}

func (e *protectEnv) openHiddenExtensionFrameTab(t *testing.T, tag string) (pageID, frameID string) {
	t.Helper()
	frameURL := extensionFrameURL()
	var created struct {
		TargetID string `json:"targetId"`
	}
	e.unguardedCall(t, "Target.createTarget", map[string]any{"url": e.allowed + "/?n=" + tag}, &created)
	t.Cleanup(func() { e.b.closeTarget(created.TargetID) })
	waitTargetURL(t, e.b, created.TargetID, e.allowed)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := e.b.client.Attach(ctx, created.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Call(ctx, "Runtime.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression": `(() => {
  const f = document.createElement('iframe');
  f.id = 'slicc-extension-frame';
  f.src = ` + jsArgs(frameURL) + `;
  f.hidden = true;
  document.body.append(f);
  return f.src;
})()`,
		"returnByValue": true,
		"awaitPromise":  true,
	}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var listed struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				URL      string `json:"url"`
			} `json:"targetInfos"`
		}
		e.unguardedCall(t, "Target.getTargets", nil, &listed)
		for _, info := range listed.TargetInfos {
			if info.Type == "iframe" && strings.HasPrefix(info.URL, "chrome-extension://"+harnessExtensionID+"/") {
				_ = s.Detach(ctx)
				return created.TargetID, info.TargetID
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the extension frame target never appeared")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestCDPExtensionFrameDoesNotLockOrdinaryTab(t *testing.T) {
	env := setupExtensionFrameProtect(t)
	pageID, frameID := env.openHiddenExtensionFrameTab(t, "extension-frame")
	var r struct {
		Attached            bool    `json:"attached"`
		Read                bool    `json:"read"`
		HiddenShot          bool    `json:"hiddenShot"`
		HiddenKey           bool    `json:"hiddenKey"`
		OpacityShot         bool    `json:"opacityShot"`
		OpacityShotRefusal  *string `json:"opacityShotRefusal"`
		PoisonedShot        bool    `json:"poisonedShot"`
		PoisonedShotRefusal *string `json:"poisonedShotRefusal"`
		FocusedShot         bool    `json:"focusedShot"`
		FocusedShotRefusal  *string `json:"focusedShotRefusal"`
		ShownShot           bool    `json:"shownShot"`
		ShownShotRefusal    *string `json:"shownShotRefusal"`
		ShownKey            bool    `json:"shownKey"`
		ShownKeyRefusal     *string `json:"shownKeyRefusal"`
		Cast                bool    `json:"cast"`
		CastRefusal         *string `json:"castRefusal"`
		TabMirror           bool    `json:"tabMirror"`
		TabMirrorRefusal    *string `json:"tabMirrorRefusal"`
		Layer               bool    `json:"layer"`
		LayerRefusal        *string `json:"layerRefusal"`
		Replay              bool    `json:"replay"`
		ReplayRefusal       *string `json:"replayRefusal"`
		PDF                 bool    `json:"pdf"`
		PDFRefusal          *string `json:"pdfRefusal"`
		Snap                bool    `json:"snap"`
		SnapRefusal         *string `json:"snapRefusal"`
		Listed              bool    `json:"listed"`
		TreeLeak            bool    `json:"treeLeak"`
		SrcLeak             bool    `json:"srcLeak"`
		OuterLeak           bool    `json:"outerLeak"`
		JSONLeak            bool    `json:"jsonLeak"`
		KeyEvalLeak         bool    `json:"keyEvalLeak"`
		KeyCallLeak         bool    `json:"keyCallLeak"`
		AXLeak              bool    `json:"axLeak"`
		OrdinaryIntact      bool    `json:"ordinaryIntact"`
		FrameAttach         bool    `json:"frameAttach"`
		ShownRefusal        string  `json:"shownRefusal"`
		PresentRefusal      string  `json:"presentRefusal"`
	}
	env.evalJSON(t, "linkTest.protect.foreignExtensionFrame("+jsArgs("chrome", pageID, frameID, env.allowed)+")", &r)
	if !r.Attached || !r.Read {
		t.Fatalf("the ordinary tab stayed locked: attached=%t read=%t", r.Attached, r.Read)
	}
	if !r.HiddenShot || !r.HiddenKey {
		t.Fatalf("a hidden extension frame still blocked screenshot or input: shot=%t key=%t", r.HiddenShot, r.HiddenKey)
	}
	wantShown := func(ok bool, refusal *string, label string) {
		t.Helper()
		if ok || refusal == nil || *refusal != protect.ShownRefusal() {
			got := "<nil>"
			if refusal != nil {
				got = *refusal
			}
			t.Fatalf("%s: ok=%t refusal=%q want %q", label, ok, got, protect.ShownRefusal())
		}
	}
	wantPresent := func(ok bool, refusal *string, label string) {
		t.Helper()
		if ok || refusal == nil || *refusal != protect.PresentRefusal() {
			got := "<nil>"
			if refusal != nil {
				got = *refusal
			}
			t.Fatalf("%s: ok=%t refusal=%q want %q", label, ok, got, protect.PresentRefusal())
		}
	}
	wantShown(r.OpacityShot, r.OpacityShotRefusal, "opacity-zero still counts as shown")
	wantShown(r.PoisonedShot, r.PoisonedShotRefusal, "poisoned probe world")
	wantShown(r.FocusedShot, r.FocusedShotRefusal, "focused extension frame")
	wantShown(r.ShownShot, r.ShownShotRefusal, "shown screenshot")
	wantShown(r.ShownKey, r.ShownKeyRefusal, "shown input")
	wantPresent(r.Cast, r.CastRefusal, "screencast")
	wantPresent(r.TabMirror, r.TabMirrorRefusal, "Cast.startTabMirroring")
	wantPresent(r.Layer, r.LayerRefusal, "LayerTree.makeSnapshot")
	wantPresent(r.Replay, r.ReplayRefusal, "LayerTree.replaySnapshot")
	wantPresent(r.PDF, r.PDFRefusal, "printToPDF")
	wantPresent(r.Snap, r.SnapRefusal, "captureSnapshot")
	if r.Listed || r.TreeLeak || r.SrcLeak || r.OuterLeak || r.JSONLeak || r.KeyEvalLeak || r.KeyCallLeak || r.AXLeak {
		t.Fatalf("the extension frame URL leaked: listed=%t treeLeak=%t srcLeak=%t outerLeak=%t jsonLeak=%t keyEvalLeak=%t keyCallLeak=%t axLeak=%t", r.Listed, r.TreeLeak, r.SrcLeak, r.OuterLeak, r.JSONLeak, r.KeyEvalLeak, r.KeyCallLeak, r.AXLeak)
	}
	if !r.OrdinaryIntact {
		t.Fatal("ordinary HTML evaluate was altered while an extension frame was tracked")
	}
	if r.FrameAttach {
		t.Fatal("the extension frame itself was attachable")
	}
	var cast struct {
		Cast   bool `json:"cast"`
		Before int  `json:"before"`
		After  int  `json:"after"`
		Grew   bool `json:"grew"`
	}
	env.evalJSON(t, "linkTest.protect.foreignExtensionFrameScreencast("+jsArgs("chrome", env.allowed, extensionFrameURL())+")", &cast)
	if !cast.Cast {
		t.Fatal("screencast before an extension frame appeared was refused")
	}
	if cast.Before == 0 {
		t.Fatal("screencast produced no frames before the extension frame appeared")
	}
	if cast.Grew {
		t.Fatalf("screencast frames kept arriving after an extension frame appeared: before=%d after=%d", cast.Before, cast.After)
	}
}

func TestCDPExtensionFrameProbeWorldStaysBounded(t *testing.T) {
	env := setupExtensionFrameProtect(t)
	pageID, _ := env.openHiddenExtensionFrameTab(t, "extension-probe-worlds")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sub := env.b.client.Subscribe()
	t.Cleanup(sub.Close)
	s, err := env.b.client.Attach(ctx, pageID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Call(ctx, "Runtime.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-sub.Events():
				if !ok {
					return
				}
				if ev.SessionID != s.ID || ev.Method != "Runtime.executionContextCreated" {
					continue
				}
				var p struct {
					Context struct {
						Name string `json:"name"`
					} `json:"context"`
				}
				if json.Unmarshal(ev.Params, &p) != nil {
					continue
				}
				if strings.HasPrefix(p.Context.Name, "slicc-ext-probe-") {
					names[p.Context.Name] = true
				}
			}
		}
	}()
	const inserts = 40
	var r struct {
		OK      bool   `json:"ok"`
		Inserts int    `json:"inserts"`
		Refusal string `json:"refusal"`
	}
	env.evalJSONWithin(t, "linkTest.protect.foreignExtensionFrameProbeWorlds("+jsArgs("chrome", pageID, inserts)+")", &r, 60*time.Second)
	cancel()
	<-done
	if !r.OK || r.Inserts != inserts {
		t.Fatalf("insertText under a hidden extension frame failed: %+v", r)
	}
	if n := len(names); n == 0 || n > 2 {
		t.Fatalf("slicc-ext-probe- worlds after %d insertText calls: %d names %v, want a small constant", inserts, n, keysOf(names))
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestCDPExtensionFrameConcurrentCaptures(t *testing.T) {
	env, _ := setupFakeProtectEnv(t, nil, func(_, _ string) fakeScript {
		return extensionTwoTabScript("chrome-extension://"+harnessExtensionID+"/frame.html", false)
	})
	var r struct {
		Left         bool    `json:"left"`
		Right        bool    `json:"right"`
		LeftRefusal  *string `json:"leftRefusal"`
		RightRefusal *string `json:"rightRefusal"`
		ShownRefusal string  `json:"shownRefusal"`
	}
	env.evalJSONWithin(t, "linkTest.protect.foreignExtensionFrameConcurrentShots('chrome-2')", &r, 30*time.Second)
	if !r.Left || !r.Right {
		t.Fatalf("concurrent hidden-frame screenshots: %+v", r)
	}
}

func TestCDPExtensionFrameDetachMidProbeReleasesHold(t *testing.T) {
	env, _ := setupFakeProtectEnv(t, nil, func(_, _ string) fakeScript {
		return extensionTwoTabScript("chrome-extension://"+harnessExtensionID+"/frame.html", true)
	})
	var r struct {
		OK           bool    `json:"ok"`
		Refusal      *string `json:"refusal"`
		ShownRefusal string  `json:"shownRefusal"`
	}
	env.evalJSONWithin(t, "linkTest.protect.foreignExtensionFrameDetachMidProbe('chrome-2')", &r, 30*time.Second)
	if !r.OK {
		got := "<nil>"
		if r.Refusal != nil {
			got = *r.Refusal
		}
		t.Fatalf("screenshot after a mid-probe detach was refused: refusal=%q", got)
	}
}

func extensionTwoTabScript(frameURL string, hangHoldProbe bool) fakeScript {
	var sessions atomic.Int32
	var contexts atomic.Int64
	var pages atomic.Int32
	var shot atomic.Bool
	var postShotOwners atomic.Int32
	pageInfo := func(id string) string {
		return `{"targetId":` + strconv.Quote(id) + `,"type":"page","title":"ordinary","url":"https://ordinary.test/","attached":false,"browserContextId":"default"}`
	}
	frameInfo := func(id, parent string) string {
		return `{"targetId":` + strconv.Quote(id) + `,"type":"iframe","title":"","url":` + strconv.Quote(frameURL) + `,"attached":false,"browserContextId":"default","parentFrameId":` + strconv.Quote(parent) + `}`
	}
	doc := `{"root":{"nodeId":1,"backendNodeId":1,"nodeType":9,"nodeName":"#document","localName":"","nodeValue":"","childNodeCount":1,"children":[]}}`
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Browser.getVersion":
			return `{"product":"Chrome/154.0.8037.98","protocolVersion":"1.3"}`, nil
		case "Target.getTargets":
			return `{"targetInfos":[]}`, nil
		case "Target.createTarget":
			n := pages.Add(1)
			pageID := "PAGE" + strconv.Itoa(int(n))
			frameID := "FRAME" + strconv.Itoa(int(n))
			return `{"targetId":` + strconv.Quote(pageID) + `}`, []string{
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + pageInfo(pageID) + `}}`,
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + frameInfo(frameID, pageID) + `}}`,
			}
		case "Target.attachToTarget":
			n := sessions.Add(1)
			id := "S" + strconv.Itoa(int(n))
			parent := ""
			if n == 1 {
				parent = `"sessionId":"RELAY",`
			}
			var p struct {
				TargetID string `json:"targetId"`
			}
			_ = json.Unmarshal(params, &p)
			info := pageInfo(p.TargetID)
			if strings.HasPrefix(p.TargetID, "FRAME") {
				info = frameInfo(p.TargetID, strings.Replace(p.TargetID, "FRAME", "PAGE", 1))
			}
			return `{"sessionId":"` + id + `"}`, []string{`{"method":"Target.attachedToTarget",` + parent + `"params":{"sessionId":"` + id + `","targetInfo":` + info + `,"waitingForDebugger":false}}`}
		case "Target.getTargetInfo":
			var p struct {
				TargetID string `json:"targetId"`
			}
			_ = json.Unmarshal(params, &p)
			info := pageInfo(p.TargetID)
			if strings.HasPrefix(p.TargetID, "FRAME") {
				info = frameInfo(p.TargetID, strings.Replace(p.TargetID, "FRAME", "PAGE", 1))
			}
			return `{"targetInfo":` + info + `}`, nil
		case "Target.detachFromTarget":
			var p struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(params, &p)
			if p.SessionID == "" {
				return "{}", nil
			}
			return "{}", []string{`{"method":"Target.detachedFromTarget","params":{"sessionId":` + strconv.Quote(p.SessionID) + `}}`}
		case "Page.getFrameTree":
			return `{"frameTree":{"frame":{"loaderId":"L1","url":"https://ordinary.test/","securityOrigin":"https://ordinary.test"}}}`, nil
		case "Page.getNavigationHistory":
			return `{"currentIndex":0,"entries":[{"id":1,"url":"https://ordinary.test/"}]}`, nil
		case "Page.captureScreenshot":
			shot.Store(true)
			return `{"data":"aaaa"}`, nil
		case "Page.createIsolatedWorld":
			id := contexts.Add(1)
			return `{"executionContextId":` + strconv.FormatInt(id, 10) + `}`, nil
		case "DOM.getDocument":
			return doc, nil
		case "DOM.getFrameOwner":
			if hangHoldProbe && shot.Load() && postShotOwners.Add(1) == 1 {
				return "#pending", nil
			}
			return `{"backendNodeId":2,"nodeId":2}`, nil
		case "DOM.resolveNode":
			return `{"object":{"type":"object","objectId":"ext-1"}}`, nil
		case "Runtime.callFunctionOn":
			return `{"result":{"type":"boolean","value":false}}`, nil
		case "Runtime.releaseObjectGroup":
			return `{}`, nil
		}
		return "{}", nil
	}
}

func extensionPaintDeadlineScript(frameURL string) fakeScript {
	var sessions atomic.Int32
	var contexts atomic.Int64
	var shot atomic.Bool
	page := `{"targetId":"PAGE","type":"page","title":"ordinary","url":"https://ordinary.test/","attached":false,"browserContextId":"default"}`
	frame := `{"targetId":"FRAME","type":"iframe","title":"","url":` + strconv.Quote(frameURL) + `,"attached":false,"browserContextId":"default","parentFrameId":"PAGE"}`
	targets := `{"targetInfos":[` + page + `,` + frame + `]}`
	doc := `{"root":{"nodeId":1,"backendNodeId":1,"nodeType":9,"nodeName":"#document","localName":"","nodeValue":"","childNodeCount":1,"children":[]}}`
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Browser.getVersion":
			return `{"product":"Chrome/154.0.8037.98","protocolVersion":"1.3"}`, nil
		case "Target.getTargets":
			return targets, nil
		case "Target.createTarget":
			return `{"targetId":"PAGE"}`, []string{
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + page + `}}`,
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + frame + `}}`,
			}
		case "Target.attachToTarget":
			n := sessions.Add(1)
			id := "S" + strconv.Itoa(int(n))
			parent := ""
			if n == 1 {
				parent = `"sessionId":"RELAY",`
			}
			info := page
			var p struct {
				TargetID string `json:"targetId"`
			}
			_ = json.Unmarshal(params, &p)
			if p.TargetID == "FRAME" {
				info = frame
			}
			return `{"sessionId":"` + id + `"}`, []string{`{"method":"Target.attachedToTarget",` + parent + `"params":{"sessionId":"` + id + `","targetInfo":` + info + `,"waitingForDebugger":false}}`}
		case "Target.getTargetInfo":
			var p struct {
				TargetID string `json:"targetId"`
			}
			_ = json.Unmarshal(params, &p)
			info := page
			if p.TargetID == "FRAME" {
				info = frame
			}
			return `{"targetInfo":` + info + `}`, nil
		case "Page.getFrameTree":
			return `{"frameTree":{"frame":{"id":"PAGE","loaderId":"L1","url":"https://ordinary.test/","securityOrigin":"https://ordinary.test"},"childFrames":[{"frame":{"id":"FRAME","loaderId":"L2","url":` + strconv.Quote(frameURL) + `,"securityOrigin":` + strconv.Quote("chrome-extension://"+harnessExtensionID) + `}}]}}`, nil
		case "Page.getNavigationHistory":
			return `{"currentIndex":0,"entries":[{"id":1,"url":"https://ordinary.test/"}]}`, nil
		case "Page.captureScreenshot":
			shot.Store(true)
			return `{"data":"aaaa"}`, nil
		case "Page.createIsolatedWorld":
			id := contexts.Add(1)
			return `{"executionContextId":` + strconv.FormatInt(id, 10) + `}`, nil
		case "DOM.getDocument":
			return doc, nil
		case "DOM.getFrameOwner":
			if shot.Load() {
				return "#pending", nil
			}
			return `{"backendNodeId":2,"nodeId":2}`, nil
		case "DOM.resolveNode":
			return `{"object":{"type":"object","objectId":"ext-1"}}`, nil
		case "Runtime.callFunctionOn":
			return `{"result":{"type":"boolean","value":false}}`, nil
		case "Runtime.releaseObjectGroup":
			return `{}`, nil
		}
		return "{}", nil
	}
}

func TestCDPExtensionFramePaintDeadlineReachesSeven(t *testing.T) {
	env, _ := setupFakeProtectEnv(t, []string{"SLICC_CDP_EXTENSION_PAINT_HOLD=500ms"}, func(_, _ string) fakeScript {
		return extensionPaintDeadlineScript("chrome-extension://" + harnessExtensionID + "/frame.html")
	})
	var r struct {
		OK           bool    `json:"ok"`
		Refusal      *string `json:"refusal"`
		ShownRefusal string  `json:"shownRefusal"`
		MS           int     `json:"ms"`
	}
	env.evalJSONWithin(t, "linkTest.protect.foreignExtensionFramePaintDeadline('chrome-2')", &r, 30*time.Second)
	if r.OK || r.Refusal == nil || *r.Refusal != protect.ShownRefusal() {
		got := "<nil>"
		if r.Refusal != nil {
			got = *r.Refusal
		}
		t.Fatalf("deadline refusal: ok=%t refusal=%q ms=%d want %q", r.OK, got, r.MS, protect.ShownRefusal())
	}
	if r.MS < 400 || r.MS > 5000 {
		t.Fatalf("deadline refusal took %d ms, want about the shortened 500ms hold without waiting on other traffic", r.MS)
	}
}
