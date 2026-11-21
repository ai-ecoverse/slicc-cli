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
    'require-registry': { type: 'boolean', default: false },
  },
});
const dryRun = values['dry-run'];
const tag = values.tag;

const guarded = {
  sliccy: { tag: 'next', version: /^7\.0\.0-next\./, skipLate: true },
};
const companions = new Set(['@ai-ecoverse/slicc-jerk']);

function fail(message, code = 1) {
  process.stderr.write(`npm-publish: ${message}\n`);
  process.exit(code);
}

function warn(message) {
  const line = `npm-publish: WARN ${message}\n`;
  process.stdout.write(line);
  process.stderr.write(line);
}

function annotate(title, message) {
  if (!process.env.GITHUB_ACTIONS) return;
  const data = message.replace(/%/g, '%25').replace(/\r/g, '%0D').replace(/\n/g, '%0A');
  process.stdout.write(`::warning title=${title}::${data}\n`);
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

async function publishPackage(pkg, args) {
  for (let attempt = 1; attempt <= 3; attempt += 1) {
    const res = npm(args);
    if (res.stdout) process.stdout.write(res.stdout);
    if (res.stderr) process.stderr.write(res.stderr);
    if (res.status === 0) return;
    if (dryRun) fail(`npm publish ${pkg.name}@${pkg.version} failed`);
    if (published(pkg.name, pkg.version)) {
      process.stdout.write(`${pkg.name}@${pkg.version} is published after the publish error\n`);
      return;
    }
    const output = `${res.stdout ?? ''}\n${res.stderr ?? ''}`;
    const conflict = /TLOG_CREATE_ENTRY_ERROR/i.test(output)
      || (/\b409\b/.test(output) && /transparency\s+log|sigstore|rekor/i.test(output));
    if (!conflict) fail(`npm publish ${pkg.name}@${pkg.version} failed`);
    if (attempt === 3) fail(`npm publish ${pkg.name}@${pkg.version} failed after ${attempt} attempts`);
    const delay = 5000 * 2 ** (attempt - 1);
    process.stdout.write(`retrying npm publish ${pkg.name}@${pkg.version} in ${delay / 1000} s after the transparency log conflict\n`);
    await new Promise((resolve) => setTimeout(resolve, delay));
  }
}

function registry() {
  const configured = process.env.npm_config_registry || npm(['config', 'get', 'registry']).stdout.trim();
  return (configured || 'https://registry.npmjs.org/').replace(/\/+$/, '');
}

function plain(text) {
  const flat = String(text ?? '').replace(/[\u0000-\u001f\u007f-\u009f\u2028\u2029]+/g, ' ').trim().slice(0, 200);
  return flat || 'no reason given';
}

async function onRegistry(base, name, version, timeoutMs) {
  const why = await probe(base, name, version, timeoutMs);
  return why === null ? '' : plain(why);
}

async function probe(base, name, version, timeoutMs) {
  try {
    const res = await fetch(`${base}/${name.replace('/', '%2f')}/${version}`, {
      headers: { accept: 'application/json', 'cache-control': 'no-cache' },
      signal: AbortSignal.timeout(timeoutMs),
    });
    if (!res.ok) return `HTTP ${res.status}`;
    const got = (await res.json()).version;
    return got === version ? null : `answers ${got ?? 'no version'}`;
  } catch (error) {
    return error.name === 'TimeoutError' ? `no answer in ${Math.round(timeoutMs / 1000)} s` : error.cause?.code ?? error.cause?.message ?? error.message;
  }
}

function seconds(name, fallback, least) {
  const value = Number(process.env[name] ?? fallback);
  return Number.isFinite(value) ? Math.max(least, value) : fallback;
}

async function requireDependencies(pkg) {
  const deps = Object.entries(pkg.optionalDependencies || {}).filter(([name]) => !companions.has(name));
  if (!guarded[pkg.name]?.tag || !deps.length) return '';
  const base = registry();
  const waitSeconds = seconds('SLICC_REGISTRY_WAIT_SECONDS', 5400, 0);
  const waitMs = waitSeconds * 1000;
  const pollMs = seconds('SLICC_REGISTRY_POLL_SECONDS', 30, 1) * 1000;
  const start = Date.now();
  const deadline = start + waitMs;
  const waited = (what, names) => waits.push(`registry wait ${Math.round((Date.now() - start) / 1000)} s of ${waitSeconds} s, ${what} ${names.join(', ')}`);
  let late = [];
  for (;;) {
    const timeoutMs = Math.min(30000, Math.max(1000, deadline - Date.now()));
    const answers = await Promise.all(deps.map(([name, version]) => onRegistry(base, name, version, timeoutMs)));
    const missing = deps.flatMap(([name, version], i) => (answers[i] ? [`${name}@${version} (${answers[i]})`] : []));
    if (!missing.length) break;
    late = deps.flatMap(([name, version], i) => (answers[i] ? [`${name}@${version}`] : []));
    if (Date.now() >= deadline) {
      if (!dryRun && guarded[pkg.name].skipLate) {
        waited('still missing', late);
        return missing.join(', ');
      }
      fail(`refusing to publish ${pkg.name}@${pkg.version}; not on the registry: ${missing.join(', ')}`, 3);
    }
    process.stdout.write(`waiting for ${missing.join(', ')} to appear on the registry\n`);
    await new Promise((resolve) => setTimeout(resolve, Math.min(pollMs, Math.max(1000, deadline - Date.now()))));
  }
  if (late.length) waited('last to appear', late);
  process.stdout.write(`every optional dependency of ${pkg.name}@${pkg.version} is on the registry\n`);
  return '';
}

function check(pkg) {
  const rule = guarded[pkg.name];
  if (!rule?.tag) return;
  if (tag !== rule.tag) fail(`refusing to publish ${pkg.name} with the tag ${tag}; it only goes to ${rule.tag}`);
  if (!rule.version.test(pkg.version)) fail(`refusing to publish ${pkg.name}@${pkg.version}; its version must match ${rule.version}`);
}

function npmTag(pkg) {
  if (dryRun && !guarded[pkg.name]?.tag && tag === 'latest' && pkg.version.includes('-')) return 'dry-run';
  return tag;
}

const packages = readdirSync(values.dir)
  .map((entry) => join(values.dir, entry))
  .map((dir) => ({ dir, pkg: JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) }))
  .sort((a, b) => Number(Boolean(a.pkg.optionalDependencies)) - Number(Boolean(b.pkg.optionalDependencies)));

if (packages.length === 0) fail(`no packages in ${values.dir}`);
for (const { pkg } of packages) check(pkg);

if (!dryRun) minimumNpm();
let already = 0;
const skipped = [];
const waits = [];
for (const { dir, pkg } of packages) {
  if (!dryRun && published(pkg.name, pkg.version)) {
    process.stdout.write(`${pkg.name}@${pkg.version} is already published\n`);
    already += 1;
    continue;
  }
  const late = !dryRun || values['require-registry'] ? await requireDependencies(pkg) : '';
  if (late) {
    const message = `${pkg.name}@${pkg.version} not published; not on the registry: ${late}`;
    warn(`${tag}: ${message}`);
    annotate(`${pkg.name}@${tag} not published`, `${message}. ${pkg.name}@${tag} stays on its previous version until the next release.`);
    skipped.push(`${pkg.name}@${pkg.version}`);
    continue;
  }
  const args = ['publish', dir, '--access', 'public', '--tag', npmTag(pkg)];
  args.push(dryRun ? '--dry-run' : '--provenance');
  await publishPackage(pkg, args);
}
const counts = dryRun ? `checked ${packages.length}` : `published ${packages.length - already - skipped.length}, ${already} already published`;
const wait = waits.map((line) => `; ${line}`).join('');
if (skipped.length) {
  process.stdout.write(`npm-publish: PARTIAL ${tag}: ${counts}, skipped ${skipped.join(', ')} of ${packages.length} packages${wait}\n`);
} else {
  process.stdout.write(`npm-publish: PASS ${tag}: ${counts} of ${packages.length} packages${wait}\n`);
}
