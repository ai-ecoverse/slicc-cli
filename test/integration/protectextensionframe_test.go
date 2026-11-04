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
		Attached         bool    `json:"attached"`
		Read             bool    `json:"read"`
		HiddenShot       bool    `json:"hiddenShot"`
		HiddenKey        bool    `json:"hiddenKey"`
		ShownShot        bool    `json:"shownShot"`
		ShownShotRefusal *string `json:"shownShotRefusal"`
		ShownKey         bool    `json:"shownKey"`
		ShownKeyRefusal  *string `json:"shownKeyRefusal"`
		Cast             bool    `json:"cast"`
		CastRefusal      *string `json:"castRefusal"`
		PDF              bool    `json:"pdf"`
		PDFRefusal       *string `json:"pdfRefusal"`
		Snap             bool    `json:"snap"`
		SnapRefusal      *string `json:"snapRefusal"`
		Listed           bool    `json:"listed"`
		TreeLeak         bool    `json:"treeLeak"`
		FrameAttach      bool    `json:"frameAttach"`
		ShownRefusal     string  `json:"shownRefusal"`
		PresentRefusal   string  `json:"presentRefusal"`
	}
	env.evalJSON(t, "linkTest.protect.foreignExtensionFrame("+jsArgs("chrome", created.TargetID, frameTargetID, env.allowed)+")", &r)
	if !r.Attached || !r.Read {
		t.Fatalf("the ordinary tab stayed locked: attached=%t read=%t", r.Attached, r.Read)
	}
	if !r.HiddenShot || !r.HiddenKey {
		t.Fatalf("a hidden extension frame still blocked screenshot or input: shot=%t key=%t", r.HiddenShot, r.HiddenKey)
	}
	if r.ShownShot || r.ShownShotRefusal == nil || *r.ShownShotRefusal != protect.ShownRefusal() {
		got := "<nil>"
		if r.ShownShotRefusal != nil {
			got = *r.ShownShotRefusal
		}
		t.Fatalf("a shown extension frame did not refuse the screenshot with shownRefusal: shot=%t refusal=%q want %q", r.ShownShot, got, protect.ShownRefusal())
	}
	if r.ShownKey || r.ShownKeyRefusal == nil || *r.ShownKeyRefusal != protect.ShownRefusal() {
		got := "<nil>"
		if r.ShownKeyRefusal != nil {
			got = *r.ShownKeyRefusal
		}
		t.Fatalf("a shown extension frame did not refuse input with shownRefusal: key=%t refusal=%q", r.ShownKey, got)
	}
	if r.Cast || r.CastRefusal == nil || *r.CastRefusal != protect.PresentRefusal() {
		got := "<nil>"
		if r.CastRefusal != nil {
			got = *r.CastRefusal
		}
		t.Fatalf("an extension frame present did not refuse screencast: cast=%t refusal=%q", r.Cast, got)
	}
	if r.PDF || r.PDFRefusal == nil || *r.PDFRefusal != protect.PresentRefusal() {
		got := "<nil>"
		if r.PDFRefusal != nil {
			got = *r.PDFRefusal
		}
		t.Fatalf("an extension frame present did not refuse printToPDF: pdf=%t refusal=%q", r.PDF, got)
	}
	if r.Snap || r.SnapRefusal == nil || *r.SnapRefusal != protect.PresentRefusal() {
		got := "<nil>"
		if r.SnapRefusal != nil {
			got = *r.SnapRefusal
		}
		t.Fatalf("an extension frame present did not refuse captureSnapshot: snap=%t refusal=%q want %q", r.Snap, got, protect.PresentRefusal())
	}
	if r.Listed || r.TreeLeak {
		t.Fatalf("the extension frame URL leaked: listed=%t treeLeak=%t", r.Listed, r.TreeLeak)
	}
	if r.FrameAttach {
		t.Fatal("the extension frame itself was attachable")
	}
}
