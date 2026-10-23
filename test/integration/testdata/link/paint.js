import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { outcome, until } from './persistent.js';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export const paintThreshold = 1000;

export async function markerPixels(data) {
  const bytes = Uint8Array.from(atob(data), (c) => c.charCodeAt(0));
  const bitmap = await createImageBitmap(new Blob([bytes], { type: 'image/png' }));
  const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
  const ctx = canvas.getContext('2d');
  ctx.drawImage(bitmap, 0, 0);
  const px = ctx.getImageData(0, 0, bitmap.width, bitmap.height).data;
  let n = 0;
  for (let i = 0; i < px.length; i += 4) if (px[i] > 200 && px[i + 1] < 80 && px[i + 2] > 200) n += 1;
  return n;
}

async function shot(reader, session) {
  const r = await reader.send('Page.captureScreenshot', { format: 'png' }, session);
  return markerPixels(r.data);
}

function record(client, session) {
  const frames = [];
  client.on((e) => {
    if (e.method !== 'Page.screencastFrame' || e.sessionId !== session) return;
    frames.push({ data: e.params.data, at: Date.now() });
    client.send('Page.screencastFrameAck', { sessionId: e.params.sessionId }, session).catch(() => {});
  });
  return frames;
}

async function tally(frames) {
  let marked = 0;
  let most = 0;
  for (const f of frames) {
    const n = await markerPixels(f.data);
    if (n >= paintThreshold) marked += 1;
    most = Math.max(most, n);
  }
  return { frames: frames.length, marked, most };
}

const errorOf = (o) => (o.ok ? '' : o.error?.message ?? '');

async function nested(reader, session, kind) {
  const want = kind === 'srcdoc' ? 'about:srcdoc' : 'about:blank';
  return until(async () => {
    const { frameTree } = await reader.send('Page.getFrameTree', {}, session);
    const nest = frameTree.childFrames?.[0];
    const child = nest?.childFrames?.find((f) => f.frame.name === 'child' && f.frame.url === want);
    return child && { nest: nest.frame.securityOrigin, child: child.frame.securityOrigin ?? '', url: child.frame.url };
  }, `nested ${want} frame`);
}

async function painted(reader, session) {
  let most = 0;
  await until(async () => {
    most = await shot(reader, session);
    return most >= paintThreshold;
  }, 'marker painted in the nested frame');
  return most;
}

async function castOn(client, session, ms) {
  const frames = record(client, session);
  const start = await outcome(client.send('Page.startScreencast', { format: 'png', everyNthFrame: 1 }, session));
  await sleep(ms);
  const stop = await outcome(client.send('Page.stopScreencast', {}, session));
  return { start: { ok: start.ok, error: errorOf(start) }, stop: stop.ok, ...(await tally(frames)) };
}

async function layers(reader, observer, client, session) {
  const start = reader.events.length;
  await reader.send('LayerTree.enable', {}, observer);
  const changed = await until(() => reader.events.slice(start).find((e) => e.sessionId === observer && e.method === 'LayerTree.layerTreeDidChange' && e.params.layers?.some((l) => l.drawsContent)), 'a layer tree with painted layers');
  const ids = changed.params.layers.filter((l) => l.drawsContent).map((l) => l.layerId);
  if (client !== reader) await outcome(client.send('LayerTree.enable', {}, session));
  const out = { layers: ids.length, made: 0, replayed: 0, marked: 0, most: 0, profiled: 0, logged: 0, logShown: false, errors: [] };
  const note = (o) => { const m = errorOf(o); if (m && !out.errors.includes(m)) out.errors.push(m); };
  for (const layerId of ids) {
    const made = await outcome(client.send('LayerTree.makeSnapshot', { layerId }, session));
    note(made);
    if (!made.ok) continue;
    out.made += 1;
    const snapshotId = made.result.snapshotId;
    const replay = await outcome(client.send('LayerTree.replaySnapshot', { snapshotId }, session));
    note(replay);
    if (replay.ok) {
      out.replayed += 1;
      const n = await markerPixels(replay.result.dataURL.replace(/^data:[^,]*,/, ''));
      if (n >= paintThreshold) out.marked += 1;
      out.most = Math.max(out.most, n);
    }
    const profile = await outcome(client.send('LayerTree.profileSnapshot', { snapshotId, minRepeatCount: 1 }, session));
    note(profile);
    if (profile.ok) out.profiled += 1;
    const log = await outcome(client.send('LayerTree.snapshotCommandLog', { snapshotId }, session));
    note(log);
    if (log.ok) {
      out.logged += 1;
      out.logShown ||= /ff00ff/i.test(JSON.stringify(log.result));
    }
    await outcome(client.send('LayerTree.releaseSnapshot', { snapshotId }, session));
  }
  return out;
}

async function restore(reader, observer, targetId) {
  const { entries, currentIndex } = await reader.send('Page.getNavigationHistory', {}, observer);
  const start = reader.events.length;
  await reader.send('Page.navigateToHistoryEntry', { entryId: entries[currentIndex - 1].id }, observer);
  const event = await until(() => reader.events.slice(start).find((e) => e.sessionId === observer && e.method === 'Page.frameNavigated' && e.params.frame.id === targetId), 'history restore');
  return event.params.type;
}

export async function measurePaint(getLink, opts) {
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 15000;
  const guarded = opts.leg !== 'control';
  let client = reader;
  let targetId;
  try {
    const first = opts.mode === 'running' ? opts.plain : opts.page;
    ({ targetId } = await reader.send('Target.createTarget', { url: opts.page }));
    const observer = (await reader.send('Target.attachToTarget', { targetId, flatten: true })).sessionId;
    await reader.send('Page.enable', {}, observer);
    await reader.send('Runtime.enable', {}, observer);
    await reader.send('Target.activateTarget', { targetId });
    const layout = await nested(reader, observer, opts.kind);
    const setup = { layout, painted: await painted(reader, observer) };
    if (first !== opts.page) {
      const start = reader.events.length;
      await reader.send('Page.navigate', { url: first }, observer);
      await until(() => reader.events.slice(start).find((e) => e.sessionId === observer && e.method === 'Page.loadEventFired'), 'plain page load');
      setup.away = await shot(reader, observer);
    }
    let session = observer;
    if (guarded) {
      client = await CdpClient.open(getLink(), opts.browser);
      client.callTimeout = 15000;
      ({ sessionId: session } = await client.send('Target.attachToTarget', { targetId, flatten: true }));
      await outcome(client.send('Page.enable', {}, session));
    }
    const row = { mode: opts.mode, kind: opts.kind, leg: opts.leg, setup };
    if (opts.mode === 'start') row.cast = await castOn(client, session, 3000);
    else if (opts.mode === 'layers') row.layers = await layers(reader, observer, client, session);
    else {
      const frames = record(client, session);
      const start = await outcome(client.send('Page.startScreencast', { format: 'png', everyNthFrame: 1 }, session));
      await until(() => frames.length > 0, 'a frame before the restore').catch(() => null);
      const before = frames.length;
      row.restoreType = await restore(reader, observer, targetId);
      await nested(reader, observer, opts.kind);
      row.restoredPaint = await painted(reader, observer);
      const restoredAt = Date.now();
      await sleep(3000);
      const stop = await outcome(client.send('Page.stopScreencast', {}, session));
      row.cast = { start: { ok: start.ok, error: errorOf(start) }, stop: stop.ok, before, beforeMarked: (await tally(frames.slice(0, before))).marked, since: await tally(frames.slice(before)), after: await tally(frames.filter((f) => f.at >= restoredAt)) };
    }
    return row;
  } finally {
    if (targetId) await outcome(reader.send('Target.closeTarget', { targetId }));
    if (client !== reader) await client.close();
    reader.close();
  }
}
