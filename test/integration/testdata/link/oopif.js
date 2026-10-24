import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { outcome, until } from './persistent.js';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function pixels(data) {
  const bitmap = await createImageBitmap(new Blob([Uint8Array.from(atob(data), (c) => c.charCodeAt(0))], { type: 'image/png' }));
  const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
  const ctx = canvas.getContext('2d');
  ctx.drawImage(bitmap, 0, 0);
  const bytes = ctx.getImageData(0, 0, bitmap.width, bitmap.height).data;
  bitmap.close();
  let count = 0;
  for (let i = 0; i < bytes.length; i += 4) if (bytes[i] > 200 && bytes[i + 1] < 80 && bytes[i + 2] > 200) count += 1;
  return count;
}

async function shot(client, session) {
  const o = await outcome(client.send('Page.captureScreenshot', { format: 'png' }, session));
  return { ok: o.ok, error: o.error?.message ?? '', marked: o.ok ? await pixels(o.result.data) : 0 };
}

export async function measureOrdinaryBesideProtected(getLink, opts) {
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 15000;
  const client = await CdpClient.open(getLink(), opts.browser);
  client.callTimeout = 15000;
  try {
    const infos = (await reader.send('Target.getTargets', { filter: [{}] })).targetInfos;
    const info = infos.find((t) => t.type === 'iframe' && t.url === opts.vault);
    if (!info) throw new Error(`vault iframe missing: ${JSON.stringify(infos)}`);
    const observer = (await reader.send('Target.attachToTarget', { targetId: opts.ordinaryId, flatten: true })).sessionId;
    await reader.send('Target.activateTarget', { targetId: opts.ordinaryId });
    const setup = await shot(reader, observer);
    const { sessionId: session } = await client.send('Target.attachToTarget', { targetId: opts.ordinaryId, flatten: true });
    const screenshot = await shot(client, session);
    const document = await outcome(client.send('DOM.getDocument', { depth: -1, pierce: true }, session));
    const key = await outcome(client.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'x', code: 'KeyX', text: 'x' }, session));
    await outcome(client.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'x', code: 'KeyX' }, session));
    const keys = await client.send('Runtime.evaluate', { expression: 'globalThis.keys', returnByValue: true }, session);
    const frames = [];
    client.on((e) => {
      if (e.method !== 'Page.screencastFrame' || e.sessionId !== session) return;
      frames.push(e.params.data);
      client.send('Page.screencastFrameAck', { sessionId: e.params.sessionId }, session).catch(() => {});
    });
    const start = await outcome(client.send('Page.startScreencast', { format: 'png', everyNthFrame: 1 }, session));
    await sleep(1500);
    await outcome(client.send('Page.stopScreencast', {}, session));
    const marks = await Promise.all(frames.map(pixels));
    const protectedAttach = await outcome(client.send('Target.attachToTarget', { targetId: opts.protectedId, flatten: true }));
    const targetEvents = client.events.filter((e) => e.method.startsWith('Target.'));
    const probeLeaks = targetEvents.filter((e) => e.params?.targetInfo?.targetId === opts.protectedId || e.params?.targetInfo?.targetId === info.targetId || e.params?.targetId === opts.protectedId || (e.sessionId && e.sessionId !== session) || (['Target.attachedToTarget', 'Target.detachedFromTarget'].includes(e.method) && e.params?.sessionId !== session));
    let parentSetup;
    let parentScreenshot;
    if (opts.parentId) {
      const rawParent = (await reader.send('Target.attachToTarget', { targetId: opts.parentId, flatten: true })).sessionId;
      parentSetup = await shot(reader, rawParent);
      const parent = (await client.send('Target.attachToTarget', { targetId: opts.parentId, flatten: true })).sessionId;
      parentScreenshot = await shot(client, parent);
    }
    return { mode: opts.mode, info, setup, screenshot, targetEvents, probeLeaks, parentSetup, parentScreenshot, document: { ok: document.ok, error: document.error?.message ?? '', shown: document.ok && JSON.stringify(document.result).includes('daily-marker') }, input: { ok: key.ok, error: key.error?.message ?? '', keys: keys.result.value }, cast: { ok: start.ok, error: start.error?.message ?? '', frames: frames.length, marked: marks.filter((n) => n >= 1000).length }, protectedAttach: { ok: protectedAttach.ok, error: protectedAttach.error?.message ?? '' } };
  } finally {
    await client.close();
    reader.close();
  }
}

export async function measureOOPIF(getLink, opts) {
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 15000;
  await reader.send('Target.setDiscoverTargets', { discover: true, filter: [{}] });
  let client = reader;
  try {
    const observer = (await reader.send('Target.attachToTarget', { targetId: opts.targetId, flatten: true })).sessionId;
    await reader.send('Page.enable', {}, observer);
    await reader.send('Runtime.enable', {}, observer);
    await reader.send('Target.activateTarget', { targetId: opts.targetId });
    let allTargets;
    const info = await until(async () => {
      allTargets = (await reader.send('Target.getTargets', { filter: [{}] })).targetInfos;
      return allTargets.find((t) => t.type === 'iframe' && t.url === opts.child);
    }, 'preloaded iframe target').catch((error) => { throw new Error(`${error.message}: ${JSON.stringify(allTargets)}`); });
    let painted = 0;
    await until(async () => { painted = (await shot(reader, observer)).marked; return painted >= 1000; }, 'preloaded iframe paint');
    let session = observer;
    if (opts.leg !== 'control') {
      client = await CdpClient.open(getLink(), opts.browser);
      client.callTimeout = 15000;
      ({ sessionId: session } = await client.send('Target.attachToTarget', { targetId: opts.targetId, flatten: true }));
    }
    const frames = [];
    client.on((e) => {
      if (e.method !== 'Page.screencastFrame' || e.sessionId !== session) return;
      frames.push(e.params.data);
      client.send('Page.screencastFrameAck', { sessionId: e.params.sessionId }, session).catch(() => {});
    });
    const row = { leg: opts.leg, info, painted, screenshot: await shot(client, session), probes: {} };
    for (const [method, params] of [
      ['LayerTree.makeSnapshot', { layerId: 'missing' }],
      ['LayerTree.loadSnapshot', { tiles: [] }],
      ['LayerTree.replaySnapshot', { snapshotId: 'missing' }],
      ['LayerTree.profileSnapshot', { snapshotId: 'missing' }],
      ['LayerTree.snapshotCommandLog', { snapshotId: 'missing' }],
      ['HeadlessExperimental.beginFrame', { screenshot: { format: 'png' } }],
      ['Cast.startTabMirroring', { sinkName: 'missing' }],
    ]) {
      const o = await outcome(client.send(method, params, session));
      row.probes[method] = { ok: o.ok, error: o.error?.message ?? '' };
    }
    const start = await outcome(client.send('Page.startScreencast', { format: 'png', everyNthFrame: 1 }, session));
    await sleep(1500);
    await outcome(client.send('Page.stopScreencast', {}, session));
    const marks = await Promise.all(frames.map(pixels));
    row.cast = { ok: start.ok, error: start.error?.message ?? '', frames: frames.length, marked: marks.filter((n) => n >= 1000).length };
    const navStart = reader.events.length;
    await reader.send('Page.navigate', { url: opts.plain }, observer);
    await until(() => reader.events.slice(navStart).find((e) => e.sessionId === observer && e.method === 'Page.loadEventFired'), 'away page load');
    row.frozen = (await reader.send('Target.getTargets', { filter: [{}] })).targetInfos.filter((t) => t.type === 'iframe');
    row.away = await shot(client, session);
    const history = await reader.send('Page.getNavigationHistory', {}, observer);
    const restoreStart = reader.events.length;
    await reader.send('Page.navigateToHistoryEntry', { entryId: history.entries[history.currentIndex - 1].id }, observer);
    const restore = await until(() => reader.events.slice(restoreStart).find((e) => e.sessionId === observer && e.method === 'Page.frameNavigated' && e.params.frame.id === opts.targetId), 'restore iframe parent');
    row.restoreType = restore.params.type;
    await until(async () => (await shot(reader, observer)).marked >= 1000, 'restored iframe paint');
    row.restored = await shot(client, session);
    await reader.send('Runtime.evaluate', { expression: "document.querySelector('iframe').remove()" }, observer);
    await until(async () => !(await reader.send('Target.getTargets', { filter: [{}] })).targetInfos.some((t) => t.targetId === info.targetId || t.url === opts.child), 'iframe target gone');
    await sleep(200);
    row.removed = await shot(client, session);
    row.live = await client.send('Runtime.evaluate', { expression: '21 * 2', returnByValue: true }, session);
    return row;
  } finally {
    if (client !== reader) await client.close();
    reader.close();
  }
}
