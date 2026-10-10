#!/usr/bin/env node
import { appendFileSync } from 'node:fs';
import semanticRelease from 'semantic-release';

function output(key, value) {
  const line = `${key}=${value}\n`;
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, line);
  process.stdout.write(line);
}

const result = await semanticRelease(
  { dryRun: true, plugins: ['@semantic-release/commit-analyzer'] },
  { stdout: process.stderr, stderr: process.stderr },
);
output('version', result ? result.nextRelease.version : '');
