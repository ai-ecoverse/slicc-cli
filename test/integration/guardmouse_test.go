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
const calls = [];
const client = { send: async (...args) => { calls.push(args); return result; } };
assert.equal(await foregroundMouseCall(client, 'Input.dispatchMouseEvent', params, 'clean'), result);
assert.deepEqual(calls, [['Page.bringToFront', {}, 'clean'], ['Input.dispatchMouseEvent', params, 'clean']]);
for (const method of ['Page.bringToFront', 'Input.dispatchMouseEvent']) {
  const failure = Object.assign(new Error('actual CDP refusal'), { cdp: { code: -32000 } });
  const sent = [];
  const refusing = { send: async (...args) => { sent.push(args); if (args[0] === method) throw failure; return result; } };
  await assert.rejects(foregroundMouseCall(refusing, 'Input.dispatchMouseEvent', params, 'clean'), (e) => e === failure);
  assert.equal(sent.length, method === 'Page.bringToFront' ? 1 : 2);
}
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

func TestCDPSharedOrdinaryMouseInput(t *testing.T) {
	for _, variant := range []string{"prompt", "lagging"} {
		t.Run(variant, func(t *testing.T) {
			sum := runGuard(t, guardRun{variant: variant, slots: 1, only: "C9.page-drivable", rowTimeout: guardRowTimeout})
			if !sum.Pass || len(sum.Results) != 1 || string(sum.Missing) != "[]" || sum.ObserverSkipped != 0 {
				t.Fatalf("ordinary mouse evidence incomplete or failed: %+v", sum)
			}
			row := sum.Results[0]
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
