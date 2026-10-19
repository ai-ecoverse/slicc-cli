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
	t.Run("the seven tab is still cut", env.testAttach)
	t.Run("script navigation onto seven still detaches", env.testScriptNavigate)
	t.Run("the link still works", env.testStillLinked)
}

func (e *protectEnv) extensionPage(t *testing.T, tag string) (string, *cdp.Session, *cdp.Subscription, worldContext) {
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
	deadline := time.After(15 * time.Second)
	for {
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
			if strings.HasPrefix(p.Context.Origin, "chrome-extension://") && !p.Context.AuxData.IsDefault {
				return created.TargetID, s, sub, p.Context
			}
		case <-deadline:
			t.Fatalf("the fixture extension's isolated world never showed up on an unguarded session; contexts %+v", seen)
		}
	}
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
		Detached bool `json:"detached"`
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
