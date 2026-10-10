#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const { values } = parseArgs({
  options: {
    dir: { type: 'string', default: join(root, 'dist', 'npm') },
    'dry-run': { type: 'boolean', default: false },
  },
});
const dryRun = values['dry-run'];

function npm(args, options = {}) {
  return spawnSync('npm', args, { encoding: 'utf8', shell: process.platform === 'win32', ...options });
}

function minimumNpm() {
  const [major, minor, patch] = npm(['--version']).stdout.trim().split('.').map(Number);
  const ok = major > 11 || (major === 11 && (minor > 5 || (minor === 5 && patch >= 1)));
  if (!ok) throw new Error('npm trusted publishing needs npm 11.5.1 or newer');
}

function published(name, version) {
  const res = npm(['view', `${name}@${version}`, 'version']);
  return res.status === 0 && res.stdout.trim() === version;
}

const dirs = readdirSync(values.dir)
  .map((entry) => join(values.dir, entry))
  .map((dir) => ({ dir, pkg: JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) }))
  .sort((a, b) => Number(Boolean(a.pkg.optionalDependencies)) - Number(Boolean(b.pkg.optionalDependencies)));

if (!dryRun && process.env.SLICC_NPM_PUBLISH !== 'true') {
  process.stdout.write('::warning::npm publishing is off; set the repository variable SLICC_NPM_PUBLISH to true once trusted publishing is set up\n');
  process.exit(0);
}
if (!dryRun) minimumNpm();
for (const { dir, pkg } of dirs) {
  if (!dryRun && published(pkg.name, pkg.version)) {
    process.stdout.write(`${pkg.name}@${pkg.version} is already published\n`);
    continue;
  }
  const args = ['publish', dir, '--access', 'public'];
  if (dryRun) args.push('--dry-run', '--tag', 'dry-run');
  else args.push('--provenance');
  const res = npm(args, { stdio: 'inherit' });
  if (res.status !== 0) {
    process.stderr.write(`npm publish ${pkg.name}@${pkg.version} failed\n`);
    process.exit(res.status || 1);
  }
}
