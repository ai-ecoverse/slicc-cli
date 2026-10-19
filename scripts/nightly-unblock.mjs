#!/usr/bin/env node
import { isMain } from './is-main.mjs';
import { WORKFLOW, judge } from './nightly-gate.mjs';

export const RELEASE = 'release.yml';

export function decide(runs, current) {
  const at = Date.parse(current.updated_at || current.created_at || '');
  const created = Date.parse(current.created_at || '');
  if (Number.isNaN(at) || Number.isNaN(created)) return { error: `nightly run ${current.id} has no creation time` };
  if (Number(current.run_attempt) > 1) return { dispatch: true, line: `${current.html_url} passed on attempt ${current.run_attempt}, so an earlier attempt was red` };
  const before = runs.find((r) => String(r.id) !== String(current.id) && Date.parse(r.created_at) < created);
  const verdict = judge(before, at);
  if (verdict.pass) return { dispatch: false, line: `${current.html_url} passed after a green nightly (${verdict.line}); the gate held nothing back` };
  return { dispatch: true, line: `${current.html_url} passed after a red gate (${verdict.line})` };
}

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
    if (!res.ok) throw new Error(`${method} ${url}: HTTP ${res.status}`);
    return res.status === 204 ? {} : res.json();
  };
}

export async function unblock(env = process.env) {
  const current = {
    id: env.NIGHTLY_ID,
    run_attempt: env.NIGHTLY_ATTEMPT,
    created_at: env.NIGHTLY_CREATED,
    updated_at: env.NIGHTLY_UPDATED,
    html_url: env.NIGHTLY_URL || `nightly run ${env.NIGHTLY_ID}`,
  };
  if (!current.id) throw new Error('NIGHTLY_ID is not set');
  const call = client();
  const body = await call('GET', `/actions/workflows/${WORKFLOW}/runs?branch=main&status=completed&exclude_pull_requests=true&per_page=20`);
  const verdict = decide(body.workflow_runs || [], current);
  if (verdict.error) throw new Error(verdict.error);
  if (!verdict.dispatch) return `nothing to release: ${verdict.line}`;
  await call('POST', `/actions/workflows/${RELEASE}/dispatches`, { ref: 'main' });
  return `dispatched ${RELEASE} on main: ${verdict.line}`;
}

if (isMain(import.meta.url)) {
  try {
    process.stdout.write(`nightly-unblock: PASS ${await unblock()}\n`);
  } catch (error) {
    process.stdout.write(`nightly-unblock: FAIL ${error.message}\n`);
    process.stdout.write(`::error::could not release what the red nightly held back: ${error.message}; run the release by hand: gh workflow run ${RELEASE} --ref main\n`);
    process.exit(1);
  }
}
