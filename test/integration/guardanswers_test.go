package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

const evaluateAnswersLimit = 8192

func TestGuardEvaluateAnswersKeepNoPageContent(t *testing.T) {
	cmd := exec.Command(requireNode(t), "--input-type=module", "-e", `
import assert from 'node:assert/strict';
import { answerLog, attachAnswers } from './testdata/link/answers.js';
const secret = 'PAGE-CONTENT-SENTINEL';
let unhandled = 0;
process.on('unhandledRejection', () => { unhandled += 1; });
const log = answerLog();
const refusal = Object.assign(new Error('slicc: refused: ' + 'r'.repeat(400)), { cdp: { code: -32000, message: 'slicc: refused', data: secret } });
await assert.rejects(log.record('guarded', 'Runtime.evaluate', async () => { throw refusal; }), (e) => e === refusal);
const answer = { result: { type: 'string', subtype: 'null', value: secret, description: secret, objectId: secret, preview: { description: secret } }, exceptionDetails: { text: secret } };
assert.equal(await log.record('guarded', 'Runtime.evaluate', async () => answer), answer);
const late = log.record('guarded', 'Runtime.evaluate', () => new Promise((resolve, reject) => setTimeout(() => reject(new Error('CDP timed out: Runtime.evaluate')), 30)));
await assert.rejects(late, /CDP timed out/);
log.record('guarded', 'Runtime.evaluate', () => new Promise(() => {}));
assert.equal(await log.record('control', 'Runtime.evaluate', async () => ({ result: { type: secret } })).then((r) => r.result.type), secret);
const [refused, valued, timedOut, pending] = log.legs.guarded.last;
assert.deepEqual(Object.keys(refused).sort(), ['error', 'method', 'ms', 'ok', 'seq']);
assert.equal(refused.ok, false);
assert.equal(refused.error.message.length, 160);
assert.ok(refused.error.message.startsWith('slicc: refused: '));
assert.equal(refused.error.code, -32000);
assert.deepEqual({ ...valued, ms: 0 }, { seq: 2, method: 'Runtime.evaluate', ok: true, ms: 0, type: 'string', subtype: 'null' });
assert.equal(timedOut.ok, false);
assert.equal(timedOut.error.message, 'CDP timed out: Runtime.evaluate');
assert.equal(timedOut.error.code, undefined);
assert.ok(timedOut.ms >= 25);
assert.deepEqual(pending, { seq: 4, method: 'Runtime.evaluate', ok: null });
assert.deepEqual(log.legs.control.last, [{ seq: 5, method: 'Runtime.evaluate', ok: true, ms: log.legs.control.last[0].ms }]);
for (let i = 0; i < 20; i += 1) await log.record('guarded', 'Runtime.evaluate', async () => ({ result: { type: 'number', value: i } }));
assert.equal(log.legs.guarded.sent, 24);
assert.equal(log.legs.guarded.last.length, 12);
assert.deepEqual(log.legs.guarded.last.map((e) => e.seq), Array.from({ length: 12 }, (_, i) => 14 + i));
const row = (control, guarded = 'pass', live = 'pass', extra = {}) => ({ id: 'C8.protected-document-restore', target: 'vault', variant: 'lagging', control, guarded, live, observations: { guarded: { evaluateRefused: true } }, ...extra });
for (const clean of [row('pass'), row('skip', 'skip', 'skip'), row('not-run', 'not-run', 'not-run'), row('pass', 'pass', 'pass', { diagnostics: { iframeLeak: { frames: [] } } })]) {
  const before = JSON.stringify(clean);
  assert.equal(attachAnswers(clean, log), clean);
  assert.equal(JSON.stringify(clean), before);
}
const unrecorded = row('fail');
const unrecordedBefore = JSON.stringify(unrecorded);
attachAnswers(unrecorded, undefined);
assert.equal(JSON.stringify(unrecorded), unrecordedBefore);
const kept = attachAnswers(row('pass', 'fail', 'pass', { diagnostics: { iframeLeak: { frames: [] } } }), log);
assert.deepEqual(kept.diagnostics.iframeLeak, { frames: [] });
const red = attachAnswers(row('pass', 'pass', 'incomplete'), log);
assert.deepEqual(Object.keys(red.diagnostics), ['evaluateAnswers']);
assert.deepEqual(Object.keys(red.diagnostics.evaluateAnswers).sort(), ['control', 'guarded']);
assert.equal(red.diagnostics.evaluateAnswers.guarded.last.length, 12);
assert.notEqual(red.diagnostics.evaluateAnswers.guarded.last[0], log.legs.guarded.last[0]);
const keys = (v) => (v && typeof v === 'object' ? Object.entries(v).flatMap(([k, c]) => [k, ...keys(c)]) : []);
for (const r of [kept, red]) {
  const text = JSON.stringify(r.diagnostics);
  assert.ok(!text.includes(secret), text);
  for (const k of ['value', 'description', 'objectId', 'preview', 'exceptionDetails', 'data']) assert.ok(!keys(r.diagnostics).includes(k), k);
}
const same = answerLog();
const original = Promise.resolve(answer);
assert.equal(same.record('guarded', 'Runtime.evaluate', () => original), original);
const rejected = Promise.reject(refusal);
const returned = same.record('guarded', 'Runtime.evaluate', () => rejected);
assert.equal(returned, rejected);
await assert.rejects(returned, (e) => e === refusal);
const thrown = new Error('sync refusal');
assert.throws(() => same.record('guarded', 'Runtime.evaluate', () => { throw thrown; }), (e) => e === thrown);
const plain = { result: { type: 'number', value: secret } };
assert.equal(same.record('guarded', 'Runtime.evaluate', () => plain), plain);
const order = [];
const tick = Promise.resolve(answer);
same.record('guarded', 'Runtime.evaluate', () => tick).then(() => order.push('recorded'));
Promise.resolve(answer).then(() => order.push('bare'));
await new Promise((resolve) => setImmediate(resolve));
assert.deepEqual(order, ['recorded', 'bare']);
assert.deepEqual(same.legs.guarded.last.map((e) => [e.seq, e.ok, e.type, e.error?.message]), [[1, true, 'string', undefined], [2, false, undefined, refusal.message.slice(0, 160)], [3, false, undefined, 'sync refusal'], [4, true, 'number', undefined], [5, true, 'string', undefined]]);
const early = answerLog();
early.record('guarded', 'Runtime.evaluate', async () => answer);
early.record('guarded', 'Runtime.evaluate', async () => { throw refusal; }).catch(() => {});
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(unhandled, 0);
console.log(JSON.stringify({ results: [attachAnswers(row('pass', 'fail'), early), attachAnswers(row('pass'), early)] }));
`)
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			out = append(out, exit.Stderr...)
		}
		t.Fatalf("evaluate answers: %v\n%s", err, out)
	}
	var sum guardSummary
	if err := json.Unmarshal(out, &sum); err != nil || len(sum.Results) != 2 {
		t.Fatalf("evaluate answers summary: %v\n%s", err, out)
	}
	red, clean := sum.Results[0], sum.Results[1]
	line := evaluateAnswersLine(red)
	if !strings.HasPrefix(line, "\n  Runtime.evaluate answers {") || !strings.Contains(line, `"type":"string"`) || !strings.Contains(line, `"message":"slicc: refused: rrr`) || !strings.Contains(line, `"code":-32000`) || strings.Contains(line, "PAGE-CONTENT-SENTINEL") {
		t.Errorf("the red row prints %q", line)
	}
	if key := evaluateAnswersPageKey(red); key != "" {
		t.Errorf("the red row copied %q: %s", key, red.Diagnostics)
	}
	if line := evaluateAnswersLine(clean); line != "" || len(clean.Diagnostics) != 0 {
		t.Errorf("the clean row carries answers %q: %s", line, clean.Diagnostics)
	}
	for _, key := range []string{"value", "description", "objectId", "preview", "exceptionDetails", "data", "expression", "params"} {
		for _, at := range []string{`{"%s":"x"}`, `{"guarded":{"sent":1,"last":[{"seq":1,"ok":false,"error":{"message":"m","%s":{"x":1}}}]}}`} {
			forged := guardRow{ID: "C8.x", Control: "pass", Guarded: "fail", Live: "pass", Diagnostics: json.RawMessage(`{"evaluateAnswers":` + fmt.Sprintf(at, key) + `}`)}
			if got := evaluateAnswersPageKey(forged); got != key {
				t.Errorf("a copied %s key went unnoticed: %q in %s", key, got, forged.Diagnostics)
			}
		}
	}
	long := guardRow{ID: "C8.x", Control: "fail", Diagnostics: json.RawMessage(`{"evaluateAnswers":{"guarded":{"note":"` + strings.Repeat("a", 2*evaluateAnswersLimit) + `"}}}`)}
	if got := evaluateAnswersLine(long); len(got) > evaluateAnswersLimit+64 {
		t.Errorf("the printed answers are unbounded: %d bytes", len(got))
	}
}

func TestCDPGuardEvaluateAnswersOnARedRow(t *testing.T) {
	const id = "C8.protected-document-restore"
	sum := runGuard(t, guardRun{variant: "lagging", slots: 1, only: id, inject: "evaluate_answered", rowTimeout: guardRowTimeout, errorPageVault: true})
	if len(sum.Results) != 1 {
		t.Fatalf("want one row of %s, got %+v", id, sum.Results)
	}
	row := sum.Results[0]
	var observed struct {
		Guarded struct {
			EvaluateRefused any `json:"evaluateRefused"`
		} `json:"guarded"`
	}
	_ = json.Unmarshal(row.Observed, &observed)
	if row.ID != id || row.Target != "vault" || row.Guarded == "pass" || observed.Guarded.EvaluateRefused != false {
		t.Fatalf("the answered guarded Runtime.evaluate should leave the guarded leg red with evaluateRefused=false: %+v", row)
	}
	line := evaluateAnswersLine(row)
	t.Logf("%s vault lagging:%s", id, line)
	if key := evaluateAnswersPageKey(row); key != "" {
		t.Errorf("the answers copied %q: %s", key, row.Diagnostics)
	}
	if strings.Contains(string(row.Diagnostics), "evaluate-answer-sentinel") || strings.Contains(line, "evaluate-answer-sentinel") {
		t.Errorf("the answered value reached the evidence: %s", row.Diagnostics)
	}
	var d struct {
		EvaluateAnswers map[string]struct {
			Sent int `json:"sent"`
			Last []struct {
				Seq    int    `json:"seq"`
				Method string `json:"method"`
				OK     *bool  `json:"ok"`
				MS     *int   `json:"ms"`
				Type   string `json:"type"`
			} `json:"last"`
		} `json:"evaluateAnswers"`
	}
	if err := json.Unmarshal(row.Diagnostics, &d); err != nil {
		t.Fatalf("diagnostics: %v: %s", err, row.Diagnostics)
	}
	for _, leg := range []string{"control", "guarded"} {
		answers := d.EvaluateAnswers[leg]
		if answers.Sent == 0 || len(answers.Last) == 0 || len(answers.Last) > 12 || answers.Sent < len(answers.Last) {
			t.Errorf("%s: no bounded Runtime.evaluate answers: %s", leg, row.Diagnostics)
		}
	}
	answered := false
	for _, e := range d.EvaluateAnswers["guarded"].Last {
		if e.Method == "Runtime.evaluate" && e.OK != nil && *e.OK && e.Type == "string" && e.MS != nil && e.Seq > 0 {
			answered = true
		}
	}
	if !answered {
		t.Errorf("the guarded leg does not show the answered Runtime.evaluate: %s", row.Diagnostics)
	}
}

func evaluateAnswersOf(r guardRow) json.RawMessage {
	var d struct {
		EvaluateAnswers json.RawMessage `json:"evaluateAnswers"`
	}
	if len(r.Diagnostics) == 0 || json.Unmarshal(r.Diagnostics, &d) != nil {
		return nil
	}
	return d.EvaluateAnswers
}

func evaluateAnswersLine(r guardRow) string {
	raw := evaluateAnswersOf(r)
	if len(raw) == 0 {
		return ""
	}
	text := string(raw)
	if len(text) > evaluateAnswersLimit {
		text = text[:evaluateAnswersLimit] + "..."
	}
	return "\n  Runtime.evaluate answers " + text
}

func evaluateAnswersPageKey(r guardRow) string {
	raw := evaluateAnswersOf(r)
	if len(raw) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "unreadable"
	}
	var walk func(any) string
	walk = func(v any) string {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				switch k {
				case "value", "description", "objectId", "preview", "exceptionDetails", "data", "expression", "params":
					return k
				}
				if k := walk(child); k != "" {
					return k
				}
			}
		case []any:
			for _, child := range v {
				if k := walk(child); k != "" {
					return k
				}
			}
		}
		return ""
	}
	return walk(v)
}

func checkEvaluateAnswers(t testing.TB, r guardRow) {
	t.Helper()
	if key := evaluateAnswersPageKey(r); key != "" {
		t.Errorf("%s %s %s: the Runtime.evaluate answers copied %q", r.ID, r.Target, r.Variant, key)
	}
	clean := true
	for _, leg := range guardRowStatuses(r) {
		clean = clean && (leg == "pass" || leg == "skip" || leg == "not-run")
	}
	if clean && len(evaluateAnswersOf(r)) != 0 {
		t.Errorf("%s %s %s: passed but carries Runtime.evaluate answers, which are kept only for red rows", r.ID, r.Target, r.Variant)
	}
}
