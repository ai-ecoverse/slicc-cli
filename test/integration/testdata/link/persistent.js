import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';

export const persistentMethods = ['Page.addScriptToEvaluateOnNewDocument', 'Page.addScriptToEvaluateOnLoad', 'Runtime.addBinding', 'Page.setBypassCSP'];

async function outcome(promise) {
  try { return { ok: true, result: await promise }; }
  catch (error) { return { ok: false, error: error.cdp ?? { message: error.message } }; }
}

async function until(read, description, ms = 15000) {
  const deadline = Date.now() + ms;
  do {
    let value;
    try { value = await read(); } catch {}
    if (value) return value;
    await new Promise((resolve) => setTimeout(resolve, 25));
  } while (Date.now() < deadline);
  throw new Error(`persistent measurement: ${description} not observed`);
}

function configuration(method, marker) {
  switch (method) {
    case 'Page.addScriptToEvaluateOnNewDocument': return { source: `globalThis.__slicc503 = ${JSON.stringify(marker)}; addEventListener('pageshow', (e) => { globalThis.__slicc503Restored = e.persisted; });` };
    case 'Page.addScriptToEvaluateOnLoad': return { scriptSource: `globalThis.__slicc503 = ${JSON.stringify(marker)}; addEventListener('pageshow', (e) => { globalThis.__slicc503Restored = e.persisted; });` };
    case 'Runtime.addBinding': return { name: '' };
    case 'Page.setBypassCSP': return { enabled: true };
    default: throw new Error(`unknown capability ${method}`);
  }
}

async function evaluate(reader, session, expression, contextId) {
  const reply = await reader.send('Runtime.evaluate', { expression, contextId, awaitPromise: true, returnByValue: true }, session);
  if (reply.exceptionDetails) throw new Error(`observer evaluation: ${JSON.stringify(reply.exceptionDetails)}`);
  return reply.result.value;
}

async function probe(reader, session, method, marker, contextId) {
  const identity = await evaluate(reader, session, `({ href: location.href, origin: self.origin, marker: document.body?.dataset.marker ?? '', restored: globalThis.__slicc503ObserverRestore === true })`, contextId);
  let expression;
  switch (method) {
    case 'Page.addScriptToEvaluateOnNewDocument':
    case 'Page.addScriptToEvaluateOnLoad': expression = `({ effect: globalThis.__slicc503 === ${JSON.stringify(marker)}, value: globalThis.__slicc503 ?? null, restoredHook: globalThis.__slicc503Restored === true })`; break;
    case 'Runtime.addBinding': expression = `(() => { const exists = typeof globalThis[''] === 'function'; if (exists) globalThis[''](${JSON.stringify(marker)}); return { effect: exists, invoked: exists }; })()`; break;
    case 'Page.setBypassCSP': expression = `(() => { delete globalThis.__slicc503Inline; const s = document.createElement('script'); s.textContent = 'globalThis.__slicc503Inline = ' + ${JSON.stringify(JSON.stringify(marker))}; document.body.append(s); return { effect: globalThis.__slicc503Inline === ${JSON.stringify(marker)} }; })()`; break;
    default: throw new Error(`unmeasured capability ${method}`);
  }
  const observed = await evaluate(reader, session, expression, contextId);
  if (typeof observed?.effect !== 'boolean') throw new Error('observer did not return a measured effect');
  return { ...identity, ...observed };
}

export async function auditSetter(gate, sessionId, wireId, setup) {
  const response = await fetch(gate, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ action: 'audit', sessionId, probes: [{ id: 1, wireId }] }) });
  if (!response.ok) throw new Error(`setter audit HTTP ${response.status}: ${await response.text()}`);
  const audit = await response.json();
  audit.complete = audit.probes?.length === 1 && audit.probes[0].id === 1 && typeof audit.probes[0].forwarded === 'boolean';
  if (!audit.complete || (setup.ok && !audit.probes[0].forwarded)) throw new Error('setter forwarding audit missing or inconsistent');
  return audit;
}

export async function laterAdmissionProof(getLink, opts) {
  const client = await CdpClient.open(getLink(), opts.browser);
  client.callTimeout = 15000;
  const { targetId, sessionId } = await client.attachTo(opts.ordinary+'/doc');
  try {
    await client.send('Page.enable', {}, sessionId);
    const rows = [];
    for (const method of persistentMethods) {
      const before = client.sent.length;
      const setup = await outcome(client.send(method, configuration(method, 'refusal-proof'), sessionId));
      const audit = await auditSetter(opts.gate, sessionId, client.sent[before].id, setup);
      rows.push({ method, setup, audit });
    }
    return rows;
  } finally { await outcome(client.send('Target.closeTarget', { targetId })); await client.close(); }
}

export async function measurePersistent(getLink, opts) {
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 15000;
  const client = opts.leg === 'guarded' ? await CdpClient.open(getLink(), opts.browser) : reader;
  client.callTimeout = 15000;
  let targetId;
  try {
    const ordinary = `${opts.ordinary}/doc?marker=${opts.marker}&stage=ordinary`;
    const protectedURL = `${opts.protected}/doc?marker=${opts.marker}&stage=protected`;
    ({ targetId } = await client.send('Target.createTarget', { url: ordinary, background: true }));
    const { sessionId } = await client.send('Target.attachToTarget', { targetId, flatten: true });
    const observer = client === reader ? sessionId : (await reader.send('Target.attachToTarget', { targetId, flatten: true })).sessionId;
    await reader.send('Page.enable', {}, observer);
    await reader.send('Runtime.enable', {}, observer);
    await client.send('Page.enable', {}, sessionId);
    await client.send('Network.enable', {}, sessionId);
    const navigate = async (url) => {
      const start = reader.events.length;
      const reply = await outcome(reader.send('Page.navigate', { url }, observer));
      const event = await until(() => reader.events.slice(start).find((e) => e.sessionId === observer && e.method === 'Page.frameNavigated' && e.params.frame.id === targetId), `commit of ${url}`);
      const blocked = event.params.frame.unreachableUrl === url && reply.result?.errorText === 'net::ERR_BLOCKED_BY_CLIENT';
      if (blocked && !url.includes('/fail')) return { reply, frame: event.params.frame, type: event.params.type, blocked: true };
      if (url.includes('/fail')) {
        if (event.params.frame.unreachableUrl !== url || event.params.frame.url !== 'chrome-error://chromewebdata/') throw new Error('protected error commit not independently observed');
      } else {
        await until(async () => (await evaluate(reader, observer, 'location.href')) === url, `document at ${url}`);
        await until(async () => (await evaluate(reader, observer, 'document.readyState')) === 'complete', `loaded document at ${url}`);
      }
      return { reply, frame: event.params.frame, type: event.params.type };
    };
    await until(async () => (await evaluate(reader, observer, 'document.readyState')) === 'complete', 'initial ordinary document');
    const before = client.sent.length;
    const setup = opts.leg === 'baseline' ? null : await outcome(client.send(opts.method, configuration(opts.method, opts.marker), sessionId));
    const call = client.sent[before];
    if (setup && !setup.ok && !Number.isInteger(setup.error?.code)) throw new Error(`setter has no structured terminal reply: ${JSON.stringify(setup)}`);
    let audit = { applicable: false, transport: 'direct-observer' };
    if (client !== reader) {
      audit = await auditSetter(opts.gate, sessionId, call.id, setup);
    }
    if (setup && !setup.ok && setup.error.code === -32601) return { method: opts.method, mode: opts.mode, leg: opts.leg, supported: false, setup, audit };
    if (opts.mode === 'admission') return { method: opts.method, mode: opts.mode, leg: opts.leg, supported: true, setup, audit };
    const ordinaryCommit = await navigate(`${ordinary}&reload=1`);
    const ordinaryProbe = await probe(reader, observer, opts.method, opts.marker);
    let commit, contextId;
    if (opts.mode.startsWith('inherited-')) {
      const kind = opts.mode.endsWith('blank') ? 'blank' : 'srcdoc';
      const parent = `${opts.protected}/parent?marker=${opts.marker}&kind=${kind}`;
      await evaluate(reader, observer, `(() => { const f = document.createElement('iframe'); f.src = ${JSON.stringify(parent)}; document.body.append(f); return true; })()`);
      const expected = kind === 'blank' ? 'about:blank' : 'about:srcdoc';
      const layout = await until(async () => {
        const failure = reader.events.findLast((e) => e.sessionId === observer && e.method === 'Page.frameNavigated' && e.params.frame.unreachableUrl === parent && e.params.frame.url === 'chrome-error://chromewebdata/');
        if (failure) return { blocked: true, frame: failure.params.frame };
        const { frameTree } = await reader.send('Page.getFrameTree', {}, observer);
        const top = frameTree.childFrames?.find((f) => f.frame.url === parent || f.frame.unreachableUrl === parent);
        if (top?.frame.unreachableUrl === parent) return { blocked: true, frame: top.frame };
        const child = top?.childFrames?.find((f) => f.frame.name === 'inherited503' && f.frame.url === expected);
        return child && { parent: top.frame, child: child.frame };
      }, 'protected inherited child frame');
      if (layout.blocked) return { method: opts.method, mode: opts.mode, leg: opts.leg, supported: true, setup, audit, ordinaryCommit, ordinaryProbe, boundaryBlocked: true, commit: layout };
      if (layout.child.url !== expected) throw new Error('inherited frame URL not observed');
      const context = await until(async () => {
        const candidate = reader.events.findLast((e) => e.sessionId === observer && e.method === 'Runtime.executionContextCreated' && e.params.context.auxData?.isDefault && e.params.context.auxData.frameId === layout.child.id)?.params.context;
        return candidate && (await evaluate(reader, observer, 'location.href', candidate.id)) === expected && candidate;
      }, 'committed inherited default execution context');
      contextId = context.id;
      const origin = await evaluate(reader, observer, 'self.origin', contextId);
      if (origin !== opts.protected) throw new Error(`inherited child did not have protected origin: ${origin}`);
      commit = { ...layout, origin, contextId };
    } else if (opts.mode === 'error') {
      commit = await navigate(`${opts.protected}/fail?marker=${opts.marker}`);
    } else {
      const seed = await navigate(protectedURL);
      if (seed.blocked) return { method: opts.method, mode: opts.mode, leg: opts.leg, supported: true, setup, audit, ordinaryCommit, ordinaryProbe, boundaryBlocked: true, commit: seed };
      if (opts.mode === 'plain') commit = seed;
      else {
        await evaluate(reader, observer, opts.mode === 'bfcache' ? `addEventListener('pageshow', (e) => { globalThis.__slicc503ObserverRestore = e.persisted; }); true` : `addEventListener('unload', () => {}); true`);
        const { entries, currentIndex } = await reader.send('Page.getNavigationHistory', {}, observer);
        const entry = entries[currentIndex];
        if (entry.url !== protectedURL) throw new Error('protected history entry not observed');
        await navigate(`${ordinary}&exit=1`);
        const start = reader.events.length;
        await reader.send('Page.navigateToHistoryEntry', { entryId: entry.id }, observer);
        const event = await until(() => reader.events.slice(start).find((e) => e.sessionId === observer && e.method === 'Page.frameNavigated' && e.params.frame.id === targetId && e.params.frame.url === protectedURL), 'protected history restore');
        const restored = await until(async () => (await evaluate(reader, observer, 'document.readyState')) === 'complete', 'restored protected document');
        const bfcache = event.params.type === 'BackForwardCacheRestore';
        if (opts.mode === 'bfcache' && (!bfcache || !(await evaluate(reader, observer, 'globalThis.__slicc503ObserverRestore === true')))) throw new Error('real protected BFCache restore not observed');
        if (opts.mode === 'history' && bfcache) throw new Error('history row unexpectedly used BFCache');
        commit = { seed, entry, frame: event.params.frame, type: event.params.type, bfcache, restored };
      }
    }
    const protectedProbe = await probe(reader, observer, opts.method, opts.marker, contextId);
    if (opts.mode === 'bfcache' && opts.method.startsWith('Page.addScriptToEvaluate')) {
      protectedProbe.seedMarkerPresent = protectedProbe.effect;
      protectedProbe.effect = protectedProbe.effect && protectedProbe.restoredHook;
    }
    if (opts.mode !== 'error' && protectedProbe.origin !== opts.protected) throw new Error('protected probe did not execute in the protected origin');
    return { method: opts.method, mode: opts.mode, leg: opts.leg, supported: true, setup, audit, ordinaryCommit, ordinaryProbe, commit, protectedProbe };
  } finally {
    if (targetId) await outcome(reader.send('Target.closeTarget', { targetId }));
    if (client !== reader) await client.close();
    reader.close();
  }
}
