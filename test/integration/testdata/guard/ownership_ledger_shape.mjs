import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, unlinkSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createRequire } from 'node:module';
import { delayedInboundAudit, gateAuditComplete } from '../link/gate-audit-complete.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '../../../..');
const pkgJson = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
const pin = pkgJson.devDependencies['@ai-ecoverse/slicc-browsers'];
const require = createRequire(import.meta.url);
const installed = require(join(root, 'node_modules/@ai-ecoverse/slicc-browsers/package.json')).version;
if (installed !== pin) {
  throw new Error(`@ai-ecoverse/slicc-browsers installed ${installed} != package.json pin ${pin}`);
}

const pkg = join(root, 'node_modules/@ai-ecoverse/slicc-browsers');
const src = readFileSync(join(pkg, 'bfcache-restore.mjs'), 'utf8');
const exported = join(pkg, `_b2_restore_export_${process.pid}.mjs`);

async function loadRestore(source) {
  writeFileSync(exported, source.replace(/^class Restore/m, 'export class Restore'));
  try {
    return (await import(`${pathToFileURL(exported).href}?t=${Date.now()}`)).Restore;
  } finally {
    if (existsSync(exported)) unlinkSync(exported);
  }
}

const Restore = await loadRestore(src);

const read = { method: 'DOM.getDocument', params: { depth: -1, pierce: true } };
const release = { method: 'DOM.disable' };
const probe = { id: 1, method: 'DOM.getDocument', params: read.params, forwarded: true };
const methods = new Set([
  'Runtime.evaluate',
  'Runtime.callFunctionOn',
  'DOM.getDocument',
  'Accessibility.getFullAXTree',
  'Input.dispatchKeyEvent',
  'Page.captureScreenshot',
  'IO.read',
]);

function makeRestore(Cls) {
  const notes = [];
  const r = new Cls({
    notes,
    side: 'guarded',
    frameOwnershipProbe: true,
    forwardAudit: true,
    tag: 'shape',
    protectedOrigin: 'http://seven.test',
    ordinary: 'http://ordinary.test',
    delayMs: 1500,
    budget: 50,
  });
  r.result = { probes: [probe], delay: {} };
  r.firstProbeLedger = [JSON.stringify({ method: 'Page.enable' })];
  r.lastLedger = [JSON.stringify({ method: 'Page.enable' })];
  return { r, notes };
}

async function drive(Cls, reply, tweak) {
  const { r, notes } = makeRestore(Cls);
  if (tweak) tweak(r);
  r.gate = { audit: async () => reply };
  await r.audit(true);
  return { r, notes };
}

{
  const { r } = await drive(Restore, {
    complete: true,
    invalidFrames: 0,
    probes: [{ id: 1, forwarded: true }],
  });
  assert.equal(r.result.commandsUnforwarded, 'incomplete');
}

{
  const reply = {
    complete: false,
    invalidFrames: 2,
    probes: [{ id: 1, forwarded: true }],
    commands: [{ method: 'Page.enable' }, read, release],
  };
  const { r } = await drive(Restore, reply);
  assert.equal(r.result.commandsUnforwarded, 'incomplete');
  assert.equal(r.result.delay.finalAudit.result.complete, false);

  const mutantSrc = src.replace(
    'const proven = complete && (!owned || owned.ledger);',
    'const proven = (!owned || owned.ledger);'
  );
  assert.notEqual(mutantSrc, src);
  const Mutant = await loadRestore(mutantSrc);
  const { r: bad } = await drive(Mutant, reply);
  assert.equal(bad.result.commandsUnforwarded, true);
  assert.throws(() => assert.equal(bad.result.commandsUnforwarded, 'incomplete'));
}

{
  const { r } = await drive(Restore, {
    complete: true,
    invalidFrames: 0,
    probes: [{ id: 1, forwarded: true }],
    commands: [{ method: 'Page.enable' }, read],
  });
  r.gate = {
    audit: async () => ({
      complete: true,
      invalidFrames: 0,
      probes: [{ id: 1, forwarded: true }],
      commands: [{ method: 'Page.bringToFront' }, read, release],
    }),
  };
  await r.audit(true);
  assert.equal(r.ledgerBreak, true);
  assert.equal(r.result.commandsUnforwarded, 'incomplete');
}

{
  const { r } = await drive(Restore, {
    complete: true,
    invalidFrames: 0,
    probes: [{ id: 1, forwarded: true }],
    commands: [{ method: 'Page.enable' }, release, read],
  });
  assert.equal(r.result.commandsUnforwarded, false);
}

{
  const { r } = await drive(Restore, {
    complete: true,
    invalidFrames: 0,
    probes: [{ id: 1, forwarded: true }],
    commands: [
      { method: 'Page.enable' },
      read,
      { method: 'Runtime.evaluate', params: { expression: '1' } },
    ],
  });
  assert.equal(r.result.commandsUnforwarded, false);
}

{
  const { r } = await drive(
    Restore,
    {
      complete: true,
      invalidFrames: 0,
      probes: [
        { id: 1, forwarded: true },
        { id: 2, forwarded: true },
      ],
      commands: [
        { method: 'Page.enable' },
        read,
        { method: 'Runtime.evaluate', params: { expression: '1' } },
      ],
    },
    (x) => {
      x.result.probes = [
        probe,
        { id: 2, method: 'Runtime.evaluate', params: { expression: '1' }, forwarded: true },
      ];
    }
  );
  assert.equal(r.result.commandsUnforwarded, false);
}

{
  const { r, notes } = await drive(Restore, {
    complete: true,
    invalidFrames: 0,
    probes: [{ id: 1, forwarded: true }],
    commands: [{ method: 'Page.enable' }, read, release],
  });
  assert.equal(r.result.commandsUnforwarded, true);
  assert.equal(r.ownershipExempt, true);
  assert.ok(notes.some((n) => n.startsWith('commandsUnforwarded: exempt:frameOwnershipProbe (')));
}

{
  const probes = [{ id: 1, method: 'DOM.getDocument', params: read.params, reply: { ok: true } }];
  const sent = [{ id: 11, method: 'DOM.getDocument', params: read.params, sessionId: 'client' }];
  const fake = async (body) => {
    assert.equal(body.action, 'audit');
    return {
      invalidFrames: 2,
      probes: [{ id: 1, forwarded: true }],
      commands: [{ method: 'Page.enable' }, read, release],
    };
  };
  const bad = await delayedInboundAudit(fake, methods, 'client', sent, 0, probes);
  assert.equal(bad.complete, false);
  assert.equal(bad.invalidFrames, 2);
  assert.equal(gateAuditComplete({ probes: bad.probes }, probes), false);

  const good = await delayedInboundAudit(
    async () => ({
      invalidFrames: 0,
      probes: [{ id: 1, forwarded: true }],
      commands: [{ method: 'Page.enable' }, read, release],
    }),
    methods,
    'client',
    sent,
    0,
    probes
  );
  assert.equal(good.complete, true);
  assert.equal(gateAuditComplete({ probes: [{ id: 1, forwarded: true }] }, probes), false);
  assert.equal(
    gateAuditComplete({ invalidFrames: 0, probes: [{ id: 1, forwarded: true }] }, probes),
    true
  );
}

console.log(JSON.stringify({ ok: true }));
