package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func worldParkScript(seven, allowed string) fakeScript {
	top := map[string]any{"targetId": "P-top", "type": "page", "url": allowed + "/nest"}
	two := map[string]any{"targetId": "P-two", "type": "page", "url": allowed + "/two"}
	bodies := map[string]map[string]any{"P-top": top, "P-two": two}
	frame := func(id, parent, url, origin string, children ...any) map[string]any {
		f := map[string]any{"id": id, "loaderId": "L-" + id, "url": url, "securityOrigin": origin, "mimeType": "text/html"}
		if parent != "" {
			f["parentId"] = parent
		}
		return map[string]any{"frame": f, "childFrames": children}
	}
	tree := frame("P-top", "", allowed+"/nest", allowed,
		frame("F-same", "P-top", allowed+"/inner", allowed),
		frame("F-seven", "P-top", seven+"/frame", seven,
			frame("F-under", "F-seven", "about:blank", seven)))
	armed := false
	attaches := 0
	worlds := 0
	return func(method string, params json.RawMessage) (string, []string) {
		var p struct {
			Name     string `json:"name"`
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(params, &p)
		switch method {
		case "Browser.getHistogram":
			if p.Name != "arm-world-park" || armed {
				return "{}", nil
			}
			armed = true
			return "{}", []string{
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": top}),
				localNestEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-top", "waitingForDebugger": false, "targetInfo": top}),
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": two}),
				localNestEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-two", "waitingForDebugger": false, "targetInfo": two}),
			}
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": []any{top, two}})
			return string(raw), nil
		case "Target.getTargetInfo":
			if body, ok := bodies[p.TargetID]; ok {
				raw, _ := json.Marshal(map[string]any{"targetInfo": body})
				return string(raw), nil
			}
		case "Target.attachToTarget":
			attaches++
			return fmt.Sprintf(`{"sessionId":"S-w%d"}`, attaches), nil
		case "Page.getFrameTree":
			raw, _ := json.Marshal(map[string]any{"frameTree": tree})
			return string(raw), nil
		case "Page.createIsolatedWorld":
			worlds++
			return fmt.Sprintf(`{"executionContextId":%d}`, 100+worlds), nil
		case "Runtime.evaluate":
			return `{"result":{"type":"number","value":1}}`, nil
		}
		return "{}", nil
	}
}

type worldParkAnswer struct {
	cdpOutcome
	MS int `json:"ms"`
}

func TestCDPParkedIsolatedWorldDoesNotStallOtherCommands(t *testing.T) {
	hold := 4 * time.Second
	env, _ := setupFakeProtectEnvOn(t, launchBrowser(t), []string{"SLICC_CDP_HOLD_TIMEOUT=" + hold.String()}, worldParkScript)
	var r struct {
		Known    worldParkAnswer `json:"known"`
		Under    worldParkAnswer `json:"under"`
		Begin    int             `json:"begin"`
		UnknownA worldParkAnswer `json:"unknownA"`
		UnknownB worldParkAnswer `json:"unknownB"`
		Later    worldParkAnswer `json:"later"`
		Other    worldParkAnswer `json:"other"`
		Order    []string        `json:"order"`
	}
	env.evalJSONWithin(t, "linkTest.protect.worldPark("+jsArgs("chrome-2")+")", &r, 120*time.Second)
	raw, _ := json.Marshal(r)
	t.Logf("world park: %s", raw)
	fast := 2000
	holdMS := int(hold.Milliseconds())
	refused := func(o worldParkAnswer) bool {
		return !o.OK && o.Error.Code == -32000 && strings.HasPrefix(o.Error.Message, "slicc: refused: ")
	}
	if !r.Known.OK || r.Known.MS > fast {
		t.Errorf("a world for a same-origin child frame that existed before the client attached: ok=%v after %d ms (%s)", r.Known.OK, r.Known.MS, r.Known.Error.Message)
	}
	if !refused(r.Under) || r.Under.MS-r.Known.MS > fast {
		t.Errorf("a world for a frame under a protected frame must be refused at once: %+v", r.Under)
	}
	if !refused(r.UnknownA) || !refused(r.UnknownB) {
		t.Errorf("worlds for frames the tree does not show must stay refused: a=%+v b=%+v", r.UnknownA, r.UnknownB)
	}
	if r.UnknownB.MS-r.Begin > holdMS+fast {
		t.Errorf("two unclassified frames on one page cost %d ms, more than one hold of %d ms", r.UnknownB.MS-r.Begin, holdMS)
	}
	if !r.Other.OK || r.Other.MS-r.Begin > fast {
		t.Errorf("Emulation.setDeviceMetricsOverride on another session waited behind a parked world: ok=%v after %d ms", r.Other.OK, r.Other.MS-r.Begin)
	}
	pos := map[string]int{}
	for i, name := range r.Order {
		pos[name] = i
	}
	if !r.Later.OK || pos["later"] < pos["unknownA"] || pos["later"] < pos["unknownB"] || pos["unknownB"] < pos["unknownA"] {
		t.Errorf("commands on the parked session must keep their order: %v", r.Order)
	}
}
