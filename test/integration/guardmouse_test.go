package integration

import (
	"encoding/json"
	"os/exec"
	"testing"
)

func TestGuardMouseForegroundPreservesTransportAndRefusals(t *testing.T) {
	cmd := exec.Command(requireNode(t), "--input-type=module", "-e", `
import assert from 'node:assert/strict';
import { foregroundMouseCall } from './testdata/link/foreground.js';
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
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mouse foreground transport: %v\n%s", err, out)
	}
}

func checkMouseEvidence(t *testing.T, row guardRow) {
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
