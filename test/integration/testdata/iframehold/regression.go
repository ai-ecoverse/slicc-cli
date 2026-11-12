package cdp

import (
	"encoding/json"
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

func TestIframeHoldUnknownParentTargetStaysHeld(t *testing.T) {
	g := iframeHoldGuard(t)
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "FRAME-unknown", shown: false}
	g.targets["F"] = frame
	g.attachParent = "S-unknown"
	frame.protected = g.decide(frame, false)
	if frame.protected {
		t.Fatal("iframe with unknown parent target was protected")
	}
	if !g.iframeUnresolvedLocked(frame) {
		t.Fatal("unknown parent target did not keep the iframe held")
	}
	g.attachParent = ""
}

func TestIframeHoldInProcessFrameUnderOrdinaryParent(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/"}
	g.sessions["S-page"] = &session{id: "S-page", target: "PAGE", typ: "page"}
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "FRAME-inprocess", shown: false}
	g.targets["F"] = frame
	g.attachParent = "S-page"
	g.noteIframeAttachParentLocked(targetInfo{TargetID: "F", Type: "iframe", URL: frame.url, ParentFrameID: "FRAME-inprocess"})
	frame.protected = g.decide(frame, false)
	if frame.protected {
		t.Fatal("ordinary iframe under ordinary parent was protected because parentFrameId was not a target")
	}
	if g.iframeUnresolvedLocked(frame) {
		t.Fatal("known ordinary parent target held an iframe whose parentFrameId was not a target")
	}
	g.attachParent = ""
	if g.listingHiddenLocked(targetInfo{TargetID: "F", Type: "iframe", URL: frame.url, ParentFrameID: "FRAME-inprocess"}) {
		t.Fatal("ordinary iframe with an in-process parentFrameId was hidden from listings")
	}
}

func TestIframeHoldRecordedFrameUnderSeven(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/"}
	g.noteChildFrameLocked("FRAME", "https://seven.sliccy.ai/embed")
	g.noteFrameParentLocked("FRAME", "PAGE")
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "FRAME", shown: false}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	if !frame.protected {
		t.Fatal("iframe was not protected from its recorded ancestor frame")
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

func TestIframeHoldURLLessStaysPendingUntilURL(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/"}
	g.sessions["S-page"] = &session{id: "S-page", target: "PAGE", typ: "page"}
	frame := &target{id: "F", typ: "iframe", url: "", parentFrame: "FRAME-plain", shown: false}
	g.targets["F"] = frame
	g.held["S-F"] = &heldSession{id: "S-F", parent: "S-page", target: "F", typ: "iframe", params: map[string]any{"sessionId": "S-F"}, waiting: true}
	if g.iframeUnresolvedLocked(frame) {
		t.Fatal("known parent made a URL-less iframe ancestry-unresolved")
	}
	if !g.iframePendingLocked(frame) {
		t.Fatal("URL-less iframe under a known parent was not pending")
	}
	settled := g.settleHeldWorkersLocked()
	if len(settled.page) != 0 || g.held["S-F"] == nil {
		t.Fatalf("settle delivered a URL-less iframe: page=%d held=%v", len(settled.page), g.held["S-F"] != nil)
	}
	frame.url = "https://ordinary.test/embed"
	frame.protected = g.decide(frame, false)
	settled = g.settleHeldWorkersLocked()
	if g.held["S-F"] != nil || len(settled.page) == 0 {
		t.Fatalf("settle did not deliver after URL: held=%v page=%d", g.held["S-F"] != nil, len(settled.page))
	}
}

func TestIframeHoldBrowserLevelNilParentStaysUnresolved(t *testing.T) {
	g := iframeHoldGuard(t)
	frame := &target{id: "CHILD", typ: "iframe", url: "https://ordinary.test/embed", shown: false}
	g.targets["CHILD"] = frame
	g.held["S-child"] = &heldSession{id: "S-child", parent: "", target: "CHILD", typ: "iframe", params: map[string]any{"sessionId": "S-child"}, waiting: false}
	if !g.iframeUnresolvedLocked(frame) {
		t.Fatal("browser-level iframe with no parent target was not ancestry-unresolved after URL")
	}
	if !g.iframePendingLocked(frame) {
		t.Fatal("browser-level iframe with a URL was not pending")
	}
	settled := g.settleHeldWorkersLocked()
	if g.held["S-child"] == nil || len(settled.page) != 0 {
		t.Fatalf("settle released browser-level iframe without ancestry: held=%v page=%d", g.held["S-child"] != nil, len(settled.page))
	}
}

func TestIframeHoldProtectedAncestorHidesURLLess(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["SEVEN"] = &target{id: "SEVEN", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	g.sessions["S-seven"] = &session{id: "S-seven", target: "SEVEN", typ: "page"}
	frame := &target{id: "F", typ: "iframe", url: "", parentFrame: "FRAME-seven", shown: false}
	g.targets["F"] = frame
	g.noteFrameParentLocked("FRAME-seven", "SEVEN")
	g.noteFrameParentLocked("F", "FRAME-seven")
	g.held["S-F"] = &heldSession{id: "S-F", parent: "S-seven", target: "F", typ: "iframe", params: map[string]any{"sessionId": "S-F"}, waiting: true}
	settled := g.settleHeldWorkersLocked()
	if g.held["S-F"] != nil {
		t.Fatal("URL-less iframe under seven stayed held instead of hiding")
	}
	if !frame.protected {
		t.Fatal("URL-less iframe under seven was not protected on settle")
	}
	if len(settled.page) != 0 {
		t.Fatalf("protected URL-less iframe was delivered to the page: %d events", len(settled.page))
	}
}

func TestIframeHoldMissingParentIdFrameTreeUnderSeven(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["SEVEN"] = &target{id: "SEVEN", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	g.frames["SEVEN"] = map[string]bool{"F": true}
	g.noteFrameParentLocked("F", "SEVEN")
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", shown: false}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	if !frame.protected {
		t.Fatal("missing parentId with frame-tree ancestry under seven was not protected")
	}
	if !g.listingHiddenLocked(targetInfo{TargetID: "F", Type: "iframe", URL: frame.url}) {
		t.Fatal("missing parentId iframe under seven was listable")
	}
	if g.targetReasonLocked("F") == "" {
		t.Fatal("missing parentId iframe under seven was attachable")
	}
}

func TestIframeHoldBrowserAttachFollowsOwnerProtect(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/doc"}
	g.browserSessions["S-browser"] = true
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "PAGE", shown: true}
	g.targets["F"] = frame
	g.sessions["S-F"] = &session{id: "S-F", target: "F", parent: "S-browser", typ: "iframe"}
	if g.iframeFollowRefusedLocked("S-F") {
		t.Fatal("ordinary browser-attached iframe was refused before its owner turned protected")
	}
	g.targets["PAGE"].url = "https://seven.sliccy.ai/doc"
	g.targets["PAGE"].protected = true
	_ = g.redecideIframesLocked("PAGE")
	if !frame.protected {
		t.Fatal("browser-attached iframe was not protected when its owner page turned protected")
	}
	if !g.sessionRefusedLocked("S-F") {
		t.Fatal("browser-attached iframe session was not refused after owner turned protected")
	}
}

func TestIframeHoldBrowserAttachOwnerHeldRefused(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/doc"}
	g.browserSessions["S-browser"] = true
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "PAGE", shown: true}
	g.targets["F"] = frame
	g.sessions["S-F"] = &session{id: "S-F", target: "F", parent: "S-browser", typ: "iframe"}
	g.held["S-page"] = &heldSession{id: "S-page", parent: "", target: "PAGE", typ: "page"}
	if !g.iframeFollowRefusedLocked("S-F") {
		t.Fatal("browser-attached iframe was not refused when its owner page was held")
	}
}

func TestIframeHoldAttachSessionDisagreesProtected(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/doc"}
	g.targets["OTHER"] = &target{id: "OTHER", typ: "page", url: "https://ordinary.test/other"}
	g.sessions["S-other"] = &session{id: "S-other", target: "OTHER", typ: "page"}
	frame := &target{id: "F", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "PAGE", shown: true}
	g.targets["F"] = frame
	g.sessions["S-F"] = &session{id: "S-F", target: "F", parent: "S-other", typ: "iframe"}
	if !g.iframeFollowRefusedLocked("S-F") {
		t.Fatal("iframe whose attaching session disagreed with its parent chain was not refused")
	}
}

func TestIframeHoldExtensionFrameUnderOrdinaryKeepsMainRule(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/doc"}
	frame := &target{id: "F", typ: "iframe", url: "chrome-extension://abcdefghijklmnopabcdefghijklmnop/frame.html", parentFrame: "FRAME-dangling", shown: false}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	info := targetInfo{TargetID: "F", Type: "iframe", URL: frame.url, ParentFrameID: "FRAME-dangling"}
	if !frame.protected || !g.attachedShouldHideLocked(info, "S-page") {
		t.Fatal("extension iframe under an ordinary page was auto-attachable")
	}
	if g.iframeAncestryUnknownLocked(frame) || g.iframeUnresolvedLocked(frame) || g.iframeConcealedLocked("F") {
		t.Fatal("extension iframe was ancestry-held or concealed under an ordinary page")
	}
	if g.iframeHiddenForParentLocked(frame) || g.listingHiddenLocked(info) || g.protectedIframeIDLocked("F") {
		t.Fatal("extension iframe lost main getTargetInfo/listing under an ordinary page")
	}
}

func TestIframeHoldExtensionFrameUnderSevenProtected(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["SEVEN"] = &target{id: "SEVEN", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	frame := &target{id: "F", typ: "iframe", url: "chrome-extension://abcdefghijklmnopabcdefghijklmnop/frame.html", parentFrame: "SEVEN", shown: false}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	info := targetInfo{TargetID: "F", Type: "iframe", URL: frame.url, ParentFrameID: "SEVEN"}
	if !g.iframeHiddenForParentLocked(frame) || !g.attachedShouldHideLocked(info, "S-seven") {
		t.Fatal("extension iframe under seven was not parent-hidden from auto-attach")
	}
	if !g.listingHiddenLocked(info) || !g.protectedIframeIDLocked("F") {
		t.Fatal("extension iframe under seven was still listable or answered by getTargetInfo")
	}
}

func reviewGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return newGuard(o, nil)
}

func reviewCommand(g *guard, raw string) pageVerdict {
	c, id, ok := parseCommand([]byte(raw))
	return g.fromCommand(c, id, ok)
}

func TestReviewMissingAncestry(t *testing.T) {
	g := reviewGuard(t)
	g.observeAll([]targetInfo{{TargetID: "SEVEN", Type: "page", URL: "https://seven.sliccy.ai/doc"}, {TargetID: "F", Type: "iframe", URL: "https://ordinary.test/embed"}})
	info := targetInfo{TargetID: "F", Type: "iframe", URL: "https://ordinary.test/embed"}
	if !g.listingHiddenLocked(info) {
		t.Error("initial discovery lists iframe with unknown ancestry")
	}
	v := reviewCommand(g, `{"id":1,"method":"Target.attachToTarget","params":{"targetId":"F","flatten":true}}`)
	if len(v.forward) > 0 {
		t.Errorf("attach forwarded: %s", v.forward)
	}
	a := g.fromBrowser([]byte(`{"id":1,"result":{"sessionId":"S-F"}}`))
	if len(a.forward) > 0 {
		t.Errorf("attach result delivered: %s", a.forward)
	}
	g.fromBrowser([]byte(`{"method":"Target.attachedToTarget","params":{"sessionId":"S-F","waitingForDebugger":false,"targetInfo":{"targetId":"F","type":"iframe","url":"https://ordinary.test/embed"}}}`))
	g.navTargets["F"] = &navTarget{target: "F", iframe: true, done: true}
	e := reviewCommand(g, `{"id":2,"sessionId":"S-F","method":"Runtime.evaluate","params":{"expression":"window.parent.postMessage({type:'test'}, '*')"}}`)
	if len(e.forward) > 0 {
		t.Errorf("evaluate forwarded with unknown ancestry: %s", e.forward)
	}
}

func TestReviewNestedHeldAncestor(t *testing.T) {
	g := reviewGuard(t)
	g.observeAll([]targetInfo{{TargetID: "PAGE", Type: "page", URL: "https://ordinary.test/"}, {TargetID: "MID", Type: "iframe", URL: "https://ordinary.test/mid", ParentID: "PAGE"}, {TargetID: "F", Type: "iframe", URL: "https://ordinary.test/embed", ParentID: "MID"}})
	g.sessions["S-F"] = &session{id: "S-F", target: "F", typ: "iframe"}
	g.held["S-MID"] = &heldSession{id: "S-MID", target: "MID", typ: "iframe"}
	if !g.sessionRefusedLocked("S-F") {
		t.Error("browser-level child session still admitted while intermediate ancestor held")
	}
	g.navTargets["F"] = &navTarget{target: "F", iframe: true, done: true}
	e := reviewCommand(g, `{"id":2,"sessionId":"S-F","method":"Runtime.evaluate","params":{"expression":"window.parent.postMessage({type:'test'}, '*')"}}`)
	if len(e.forward) > 0 {
		t.Errorf("evaluate forwarded under held ancestor: %s", e.forward)
	}
}

func TestReviewAttachOverwritesAncestry(t *testing.T) {
	g := reviewGuard(t)
	g.observeAll([]targetInfo{{TargetID: "SEVEN", Type: "page", URL: "https://seven.sliccy.ai/doc"}, {TargetID: "PAGE", Type: "page", URL: "https://ordinary.test/"}})
	g.sessions["S-page"] = &session{id: "S-page", target: "PAGE", typ: "page"}
	g.noteFrameParentLocked("INTERMEDIATE", "SEVEN")
	g.noteChildFrameLocked("INTERMEDIATE", "https://ordinary.test/intermediate")
	raw, _ := json.Marshal(map[string]any{"method": "Target.attachedToTarget", "sessionId": "S-page", "params": map[string]any{"sessionId": "S-F", "waitingForDebugger": false, "targetInfo": map[string]any{"targetId": "F", "type": "iframe", "url": "https://ordinary.test/embed", "parentId": "INTERMEDIATE"}}})
	v := g.fromBrowser(raw)
	if len(v.forward) > 0 || len(v.page) > 0 {
		t.Errorf("disagreeing attach delivered after protected chain overwritten: forward=%s page=%s parent=%q", v.forward, v.page, g.frameParents["INTERMEDIATE"])
	}
	if !g.targets["F"].protected {
		t.Error("iframe not sticky protected on ancestry disagreement")
	}
}

func TestReviewOwnerUnconfirmed(t *testing.T) {
	g := reviewGuard(t)
	g.observeAll([]targetInfo{{TargetID: "PAGE", Type: "page", URL: "https://ordinary.test/"}, {TargetID: "F", Type: "iframe", URL: "https://ordinary.test/embed", ParentID: "PAGE"}})
	g.sessions["S-F"] = &session{id: "S-F", target: "F", typ: "iframe"}
	g.navTargets["F"] = &navTarget{target: "F", iframe: true, done: true}
	g.navTargets["PAGE"] = &navTarget{target: "PAGE", done: true}
	g.observeLocked(targetInfo{TargetID: "PAGE", Type: "page", URL: "https://seven.sliccy.ai/doc"})
	if !g.sessionRefusedLocked("S-F") {
		t.Error("iframe remains admitted while owner navigation is unconfirmed")
	}
	e := reviewCommand(g, `{"id":2,"sessionId":"S-F","method":"Runtime.evaluate","params":{"expression":"window.parent.postMessage({type:'test'}, '*')"}}`)
	if len(e.forward) > 0 {
		t.Errorf("evaluate forwarded during owner confirmation: %s", e.forward)
	}
	a := reviewCommand(g, `{"id":3,"method":"Target.attachToTarget","params":{"targetId":"F","flatten":true}}`)
	if len(a.forward) > 0 {
		t.Errorf("new browser attach forwarded during owner confirmation: %s", a.forward)
	}
}

func TestReviewPlaceholderOverwrite(t *testing.T) {
	g := reviewGuard(t)
	g.observeAll([]targetInfo{{TargetID: "PAGE", Type: "page", URL: "https://ordinary.test/"}})
	g.noteFrameParentLocked("MID", "PAGE")
	g.noteChildFrameLocked("MID", "https://seven.sliccy.ai/doc")
	g.noteChildFrameLocked("MID", "://")
	g.observeLocked(targetInfo{TargetID: "F", Type: "iframe", URL: "https://ordinary.test/embed", ParentID: "MID"})
	if !g.targets["F"].protected || !g.listingHiddenLocked(targetInfo{TargetID: "F", Type: "iframe", URL: "https://ordinary.test/embed", ParentID: "MID"}) {
		t.Error("placeholder overwrite erased recorded protected frame before descendant discovery")
	}
}
