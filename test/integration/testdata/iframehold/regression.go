package cdp

import (
	"encoding/json"
	"testing"
	"time"

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
	if !g.listingHiddenLocked(targetInfo{TargetID: "CHILD", Type: "iframe", URL: frame.url}) || g.targetReasonLocked("CHILD") == "" {
		t.Fatal("browser-level ordinary iframe with unknown ancestry was listable or attachable")
	}
}

func TestIframeHoldBrowserLevelChromeErrorReleasesForConfirm(t *testing.T) {
	g := iframeHoldGuard(t)
	frame := &target{id: "CHILD", typ: "iframe", url: "", shown: false}
	g.targets["CHILD"] = frame
	g.held["S-child"] = &heldSession{id: "S-child", parent: "", target: "CHILD", typ: "iframe", params: map[string]any{"sessionId": "S-child"}, waiting: false}
	if !g.iframePendingLocked(frame) {
		t.Fatal("URL-less browser-level iframe was not pending")
	}
	frame.url = "chrome-error://chromewebdata/"
	frame.protected = g.decide(frame, false)
	if g.iframeUnresolvedLocked(frame) {
		t.Fatal("browser-level chrome-error iframe stayed ancestry-unresolved")
	}
	settled := g.settleHeldWorkersLocked()
	if g.held["S-child"] != nil || len(settled.page) == 0 {
		t.Fatalf("settle did not deliver browser-level chrome-error iframe for confirmation: held=%v page=%d", g.held["S-child"] != nil, len(settled.page))
	}
	if !g.listingHiddenLocked(targetInfo{TargetID: "CHILD", Type: "iframe", URL: frame.url}) {
		t.Fatal("browser-level chrome-error iframe with unknown ancestry was listable")
	}
}

func chromeErrorConfirmMessage(target, parent, unreachable string) message {
	frame := map[string]any{"id": target, "url": "chrome-error://chromewebdata/", "securityOrigin": "://", "unreachableUrl": unreachable}
	if parent != "" {
		frame["parentId"] = parent
	}
	raw, _ := json.Marshal(map[string]any{"frameTree": map[string]any{"frame": frame}})
	return message{Result: raw}
}

func chromeErrorConfirmGuard(t *testing.T, parent string) (*guard, *target, *navTarget) {
	t.Helper()
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{{TargetID: "SEVEN", Type: "page", URL: "https://seven.sliccy.ai/doc"}, {TargetID: "PAGE", Type: "page", URL: "https://ordinary.test/"}})
	frame := &target{id: "CHILD", typ: "iframe", url: "chrome-error://chromewebdata/", shown: true}
	g.targets["CHILD"] = frame
	g.sessions["S-child"] = &session{id: "S-child", target: "CHILD", typ: "iframe"}
	if parent == "HELD" {
		g.targets["MID"] = &target{id: "MID", typ: "iframe", url: "https://ordinary.test/mid"}
		g.held["S-mid"] = &heldSession{id: "S-mid", target: "MID", typ: "iframe"}
	}
	if parent == "M" {
		g.observeLocked(targetInfo{TargetID: "M", Type: "iframe", URL: "https://embed.test/m", ParentFrameID: "INPROC"})
	}
	confirm := &navTarget{ready: make(chan struct{}), start: time.Now()}
	nt := &navTarget{target: "CHILD", session: "GUARD", iframe: true, confirm: confirm}
	g.navTargets["CHILD"] = nt
	return g, frame, nt
}

func TestIframeHoldChromeErrorFrameTreeProtectedParent(t *testing.T) {
	g, frame, nt := chromeErrorConfirmGuard(t, "SEVEN")
	_ = g.confirmedTargetLocked(nt, nt.confirm, 0, chromeErrorConfirmMessage("CHILD", "SEVEN", "https://ordinary.test/fail"))
	if g.frameParents["CHILD"] != "SEVEN" {
		t.Fatalf("frame-tree parent was not recorded: %q", g.frameParents["CHILD"])
	}
	if !frame.protected || !nt.confirm.done || !nt.confirm.failed {
		t.Fatalf("chrome-error under seven was not protected: protected=%v done=%v failed=%v", frame.protected, nt.confirm.done, nt.confirm.failed)
	}
	if !g.sessionRefusedLocked("S-child") {
		t.Fatal("client session remained admitted under seven")
	}
	e := reviewCommand(g, `{"id":2,"sessionId":"S-child","method":"Runtime.evaluate","params":{"expression":"window.parent.postMessage({type:'test'}, '*')"}}`)
	if len(e.forward) > 0 {
		t.Errorf("evaluate forwarded under seven: %s", e.forward)
	}
}

func TestIframeHoldChromeErrorFrameTreeHeldOrUnknownParent(t *testing.T) {
	for _, parent := range []string{"HELD", "UNKNOWN", "M"} {
		t.Run(parent, func(t *testing.T) {
			id := parent
			if parent == "HELD" {
				id = "MID"
			}
			g, frame, nt := chromeErrorConfirmGuard(t, parent)
			_ = g.confirmedTargetLocked(nt, nt.confirm, 0, chromeErrorConfirmMessage("CHILD", id, "https://ordinary.test/fail"))
			if !frame.protected || !nt.confirm.failed {
				t.Fatalf("chrome-error with %s parent was released: protected=%v failed=%v parent=%q", parent, frame.protected, nt.confirm.failed, g.frameParents["CHILD"])
			}
			if !g.sessionRefusedLocked("S-child") {
				t.Fatal("client session remained admitted")
			}
		})
	}
}

func TestIframeHoldUnknownAncestryPropagatesToChild(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "SEVEN", Type: "page", URL: "https://seven.sliccy.ai/doc"},
		{TargetID: "M", Type: "iframe", URL: "https://embed.test/m", ParentFrameID: "INPROC"},
		{TargetID: "N", Type: "iframe", URL: "https://inner.test/n", ParentFrameID: "M"},
	})
	if !g.listingHiddenLocked(targetInfo{TargetID: "M", Type: "iframe", URL: "https://embed.test/m", ParentFrameID: "INPROC"}) {
		t.Fatal("M with unknown ancestry was listable")
	}
	info := targetInfo{TargetID: "N", Type: "iframe", URL: "https://inner.test/n", ParentFrameID: "M"}
	if !g.listingHiddenLocked(info) || g.targetReasonLocked("N") == "" {
		t.Fatalf("N under unknown-ancestry M was shown: reason=%q", g.targetReasonLocked("N"))
	}
	g.sessions["S-N"] = &session{id: "S-N", target: "N", typ: "iframe"}
	g.navTargets["N"] = &navTarget{target: "N", iframe: true, done: true}
	if !g.sessionRefusedLocked("S-N") {
		t.Fatal("N session remained admitted under unknown-ancestry M")
	}
	e := reviewCommand(g, `{"id":2,"sessionId":"S-N","method":"Runtime.evaluate","params":{"expression":"1"}}`)
	if len(e.forward) > 0 {
		t.Errorf("evaluate forwarded for N under unknown-ancestry M: %s", e.forward)
	}
	raw, _ := json.Marshal(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S-root-N", "waitingForDebugger": false, "targetInfo": map[string]any{"targetId": "N", "type": "iframe", "url": "https://inner.test/n", "parentFrameId": "M"}}})
	v := g.fromBrowser(raw)
	if len(v.forward) > 0 || len(v.page) > 0 {
		t.Errorf("root attachedToTarget for N was delivered: forward=%s page=%s", v.forward, v.page)
	}
}

func iframeChainURL(i int) string {
	host := "a.test"
	if i%2 == 1 {
		host = "b.test"
	}
	return "https://" + host + "/" + itoa(int64(i))
}

func TestIframeHoldDeepUnknownAncestryStaysHidden(t *testing.T) {
	g := iframeHoldGuard(t)
	infos := []targetInfo{
		{TargetID: "SEVEN", Type: "page", URL: "https://seven.sliccy.ai/doc"},
		{TargetID: "F0", Type: "iframe", URL: iframeChainURL(0), ParentFrameID: "INPROC"},
	}
	prev := "F0"
	for i := 1; i <= 70; i++ {
		id := "F" + itoa(int64(i))
		infos = append(infos, targetInfo{TargetID: id, Type: "iframe", URL: iframeChainURL(i), ParentFrameID: prev})
		prev = id
	}
	g.observeAll(infos)
	bottom := "F70"
	info := targetInfo{TargetID: bottom, Type: "iframe", URL: iframeChainURL(70), ParentFrameID: "F69"}
	if !g.listingHiddenLocked(info) || g.targetReasonLocked(bottom) == "" {
		t.Fatalf("70-deep iframe under unknown ancestry was shown: reason=%q", g.targetReasonLocked(bottom))
	}
	g.browserSessions["S-browser"] = true
	g.sessions["S-bottom"] = &session{id: "S-bottom", target: bottom, parent: "S-browser", typ: "iframe"}
	g.navTargets[bottom] = &navTarget{target: bottom, iframe: true, done: true}
	if !g.sessionRefusedLocked("S-bottom") {
		t.Fatal("browser-session attach under deep unknown ancestry remained admitted")
	}
	e := reviewCommand(g, `{"id":2,"sessionId":"S-bottom","method":"Runtime.evaluate","params":{"expression":"1"}}`)
	if len(e.forward) > 0 {
		t.Errorf("evaluate forwarded under deep unknown ancestry: %s", e.forward)
	}
	raw, _ := json.Marshal(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "S-root-bottom", "waitingForDebugger": false, "targetInfo": map[string]any{"targetId": bottom, "type": "iframe", "url": iframeChainURL(70), "parentFrameId": "F69"}}})
	v := g.fromBrowser(raw)
	if len(v.forward) > 0 || len(v.page) > 0 {
		t.Errorf("browser-root attachedToTarget for deep unknown ancestry was delivered: forward=%s page=%s", v.forward, v.page)
	}
}

func TestIframeHoldParentFrameCycleStaysHidden(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "SEVEN", Type: "page", URL: "https://seven.sliccy.ai/doc"},
		{TargetID: "A", Type: "iframe", URL: "https://a.test/", ParentFrameID: "B"},
		{TargetID: "B", Type: "iframe", URL: "https://b.test/", ParentFrameID: "A"},
	})
	for _, id := range []string{"A", "B"} {
		url := "https://a.test/"
		parent := "B"
		if id == "B" {
			url = "https://b.test/"
			parent = "A"
		}
		info := targetInfo{TargetID: id, Type: "iframe", URL: url, ParentFrameID: parent}
		if !g.listingHiddenLocked(info) || g.targetReasonLocked(id) == "" {
			t.Fatalf("parentFrame cycle member %s was shown: reason=%q", id, g.targetReasonLocked(id))
		}
		g.sessions["S-"+id] = &session{id: "S-" + id, target: id, typ: "iframe"}
		g.navTargets[id] = &navTarget{target: id, iframe: true, done: true}
		if !g.sessionRefusedLocked("S-" + id) {
			t.Fatalf("cycle member %s session remained admitted", id)
		}
	}
}

func TestIframeHoldOrdinaryDeepWithinLimitShown(t *testing.T) {
	g := iframeHoldGuard(t)
	infos := []targetInfo{{TargetID: "PAGE", Type: "page", URL: "https://ordinary.test/"}}
	prev := "PAGE"
	for i := 0; i < 32; i++ {
		id := "F" + itoa(int64(i))
		infos = append(infos, targetInfo{TargetID: id, Type: "iframe", URL: iframeChainURL(i), ParentFrameID: prev})
		prev = id
	}
	g.observeAll(infos)
	bottom := "F31"
	info := targetInfo{TargetID: bottom, Type: "iframe", URL: iframeChainURL(31), ParentFrameID: "F30"}
	if g.listingHiddenLocked(info) || g.targetReasonLocked(bottom) != "" {
		t.Fatalf("ordinary deep chain within the limit was hidden: reason=%q", g.targetReasonLocked(bottom))
	}
}

func TestIframeHoldOrdinaryDeepBeyondLimitRefusesAncestry(t *testing.T) {
	g := iframeHoldGuard(t)
	infos := []targetInfo{{TargetID: "PAGE", Type: "page", URL: "https://ordinary.test/"}}
	prev := "PAGE"
	for i := 0; i <= maxFrameAncestors; i++ {
		id := "F" + itoa(int64(i))
		infos = append(infos, targetInfo{TargetID: id, Type: "iframe", URL: iframeChainURL(i), ParentFrameID: prev})
		prev = id
	}
	g.observeAll(infos)
	bottom := "F" + itoa(int64(maxFrameAncestors))
	reason := g.targetReasonLocked(bottom)
	if reason != refusedAncestry {
		t.Fatalf("deep ordinary chain reason=%q want %q protected=%v", reason, refusedAncestry, g.targets[bottom] != nil && g.targets[bottom].protected)
	}
	info := targetInfo{TargetID: bottom, Type: "iframe", URL: iframeChainURL(maxFrameAncestors), ParentFrameID: "F" + itoa(int64(maxFrameAncestors-1))}
	if !g.listingHiddenLocked(info) {
		t.Fatal("deep ordinary chain beyond the limit was listable")
	}
}

func TestIframeHoldChromeErrorFrameTreeOrdinaryOrMissingParent(t *testing.T) {
	for _, parent := range []string{"", "PAGE"} {
		name := parent
		if name == "" {
			name = "none"
		}
		t.Run(name, func(t *testing.T) {
			g, frame, nt := chromeErrorConfirmGuard(t, parent)
			_ = g.confirmedTargetLocked(nt, nt.confirm, 0, chromeErrorConfirmMessage("CHILD", parent, "https://ordinary.test/fail"))
			if frame.protected || nt.confirm.failed {
				t.Fatalf("chrome-error with ordinary/missing parent stayed blocked: protected=%v failed=%v parent=%q", frame.protected, nt.confirm.failed, g.frameParents["CHILD"])
			}
			if !nt.confirm.done {
				t.Fatal("confirmation did not settle")
			}
		})
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

func TestIframeHoldOrdinaryNestedAttachReachesOwner(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{{TargetID: "P", Type: "page", URL: "https://ordinary.test/"}})
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.noteFrameParentLocked("MID", "P")
	g.noteFrameParentLocked("INNER", "MID")
	g.noteChildFrameLocked("MID", "https://ordinary.test/mid")
	g.noteChildFrameLocked("INNER", "https://ordinary.test/inner")
	raw, _ := json.Marshal(map[string]any{"method": "Target.attachedToTarget", "sessionId": "S-P", "params": map[string]any{"sessionId": "S-X", "waitingForDebugger": false, "targetInfo": map[string]any{"targetId": "X", "type": "iframe", "url": "https://cross.test/embed", "parentFrameId": "INNER", "parentId": "P"}}})
	v := g.fromBrowser(raw)
	if len(v.page) == 0 && len(v.forward) == 0 {
		t.Fatalf("ordinary nested iframe was not delivered: forward=%s page=%s disputed=%v parent=%q", v.forward, v.page, g.ancestryDisputed["X"], g.frameParents["INNER"])
	}
	if g.ancestryDisputed["X"] || g.targets["X"].protected {
		t.Fatalf("ordinary nested iframe treated as disputed: disputed=%v protected=%v", g.ancestryDisputed["X"], g.targets["X"].protected)
	}
	if g.listingHiddenLocked(targetInfo{TargetID: "X", Type: "iframe", URL: "https://cross.test/embed", ParentFrameID: "INNER"}) || g.targetReasonLocked("X") != "" {
		t.Fatal("ordinary nested iframe was hidden or unattachable")
	}
}

func TestIframeHoldAttachChainMissesOwnerDisputed(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{{TargetID: "SEVEN", Type: "page", URL: "https://seven.sliccy.ai/doc"}, {TargetID: "P", Type: "page", URL: "https://ordinary.test/"}})
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.noteFrameParentLocked("INNER", "SEVEN")
	raw, _ := json.Marshal(map[string]any{"method": "Target.attachedToTarget", "sessionId": "S-P", "params": map[string]any{"sessionId": "S-X", "waitingForDebugger": false, "targetInfo": map[string]any{"targetId": "X", "type": "iframe", "url": "https://ordinary.test/embed", "parentFrameId": "INNER", "parentId": "P"}}})
	v := g.fromBrowser(raw)
	if len(v.forward) > 0 || len(v.page) > 0 {
		t.Errorf("disputed attach delivered: forward=%s page=%s parent=%q", v.forward, v.page, g.frameParents["INNER"])
	}
	if g.frameParents["INNER"] != "SEVEN" {
		t.Fatalf("recorded protected ancestry was rewritten: %q", g.frameParents["INNER"])
	}
	if !g.ancestryDisputed["X"] || !g.targets["X"].protected {
		t.Fatalf("chain that misses the owner was not sticky protected: disputed=%v protected=%v", g.ancestryDisputed["X"], g.targets["X"] != nil && g.targets["X"].protected)
	}
}

func TestIframeHoldFrameParentsPrunedOnDestroy(t *testing.T) {
	g := iframeHoldGuard(t)
	g.noteFrameParentLocked("MID", "PAGE")
	g.noteFrameParentLocked("INNER", "MID")
	g.noteFrameParentLocked("OTHER", "PAGE")
	g.destroyedLocked("MID")
	if _, ok := g.frameParents["MID"]; ok {
		t.Fatal("destroyed frame kept its own frameParents entry")
	}
	if _, ok := g.frameParents["INNER"]; ok {
		t.Fatal("child frameParents entry survived parent destroy")
	}
	if g.frameParents["OTHER"] != "PAGE" {
		t.Fatalf("unrelated frameParents entry changed: %q", g.frameParents["OTHER"])
	}
}

func TestIframeHoldFrameParentsCapFailsClosed(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["P"] = &target{id: "P", typ: "page", url: "https://ordinary.test/"}
	for i := 0; i < 4096; i++ {
		if !g.noteFrameParentLocked("F"+itoa(int64(i)), "P") {
			t.Fatalf("failed to fill frameParents before cap at %d", i)
		}
	}
	if g.noteFrameParentLocked("OVERFLOW", "P") || !g.frameParentsFull {
		t.Fatal("frameParents cap did not fail closed")
	}
	frame := &target{id: "X", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "OVERFLOW"}
	g.targets["X"] = frame
	if !g.iframeAncestryUnknownLocked(frame) || !g.listingHiddenLocked(targetInfo{TargetID: "X", Type: "iframe", URL: frame.url, ParentFrameID: "OVERFLOW"}) {
		t.Fatal("iframe was not concealed after frameParents cap")
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
