#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const { values } = parseArgs({
  options: {
    repo: { type: 'string', default: 'https://tuf-repo-cdn.sigstore.dev' },
    file: { type: 'string', default: join(root, 'internal', 'attest', 'trusted_root.json') },
    write: { type: 'boolean', default: false },
  },
});

function fail(message, code = 2) {
  process.stderr.write(`sigstore-root: ${message}\n`);
  process.exit(code);
}

async function get(path, json = true) {
  let res;
  try {
    res = await fetch(`${values.repo.replace(/\/+$/, '')}/${path}`, { signal: AbortSignal.timeout(30000) });
  } catch (error) {
    fail(`GET ${path}: ${error.cause?.code ?? error.message}`, 3);
  }
  if (res.status >= 500) fail(`GET ${path}: HTTP ${res.status}`, 3);
  if (!res.ok) fail(`GET ${path}: HTTP ${res.status}`);
  return json ? res.json() : Buffer.from(await res.arrayBuffer());
}

const sha256 = (buf) => createHash('sha256').update(buf).digest('hex');

const timestamp = await get('timestamp.json');
const snapshotVersion = timestamp.signed?.meta?.['snapshot.json']?.version;
if (!snapshotVersion) fail('timestamp.json names no snapshot');
const snapshot = await get(`${snapshotVersion}.snapshot.json`);
const targetsVersion = snapshot.signed?.meta?.['targets.json']?.version;
if (!targetsVersion) fail('the snapshot names no targets');
const targets = await get(`${targetsVersion}.targets.json`);
const target = targets.signed?.targets?.['trusted_root.json'];
const want = target?.hashes?.sha256;
if (!want) fail('the targets name no trusted_root.json');
const have = sha256(readFileSync(values.file));
if (have === want) {
  process.stdout.write(`trusted_root.json is current (sha256 ${have}, targets version ${targetsVersion})\n`);
  process.exit(0);
}
const live = await get(`targets/${want}.trusted_root.json`, false);
if (sha256(live) !== want) fail('the downloaded trusted_root.json does not match its targets hash');
process.stdout.write(`trusted_root.json changed: embedded sha256 ${have}, live sha256 ${want} (targets version ${targetsVersion})\n`);
if (values.write) {
  writeFileSync(values.file, live);
  process.stdout.write(`wrote ${values.file}\n`);
  process.exit(0);
}
process.exit(1);
