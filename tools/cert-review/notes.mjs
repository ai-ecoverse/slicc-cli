#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { appendFileSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { parseArgs } from 'node:util';
import { releaseNotes } from './lib.mjs';

const { values } = parseArgs({
  options: {
    version: { type: 'string' },
    repo: { type: 'string', default: 'ai-ecoverse/slicc-cli' },
    lines: { type: 'string' },
  },
});
const { version, repo } = values;
const say = (text) => {
  console.log(text);
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, `${text}\n`);
};
const sh = (args) => execFileSync('gh', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
const lines = JSON.parse(readFileSync(values.lines, 'utf8'));
const shape = /^(certified|verified) \S+ (binaries SHA256SUMS )?sha256=[0-9a-f]{64} \(/;
if (!Array.isArray(lines) || lines.length === 0 || !lines.every((l) => shape.test(l.line ?? '') && l.line.replace(/^\w+ /, '').startsWith(`${l.prefix} sha256=${l.sha256} `))) {
  say(`cert-ci: INCOMPLETE slicc-cli v${version}: verify handed over no well-formed lines`);
  process.exit(1);
}
const notes = releaseNotes(JSON.parse(sh(['api', `repos/${repo}/releases/tags/v${version}`])).body, lines);
if (notes.conflict) {
  say(`supply-chain alert: slicc-cli v${version}: ${notes.conflict}; the notes stay as they are`);
  process.exit(1);
}
if (notes.unchanged) {
  say(`notes: v${version} already carries the ${lines.length} certified lines, unchanged`);
  process.exit(0);
}
const file = join(mkdtempSync(join(process.env.RUNNER_TEMP ?? tmpdir(), 'cert-notes-')), 'notes.md');
writeFileSync(file, notes.body);
sh(['release', 'edit', `v${version}`, '--repo', repo, '--notes-file', file]);
say(`notes: wrote the ${lines.length} certified lines into v${version}'s release notes`);
