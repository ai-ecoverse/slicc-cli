package cdp

import (
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

func workerHoldGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return newGuard(o, nil)
}

func TestWorkerHoldNullOriginDoesNotOverwriteFrame(t *testing.T) {
	g := workerHoldGuard(t)
	g.noteChildFrameLocked("FRAME", "https://ordinary.test/")
	g.noteChildFrameLocked("FRAME", "null")
	if got := g.childFrames["FRAME"]; got != "https://ordinary.test/" {
		t.Fatalf("sandboxed null origin overwrote the frame map: %q", got)
	}
}

func TestWorkerHoldPrunesGoneFrames(t *testing.T) {
	g := workerHoldGuard(t)
	g.noteChildFrameLocked("FRAME", "https://ordinary.test/")
	g.forgetChildFrameLocked("FRAME")
	if _, ok := g.childFrames["FRAME"]; ok {
		t.Fatal("gone frame stayed in the origin map")
	}
}

func TestWorkerHoldDataWorkerFromOrdinarySubFrame(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["PAGE"] = &target{id: "PAGE", typ: "page", url: "https://ordinary.test/"}
	g.noteChildFrameLocked("FRAME", "https://ordinary.test/embed")
	w := &target{id: "W", typ: "worker", url: "data:text/javascript,1", parentFrame: "FRAME"}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if w.protected {
		t.Fatal("data: worker from an ordinary sub-frame was protected")
	}
	if g.workerFromProtectedCreatorLocked(w) {
		t.Fatal("ordinary sub-frame still counted as a protected creator for a data: worker")
	}
}

func TestWorkerHoldDataWorkerFromProtectedSubFrame(t *testing.T) {
	g := workerHoldGuard(t)
	g.noteChildFrameLocked("FRAME", "https://seven.sliccy.ai/embed")
	w := &target{id: "W", typ: "worker", url: "data:text/javascript,1", parentFrame: "FRAME"}
	g.targets["W"] = w
	w.protected = g.decide(w, false)
	if !w.protected {
		t.Fatal("data: worker from a protected sub-frame was not protected")
	}
}

func TestWorkerHoldAttachOrdinaryUnshownByID(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["W"] = &target{id: "W", typ: "worker", url: "https://ordinary.test/worker.js", shown: false}
	if g.targetReasonLocked("W") != "" {
		t.Fatalf("ordinary unshown worker refused by id: %q", g.targetReasonLocked("W"))
	}
	if g.workerConcealedLocked("W") || g.protectedWorkerIDLocked("W") {
		t.Fatal("ordinary worker was concealed")
	}
	send, hide := g.applyResultKindLocked(kindAttach, "W", "", map[string]any{"sessionId": "S-W"})
	if hide {
		t.Fatalf("attach of ordinary unshown worker was hidden: send=%d", len(send))
	}
	if !g.targets["W"].shown {
		t.Fatal("attach by id did not mark the ordinary worker shown")
	}
}

func TestWorkerHoldAttachUnresolvedStillRefused(t *testing.T) {
	g := workerHoldGuard(t)
	g.targets["W"] = &target{id: "W", typ: "worker", url: "", parentFrame: "FRAME-unknown", shown: false}
	if reason := g.targetReasonLocked("W"); reason == "" {
		t.Fatal("unresolved worker was attachable by id")
	}
	_, hide := g.applyResultKindLocked(kindAttach, "W", "", map[string]any{"sessionId": "S-W"})
	if !hide {
		t.Fatal("attach of an unresolved worker was admitted")
	}
	if strings.Contains(g.targets["W"].url, "ordinary") {
		t.Fatal("unexpected")
	}
}
