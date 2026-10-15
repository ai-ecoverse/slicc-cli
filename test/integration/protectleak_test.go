package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCDPRefusesAnAnswerToAPauseThePageAlreadySettled(t *testing.T) {
	e := setupNav(t)
	var r struct {
		First cdpOutcome `json:"first"`
		Text  string     `json:"text"`
		Again cdpOutcome `json:"again"`
	}
	(&protectEnv{linkEnv: e.linkEnv}).evalJSON(t, "linkTest.protect.continuedTwice("+jsArgs("chrome", e.allowed)+")", &r)
	if !r.First.OK || r.Text != "real-twice" {
		t.Fatalf("the page couldn't continue its own paused request: %+v", r)
	}
	if r.Again.OK || !strings.HasPrefix(r.Again.Error.Message, "slicc: refused: slicc didn't pass this paused request to the page") {
		t.Errorf("answering the settled pause again: %+v, want slicc's refusal", r.Again)
	}
}

func TestCDPKeepsProtectedURLsOutOfEventsAndResults(t *testing.T) {
	e := setupNav(t)
	shown := func(to string) (events []string, tree bool) {
		var r struct {
			TargetID string          `json:"targetId"`
			Events   []string        `json:"events"`
			Tree     json.RawMessage `json:"tree"`
		}
		(&protectEnv{linkEnv: e.linkEnv}).evalJSON(t, "linkTest.protect.urlsShown("+jsArgs("chrome", e.allowed+"/doc?n=shown-start", to)+")", &r)
		e.closeTarget(r.TargetID)
		return r.Events, string(r.Tree) == "true"
	}
	if events, tree := shown(e.allowed + "/doc?n=" + e.nonce("shown-control")); len(events) == 0 || !tree {
		t.Fatalf("the control: an ordinary URL should show in events and the frame tree: %v %v", events, tree)
	}
	for _, base := range []string{e.seven, e.vault} {
		if events, tree := shown(base + "/doc?n=" + e.nonce("shown")); len(events) > 0 || tree {
			t.Errorf("%s: the protected URL reached the page in %v (frame tree: %v)", base, events, tree)
		}
	}
}
