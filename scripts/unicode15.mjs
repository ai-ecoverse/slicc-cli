#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';

const source = 'https://www.unicode.org/Public/15.0.0/ucd/DerivedAge.txt';
const sha256 = '7570877e0fa197c45338f7c41a02636da4e14c8dba6a3611a01cd30bf329d5ca';

const data = process.argv[2] ? readFileSync(process.argv[2]) : Buffer.from(await (await fetch(source)).arrayBuffer());
const got = createHash('sha256').update(data).digest('hex');
if (got !== sha256) {
  console.error(`unicode15: ${process.argv[2] ?? source} has sha256 ${got}, want ${sha256}`);
  process.exit(1);
}

const ranges = [];
for (const line of data.toString('utf8').split('\n')) {
  const body = line.split('#')[0].trim();
  if (!body) continue;
  const [cp] = body.split(';').map((s) => s.trim());
  const [lo, hi = lo] = cp.split('..');
  ranges.push([Number.parseInt(lo, 16), Number.parseInt(hi, 16)]);
}
ranges.sort((a, b) => a[0] - b[0]);
const merged = [];
for (const [lo, hi] of ranges) {
  const last = merged.at(-1);
  if (last && lo <= last[1] + 1) last[1] = Math.max(last[1], hi);
  else merged.push([lo, hi]);
}
const split = merged.flatMap(([lo, hi]) => (lo <= 0xffff && hi > 0xffff ? [[lo, 0xffff], [0x10000, hi]] : [[lo, hi]]));
const r16 = split.filter(([, hi]) => hi <= 0xffff);
const r32 = split.filter(([lo]) => lo > 0xffff);
const hex = (n, width) => `0x${n.toString(16).padStart(width, '0')}`;
const out = [
  'package protect',
  '',
  'import "unicode"',
  '',
  'var unicode15 = &unicode.RangeTable{',
  '\tR16: []unicode.Range16{',
  ...r16.map(([lo, hi]) => `\t\t{Lo: ${hex(lo, 4)}, Hi: ${hex(hi, 4)}, Stride: 1},`),
  '\t},',
  '\tR32: []unicode.Range32{',
  ...r32.map(([lo, hi]) => `\t\t{Lo: ${hex(lo, 1)}, Hi: ${hex(hi, 1)}, Stride: 1},`),
  '\t},',
  `\tLatinOffset: ${r16.filter(([, hi]) => hi <= 0xff).length},`,
  '}',
  '',
];
process.stdout.write(out.join('\n'));
