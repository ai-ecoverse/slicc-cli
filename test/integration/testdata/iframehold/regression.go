package cdp

import (
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func iframeHoldGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return newGuard(o, nil)
}

func TestIframeHoldOrdinaryURLUnderSeven(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["SEVEN"] = &target{id: "SEVEN", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "SEVEN"}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	if !frame.protected {
		t.Fatal("ordinary-URL iframe under seven was not protected")
	}
	if g.targetReasonLocked("F") == "" {
		t.Fatal("ordinary-URL iframe under seven was attachable")
	}
	info := targetInfo{TargetID: "F", Type: "iframe", URL: frame.url, ParentFrameID: "SEVEN"}
	if !g.listingHiddenLocked(info) {
		t.Fatal("ordinary-URL iframe under seven was listable")
	}
}

func TestIframeHoldSrcdocUnderSeven(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["SEVEN"] = &target{id: "SEVEN", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	frame := &target{id: "F", typ: "iframe", url: "about:srcdoc", parentFrame: "SEVEN"}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	if !frame.protected {
		t.Fatal("about:srcdoc iframe under seven was not protected")
	}
}

func TestIframeHoldNestedUnderSevenOrdinary(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["SEVEN"] = &target{id: "SEVEN", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	mid := &target{id: "MID", typ: "iframe", url: "https://ordinary.test/mid", parentFrame: "SEVEN"}
	g.targets["MID"] = mid
	mid.protected = g.decide(mid, false)
	nested := &target{id: "NEST", typ: "iframe", url: "https://ordinary.test/nest", parentFrame: "MID"}
	g.targets["NEST"] = nested
	nested.protected = g.decide(nested, false)
	if !mid.protected || !nested.protected {
		t.Fatalf("nested ordinary iframe under seven: mid=%t nest=%t", mid.protected, nested.protected)
	}
}

func TestIframeHoldOrdinaryURLUnderVault(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["VAULT"] = &target{id: "VAULT", typ: "page", url: "https://vault.sliccy.ai/doc", protected: true}
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "VAULT"}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	if !frame.protected {
		t.Fatal("ordinary-URL iframe under vault was not protected")
	}
}

func TestIframeHoldSrcdocUnderVault(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["VAULT"] = &target{id: "VAULT", typ: "page", url: "https://vault.sliccy.ai/doc", protected: true}
	frame := &target{id: "F", typ: "iframe", url: "about:srcdoc", parentFrame: "VAULT"}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	if !frame.protected {
		t.Fatal("about:srcdoc iframe under vault was not protected")
	}
}

func TestIframeHoldNestedUnderVaultSrcdoc(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["VAULT"] = &target{id: "VAULT", typ: "page", url: "https://vault.sliccy.ai/doc", protected: true}
	mid := &target{id: "MID", typ: "iframe", url: "about:srcdoc", parentFrame: "VAULT"}
	g.targets["MID"] = mid
	mid.protected = g.decide(mid, false)
	nested := &target{id: "NEST", typ: "iframe", url: "https://ordinary.test/nest", parentFrame: "MID"}
	g.targets["NEST"] = nested
	nested.protected = g.decide(nested, false)
	if !mid.protected || !nested.protected {
		t.Fatalf("nested iframe under vault srcdoc: mid=%t nest=%t", mid.protected, nested.protected)
	}
}

func TestIframeHoldUnknownParentStaysHeld(t *testing.T) {
	g := iframeHoldGuard(t)
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "FRAME-unknown", shown: false}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	if frame.protected {
		t.Fatal("iframe with unknown parent was protected before the chain was known")
	}
	if !g.iframeUnresolvedLocked(frame) {
		t.Fatal("unknown parent did not keep the iframe held")
	}
	if reason := g.targetReasonLocked("F"); reason == "" {
		t.Fatal("iframe with unknown parent was attachable by id")
	}
	_, hide := g.applyResultKindLocked(kindAttach, "F", "", map[string]any{"sessionId": "S-F"})
	if !hide {
		t.Fatal("attach of an unresolved iframe was admitted")
	}
}

func TestIframeHoldStickyAfterAncestorNavigatesAway(t *testing.T) {
	g := iframeHoldGuard(t)
	g.noteChildFrameLocked("FRAME", "https://seven.sliccy.ai/embed")
	g.noteFrameParentLocked("FRAME", "PAGE")
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/"}
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "FRAME", shown: false}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	if !frame.protected {
		t.Fatal("iframe was not protected from its recorded ancestor frame")
	}
	_ = g.noteChildFrameLocked("FRAME", "https://ordinary.test/embed")
	frame.protected = g.decide(frame, frame.protected)
	if !frame.protected {
		t.Fatal("redecide cleared a sticky protected iframe after its ancestor navigated away")
	}
	if g.targetReasonLocked("F") == "" {
		t.Fatal("sticky protected iframe became attachable after its ancestor navigated away")
	}
	info := targetInfo{TargetID: "F", Type: "iframe", URL: frame.url, ParentFrameID: "FRAME"}
	if !g.listingHiddenLocked(info) {
		t.Fatal("sticky protected iframe was not filtered from listings")
	}
}

func TestIframeHoldOrdinaryUnderOrdinary(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/"}
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "PAGE"}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	if frame.protected {
		t.Fatal("ordinary iframe under ordinary page was protected")
	}
	if g.iframeUnresolvedLocked(frame) {
		t.Fatal("ordinary iframe under ordinary page stayed unresolved")
	}
	if g.targetReasonLocked("F") != "" {
		t.Fatalf("ordinary iframe was refused: %q", g.targetReasonLocked("F"))
	}
}
