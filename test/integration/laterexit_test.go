package integration

import (
	"os/exec"
	"testing"
)

func TestLaterExitVectorRetainsAllNonScriptCleanupEvidence(t *testing.T) {
	cmd := exec.Command(requireNode(t), "--input-type=module", "-e", `
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { adaptLaterExitVector, laterRefusal } from './testdata/link/later-exit.js';
const set = JSON.parse(await readFile('../../node_modules/@ai-ecoverse/slicc-browsers/guard-vectors.json', 'utf8'));
const before = structuredClone(set);
adaptLaterExitVector(set);
for (let i = 0; i < set.vectors.length; i++) {
  const v = set.vectors[i], old = before.vectors[i];
  if (v.id !== 'X1.after-exit') { assert.deepEqual(v, old); continue; }
  assert.equal(v.skip, undefined);
  assert.deepEqual(v.targets, old.targets);
  assert.deepEqual(v.liveCheck, old.liveCheck);
  assert.deepEqual(v.observe.instrumented, old.observe.instrumented);
  for (const key of Object.keys(old.observe).filter((key) => key !== 'instrumented')) assert.deepEqual(v.observe[key], old.observe[key]);
  for (const expected of [v.control.expected, v.expected]) {
    assert.equal(expected.instrumented, false);
    assert.equal(expected.laterScriptRefused, true);
    for (const key of Object.keys(old.expected).filter((key) => key !== 'instrumented')) assert.equal(expected[key], old.expected[key]);
  }
  const script = v.setup.find((s) => s.cdp === 'Page.addScriptToEvaluateOnNewDocument');
  assert.equal(script.refusable, true);
  const exact = new RegExp(script.refusal.match);
  assert.equal(exact.test(laterRefusal), true);
  assert.equal(exact.test('CDP timed out'), false);
  assert.equal(exact.test('slicc: refused: a different policy'), false);
  for (let k = 0; k < v.setup.length; k++) if (v.setup[k].cdp !== 'Page.addScriptToEvaluateOnNewDocument') assert.deepEqual(v.setup[k], old.setup[k]);
  for (let k = 0; k < v.trigger.length; k++) if (!v.trigger[k].settle?.beacon) assert.deepEqual(v.trigger[k], old.trigger[k]);
}
const bad = structuredClone(before);
bad.vectors.find((v) => v.id === 'X1.after-exit').expected.instrumented = false;
assert.throws(() => adaptLaterExitVector(bad), /changed upstream/);
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("retired-script exit vector: %v\n%s", err, out)
	}
}
