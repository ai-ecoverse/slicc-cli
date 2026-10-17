import { matchesGlob } from 'node:path';

const MERGE = /^Merge pull request #(\d+) from /;
const KIND = /^(\w+)(?:\([^)]*\))?(!)?:/;
const BEHAVIOUR_KINDS = new Set(['fix', 'feat', 'perf', 'revert']);
const GO_TEST_FILE = /^test\/integration\/[^/]+_test\.go$/;
const FUNC = /^func\s+(?:\(\s*\w*\s*\*?\s*(\w+)(?:\[[^\]]*\])?\s*\)\s*)?(\w+)\s*[[(]/;
const TOP = /^(?:var|const|type)\b/;

export const NAME = 'ai-ecoverse/slicc-cli';

export const REVIEWER_GLOBS = ['.github/**', 'tools/cert-review/**'];

export const FORCED_GLOBS = [
  'scripts/**',
  'install.sh',
  'npm/**',
  'package.json',
  'package-lock.json',
  '.releaserc*',
];

const names = (files) =>
  (files ?? []).flatMap((f) => [
    f.filename ?? f,
    ...(f.previous_filename ? [f.previous_filename] : []),
  ]);

const isReviewer = (name) => REVIEWER_GLOBS.some((glob) => matchesGlob(name, glob));

export function forcedFiles(files) {
  return [
    ...new Set(
      names(files).filter(
        (name) => isReviewer(name) || FORCED_GLOBS.some((glob) => matchesGlob(name, glob))
      )
    ),
  ];
}

export function reviewsItself(files) {
  return names(files).some(isReviewer);
}

export function outputDiff(base, candidate) {
  const out = [];
  if (!base || !candidate) return undefined;
  const pairs = [
    [base.files ?? {}, candidate.files ?? {}, (k) => k],
    [base.packages ?? {}, candidate.packages ?? {}, (k) => `npm ${k}`],
  ];
  for (const [a, b, label] of pairs) {
    for (const key of [...new Set([...Object.keys(a), ...Object.keys(b)])].sort()) {
      const x = typeof a[key] === 'string' ? a[key] : a[key]?.sha256;
      const y = typeof b[key] === 'string' ? b[key] : b[key]?.sha256;
      if (x !== y) out.push(label(key));
    }
  }
  return out;
}

export function mergedPrs(commits) {
  const seen = new Set();
  const out = [];
  for (const commit of commits) {
    const message = commit.commit?.message ?? commit.message ?? '';
    const n = MERGE.exec(message)?.[1];
    if (n !== undefined && !seen.has(Number(n))) {
      seen.add(Number(n));
      out.push(Number(n));
    }
  }
  return out.sort((a, b) => a - b);
}

export function kindOf(title) {
  const m = KIND.exec(title ?? '');
  return m ? m[1].toLowerCase() : 'other';
}

function funcOf(line) {
  const m = FUNC.exec(line);
  if (!m) return undefined;
  return m[1] ? `${m[1]}.${m[2]}` : m[2];
}

const isTest = (name) => /^Test[A-Z0-9_]/.test(name ?? '') || name === 'TestMain';

function scopeOf(text, current) {
  const declared = funcOf(text);
  if (declared) return declared;
  if (TOP.test(text)) return '';
  if (/^(?:import|package)\b/.test(text)) return undefined;
  return current;
}

export function goChanges(patch) {
  const changed = new Set();
  const removed = new Set();
  const added = new Set();
  let top = false;
  let current;
  for (const line of (patch ?? '').split('\n')) {
    const hunk = /^@@[^@]*@@ ?(.*)$/.exec(line);
    if (hunk) {
      current = scopeOf(hunk[1], undefined);
      continue;
    }
    const sign = line[0];
    if (sign !== '+' && sign !== '-' && sign !== ' ') continue;
    const text = line.slice(1);
    if (/^\s*\/\//.test(text)) continue;
    current = scopeOf(text, current);
    const declared = funcOf(text);
    if (declared && sign === '+') added.add(declared);
    if (declared && sign === '-') removed.add(declared);
    if (sign === ' ' || text.trim() === '' || current === undefined) continue;
    if (current === '') top = true;
    else changed.add(current);
  }
  const gone = [...removed].filter((n) => !added.has(n));
  for (const n of gone) changed.delete(n);
  const all = [...changed];
  return {
    tests: all.filter((n) => isTest(n) && n !== 'TestMain').sort(),
    helpers: all.filter((n) => !isTest(n) || n === 'TestMain').sort(),
    dropped: gone.filter((n) => isTest(n) && n !== 'TestMain').sort(),
    top,
  };
}

export function testsIn(source) {
  const out = [];
  for (const line of (source ?? '').split('\n')) {
    const name = funcOf(line);
    if (name && isTest(name) && name !== 'TestMain' && !name.includes('.')) out.push(name);
  }
  return [...new Set(out)].sort();
}

export function rowOf(pr, files, sources = {}) {
  const kind = kindOf(pr.title);
  const tests = [];
  const gaps = [];
  const dropped = [];
  for (const f of files) {
    if (!GO_TEST_FILE.test(f.filename)) {
      if (f.previous_filename && GO_TEST_FILE.test(f.previous_filename))
        dropped.push({ file: f.previous_filename, to: f.filename });
      continue;
    }
    if (f.status === 'removed') {
      dropped.push({ file: f.filename });
      continue;
    }
    const inFile = sources[f.filename];
    let names;
    if (f.patch === undefined) {
      if (inFile === undefined) {
        gaps.push(`file ${f.filename}: no patch and no source`);
        continue;
      }
      names = testsIn(inFile);
    } else {
      const c = goChanges(f.patch);
      names = [...c.tests];
      for (const d of c.dropped) dropped.push({ file: f.filename, test: d });
      if (c.helpers.length > 0 || c.top) {
        const all = inFile === undefined ? [] : testsIn(inFile);
        if (all.length > 0) names.push(...all);
        else
          for (const h of c.helpers.length ? c.helpers : ['(top level)'])
            gaps.push(`helper ${h} in ${f.filename}`);
      }
    }
    names = [...new Set(names)].sort();
    if (names.length) tests.push({ file: f.filename, status: f.status, names });
  }
  return {
    pr: pr.number,
    title: pr.title,
    kind,
    fix: kind === 'fix',
    behaviourKind: BEHAVIOUR_KINDS.has(kind),
    tests,
    names: [...new Set(tests.flatMap((t) => t.names))].sort(),
    gaps,
    dropped,
  };
}

export function runPattern(names) {
  if (!names?.length) return '';
  return `^(${names.join('|')})$`;
}

export function goResults(text) {
  const state = new Map();
  const output = new Map();
  const order = [];
  let buildFailed = false;
  const packages = new Map();
  for (const line of String(text ?? '').split('\n')) {
    if (!line.startsWith('{')) continue;
    let ev;
    try {
      ev = JSON.parse(line);
    } catch {
      continue;
    }
    if (ev.Action === 'build-fail') buildFailed = true;
    if (!ev.Test) {
      if (ev.Package && ['pass', 'fail', 'skip'].includes(ev.Action))
        packages.set(ev.Package, ev.Action);
      if (ev.Action === 'output' && /\[build failed\]|\[setup failed\]/.test(ev.Output ?? ''))
        buildFailed = true;
      continue;
    }
    const key = ev.Test;
    if (ev.Action === 'run' && !state.has(key)) {
      state.set(key, 'run');
      order.push(key);
    }
    if (ev.Action === 'output') {
      const lines = output.get(key) ?? [];
      lines.push(ev.Output ?? '');
      if (lines.length > 40) lines.shift();
      output.set(key, lines);
    }
    if (['pass', 'fail', 'skip'].includes(ev.Action)) {
      if (!state.has(key)) order.push(key);
      state.set(key, ev.Action);
    }
  }
  const rows = order.map((name) => {
    const s = state.get(name);
    const status = s === 'run' ? 'cancelled' : s;
    const row = { name, status };
    if (status === 'fail' || status === 'cancelled')
      row.error = (output.get(name) ?? []).join('').trim().slice(-600);
    return row;
  });
  return { rows, buildFailed, packages: Object.fromEntries(packages) };
}

function under(rows, name) {
  return rows.filter((r) => r.name === name || r.name.startsWith(`${name}/`));
}

export function verdictOf(rows, name) {
  const found = under(rows ?? [], name);
  if (found.length === 0) return 'missing';
  if (found.some((r) => r.status === 'fail' || r.status === 'cancelled')) return 'fail';
  const top = found.find((r) => r.name === name);
  if (top?.status === 'skip') return 'skip';
  if (found.every((r) => r.status === 'skip')) return 'skip';
  return 'pass';
}

export function decide(row, prev, next) {
  const checks = row.names.map((name) => ({
    name,
    next: verdictOf(next, name),
    prev: verdictOf(prev, name),
  }));
  const base = { pr: row.pr, title: row.title, kind: row.kind, checks };
  if (row.names.length === 0)
    return { ...base, decision: 'no-test' };
  if (checks.some((c) => c.next === 'fail')) return { ...base, decision: 'fails-on-candidate' };
  const ran = checks.filter((c) => c.next === 'pass');
  if (ran.length === 0) return { ...base, decision: 'not-run' };
  if (!row.fix) return { ...base, decision: 'passes' };
  if (ran.some((c) => c.prev === 'fail')) return { ...base, decision: 'negative' };
  if (ran.some((c) => c.prev === 'missing' || c.prev === 'skip'))
    return { ...base, decision: 'prev-not-run' };
  return { ...base, decision: 'no-negative' };
}

export const GAPS = new Set(['no-test', 'no-negative', 'prev-not-run', 'not-run']);

export function ciEvidence(runs) {
  const missing = [];
  const latest = new Map();
  for (const r of runs ?? []) {
    const before = latest.get(r.name);
    if (!before || String(r.started_at ?? '') > String(before.started_at ?? '')) latest.set(r.name, r);
  }
  const all = [...latest.values()];
  const tests = all.filter((r) => /^test \(/.test(r.name));
  const per = {};
  for (const os of ['ubuntu-latest', 'macos-latest', 'windows-latest']) {
    const legs = tests.filter((r) => r.name.startsWith(`test (${os},`));
    per[os] = legs.map((r) => `${r.name}=${r.status === 'completed' ? r.conclusion : r.status}`);
    if (legs.length === 0) missing.push(`CI has no ${os} test run on the head`);
    for (const r of legs)
      if (r.status !== 'completed' || r.conclusion !== 'success')
        missing.push(
          `CI's ${r.name} on the head is ${r.status === 'completed' ? r.conclusion : r.status}`
        );
  }
  const ci = latest.get('ci');
  if (!ci) missing.push("CI's ci check is missing on the head");
  else if (ci.status !== 'completed' || ci.conclusion !== 'success')
    missing.push(`CI's ci check on the head is ${ci.status === 'completed' ? ci.conclusion : ci.status}`);
  return { per, ci: ci ? `${ci.status}/${ci.conclusion ?? ''}` : 'missing', missing };
}

export function knownLine(repo, entries) {
  const out = [];
  for (const entry of entries) {
    const text = String(entry).trim();
    const local = /^#(\d+)$/.exec(text);
    const full = local ? `${repo}#${local[1]}` : text;
    if (full && /^[\w.-]+\/[\w.-]+#\d+$/.test(full) && !out.includes(full)) out.push(full);
  }
  return out;
}

export function withMissing(incomplete, reasons) {
  const more = (reasons ?? []).filter(Boolean);
  if (more.length === 0) return incomplete;
  const named = [incomplete?.named, ...more].filter(Boolean).join('; ');
  return { named, remedy: 're-run the failed jobs' };
}

export function jobsEvidence(text, incomplete) {
  const failed = String(text ?? '')
    .split(',')
    .map((x) => x.trim().split('='))
    .filter(([job, result]) => job && result !== 'success')
    .map(([job, result]) => `the ${job} job ended ${result || 'without a result'}`);
  return withMissing(incomplete, failed);
}

export function verdictLine({ tag, ci, problems, review, incomplete, certified, known = [] }) {
  if (problems.length === 0 && !incomplete && review?.verdict === 'certified')
    return { state: 'success', line: certified };
  if (incomplete && problems.length === 0) {
    return {
      state: 'error',
      line: `cert-ci: INCOMPLETE ${tag} (${incomplete.named}; ${incomplete.remedy}) (${ci}; known: ${known.length ? known.join(', ') : 'none'})`,
    };
  }
  const why = [
    ...(review?.verdict === 'fail' && !incomplete
      ? [`the reviewer: ${review.reason || 'no reason given'}`]
      : []),
    ...(review?.verdict === 'certified' && !incomplete ? ['the reviewer certified it'] : []),
    ...problems,
    ...(incomplete ? [`missing evidence (${incomplete.named})`] : []),
  ];
  return { state: 'failure', line: `cert-ci: FAIL ${tag}: ${why.join('; ')} (${ci})` };
}

export function groupVerdict(prs) {
  const lines = [];
  const missing = [];
  for (const p of prs) {
    if (p.state === 'success') {
      lines.push(`#${p.pr} ${/^not behaviour/.test(p.description ?? '') ? 'not behaviour' : 'certified'} at ${p.head.slice(0, 12)}`);
      continue;
    }
    missing.push(
      `#${p.pr} has ${p.state ? `a ${p.state} cert-review` : 'no cert-review'} on its head ${p.head.slice(0, 12)}${p.description ? ` (${p.description})` : ''}`
    );
  }
  return missing.length
    ? { state: 'failure', line: `not certified: ${missing.join('; ')}`, lines: [...lines, ...missing] }
    : { state: 'success', line: lines.join(', ') || 'no pull requests', lines };
}

export function prVersion(latest, pr, run) {
  const m = /^(\d+)\.(\d+)\.(\d+)$/.exec(latest ?? '');
  if (!m) throw new Error(`npm's latest reads as '${latest}'`);
  return `${m[1]}.${m[2]}.${Number(m[3]) + 1}-pr.${pr}.${run}`;
}

export const PLATFORMS = [
  'darwin-arm64',
  'darwin-amd64',
  'linux-arm64',
  'linux-amd64',
  'windows-arm64',
  'windows-amd64',
];

export const ASSETS = [
  ...PLATFORMS.map((p) => `slicc-${p}${p.startsWith('windows') ? '.exe' : ''}`),
  'slicc-wasip1-wasm.wasm',
  'install.sh',
];

export function releasePackages(version) {
  return [
    { name: '@ai-ecoverse/slicc', version, gate: 'SLICC_NPM_PUBLISH' },
    ...PLATFORMS.map((p) => ({
      name: `@ai-ecoverse/slicc-${p}`,
      version,
      gate: 'SLICC_NPM_PUBLISH',
      asset: `slicc-${p}${p.startsWith('windows') ? '.exe' : ''}`,
    })),
    { name: '@ai-ecoverse/wasi-slicc', version, gate: 'WASI_SLICC_PUBLISH', asset: 'slicc-wasip1-wasm.wasm' },
    { name: 'sliccy', version: `7.0.0-next.${version}`, gate: 'SLICCY_NEXT_PUBLISH' },
  ];
}

export function provenanceProblems(statement, { repo, name, version, sha512, commit }) {
  const problems = [];
  const subject = statement.subject?.find((s) => s.name === `pkg:npm/${name.replace('@', '%40')}@${version}`);
  if (subject?.digest?.sha512 !== sha512)
    problems.push(`${name}@${version}: the provenance subject isn't this tarball's sha512`);
  const build = statement.predicate?.buildDefinition ?? {};
  const workflow = build.externalParameters?.workflow ?? {};
  const gitCommit = build.resolvedDependencies?.[0]?.digest?.gitCommit;
  const builder = statement.predicate?.runDetails?.builder?.id ?? '';
  if (workflow.repository !== `https://github.com/${repo}`)
    problems.push(`${name}@${version}: provenance repository is ${workflow.repository}`);
  if (workflow.path !== '.github/workflows/release.yml')
    problems.push(`${name}@${version}: provenance workflow is ${workflow.path}`);
  if (workflow.ref !== 'refs/heads/main') problems.push(`${name}@${version}: provenance ref is ${workflow.ref}`);
  if (!builder.endsWith('/github-hosted'))
    problems.push(`${name}@${version}: provenance builder is ${builder || 'missing'}`);
  if (gitCommit !== commit)
    problems.push(`${name}@${version}: provenance gitCommit ${gitCommit} isn't the tag's commit ${commit}`);
  return { problems, facts: { workflow: `${workflow.path}@${workflow.ref}`, gitCommit, builder } };
}

export function sumsOf(text) {
  const out = {};
  for (const line of String(text ?? '').split('\n')) {
    const m = /^([0-9a-f]{64}) [ *]?(.+)$/.exec(line.trim());
    if (m) out[m[2]] = m[1];
  }
  return out;
}

export function reproducedFrom(release, rebuilt) {
  const a = sumsOf(release);
  const b = sumsOf(rebuilt);
  const names = [...new Set([...Object.keys(a), ...Object.keys(b)])].sort();
  const differ = names.filter((n) => !n.includes('-darwin-') && a[n] !== b[n]);
  const signed = names.filter((n) => n.includes('-darwin-'));
  return { match: differ.length === 0 && names.length > 0, differ, signed, files: names.length - signed.length };
}

const escape = (t) => t.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&');

export function releaseNotes(body, lines) {
  const text = body ?? '';
  let all = lines.length > 0;
  for (const { prefix, sha256 } of lines) {
    const found = [...text.matchAll(new RegExp(`(?:certified|verified) ${escape(prefix)} sha256=([0-9a-f]{64})`, 'g'))].map((m) => m[1]);
    const other = found.find((s) => s !== sha256);
    if (other) return { conflict: `the release notes already certify ${prefix} sha256=${other}` };
    if (found.length === 0) all = false;
  }
  if (all) return { unchanged: true, body: text };
  const old = [...text.matchAll(/<!-- cert-verify -->\n([\s\S]*?)<!-- \/cert-verify -->/g)].flatMap((m) => m[1].split('\n'));
  const kept = old.filter((l) => /^(certified|verified) /.test(l) && !lines.some((n) => l.replace(/^\w+ /, '').startsWith(`${n.prefix} sha256=`)));
  const rest = text.replace(/\n*<!-- cert-verify -->[\s\S]*?<!-- \/cert-verify -->\n*/g, '').trimEnd();
  return {
    body: `${rest}${rest ? '\n\n' : ''}<!-- cert-verify -->\n${[...kept, ...lines.map((l) => l.line)].join('\n')}\n<!-- /cert-verify -->\n`,
  };
}

export function notarized(answers) {
  return answers.some((a) => a.exit === 0 && /source=Notarized Developer ID/.test(a.output) && /\baccepted\b/.test(a.output));
}
