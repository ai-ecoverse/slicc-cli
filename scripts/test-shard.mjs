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

if (isMain(import.meta.url)) {
  const [spec, ...rest] = process.argv.slice(2);
  let flag;
  let shard;
  try {
    const parsed = parseShard(spec);
    const shards = split(testNames(suite), loadTimes(timesFile, platform), parsed.count);
    shard = shards[parsed.index - 1];
    flag = shardFlag(shards, parsed);
  } catch (err) {
    process.stderr.write(`test-shard: ${err.message}\nusage: test-shard.mjs <index>/<count> [--print | go test flags and packages]\n`);
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
  const run = spawn('go', ['test', ...flag, ...rest], { stdio: ['inherit', 'pipe', 'inherit'] });
  let ran = 0;
  let tail = '';
  run.stdout.on('data', (chunk) => {
    process.stdout.write(chunk);
    const lines = (tail + chunk.toString('utf8')).split('\n');
    tail = lines.pop();
    for (const line of lines) if (/^--- (PASS|FAIL|SKIP): Test/.test(line)) ran += 1;
  });
  run.on('error', (err) => {
    process.stderr.write(`test-shard: go test: ${err.message}\n`);
    process.exit(1);
  });
  run.on('close', (code, signal) => {
    if (/^--- (PASS|FAIL|SKIP): Test/.test(tail)) ran += 1;
    if (code !== 0) {
      process.stderr.write(`test-shard: FAIL ${platform} ${spec}: go test exited ${code ?? signal}\n`);
      process.exit(code || 1);
    }
    if (verbose && ran === 0) {
      process.stderr.write(`test-shard: FAIL ${platform} ${spec}: go test ran no tests of the ${shard.tests.length} selected\n`);
      process.exit(1);
    }
    process.stdout.write(`test-shard: PASS ${platform} ${spec} ${verbose ? `ran ${ran} tests` : `selected ${shard.tests.length} tests`}\n`);
  });
}
