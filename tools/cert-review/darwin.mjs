#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { parseArgs } from 'node:util';
import { notarized } from './lib.mjs';

const { values } = parseArgs({
  options: {
    dir: { type: 'string' },
    out: { type: 'string' },
    team: { type: 'string', default: 'S8LB56P782' },
    identifier: { type: 'string', default: 'ai.sliccy.slicc' },
    tries: { type: 'string', default: '3' },
    backoff: { type: 'string', default: '20' },
  },
});
const sleep = (s) => new Promise((r) => setTimeout(r, s * 1000));
const capture = (cmd, args) => {
  const r = spawnSync(cmd, args, { encoding: 'utf8', timeout: 120_000 });
  return { cmd: `${cmd} ${args.join(' ')}`, exit: r.status, signal: r.signal, output: `${r.stdout ?? ''}${r.stderr ?? ''}`, error: r.error?.message };
};
const binaries = [];
for (const asset of ['slicc-darwin-arm64', 'slicc-darwin-amd64']) {
  const file = join(values.dir, asset);
  let bytes;
  try {
    bytes = readFileSync(file);
  } catch {
    binaries.push({ asset, status: 'INCOMPLETE', why: `${asset} wasn't downloaded` });
    continue;
  }
  chmodSync(file, 0o755);
  const display = capture('codesign', ['-dv', '--verbose=2', file]);
  const strict = capture('codesign', ['--verify', '--strict', '--verbose=2', file]);
  const answers = [];
  for (let i = 0; i < Number(values.tries); i++) {
    if (i > 0) await sleep(Number(values.backoff) * i);
    answers.push(capture('spctl', ['-a', '-vv', '-t', 'install', file]));
    if (notarized(answers)) break;
  }
  const finished = (r) => r.exit !== null && !r.signal && !r.error;
  const why = [];
  if (!display.output.includes(`Identifier=${values.identifier}\n`)) why.push(`its identifier isn't ${values.identifier}`);
  if (!display.output.includes('Authority=Developer ID Application:')) why.push('it has no Developer ID Application authority');
  if (!display.output.includes(`TeamIdentifier=${values.team}\n`)) why.push(`its team isn't ${values.team}`);
  if (!/flags=0x[0-9a-f]*\(runtime\)/.test(display.output)) why.push('it lacks the hardened runtime');
  if (display.exit !== 0 || strict.exit !== 0) why.push('codesign --verify --strict refuses it');
  const said = answers.map((a) => /source=([^\n]+)/.exec(a.output)?.[1] ?? (finished(a) ? `exit ${a.exit}` : 'no answer'));
  let status;
  if (!finished(display) || !finished(strict)) {
    status = 'INCOMPLETE';
    why.unshift('codesign did not finish');
  } else if (why.length > 0) status = 'FAIL';
  else if (notarized(answers)) status = 'PASS';
  else {
    status = answers.every(finished) ? 'FAIL' : 'INCOMPLETE';
    why.push(`Gatekeeper never said Notarized Developer ID in ${answers.length} tries: ${said.join(' / ')}`);
  }
  binaries.push({
    asset,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    status,
    why: why.join('; '),
    display,
    strict,
    answers,
  });
}
mkdirSync(join(values.out, '..'), { recursive: true });
writeFileSync(values.out, `${JSON.stringify({ team: values.team, identifier: values.identifier, binaries }, null, 2)}\n`);
for (const b of binaries) console.log(`${b.asset}: ${b.status}${b.why ? ` (${b.why})` : ''}`);
for (const b of binaries) for (const a of b.answers ?? []) console.log(`  spctl: ${a.output.replace(/\n/g, ' | ')}`);
