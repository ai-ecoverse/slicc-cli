#!/usr/bin/env node
import { chmodSync, copyFileSync, existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

const TARGETS = [
  { goos: 'darwin', goarch: 'arm64', os: 'darwin', cpu: 'arm64', label: 'macOS on Apple silicon' },
  { goos: 'darwin', goarch: 'amd64', os: 'darwin', cpu: 'x64', label: 'macOS on Intel' },
  { goos: 'linux', goarch: 'arm64', os: 'linux', cpu: 'arm64', label: 'Linux on arm64' },
  { goos: 'linux', goarch: 'amd64', os: 'linux', cpu: 'x64', label: 'Linux on x86-64' },
  { goos: 'windows', goarch: 'arm64', os: 'win32', cpu: 'arm64', label: 'Windows on arm64' },
  { goos: 'windows', goarch: 'amd64', os: 'win32', cpu: 'x64', label: 'Windows on x86-64' },
];

const SCOPE = '@ai-ecoverse';
const repository = { type: 'git', url: 'git+https://github.com/ai-ecoverse/slicc-cli.git' };
const common = {
  license: 'Apache-2.0',
  repository,
  homepage: 'https://github.com/ai-ecoverse/slicc-cli#readme',
};

const { values } = parseArgs({
  options: {
    version: { type: 'string' },
    from: { type: 'string', default: join(root, 'artifacts', 'release') },
    out: { type: 'string', default: join(root, 'dist', 'npm') },
    name: { type: 'string', default: `${SCOPE}/slicc` },
    bin: { type: 'string', multiple: true },
    only: { type: 'string', multiple: true },
    'deps-version': { type: 'string' },
    'launcher-only': { type: 'boolean', default: false },
    launcher: { type: 'string', default: join(root, 'npm', 'slicc.js') },
    companions: { type: 'boolean', default: false },
  },
});

const COMPANIONS = ['@ai-ecoverse/slicc-jerk'];

function registryBase() {
  return (process.env.npm_config_registry || 'https://registry.npmjs.org/').replace(/\/+$/, '');
}

async function latest(name) {
  try {
    const res = await fetch(`${registryBase()}/${name.replace('/', '%2f')}/latest`, {
      headers: { accept: 'application/json', 'cache-control': 'no-cache' },
      signal: AbortSignal.timeout(15000),
    });
    if (!res.ok) return null;
    const version = (await res.json()).version;
    return /^\d+\.\d+\.\d+$/.test(version) ? version : null;
  } catch {
    return null;
  }
}

async function companionDeps() {
  const deps = {};
  if (!values.companions) return deps;
  for (const name of COMPANIONS) {
    const version = await latest(name);
    if (version) deps[name] = `^${version}`;
    else process.stdout.write(`${name} has no release on the registry yet; leaving it out\n`);
  }
  return deps;
}

if (!values.version) {
  process.stderr.write('usage: npm-stage.mjs --version <semver> [--from dir] [--out dir] [--name pkg] [--bin name]... [--only os-arch] [--deps-version semver] [--launcher-only] [--launcher file] [--companions]\n');
  process.exit(2);
}
const version = values.version.replace(/^v/, '');
const depsVersion = (values['deps-version'] || version).replace(/^v/, '');
const binNames = values.bin?.length
  ? values.bin
  : values.name === 'sliccy'
    ? ['sliccy', 'slicc']
    : [values.name.split('/').pop()];
const selected = values.only?.length
  ? TARGETS.filter((t) => values.only.includes(`${t.goos}-${t.goarch}`))
  : TARGETS;

const sliccyReadme = `# sliccy@next

This is the preview of the new SLICC, seven, the SLICC on \`*.sliccy.ai\`. \`npx sliccy@next\` runs \`slicc\`, seven's command line: it launches and links browsers, and shares a machine's network, compute and browsers with seven over WebRTC. It installs two commands, \`sliccy\` and \`slicc\`, which run the same launcher. It is the same launcher as [@ai-ecoverse/slicc](https://www.npmjs.com/package/@ai-ecoverse/slicc), built from [ai-ecoverse/slicc-cli](https://github.com/ai-ecoverse/slicc-cli).

For SLICC six, run \`npx sliccy@6\`.
`;

function write(dir, file, content) {
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, file), typeof content === 'string' ? content : `${JSON.stringify(content, null, 2)}\n`);
}

function stagePlatform(target) {
  const asset = `slicc-${target.goos}-${target.goarch}${target.goos === 'windows' ? '.exe' : ''}`;
  const source = join(values.from, asset);
  if (!existsSync(source)) throw new Error(`missing ${source}`);
  const name = platformName(target);
  const dir = join(values.out, `slicc-${target.goos}-${target.goarch}`);
  const file = target.goos === 'windows' ? 'slicc.exe' : 'slicc';
  mkdirSync(join(dir, 'bin'), { recursive: true });
  copyFileSync(source, join(dir, 'bin', file));
  chmodSync(join(dir, 'bin', file), 0o755);
  copyFileSync(join(root, 'LICENSE'), join(dir, 'LICENSE'));
  write(dir, 'package.json', {
    name,
    version,
    description: `The slicc binary for ${target.label}`,
    ...common,
    os: [target.os],
    cpu: [target.cpu],
    files: [`bin/${file}`],
    preferUnplugged: true,
  });
  write(dir, 'README.md', `# ${name}\n\nThe \`slicc\` binary for ${target.label}. Install [${values.name}](https://www.npmjs.com/package/${values.name}) instead of this package.\n`);
  return name;
}

function stageLauncher(platforms, companions) {
  const dir = join(values.out, 'slicc');
  mkdirSync(join(dir, 'bin'), { recursive: true });
  copyFileSync(values.launcher, join(dir, 'bin', 'slicc.js'));
  chmodSync(join(dir, 'bin', 'slicc.js'), 0o755);
  copyFileSync(join(root, 'LICENSE'), join(dir, 'LICENSE'));
  if (values.name === 'sliccy') write(dir, 'README.md', sliccyReadme);
  else copyFileSync(join(root, 'README.md'), join(dir, 'README.md'));
  write(dir, 'package.json', {
    name: values.name,
    version,
    description: "SLICC's command line for seven: links a machine's network, compute and browsers to seven over WebRTC",
    ...common,
    bin: Object.fromEntries(binNames.map((name) => [name, 'bin/slicc.js'])),
    files: ['bin/slicc.js'],
    engines: { node: '>=18' },
    optionalDependencies: { ...Object.fromEntries(platforms.map((name) => [name, depsVersion])), ...companions },
  });
}

rmSync(values.out, { recursive: true, force: true });
const platformName = (t) => `${SCOPE}/slicc-${t.goos}-${t.goarch}`;
const platforms = values['launcher-only'] ? selected.map(platformName) : selected.map(stagePlatform);
stageLauncher(platforms, await companionDeps());
const staged = values['launcher-only'] ? 'no platform packages' : `${platforms.length} platform packages`;
process.stdout.write(`staged ${values.name}@${version} (platform packages at ${depsVersion}) and ${staged} in ${values.out}\n`);
