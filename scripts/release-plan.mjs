#!/usr/bin/env node
import { appendFileSync, readFileSync } from 'node:fs';
import semanticRelease from 'semantic-release';
import { checkMajor } from './go-major.mjs';

function output(key, value) {
  const line = `${key}=${value}\n`;
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, line);
  process.stdout.write(line);
}

const result = await semanticRelease(
  { dryRun: true, plugins: ['@semantic-release/commit-analyzer'] },
  { stdout: process.stderr, stderr: process.stderr },
);
if (result) checkMajor(result.nextRelease.version, readFileSync('go.mod', 'utf8'));
output('version', result ? result.nextRelease.version : '');
process.stdout.write(`release-plan: PASS ${result ? `release ${result.nextRelease.version}` : 'no release due'}\n`);
