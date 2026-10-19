#!/usr/bin/env node
import { isMain } from './is-main.mjs';

export const LABEL = 'nightly-red';
const BAD = new Set(['failure', 'cancelled', 'timed_out', 'startup_failure', 'action_required', 'stale']);

function client() {
  const api = (process.env.GITHUB_API_URL || 'https://api.github.com').replace(/\/$/, '');
  const repo = process.env.GITHUB_REPOSITORY;
  if (!repo) throw new Error('GITHUB_REPOSITORY is not set');
  const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN;
  return async function call(method, path, body) {
    const headers = { Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28' };
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    const url = `${api}/repos/${repo}${path}`;
    const res = await fetch(url, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    if (method === 'GET' && res.status === 404) return null;
    if (!res.ok) throw new Error(`${method} ${url}: HTTP ${res.status}`);
    return res.status === 204 ? {} : res.json();
  };
}

async function failedJobs(call, runId) {
  try {
    const body = await call('GET', `/actions/runs/${runId}/jobs?filter=latest&per_page=100`);
    const jobs = (body?.jobs || []).filter((j) => BAD.has(j.conclusion));
    if (!jobs.length) return '- none of the jobs reported a failure; see the run';
    return jobs.map((j) => `- [${j.name}](${j.html_url}): ${j.conclusion}`).join('\n');
  } catch (error) {
    return `- could not list the jobs: ${error.message}`;
  }
}

export async function report(env = process.env) {
  const call = client();
  const repo = env.GITHUB_REPOSITORY;
  const server = (env.GITHUB_SERVER_URL || 'https://github.com').replace(/\/$/, '');
  const runId = env.GITHUB_RUN_ID;
  const sha = env.GITHUB_SHA;
  const result = env.RESULT || '';
  if (!runId || !sha) throw new Error('GITHUB_RUN_ID and GITHUB_SHA must be set');
  const run = `${server}/${repo}/actions/runs/${runId}`;
  const commit = `${server}/${repo}/commit/${sha}`;
  const listed = (await call('GET', `/issues?labels=${LABEL}&state=open&per_page=100`)) || [];
  const open = listed.filter((i) => !i.pull_request).sort((a, b) => a.number - b.number);
  if (result === 'success') {
    for (const issue of open) {
      await call('POST', `/issues/${issue.number}/comments`, { body: `Nightly [run ${runId}](${run}) passed on ${sha}; closing. Releases are no longer blocked by the nightly.` });
      await call('PATCH', `/issues/${issue.number}`, { state: 'closed', state_reason: 'completed' });
    }
    return open.length ? `green; closed ${open.map((i) => `#${i.number}`).join(', ')}` : `green; no open ${LABEL} issue`;
  }
  const body = [
    `The nightly full matrix ended **${result || 'unknown'}**.`,
    '',
    `- Run: ${run}`,
    `- Commit: [${sha}](${commit})`,
    '',
    'Failed jobs:',
    await failedJobs(call, runId),
    '',
    `Releases are refused until a nightly on main passes. Fix main, run the Nightly workflow (\`gh workflow run nightly.yml --ref main\`), and once it passes the release runs by itself (or run it: \`gh workflow run release.yml --ref main\`). A green nightly closes this issue.`,
  ].join('\n');
  if (open.length) {
    await call('POST', `/issues/${open[0].number}/comments`, { body });
    return `red; commented on #${open[0].number}`;
  }
  if (!(await call('GET', `/labels/${LABEL}`))) {
    await call('POST', '/labels', { name: LABEL, color: 'b60205', description: 'The nightly full matrix failed; releases are blocked' });
  }
  const issue = await call('POST', '/issues', { title: 'Nightly full matrix is red', body, labels: [LABEL] });
  return `red; opened #${issue.number}`;
}

if (isMain(import.meta.url)) {
  try {
    process.stdout.write(`nightly-issue: PASS ${await report()}\n`);
  } catch (error) {
    process.stdout.write(`nightly-issue: FAIL ${error.message}\n`);
    process.exit(1);
  }
}
