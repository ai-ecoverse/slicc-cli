#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const { values } = parseArgs({
  options: {
    repo: { type: 'string', default: root },
    out: { type: 'string', default: join(root, 'dist', 'sliccy-next') },
    'dry-run': { type: 'boolean', default: false },
  },
});

function fail(message) {
  process.stderr.write(`sliccy-next: ${message}\n`);
  process.exit(1);
}

function git(args) {
  const res = spawnSync('git', ['-C', values.repo, ...args], { encoding: 'utf8', maxBuffer: 16 << 20 });
  if (res.status !== 0) fail(`git ${args.join(' ')}: ${(res.stderr || '').trim()}`);
  return res.stdout;
}

function node(script, args) {
  const res = spawnSync(process.execPath, [join(root, 'scripts', script), ...args], { stdio: 'inherit' });
  if (res.status !== 0) process.exit(res.status ?? 1);
}

const tags = git(['tag', '--merged', 'HEAD', '--list', 'v[0-9]*', '--sort=-v:refname'])
  .split('\n')
  .map((t) => t.trim())
  .filter((t) => /^v[0-9]+\.[0-9]+\.[0-9]+$/.test(t));
if (!tags.length) fail('no release tag (vX.Y.Z) is reachable from HEAD');
const tag = tags[0];
const version = tag.slice(1);
process.stdout.write(`sliccy-next: the latest release is ${tag}; staging sliccy@7.0.0-next.${version} from its launcher\n`);

const work = mkdtempSync(join(tmpdir(), 'sliccy-next-'));
try {
  const launcher = join(work, 'slicc.js');
  writeFileSync(launcher, git(['show', `${tag}:npm/slicc.js`]));
  node('npm-stage.mjs', [
    '--name', 'sliccy',
    '--launcher-only',
    '--launcher', launcher,
    '--out', values.out,
    '--version', `7.0.0-next.${version}`,
    '--deps-version', version,
  ]);
} finally {
  rmSync(work, { recursive: true, force: true });
}
node('npm-publish.mjs', ['--dir', values.out, '--tag', 'next', '--require-registry', ...(values['dry-run'] ? ['--dry-run'] : [])]);
