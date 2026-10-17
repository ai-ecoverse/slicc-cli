#!/usr/bin/env node
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { parseArgs } from 'node:util';
import { rowOf, runPattern } from './lib.mjs';

const { values } = parseArgs({
  options: {
    repo: { type: 'string', default: 'ai-ecoverse/slicc-cli' },
    pr: { type: 'string' },
    head: { type: 'string', default: '' },
    out: { type: 'string' },
  },
});
const { repo } = values;
const API = process.env.GITHUB_API_URL ?? 'https://api.github.com';
const headers = {
  accept: 'application/vnd.github+json',
  ...(process.env.GH_TOKEN ? { authorization: `Bearer ${process.env.GH_TOKEN}` } : {}),
};

async function request(url, accept) {
  const r = await fetch(url.startsWith('http') ? url : `${API}/${url}`, {
    headers: { ...headers, ...(accept ? { accept } : {}) },
  });
  if (!r.ok) throw new Error(`GET ${url}: HTTP ${r.status}`);
  return r;
}

async function all(path) {
  const out = [];
  let url = `${API}/${path}${path.includes('?') ? '&' : '?'}per_page=100`;
  while (url) {
    const r = await request(url);
    out.push(...(await r.json()));
    url = /<([^>]+)>;\s*rel="next"/.exec(r.headers.get('link') ?? '')?.[1];
  }
  return out;
}

const pr = await (await request(`repos/${repo}/pulls/${values.pr}`)).json();
const head = values.head || pr.head.sha;
const files = await all(`repos/${repo}/pulls/${values.pr}/files`);
const sources = {};
for (const f of files) {
  if (!/^test\/integration\/[^/]+_test\.go$/.test(f.filename) || f.status === 'removed') continue;
  const path = f.filename.split('/').map(encodeURIComponent).join('/');
  sources[f.filename] = await (
    await request(`repos/${repo}/contents/${path}?ref=${head}`, 'application/vnd.github.raw+json')
  ).text();
}
const row = rowOf(pr, files, sources);
const delta = { repo, pr: pr.number, head, row, names: row.names, run: runPattern(row.names) };
mkdirSync(dirname(values.out), { recursive: true });
writeFileSync(values.out, `${JSON.stringify(delta, null, 2)}\n`);
console.log(`#${row.pr}: ${row.title}`);
for (const t of row.tests) console.log(`- ${t.file}: ${t.names.join(', ')}`);
for (const g of row.gaps) console.log(`- gap: ${g}`);
for (const d of row.dropped) console.log(`- dropped: ${d.file}${d.test ? ` ${d.test}` : ''}${d.to ? ` -> ${d.to}` : ''}`);
console.log(`run: ${delta.run || '(none)'}`);
