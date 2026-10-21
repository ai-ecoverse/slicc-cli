#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { isMain } from './is-main.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const suite = process.env.SLICC_TEST_SHARD_SUITE || join(root, 'test', 'integration');
const timesFile = process.env.SLICC_TEST_SHARD_TIMES || join(suite, 'shard-times.json');
const platform = process.env.SLICC_TEST_SHARD_PLATFORM || process.platform;

export function testNames(dir) {
  const names = new Set();
  for (const file of readdirSync(dir)) {
    if (!file.endsWith('_test.go')) continue;
    for (const match of readFileSync(join(dir, file), 'utf8').matchAll(/^func (Test\w+)\(/gm)) {
      if (match[1] !== 'TestMain') names.add(match[1]);
    }
  }
  return [...names].sort();
}

export function parseShard(spec) {
  const match = String(spec).match(/^([0-9]+)\/([0-9]+)$/);
  if (!match) throw new Error(`shard ${spec} is not <index>/<count>`);
  const index = Number(match[1]);
  const count = Number(match[2]);
  if (count < 1 || index < 1 || index > count) throw new Error(`shard ${spec} is out of range`);
  return { index, count };
}

export function loadTimes(file, os) {
  if (!existsSync(file)) return {};
  const all = JSON.parse(readFileSync(file, 'utf8'));
  return all[os] ?? {};
}

export function weights(names, times) {
  const known = names.map((n) => times[n]).filter((s) => typeof s === 'number' && s >= 0).sort((a, b) => a - b);
  const fallback = known.length ? known[Math.floor(known.length / 2)] : 1;
  return new Map(names.map((n) => [n, typeof times[n] === 'number' && times[n] >= 0 ? times[n] : fallback]));
}

export function split(names, times, count) {
  const w = weights(names, times);
  const order = [...names].sort((a, b) => w.get(b) - w.get(a) || (a < b ? -1 : a > b ? 1 : 0));
  const shards = Array.from({ length: count }, () => ({ tests: [], seconds: 0 }));
  for (const name of order) {
    let best = 0;
    for (let i = 1; i < count; i += 1) if (shards[i].seconds < shards[best].seconds) best = i;
    shards[best].tests.push(name);
    shards[best].seconds += w.get(name);
  }
  for (const s of shards) s.tests.sort();
  return shards;
}

export function shardFlag(shards, { index, count }) {
  if (count === 1) return [];
  if (index < count) return ['-run', `^(${shards[index - 1].tests.join('|')})$`];
  const others = shards.slice(0, count - 1).flatMap((s) => s.tests);
  return ['-skip', `^(${others.sort().join('|')})$`];
}

export function retryArgs(rest, failed) {
  const out = [];
  for (let i = 0; i < rest.length; i += 1) {
    const a = rest[i];
    if (/^--?(test\.)?(run|skip)$/.test(a)) {
      i += 1;
      continue;
    }
    if (/^--?(test\.)?(run|skip)=/.test(a)) continue;
    out.push(a);
  }
  return ['-run', `^(${failed.join('|')})$`, ...out];
}

export function takeRetry(args) {
  const rest = [...args];
  const at = rest.indexOf('--retry');
  if (at < 0) return { retries: 0, rest };
  const retries = Number(rest[at + 1]);
  if (!Number.isInteger(retries) || retries < 0) throw new Error(`--retry ${rest[at + 1]} is not a count`);
  rest.splice(at, 2);
  return { retries, rest };
}

function goTest(args) {
  return new Promise((done) => {
    const run = spawn('go', ['test', ...args], { stdio: ['inherit', 'pipe', 'inherit'] });
    const seen = { ran: 0, took: [], failed: [], timedOut: '', stuck: [] };
    let tail = '';
    const see = (line) => {
      const finished = line.match(/^--- (PASS|FAIL|SKIP): (Test\w+) \(([0-9.]+)s\)/);
      if (finished) {
        seen.ran += 1;
        seen.took.push([finished[2], Number(finished[3])]);
        if (finished[1] === 'FAIL') seen.failed.push(finished[2]);
      }
      const panic = line.match(/^panic: test timed out after (\S+)/);
      if (panic) seen.timedOut = panic[1];
      const running = seen.timedOut && line.match(/^\s+(Test\S+) \((\S+)\)$/);
      if (running) seen.stuck.push(`${running[1]} for ${running[2]}`);
    };
    run.stdout.on('data', (chunk) => {
      process.stdout.write(chunk);
      const lines = (tail + chunk.toString('utf8')).split('\n');
      tail = lines.pop();
      for (const line of lines) see(line);
    });
    run.on('error', (err) => {
      process.stderr.write(`test-shard: go test: ${err.message}\n`);
      process.exit(1);
    });
    run.on('close', (code, signal) => {
      see(tail);
      done({ ...seen, code, signal });
    });
  });
}

if (isMain(import.meta.url)) {
  const [spec, ...args] = process.argv.slice(2);
  let flag;
  let shard;
  let retries;
  let rest;
  try {
    ({ retries, rest } = takeRetry(args));
    const parsed = parseShard(spec);
    const shards = split(testNames(suite), loadTimes(timesFile, platform), parsed.count);
    shard = shards[parsed.index - 1];
    flag = shardFlag(shards, parsed);
  } catch (err) {
    process.stderr.write(`test-shard: ${err.message}\nusage: test-shard.mjs <index>/<count> [--print | [--retry <n>] go test flags and packages]\n`);
    process.exit(2);
  }
  if (shard.tests.length === 0) {
    process.stderr.write(`test-shard ${spec}: no tests in ${suite}; a shard that runs nothing would pass\n`);
    process.exit(1);
  }
  if (rest.length === 1 && rest[0] === '--print') {
    process.stdout.write(flag.join('\n') + '\n');
    process.exit(0);
  }
  process.stderr.write(`test-shard ${spec}: ${shard.tests.length} tests, about ${Math.round(shard.seconds)} s by the ${platform} times\n`);
  const verbose = rest.some((a) => /^--?(test\.)?v(=true)?$/.test(a));
  let result = await goTest([...flag, ...rest]);
  const { ran, took, timedOut, stuck } = result;
  if (timedOut) {
    const total = took.reduce((sum, [, s]) => sum + s, 0);
    const slowest = [...took].sort((a, b) => b[1] - a[1]).slice(0, 5).map(([n, s]) => `${n} ${s} s`).join(', ');
    process.stderr.write(`test-shard: ${platform} ${spec} hit its -timeout ${timedOut} with ${stuck.join(', ') || 'no test'} running: the ${took.length} finished tests took ${Math.round(total)} s, the times predicted ${Math.round(shard.seconds)} s for the whole shard; the slowest finished: ${slowest || 'none'}\n`);
  }
  const flaky = [];
  for (let attempt = 1; result.code !== 0 && !result.timedOut && result.failed.length && attempt <= retries; attempt += 1) {
    const failed = [...new Set(result.failed)].sort();
    process.stderr.write(`test-shard ${spec}: retrying ${failed.join(', ')} (retry ${attempt} of ${retries})\n`);
    result = await goTest(retryArgs(rest, failed));
    const again = new Set(result.failed);
    flaky.push(...failed.filter((n) => !again.has(n) && result.code === 0));
    if (result.code !== 0) process.stderr.write(`test-shard ${spec}: ${[...again].sort().join(', ') || 'the retry'} failed again on retry ${attempt}\n`);
  }
  if (result.code !== 0) {
    process.stderr.write(`test-shard: FAIL ${platform} ${spec}: go test exited ${result.code ?? result.signal}\n`);
    process.exit(result.code || 1);
  }
  if (verbose && ran === 0) {
    process.stderr.write(`test-shard: FAIL ${platform} ${spec}: go test ran no tests of the ${shard.tests.length} selected\n`);
    process.exit(1);
  }
  if (flaky.length) process.stdout.write(`::warning title=flaky tests::${platform} ${spec}: ${flaky.join(', ')} failed, then passed on retry\n`);
  process.stdout.write(`test-shard: PASS ${platform} ${spec} ${verbose ? `ran ${ran} tests` : `selected ${shard.tests.length} tests`}${flaky.length ? `; ${flaky.join(', ')} passed on retry` : ''}\n`);
}
