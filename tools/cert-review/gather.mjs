#!/usr/bin/env node
import { copyFileSync, existsSync, mkdirSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { parseArgs } from 'node:util';

const OWN = { delta: 'delta', rebuild: 'rebuild', 'suite-candidate': 'candidate', 'suite-base': 'prev' };
const { values } = parseArgs({
  options: { in: { type: 'string' }, out: { type: 'string' }, downloads: { type: 'string', default: '' } },
});
if (!values.in || !values.out) {
  console.error('gather: --in DIR --downloads producer=outcome,… --out DIR');
  process.exit(2);
}
const outcomes = Object.fromEntries(values.downloads.split(',').map((x) => x.trim().split('=')));
mkdirSync(join(values.out, 'logs'), { recursive: true });
for (const [producer, stem] of Object.entries(OWN)) {
  const from = join(values.in, producer);
  if (outcomes[producer] !== 'success') {
    console.log(`gather: left out ${producer}: its download was ${outcomes[producer] || 'not reported'}`);
    continue;
  }
  const names = existsSync(from) ? readdirSync(from).sort() : [];
  if (names.length === 0) console.log(`gather: no ${producer} artifact`);
  for (const name of names) {
    const file = join(from, name);
    const logs = [`${stem}.jsonl`, `${stem}.retry.jsonl`, `${stem}.log`];
    const to = name === `${stem}.json` ? values.out : logs.includes(name) ? join(values.out, 'logs') : undefined;
    if (!to || !statSync(file).isFile()) {
      console.log(`gather: left out ${producer}/${name}, which isn't ${producer}'s evidence`);
      continue;
    }
    copyFileSync(file, join(to, name));
    console.log(`gather: ${producer}/${name}`);
  }
}
