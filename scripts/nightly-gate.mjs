#!/usr/bin/env node
import { isMain } from './is-main.mjs';

export const WORKFLOW = 'nightly.yml';
export const MAX_AGE_HOURS = 36;
const UNBLOCK = `fix main if it is red, run the Nightly workflow (gh workflow run ${WORKFLOW} --ref main), and once it passes run the release again`;

export function age(ms) {
  const minutes = Math.max(0, Math.floor(ms / 60000));
  return `${Math.floor(minutes / 60)}h${String(minutes % 60).padStart(2, '0')}m`;
}

export function judge(run, now = Date.now()) {
  if (!run) return { pass: false, line: `no completed ${WORKFLOW} run on main` };
  const started = Date.parse(run.run_started_at || run.created_at || '');
  const where = `${run.html_url} ${run.head_sha}`;
  if (Number.isNaN(started)) return { pass: false, line: `${where}: the run has no start time` };
  const old = now - started;
  const ago = age(old);
  if (run.conclusion !== 'success') return { pass: false, line: `${where} ${ago}: the latest nightly ended ${run.conclusion || 'without a conclusion'}` };
  if (old > MAX_AGE_HOURS * 3600000) return { pass: false, line: `${where} ${ago}: the latest nightly is older than ${MAX_AGE_HOURS}h` };
  return { pass: true, line: `${where} ${ago}` };
}

async function latest() {
  const api = (process.env.GITHUB_API_URL || 'https://api.github.com').replace(/\/$/, '');
  const repo = process.env.GITHUB_REPOSITORY;
  if (!repo) throw new Error('GITHUB_REPOSITORY is not set');
  const url = `${api}/repos/${repo}/actions/workflows/${WORKFLOW}/runs?branch=main&status=completed&exclude_pull_requests=true&per_page=1`;
  const headers = { Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28' };
  const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN;
  if (token) headers.Authorization = `Bearer ${token}`;
  const res = await fetch(url, { headers });
  if (!res.ok) throw new Error(`GET ${url}: HTTP ${res.status}`);
  const body = await res.json();
  return (body.workflow_runs || [])[0];
}

if (isMain(import.meta.url)) {
  let verdict;
  try {
    verdict = judge(await latest());
  } catch (error) {
    verdict = { pass: false, line: `could not read the latest nightly: ${error.message}` };
  }
  if (verdict.pass) {
    process.stdout.write(`nightly: PASS ${verdict.line}\n`);
  } else {
    process.stdout.write(`nightly: FAIL ${verdict.line}\n`);
    process.stdout.write(`::error::release refused: ${verdict.line}; ${UNBLOCK}\n`);
    process.exit(1);
  }
}
