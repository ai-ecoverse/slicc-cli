import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { outcome, until } from './persistent.js';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export const eventDomains = ['Accessibility', 'DOM', 'CSS', 'Overlay', 'Animation', 'LayerTree', 'Page', 'Runtime', 'Log'];

export function eventsShowing(events, needle) {
  const out = {};
  for (const e of events) {
    if (!JSON.stringify(e.params ?? {}).includes(needle)) continue;
    const domain = e.method.split('.')[0];
    out[domain] ??= [];
    if (!out[domain].includes(e.method)) out[domain].push(e.method);
  }
  return out;
}

async function nested(reader, session, kind, needle) {
  const want = kind === 'srcdoc' ? 'about:srcdoc' : 'about:blank';
  return until(async () => {
    const { frameTree } = await reader.send('Page.getFrameTree', {}, session);
    const nest = frameTree.childFrames?.[0];
    const child = nest?.childFrames?.find((f) => f.frame.name === 'child' && f.frame.url === want);
    if (!child) return undefined;
    const context = reader.events.findLast((e) => e.sessionId === session && e.method === 'Runtime.executionContextCreated' && e.params.context.auxData?.isDefault && e.params.context.auxData.frameId === child.frame.id);
    if (!context) return undefined;
    const text = await outcome(reader.send('Runtime.evaluate', { expression: 'document.body?.textContent ?? ""', contextId: context.params.context.id, returnByValue: true }, session));
    if (!text.ok || !String(text.result.result?.value).includes(needle)) return undefined;
    return { nest: nest.frame.securityOrigin, child: context.params.context.origin, url: child.frame.url, frameId: nest.frame.id };
  }, `nested ${want} frame showing its text`);
}

async function enable(client, session) {
  const out = {};
  for (const method of ['Page.enable', 'DOM.enable', 'CSS.enable', 'Accessibility.enable', 'Overlay.enable', 'Animation.enable', 'LayerTree.enable', 'Log.enable']) {
    const o = await outcome(client.send(method, {}, session));
    out[method] = o.ok ? '' : o.error?.message ?? 'failed';
  }
  const doc = await outcome(client.send('DOM.getDocument', { depth: -1, pierce: true }, session));
  out['DOM.getDocument'] = doc.ok ? '' : doc.error?.message ?? 'failed';
  const ax = await outcome(client.send('Accessibility.getFullAXTree', {}, session));
  out['Accessibility.getFullAXTree'] = ax.ok ? '' : ax.error?.message ?? 'failed';
  return out;
}

export async function measureFrameEvents(getLink, opts) {
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 15000;
  const guarded = opts.leg !== 'control';
  let client = reader;
  let targetId;
  try {
    ({ targetId } = await reader.send('Target.createTarget', { url: opts.mode === 'running' ? opts.initial : opts.page }));
    const observer = (await reader.send('Target.attachToTarget', { targetId, flatten: true })).sessionId;
    await reader.send('Page.enable', {}, observer);
    await reader.send('Runtime.enable', {}, observer);
    await reader.send('Target.activateTarget', { targetId });
    const initial = await nested(reader, observer, opts.kind, opts.needle);
    let session = observer;
    if (guarded) {
      client = await CdpClient.open(getLink(), opts.browser);
      client.callTimeout = 15000;
      ({ sessionId: session } = await client.send('Target.attachToTarget', { targetId, flatten: true }));
    }
    const mark = client.events.length;
    const enabled = await enable(client, session);
    await sleep(500);
    const before = eventsShowing(client.events.slice(mark).filter((e) => e.sessionId === session), opts.needle);
    const afterEnable = client.events.length;
    await reader.send('Page.navigate', { url: opts.nest, frameId: initial.frameId }, observer);
    const setup = { layout: await nested(reader, observer, opts.kind, opts.needle) };
    const row = { mode: opts.mode, kind: opts.kind, leg: opts.leg, setup, enabled, before };
    await sleep(3000);
    const mine = client.events.slice(afterEnable).filter((e) => e.sessionId === session);
    row.events = mine.length;
    row.methods = {};
    for (const e of mine) row.methods[e.method] = (row.methods[e.method] ?? 0) + 1;
    row.shown = eventsShowing(mine, opts.needle);
    return row;
  } finally {
    if (targetId) await outcome(reader.send('Target.closeTarget', { targetId }));
    if (client !== reader) await client.close();
    reader.close();
  }
}
