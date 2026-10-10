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
    bin: { type: 'string' },
    only: { type: 'string', multiple: true },
  },
});

if (!values.version) {
  process.stderr.write('usage: npm-stage.mjs --version <semver> [--from dir] [--out dir] [--name pkg] [--bin name] [--only os-arch]\n');
  process.exit(2);
}
const version = values.version.replace(/^v/, '');
const binName = values.bin || values.name.split('/').pop();
const selected = values.only?.length
  ? TARGETS.filter((t) => values.only.includes(`${t.goos}-${t.goarch}`))
  : TARGETS;

function write(dir, file, content) {
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, file), typeof content === 'string' ? content : `${JSON.stringify(content, null, 2)}\n`);
}

function stagePlatform(target) {
  const asset = `slicc-${target.goos}-${target.goarch}${target.goos === 'windows' ? '.exe' : ''}`;
  const source = join(values.from, asset);
  if (!existsSync(source)) throw new Error(`missing ${source}`);
  const name = `${SCOPE}/slicc-${target.goos}-${target.goarch}`;
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

function stageLauncher(platforms) {
  const dir = join(values.out, 'slicc');
  mkdirSync(join(dir, 'bin'), { recursive: true });
  copyFileSync(join(root, 'npm', 'slicc.js'), join(dir, 'bin', 'slicc.js'));
  chmodSync(join(dir, 'bin', 'slicc.js'), 0o755);
  copyFileSync(join(root, 'LICENSE'), join(dir, 'LICENSE'));
  copyFileSync(join(root, 'README.md'), join(dir, 'README.md'));
  write(dir, 'package.json', {
    name: values.name,
    version,
    description: "SLICC's command line for seven: links a machine's network, compute and browsers to seven over WebRTC",
    ...common,
    bin: { [binName]: 'bin/slicc.js' },
    files: ['bin/slicc.js'],
    engines: { node: '>=18' },
    optionalDependencies: Object.fromEntries(platforms.map((name) => [name, version])),
  });
}

rmSync(values.out, { recursive: true, force: true });
const platforms = selected.map(stagePlatform);
stageLauncher(platforms);
process.stdout.write(`staged ${values.name}@${version} and ${platforms.length} platform packages in ${values.out}\n`);
