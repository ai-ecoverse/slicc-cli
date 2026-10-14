#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const suite = process.env.SLICC_TEST_SHARD_SUITE || join(root, 'test', 'integration');

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

export function shardFlag(names, { index, count }) {
  if (count === 1) return [];
  const pick = (i) => names.filter((_, n) => n % count === i - 1);
  if (index < count) return ['-run', `^(${pick(index).join('|')})$`];
  const others = names.filter((_, n) => n % count !== count - 1);
  return ['-skip', `^(${others.join('|')})$`];
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [spec, ...rest] = process.argv.slice(2);
  let flag;
  try {
    flag = shardFlag(testNames(suite), parseShard(spec));
  } catch (err) {
    process.stderr.write(`test-shard: ${err.message}\nusage: test-shard.mjs <index>/<count> [--print | go test flags and packages]\n`);
    process.exit(2);
  }
  if (rest.length === 1 && rest[0] === '--print') {
    process.stdout.write(flag.join('\n') + '\n');
    process.exit(0);
  }
  const run = spawnSync('go', ['test', ...flag, ...rest], { stdio: 'inherit' });
  if (run.error) {
    process.stderr.write(`test-shard: go test: ${run.error.message}\n`);
    process.exit(1);
  }
  process.exit(run.status ?? 1);
}
