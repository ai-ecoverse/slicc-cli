#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { closeSync, mkdirSync, openSync, readFileSync, writeFileSync, writeSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { goResults, runPattern } from './lib.mjs';

const { values } = parseArgs({
  options: {
    tree: { type: 'string' },
    out: { type: 'string' },
    run: { type: 'string', default: '' },
    timeout: { type: 'string', default: '40' },
    pkg: { type: 'string', default: './test/integration/...' },
  },
});
const tree = resolve(values.tree);
const out = resolve(values.out);
mkdirSync(dirname(out), { recursive: true });
const env = { ...process.env };
for (const k of ['AWS_BEARER_TOKEN_BEDROCK', 'GH_TOKEN', 'GITHUB_TOKEN', 'ACTIONS_RUNTIME_TOKEN', 'ACTIONS_ID_TOKEN_REQUEST_TOKEN'])
  delete env[k];
const minutes = Number(values.timeout);
const log = openSync(`${out}.log`, 'w');

function goTest(run, file, budget) {
  const args = ['test', '-json', '-count=1', `-timeout=${budget}m`, ...(run ? [`-run=${run}`] : []), values.pkg];
  writeSync(log, `\nsuite: (${tree}) go ${args.join(' ')}\n`);
  console.log(`suite: (${tree}) go ${args.join(' ')}`);
  return new Promise((done) => {
    const fd = openSync(file, 'w');
    const child = spawn('go', args, { cwd: tree, env, detached: true, stdio: ['ignore', fd, 'pipe'] });
    child.stderr.on('data', (b) => writeSync(log, b));
    let timedOut = false;
    const kill = () => {
      try {
        process.kill(-child.pid, 'SIGKILL');
      } catch {}
    };
    const timer = setTimeout(() => {
      timedOut = true;
      kill();
    }, (budget + 3) * 60_000);
    process.once('SIGTERM', () => {
      kill();
      process.exit(143);
    });
    child.once('close', (status, signal) => {
      clearTimeout(timer);
      closeSync(fd);
      done({ status, signal, timedOut });
    });
  });
}

const started = Date.now();
const first = await goTest(values.run, `${out}.jsonl`, minutes);
let parsed = goResults(readFileSync(`${out}.jsonl`, 'utf8'));
let rows = parsed.rows;
let retried = [];
const exitOf = (r) => (r.timedOut ? 'timeout' : (r.status ?? `signal ${r.signal}`));
let exit = exitOf(first);
const cut = rows.filter((r) => r.status === 'cancelled' && !r.name.includes('/')).map((r) => r.name);
if (cut.length > 0 && !parsed.buildFailed) {
  retried = cut;
  const left = Math.max(5, Math.round(minutes - (Date.now() - started) / 60_000));
  const again = await goTest(runPattern(cut), `${out}.retry.jsonl`, left);
  const second = goResults(readFileSync(`${out}.retry.jsonl`, 'utf8'));
  const redo = new Set(cut);
  rows = [
    ...rows.filter((r) => !redo.has(r.name.split('/')[0])),
    ...second.rows,
  ];
  exit = `${exit}, retry ${exitOf(again)}`;
}
closeSync(log);
const count = (s) => rows.filter((r) => r.status === s).length;
const unfinished = rows.filter((r) => r.status === 'cancelled' && !r.name.includes('/')).map((r) => r.name);
const complete = !parsed.buildFailed && unfinished.length === 0 && rows.length > 0;
const summary = {
  tree,
  run: values.run,
  exit,
  complete,
  buildFailed: parsed.buildFailed,
  ...(retried.length ? { retried } : {}),
  unfinished,
  seconds: Math.round((Date.now() - started) / 1000),
  pass: count('pass'),
  fail: count('fail') + count('cancelled'),
  skip: count('skip'),
  results: rows,
};
writeFileSync(`${out}.json`, `${JSON.stringify(summary, null, 2)}\n`);
console.log(
  `suite: exit ${exit}, pass ${summary.pass}, fail ${summary.fail}, skip ${summary.skip}, ${summary.seconds}s${summary.buildFailed ? ', the build failed' : ''}${unfinished.length ? `, unfinished: ${unfinished.join(' ')}` : ''}`
);
