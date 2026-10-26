import { CdpClient } from './cdp.js';

let state;

export async function errorContent(getLink, opts) {
  if (opts.action === 'attach') {
    const c = await CdpClient.open(getLink(), opts.browser);
    c.callTimeout = 15000;
    const { sessionId } = await c.send('Target.attachToTarget', { targetId: opts.targetId, flatten: true });
    await c.send('Page.enable', {}, sessionId);
    state = { c, sessionId, targetId: opts.targetId, start: c.events.length };
    return { attached: true };
  }
  const { c, sessionId, targetId } = state;
  if (opts.action === 'arm') {
    state.start = c.events.length;
    return { armed: true };
  }
  if (opts.action === 'close') {
    await c.send('Target.detachFromTarget', { sessionId });
    const closed = await c.close();
    state = undefined;
    return { closed };
  }
  if (opts.action !== 'read') throw new Error('unknown error document action');
  const commit = await c.waitEvent((e) => c.events.indexOf(e) >= state.start && e.sessionId === sessionId && e.method === 'Page.frameNavigated' && e.params.frame.id === targetId && e.params.frame.url === 'chrome-error://chromewebdata/');
  const reads = {};
  const read = async (name, method, params, usable) => {
    try {
      const reply = await c.send(method, params, sessionId);
      reads[name] = { ok: true, usable: usable(reply) };
    } catch (error) {
      reads[name] = { ok: false, error: error.cdp ?? { message: String(error?.message ?? error) } };
    }
  };
  for (const [name, expression] of [
    ['title', 'document.title'],
    ['loadTimeData', 'globalThis.loadTimeData?.data_ ?? null'],
    ['reloadURL', "document.querySelector('#reload-button')?.getAttribute('data-url') ?? null"],
  ]) await read(name, 'Runtime.evaluate', { expression, returnByValue: true }, (r) => !!r.result && !r.exceptionDetails);
  await read('document', 'DOM.getDocument', { depth: -1, pierce: true }, (r) => r.root?.nodeId > 0);
  await read('html', 'DOM.getOuterHTML', { backendNodeId: opts.backendNodeId }, (r) => r.outerHTML?.includes('<html'));
  await read('accessibility', 'Accessibility.getFullAXTree', {}, (r) => r.nodes?.length > 0);
  await read('snapshot', 'DOMSnapshot.captureSnapshot', { computedStyles: [] }, (r) => r.documents?.length > 0 && r.strings?.length > 0);
  await read('screenshot', 'Page.captureScreenshot', { format: 'png' }, (r) => r.data?.startsWith('iVBOR'));
  const listed = (await c.send('Target.getTargets')).targetInfos.find((t) => t.targetId === targetId);
  const { targetInfo: info } = await c.send('Target.getTargetInfo', { targetId });
  return { reads, listed, info, navigationType: commit.params.type };
}
