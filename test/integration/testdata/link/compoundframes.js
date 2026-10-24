import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { markerPixels, paintThreshold } from './paint.js';
import { outcome, until } from './persistent.js';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function screenshot(client, session) {
  const o = await outcome(client.send('Page.captureScreenshot', { format: 'png' }, session));
  return { ok: o.ok, error: o.error?.message ?? '', marked: o.ok ? await markerPixels(o.result.data) : 0 };
}

export async function measureCompoundFrames(getLink, opts) {
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 15000;
  let client = reader;
  try {
    await reader.send('Target.setDiscoverTargets', { discover: true, filter: [{}] });
    const info = await until(async () => (await reader.send('Target.getTargets', { filter: [{}] })).targetInfos.find((t) => t.type === 'iframe' && t.url === opts.middle), 'ordinary OOPIF target');
    const observer = (await reader.send('Target.attachToTarget', { targetId: opts.targetId, flatten: true })).sessionId;
    const inner = (await reader.send('Target.attachToTarget', { targetId: info.targetId, flatten: true })).sessionId;
    await reader.send('Runtime.enable', {}, inner);
    await reader.send('Target.activateTarget', { targetId: opts.targetId });
    const layout = await until(async () => {
      const tree = (await reader.send('Page.getFrameTree', {}, inner)).frameTree;
      const nest = tree.childFrames?.find((f) => f.frame.name === 'nest');
      const child = nest?.childFrames?.find((f) => f.frame.name === 'child');
      if (!child) return undefined;
      const context = reader.events.findLast((e) => e.sessionId === inner && e.method === 'Runtime.executionContextCreated' && e.params.context.auxData?.isDefault && e.params.context.auxData.frameId === child.frame.id);
      return context && { origin: nest.frame.securityOrigin, child: context.params.context.origin, frameId: child.frame.id };
    }, 'protected local context in the ordinary OOPIF renderer');
    const targets = (await reader.send('Target.getTargets', { filter: [{}] })).targetInfos;
    if (targets.some((t) => t.type === 'iframe' && t.url.startsWith(`${opts.protected}/`))) throw new Error('protected child is not in the ordinary iframe process');
    let painted = 0;
    await until(async () => { painted = (await screenshot(reader, observer)).marked; return painted >= paintThreshold; }, 'protected local child painted in the page ancestor');
    let session = observer;
    let ownSession = inner;
    if (opts.leg !== 'control') {
      client = await CdpClient.open(getLink(), opts.browser);
      client.callTimeout = 15000;
      ({ sessionId: session } = await client.send('Target.attachToTarget', { targetId: opts.targetId, flatten: true }));
      ({ sessionId: ownSession } = await client.send('Target.attachToTarget', { targetId: info.targetId, flatten: true }));
      await client.send('Runtime.enable', {}, ownSession);
    }
    const own = await screenshot(client, ownSession);
    const shot = await screenshot(client, session);
    const frames = [];
    client.on((e) => {
      if (e.method !== 'Page.screencastFrame' || e.sessionId !== session) return;
      frames.push(e.params.data);
      client.send('Page.screencastFrameAck', { sessionId: e.params.sessionId }, session).catch(() => {});
    });
    const start = await outcome(client.send('Page.startScreencast', { format: 'png', everyNthFrame: 1 }, session));
    await sleep(1500);
    await outcome(client.send('Page.stopScreencast', {}, session));
    const marks = await Promise.all(frames.map(markerPixels));
    return { leg: opts.leg, kind: opts.kind, info, layout, painted, own, screenshot: shot, cast: { ok: start.ok, error: start.error?.message ?? '', frames: frames.length, marked: marks.filter((n) => n >= paintThreshold).length } };
  } finally {
    if (client !== reader) await client.close();
    reader.close();
  }
}
