#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const usage = 'usage: shard-times.mjs [--out <file>] (--run <CI run id> [--repo <owner/name>] | <platform>=<go test -v log>...)';

export function parseLog(text) {
  const times = {};
  for (const line of text.split(/\r?\n/)) {
    const m = line.replace(/^\S+Z /, '').match(/^--- (?:PASS|FAIL|SKIP): (Test\w+) \(([0-9.]+)s\)/);
    if (m) times[m[1]] = Math.max(times[m[1]] ?? 0, Number(m[2]));
  }
  return times;
}

export function platformOf(jobName) {
  const m = String(jobName).match(/^test \((ubuntu|macos|windows)-[^,]*, /);
  return m ? { ubuntu: 'linux', macos: 'darwin', windows: 'win32' }[m[1]] : '';
}

export function merge(existing, logs) {
  const out = { ...existing };
  const fresh = {};
  for (const [os, text] of logs) fresh[os] = { ...(fresh[os] ?? {}), ...parseLog(text) };
  for (const [os, times] of Object.entries(fresh)) {
    if (Object.keys(times).length === 0) throw new Error(`no test timings in the ${os} logs; run go test with -v`);
    out[os] = Object.fromEntries(
      Object.keys(times)
        .sort()
        .map((n) => [n, Math.round(times[n] * 10) / 10]),
    );
  }
  return Object.fromEntries(Object.keys(out).sort().map((os) => [os, out[os]]));
}

function gh(args) {
  return execFileSync('gh', args, { encoding: 'utf8', maxBuffer: 256 * 1024 * 1024 });
}

function runLogs(run, repo) {
  const jobs = JSON.parse(gh(['api', '--paginate', '--slurp', `repos/${repo}/actions/runs/${run}/jobs?per_page=100`])).flatMap((p) => p.jobs);
  const logs = [];
  for (const job of jobs) {
    const os = platformOf(job.name);
    if (!os) continue;
    if (job.status !== 'completed') throw new Error(`${job.name} has not finished`);
    const path = `repos/${repo}/actions/jobs/${job.id}/logs`;
    let text;
    try {
      text = gh(['api', '--allow-escape-sequences', path]);
    } catch {
      text = gh(['api', path]);
    }
    logs.push([os, text]);
  }
  if (logs.length === 0) throw new Error(`run ${run} has no test jobs`);
  return logs;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  let out = join(root, 'test', 'integration', 'shard-times.json');
  let run = '';
  let repo = 'ai-ecoverse/slicc-cli';
  const files = [];
  try {
    for (let i = 0; i < args.length; i += 1) {
      const a = args[i];
      if (a === '--out' || a === '--run' || a === '--repo') {
        const v = args[++i];
        if (!v) throw new Error(`${a} needs a value`);
        if (a === '--out') out = v;
        else if (a === '--run') run = v;
        else repo = v;
      } else if (/^[a-z0-9]+=./.test(a)) {
        const at = a.indexOf('=');
        files.push([a.slice(0, at), a.slice(at + 1)]);
      } else throw new Error(`unknown argument ${a}`);
    }
    if (Boolean(run) === files.length > 0) throw new Error('give either --run or log files');
    const logs = run ? runLogs(run, repo) : files.map(([os, f]) => [os, readFileSync(f, 'utf8')]);
    const existing = existsSync(out) ? JSON.parse(readFileSync(out, 'utf8')) : {};
    const merged = merge(existing, logs);
    writeFileSync(out, JSON.stringify(merged, null, 2) + '\n');
    for (const [os, times] of Object.entries(merged)) {
      const total = Object.values(times).reduce((a, b) => a + b, 0);
      process.stdout.write(`${os}: ${Object.keys(times).length} tests, ${Math.round(total)} s\n`);
    }
  } catch (err) {
    process.stderr.write(`shard-times: ${err.message}\n${usage}\n`);
    process.exit(2);
  }
}
