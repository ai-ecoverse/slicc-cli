package integration

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const harnessExtensionID = "ankmbodmgimjemnojmeddfcdlbocolob"

func TestCDPExtensionFrameDoesNotLockOrdinaryTab(t *testing.T) {
	ext := filepath.Join("..", "..", "node_modules", "@ai-ecoverse", "slicc-shared-web", "harness", "extension")
	env := setupProtectOn(t, launchExtensionBrowser(t, ext), true)
	frameURL := "chrome-extension://" + harnessExtensionID + "/frame.html"
	var created struct {
		TargetID string `json:"targetId"`
	}
	env.unguardedCall(t, "Target.createTarget", map[string]any{"url": env.allowed + "/?n=extension-frame"}, &created)
	t.Cleanup(func() { env.b.closeTarget(created.TargetID) })
	waitTargetURL(t, env.b, created.TargetID, env.allowed)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := env.b.client.Attach(ctx, created.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Call(ctx, "Runtime.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	var embedded any
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
	}, &embedded); err != nil {
		t.Fatal(err)
	}
	frameTargetID := ""
	deadline := time.Now().Add(10 * time.Second)
	for {
		var listed struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				URL      string `json:"url"`
			} `json:"targetInfos"`
		}
		env.unguardedCall(t, "Target.getTargets", nil, &listed)
		for _, info := range listed.TargetInfos {
			if info.Type == "iframe" && strings.HasPrefix(info.URL, "chrome-extension://"+harnessExtensionID+"/") {
				frameTargetID = info.TargetID
				break
			}
		}
		if frameTargetID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the extension frame target never appeared")
		}
		time.Sleep(100 * time.Millisecond)
	}
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
	env.evalJSON(t, "linkTest.protect.foreignExtensionFrame("+jsArgs("chrome", created.TargetID, frameTargetID, env.allowed)+")", &r)
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
	env.evalJSON(t, "linkTest.protect.foreignExtensionFrameScreencast("+jsArgs("chrome", env.allowed, frameURL)+")", &cast)
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
