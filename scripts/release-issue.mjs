#!/usr/bin/env node
import { isMain } from './is-main.mjs';

export const LABEL = 'release-red';
const BAD = new Set(['failure', 'cancelled', 'timed_out', 'startup_failure', 'action_required', 'stale']);
const RED = new Set(['failure', 'timed_out', 'startup_failure', 'action_required', 'stale']);

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

export function verdict(results) {
  const pairs = String(results || '')
    .split(/\s+/)
    .filter(Boolean)
    .map((pair) => {
      const at = pair.indexOf('=');
      return at < 0 ? [pair, ''] : [pair.slice(0, at), pair.slice(at + 1)];
    });
  if (!pairs.length) return { state: 'red', failed: ['no job results'] };
  const failed = pairs.filter(([, result]) => RED.has(result) || result === '').map(([job, result]) => `${job} ${result || 'unknown'}`);
  if (failed.length) return { state: 'red', failed };
  const cancelled = pairs.filter(([, result]) => result === 'cancelled').map(([job]) => job);
  if (cancelled.length) return { state: 'cancelled', failed: cancelled };
  return { state: 'green', failed: [] };
}

export async function report(env = process.env) {
  const call = client();
  const repo = env.GITHUB_REPOSITORY;
  const server = (env.GITHUB_SERVER_URL || 'https://github.com').replace(/\/$/, '');
  const runId = env.GITHUB_RUN_ID;
  const sha = env.SHA;
  if (!runId || !sha) throw new Error('GITHUB_RUN_ID and SHA must be set');
  const { state, failed } = verdict(env.RESULTS);
  if (state === 'cancelled') return `cancelled (${failed.join(', ')}) without a failure; nothing to report`;
  const run = `${server}/${repo}/actions/runs/${runId}`;
  const commit = `${server}/${repo}/commit/${sha}`;
  const listed = (await call('GET', `/issues?labels=${LABEL}&state=open&per_page=100`)) || [];
  const open = listed.filter((i) => !i.pull_request).sort((a, b) => a.number - b.number);
  if (state === 'green') {
    for (const issue of open) {
      await call('POST', `/issues/${issue.number}/comments`, { body: `Release [run ${runId}](${run}) built, tested and released ${sha}; closing.` });
      await call('PATCH', `/issues/${issue.number}`, { state: 'closed', state_reason: 'completed' });
    }
    return open.length ? `green; closed ${open.map((i) => `#${i.number}`).join(', ')}` : `green; no open ${LABEL} issue`;
  }
  const body = [
    `The release of ${sha} failed: **${failed.join(', ')}**.`,
    '',
    `- Run: ${run}`,
    `- Commit: [${sha}](${commit})`,
    '',
    'Failed jobs:',
    await failedJobs(call, runId),
    '',
    `This commit isn't released. The next push to main builds and releases again from scratch, with every commit since the last tag; to retry this one, re-run the run's failed jobs or \`gh workflow run release.yml --ref main\`. A green release closes this issue.`,
  ].join('\n');
  if (open.length) {
    await call('POST', `/issues/${open[0].number}/comments`, { body });
    return `red; commented on #${open[0].number}`;
  }
  if (!(await call('GET', `/labels/${LABEL}`))) {
    await call('POST', '/labels', { name: LABEL, color: 'b60205', description: 'A release from main failed in its full build, release or smoke tests' });
  }
  const issue = await call('POST', '/issues', { title: 'Release from main failed', body, labels: [LABEL] });
  return `red; opened #${issue.number}`;
}

if (isMain(import.meta.url)) {
  try {
    process.stdout.write(`release-issue: PASS ${await report()}\n`);
  } catch (error) {
    process.stdout.write(`release-issue: FAIL ${error.message}\n`);
    process.exit(1);
  }
}
