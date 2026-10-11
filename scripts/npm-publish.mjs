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
    tag: { type: 'string', default: 'latest' },
    'dry-run': { type: 'boolean', default: false },
  },
});
const dryRun = values['dry-run'];
const tag = values.tag;

const guarded = {
  sliccy: { tag: 'next', version: /^7\.0\.0-next\./, gate: 'SLICCY_NEXT_PUBLISH' },
};
const defaultGate = 'SLICC_NPM_PUBLISH';

function fail(message) {
  process.stderr.write(`npm-publish: ${message}\n`);
  process.exit(1);
}

function npm(args, options = {}) {
  return spawnSync('npm', args, { encoding: 'utf8', shell: process.platform === 'win32', ...options });
}

function minimumNpm() {
  const [major, minor, patch] = npm(['--version']).stdout.trim().split('.').map(Number);
  const ok = major > 11 || (major === 11 && (minor > 5 || (minor === 5 && patch >= 1)));
  if (!ok) fail('npm trusted publishing needs npm 11.5.1 or newer');
}

function published(name, version) {
  const res = npm(['view', `${name}@${version}`, 'version']);
  return res.status === 0 && res.stdout.trim() === version;
}

function check(pkg) {
  const rule = guarded[pkg.name];
  if (!rule) return;
  if (tag !== rule.tag) fail(`refusing to publish ${pkg.name} with the tag ${tag}; it only goes to ${rule.tag}`);
  if (!rule.version.test(pkg.version)) fail(`refusing to publish ${pkg.name}@${pkg.version}; its version must match ${rule.version}`);
}

function npmTag(pkg) {
  if (dryRun && !guarded[pkg.name] && tag === 'latest' && pkg.version.includes('-')) return 'dry-run';
  return tag;
}

const packages = readdirSync(values.dir)
  .map((entry) => join(values.dir, entry))
  .map((dir) => ({ dir, pkg: JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) }))
  .sort((a, b) => Number(Boolean(a.pkg.optionalDependencies)) - Number(Boolean(b.pkg.optionalDependencies)));

for (const { pkg } of packages) check(pkg);

const enabled = packages.filter(({ pkg }) => {
  const gate = guarded[pkg.name]?.gate || defaultGate;
  if (dryRun || process.env[gate] === 'true') return true;
  process.stdout.write(`::notice::skipping ${pkg.name}@${pkg.version}; set the repository variable ${gate} to true to publish it\n`);
  return false;
});

if (!dryRun && enabled.length) minimumNpm();
for (const { dir, pkg } of enabled) {
  if (!dryRun && published(pkg.name, pkg.version)) {
    process.stdout.write(`${pkg.name}@${pkg.version} is already published\n`);
    continue;
  }
  const args = ['publish', dir, '--access', 'public', '--tag', npmTag(pkg)];
  args.push(dryRun ? '--dry-run' : '--provenance');
  const res = npm(args, { stdio: 'inherit' });
  if (res.status !== 0) fail(`npm publish ${pkg.name}@${pkg.version} failed`);
}
