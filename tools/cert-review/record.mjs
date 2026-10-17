#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { ciEvidence, GAPS, jobsEvidence, knownLine, NAME, verdictLine, withMissing } from './lib.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const { values } = parseArgs({
  options: {
    version: { type: 'string' },
    repo: { type: 'string', default: NAME },
    pr: { type: 'string' },
    head: { type: 'string', default: '' },
    commit: { type: 'string', default: '' },
    run: { type: 'string', default: '' },
    artifact: { type: 'string', default: '' },
    sums: { type: 'string', default: '' },
    findings: { type: 'string', default: '' },
    review: { type: 'string', default: '' },
    'review-result': { type: 'string', default: 'success' },
    jobs: { type: 'string', default: '' },
    key: { type: 'string', default: 'true' },
    'ci-wait': { type: 'string', default: '0' },
    out: { type: 'string' },
  },
});
const { version, repo, pr } = values;
const API = process.env.GITHUB_API_URL ?? 'https://api.github.com';
const headers = {
  accept: 'application/vnd.github+json',
  ...(process.env.GH_TOKEN ? { authorization: `Bearer ${process.env.GH_TOKEN}` } : {}),
};
const problems = [];
const missing = [];
const facts = {};
const one = (t) => String(t ?? '').replace(/\s+/g, ' ').trim();
const sha = (file, algo = 'sha256') => createHash(algo).update(readFileSync(file)).digest('hex');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function all(path) {
  const out = [];
  let url = `${API}/${path}${path.includes('?') ? '&' : '?'}per_page=100`;
  while (url) {
    const r = await fetch(url, { headers });
    if (!r.ok) throw new Error(`GET ${path}: HTTP ${r.status}`);
    const page = await r.json();
    out.push(...(Array.isArray(page) ? page : (page.check_runs ?? [])));
    url = /<([^>]+)>;\s*rel="next"/.exec(r.headers.get('link') ?? '')?.[1];
  }
  return out;
}

function sumsOf(text) {
  const out = {};
  for (const line of text.split('\n')) {
    const m = /^([0-9a-f]{64}) [ *]?(.+)$/.exec(line.trim());
    if (m) out[m[2]] = m[1];
  }
  return out;
}

function artifact() {
  const dir = values.artifact;
  const file = join(dir, 'manifest.json');
  if (!dir || !existsSync(file)) return missing.push('no build artifact to review');
  const m = JSON.parse(readFileSync(file, 'utf8'));
  facts.go = m.go;
  if (m.version !== version) problems.push(`the build artifact is version ${m.version}, not ${version}`);
  const release = join(dir, 'release');
  const sumsFile = join(release, 'SHA256SUMS');
  if (!existsSync(sumsFile)) return problems.push('the build artifact has no SHA256SUMS');
  facts.sums = sha(sumsFile);
  if (facts.sums !== values.sums.trim())
    problems.push(`the artifact's SHA256SUMS is sha256=${facts.sums}, the build job said ${values.sums || '(none)'}`);
  const listed = sumsOf(readFileSync(sumsFile, 'utf8'));
  const present = readdirSync(release).filter((n) => n !== 'SHA256SUMS');
  for (const name of present) {
    const got = sha(join(release, name));
    if (listed[name] !== got) problems.push(`${name} re-hashes to ${got}, SHA256SUMS says ${listed[name] ?? 'nothing'}`);
  }
  for (const name of Object.keys(listed)) if (!present.includes(name)) problems.push(`SHA256SUMS lists ${name}, the artifact lacks it`);
  facts.files = present.length;
  facts.wasm = listed['slicc-wasip1-wasm.wasm'];
  if (!facts.wasm) problems.push('the build has no slicc-wasip1-wasm.wasm');
  const pkgs = m.packages ?? {};
  facts.packages = Object.keys(pkgs).length;
  for (const [name, p] of Object.entries(pkgs)) {
    const want = name === 'sliccy' ? `7.0.0-next.${version}` : version;
    if (p.version !== want) problems.push(`${name} is staged as ${p.version}, not ${want}`);
    const tgz = join(dir, 'npm', p.file);
    if (!existsSync(tgz)) problems.push(`${name}'s tarball ${p.file} is missing`);
    else if (sha(tgz) !== p.sha256) problems.push(`${name}'s tarball re-hashes to ${sha(tgz)}, the build said ${p.sha256}`);
  }
  return undefined;
}

async function ci() {
  const until = Date.now() + Number(values['ci-wait']) * 1000;
  for (;;) {
    let runs;
    try {
      runs = await all(`repos/${repo}/commits/${values.head}/check-runs?filter=latest`);
    } catch (error) {
      return missing.push(`CI's results couldn't be read: ${one(error.message)}`);
    }
    const ev = ciEvidence(runs);
    const pending = ev.missing.some((m) => /is (queued|in_progress|waiting|pending)$|is missing|has no /.test(m));
    if (!pending || Date.now() > until) {
      facts.ci = ev.per;
      missing.push(...ev.missing);
      return undefined;
    }
    await sleep(30_000);
  }
}

function readVerdict() {
  if (values.key !== 'true') {
    missing.push('INCOMPLETE: no reviewer key');
    return undefined;
  }
  try {
    const v = JSON.parse(readFileSync(join(values.review, 'verdict.json'), 'utf8'));
    if (v?.verdict !== 'certified' && v?.verdict !== 'fail') {
      missing.push(`verdict.json says "${one(v?.verdict)}", neither certified nor fail`);
      return undefined;
    }
    return {
      verdict: v.verdict,
      known: (Array.isArray(v.known) ? v.known : []).map((k) => one(k).slice(0, 120)).filter(Boolean),
      reason: one(v.reason).slice(0, 300),
      elsewhere: (Array.isArray(v.elsewhere) ? v.elsewhere : [])
        .filter((e) => e && /^[\w.-]+\/[\w.-]+$/.test(String(e.repo ?? '')))
        .slice(0, 20)
        .map((e) => ({ repo: String(e.repo), title: one(e.title).slice(0, 200), body: String(e.body ?? '').slice(0, 4000) })),
    };
  } catch (error) {
    missing.push(`no readable verdict.json (${one(error.message)})`);
    return undefined;
  }
}

let gaps = [];
function codedFindings() {
  if (!values.findings || !existsSync(values.findings)) return missing.push('no findings.json');
  const f = JSON.parse(readFileSync(values.findings, 'utf8'));
  facts.decision = f.row?.decision;
  facts.rebuild = f.rebuild ? { match: f.rebuild.match, files: f.rebuild.files } : undefined;
  for (const x of f.findings ?? []) {
    if (x.kind === 'not-reproducible')
      problems.push(`not reproducible: a git archive of the merge commit builds different ${x.differ.join(', ')}`);
    if (x.kind === 'missing-evidence' && x.blocking) missing.push(`${x.what} has no complete result`);
  }
  gaps = [
    ...(f.findings ?? []).filter((x) => GAPS.has(x.kind)).map((x) => `#${x.pr} ${x.kind}`),
    ...(f.findings ?? []).filter((x) => x.kind === 'helper-gap').map((x) => x.what),
  ];
  return undefined;
}

async function stillOpen(entry) {
  const m = /^([\w.-]+\/[\w.-]+)#(\d+)$/.exec(entry);
  if (!m) return false;
  try {
    const r = await fetch(`${API}/repos/${m[1]}/issues/${m[2]}`, { headers });
    if (r.status === 404) {
      missing.push(`known issue ${entry} not found`);
      return false;
    }
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    return (await r.json()).state === 'open';
  } catch (error) {
    missing.push(`known issue ${entry} couldn't be read: ${one(error.message)}`);
    return false;
  }
}

async function knownList(review) {
  const listed = JSON.parse(readFileSync(join(here, 'known.json'), 'utf8')).always ?? [];
  let labelled = [];
  try {
    labelled = (await all(`repos/${repo}/issues?labels=known-issue&state=open`))
      .filter((i) => !i.pull_request)
      .map((i) => `${repo}#${i.number}`);
  } catch (error) {
    missing.push(`the known issues couldn't be read: ${one(error.message)}`);
  }
  const out = [];
  for (const e of knownLine(repo, [...labelled, ...listed, ...(review?.known ?? [])]))
    if (labelled.includes(e) || (await stillOpen(e))) out.push(e);
  return out;
}

if (!/^\d+\.\d+\.\d+-pr\.\d+\.\d+$/.test(version ?? '') || !/^\d+$/.test(pr ?? '')) {
  console.error('record: --version x.y.z-pr.N.M and --pr N are required');
  process.exit(2);
}
try {
  artifact();
} catch (error) {
  problems.push(`the build check failed: ${one(error.message).slice(0, 300)}`);
}
codedFindings();
await ci();
const review = readVerdict();
const known = await knownList(review);
const incomplete = withMissing(jobsEvidence(`${values.jobs},review=${values['review-result']}`), missing);
const ci_ = values.run ? `ci: ${values.run}` : 'ci: local';
const tag = `${NAME} PR #${pr} at ${values.head.slice(0, 12)}`;
const gap = gaps.length ? `; test gaps: ${gaps.join(', ')}` : '';
const { line, state } = verdictLine({
  tag,
  ci: ci_,
  problems,
  review,
  incomplete,
  known,
  certified: `certified ${tag}, build ${version} SHA256SUMS sha256=${facts.sums} (wasm sha256=${facts.wasm}; reproducible: ${facts.rebuild?.files ?? '?'} files from git archive; ${ci_}; known: ${known.length ? known.join(', ') : 'none'}${gap})`,
});
const comment =
  values.review && existsSync(join(values.review, 'comment.md'))
    ? readFileSync(join(values.review, 'comment.md'), 'utf8').trim()
    : '**Cert review:** the reviewer wrote no comment.';
const checks = [
  `- the reviewed build: ${version}, SHA256SUMS sha256 ${facts.sums ?? '?'} (${facts.files ?? '?'} files, ${facts.packages ?? '?'} npm packages), re-hashed here from the artifact; the build job said ${values.sums || '?'}; ${facts.go ?? '?'}`,
  `- reproducible from a git archive of the merge commit: ${facts.rebuild ? (facts.rebuild.match ? 'yes, byte for byte' : 'NO') : '?'}`,
  `- the NEGATIVE: ${facts.decision ?? '?'}`,
  `- CI at the head: ${facts.ci ? Object.entries(facts.ci).map(([os, legs]) => `${os} ${legs.length ? legs.map((l) => l.split('=')[1]).join('/') : 'none'}`).join(', ') : '?'}`,
  `- pull request #${pr}, head ${values.head || '?'}, reviewed as merged onto main (${values.commit || '?'})`,
].join('\n');
const elsewhere = review?.elsewhere?.length
  ? `\n\n**For the cert lane to file elsewhere:**\n${review.elsewhere.map((e) => `- ${e.repo}: ${e.title}\n\n${e.body.replace(/^/gm, '  > ')}`).join('\n')}`
  : '';
const trouble = [...problems, ...missing].length
  ? `\n\n**Problems:**\n${[...problems, ...missing].map((p) => `- ${p}`).join('\n')}`
  : '';
const body = `<!-- cert-review -->\n${comment}\n\n**Checked by record, in code:**\n${checks}\n\n\`\`\`\n${line}\n\`\`\`${elsewhere}${trouble}\n<!-- /cert-review -->\n`;
mkdirSync(values.out, { recursive: true });
writeFileSync(join(values.out, 'line.txt'), `${line}\n`);
writeFileSync(join(values.out, 'body.md'), body);
writeFileSync(
  join(values.out, 'record.json'),
  `${JSON.stringify({ name: NAME, version, pr, head: values.head, commit: values.commit, sums: facts.sums ?? null, state, certified: state === 'success', line, facts, problems, missing, review, known }, null, 2)}\n`
);
console.log(line);
for (const p of problems) console.error(`problem: ${p}`);
for (const p of missing) console.error(`missing: ${p}`);
process.exit(state === 'success' ? 0 : 1);
