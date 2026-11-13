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
	env, fake := setupFakeProtectEnv(t, nil, func(_, _ string) fakeScript {
		return extensionTwoTabScript(false)
	})
	fake.overlapCaptureHolds()
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

func TestCDPExtensionFrameSiblingProbeDirtiesInFlight(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, nil, func(_, _ string) fakeScript {
		return extensionSiblingDirtyScript("chrome-extension://" + harnessExtensionID + "/frame.html")
	})
	fake.delay("Page.captureScreenshot", 300*time.Millisecond)
	var r struct {
		FirstOK      bool    `json:"firstOk"`
		FirstRefusal *string `json:"firstRefusal"`
		ShownRefusal string  `json:"shownRefusal"`
	}
	env.evalJSONWithin(t, "linkTest.protect.foreignExtensionFrameSiblingDirtyShots('chrome-2')", &r, 30*time.Second)
	if r.FirstOK || r.FirstRefusal == nil || *r.FirstRefusal != protect.ShownRefusal() {
		got := "<nil>"
		if r.FirstRefusal != nil {
			got = *r.FirstRefusal
		}
		t.Fatalf("in-flight capture was not refused when a sibling probe sampled shown: ok=%t refusal=%q", r.FirstOK, got)
	}
}

func TestCDPExtensionFrameDetachMidProbeReleasesHold(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, nil, func(_, _ string) fakeScript {
		return extensionTwoTabScript(false)
	})
	fake.overlapCaptureHolds()
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

func extensionTwoTabScript(hangHoldProbe bool) fakeScript {
	frameURL := extensionFrameURL()
	var sessions atomic.Int32
	var contexts atomic.Int64
	var pages atomic.Int32
	var shot atomic.Bool
	var postShotCalls atomic.Int32
	var holdParked atomic.Bool
	pageInfo := func(id string) string {
		return `{"targetId":` + strconv.Quote(id) + `,"type":"page","title":"ordinary","url":"https://ordinary.test/","attached":false,"browserContextId":"default"}`
	}
	frameInfo := func(id, parent string) string {
		return `{"targetId":` + strconv.Quote(id) + `,"type":"iframe","title":"","url":` + strconv.Quote(frameURL) + `,"attached":false,"browserContextId":"default","parentFrameId":` + strconv.Quote(parent) + `}`
	}
	doc := `{"root":{"nodeId":1,"backendNodeId":1,"nodeType":9,"nodeName":"#document","localName":"","nodeValue":"","childNodeCount":1,"children":[]}}`
	owner := `{"backendNodeId":2,"nodeId":2}`
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
		case "Page.getLayoutMetrics":
			if hangHoldProbe && !holdParked.Load() {
				return "#pending:await-hold|{}", nil
			}
			return `{"cssVisualViewport":{"clientWidth":800,"clientHeight":600}}`, nil
		case "Page.captureScreenshot":
			shot.Store(true)
			return `{"data":"aaaa"}`, nil
		case "Page.createIsolatedWorld":
			id := contexts.Add(1)
			return `{"executionContextId":` + strconv.FormatInt(id, 10) + `}`, nil
		case "DOM.getDocument":
			return doc, nil
		case "DOM.getFrameOwner":
			return owner, nil
		case "DOM.resolveNode":
			return `{"object":{"type":"object","objectId":"ext-1"}}`, nil
		case "Runtime.callFunctionOn":
			if hangHoldProbe && shot.Load() && postShotCalls.Add(1) == 2 {
				holdParked.Store(true)
				return `#pending:hold|{"result":{"type":"boolean","value":false}}`, nil
			}
			return `{"result":{"type":"boolean","value":false}}`, nil
		case "Runtime.releaseObjectGroup":
			return `{}`, nil
		}
		return "{}", nil
	}
}

func extensionSiblingDirtyScript(frameURL string) fakeScript {
	var sessions atomic.Int32
	var contexts atomic.Int64
	var probes atomic.Int32
	page := `{"targetId":"PAGE","type":"page","title":"ordinary","url":"https://ordinary.test/","attached":false,"browserContextId":"default"}`
	frame := `{"targetId":"FRAME","type":"iframe","title":"","url":` + strconv.Quote(frameURL) + `,"attached":false,"browserContextId":"default","parentFrameId":"PAGE"}`
	doc := `{"root":{"nodeId":1,"backendNodeId":1,"nodeType":9,"nodeName":"#document","localName":"","nodeValue":"","childNodeCount":1,"children":[]}}`
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Browser.getVersion":
			return `{"product":"Chrome/154.0.8037.98","protocolVersion":"1.3"}`, nil
		case "Target.getTargets":
			return `{"targetInfos":[]}`, nil
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
			return `{"frameTree":{"frame":{"id":"PAGE","loaderId":"L1","url":"https://ordinary.test/","securityOrigin":"https://ordinary.test"}}}`, nil
		case "Page.getNavigationHistory":
			return `{"currentIndex":0,"entries":[{"id":1,"url":"https://ordinary.test/"}]}`, nil
		case "Page.captureScreenshot":
			return `{"data":"aaaa"}`, nil
		case "Page.createIsolatedWorld":
			id := contexts.Add(1)
			return `{"executionContextId":` + strconv.FormatInt(id, 10) + `}`, nil
		case "DOM.getDocument":
			return doc, nil
		case "DOM.getFrameOwner":
			return `{"backendNodeId":2,"nodeId":2}`, nil
		case "DOM.resolveNode":
			return `{"object":{"type":"object","objectId":"ext-1"}}`, nil
		case "Runtime.callFunctionOn":
			if probes.Add(1) == 4 {
				return `{"result":{"type":"boolean","value":true}}`, nil
			}
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

const ownExtensionOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"

func extensionFrameMarkerScript(foreignURL, ownURL string) fakeScript {
	var sessions atomic.Int32
	var creates atomic.Int32
	var lastPageSession atomic.Value
	page := `{"targetId":"PAGE","type":"page","title":"ordinary","url":"https://ordinary.test/","attached":false,"browserContextId":"default"}`
	page2 := `{"targetId":"PAGE2","type":"page","title":"other","url":"https://ordinary.test/other","attached":false,"browserContextId":"default"}`
	frame := `{"targetId":"FRAME","type":"iframe","title":"","url":` + strconv.Quote(foreignURL) + `,"attached":false,"browserContextId":"default","parentFrameId":"PAGE"}`
	frameEl := `{"targetId":"FRAMEEL","type":"iframe","title":"","url":` + strconv.Quote(foreignURL) + `,"attached":false,"browserContextId":"default","parentFrameId":"PAGE"}`
	frame2 := `{"targetId":"FRAME2","type":"iframe","title":"","url":` + strconv.Quote(foreignURL) + `,"attached":false,"browserContextId":"default","parentFrameId":"PAGE2"}`
	own := `{"targetId":"OWN","type":"iframe","title":"","url":` + strconv.Quote(ownURL) + `,"attached":false,"browserContextId":"other","parentFrameId":"OTHER"}`
	ordinary := `{"targetId":"ORDINARY","type":"iframe","title":"","url":"https://embed.test/","attached":false,"browserContextId":"default","parentFrameId":"PAGE"}`
	poison := `{"version":1,"kind":"extensionFrame","hidden":true,"focused":true,"url":` + strconv.Quote(foreignURL) + `,"attacker":"poisoned"}`
	iframe := func(id, frameID, src string) string {
		attrs := `["src",` + strconv.Quote(src) + `]`
		return `{"nodeId":` + id + `,"backendNodeId":` + id + `,"nodeType":1,"nodeName":"IFRAME","localName":"iframe","nodeValue":"","childNodeCount":0,"attributes":` + attrs + `,"frameId":` + strconv.Quote(frameID) + `,"sliccExtensionFrame":` + poison + `}`
	}
	frameNode := `{"nodeId":6,"backendNodeId":6,"nodeType":1,"nodeName":"FRAME","localName":"frame","nodeValue":"","childNodeCount":0,"attributes":[],"frameId":"FRAMEEL","sliccExtensionFrame":` + poison + `}`
	srcOnly := `{"nodeId":5,"backendNodeId":5,"nodeType":1,"nodeName":"IFRAME","localName":"iframe","nodeValue":"","childNodeCount":0,"attributes":["src",` + strconv.Quote(foreignURL) + `],"sliccExtensionFrame":` + poison + `}`
	doc := `{"root":{"nodeId":1,"backendNodeId":1,"nodeType":9,"nodeName":"#document","localName":"","nodeValue":"","childNodeCount":5,"children":[` +
		iframe("2", "FRAME", foreignURL) + `,` +
		iframe("3", "ORDINARY", "https://embed.test/") + `,` +
		srcOnly + `,` +
		iframe("4", "OWN", ownURL) + `,` +
		frameNode +
		`]}}`
	targets := `{"targetInfos":[` + page + `,` + page2 + `,` + frame + `,` + frameEl + `,` + frame2 + `,` + own + `,` + ordinary + `]}`
	infoOf := func(id string) string {
		switch id {
		case "PAGE2":
			return page2
		case "FRAME":
			return frame
		case "FRAMEEL":
			return frameEl
		case "FRAME2":
			return frame2
		case "OWN":
			return own
		case "ORDINARY":
			return ordinary
		}
		return page
	}
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Browser.getVersion":
			return `{"product":"Chrome/154.0.8037.98","protocolVersion":"1.3"}`, nil
		case "Target.getTargets":
			return targets, nil
		case "Target.createTarget":
			n := creates.Add(1)
			id, info := "PAGE", page
			if n > 1 {
				id, info = "PAGE2", page2
			}
			return `{"targetId":` + strconv.Quote(id) + `}`, []string{
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + info + `}}`,
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + frame + `}}`,
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + frameEl + `}}`,
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + frame2 + `}}`,
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + own + `}}`,
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":` + ordinary + `}}`,
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
			info := infoOf(p.TargetID)
			if p.TargetID == "PAGE" || p.TargetID == "PAGE2" || p.TargetID == "" {
				lastPageSession.Store(id)
			}
			return `{"sessionId":"` + id + `"}`, []string{`{"method":"Target.attachedToTarget",` + parent + `"params":{"sessionId":"` + id + `","targetInfo":` + info + `,"waitingForDebugger":false}}`}
		case "Target.getTargetInfo":
			var p struct {
				TargetID string `json:"targetId"`
			}
			_ = json.Unmarshal(params, &p)
			return `{"targetInfo":` + infoOf(p.TargetID) + `}`, nil
		case "Page.getFrameTree":
			return `{"frameTree":{"frame":{"id":"PAGE","loaderId":"L1","url":"https://ordinary.test/","securityOrigin":"https://ordinary.test"},"childFrames":[{"frame":{"id":"FRAME","loaderId":"L2","url":` + strconv.Quote(foreignURL) + `,"securityOrigin":` + strconv.Quote("chrome-extension://"+harnessExtensionID) + `}},{"frame":{"id":"FRAMEEL","loaderId":"L3","url":` + strconv.Quote(foreignURL) + `,"securityOrigin":` + strconv.Quote("chrome-extension://"+harnessExtensionID) + `}},{"frame":{"id":"ORDINARY","loaderId":"L4","url":"https://embed.test/","securityOrigin":"https://embed.test"}}]}}`, nil
		case "Page.getNavigationHistory":
			return `{"currentIndex":0,"entries":[{"id":1,"url":"https://ordinary.test/"}]}`, nil
		case "DOM.getDocument":
			sid, _ := lastPageSession.Load().(string)
			if sid == "" {
				sid = "S1"
			}
			return doc, []string{
				`{"method":"DOM.setChildNodes","sessionId":` + strconv.Quote(sid) + `,"params":{"parentNodeId":1,"nodes":[` + iframe("20", "FRAME", foreignURL) + `]}}`,
				`{"method":"DOM.childNodeInserted","sessionId":` + strconv.Quote(sid) + `,"params":{"parentNodeId":1,"previousNodeId":0,"node":` + iframe("21", "FRAME", foreignURL) + `}}`,
			}
		case "DOM.describeNode":
			return `{"node":` + iframe("2", "FRAME", foreignURL) + `}`, nil
		case "DOM.getFrameOwner":
			var p struct {
				FrameID string `json:"frameId"`
			}
			_ = json.Unmarshal(params, &p)
			switch p.FrameID {
			case "FRAME":
				return `{"backendNodeId":2,"nodeId":2,"contentDocument":{"nodeId":99},"sliccExtensionFrame":` + poison + `}`, nil
			case "FRAME2":
				return `{"backendNodeId":22,"nodeId":22}`, nil
			}
			return `{"backendNodeId":0}`, nil
		case "Page.createIsolatedWorld":
			return `{"executionContextId":7}`, nil
		case "Runtime.enable":
			sid, _ := lastPageSession.Load().(string)
			if sid == "" {
				sid = "S1"
			}
			inner := `{"id":1,"result":{"nodeName":"IFRAME","frameId":"FRAME","sliccExtensionFrame":` + poison + `}}`
			return `{}`, []string{
				`{"method":"Target.receivedMessageFromTarget","params":{"sessionId":` + strconv.Quote(sid) + `,"message":` + strconv.Quote(inner) + `}}`,
			}
		case "Runtime.evaluate":
			var p struct {
				Expression    string `json:"expression"`
				ReturnByValue bool   `json:"returnByValue"`
			}
			_ = json.Unmarshal(params, &p)
			if p.ReturnByValue && strings.Contains(p.Expression, "IFRAME") {
				return `{"result":{"type":"object","value":{"nodeName":"IFRAME","frameId":"FRAME","nodeId":2,"backendNodeId":2}}}`, nil
			}
			return `{"result":{"type":"number","value":1}}`, nil
		}
		return "{}", nil
	}
}

func setupExtensionFrameMarkerProtect(t *testing.T) *protectEnv {
	t.Helper()
	foreign := "chrome-extension://" + harnessExtensionID + "/frame.html"
	own := ownExtensionOrigin + "/frame.html"
	b := launchBrowser(t)
	srv := pageServer(t)
	plain := plainServer(t)
	allowed := strings.Replace(plain.URL, "127.0.0.1", "localhost", 1)
	fake := newFakeCDPBrowser(t, extensionFrameMarkerScript(foreign, own))
	env := &protectEnv{linkEnv: &linkEnv{t: t, b: b, origin: srv.URL}, plain: plain, allowed: allowed, seven: srv.URL}
	env.a = startAttachEnv(t, false, nil, "--cdp", b.endpoint, "--cdp-extra", fake.srv.URL, "--origin", srv.URL, "--origin", plain.URL, "--origin", ownExtensionOrigin, "--name", "it-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	return env
}

func extensionFrameMarkerOK(raw json.RawMessage) bool {
	var m struct {
		Version int    `json:"version"`
		Kind    string `json:"kind"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Version != 1 || m.Kind != "extensionFrame" {
		return false
	}
	var obj map[string]any
	return json.Unmarshal(raw, &obj) == nil && len(obj) == 2
}

func TestCDPExtensionFramePlacementMarker(t *testing.T) {
	env := setupExtensionFrameMarkerProtect(t)
	var r struct {
		OK          bool            `json:"ok"`
		Foreign     json.RawMessage `json:"foreign"`
		FrameEl     json.RawMessage `json:"frameEl"`
		Ordinary    json.RawMessage `json:"ordinary"`
		SrcOnly     json.RawMessage `json:"srcOnly"`
		Own         json.RawMessage `json:"own"`
		Described   json.RawMessage `json:"described"`
		SetChild    json.RawMessage `json:"setChild"`
		Inserted    json.RawMessage `json:"inserted"`
		Leaked      bool            `json:"leaked"`
		InboundLeft bool            `json:"inboundLeft"`
		Payload     string          `json:"payload"`
	}
	env.evalJSON(t, "linkTest.protect.extensionFramePlacementMarker('chrome-2')", &r)
	if !r.OK {
		t.Fatalf("placement marker fixture failed: %s", r.Payload)
	}
	for label, got := range map[string]json.RawMessage{"foreign": r.Foreign, "frameEl": r.FrameEl, "described": r.Described, "setChild": r.SetChild, "inserted": r.Inserted} {
		if !extensionFrameMarkerOK(got) {
			t.Fatalf("%s missing marker: got %s payload=%s", label, got, r.Payload)
		}
	}
	for label, got := range map[string]json.RawMessage{"ordinary": r.Ordinary, "srcOnly": r.SrcOnly, "own": r.Own} {
		if string(got) != "null" {
			t.Fatalf("%s unexpectedly marked: %s payload=%s", label, got, r.Payload)
		}
	}
	if r.Leaked || r.InboundLeft {
		t.Fatalf("marker leaked URL/id/origin or left inbound poison: leaked=%t inbound=%t payload=%s", r.Leaked, r.InboundLeft, r.Payload)
	}
}

func TestCDPExtensionFrameOwnerAdmission(t *testing.T) {
	env := setupExtensionFrameMarkerProtect(t)
	var r struct {
		Owner struct {
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"owner"`
		ForeignPage struct {
			OK bool `json:"ok"`
		} `json:"foreignPage"`
		Attach struct {
			OK bool `json:"ok"`
		} `json:"attach"`
		World struct {
			OK bool `json:"ok"`
		} `json:"world"`
		WorldOnParent struct {
			OK bool `json:"ok"`
		} `json:"worldOnParent"`
		Marker json.RawMessage `json:"marker"`
		Keys   []string        `json:"keys"`
	}
	env.evalJSON(t, "linkTest.protect.extensionFrameOwnerAdmission('chrome-2')", &r)
	if !r.Owner.OK {
		msg := ""
		if r.Owner.Error != nil {
			msg = r.Owner.Error.Message
		}
		t.Fatalf("DOM.getFrameOwner for the page's extension frame was refused: %s", msg)
	}
	if !extensionFrameMarkerOK(r.Marker) {
		t.Fatalf("getFrameOwner marker: %s", r.Marker)
	}
	wantKeys := []string{"backendNodeId", "nodeId", "sliccExtensionFrame"}
	if len(r.Keys) != len(wantKeys) {
		t.Fatalf("getFrameOwner keys %v want %v result=%s", r.Keys, wantKeys, r.Owner.Result)
	}
	for i, k := range wantKeys {
		if r.Keys[i] != k {
			t.Fatalf("getFrameOwner keys %v want %v", r.Keys, wantKeys)
		}
	}
	if strings.Contains(string(r.Owner.Result), "contentDocument") || strings.Contains(string(r.Owner.Result), "chrome-extension://") || strings.Contains(string(r.Owner.Result), "poisoned") {
		t.Fatalf("getFrameOwner leaked frame content or inbound marker: %s", r.Owner.Result)
	}
	if r.ForeignPage.OK {
		t.Fatal("DOM.getFrameOwner for another page's extension frame was admitted")
	}
	if r.Attach.OK {
		t.Fatal("Target.attachToTarget on the extension frame was admitted")
	}
	if r.World.OK {
		t.Fatal("Page.createIsolatedWorld on the extension frame was admitted")
	}
	if !r.WorldOnParent.OK {
		t.Fatal("Page.createIsolatedWorld on the parent frame was refused")
	}
}

func TestCDPExtensionFrameBiosLiveCheck(t *testing.T) {
	env := setupExtensionFrameProtect(t)
	pageID, frameID := env.openHiddenExtensionFrameTab(t, "extension-bios-live")
	var r struct {
		World     bool            `json:"world"`
		Owner     bool            `json:"owner"`
		Marker    json.RawMessage `json:"marker"`
		Resolved  bool            `json:"resolved"`
		Shown     *bool           `json:"shown"`
		CallOK    bool            `json:"callOk"`
		OwnerKeys []string        `json:"ownerKeys"`
	}
	env.evalJSONWithin(t, "linkTest.protect.extensionFrameBiosLiveCheck("+jsArgs("chrome", pageID, frameID)+")", &r, 30*time.Second)
	if !r.World || !r.Owner || !r.Resolved || !r.CallOK || r.Shown == nil {
		t.Fatalf("bios live-check sequence failed: %+v", r)
	}
	if *r.Shown {
		t.Fatal("hidden extension frame was reported as shown")
	}
	if !extensionFrameMarkerOK(r.Marker) {
		t.Fatalf("live-check getFrameOwner marker: %s", r.Marker)
	}
	hasBackend, hasMarker := false, false
	for _, k := range r.OwnerKeys {
		switch k {
		case "backendNodeId":
			hasBackend = true
		case "sliccExtensionFrame":
			hasMarker = true
		case "nodeId":
		default:
			t.Fatalf("live-check owner had unexpected key %q in %v", k, r.OwnerKeys)
		}
	}
	if !hasBackend || !hasMarker {
		t.Fatalf("live-check owner keys %v want backendNodeId and sliccExtensionFrame", r.OwnerKeys)
	}
}

func TestCDPExtensionFrameEvaluateResultUnmarked(t *testing.T) {
	env := setupExtensionFrameMarkerProtect(t)
	var r struct {
		OK     bool `json:"ok"`
		Marked bool `json:"marked"`
	}
	env.evalJSON(t, "linkTest.protect.extensionFrameEvaluateUnmarked('chrome-2')", &r)
	if !r.OK {
		t.Fatal("Runtime.evaluate failed")
	}
	if r.Marked {
		t.Fatal("evaluate result shaped like an owner node was marked")
	}
}

func TestCDPExtensionFrameInnerMessageMarkerStripped(t *testing.T) {
	env := setupExtensionFrameMarkerProtect(t)
	var r struct {
		OK      bool   `json:"ok"`
		Marked  bool   `json:"marked"`
		Message string `json:"message"`
	}
	env.evalJSON(t, "linkTest.protect.extensionFrameInnerMessageStrip('chrome-2')", &r)
	if !r.OK {
		t.Fatal("Target.receivedMessageFromTarget was not delivered")
	}
	if r.Marked {
		t.Fatalf("inner-message marker survived strip: %s", r.Message)
	}
}

func TestCDPExtensionFrameConcurrentProbesShareWorld(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, nil, func(_, _ string) fakeScript {
		return extensionTwoTabScript(false)
	})
	fake.delay("Page.getFrameTree", 150*time.Millisecond)
	fake.delay("Network.enable", 150*time.Millisecond)
	fake.delay("Page.createIsolatedWorld", 500*time.Millisecond)
	var r struct {
		OK      bool `json:"ok"`
		ShotOK  bool `json:"shotOk"`
		TypedOK bool `json:"typedOk"`
	}
	env.evalJSONWithin(t, "linkTest.protect.foreignExtensionFrameConcurrentProbeWorlds('chrome-2')", &r, 30*time.Second)
	if !r.OK || !r.ShotOK || !r.TypedOK {
		t.Fatalf("overlapping screenshot and insertText probes: %+v", r)
	}
	worlds := 0
	for _, raw := range fake.received() {
		if strings.Contains(raw, `"method":"Page.createIsolatedWorld"`) {
			worlds++
		}
	}
	if worlds != 1 {
		t.Fatalf("Page.createIsolatedWorld count=%d want 1 (base overlaps two probes and creates two)", worlds)
	}
}

func TestCDPExtensionFrameDetachReleasesHeldPaint(t *testing.T) {
	env, _ := setupFakeProtectEnv(t, []string{"SLICC_CDP_EXTENSION_PAINT_HOLD=30s"}, func(_, _ string) fakeScript {
		return extensionTwoTabScript(true)
	})
	var r struct {
		OK           bool    `json:"ok"`
		Refusal      *string `json:"refusal"`
		ShownRefusal string  `json:"shownRefusal"`
		MS           int     `json:"ms"`
	}
	env.evalJSONWithin(t, "linkTest.protect.foreignExtensionFrameDetachReleasesHold('chrome-2')", &r, 30*time.Second)
	if !r.OK {
		got := "<nil>"
		if r.Refusal != nil {
			got = *r.Refusal
		}
		t.Fatalf("detach did not refuse the held capture: refusal=%q", got)
	}
	if r.MS > 5000 {
		t.Fatalf("refusal arrived after %dms; want well under the 30s hold", r.MS)
	}
}
