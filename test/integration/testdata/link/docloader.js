import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { evaluate, outcome, until } from './persistent.js';

async function warmDocument(reader, sessionId, url) {
  const warm = url.includes('?') ? `${url}&warm=1` : `${url}?warm=1`;
  const nav = await outcome(reader.send('Page.navigate', { url: warm }, sessionId));
  if (!nav.ok) return { ok: false, url: warm, error: nav.error?.message ?? 'navigate' };
  await until(async () => await evaluate(reader, sessionId, 'location.href') === warm && await evaluate(reader, sessionId, 'document.readyState') === 'complete', `warm ${warm}`, 20000);
  return { ok: true, url: warm, error: null };
}

export async function measureDocLoader(getLink, opts) {
  const start = Date.now();
  const steps = [];
  const step = (name, data = {}) => steps.push({ name, ms: Date.now() - start, ...data });
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 15000;
  const client = await CdpClient.open(getLink(), opts.browser);
  client.callTimeout = 20000;
  let targetId = null;
  const method = opts.method || 'Runtime.evaluate';
  try {
    ({ targetId } = await reader.send('Target.createTarget', { url: opts.page, background: true }));
    const { sessionId } = await reader.send('Target.attachToTarget', { targetId, flatten: true });
    await reader.send('Page.enable', {}, sessionId);
    await reader.send('Runtime.enable', {}, sessionId);
    await until(async () => await evaluate(reader, sessionId, 'location.href') === opts.page && await evaluate(reader, sessionId, 'document.readyState') === 'complete', `document ${opts.page}`, 15000);
    step('page-loaded', { targetId, readerSession: sessionId });
    const { sessionId: clientSession } = await client.send('Target.attachToTarget', { targetId, flatten: true });
    await client.send('DOM.enable', {}, clientSession).catch(() => {});
    await client.send('Page.enable', {}, clientSession).catch(() => {});
    const warm = await warmDocument(reader, sessionId, opts.page);
    step('warm-navigate', warm);
    if (!warm.ok) {
      return { steps, targetId, readerSession: sessionId, clientSession, control: false, before: null, racedOk: false, racedValue: null, racedError: warm.error, readerLoaders: [], after: null, usable: false, method, page: opts.page };
    }
    const control = await outcome(client.send('Runtime.evaluate', { expression: 'String(performance.timeOrigin)', returnByValue: true }, clientSession));
    const before = control.ok ? control.result.result?.value ?? null : null;
    step('client-control', { ok: control.ok, before, error: control.ok ? null : control.error?.message ?? null });
    const from = reader.events.length;
    let readPromise;
    if (method === 'DOM.getDocument') {
      readPromise = outcome(client.send('DOM.getDocument', { depth: -1, pierce: true }, clientSession));
    } else if (method === 'DOM.getOuterHTML') {
      const doc = await outcome(client.send('DOM.getDocument', { depth: 0 }, clientSession));
      const nodeId = doc.ok ? doc.result.root?.nodeId : null;
      readPromise = nodeId == null ? Promise.resolve({ ok: false, error: { message: 'no-root' } }) : outcome(client.send('DOM.getOuterHTML', { nodeId }, clientSession));
    } else if (method === 'Page.captureScreenshot') {
      readPromise = outcome(client.send('Page.captureScreenshot', { format: 'png' }, clientSession));
    } else {
      const expression = `(() => { const marker = 'docloader-race'; const end = Date.now() + ${opts.busyMs}; while (Date.now() < end) {} return marker + ' ' + performance.timeOrigin; })()`;
      readPromise = outcome(client.send('Runtime.evaluate', { expression, returnByValue: true }, clientSession));
    }
    await new Promise((resolve) => setTimeout(resolve, opts.reloadAfterMs));
    const reload = await outcome(reader.send('Page.reload', { ignoreCache: true }, sessionId));
    step('reload-sent', { ok: reload.ok, error: reload.ok ? null : reload.error?.message ?? null });
    const raced = await readPromise;
    let racedValue = null;
    let racedError = null;
    if (raced.ok) {
      if (method === 'Runtime.evaluate') {
        racedValue = raced.result.result?.value ?? null;
        racedError = raced.result.exceptionDetails ? 'exception' : null;
      } else if (method === 'DOM.getDocument') {
        racedValue = raced.result.root ? `node:${raced.result.root.nodeId}` : null;
      } else if (method === 'DOM.getOuterHTML') {
        racedValue = typeof raced.result.outerHTML === 'string' ? `html:${raced.result.outerHTML.length}` : null;
      } else if (method === 'Page.captureScreenshot') {
        racedValue = typeof raced.result.data === 'string' ? `png:${raced.result.data.length}` : null;
      }
    } else {
      racedError = raced.error?.message ?? null;
    }
    step('raced', { ok: raced.ok, value: racedValue, error: racedError, method });
    await until(async () => await evaluate(reader, sessionId, 'document.readyState') === 'complete' && String(await evaluate(reader, sessionId, 'performance.timeOrigin')) !== before, 'reloaded document', 20000);
    const loaders = reader.events.slice(from).filter((e) => e.sessionId === sessionId && e.method === 'Page.frameNavigated' && e.params.frame.id === targetId && !e.params.frame.parentId).map((e) => e.params.frame.loaderId);
    let after = null;
    let usable = null;
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      usable = await outcome(client.send('Runtime.evaluate', { expression: 'String(performance.timeOrigin)', returnByValue: true }, clientSession));
      if (usable.ok && !usable.result.exceptionDetails) {
        after = usable.result.result?.value ?? null;
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    step('after', { after, usable: Boolean(usable?.ok), error: usable && !usable.ok ? usable.error?.message ?? null : null });
    step('done');
    return { steps, targetId, readerSession: sessionId, clientSession, control: control.ok, before, racedOk: raced.ok, racedValue, racedError, readerLoaders: loaders, after, usable: after !== null, method, page: warm.url };
  } finally {
    if (targetId) await outcome(reader.send('Target.closeTarget', { targetId }));
    await client.close();
    reader.close();
  }
}

export async function measureDocLoaderKeep(getLink, opts) {
  const start = Date.now();
  const steps = [];
  const step = (name, data = {}) => steps.push({ name, ms: Date.now() - start, ...data });
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 15000;
  const client = await CdpClient.open(getLink(), opts.browser);
  client.callTimeout = 20000;
  let targetId = null;
  const topLoader = async (sessionId) => {
    const tree = await outcome(reader.send('Page.getFrameTree', {}, sessionId));
    return tree.ok ? tree.result.frameTree?.frame?.loaderId ?? null : null;
  };
  const loaded = (sessionId, url) => until(async () => await evaluate(reader, sessionId, 'location.href') === url && await evaluate(reader, sessionId, 'document.readyState') === 'complete', `document ${url}`, 20000);
  try {
    ({ targetId } = await reader.send('Target.createTarget', { url: opts.page, background: true }));
    const { sessionId } = await reader.send('Target.attachToTarget', { targetId, flatten: true });
    await reader.send('Page.enable', {}, sessionId);
    await reader.send('Runtime.enable', {}, sessionId);
    await loaded(sessionId, opts.page);
    if (opts.mode === 'iframe' || opts.mode === 'iframe-known') {
      await until(async () => await evaluate(reader, sessionId, `document.querySelector('iframe')?.contentDocument?.readyState === 'complete' && document.querySelector('iframe').contentWindow.location.href === ${JSON.stringify(opts.child)}`), 'child frame', 15000);
    }
    let pageURL = opts.page;
    const { sessionId: clientSession } = await client.send('Target.attachToTarget', { targetId, flatten: true });
    const timeOrigin = () => outcome(client.send('Runtime.evaluate', { expression: 'String(performance.timeOrigin)', returnByValue: true }, clientSession));
    let control = await timeOrigin();
    for (let i = 0; i < 40 && !(control.ok && !control.result.exceptionDetails); i++) {
      await new Promise((resolve) => setTimeout(resolve, 250));
      control = await timeOrigin();
    }
    step('client-control', { ok: control.ok, error: control.ok ? null : control.error?.message ?? null });
    if (opts.warm || opts.mode.endsWith('-known')) {
      if (!control.ok) {
        return { steps, mode: opts.mode, targetId, readerSession: sessionId, clientSession, control: false, firstLoader: null, lastLoader: null, history: { used: false }, before: null, racedOk: false, racedValue: null, racedError: control.error?.message ?? 'guard-not-ready', readerTopLoaders: [], childNavs: 0, sameDocNavs: 0, after: null, usable: false };
      }
      const warmFrom = reader.events.length;
      const warm = await warmDocument(reader, sessionId, opts.page);
      step('warm-navigate', warm);
      if (!warm.ok) {
        return { steps, mode: opts.mode, targetId, readerSession: sessionId, clientSession, control: false, firstLoader: null, lastLoader: null, history: { used: false }, before: null, racedOk: false, racedValue: null, racedError: warm.error, readerTopLoaders: [], childNavs: 0, sameDocNavs: 0, after: null, usable: false };
      }
      pageURL = warm.url;
      if (opts.mode.startsWith('iframe')) {
        await until(async () => await evaluate(reader, sessionId, `document.querySelector('iframe')?.contentDocument?.readyState === 'complete'`), 'warm child', 15000).catch(() => {});
      }
      const warmCommit = () => reader.events.slice(warmFrom).some((e) => e.sessionId === sessionId && e.method === 'Page.frameNavigated' && e.params.frame?.id === targetId && !e.params.frame.parentId && e.params.frame.loaderId);
      try {
        await until(async () => warmCommit() && Boolean(await topLoader(sessionId)), 'warm top-frame loader', 5000);
      } catch (err) {
        step('warm-premise', { ok: false, error: err?.message ?? String(err), loader: await topLoader(sessionId) });
      }
    }
    const firstLoader = await topLoader(sessionId);
    step('page-loaded', { targetId, readerSession: sessionId, firstLoader });
    const restore = { used: false, restoreType: null, restoredLoader: null, secondLoader: null, back: null };
    const baseMode = opts.mode.replace(/-known$/, '');
    if (baseMode === 'history') {
      restore.used = true;
      const nav = await outcome(reader.send('Page.navigate', { url: opts.second }, sessionId));
      step('navigate-second', { ok: nav.ok, error: nav.ok ? null : nav.error?.message ?? null });
      await loaded(sessionId, opts.second);
      restore.secondLoader = await topLoader(sessionId);
      const entries = await reader.send('Page.getNavigationHistory', {}, sessionId);
      const prev = entries.entries[entries.currentIndex - 1];
      const from = reader.events.length;
      const back = await outcome(reader.send('Page.navigateToHistoryEntry', { entryId: prev?.id }, sessionId));
      restore.back = back.ok ? 'ok' : back.error?.message ?? 'error';
      await loaded(sessionId, pageURL);
      const commit = reader.events.slice(from).find((e) => e.sessionId === sessionId && e.method === 'Page.frameNavigated' && e.params.frame.id === targetId && !e.params.frame.parentId);
      restore.restoreType = commit ? commit.params.type : null;
      restore.restoredLoader = await topLoader(sessionId);
      step('history-back', restore);
      await new Promise((resolve) => setTimeout(resolve, 300));
    }
    let beforeRead = await timeOrigin();
    for (let i = 0; i < 20 && !(beforeRead.ok && !beforeRead.result.exceptionDetails); i++) {
      await new Promise((resolve) => setTimeout(resolve, 250));
      beforeRead = await timeOrigin();
    }
    const before = beforeRead.ok ? beforeRead.result.result?.value ?? null : null;
    step('before-read', { before, error: beforeRead.ok ? null : beforeRead.error?.message ?? null });
    const marker = `docloader-${opts.mode}`;
    const expressions = {
      pushState: `new Promise((r) => setTimeout(() => { history.pushState({ n: 1 }, '', location.pathname + '?pushed=1'); setTimeout(() => r('${marker} ' + performance.timeOrigin), 300); }, 100))`,
      hash: `new Promise((r) => setTimeout(() => { r('${marker} ' + performance.timeOrigin); setTimeout(() => { location.hash = 'h' + Date.now(); }, 0); }, 100))`,
      iframe: `new Promise((r) => { const f = document.querySelector('iframe'); f.addEventListener('load', () => setTimeout(() => r('${marker} ' + performance.timeOrigin), 50), { once: true }); f.src = ${JSON.stringify(opts.child2)}; })`,
      history: `'${marker} ' + performance.timeOrigin`,
    };
    const from = reader.events.length;
    const read = await outcome(client.send('Runtime.evaluate', { expression: expressions[baseMode], awaitPromise: true, returnByValue: true }, clientSession));
    const racedValue = read.ok ? read.result.result?.value ?? null : null;
    const racedError = read.ok ? (read.result.exceptionDetails ? 'exception' : null) : read.error?.message ?? null;
    step('read', { ok: read.ok, value: racedValue, error: racedError });
    const ours = () => reader.events.slice(from).filter((e) => e.sessionId === sessionId);
    const want = { pushState: 'Page.navigatedWithinDocument', hash: 'Page.navigatedWithinDocument', iframe: 'Page.frameNavigated' }[baseMode];
    if (want) await until(async () => ours().some((e) => e.method === want && (baseMode !== 'iframe' || e.params.frame.parentId)), `event ${want}`, 5000).catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 300));
    let after = null;
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      const usable = await timeOrigin();
      if (usable.ok && !usable.result.exceptionDetails) {
        after = usable.result.result?.value ?? null;
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    const events = ours();
    const readerTopLoaders = events.filter((e) => e.method === 'Page.frameNavigated' && e.params.frame.id === targetId && !e.params.frame.parentId).map((e) => e.params.frame.loaderId);
    const childNavs = events.filter((e) => e.method === 'Page.frameNavigated' && e.params.frame.parentId).length;
    const sameDocNavs = events.filter((e) => e.method === 'Page.navigatedWithinDocument').length;
    const lastLoader = await topLoader(sessionId);
    step('after', { after, readerTopLoaders, childNavs, sameDocNavs, lastLoader });
    step('done');
    return { steps, mode: opts.mode, targetId, readerSession: sessionId, clientSession, control: control.ok, firstLoader, lastLoader, history: restore, before, racedOk: read.ok, racedValue, racedError, readerTopLoaders, childNavs, sameDocNavs, after, usable: after !== null };
  } finally {
    if (targetId) await outcome(reader.send('Target.closeTarget', { targetId }));
    await client.close();
    reader.close();
  }
}
