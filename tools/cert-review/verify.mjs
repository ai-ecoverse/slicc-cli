#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { appendFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { parseArgs } from 'node:util';
import {
  ASSETS,
  artifactBinding,
  declaredArtifacts,
  groupVerdict,
  mergedPrs,
  provenanceProblems,
  releaseNotes,
  releasePackages,
  sumsOf,
} from './lib.mjs';

const { values } = parseArgs({
  options: {
    version: { type: 'string' },
    repo: { type: 'string', default: 'ai-ecoverse/slicc-cli' },
    run: { type: 'string', default: '' },
    wait: { type: 'string', default: '3600' },
    lines: { type: 'string', default: '' },
    rebuild: { type: 'string', default: '' },
    darwin: { type: 'string', default: '' },
    artifacts: { type: 'string', default: '' },
    installer: { type: 'string', default: 'https://get.sliccy.ai/slicc' },
    out: { type: 'string', default: '' },
  },
});
const { version, repo } = values;
const REGISTRY = new URL(process.env.CERT_NPM_REGISTRY || 'https://registry.npmjs.org').origin;
const problems = [];
const missing = [];
const absent = [];
const facts = { packages: {}, assets: {} };
const one = (t) => String(t ?? '').replace(/\s+/g, ' ').trim();
const sh = (cmd, args, opts = {}) =>
  execFileSync(cmd, args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 256 * 1024 * 1024, ...opts });
const gh = (path) => JSON.parse(sh('gh', ['api', path]));
const ghAll = (path) =>
  JSON.parse(sh('gh', ['api', '--paginate', '--slurp', path])).flatMap((p) => (Array.isArray(p) ? p : (p.commits ?? p.artifacts ?? [])));
const sha = (bytes, algo = 'sha256', enc = 'hex') => createHash(algo).update(bytes).digest(enc);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const summary = (text) => {
  console.log(text);
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `${text}\n`);
};
const work = mkdtempSync(join(process.env.RUNNER_TEMP ?? tmpdir(), 'cert-verify-'));

function npmView(spec) {
  try {
    return JSON.parse(sh('npm', ['view', spec, '--json', '--prefer-online']));
  } catch (error) {
    if (/E404|No match found/.test(`${error.stdout ?? ''}${error.stderr ?? ''}`)) return undefined;
    throw error;
  }
}

async function served(p, until) {
  for (let waited = false; ; waited = true) {
    const m = npmView(`${p.name}@${p.version}`);
    if (m?.version === p.version && m.dist?.tarball && (await fetch(m.dist.tarball, { method: 'HEAD' })).ok) return m;
    if (Date.now() > until) return undefined;
    if (!waited) console.log(`waiting for npm to serve ${p.name}@${p.version}`);
    await sleep(Math.min(15_000, Math.max(0, until - Date.now()) + 1));
  }
}

function tagCommit() {
  let ref = gh(`repos/${repo}/git/ref/tags/v${version}`).object;
  if (ref.type === 'tag') ref = gh(`repos/${repo}/git/tags/${ref.sha}`).object;
  facts.commit = ref.sha;
  const status = gh(`repos/${repo}/compare/main...${ref.sha}`).status;
  facts.onMain = status;
  if (status !== 'identical' && status !== 'behind') problems.push(`v${version} (${ref.sha}) is ${status} of main`);
}

async function npmPackage(p, until, sums) {
  const m = await served(p, until);
  if (!m) {
    absent.push(p);
    return missing.push(`${p.name}@${p.version} isn't on npm after ${values.wait} s`);
  }
  const f = { tarball: m.dist.tarball };
  facts.packages[p.name] = f;
  const url = new URL(m.dist.tarball);
  if (url.origin !== REGISTRY) return problems.push(`${p.name}: the tarball is at ${url.origin}`);
  const res = await fetch(url);
  if (!res.ok) return problems.push(`${p.name}: downloading the tarball answered ${res.status}`);
  const bytes = Buffer.from(await res.arrayBuffer());
  f.sha256 = sha(bytes);
  f.sha512 = sha(bytes, 'sha512');
  const integrity = `sha512-${sha(bytes, 'sha512', 'base64')}`;
  if (m.dist.integrity !== integrity) problems.push(`${p.name}: dist.integrity is ${m.dist.integrity}, the tarball is ${integrity}`);
  if (m.gitHead !== facts.commit) problems.push(`${p.name}: gitHead ${m.gitHead} isn't v${version}'s commit ${facts.commit}`);
  const att = m.dist.attestations?.url;
  if (!att) problems.push(`${p.name}: no attestations on npm`);
  else {
    const bundle = ((await (await fetch(att)).json()).attestations ?? []).find((b) => b.predicateType === 'https://slsa.dev/provenance/v1');
    if (!bundle) problems.push(`${p.name}: no SLSA provenance attestation`);
    else {
      const statement = JSON.parse(Buffer.from(bundle.bundle.dsseEnvelope.payload, 'base64').toString('utf8'));
      const found = provenanceProblems(statement, { repo, name: p.name, version: p.version, sha512: f.sha512, commit: facts.commit });
      f.provenance = found.facts;
      problems.push(...found.problems);
    }
  }
  const dir = join(work, 'npm', p.name.replace('/', '__'));
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'p.tgz'), bytes);
  sh('tar', ['-xzf', join(dir, 'p.tgz'), '-C', dir]);
  const pkg = JSON.parse(readFileSync(join(dir, 'package', 'package.json'), 'utf8'));
  if (pkg.name !== p.name || pkg.version !== p.version) problems.push(`${p.name}: the tarball says ${pkg.name}@${pkg.version}`);
  if (p.asset) {
    const bin = p.asset.endsWith('.wasm') ? 'bin/slicc.wasm' : p.asset.endsWith('.exe') ? 'bin/slicc.exe' : 'bin/slicc';
    const file = join(dir, 'package', bin);
    const got = existsSync(file) ? sha(readFileSync(file)) : 'nothing';
    if (got !== sums[p.asset]) problems.push(`${p.name}'s ${bin} is ${got}, the release's ${p.asset} is ${sums[p.asset]}`);
  }
  if (p.name === 'sliccy' || p.name === '@ai-ecoverse/slicc') {
    const deps = pkg.optionalDependencies ?? {};
    for (const q of releasePackages(version).filter((x) => x.asset && !x.asset.endsWith('.wasm')))
      if (deps[q.name] !== version) problems.push(`${p.name} depends on ${q.name}@${deps[q.name] ?? 'nothing'}, not ${version}`);
  }
  return undefined;
}

function signatures(packages) {
  const dir = join(work, 'audit');
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'package.json'), '{"private":true}\n');
  sh('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', '--force', ...packages.map((p) => `${p.name}@${p.version}`)], { cwd: dir });
  let out;
  try {
    out = sh('npm', ['audit', 'signatures'], { cwd: dir });
  } catch (error) {
    out = `${error.stdout ?? ''}${error.stderr ?? ''}`;
    problems.push(`npm audit signatures failed: ${one(out).slice(0, 300)}`);
  }
  facts.signatures = one(out);
  if (!/verified attestation/.test(out)) problems.push('npm audit signatures verified no attestation');
}

function githubRelease() {
  const release = gh(`repos/${repo}/releases/tags/v${version}`);
  facts.release = release.html_url;
  const names = release.assets.map((a) => a.name).sort();
  const want = [...ASSETS, 'SHA256SUMS'].sort();
  if (names.join(',') !== want.join(',')) problems.push(`v${version}'s assets are ${names.join(', ')}, want ${want.join(', ')}`);
  const dir = join(work, 'release');
  mkdirSync(dir, { recursive: true });
  sh('gh', ['release', 'download', `v${version}`, '--repo', repo, '--dir', dir, '--clobber']);
  const text = readFileSync(join(dir, 'SHA256SUMS'), 'utf8');
  const sums = sumsOf(text);
  facts.sums = sha(Buffer.from(text));
  if (Object.keys(sums).sort().join(',') !== [...ASSETS].sort().join(','))
    problems.push(`SHA256SUMS lists ${Object.keys(sums).join(', ')}`);
  for (const name of ASSETS) {
    const file = join(dir, name);
    if (!existsSync(file)) continue;
    const got = sha(readFileSync(file));
    facts.assets[name] = got;
    if (got !== sums[name]) problems.push(`the release's ${name} is ${got}, SHA256SUMS says ${sums[name]}`);
    try {
      sh('gh', ['attestation', 'verify', file, '--repo', repo, '--signer-workflow', `${repo}/.github/workflows/release.yml`, '--source-ref', 'refs/heads/main', '--deny-self-hosted-runners']);
    } catch (error) {
      problems.push(`gh attestation verify ${name}: ${one(error.stderr || error.message).slice(0, 200)}`);
    }
  }
  return { sums, text };
}

async function installer(sums) {
  const first = await fetch(values.installer, { redirect: 'manual' });
  const to = first.headers.get('location') ?? '';
  facts.installer = `${values.installer} → ${to}`;
  const latest = `https://github.com/${repo}/releases/latest/download/install.sh`;
  if (first.status < 300 || first.status > 399 || to !== latest)
    return problems.push(`${values.installer} answers ${first.status} → ${to || 'nothing'}, not ${latest}`);
  const now = gh(`repos/${repo}/releases/latest`).tag_name;
  if (now !== `v${version}`) {
    facts.installer += ` (latest is now ${now}, so this release's install.sh is checked as an asset only)`;
    return undefined;
  }
  const res = await fetch(latest);
  const got = sha(Buffer.from(await res.arrayBuffer()));
  if (got !== sums['install.sh']) problems.push(`${latest} serves ${got}, SHA256SUMS says ${sums['install.sh']}`);
  return undefined;
}

function bound() {
  try {
    const since = gh(`repos/${repo}/actions/runs/${values.run}`).run_started_at;
    const listed = ghAll(`repos/${repo}/actions/runs/${values.run}/artifacts`);
    missing.push(...artifactBinding({ declared: declaredArtifacts(values.artifacts), listed, since }));
  } catch (error) {
    missing.push(`the run's artifacts couldn't be listed: ${one(error.stderr || error.message).slice(0, 200)}`);
  }
}

function evidence() {
  const read = (file) => {
    try {
      return JSON.parse(readFileSync(file, 'utf8'));
    } catch {
      return undefined;
    }
  };
  const rebuild = read(values.rebuild);
  facts.rebuild = rebuild;
  if (!rebuild) missing.push('no rebuild from the tag');
  else if (rebuild.sums !== facts.sums && rebuild.releaseSums !== facts.sums)
    missing.push("the rebuild compared another SHA256SUMS than the release's");
  else if (!rebuild.match)
    problems.push(`not reproducible: a git archive of v${version} builds different ${rebuild.differ.join(', ')}`);
  const darwin = read(values.darwin);
  facts.darwin = darwin;
  if (!darwin) missing.push('no darwin signature check');
  else
    for (const b of darwin.binaries ?? []) {
      if (b.sha256 !== facts.assets[b.asset]) missing.push(`the darwin check read another ${b.asset} (${b.sha256})`);
      else if (b.status === 'FAIL') problems.push(`${b.asset}: ${b.why}`);
      else if (b.status !== 'PASS') missing.push(`${b.asset}: ${b.why || 'no complete signature check'}`);
    }
  if (darwin && (darwin.binaries ?? []).length !== 2) missing.push('the darwin check covers not both darwin binaries');
}

function context() {
  const tags = ghAll(`repos/${repo}/tags`).map((t) => t.name).filter((t) => /^v\d+\.\d+\.\d+$/.test(t));
  const older = tags
    .map((t) => t.slice(1).split('.').map(Number))
    .filter((v) => {
      const [a, b, c] = version.split('.').map(Number);
      return v[0] < a || (v[0] === a && (v[1] < b || (v[1] === b && v[2] < c)));
    })
    .sort((x, y) => y[0] - x[0] || y[1] - x[1] || y[2] - x[2]);
  const since = older[0]?.join('.');
  const prs = [];
  if (since) {
    for (const n of mergedPrs(ghAll(`repos/${repo}/compare/v${since}...v${version}`))) {
      const pull = gh(`repos/${repo}/pulls/${n}`);
      const statuses = ghAll(`repos/${repo}/commits/${pull.head.sha}/statuses`);
      const found = statuses.find((s) => s.context === 'cert-review') ?? statuses.find((s) => s.context === 'cert-review-pr');
      prs.push({ pr: n, head: pull.head.sha, state: found?.state, description: found?.description });
    }
  }
  const verdict = groupVerdict(prs);
  facts.prAll = verdict.state === 'success';
  facts.prCertified = verdict.state === 'success' ? verdict.line : verdict.line.replace(/^not certified: /, 'not all: ');
  facts.known = ghAll(`repos/${repo}/issues?labels=known-issue&state=open`)
    .filter((i) => !i.pull_request)
    .map((i) => `${repo}#${i.number}`);
}

if (!/^\d+\.\d+\.\d+$/.test(version ?? '')) {
  console.error('verify: --version must be x.y.z');
  process.exit(2);
}
const packages = releasePackages(version).filter((p) => process.env[p.gate] === 'true' || process.env[p.gate] === undefined);
const skipped = releasePackages(version).filter((p) => !packages.some((q) => q.name === p.name));
bound();
try {
  tagCommit();
  const { sums } = githubRelease();
  evidence();
  const until = Date.now() + Number(values.wait) * 1000;
  for (const p of packages) await npmPackage(p, until, sums);
  const present = packages.filter((p) => !absent.includes(p));
  if (present.length) signatures(present);
  await installer(sums);
  context();
} catch (error) {
  missing.push(`a check didn't finish: ${one(error.stderr || error.message).slice(0, 300)}`);
}
rmSync(work, { recursive: true, force: true });

const ci = `${process.env.GITHUB_SERVER_URL ?? 'https://github.com'}/${repo}/actions/runs/${values.run}`;
const record = (state, extra = {}) => {
  if (values.out) writeFileSync(values.out, `${JSON.stringify({ version, state, problems, missing, facts, ...extra }, null, 2)}\n`);
};
const fail = (state, line) => {
  summary(line);
  record(state, { line });
  console.log(`::error title=verify on npm::${line}`);
  process.exit(1);
};
if (problems.length)
  fail('failure', `supply-chain alert: slicc-cli v${version} doesn't verify: ${problems.join('; ')}${missing.length ? `; also missing: ${missing.join('; ')}` : ''} (ci: ${ci})`);
const redo = absent.length
  ? `publish ${absent.map((p) => `${p.name}@${p.version}`).join(', ')}, then dispatch cert-verify with version=${version}`
  : 're-run the failed jobs';
if (missing.length) fail('error', `cert-ci: INCOMPLETE slicc-cli v${version} (${missing.join('; ')}; ${redo}) (ci: ${ci})`);
const tail = `ci: ${ci}; pr-certified: ${facts.prCertified}; known: ${facts.known.length ? facts.known.join(', ') : 'none'}`;
const word = facts.prAll ? 'certified' : 'verified';
const lines = [
  ...packages.map((p) => ({
    prefix: `${p.name}@${p.version}`,
    sha256: facts.packages[p.name].sha256,
    line: `${word} ${p.name}@${p.version} sha256=${facts.packages[p.name].sha256} (covers ${p.version}; ${tail})`,
  })),
  {
    prefix: `slicc-cli@${version} binaries SHA256SUMS`,
    sha256: facts.sums,
    line: `${word} slicc-cli@${version} binaries SHA256SUMS sha256=${facts.sums} (reproducible: ${facts.rebuild.files} files from a git archive of the tag; darwin: Developer ID ${facts.darwin.team}, notarized; attested by release.yml@refs/heads/main; installer: ${values.installer}; ${tail}${skipped.length ? `; not published: ${skipped.map((p) => p.name).join(', ')}` : ''})`,
  },
];
let notes;
try {
  notes = releaseNotes(gh(`repos/${repo}/releases/tags/v${version}`).body, lines);
} catch (error) {
  fail('error', `cert-ci: INCOMPLETE slicc-cli v${version} (the release notes couldn't be read: ${one(error.stderr || error.message).slice(0, 200)}; re-run the failed jobs) (ci: ${ci})`);
}
if (notes.conflict) fail('failure', `supply-chain alert: slicc-cli v${version}: ${notes.conflict}, but it verifies as another (ci: ${ci})`);
for (const l of lines) summary(l.line);
summary(notes.unchanged ? `the release notes of v${version} already carry these lines` : `the release notes of v${version} get these lines in the next job`);
if (values.lines) writeFileSync(values.lines, `${JSON.stringify(lines, null, 2)}\n`);
record('success', { lines: lines.map((l) => l.line), notes: notes.unchanged ? 'unchanged' : 'to write' });
