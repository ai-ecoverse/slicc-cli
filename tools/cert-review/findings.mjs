#!/usr/bin/env node
import { readFileSync, writeFileSync } from 'node:fs';
import { parseArgs } from 'node:util';
import { decide, GAPS } from './lib.mjs';

const { values } = parseArgs({
  options: {
    delta: { type: 'string' },
    candidate: { type: 'string' },
    prev: { type: 'string' },
    rebuild: { type: 'string' },
    out: { type: 'string' },
  },
});

const read = (file) => {
  try {
    return JSON.parse(readFileSync(file, 'utf8'));
  } catch {
    return undefined;
  }
};

const delta = read(values.delta);
const candidate = read(values.candidate);
const prev = read(values.prev);
const rebuild = read(values.rebuild);
const findings = [];
if (!delta) findings.push({ kind: 'missing-evidence', what: 'the delta', blocking: true });
if (!candidate?.complete)
  findings.push({
    kind: 'missing-evidence',
    what: 'the candidate suite',
    blocking: true,
    ...(candidate ? { exit: candidate.exit, results: candidate.results.length, unfinished: candidate.unfinished, buildFailed: candidate.buildFailed } : {}),
  });
if (!rebuild) findings.push({ kind: 'missing-evidence', what: 'the rebuild from git archive', blocking: true });
else if (!rebuild.match) findings.push({ kind: 'not-reproducible', differ: rebuild.differ });
const row = delta?.row;
const names = new Set(row?.names ?? []);
const decision = row ? decide(row, prev?.results ?? [], candidate?.results ?? []) : undefined;
if (row && row.names.length && !prev)
  findings.push({ kind: 'missing-evidence', what: "the NEGATIVE run on main's tree", blocking: false });
if (decision && (GAPS.has(decision.decision) || decision.decision === 'fails-on-candidate'))
  findings.push({ kind: decision.decision, pr: decision.pr, title: decision.title });
for (const g of row?.gaps ?? []) findings.push({ kind: 'helper-gap', pr: row.pr, what: g });
for (const d of row?.dropped ?? []) findings.push({ kind: 'dropped-test', pr: row.pr, ...d });
for (const r of candidate?.results ?? [])
  if ((r.status === 'fail' || r.status === 'cancelled') && !names.has(r.name.split('/')[0]) && !r.name.includes('/'))
    findings.push({ kind: 'suite-failure', test: r.name, error: r.error ?? '' });
const brief = (s) => s && { exit: s.exit, complete: s.complete, pass: s.pass, fail: s.fail, skip: s.skip, seconds: s.seconds, buildFailed: s.buildFailed };
const result = {
  pr: row?.pr,
  row: decision,
  runs: { candidate: brief(candidate), prev: brief(prev) },
  rebuild,
  findings,
};
writeFileSync(values.out, `${JSON.stringify(result, null, 2)}\n`);
console.log(`findings: ${findings.length}`);
for (const f of findings) console.log(`- ${f.kind}${f.what ? ` ${f.what}` : ''}${f.test ? ` ${f.test}` : ''}`);
if (decision) console.log(`#${decision.pr} ${decision.decision}: ${decision.title}`);
