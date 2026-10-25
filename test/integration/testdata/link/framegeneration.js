import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { evaluate, outcome, until } from './persistent.js';

export async function measureFrameGeneration(getLink, opts) {
  const reader = await SocketClient.open(opts.ws);
  let client, targetId, context;
  try {
    ({ browserContextId: context } = await reader.send('Target.createBrowserContext'));
    ({ targetId } = await reader.send('Target.createTarget', { url: opts.ordinary + '/framed', browserContextId: context }));
    const { sessionId: observer } = await reader.send('Target.attachToTarget', { targetId, flatten: true });
    await reader.send('Page.enable', {}, observer);
    await reader.send('Target.activateTarget', { targetId });
    const old = await until(async () => {
      const tree = (await reader.send('Page.getFrameTree', {}, observer)).frameTree;
      return tree.childFrames?.some((f) => f.frame.url === opts.protected + '/child') && tree;
    }, 'protected OOPIF document');
    const child = old.childFrames.find((f) => f.frame.url === opts.protected + '/child').frame.id;
    const info = (await reader.send('Target.getTargetInfo', { targetId: child })).targetInfo;
    const owned = await outcome(reader.send('DOM.getFrameOwner', { frameId: child }, observer));
    if (info.type !== 'iframe' || info.parentFrameId !== targetId || !owned.ok || !owned.result.backendNodeId) throw new Error('current protected OOPIF ownership missing');
    await evaluate(reader, observer, "addEventListener('pageshow', (e) => { globalThis.restored = e.persisted; }); true");
    const history = await reader.send('Page.getNavigationHistory', {}, observer);
    const entry = history.entries[history.currentIndex];
    await reader.send('Page.navigate', { url: opts.ordinary + '/empty' }, observer);
    const next = await until(async () => {
      const tree = (await reader.send('Page.getFrameTree', {}, observer)).frameTree;
      return tree.frame.url === opts.ordinary + '/empty' && !tree.childFrames?.length && !tree.frame.unreachableUrl && tree;
    }, 'new ordinary document');
    const frozen = (await reader.send('Target.getTargetInfo', { targetId: child })).targetInfo;
    const absent = await outcome(reader.send('DOM.getFrameOwner', { frameId: child }, observer));
    if (old.frame.loaderId === next.frame.loaderId || frozen.parentFrameId !== targetId || absent.ok) throw new Error('frozen OOPIF ownership was not distinguished');
    client = await CdpClient.open(getLink(), opts.browser);
    const { sessionId } = await client.send('Target.attachToTarget', { targetId, flatten: true });
    await client.send('Page.enable', {}, sessionId);
    const start = Date.now();
    let tree;
    await until(async () => {
      tree = await outcome(client.send('Page.getFrameTree', {}, sessionId));
      return tree.ok;
    }, 'ordinary frame tree through relay', 5000).catch(() => {});
    const dom = await outcome(client.send('DOM.getDocument', {}, sessionId));
    const shot = await outcome(client.send('Page.captureScreenshot', {}, sessionId));
    const input = await outcome(client.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 1, y: 1 }, sessionId));
    const ms = Date.now() - start;
    const committed = reader.waitEvent((e) => e.sessionId === observer && e.method === 'Page.frameNavigated' && e.params.frame.id === targetId);
    const relayCommit = client.waitEvent((e) => e.sessionId === sessionId && e.method === 'Page.frameNavigated' && e.params.frame.id === targetId);
    await reader.send('Page.navigateToHistoryEntry', { entryId: entry.id }, observer);
    const restore = await committed;
    await relayCommit;
    if (restore.params.type !== 'BackForwardCacheRestore' || restore.params.frame.loaderId !== old.frame.loaderId) throw new Error('protected-OOPIF document did not restore from BFCache');
    const restoredOwner = await outcome(reader.send('DOM.getFrameOwner', { frameId: child }, observer));
    const back = [];
    for (const [method, params] of [
      ['Page.getFrameTree', {}], ['DOM.getDocument', {}], ['Page.captureScreenshot', {}],
      ['Input.dispatchMouseEvent', { type: 'mouseMoved', x: 1, y: 1 }],
    ]) back.push(await outcome(client.send(method, params, sessionId)));
    return { old, next, info, frozen, owned, absent, restoredOwner, restore: restore.params, tree, dom, shot: { ok: shot.ok, error: shot.error }, input, ms, back };
  } finally {
    if (client) await client.close();
    if (context) await outcome(reader.send('Target.disposeBrowserContext', { browserContextId: context }));
    reader.close();
  }
}
