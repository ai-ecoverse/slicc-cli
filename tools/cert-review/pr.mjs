#!/usr/bin/env node
import { appendFileSync, existsSync, readFileSync } from 'node:fs';
import { parseArgs } from 'node:util';
import { artifactBinding, declaredArtifacts, forcedFiles, groupVerdict, mergedPrs, outputDiff, reviewsItself } from './lib.mjs';

const { positionals, values } = parseArgs({
  allowPositionals: true,
  options: {
    repo: { type: 'string', default: 'ai-ecoverse/slicc-cli' },
    pr: { type: 'string', default: '' },
    head: { type: 'string', default: '' },
    action: { type: 'string', default: '' },
    label: { type: 'string', default: '' },
    labels: { type: 'string', default: '' },
    base: { type: 'string', default: '' },
    run: { type: 'string', default: '' },
    context: { type: 'string', default: 'cert-review' },
    guard: { type: 'string', default: '' },
    approval: { type: 'string', default: 'cert-review-approved' },
    forced: { type: 'string', default: '' },
    artifacts: { type: 'string' },
    'base-manifest': { type: 'string', default: '' },
    'candidate-manifest': { type: 'string', default: '' },
  },
});
const { repo, context } = values;
const API = process.env.GITHUB_API_URL ?? 'https://api.github.com';
const headers = {
  accept: 'application/vnd.github+json',
  ...(process.env.GH_TOKEN ? { authorization: `Bearer ${process.env.GH_TOKEN}` } : {}),
};
const RUN_URL = `${process.env.GITHUB_SERVER_URL ?? 'https://github.com'}/${repo}/actions/runs/${values.run}`;

async function request(url, init = {}) {
  const r = await fetch(url.startsWith('http') ? url : `${API}/${url}`, {
    ...init,
    headers: { ...headers, ...init.headers },
  });
  if (!r.ok) throw new Error(`${init.method ?? 'GET'} ${url}: HTTP ${r.status}`);
  return r;
}

async function all(path) {
  const out = [];
  let url = `${API}/${path}${path.includes('?') ? '&' : '?'}per_page=100`;
  while (url) {
    const r = await request(url);
    const page = await r.json();
    out.push(...(Array.isArray(page) ? page : (page.commits ?? page.artifacts ?? [])));
    url = /<([^>]+)>;\s*rel="next"/.exec(r.headers.get('link') ?? '')?.[1];
  }
  return out;
}

const output = (pairs) => {
  const text = Object.entries(pairs)
    .map(([k, v]) => `${k}=${v}`)
    .join('\n');
  console.log(text);
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `${text}\n`);
};
const summary = (text) => {
  console.log(text);
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `${text}\n`);
};

async function status(sha, state, description) {
  await request(`repos/${repo}/statuses/${sha}`, {
    method: 'POST',
    body: JSON.stringify({ state, context, target_url: RUN_URL, description: description.slice(0, 140) }),
  });
  console.log(`${context} ${state} on ${sha}: ${description}`);
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, 'posted=true\n');
}

const GUARD_FAIL = (label) => `reviewer change: needs coordinator review (${label} on this head)`;
const GUARD_INCOMPLETE = 'the certification self-guard did not finish: re-run the failed jobs';

async function dangling() {
  try {
    await settle();
  } catch (error) {
    console.log(`couldn't check the head's ${context} status: ${error.message}`);
  }
}

async function settle() {
  const statuses = await all(`repos/${repo}/commits/${values.head}/statuses`);
  const latest = statuses.find((s) => s.context === context);
  if (latest?.state !== 'pending') return;
  const run = /\/actions\/runs\/(\d+)/.exec(latest.target_url ?? '')?.[1];
  if (run === values.run) return;
  let done = true;
  if (run) {
    try {
      done = (await (await request(`repos/${repo}/actions/runs/${run}`)).json()).status === 'completed';
    } catch {
      done = true;
    }
  }
  if (done) await status(values.head, 'error', `cert-review's run ${run ?? '?'} ended without a result: re-run the failed jobs`);
}

async function plan() {
  const { action, label, approval } = values;
  const starts = action === 'labeled' ? ['merge-ok', approval] : action === 'unlabeled' ? [approval] : [];
  if (action === 'ready_for_review' || ((action === 'labeled' || action === 'unlabeled') && !starts.includes(label))) {
    summary(`#${values.pr}: ${action} ${label} doesn't start or change a review`);
    await dangling();
    return output({ build: 'false', forced: '', self: 'false' });
  }
  const files = await all(`repos/${repo}/pulls/${values.pr}/files`);
  const forced = forcedFiles(files);
  const self = reviewsItself(files);
  if (values.guard !== 'PASS') {
    const fail = values.guard === 'FAIL';
    await status(values.head, fail ? 'failure' : 'error', fail ? GUARD_FAIL(approval) : GUARD_INCOMPLETE);
    summary(`#${values.pr}: the certification self-guard says ${values.guard || 'nothing'}: not reviewed`);
    return output({ build: 'false', forced: forced.join(','), self: String(self) });
  }
  if (label === approval && !self)
    summary(`#${values.pr} doesn't change the reviewer: ${action} ${label} may have cancelled a run, so it plans again`);
  await status(values.head, 'pending', `building #${values.pr} at ${values.head.slice(0, 12)} and main, to compare what ships`);
  return output({ build: 'true', forced: forced.join(','), self: String(self) });
}

function readManifest(file) {
  if (!file || !existsSync(file)) return undefined;
  try {
    const m = JSON.parse(readFileSync(file, 'utf8'));
    return m.problems?.length ? undefined : m;
  } catch {
    return undefined;
  }
}

async function bound() {
  if (values.artifacts === undefined) return [];
  try {
    const run = await (await request(`repos/${repo}/actions/runs/${values.run}`)).json();
    const listed = await all(`repos/${repo}/actions/runs/${values.run}/artifacts`);
    return artifactBinding({ declared: declaredArtifacts(values.artifacts), listed, since: run.run_started_at });
  } catch (error) {
    return [`the run's artifacts couldn't be listed: ${error.message}`];
  }
}

async function classify() {
  const unbound = await bound();
  if (unbound.length) {
    await status(values.head, 'error', `INCOMPLETE: ${unbound.join('; ')}`);
    summary(`#${values.pr}: the builds aren't their producers' artifacts: ${unbound.join('; ')}`);
    process.exit(1);
  }
  const base = readManifest(values['base-manifest']);
  const candidate = readManifest(values['candidate-manifest']);
  const forced = values.forced.split(',').filter(Boolean);
  if (!base || !candidate) {
    await status(values.head, 'error', `cert-review couldn't build ${!candidate ? 'the pull request' : 'main'}: re-run the failed jobs`);
    summary(`#${values.pr}: no ${!candidate ? 'candidate' : 'base'} build to compare: error`);
    process.exit(1);
  }
  const differ = outputDiff(base, candidate);
  const behaviour = [...forced, ...differ];
  const labelled = values.labels.split(',').includes('merge-ok');
  summary(`#${values.pr}: ${differ.length ? `ships differently: ${differ.join(', ')}` : 'identical build outputs'}${forced.length ? `; behaviour by path: ${forced.join(', ')}` : ''}`);
  if (behaviour.length === 0) {
    const n = Object.keys(candidate.files).length + Object.keys(candidate.packages).length;
    await status(values.head, 'success', `not behaviour: identical build outputs (${n} files and packages, SHA256SUMS ${candidate.sums.slice(0, 12)})`);
    return output({ go: 'false', behaviour: '' });
  }
  if (!labelled) {
    await status(values.head, 'failure', `waits for merge-ok to be reviewed (changes ${behaviour.slice(0, 4).join(', ')}${behaviour.length > 4 ? ', …' : ''})`);
    return output({ go: 'false', behaviour: behaviour.join(',') });
  }
  await status(values.head, 'pending', `reviewing #${values.pr} at ${values.head.slice(0, 12)}`);
  return output({ go: 'true', behaviour: behaviour.join(',') });
}

async function lookup() {
  if (values.guard !== 'PASS') {
    const fail = values.guard === 'FAIL';
    const line = fail
      ? `not certified: a pull request in the group changes the reviewer without ${values.approval} on its head`
      : GUARD_INCOMPLETE;
    await status(values.head, fail ? 'failure' : 'error', line);
    console.log(`::error title=not certified::${line}`);
    process.exit(1);
  }
  const commits = await all(`repos/${repo}/compare/${values.base}...${values.head}`);
  const prs = [];
  for (const n of mergedPrs(commits)) {
    const pull = await (await request(`repos/${repo}/pulls/${n}`)).json();
    const statuses = await all(`repos/${repo}/commits/${pull.head.sha}/statuses`);
    const found = statuses.find((s) => s.context === context);
    prs.push({ pr: n, head: pull.head.sha, state: found?.state, description: found?.description });
  }
  if (prs.length === 0) {
    const line = 'not certified: no pull request found in the group';
    await status(values.head, 'failure', line);
    console.log(`::error title=not certified::${line}`);
    process.exit(1);
  }
  const verdict = groupVerdict(prs);
  for (const l of verdict.lines) summary(`- ${l}`);
  await status(values.head, verdict.state, verdict.line);
  if (verdict.state !== 'success') {
    console.log(`::error title=not certified::${verdict.line}`);
    process.exit(1);
  }
}

if (positionals[0] === 'plan') await plan();
else if (positionals[0] === 'classify') await classify();
else if (positionals[0] === 'lookup') await lookup();
else {
  console.error('pr.mjs plan|classify|lookup');
  process.exit(2);
}
