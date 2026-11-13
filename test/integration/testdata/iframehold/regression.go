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
			if !nt.confirm.failed {
				t.Fatalf("chrome-error with %s parent was confirmed: failed=%v parent=%q", parent, nt.confirm.failed, g.frameParents["CHILD"])
			}
			if parent == "HELD" {
				if frame.protected {
					t.Fatal("held parent sticky-protected chrome-error iframe")
				}
			} else if !frame.protected {
				t.Fatalf("chrome-error with %s parent was not protected: parent=%q", parent, g.frameParents["CHILD"])
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
		if _, ok := g.noteFrameParentLocked("F"+itoa(int64(i)), "P"); !ok {
			t.Fatalf("failed to fill frameParents before cap at %d", i)
		}
	}
	if _, ok := g.noteFrameParentLocked("OVERFLOW", "P"); ok || !g.frameParentsFull {
		t.Fatal("frameParents cap did not fail closed")
	}
	frame := &target{id: "X", typ: "iframe", url: "https://ordinary.test/embed", parentFrame: "OVERFLOW"}
	g.targets["X"] = frame
	if !g.iframeAncestryUnknownLocked(frame) || !g.listingHiddenLocked(targetInfo{TargetID: "X", Type: "iframe", URL: frame.url, ParentFrameID: "OVERFLOW"}) {
		t.Fatal("iframe was not concealed after frameParents cap")
	}
}

func TestIframeHoldLateParentUnderSevenRedecidesDescendants(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["SEVEN"] = &target{id: "SEVEN", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	g.browserSessions["S-browser"] = true
	x := &target{id: "X", typ: "iframe", url: "https://ordinary.test/x", parentFrame: "FRAME", shown: true}
	g.targets["X"] = x
	child := &target{id: "CHILD", typ: "iframe", url: "https://ordinary.test/child", parentFrame: "X", shown: true}
	g.targets["CHILD"] = child
	g.sessions["S-CHILD"] = &session{id: "S-CHILD", target: "CHILD", parent: "S-browser", typ: "iframe"}
	x.protected = g.decide(x, false)
	child.protected = g.decide(child, false)
	if x.protected || child.protected {
		t.Fatalf("unknown parent protected early: x=%t child=%t", x.protected, child.protected)
	}
	if !g.iframeAncestryUnknownLocked(x) {
		t.Fatal("X was not ancestry-unknown before the late parent")
	}
	_, ok := g.noteFrameParentLocked("FRAME", "SEVEN")
	if !ok {
		t.Fatal("late frameParents entry was not recorded")
	}
	if !x.protected || !child.protected {
		t.Fatalf("late parent under seven did not redecide: x=%t child=%t", x.protected, child.protected)
	}
	infoX := targetInfo{TargetID: "X", Type: "iframe", URL: x.url, ParentFrameID: "FRAME"}
	infoChild := targetInfo{TargetID: "CHILD", Type: "iframe", URL: child.url, ParentFrameID: "X"}
	if !g.listingHiddenLocked(infoX) || !g.listingHiddenLocked(infoChild) {
		t.Fatal("late-parent seven descendants remained listable")
	}
	if g.targetReasonLocked("X") == "" || g.targetReasonLocked("CHILD") == "" {
		t.Fatal("late-parent seven descendants remained attachable by id")
	}
	if !g.protectedIframeIDLocked("X") || !g.protectedIframeIDLocked("CHILD") {
		t.Fatal("late-parent seven descendants remained getTargetInfo-able")
	}
	if !g.sessionRefusedLocked("S-CHILD") {
		t.Fatal("browser-level session on a late-protected descendant was not refused")
	}
}

func TestIframeHoldLateParentUnderVaultRedecidesDescendants(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["VAULT"] = &target{id: "VAULT", typ: "page", url: "https://vault.sliccy.ai/doc", protected: true}
	x := &target{id: "X", typ: "iframe", url: "https://ordinary.test/x", parentFrame: "FRAME", shown: true}
	g.targets["X"] = x
	child := &target{id: "CHILD", typ: "iframe", url: "https://ordinary.test/child", parentFrame: "X", shown: true}
	g.targets["CHILD"] = child
	x.protected = g.decide(x, false)
	child.protected = g.decide(child, false)
	_, ok := g.noteFrameParentLocked("FRAME", "VAULT")
	if !ok {
		t.Fatal("late vault frameParents entry was not recorded")
	}
	if !x.protected || !child.protected {
		t.Fatalf("late parent under vault did not redecide: x=%t child=%t", x.protected, child.protected)
	}
	infoChild := targetInfo{TargetID: "CHILD", Type: "iframe", URL: child.url, ParentFrameID: "X"}
	if !g.listingHiddenLocked(infoChild) || g.targetReasonLocked("CHILD") == "" || !g.protectedIframeIDLocked("CHILD") {
		t.Fatal("late-parent vault descendants stayed visible or attachable")
	}
}

func TestIframeHoldOrdinaryLateParentStaysShown(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/doc"}
	x := &target{id: "X", typ: "iframe", url: "https://ordinary.test/x", parentFrame: "FRAME", shown: true}
	g.targets["X"] = x
	child := &target{id: "CHILD", typ: "iframe", url: "https://ordinary.test/child", parentFrame: "X", shown: true}
	g.targets["CHILD"] = child
	x.protected = g.decide(x, false)
	child.protected = g.decide(child, false)
	if !g.listingHiddenLocked(targetInfo{TargetID: "X", Type: "iframe", URL: x.url, ParentFrameID: "FRAME"}) {
		t.Fatal("unknown-parent iframe was listable before the late ordinary parent")
	}
	_, ok := g.noteFrameParentLocked("FRAME", "PAGE")
	if !ok {
		t.Fatal("late ordinary frameParents entry was not recorded")
	}
	if x.protected || child.protected {
		t.Fatalf("ordinary late parent protected iframes: x=%t child=%t", x.protected, child.protected)
	}
	infoX := targetInfo{TargetID: "X", Type: "iframe", URL: x.url, ParentFrameID: "FRAME"}
	infoChild := targetInfo{TargetID: "CHILD", Type: "iframe", URL: child.url, ParentFrameID: "X"}
	if g.listingHiddenLocked(infoX) || g.listingHiddenLocked(infoChild) {
		t.Fatal("ordinary late-parent descendants stayed hidden")
	}
	if g.targetReasonLocked("X") != "" || g.targetReasonLocked("CHILD") != "" {
		t.Fatal("ordinary late-parent descendants were refused by id")
	}
}

func TestIframeHoldLateParentCascadesWorkerUnderSeven(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["SEVEN"] = &target{id: "SEVEN", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	g.browserSessions["S-browser"] = true
	x := &target{id: "X", typ: "iframe", url: "https://ordinary.test/x", parentFrame: "FRAME", shown: true}
	g.targets["X"] = x
	w := &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", openerFrame: "X", shown: true}
	g.targets["W"] = w
	g.sessions["S-W"] = &session{id: "S-W", target: "W", parent: "S-browser", typ: "worker"}
	x.protected = g.decide(x, false)
	w.protected = g.decide(w, false)
	if x.protected || w.protected {
		t.Fatalf("precondition protected early: x=%t w=%t", x.protected, w.protected)
	}
	if g.listingHiddenLocked(targetInfo{TargetID: "W", Type: "worker", URL: w.url}) || g.targetReasonLocked("W") != "" {
		t.Fatal("ordinary worker under unknown-frame iframe was hidden or refused before the late parent")
	}
	_, ok := g.noteFrameParentLocked("FRAME", "SEVEN")
	if !ok {
		t.Fatal("late frameParents entry was not recorded")
	}
	if !x.protected {
		t.Fatal("late parent under seven did not protect the iframe")
	}
	if !w.protected {
		t.Fatal("late parent under seven did not cascade protection into the worker")
	}
	if !g.listingHiddenLocked(targetInfo{TargetID: "W", Type: "worker", URL: w.url}) || g.targetReasonLocked("W") == "" || !g.protectedWorkerIDLocked("W") {
		t.Fatal("cascaded worker stayed listable or attachable")
	}
	if !g.sessionRefusedLocked("S-W") {
		t.Fatal("existing worker session was not refused after cascade")
	}
}

func TestIframeHoldLateParentCascadesWorkerUnderVault(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["VAULT"] = &target{id: "VAULT", typ: "page", url: "https://vault.sliccy.ai/doc", protected: true}
	g.browserSessions["S-browser"] = true
	x := &target{id: "X", typ: "iframe", url: "https://ordinary.test/x", parentFrame: "FRAME", shown: true}
	g.targets["X"] = x
	w := &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", openerFrame: "X", shown: true}
	g.targets["W"] = w
	g.sessions["S-W"] = &session{id: "S-W", target: "W", parent: "S-browser", typ: "worker"}
	x.protected = g.decide(x, false)
	w.protected = g.decide(w, false)
	_, ok := g.noteFrameParentLocked("FRAME", "VAULT")
	if !ok {
		t.Fatal("late vault frameParents entry was not recorded")
	}
	if !x.protected || !w.protected {
		t.Fatalf("late parent under vault did not cascade: x=%t w=%t", x.protected, w.protected)
	}
	if !g.listingHiddenLocked(targetInfo{TargetID: "W", Type: "worker", URL: w.url}) || g.targetReasonLocked("W") == "" || !g.sessionRefusedLocked("S-W") {
		t.Fatal("vault-cascaded worker stayed visible or its session stayed open")
	}
}

func TestIframeHoldOrdinaryLateParentWorkerStaysDelivered(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/doc"}
	g.browserSessions["S-browser"] = true
	x := &target{id: "X", typ: "iframe", url: "https://ordinary.test/x", parentFrame: "FRAME", shown: true}
	g.targets["X"] = x
	w := &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", openerFrame: "X", shown: true}
	g.targets["W"] = w
	g.sessions["S-W"] = &session{id: "S-W", target: "W", parent: "S-browser", typ: "worker"}
	x.protected = g.decide(x, false)
	w.protected = g.decide(w, false)
	_, ok := g.noteFrameParentLocked("FRAME", "PAGE")
	if !ok {
		t.Fatal("late ordinary frameParents entry was not recorded")
	}
	if x.protected || w.protected {
		t.Fatalf("ordinary late parent protected targets: x=%t w=%t", x.protected, w.protected)
	}
	if g.listingHiddenLocked(targetInfo{TargetID: "W", Type: "worker", URL: w.url}) || g.targetReasonLocked("W") != "" || g.sessionRefusedLocked("S-W") {
		t.Fatal("worker under ordinary late parent was refused")
	}
}

func TestIframeHoldLateParentCascadesWorkerOfWorkerUnderSeven(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["SEVEN"] = &target{id: "SEVEN", typ: "page", url: "https://seven.sliccy.ai/doc", protected: true}
	g.browserSessions["S-browser"] = true
	x := &target{id: "X", typ: "iframe", url: "https://ordinary.test/x", parentFrame: "FRAME", shown: true}
	g.targets["X"] = x
	w1 := &target{id: "W1", typ: "worker", url: "https://ordinary.test/w1.js", openerFrame: "X", shown: true}
	g.targets["W1"] = w1
	w2 := &target{id: "W2", typ: "worker", url: "https://ordinary.test/w2.js", opener: "W1", shown: true}
	g.targets["W2"] = w2
	g.sessions["S-W1"] = &session{id: "S-W1", target: "W1", parent: "S-browser", typ: "worker"}
	g.sessions["S-W2"] = &session{id: "S-W2", target: "W2", parent: "S-browser", typ: "worker"}
	x.protected = g.decide(x, false)
	w1.protected = g.decide(w1, false)
	w2.protected = g.decide(w2, false)
	if x.protected || w1.protected || w2.protected {
		t.Fatalf("precondition protected early: x=%t w1=%t w2=%t", x.protected, w1.protected, w2.protected)
	}
	_, ok := g.noteFrameParentLocked("FRAME", "SEVEN")
	if !ok {
		t.Fatal("late frameParents entry was not recorded")
	}
	if !x.protected || !w1.protected || !w2.protected {
		t.Fatalf("late parent did not cascade to worker-of-worker: x=%t w1=%t w2=%t", x.protected, w1.protected, w2.protected)
	}
	if !g.listingHiddenLocked(targetInfo{TargetID: "W2", Type: "worker", URL: w2.url}) || g.targetReasonLocked("W2") == "" || !g.protectedWorkerIDLocked("W2") {
		t.Fatal("cascaded worker-of-worker stayed listable or attachable")
	}
	if !g.sessionRefusedLocked("S-W1") || !g.sessionRefusedLocked("S-W2") {
		t.Fatal("worker sessions were not refused after cascade")
	}
}

func TestIframeHoldLateParentCascadesWorkerOfWorkerUnderVault(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["VAULT"] = &target{id: "VAULT", typ: "page", url: "https://vault.sliccy.ai/doc", protected: true}
	g.browserSessions["S-browser"] = true
	x := &target{id: "X", typ: "iframe", url: "https://ordinary.test/x", parentFrame: "FRAME", shown: true}
	g.targets["X"] = x
	w1 := &target{id: "W1", typ: "worker", url: "https://ordinary.test/w1.js", openerFrame: "X", shown: true}
	g.targets["W1"] = w1
	w2 := &target{id: "W2", typ: "worker", url: "https://ordinary.test/w2.js", opener: "W1", shown: true}
	g.targets["W2"] = w2
	g.sessions["S-W2"] = &session{id: "S-W2", target: "W2", parent: "S-browser", typ: "worker"}
	x.protected = g.decide(x, false)
	w1.protected = g.decide(w1, false)
	w2.protected = g.decide(w2, false)
	_, ok := g.noteFrameParentLocked("FRAME", "VAULT")
	if !ok {
		t.Fatal("late vault frameParents entry was not recorded")
	}
	if !w2.protected || !g.listingHiddenLocked(targetInfo{TargetID: "W2", Type: "worker", URL: w2.url}) || g.targetReasonLocked("W2") == "" || !g.sessionRefusedLocked("S-W2") {
		t.Fatal("vault worker-of-worker stayed visible or its session stayed open")
	}
}

func TestIframeHoldOrdinaryLateParentWorkerOfWorkerStaysDelivered(t *testing.T) {
	g := iframeHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/doc"}
	g.browserSessions["S-browser"] = true
	x := &target{id: "X", typ: "iframe", url: "https://ordinary.test/x", parentFrame: "FRAME", shown: true}
	g.targets["X"] = x
	w1 := &target{id: "W1", typ: "worker", url: "https://ordinary.test/w1.js", openerFrame: "X", shown: true}
	g.targets["W1"] = w1
	w2 := &target{id: "W2", typ: "worker", url: "https://ordinary.test/w2.js", opener: "W1", shown: true}
	g.targets["W2"] = w2
	g.sessions["S-W2"] = &session{id: "S-W2", target: "W2", parent: "S-browser", typ: "worker"}
	x.protected = g.decide(x, false)
	w1.protected = g.decide(w1, false)
	w2.protected = g.decide(w2, false)
	_, ok := g.noteFrameParentLocked("FRAME", "PAGE")
	if !ok {
		t.Fatal("late ordinary frameParents entry was not recorded")
	}
	if w2.protected || g.listingHiddenLocked(targetInfo{TargetID: "W2", Type: "worker", URL: w2.url}) || g.targetReasonLocked("W2") != "" || g.sessionRefusedLocked("S-W2") {
		t.Fatal("worker-of-worker under ordinary late parent was refused")
	}
}

func TestIframeHoldFrameAttachedOnTrackedPageRedecides(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "PAGE", Type: "page", URL: "https://ordinary.test/doc"},
		{TargetID: "SEVEN", Type: "page", URL: "https://seven.sliccy.ai/doc"},
	})
	g.sessions["S-page"] = &session{id: "S-page", target: "PAGE", typ: "page"}
	x := &target{id: "X", typ: "iframe", url: "https://ordinary.test/x", parentFrame: "FRAME", shown: true}
	g.targets["X"] = x
	child := &target{id: "CHILD", typ: "iframe", url: "https://ordinary.test/child", parentFrame: "X", shown: true}
	g.targets["CHILD"] = child
	x.protected = g.decide(x, false)
	child.protected = g.decide(child, false)
	if x.protected || child.protected || g.targetReasonLocked("CHILD") != refusedAncestry {
		t.Fatalf("precondition: want unresolved ancestry, got x=%t child=%t reason=%q", x.protected, child.protected, g.targetReasonLocked("CHILD"))
	}
	raw, _ := json.Marshal(map[string]any{
		"sessionId": "S-page",
		"method":    "Page.frameAttached",
		"params":    map[string]any{"frameId": "FRAME", "parentFrameId": "SEVEN"},
	})
	_ = g.fromBrowser(raw)
	if g.frameParents["FRAME"] != "SEVEN" {
		t.Fatalf("tracked page frameAttached did not record parent: %q", g.frameParents["FRAME"])
	}
	if !x.protected || !child.protected {
		t.Fatalf("tracked page frameAttached did not redecide: x=%t child=%t", x.protected, child.protected)
	}
	if g.targetReasonLocked("CHILD") != refusedTarget {
		t.Fatalf("after tracked frameAttached reason=%q want %q", g.targetReasonLocked("CHILD"), refusedTarget)
	}
	if g.listingHiddenLocked(targetInfo{TargetID: "CHILD", Type: "iframe", URL: child.url, ParentFrameID: "X"}) == false {
		t.Fatal("after tracked frameAttached CHILD was listable")
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

func dualAttachInfo(id, parent string, url string) map[string]any {
	info := map[string]any{"targetId": id, "type": "iframe", "url": url}
	if parent != "" {
		info["parentFrameId"] = parent
		info["parentId"] = parent
	}
	return info
}

func dualAttach(g *guard, parentSession, session string, waiting bool, info map[string]any) browserVerdict {
	raw, _ := json.Marshal(map[string]any{
		"method":    "Target.attachedToTarget",
		"sessionId": parentSession,
		"params":    map[string]any{"sessionId": session, "waitingForDebugger": waiting, "targetInfo": info},
	})
	return g.fromBrowser(raw)
}

func TestIframeHoldTabOwnerEmptyParentThenPage(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "TAB", Type: "tab", URL: "https://ordinary.test/", BrowserContextID: "C1"},
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/", BrowserContextID: "C1"},
	})
	g.sessions["S-tab"] = &session{id: "S-tab", target: "TAB", typ: "tab"}
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	_ = dualAttach(g, "S-tab", "S-X-tab", false, dualAttachInfo("X", "", "https://frame.test/embed"))
	v := dualAttach(g, "S-P", "S-X-page", true, dualAttachInfo("X", "P", "https://frame.test/embed"))
	if g.ancestryDisputed["X"] || g.targets["X"].protected {
		t.Fatalf("tab then page disputed: disputed=%v protected=%v parents=%v", g.ancestryDisputed["X"], g.targets["X"].protected, g.frameParents)
	}
	if len(v.forward) == 0 && len(v.page) == 0 {
		t.Fatalf("page attach not delivered: forward=%s page=%s", v.forward, v.page)
	}
}

func TestIframeHoldPageThenTabOwnerEmptyParent(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "TAB", Type: "tab", URL: "https://ordinary.test/", BrowserContextID: "C1"},
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/", BrowserContextID: "C1"},
	})
	g.sessions["S-tab"] = &session{id: "S-tab", target: "TAB", typ: "tab"}
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	_ = dualAttach(g, "S-P", "S-X-page", true, dualAttachInfo("X", "P", "https://frame.test/embed"))
	_ = dualAttach(g, "S-tab", "S-X-tab", false, dualAttachInfo("X", "", "https://frame.test/embed"))
	if g.ancestryDisputed["X"] || g.targets["X"].protected {
		t.Fatalf("page then tab disputed: disputed=%v protected=%v parents=%v", g.ancestryDisputed["X"], g.targets["X"].protected, g.frameParents)
	}
}

func TestIframeHoldTabOwnerNestedChainReachesPage(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "TAB", Type: "tab", URL: "https://ordinary.test/", BrowserContextID: "C1"},
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/", BrowserContextID: "C1"},
	})
	g.sessions["S-tab"] = &session{id: "S-tab", target: "TAB", typ: "tab"}
	g.noteFrameParentLocked("INNER", "P")
	v := dualAttach(g, "S-tab", "S-X", false, dualAttachInfo("X", "INNER", "https://frame.test/embed"))
	if g.ancestryDisputed["X"] || g.targets["X"].protected {
		t.Fatalf("tab nested disputed: disputed=%v protected=%v", g.ancestryDisputed["X"], g.targets["X"].protected)
	}
	if len(v.forward) == 0 && len(v.page) == 0 {
		t.Fatal("tab nested attach not delivered")
	}
}

func TestIframeHoldBrowserOwnerEmptyParentThenPage(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "BROWSER", Type: "browser", URL: ""},
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/"},
	})
	g.sessions["S-browser"] = &session{id: "S-browser", target: "BROWSER", typ: "browser"}
	g.browserSessions["S-browser"] = true
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	_ = dualAttach(g, "S-browser", "S-X-b", false, dualAttachInfo("X", "", "https://frame.test/embed"))
	_ = dualAttach(g, "S-P", "S-X-page", true, dualAttachInfo("X", "P", "https://frame.test/embed"))
	if g.ancestryDisputed["X"] || g.targets["X"].protected {
		t.Fatalf("browser then page disputed: disputed=%v protected=%v parents=%v", g.ancestryDisputed["X"], g.targets["X"].protected, g.frameParents)
	}
}

func TestIframeHoldBrowserOwnerNestedChainNoDispute(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "BROWSER", Type: "browser", URL: ""},
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/"},
	})
	g.sessions["S-browser"] = &session{id: "S-browser", target: "BROWSER", typ: "browser"}
	g.browserSessions["S-browser"] = true
	g.noteFrameParentLocked("INNER", "P")
	v := dualAttach(g, "S-browser", "S-X", false, dualAttachInfo("X", "INNER", "https://frame.test/embed"))
	if g.ancestryDisputed["X"] || g.targets["X"].protected {
		t.Fatalf("browser nested disputed: disputed=%v protected=%v", g.ancestryDisputed["X"], g.targets["X"].protected)
	}
	if len(v.forward) == 0 && len(v.page) == 0 {
		t.Fatal("browser nested attach not delivered")
	}
}

func TestIframeHoldUnknownOwnerTypeStillDisputes(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "OTHER", Type: "other", URL: "https://ordinary.test/"},
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/"},
	})
	g.sessions["S-other"] = &session{id: "S-other", target: "OTHER", typ: "other"}
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	_ = dualAttach(g, "S-P", "S-X-page", true, dualAttachInfo("X", "P", "https://frame.test/embed"))
	_ = dualAttach(g, "S-other", "S-X-other", false, dualAttachInfo("X", "", "https://frame.test/embed"))
	if !g.ancestryDisputed["X"] || !g.targets["X"].protected {
		t.Fatalf("unknown owner type did not dispute: disputed=%v protected=%v", g.ancestryDisputed["X"], g.targets["X"] != nil && g.targets["X"].protected)
	}
}

func TestIframeHoldUnresolvedTabOwnerFailsClosed(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "TAB", Type: "tab", URL: "https://ordinary.test/", BrowserContextID: "C-tab"},
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/", BrowserContextID: "C-page"},
	})
	g.sessions["S-tab"] = &session{id: "S-tab", target: "TAB", typ: "tab"}
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	_ = dualAttach(g, "S-P", "S-X-page", true, dualAttachInfo("X", "P", "https://frame.test/embed"))
	_ = dualAttach(g, "S-tab", "S-X-tab", false, dualAttachInfo("X", "", "https://frame.test/embed"))
	if !g.ancestryDisputed["X"] || !g.targets["X"].protected {
		t.Fatalf("unresolved tab owner did not fail closed: disputed=%v protected=%v", g.ancestryDisputed["X"], g.targets["X"] != nil && g.targets["X"].protected)
	}
}

func TestIframeHoldAmbiguousTabPagesFailClosed(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "TAB", Type: "tab", URL: "https://ordinary.test/", BrowserContextID: "C1"},
		{TargetID: "P1", Type: "page", URL: "https://ordinary.test/a", BrowserContextID: "C1"},
		{TargetID: "P2", Type: "page", URL: "https://ordinary.test/b", BrowserContextID: "C1"},
	})
	g.sessions["S-tab"] = &session{id: "S-tab", target: "TAB", typ: "tab"}
	g.noteFrameParentLocked("INNER", "P1")
	_ = dualAttach(g, "S-tab", "S-X", false, dualAttachInfo("X", "INNER", "https://frame.test/embed"))
	if !g.ancestryDisputed["X"] || !g.targets["X"].protected {
		t.Fatalf("ambiguous tab pages did not fail closed: disputed=%v protected=%v", g.ancestryDisputed["X"], g.targets["X"] != nil && g.targets["X"].protected)
	}
}

func TestIframeHoldDifferentPageOwnerStillDisputes(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/"},
		{TargetID: "OTHER", Type: "page", URL: "https://ordinary.test/other"},
	})
	g.sessions["S-other"] = &session{id: "S-other", target: "OTHER", typ: "page"}
	g.noteFrameParentLocked("INNER", "P")
	_ = dualAttach(g, "S-other", "S-X", false, dualAttachInfo("X", "INNER", "https://frame.test/embed"))
	if !g.ancestryDisputed["X"] || !g.targets["X"].protected {
		t.Fatalf("different page owner did not dispute: disputed=%v protected=%v", g.ancestryDisputed["X"], g.targets["X"] != nil && g.targets["X"].protected)
	}
}

func TestIframeHoldTabSessionFollowMatchesResolvedPage(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "TAB", Type: "tab", URL: "https://ordinary.test/", BrowserContextID: "C1"},
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/", BrowserContextID: "C1"},
	})
	g.frames["P"] = map[string]bool{"X": true}
	frame := &target{id: "X", typ: "iframe", url: "https://frame.test/embed", parentFrame: "P", shown: true, ctx: "C1"}
	g.targets["X"] = frame
	g.sessions["S-X"] = &session{id: "S-X", target: "X", parent: "S-tab", typ: "iframe"}
	g.sessions["S-tab"] = &session{id: "S-tab", target: "TAB", typ: "tab"}
	if g.iframeFollowRefusedLocked("S-X") {
		t.Fatal("tab-attached iframe was refused against its resolved page chain")
	}
}

func TestIframeHoldUnconfirmedPageHoldsNotProtects(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "TAB", Type: "tab", URL: "https://ordinary.test/framed", BrowserContextID: "C1"},
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/framed", BrowserContextID: "C1"},
	})
	g.sessions["S-tab"] = &session{id: "S-tab", target: "TAB", typ: "tab"}
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.sessions["S-P2"] = &session{id: "S-P2", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	g.beginConfirmLocked(g.navTargets["P"])
	if !g.targetUnconfirmedLocked("P") {
		t.Fatal("page was not unconfirmed")
	}
	_ = dualAttach(g, "S-tab", "S-F-tab", false, dualAttachInfo("F", "", ""))
	v := dualAttach(g, "S-P", "S-F", true, dualAttachInfo("F", "P", ""))
	frame := g.targets["F"]
	if frame == nil || frame.protected || g.ancestryDisputed["F"] {
		t.Fatalf("unconfirmed page sticky-protected iframe: protected=%v disputed=%v", frame != nil && frame.protected, g.ancestryDisputed["F"])
	}
	if !g.iframeUnresolvedLocked(frame) || g.held["S-F"] == nil {
		t.Fatalf("unconfirmed page did not hold iframe: unresolved=%v held=%v", frame != nil && g.iframeUnresolvedLocked(frame), g.held["S-F"] != nil)
	}
	if len(v.page)+len(v.forward) != 0 {
		t.Fatalf("held iframe was delivered early: forward=%s page=%s", v.forward, v.page)
	}
}

func TestIframeHoldUnconfirmedPageThenOrdinaryDelivers(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{{TargetID: "P", Type: "page", URL: "https://ordinary.test/framed"}})
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.sessions["S-P2"] = &session{id: "S-P2", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	g.beginConfirmLocked(g.navTargets["P"])
	_ = dualAttach(g, "S-P2", "S-F-priv", false, dualAttachInfo("F", "P", ""))
	_ = dualAttach(g, "S-P", "S-F", true, dualAttachInfo("F", "P", ""))
	if g.targets["F"].protected || g.held["S-F"] == nil || g.held["S-F-priv"] == nil {
		t.Fatal("expected held unprotected iframe under both parents while page unconfirmed")
	}
	g.browserCommitIframeURL(t, "F", "P", "https://frame.test/embed")
	if g.targets["F"].protected {
		t.Fatal("URL commit under unconfirmed page sticky-protected iframe")
	}
	if g.held["S-F"] == nil || g.held["S-F-priv"] == nil {
		t.Fatal("URL commit released iframe while page still unconfirmed")
	}
	g.navTargets["P"].confirm.settle(false)
	settled := g.settleHeldWorkersLocked()
	if g.targets["F"].protected {
		t.Fatal("ordinary confirm sticky-protected iframe")
	}
	if g.held["S-F"] != nil || g.held["S-F-priv"] != nil || len(settled.page) == 0 {
		t.Fatalf("ordinary confirm did not deliver held iframe: held=%v/%v page=%d", g.held["S-F"] != nil, g.held["S-F-priv"] != nil, len(settled.page))
	}
	got := map[string]bool{}
	for _, raw := range settled.page {
		var m struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			Params    struct {
				Waiting bool   `json:"waitingForDebugger"`
				Session string `json:"sessionId"`
			} `json:"params"`
		}
		_ = json.Unmarshal(raw, &m)
		if m.Method != "Target.attachedToTarget" {
			continue
		}
		if m.Params.Waiting {
			t.Fatalf("delivered attach kept waitingForDebugger true session=%s child=%s", m.SessionID, m.Params.Session)
		}
		got[m.Params.Session] = true
	}
	if !got["S-F"] || !got["S-F-priv"] {
		t.Fatalf("both parents were not delivered once: %v", got)
	}
}

func TestIframeHoldUnconfirmedPageThenSevenProtects(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{{TargetID: "P", Type: "page", URL: "https://ordinary.test/framed"}})
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	g.beginConfirmLocked(g.navTargets["P"])
	_ = dualAttach(g, "S-P", "S-F", true, dualAttachInfo("F", "P", ""))
	g.browserCommitIframeURL(t, "F", "P", "https://frame.test/embed")
	if g.targets["F"].protected || g.held["S-F"] == nil {
		t.Fatal("expected held iframe before owner turned protected")
	}
	delete(g.navTargets, "P")
	_ = g.observeLocked(targetInfo{TargetID: "P", Type: "page", URL: "https://seven.sliccy.ai/doc"})
	settled := g.settleHeldWorkersLocked()
	if !g.targets["P"].protected {
		t.Fatal("page was not protected after navigating to seven")
	}
	if !g.targets["F"].protected {
		t.Fatal("iframe was not protected when owner confirmed onto seven")
	}
	if g.held["S-F"] != nil {
		t.Fatal("held iframe session was not detached when owner turned protected")
	}
	if len(settled.page) != 0 {
		t.Fatalf("protected iframe was delivered: %s", settled.page)
	}
}

func (g *guard) browserCommitIframeURL(t *testing.T, id, parent, url string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"method": "Target.targetInfoChanged", "sessionId": "RELAY",
		"params": map[string]any{"targetInfo": map[string]any{"targetId": id, "type": "iframe", "url": url, "parentId": parent, "parentFrameId": parent}},
	})
	_ = g.fromBrowser(raw)
}

func TestIframeHoldConfirmPendingParentDoesNotStickyProtect(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "P", Type: "page", URL: "https://ordinary.test/framed"},
		{TargetID: "F", Type: "iframe", URL: "https://frame.test/embed", ParentFrameID: "P"},
	})
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	g.beginConfirmLocked(g.navTargets["P"])
	send, blocked := g.confirmIframeParentLocked(g.targets["F"], "P")
	if !blocked {
		t.Fatal("confirm with pending parent was not blocked")
	}
	if g.targets["F"].protected {
		t.Fatalf("pending parent sticky-protected iframe during confirm; send=%d", len(send))
	}
}

func TestIframeHoldURLReleaseAttachesWithoutSynthesizedInfoChanged(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{{TargetID: "P", Type: "page", URL: "https://ordinary.test/framed"}})
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	_ = dualAttach(g, "S-P", "S-F", true, dualAttachInfo("F", "P", ""))
	if g.held["S-F"] == nil {
		t.Fatal("URL-less iframe was not held")
	}
	raw, _ := json.Marshal(map[string]any{
		"method": "Target.targetInfoChanged",
		"params": map[string]any{"targetInfo": map[string]any{"targetId": "F", "type": "iframe", "url": "https://frame.test/embed", "parentId": "P", "parentFrameId": "P"}},
	})
	v := g.fromBrowser(raw)
	var methods []string
	for _, page := range v.page {
		var m struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(page, &m)
		if m.SessionID == "S-P" {
			methods = append(methods, m.Method)
		}
	}
	if len(methods) != 1 || methods[0] != "Target.attachedToTarget" {
		t.Fatalf("held iframe release order=%v, want attachedToTarget only (Chrome's later targetInfoChanged is separate)", methods)
	}
	if g.held["S-F"] != nil {
		t.Fatal("held iframe was not delivered")
	}
}

func heldOwnerSettleGuard(t *testing.T) *guard {
	t.Helper()
	g := iframeHoldGuard(t)
	g.relaySession = "RELAY"
	g.observeAll([]targetInfo{{TargetID: "P", Type: "page", URL: "https://ordinary.test/framed"}})
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", session: "GUARD", ready: make(chan struct{}), done: true}
	_ = dualAttach(g, "S-P", "S-F", true, dualAttachInfo("F", "P", ""))
	if g.targets["F"].protected || g.held["S-F"] == nil {
		t.Fatal("expected held unprotected iframe before owner settle")
	}
	return g
}

func assertHeldHiddenAfterOwnerProtect(t *testing.T, g *guard, label string) {
	t.Helper()
	if !g.targets["P"].protected {
		t.Fatalf("%s: page was not protected", label)
	}
	if !g.targets["F"].protected {
		t.Fatalf("%s: iframe was not protected reason=%q", label, g.targetReasonLocked("F"))
	}
	if g.held["S-F"] != nil {
		t.Fatalf("%s: held iframe session remained", label)
	}
}

func TestIframeHoldOwnerFrameNavigatedSevenHidesHeld(t *testing.T) {
	for _, host := range []string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"} {
		g := heldOwnerSettleGuard(t)
		raw, _ := json.Marshal(map[string]any{
			"method": "Page.frameNavigated", "sessionId": "S-P",
			"params": map[string]any{"type": "Navigation", "frame": map[string]any{
				"id": "P", "url": host + "/doc", "securityOrigin": host,
			}},
		})
		_ = g.fromBrowser(raw)
		assertHeldHiddenAfterOwnerProtect(t, g, "frameNavigated "+host)
	}
}

func TestIframeHoldOwnerConfirmReplySevenHidesHeld(t *testing.T) {
	for _, host := range []string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"} {
		g := heldOwnerSettleGuard(t)
		g.beginConfirmLocked(g.navTargets["P"])
		g.browserCommitIframeURL(t, "F", "P", "https://frame.test/embed")
		if g.held["S-F"] == nil {
			t.Fatal("iframe released before confirm settled")
		}
		result, _ := json.Marshal(map[string]any{"frameTree": map[string]any{"frame": map[string]any{
			"id": "P", "url": host + "/doc", "securityOrigin": host,
		}}})
		_ = g.confirmedTargetLocked(g.navTargets["P"], g.navTargets["P"].confirm, 0, message{Result: result})
		_ = g.settleHeldWorkersLocked()
		assertHeldHiddenAfterOwnerProtect(t, g, "confirm reply "+host)
	}
}

func TestIframeHoldOwnerURLChangedSevenHidesHeld(t *testing.T) {
	for _, host := range []string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"} {
		g := heldOwnerSettleGuard(t)
		delete(g.navTargets, "P")
		raw, _ := json.Marshal(map[string]any{
			"method": "Target.targetInfoChanged", "sessionId": "RELAY",
			"params": map[string]any{"targetInfo": map[string]any{"targetId": "P", "type": "page", "url": host + "/doc"}},
		})
		_ = g.fromBrowser(raw)
		assertHeldHiddenAfterOwnerProtect(t, g, "targetInfoChanged "+host)
	}
}

func TestIframeHoldContentScriptWorldDoesNotProtectSibling(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{{TargetID: "P", Type: "page", URL: "https://ordinary.test/framed"}})
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	g.navTargets["P"] = &navTarget{target: "P", ready: make(chan struct{}), done: true}
	ext := "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob"
	raw, _ := json.Marshal(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": "S-P",
		"params": map[string]any{"context": map[string]any{
			"id": 2, "origin": ext, "name": "", "uniqueId": "ext-world",
			"auxData": map[string]any{"isDefault": false, "type": "default", "frameId": "P"},
		}},
	})
	_ = g.fromBrowser(raw)
	if g.childFrames["P"] == ext {
		t.Fatalf("content-script world origin was recorded as the page child frame: %q", g.childFrames["P"])
	}
	if g.creatorProtectedLocked("P") {
		t.Fatal("ordinary page was creator-protected after a content-script world")
	}
	v := dualAttach(g, "S-P", "S-F", true, dualAttachInfo("F", "P", "https://frame.test/embed"))
	if g.targets["F"].protected {
		t.Fatalf("sibling iframe was protected by content-script world: reason=%q", g.targetReasonLocked("F"))
	}
	if len(v.forward)+len(v.page) == 0 && g.held["S-F"] != nil {
		t.Fatal("ordinary iframe was held after content-script world on its page")
	}
}

func TestIframeHoldPollutedExtensionChildFrameDoesNotProtectOrdinaryPage(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{{TargetID: "P", Type: "page", URL: "https://ordinary.test/framed"}})
	g.childFrames["P"] = "chrome-extension://ankmbodmgimjemnojmeddfcdlbocolob"
	if g.creatorProtectedLocked("P") || g.creatorProtectRuleLocked("P") != "none" {
		t.Fatalf("polluted extension childFrame still protected the page: rule=%q", g.creatorProtectRuleLocked("P"))
	}
	frame := &target{id: "F", typ: "iframe", url: "https://frame.test/embed", parentFrame: "P"}
	g.targets["F"] = frame
	frame.protected = g.decide(frame, false)
	if frame.protected {
		t.Fatalf("ordinary iframe under polluted page was protected: reason=%q", g.targetReasonLocked("F"))
	}
}

func TestIframeHoldDefaultProtectedContextStillLocksPage(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{{TargetID: "P", Type: "page", URL: "https://ordinary.test/framed"}})
	g.sessions["S-P"] = &session{id: "S-P", target: "P", typ: "page"}
	raw, _ := json.Marshal(map[string]any{
		"method": "Runtime.executionContextCreated", "sessionId": "S-P",
		"params": map[string]any{"context": map[string]any{
			"id": 1, "origin": "https://seven.sliccy.ai", "name": "", "uniqueId": "main",
			"auxData": map[string]any{"isDefault": true, "type": "default", "frameId": "P"},
		}},
	})
	v := g.fromBrowser(raw)
	if g.childFrames["P"] != "https://seven.sliccy.ai" {
		t.Fatalf("default protected context was not recorded: %q", g.childFrames["P"])
	}
	if !g.targets["P"].protected {
		t.Fatalf("default protected context did not lock the page; send=%d", len(v.send))
	}
}

func TestIframeHoldConfirmSettledSevenParentProtects(t *testing.T) {
	g := iframeHoldGuard(t)
	g.observeAll([]targetInfo{
		{TargetID: "P", Type: "page", URL: "https://seven.sliccy.ai/doc"},
		{TargetID: "F", Type: "iframe", URL: "https://frame.test/embed", ParentFrameID: "P"},
	})
	g.targets["P"].protected = true
	send, blocked := g.confirmIframeParentLocked(g.targets["F"], "P")
	if !blocked || !g.targets["F"].protected {
		t.Fatalf("settled seven parent did not protect: blocked=%v protected=%v send=%d", blocked, g.targets["F"].protected, len(send))
	}
}
