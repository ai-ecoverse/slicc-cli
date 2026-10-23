package integration

import (
	"os/exec"
	"testing"
)

func TestNativeLaterExitVectorUsesUnguardedControlsAndStrictCleanup(t *testing.T) {
	cmd := exec.Command(requireNode(t), "--input-type=module", "-e", `
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
const set = JSON.parse(await readFile('../../node_modules/@ai-ecoverse/slicc-browsers/guard-vectors.json', 'utf8'));
const v = set.vectors.find((v) => v.id === 'X1.after-exit');
assert.ok(v);
assert.equal(v.skip, undefined);
assert.deepEqual(v.targets, ['seven', 'vault']);
assert.equal(v.control.via, 'unguarded');
for (const name of ['instrumented', 'headed', 'intercepted']) assert.equal(v.control.expected[name], true);
for (const [method, name, proof] of [['Page.addScriptToEvaluateOnNewDocument', 'initScript', 'instrumented'], ['Network.setExtraHTTPHeaders', 'extraHeaders', 'headed'], ['Fetch.enable', 'interception', 'intercepted']]) {
  const step = v.setup.find((s) => s.cdp === method);
  assert.equal(step.as, name);
  assert.equal(step.refusable, true);
  assert.deepEqual(v.observe[proof].dependsOn, [name]);
}
for (const proof of ['served', 'document', 'forged', 'scripted', 'probed', 'paused']) assert.deepEqual(v.observe[proof].dependsOn, []);
for (const expected of [v.control.expected, v.expected]) {
  for (const proof of ['served', 'document']) assert.equal(expected[proof], true);
  for (const proof of ['forged', 'scripted', 'probed', 'paused']) assert.equal(expected[proof], false);
}
assert.equal(v.liveCheck.loaded, '{target}/canary/{tag}');
assert.equal(v.liveCheck.observer, 'fixture');
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native retired-script exit vector: %v\n%s", err, out)
	}
}
