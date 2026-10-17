#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { isMain } from './is-main.mjs';
import { parseArgs } from 'node:util';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

export function modulePath(gomod) {
  const match = gomod.match(/^module\s+("?)([^\s"]+)\1\s*$/m);
  if (!match) throw new Error('go.mod has no module line');
  return match[2];
}

export function impliedMajor(path) {
  const match = path.match(/\/v([0-9]+)$/);
  if (!match) return 1;
  const major = Number(match[1]);
  if (major < 2) throw new Error(`module path ${path} has an invalid major suffix /v${match[1]}`);
  return major;
}

export function checkMajor(version, gomod) {
  const match = String(version).match(/^v?([0-9]+)\.[0-9]+\.[0-9]+(?:[-+].*)?$/);
  if (!match) throw new Error(`${version} is not a semantic version`);
  const major = Number(match[1]);
  const path = modulePath(gomod);
  const implied = impliedMajor(path);
  if (major > implied) {
    throw new Error(
      `release ${version} is major ${major}, but module path ${path} only allows v${implied === 1 ? '0 or v1' : implied}; change the module path to ${path.replace(/\/v[0-9]+$/, '')}/v${major} first`,
    );
  }
  if (major < implied && !(implied === 1 && major === 0)) {
    throw new Error(`release ${version} is major ${major}, but module path ${path} needs v${implied}`);
  }
  return { major, path };
}

if (isMain(import.meta.url)) {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: { 'go-mod': { type: 'string', default: join(root, 'go.mod') } },
  });
  if (positionals.length !== 1) {
    process.stderr.write('usage: go-major.mjs [--go-mod <file>] <version>\n');
    process.exit(2);
  }
  try {
    const { major, path } = checkMajor(positionals[0], readFileSync(values['go-mod'], 'utf8'));
    process.stdout.write(`go-major: PASS ${positionals[0]} (major ${major}) fits module path ${path}\n`);
  } catch (err) {
    process.stderr.write(`go-major: FAIL ${err.message}\n`);
    process.exit(1);
  }
}
