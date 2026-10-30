#!/usr/bin/env node
'use strict';

const { spawn } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const tty = require('node:tty');

const targets = {
  'darwin arm64': 'darwin-arm64',
  'darwin x64': 'darwin-amd64',
  'linux arm64': 'linux-arm64',
  'linux x64': 'linux-amd64',
  'win32 arm64': 'windows-arm64',
  'win32 x64': 'windows-amd64',
};

function fail(message) {
  process.stderr.write(`slicc: ${message}\n`);
  process.exit(1);
}

function binaryPath() {
  const target = targets[`${process.platform} ${process.arch}`];
  if (!target) fail(`no slicc binary is published for ${process.platform}-${process.arch}`);
  const pkg = `@ai-ecoverse/slicc-${target}`;
  const file = process.platform === 'win32' ? 'slicc.exe' : 'slicc';
  let bin;
  try {
    bin = require.resolve(`${pkg}/bin/${file}`);
  } catch {
    fail(`${pkg} is not installed; reinstall without --omit=optional or --no-optional`);
  }
  if (process.platform !== 'win32') {
    try {
      fs.accessSync(bin, fs.constants.X_OK);
    } catch {
      try {
        fs.chmodSync(bin, 0o755);
      } catch {}
    }
  }
  return bin;
}

const companions = { '@ai-ecoverse/slicc-jerk': 'jerk' };

function companionPaths(modules, pkg, bin) {
  const bins = path.join(modules, '.bin');
  if (process.platform !== 'win32') return [path.join(bins, bin)];
  const exe = path.join(`${pkg}-win32-${process.arch}`, 'bin', `${bin}.exe`);
  return [path.join(modules, pkg, 'node_modules', exe), path.join(modules, exe), path.join(bins, `${bin}.exe`)];
}

function companionDirs() {
  const dirs = [];
  for (const [pkg, bin] of Object.entries(companions)) {
    for (const modules of require.resolve.paths(pkg) || []) {
      if (!fs.existsSync(path.join(modules, pkg, 'package.json'))) continue;
      const found = companionPaths(modules, pkg, bin).find((file) => fs.existsSync(file));
      if (found && !dirs.includes(path.dirname(found))) dirs.push(path.dirname(found));
      break;
    }
  }
  return dirs;
}

function childEnv() {
  const env = { ...process.env, SLICC_LAUNCHER: 'npm' };
  const dirs = companionDirs();
  if (!dirs.length) return env;
  const key = Object.keys(env).find((k) => k.toUpperCase() === 'PATH') || 'PATH';
  env[key] = [...dirs, env[key]].filter(Boolean).join(path.delimiter);
  return env;
}

const forwarded =
  process.platform === 'win32' ? [] : ['SIGTERM', 'SIGHUP', 'SIGQUIT', 'SIGUSR2'];
const swallowed = process.platform === 'win32' ? ['SIGINT', 'SIGBREAK'] : [];
if (process.platform !== 'win32') {
  (tty.isatty(0) ? swallowed : forwarded).push('SIGINT');
}

const child = spawn(binaryPath(), process.argv.slice(2), {
  stdio: 'inherit',
  env: childEnv(),
});

const handlers = new Map();
for (const signal of forwarded) {
  handlers.set(signal, () => {
    if (child.exitCode === null && child.signalCode === null) child.kill(signal);
  });
}
for (const signal of swallowed) handlers.set(signal, () => {});
for (const [signal, handler] of handlers) process.on(signal, handler);

child.on('error', (err) => fail(err.message));
child.on('exit', (code, signal) => {
  for (const [name, handler] of handlers) process.removeListener(name, handler);
  if (signal) {
    process.exitCode = 128 + (os.constants.signals[signal] || 0);
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code === null ? 1 : code);
});
