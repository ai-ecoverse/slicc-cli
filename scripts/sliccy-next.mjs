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

function run(script, args, env = process.env) {
  return spawnSync(process.execPath, [join(root, 'scripts', script), ...args], { stdio: 'inherit', env }).status ?? 1;
}

function node(script, args) {
  const status = run(script, args);
  if (status !== 0) process.exit(status);
}

const tags = git(['tag', '--merged', 'HEAD', '--list', 'v[0-9]*', '--sort=-v:refname'])
  .split('\n')
  .map((t) => t.trim())
  .filter((t) => /^v[0-9]+\.[0-9]+\.[0-9]+$/.test(t));
if (!tags.length) fail('no release tag (vX.Y.Z) is reachable from HEAD');
const dryRun = values['dry-run'];
const head = git(['rev-parse', 'HEAD']).trim();
const tagged = git(['rev-parse', `${tags[0]}^{commit}`]).trim();
if (head !== tagged && !dryRun) fail(`HEAD ${head} isn't ${tags[0]}'s commit ${tagged}; sliccy@next for ${tags[0]} is published from that commit only, or its provenance never matches the tag`);

function stage(tag) {
  const version = tag.slice(1);
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
      '--companions',
    ]);
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}

const candidates = dryRun ? tags.slice(0, 2) : tags.slice(0, 1);
for (const [i, tag] of candidates.entries()) {
  const version = tag.slice(1);
  const fallback = candidates[i + 1];
  process.stdout.write(`sliccy-next: ${i ? 'checking' : 'the latest release is'} ${tag}; staging sliccy@7.0.0-next.${version} from its launcher\n`);
  stage(tag);
  const env = fallback ? { ...process.env, SLICC_REGISTRY_WAIT_SECONDS: '0' } : process.env;
  const status = run('npm-publish.mjs', ['--dir', values.out, '--tag', 'next', '--require-registry', ...(dryRun ? ['--dry-run'] : [])], env);
  if (status === 3 && fallback) {
    process.stdout.write(`sliccy-next: npm doesn't serve every platform package of ${tag} yet, so its release is still publishing; the dry run checks ${fallback} instead\n`);
    continue;
  }
  if (status !== 0) process.exit(status);
  process.stdout.write(`sliccy-next: PASS sliccy@7.0.0-next.${version}${dryRun ? ' (dry run)' : ''}\n`);
  break;
}
