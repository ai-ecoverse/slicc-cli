package integration

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestGuardMouseForegroundPreservesTransportAndRefusals(t *testing.T) {
	cmd := exec.Command(requireNode(t), "--input-type=module", "-e", `
import assert from 'node:assert/strict';
import { failureEvidence, foregroundMouseCall } from './testdata/link/foreground.js';
const params = { type: 'mousePressed', x: 20, y: 20, button: 'left', clickCount: 1 };
const result = { actualReply: true };
const ready = { result: { value: { visible: true, focused: true, frames: 2 } } };
const calls = [];
const client = { send: async (...args) => { calls.push(args); return args[0] === 'Runtime.evaluate' ? ready : result; } };
assert.equal(await foregroundMouseCall(client, 'Input.dispatchMouseEvent', params, 'clean'), result);
assert.deepEqual(calls.map(([method]) => method), ['Page.bringToFront', 'Runtime.evaluate', 'Input.dispatchMouseEvent']);
assert.equal(calls[1][1].awaitPromise, true);
assert.equal(calls[1][1].returnByValue, true);
assert.equal(calls[1][2], 'clean');
assert.deepEqual(calls[2], ['Input.dispatchMouseEvent', params, 'clean']);
for (const [index, method] of ['Page.bringToFront', 'Runtime.evaluate', 'Input.dispatchMouseEvent'].entries()) {
  const failure = Object.assign(new Error('actual CDP refusal'), { cdp: { code: -32000 } });
  const sent = [];
  const refusing = { send: async (...args) => { sent.push(args); if (args[0] === method) throw failure; return args[0] === 'Runtime.evaluate' ? ready : result; } };
  await assert.rejects(foregroundMouseCall(refusing, 'Input.dispatchMouseEvent', params, 'clean'), (e) => e === failure);
  assert.equal(sent.length, index + 1);
}
for (const value of [{ visible: false, focused: true, frames: 2 }, { visible: true, focused: false, frames: 2 }, { visible: true, focused: true, frames: 1 }]) {
  const sent = [];
  const unready = { send: async (method) => { sent.push(method); return { result: { value } }; } };
  await assert.rejects(foregroundMouseCall(unready, 'Input.dispatchMouseEvent', params, 'clean'), /two focused visible frames/);
  assert.deepEqual(sent, ['Page.bringToFront', 'Runtime.evaluate']);
}
let render;
const held = [];
const pending = foregroundMouseCall({ send: async (method) => { held.push(method); return method === 'Runtime.evaluate' ? new Promise((resolve) => { render = resolve; }) : result; } }, 'Input.dispatchMouseEvent', params, 'clean');
await new Promise((resolve) => setImmediate(resolve));
assert.deepEqual(held, ['Page.bringToFront', 'Runtime.evaluate']);
render(ready);
assert.equal(await pending, result);
assert.deepEqual(held, ['Page.bringToFront', 'Runtime.evaluate', 'Input.dispatchMouseEvent']);
calls.length = 0;
await foregroundMouseCall(client, 'Input.dispatchMouseEvent', { ...params, type: 'mouseReleased' }, 'clean');
assert.equal(calls.length, 1);
assert.equal(calls[0][0], 'Input.dispatchMouseEvent');
calls.length = 0;
assert.equal(await foregroundMouseCall(client, 'Input.dispatchKeyEvent', params, 'clean'), result);
assert.deepEqual(calls, [['Input.dispatchKeyEvent', params, 'clean']]);
calls.length = 0;
await foregroundMouseCall(client, 'Runtime.evaluate', { expression: '1' }, 'clean');
assert.deepEqual(calls, [['Runtime.evaluate', { expression: '1' }, 'clean']]);
const recording = { mouseEvidence: [], send: client.send };
calls.length = 0;
assert.equal(await foregroundMouseCall(recording, 'Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab' }, 'clean'), result);
assert.equal(await foregroundMouseCall(recording, 'Input.dispatchKeyEvent', { type: 'keyDown', key: 'a', code: 'KeyA' }, 'clean'), result);
assert.equal(await foregroundMouseCall(recording, 'Input.dispatchMouseEvent', params, 'clean'), result);
assert.deepEqual(calls.map(([method]) => method), ['Input.dispatchKeyEvent', 'Input.dispatchKeyEvent', 'Page.bringToFront', 'Runtime.evaluate', 'Input.dispatchMouseEvent']);
assert.equal(recording.mouseEvidence.length, 2);
const [tab, press] = recording.mouseEvidence;
assert.equal(tab.type, 'keyDown');
assert.equal(tab.key, 'Tab');
assert.equal(tab.inputReply, result);
assert.ok(tab.at <= tab.replyAt && tab.replyAt <= press.at);
assert.equal(press.x, 20);
assert.equal(press.y, 20);
assert.equal(press.inputReply, result);
assert.deepEqual(press.renderReply, ready);
assert.ok(press.at <= press.focusAt && press.focusAt <= press.renderAt && press.renderAt <= press.replyAt);
const forced = { mouseEvidence: [], send: client.send };
calls.length = 0;
await assert.rejects(foregroundMouseCall(forced, 'Input.dispatchMouseEvent', params, 'clean', { unfocused: true }), /two focused visible frames/);
assert.deepEqual(calls.map(([method]) => method), ['Page.bringToFront', 'Runtime.evaluate']);
assert.equal(forced.mouseEvidence.length, 1);
assert.equal(forced.mouseEvidence[0].injected, 'unfocused');
assert.match(forced.mouseEvidence[0].error.message, /two focused visible frames/);
assert.equal(forced.mouseEvidence[0].inputReply, undefined);
assert.equal(typeof forced.mouseEvidence[0].replyAt, 'number');
const evidence = { control: [press], guarded: [] };
const row = (id, control, guarded = 'pass', live = 'pass') => ({ id, control, guarded, live });
assert.equal(failureEvidence(row('C9.frame-nested', 'pass'), evidence), undefined);
assert.equal(failureEvidence(row('C9.frame-nested', 'skip', 'skip', 'skip'), evidence), undefined);
assert.equal(failureEvidence(row('C9.frame-nested', 'not-run', 'not-run', 'not-run'), evidence), undefined);
assert.equal(failureEvidence(row('C9.frame-nested', 'fail'), evidence), evidence);
assert.equal(failureEvidence(row('C9.frame-nested', 'pass', 'incomplete'), evidence), evidence);
assert.equal(failureEvidence(row('C9.frame-nested', 'pass', 'pass', 'fail'), evidence), evidence);
assert.equal(failureEvidence(row('C9.page-drivable', 'pass'), evidence), evidence);
assert.equal(failureEvidence(row('C9.frame-nested', 'fail'), undefined), undefined);
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mouse foreground transport: %v\n%s", err, out)
	}
}

func checkMouseEvidence(t testing.TB, row guardRow) {
	t.Helper()
	var evidence map[string][]struct {
		Type        string          `json:"type"`
		Error       json.RawMessage `json:"error"`
		InputReply  json.RawMessage `json:"inputReply"`
		RenderReply struct {
			Result struct {
				Value struct {
					Visible bool `json:"visible"`
					Focused bool `json:"focused"`
					Frames  int  `json:"frames"`
				} `json:"value"`
			} `json:"result"`
		} `json:"renderReply"`
	}
	if err := json.Unmarshal(row.Diagnostics, &evidence); err != nil {
		t.Errorf("mouse transport evidence missing: %v", err)
		return
	}
	for _, side := range []string{"control", "guarded"} {
		calls := evidence[side]
		if len(calls) != 2 {
			t.Errorf("%s: want two actual mouse replies, got %s", side, row.Diagnostics)
			continue
		}
		for i, kind := range []string{"mousePressed", "mouseReleased"} {
			call := calls[i]
			if call.Type != kind || len(call.Error) != 0 || len(call.InputReply) == 0 || string(call.InputReply) == "null" {
				t.Errorf("%s: %s did not receive an actual successful reply: %s", side, kind, row.Diagnostics)
			}
		}
		ready := calls[0].RenderReply.Result.Value
		if !ready.Visible || !ready.Focused || ready.Frames < 2 {
			t.Errorf("%s: click preceded focused visible render frames: %s", side, row.Diagnostics)
		}
	}
}

func TestCDPSharedOrdinaryMouseInput(t *testing.T) {
	for _, variant := range []string{"prompt", "lagging"} {
		t.Run(variant, func(t *testing.T) {
			sum := runGuard(t, guardRun{variant: variant, slots: 1, only: "C9.page-drivable", rowTimeout: guardRowTimeout})
			if !sum.Pass || len(sum.Results) != 1 || string(sum.Missing) != "[]" || sum.ObserverSkipped != 0 {
				t.Fatalf("ordinary mouse evidence incomplete or failed: %+v", sum)
			}
			row := sum.Results[0]
			if row.Control == "skip" {
				if row.ID != "C9.page-drivable" || row.Target != "ordinary" || row.Guarded != "skip" || row.Live != "skip" || row.Reason != guardC9MacReason {
					t.Fatalf("unexpected ordinary mouse skip: %+v", row)
				}
				t.Logf("skipping ordinary mouse evidence: %s", row.Reason)
				return
			}
			t.Logf("mouse transport evidence=%s", row.Diagnostics)
			checkMouseEvidence(t, row)
			if row.ID != "C9.page-drivable" || row.Target != "ordinary" || row.Control != "pass" || row.Guarded != "pass" || row.Live != "pass" || len(row.Errors) != 0 {
				t.Fatalf("ordinary mouse row did not pass: %+v", row)
			}
			var observed map[string]map[string]bool
			if err := json.Unmarshal(row.Observed, &observed); err != nil {
				t.Fatal(err)
			}
			for _, side := range []string{"control", "guarded"} {
				for _, key := range []string{"exists", "alive", "snapshot", "click"} {
					if !observed[side][key] {
						t.Errorf("%s %s not proven: %s", side, key, row.Observed)
					}
				}
			}
		})
	}
}

func c9FailureEvidenceRow(r guardRow) bool {
	return strings.HasPrefix(r.ID, "C9.") && r.ID != "C9.page-drivable"
}

func c9EvidenceLine(r guardRow) string {
	if !c9FailureEvidenceRow(r) || len(r.Diagnostics) == 0 {
		return ""
	}
	return "\n  mouse and focus evidence " + string(r.Diagnostics)
}

type c9Call struct {
	Type        string          `json:"type"`
	Key         string          `json:"key"`
	X           float64         `json:"x"`
	Y           float64         `json:"y"`
	At          *float64        `json:"at"`
	FocusAt     *float64        `json:"focusAt"`
	RenderAt    *float64        `json:"renderAt"`
	ReplyAt     *float64        `json:"replyAt"`
	Injected    string          `json:"injected"`
	InputReply  json.RawMessage `json:"inputReply"`
	RenderReply json.RawMessage `json:"renderReply"`
	Error       *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func TestCDPGuardC9EvidenceOnAFailedControlClick(t *testing.T) {
	const id = "C9.frame-transparent"
	sum := runGuard(t, guardRun{variant: "prompt", slots: 1, only: id, inject: "unfocused_click", rowTimeout: guardRowTimeout})
	if len(sum.Results) != 1 {
		t.Fatalf("want one row of %s, got %+v", id, sum.Results)
	}
	row := sum.Results[0]
	if row.Control == "skip" {
		if row.ID != id || row.Guarded != "skip" || row.Live != "skip" || row.Reason != guardC9MacReason {
			t.Fatalf("unexpected skip: %+v", row)
		}
		t.Logf("skipping the C9 evidence check: %s", row.Reason)
		return
	}
	if row.ID != id || row.Target != "ordinary" || row.Control != "fail" || row.Guarded != "pass" || row.Live != "pass" || string(row.Why) != `"control click: got false, want true"` {
		t.Fatalf("the unfocused control click should fail only the control click: %+v", row)
	}
	line := c9EvidenceLine(row)
	if line == "" {
		t.Fatal("the failed row carries no mouse and focus evidence")
	}
	t.Logf("%s ordinary prompt:%s", id, line)
	var evidence map[string][]c9Call
	if err := json.Unmarshal(row.Diagnostics, &evidence); err != nil {
		t.Fatal(err)
	}
	control := evidence["control"]
	forced, tab := -1, -1
	for i, c := range control {
		if c.Injected == "unfocused" {
			if forced >= 0 {
				t.Fatalf("more than one press was made unfocused: %s", row.Diagnostics)
			}
			forced = i
		}
		if c.Key == "Tab" && forced < 0 {
			tab = i
		}
	}
	if forced < 0 {
		t.Fatalf("no press was made unfocused: %s", row.Diagnostics)
	}
	f := control[forced]
	if f.Type != "mousePressed" || f.X != 20 || f.Y != 20 || f.Error == nil || !strings.Contains(f.Error.Message, "did not reach two focused visible frames") || len(f.InputReply) != 0 || len(f.RenderReply) == 0 {
		t.Errorf("the unfocused press lacks its focus check evidence: %+v", f)
	}
	if f.At == nil || f.FocusAt == nil || f.RenderAt == nil || f.ReplyAt == nil || *f.At > *f.FocusAt || *f.FocusAt > *f.RenderAt || *f.RenderAt > *f.ReplyAt {
		t.Errorf("the unfocused press lacks ordered timestamps: %+v", f)
	}
	if tab < 0 {
		t.Errorf("no Tab was recorded before the unfocused press: %s", row.Diagnostics)
	} else if p := control[tab]; p.Type != "keyDown" || p.ReplyAt == nil || f.At == nil || *p.ReplyAt > *f.At || len(p.InputReply) == 0 {
		t.Errorf("the Tab before the unfocused press lacks its reply or timestamps: %+v", p)
	}
	if len(evidence["guarded"]) == 0 {
		t.Errorf("no guarded mouse evidence: %s", row.Diagnostics)
	}
}
