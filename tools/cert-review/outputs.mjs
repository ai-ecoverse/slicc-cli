#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  writeFileSync,
} from 'node:fs';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { outputDiff, reproducedFrom } from './lib.mjs';

const { positionals, values } = parseArgs({
  allowPositionals: true,
  options: {
    tree: { type: 'string', default: '.' },
    version: { type: 'string', default: '' },
    commit: { type: 'string', default: '' },
    out: { type: 'string', default: '' },
    'no-npm': { type: 'boolean', default: false },
    base: { type: 'string', default: '' },
    candidate: { type: 'string', default: '' },
    'files-only': { type: 'boolean', default: false },
    release: { type: 'string', default: '' },
    rebuilt: { type: 'string', default: '' },
  },
});

const sha = (file, algo = 'sha256', enc = 'hex') =>
  createHash(algo).update(readFileSync(file)).digest(enc);

function sums(text) {
  const out = {};
  for (const line of text.split('\n')) {
    const m = /^([0-9a-f]{64}) [ *]?(.+)$/.exec(line.trim());
    if (m) out[m[2]] = m[1];
  }
  return out;
}

function manifest() {
  const tree = resolve(values.tree);
  const out = resolve(values.out);
  const release = join(tree, 'artifacts', 'release');
  const listed = sums(readFileSync(join(release, 'SHA256SUMS'), 'utf8'));
  const files = {};
  const problems = [];
  mkdirSync(join(out, 'release'), { recursive: true });
  for (const name of readdirSync(release).sort()) {
    files[name] = sha(join(release, name));
    copyFileSync(join(release, name), join(out, 'release', name));
    if (name !== 'SHA256SUMS' && listed[name] !== files[name])
      problems.push(`${name} is ${files[name]}, SHA256SUMS says ${listed[name] ?? 'nothing'}`);
  }
  for (const name of Object.keys(listed))
    if (!(name in files)) problems.push(`SHA256SUMS lists ${name}, which wasn't built`);
  const packages = {};
  if (!values['no-npm']) {
    mkdirSync(join(out, 'npm'), { recursive: true });
    for (const dir of ['dist/npm', 'dist/npm-next']) {
      const root = join(tree, dir);
      if (!existsSync(root)) continue;
      for (const entry of readdirSync(root).sort()) {
        const pkgDir = join(root, entry);
        const pkg = JSON.parse(readFileSync(join(pkgDir, 'package.json'), 'utf8'));
        const made = JSON.parse(
          execFileSync(
            'npm',
            ['pack', pkgDir, '--pack-destination', join(out, 'npm'), '--ignore-scripts', '--json'],
            { encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] }
          )
        )[0];
        const file = join(out, 'npm', made.filename);
        packages[pkg.name] = {
          version: pkg.version,
          file: made.filename,
          sha256: sha(file),
          integrity: `sha512-${sha(file, 'sha512', 'base64')}`,
          dependencies: pkg.dependencies ?? {},
          optionalDependencies: pkg.optionalDependencies ?? {},
        };
      }
    }
  }
  const facts = {
    version: values.version,
    commit: values.commit,
    go: execFileSync('go', ['version'], { cwd: tree, encoding: 'utf8' }).trim(),
    sums: files.SHA256SUMS,
    files,
    packages,
    problems,
  };
  writeFileSync(join(out, 'manifest.json'), `${JSON.stringify(facts, null, 2)}\n`);
  console.log(
    `${Object.keys(files).length} release files, ${Object.keys(packages).length} npm packages, SHA256SUMS sha256=${facts.sums}`
  );
  for (const p of problems) console.error(`problem: ${p}`);
  process.exit(problems.length ? 1 : 0);
}

function diff() {
  const read = (f) => JSON.parse(readFileSync(f, 'utf8'));
  const base = read(values.base);
  const candidate = read(values.candidate);
  const strip = (m) => (values['files-only'] ? { ...m, packages: {} } : m);
  const differ = outputDiff(strip(base), strip(candidate));
  const result = {
    match: differ.length === 0,
    differ,
    files: Object.keys(candidate.files ?? {}).length,
    base: { go: base.go, commit: base.commit, sums: base.sums },
    candidate: { go: candidate.go, commit: candidate.commit, sums: candidate.sums },
  };
  const text = `${JSON.stringify(result, null, 2)}\n`;
  if (values.out) writeFileSync(values.out, text);
  console.log(result.match ? 'identical outputs' : `different: ${differ.join(', ')}`);
}

function reproduce() {
  const release = readFileSync(values.release, 'utf8');
  const rebuilt = readFileSync(values.rebuilt, 'utf8');
  const result = {
    ...reproducedFrom(release, rebuilt),
    releaseSums: sha(values.release),
    rebuiltSums: sha(values.rebuilt),
    go: execFileSync('go', ['version'], { cwd: resolve(values.tree), encoding: 'utf8' }).trim(),
  };
  writeFileSync(values.out, `${JSON.stringify(result, null, 2)}\n`);
  console.log(result.match ? `reproduced ${result.files} files byte for byte (darwin is signed: ${result.signed.join(', ')})` : `different: ${result.differ.join(', ')}`);
}

if (positionals[0] === 'manifest') manifest();
else if (positionals[0] === 'reproduce') reproduce();
else if (positionals[0] === 'diff') diff();
else {
  console.error('outputs.mjs manifest|diff|reproduce');
  process.exit(2);
}
