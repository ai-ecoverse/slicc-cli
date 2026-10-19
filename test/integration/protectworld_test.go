package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
)

const refusedWorld = "slicc: refused: that execution context belongs to a browser extension"

type extensionWorld struct {
	ID          int64  `json:"id"`
	UniqueID    string `json:"uniqueId"`
	ObjectID    string `json:"objectId"`
	CallFrameID string `json:"callFrameId,omitempty"`
}

type worldContext struct {
	ID       int64  `json:"id"`
	Origin   string `json:"origin"`
	Name     string `json:"name"`
	UniqueID string `json:"uniqueId"`
	AuxData  struct {
		IsDefault bool   `json:"isDefault"`
		Type      string `json:"type"`
		FrameID   string `json:"frameId"`
	} `json:"auxData"`
}

func setupProtectExtension(t *testing.T) *protectEnv {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "extension"))
	if err != nil {
		t.Fatal(err)
	}
	pipe := newBrowserPipe(t)
	b := launchBrowserWith(t, 0, pipe.prep(t), "--enable-unsafe-extension-debugging")
	var loaded struct {
		ID string `json:"id"`
	}
	pipe.call(t, "Extensions.loadUnpacked", map[string]any{"path": dir}, &loaded)
	if loaded.ID == "" {
		t.Fatal("Extensions.loadUnpacked gave no extension id")
	}
	return setupProtectOn(t, b)
}

func TestCDPHidesExtensionWorlds(t *testing.T) {
	env := setupProtectExtension(t)
	t.Run("an extension's world is hidden and its page stays usable", env.testExtensionWorld)
	t.Run("call frames in an extension's world are refused", env.testExtensionFrame)
	t.Run("a live world stays hidden through any churn of other worlds", env.testExtensionChurn)
	t.Run("breakpoint conditions can't run in an extension's world", env.testExtensionBreakpoint)
	t.Run("profiles, coverage and stacks leave out an extension's world", env.testExtensionProfile)
	t.Run("requests an extension's world makes show only their origin", env.testExtensionRequests)
	t.Run("the seven tab is still cut", env.testAttach)
	t.Run("script navigation onto seven still detaches", env.testScriptNavigate)
	t.Run("the link still works", env.testStillLinked)
}

func (e *protectEnv) extensionPage(t *testing.T, tag string) (string, *cdp.Session, *cdp.Subscription, worldContext) {
	t.Helper()
	target, s, sub, world, _ := e.extensionWorlds(t, tag)
	return target, s, sub, world
}

func (e *protectEnv) extensionWorlds(t *testing.T, tag string) (string, *cdp.Session, *cdp.Subscription, worldContext, worldContext) {
	t.Helper()
	var created struct {
		TargetID string `json:"targetId"`
	}
	e.unguardedCall(t, "Target.createTarget", map[string]any{"url": e.allowed + "/?n=" + tag}, &created)
	t.Cleanup(func() { e.b.closeTarget(created.TargetID) })
	waitTargetURL(t, e.b, created.TargetID, e.allowed)
	sub := e.b.client.Subscribe()
	t.Cleanup(sub.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := e.b.client.Attach(ctx, created.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Call(ctx, "Runtime.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	var seen []worldContext
	var world, main *worldContext
	deadline := time.After(15 * time.Second)
	for world == nil || main == nil {
		select {
		case ev := <-sub.Events():
			if ev.SessionID != s.ID || ev.Method != "Runtime.executionContextCreated" {
				continue
			}
			var p struct {
				Context worldContext `json:"context"`
			}
			_ = json.Unmarshal(ev.Params, &p)
			seen = append(seen, p.Context)
			switch c := p.Context; {
			case c.AuxData.FrameID != created.TargetID:
			case strings.HasPrefix(c.Origin, "chrome-extension://") && !c.AuxData.IsDefault:
				world = &c
			case c.AuxData.IsDefault:
				main = &c
			}
		case <-deadline:
			t.Fatalf("the fixture extension's isolated world never showed up on an unguarded session; contexts %+v", seen)
		}
	}
	return created.TargetID, s, sub, *world, *main
}

func (e *protectEnv) testExtensionWorld(t *testing.T) {
	target, s, _, world := e.extensionPage(t, "world")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var obj struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if err := s.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "globalThis.sliccFixtureWorld", "contextId": world.ID}, &obj); err != nil || obj.Result.ObjectID == "" {
		t.Fatalf("the unguarded session can't reach the fixture's world: %v %+v", err, obj)
	}
	var r struct {
		Main            cdpOutcome     `json:"main"`
		ContextID       cdpOutcome     `json:"contextId"`
		UniqueContextID cdpOutcome     `json:"uniqueContextId"`
		CallOnContext   cdpOutcome     `json:"callOnContext"`
		Lexical         cdpOutcome     `json:"lexical"`
		Observer        cdpOutcome     `json:"observerObjectId"`
		ObjectID        cdpOutcome     `json:"objectId"`
		Properties      cdpOutcome     `json:"properties"`
		Argument        cdpOutcome     `json:"argument"`
		After           cdpOutcome     `json:"after"`
		Contexts        []worldContext `json:"contexts"`
		Console         []struct {
			ContextID int64 `json:"contextId"`
		} `json:"console"`
		Detached   bool       `json:"detached"`
		Heap       cdpOutcome `json:"heap"`
		HeapLeak   bool       `json:"heapLeak"`
		HeapChunks int        `json:"heapChunks"`
		Binding    cdpOutcome `json:"binding"`
		Bound      string     `json:"bound"`
	}
	w := extensionWorld{ID: world.ID, UniqueID: world.UniqueID, ObjectID: obj.Result.ObjectID}
	var raw map[string]any
	e.evalJSON(t, "linkTest.protect.extensionWorld("+jsArgs("chrome", target, w)+")", &raw)
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if !r.Main.OK || strings.Contains(string(b), "fake-value-0001") {
		t.Errorf("the page's own world through the guard: %s", b)
	}
	for name, o := range map[string]cdpOutcome{"Runtime.evaluate contextId": r.ContextID, "Runtime.evaluate uniqueContextId": r.UniqueContextID, "Runtime.callFunctionOn executionContextId": r.CallOnContext, "Runtime.globalLexicalScopeNames": r.Lexical, "an objectId the observer got": r.Observer, "Runtime.callFunctionOn objectId": r.ObjectID, "Runtime.getProperties": r.Properties, "an objectId as an argument": r.Argument} {
		if o.Error.Message != refusedWorld {
			t.Errorf("%s in the extension's world: %+v", name, o)
		}
	}
	if r.Heap.Error.Message != "slicc: refused: HeapProfiler reads the page's whole renderer, browser extensions' worlds included" || r.HeapLeak || r.HeapChunks != 0 {
		t.Errorf("a heap snapshot of the page with an extension's world: %+v, leaked %v in %d chunks", r.Heap, r.HeapLeak, r.HeapChunks)
	}
	var inWorld string
	wctx, wcancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer wcancel()
	if err := s.Evaluate(wctx, "typeof globalThis.sliccSevenBinding", cdp.EvaluateOptions{ContextID: int(world.ID), ReturnByValue: true}, &inWorld); err != nil {
		t.Fatal(err)
	}
	if !r.Binding.OK || r.Bound != "function" || inWorld != "undefined" {
		t.Errorf("an untargeted Runtime.addBinding gave %+v; the page's world sees a %s, the extension's world a %s", r.Binding, r.Bound, inWorld)
	}
	if r.Detached || !r.After.OK {
		t.Errorf("the page with an extension's world was cut: detached %v, evaluate after %+v", r.Detached, r.After)
	}
	if len(r.Contexts) == 0 {
		t.Errorf("seven saw no execution context at all on the page")
	}
	for _, c := range r.Console {
		if c.ContextID == world.ID {
			t.Errorf("seven saw the extension's console message: %s", b)
		}
	}
	for _, c := range r.Contexts {
		if strings.HasPrefix(c.Origin, "chrome-extension://") || c.ID == world.ID || c.UniqueID == world.UniqueID {
			t.Errorf("seven saw the extension's world: %+v", c)
		}
	}
}

func (e *protectEnv) testExtensionFrame(t *testing.T) {
	target, s, sub, world := e.extensionPage(t, "frame")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.Call(ctx, "Debugger.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = s.Call(c, "Runtime.evaluate", map[string]any{"expression": "(function () { let probe = 0; debugger; return probe; })()", "contextId": world.ID}, nil)
	}()
	var frame string
	deadline := time.After(15 * time.Second)
	for frame == "" {
		select {
		case ev := <-sub.Events():
			if ev.SessionID != s.ID || ev.Method != "Debugger.paused" {
				continue
			}
			var p struct {
				CallFrames []struct {
					CallFrameID string `json:"callFrameId"`
				} `json:"callFrames"`
			}
			_ = json.Unmarshal(ev.Params, &p)
			if len(p.CallFrames) > 0 {
				frame = p.CallFrames[0].CallFrameID
			}
		case <-deadline:
			t.Fatal("the unguarded session never paused in the extension's world")
		}
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Call(c, "Debugger.resume", nil, nil)
		_ = s.Call(c, "Debugger.disable", nil, nil)
	})
	var r struct {
		Evaluate     cdpOutcome `json:"evaluate"`
		SetVariable  cdpOutcome `json:"setVariable"`
		PausedFrames [][]string `json:"pausedFrames"`
		Scripts      []struct {
			URL       string `json:"url"`
			ContextID int64  `json:"contextId"`
		} `json:"scripts"`
	}
	var raw map[string]any
	e.evalJSON(t, "linkTest.protect.extensionFrame("+jsArgs("chrome", target, frame)+")", &raw)
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "fake-value-0001") {
		t.Errorf("the extension's secret reached seven: %s", b)
	}
	for name, o := range map[string]cdpOutcome{"Debugger.evaluateOnCallFrame": r.Evaluate, "Debugger.setVariableValue": r.SetVariable} {
		if o.Error.Message != refusedWorld {
			t.Errorf("%s on a frame in the extension's world: %+v", name, o)
		}
	}
	for _, frames := range r.PausedFrames {
		for _, id := range frames {
			if id == frame {
				t.Errorf("seven was shown a pause in the extension's world: %v", r.PausedFrames)
			}
		}
	}
	for _, sc := range r.Scripts {
		if sc.ContextID == world.ID || strings.HasPrefix(sc.URL, "chrome-extension://") {
			t.Errorf("seven was shown a script in the extension's world: %+v", sc)
		}
	}
}

func (e *protectEnv) testExtensionChurn(t *testing.T) {
	target, _, _, world, main := e.extensionWorlds(t, "churn")
	var r struct {
		Ms    int        `json:"ms"`
		World cdpOutcome `json:"world"`
		Main  cdpOutcome `json:"main"`
	}
	e.evalJSONWithin(t, "linkTest.protect.extensionChurn("+jsArgs("chrome", target, world.ID, main.ID, 50, 50)+")", &r, 3*time.Minute)
	t.Logf("2500 frames, each with its own extension world, came and went in %d ms", r.Ms)
	if r.World.Error.Message != refusedWorld {
		t.Errorf("the page's extension world after the churn: %+v", r.World)
	}
	if !r.Main.OK {
		t.Errorf("the page's own world after the churn: %+v", r.Main)
	}
}

func (e *protectEnv) testExtensionBreakpoint(t *testing.T) {
	target, _, _, _ := e.extensionPage(t, "breakpoint")
	var r struct {
		Regex  cdpOutcome `json:"regex"`
		Hash   cdpOutcome `json:"hash"`
		Blank  cdpOutcome `json:"blank"`
		Page   cdpOutcome `json:"page"`
		Plain  cdpOutcome `json:"plain"`
		Leak   string     `json:"leak"`
		Paused int        `json:"paused"`
	}
	e.evalJSON(t, "linkTest.protect.extensionBreakpoint("+jsArgs("chrome", target, e.allowed+"/?n=breakpoint")+")", &r)
	const want = "slicc: refused: a breakpoint condition that could run in a browser extension's world needs a url naming the page's own script"
	for name, o := range map[string]cdpOutcome{"urlRegex": r.Regex, "scriptHash": r.Hash, "an empty url": r.Blank} {
		if o.Error.Message != want {
			t.Errorf("a conditional breakpoint by %s: %+v", name, o)
		}
	}
	if !r.Page.OK || !r.Plain.OK {
		t.Errorf("a conditional breakpoint on the page's own URL gave %+v, and a plain one by urlRegex %+v", r.Page, r.Plain)
	}
	if r.Leak != `{"sliccWorld":""}` {
		t.Errorf("after a reload breakpoint conditions left %s in the page's DOM", r.Leak)
	}
	if r.Paused != 0 {
		t.Errorf("seven saw %d pauses in the extension's world", r.Paused)
	}
}

func (e *protectEnv) testExtensionProfile(t *testing.T) {
	target, _, _, _ := e.extensionPage(t, "profile")
	var r struct {
		Profile   string     `json:"profile"`
		Precise   string     `json:"precise"`
		Best      string     `json:"best"`
		Stacks    string     `json:"stacks"`
		Pongs     int        `json:"pongs"`
		Console   string     `json:"console"`
		Initiator string     `json:"initiator"`
		Socket    string     `json:"socket"`
		NodeStack string     `json:"nodeStack"`
		Body      cdpOutcome `json:"body"`
	}
	e.evalJSON(t, "linkTest.protect.extensionProfile("+jsArgs("chrome", target)+")", &r)
	if r.Pongs == 0 {
		t.Fatal("the page never heard back from the fixture's world")
	}
	if strings.Contains(r.Console, "fake-value-0001") || strings.Contains(r.Console, "slicc-fixture-profile") || strings.Contains(r.Console, "chrome-extension://") {
		t.Errorf("legacy console or console.profile events show the extension's world: %s", r.Console)
	}
	if strings.Contains(r.Initiator, "chrome-extension://") || strings.Contains(r.Initiator, "sliccFixtureBusy") || !r.Body.refused() {
		t.Errorf("a request from the extension's world shows its initiator %s, and its body gave %+v", r.Initiator, r.Body)
	}
	if !strings.Contains(r.NodeStack, `"ok":true`) || !strings.Contains(r.Socket, "slicc-page.js") {
		t.Errorf("the page's own frames are gone: node stack %s, socket initiator %s", r.NodeStack, r.Socket)
	}
	for name, got := range map[string]string{"Profiler.stop": r.Profile, "Profiler.takePreciseCoverage": r.Precise, "Profiler.getBestEffortCoverage": r.Best, "consoleAPICalled stack traces": r.Stacks, "webSocketCreated initiator": r.Socket, "DOM.getNodeStackTraces": r.NodeStack} {
		if strings.Contains(got, "chrome-extension://") || strings.Contains(got, "sliccFixtureBusy") {
			t.Errorf("%s shows the extension's world: %s", name, got)
		}
	}
	if !strings.Contains(r.Profile, "(program)") || !strings.Contains(r.Best, "slicc-warm.js") || !strings.Contains(r.Precise, "slicc-page.js") || !strings.Contains(r.Stacks, "slicc-page.js") {
		t.Errorf("the profile or coverage lost the page's own scripts: %s %s", r.Profile, r.Best)
	}
}

func (e *protectEnv) testExtensionRequests(t *testing.T) {
	for _, intercept := range []bool{false, true} {
		target, _, _, _ := e.extensionPage(t, "requests")
		var r struct {
			Fetched  *string     `json:"fetched"`
			Seen     []string    `json:"seen"`
			Text     string      `json:"text"`
			PostData *cdpOutcome `json:"postData"`
			Paused   int         `json:"paused"`
		}
		e.evalJSON(t, "linkTest.protect.extensionRequests("+jsArgs("chrome", target, intercept)+")", &r)
		if r.Fetched == nil || *r.Fetched != "" {
			t.Errorf("intercept %v: the extension's own request didn't complete: %v", intercept, r.Fetched)
		}
		for _, leak := range []string{"fake-value-0002", "fake-value-0003", "fake-value-0004", "account=", "x-slicc-fixture"} {
			if strings.Contains(r.Text, leak) {
				t.Errorf("intercept %v: seven saw %s from the extension's request: %s", intercept, leak, r.Text)
			}
		}
		if intercept && r.Paused != 0 {
			t.Errorf("seven was handed %d pauses of the extension's request", r.Paused)
		}
		if !intercept && (len(r.Seen) == 0 || r.PostData == nil || !r.PostData.refused()) {
			t.Errorf("the extension's request reached seven as %v, and its post data gave %+v", r.Seen, r.PostData)
		}
	}
}
