import { CdpClient } from './cdp.js';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function outcome(promise) {
  try {
    return { ok: true, result: await promise };
  } catch (error) {
    return { ok: false, error: error.cdp ?? { message: String(error?.message ?? error) } };
  }
}

async function names(c, method, params, sessionId) {
  const r = await outcome(c.send(method, params, sessionId));
  if (!r.ok) return { error: r.error };
  return { names: r.result.cookies.map((k) => `${k.name}@${k.domain}`) };
}

const beacon = (base, tag) => `try { navigator.sendBeacon(${JSON.stringify(base)} + '/beacon?tag=' + ${JSON.stringify(tag)} + '&origin=' + encodeURIComponent(location.origin)); } catch {}`;

const later = (url) => `setTimeout(() => { location.href = ${JSON.stringify(url)}; }, 50); true`;

async function load(c, sessionId, url) {
  const loaded = c.waitEvent((e) => e.method === 'Page.loadEventFired' && e.sessionId === sessionId, 20000);
  await c.send('Page.navigate', { url }, sessionId);
  await loaded;
  return c.evaluate('location.href', sessionId);
}

async function leave(c, sessionId, url) {
  const gone = c.waitEvent((e) => e.method === 'Target.detachedFromTarget' && e.params.sessionId === sessionId, 20000).then(
    () => true,
    () => false,
  );
  await c.send('Runtime.evaluate', { expression: later(url) }, sessionId);
  return gone;
}

async function instrument(c, sessionId, base, tag) {
  await c.send('Page.enable', {}, sessionId);
  await c.send('Page.addScriptToEvaluateOnNewDocument', { source: beacon(base, tag) }, sessionId);
  await c.send('Page.setBypassCSP', { enabled: true }, sessionId);
}

let watching = null;

export function protectTest(getLink, describe) {
  const run = async (browser, fn) => {
    let c;
    try {
      c = await CdpClient.open(getLink(), browser);
      const result = await fn(c);
      result.closed = await c.close();
      return result;
    } catch (error) {
      return { ...describe(error), cdpError: error.cdp ?? null, targets: error.targets ?? [] };
    }
  };
  return {
    continuedTwice: (browser, base) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo(`${base}/doc?n=twice-start`);
        await c.send('Fetch.enable', { patterns: [{ urlPattern: '*/data?n=twice*' }] }, sessionId);
        const paused = c.waitEvent((e) => e.method === 'Fetch.requestPaused' && e.sessionId === sessionId);
        const body = c.evaluate(`fetch(${JSON.stringify(`${base}/data?n=twice`)}).then((r) => r.text())`, sessionId);
        const { params } = await paused;
        const first = await outcome(c.send('Fetch.continueRequest', { requestId: params.requestId }, sessionId));
        const text = await body;
        const again = await outcome(c.send('Fetch.continueWithAuth', { requestId: params.requestId, authChallengeResponse: { response: 'Default' } }, sessionId));
        await c.send('Target.closeTarget', { targetId });
        return { first, text, again };
      }),
    urlsShown: (browser, from, to) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo(from);
        await c.evaluate("new Promise((resolve) => (document.body ? resolve(true) : addEventListener('DOMContentLoaded', () => resolve(true))))", sessionId);
        await c.send('Page.enable', {}, sessionId);
        await c.send('Network.enable', {}, sessionId);
        const start = c.events.length;
        await c.evaluate(`(() => { const f = document.createElement('iframe'); f.src = ${JSON.stringify(to)}; document.body.append(f); return true; })()`, sessionId);
        await sleep(2000);
        const tree = await outcome(c.send('Page.getFrameTree', {}, sessionId));
        await outcome(c.send('Runtime.evaluate', { expression: later(to) }, sessionId));
        await sleep(2000);
        const events = c.events.slice(start).filter((e) => JSON.stringify(e).includes(to)).map((e) => e.method);
        return { targetId, events, tree: tree.ok ? JSON.stringify(tree.result).includes(to) : null };
      }),
    guardWait: (browser) =>
      run(browser, async (c) => {
        const { targetId } = await c.send('Target.createTarget', { url: 'about:blank' });
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        const start = Date.now();
        const enable = outcome(c.send('Page.enable', {}, sessionId)).then((r) => ({ ...r, ms: Date.now() - start }));
        await sleep(500);
        const other = await outcome(c.send('Browser.getVersion')).then((r) => ({ ...r, ms: Date.now() - start }));
        return { enable: await enable, other };
      }),
    guardHold: (browser) =>
      run(browser, async (c) => {
        const { targetId } = await c.send('Target.createTarget', { url: 'about:blank' });
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        const start = Date.now();
        const enable = await outcome(c.send('Page.enable', {}, sessionId));
        return { enable: { ...enable, ms: Date.now() - start } };
      }),
    lateSession: (browser, sessionId, targetId) =>
      run(browser, async (c) => {
        const start = Date.now();
        const enable = outcome(c.send('Page.enable', {}, sessionId)).then((r) => ({ ...r, ms: Date.now() - start }));
        const attach = targetId ? await outcome(c.send('Target.attachToTarget', { targetId, flatten: true })) : null;
        return { enable: await enable, attach };
      }),
    guardFlood: async (browser, count, size) => {
      try {
        const c = await CdpClient.open(getLink(), browser);
        const { targetId } = await c.send('Target.createTarget', { url: 'about:blank' });
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        const params = size ? { pad: 'x'.repeat(size) } : {};
        for (let i = 0; i < count; i += 1) c.send('Page.enable', params, sessionId).catch(() => {});
        const stream = await Promise.race([c.closed, sleep(60000).then(() => ({ open: true }))]);
        return { stream };
      } catch (error) {
        return { failed: String(error?.message ?? error) };
      }
    },
    targets: (browser) =>
      run(browser, async (c) => {
        const { targetInfos } = await c.send('Target.getTargets');
        return { targets: targetInfos.map(({ targetId, type, url, title, openerId }) => ({ targetId, type, url, title, openerId: openerId ?? '' })) };
      }),
    create: (browser, url) =>
      run(browser, async (c) => {
        const r = await outcome(c.send('Target.createTarget', { url }));
        if (r.ok) await c.send('Target.closeTarget', { targetId: r.result.targetId });
        return r;
      }),
    navigate: (browser, url) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo('about:blank');
        const r = await outcome(c.send('Page.navigate', { url }, sessionId));
        const href = await c.evaluate('location.href', sessionId);
        await c.send('Target.closeTarget', { targetId });
        return { ...r, href };
      }),
    privileged: (browser, urls) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo('about:blank');
        const navigate = {};
        for (const url of urls) {
          const r = await outcome(c.send('Page.navigate', { url }, sessionId));
          await sleep(300);
          navigate[url] = { ...r, href: await c.evaluate('location.href', sessionId).catch((e) => `unknown: ${e?.message ?? e}`) };
        }
        await outcome(c.send('Target.closeTarget', { targetId }));
        const create = {};
        for (const url of urls) {
          const r = await outcome(c.send('Target.createTarget', { url }));
          if (r.ok) await outcome(c.send('Target.closeTarget', { targetId: r.result.targetId }));
          create[url] = r;
        }
        return { navigate, create };
      }),
    browserUI: (browser, uiId) =>
      run(browser, async (c) => {
        const isUI = (info) => info?.type === 'browser_ui' || info?.targetId === uiId;
        await c.send('Target.setDiscoverTargets', { discover: true });
        await c.send('Target.setAutoAttach', { autoAttach: true, flatten: true, waitForDebuggerOnStart: false });
        await sleep(1500);
        const { targetInfos } = await c.send('Target.getTargets');
        const out = {
          listed: targetInfos.filter(isUI).length,
          discovered: c.events.filter((e) => e.method === 'Target.targetCreated' && isUI(e.params.targetInfo)).length,
          autoAttached: c.events.filter((e) => e.method === 'Target.attachedToTarget' && isUI(e.params.targetInfo)).length,
        };
        await c.send('Target.setAutoAttach', { autoAttach: false, flatten: true, waitForDebuggerOnStart: false });
        await c.send('Target.setDiscoverTargets', { discover: false });
        for (const [method, params] of [
          ['Target.attachToTarget', { targetId: uiId, flatten: true }],
          ['Target.getTargetInfo', { targetId: uiId }],
          ['Target.activateTarget', { targetId: uiId }],
          ['Browser.getWindowForTarget', { targetId: uiId }],
        ]) {
          out[method] = await outcome(c.send(method, params));
        }
        return out;
      }),
    attach: (browser, targetId) =>
      run(browser, async (c) => {
        const out = {};
        for (const [method, params] of [
          ['Target.attachToTarget', { targetId, flatten: true }],
          ['Target.activateTarget', { targetId }],
          ['Target.closeTarget', { targetId }],
          ['Target.exposeDevToolsProtocol', { targetId }],
          ['Target.sendMessageToTarget', { targetId, message: '{}' }],
          ['Browser.getWindowForTarget', { targetId }],
        ]) {
          out[method] = await outcome(c.send(method, params));
        }
        const info = await c.send('Target.getTargetInfo', { targetId });
        out.info = { url: info.targetInfo.url, title: info.targetInfo.title };
        return out;
      }),
    autoAttach: (browser, plainURL) =>
      run(browser, async (c) => {
        const { targetId } = await c.send('Target.createTarget', { url: plainURL });
        await c.send('Target.setDiscoverTargets', { discover: true });
        await c.send('Target.setAutoAttach', { autoAttach: true, flatten: true, waitForDebuggerOnStart: false });
        await c.waitEvent((e) => e.method === 'Target.attachedToTarget' && e.params.targetInfo.targetId === targetId);
        await sleep(1000);
        const attached = c.events.filter((e) => e.method === 'Target.attachedToTarget').map((e) => ({ targetId: e.params.targetInfo.targetId, url: e.params.targetInfo.url, sessionId: e.params.sessionId }));
        const discovered = c.events.filter((e) => e.method === 'Target.targetCreated').map((e) => ({ targetId: e.params.targetInfo.targetId, url: e.params.targetInfo.url, title: e.params.targetInfo.title }));
        await c.send('Target.closeTarget', { targetId });
        return { attached, discovered, plain: targetId };
      }),
    scriptNavigate: (browser, plainURL, sevenURL) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo(plainURL);
        await c.send('Runtime.enable', {}, sessionId);
        const before = await c.evaluate('location.href', sessionId);
        const detached = c.waitEvent((e) => e.method === 'Target.detachedFromTarget' && e.params.sessionId === sessionId, 20000);
        await c.send('Runtime.evaluate', { expression: `setTimeout(() => { location.href = ${JSON.stringify(sevenURL)}; }, 50); true` }, sessionId);
        await detached;
        const after = await outcome(c.send('Runtime.evaluate', { expression: 'document.cookie' }, sessionId));
        const reattach = await outcome(c.send('Target.attachToTarget', { targetId, flatten: true }));
        const { targetInfo } = await c.send('Target.getTargetInfo', { targetId });
        const leaked = c.events.filter((e) => e.sessionId === sessionId && e.method === 'Runtime.executionContextCreated' && e.params.context.origin === new URL(sevenURL).origin).length;
        return { targetId, before, after, reattach, url: targetInfo.url, title: targetInfo.title, leaked };
      }),
    popup: (browser, openerId) =>
      run(browser, async (c) => {
        const { targetInfos } = await c.send('Target.getTargets');
        const popup = targetInfos.find((t) => t.openerId === openerId);
        if (!popup) return { error: 'no popup' };
        return { url: popup.url, attach: await outcome(c.send('Target.attachToTarget', { targetId: popup.targetId, flatten: true })) };
      }),
    history: (browser, targetId, sevenURL) =>
      run(browser, async (c) => {
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        const { entries } = await c.send('Page.getNavigationHistory', {}, sessionId);
        const seven = entries.find((e) => e.url.startsWith(sevenURL));
        const plain = entries.find((e) => !e.url.startsWith(sevenURL) && e.url !== 'about:blank');
        const refused = await outcome(c.send('Page.navigateToHistoryEntry', { entryId: seven.id }, sessionId));
        const allowed = await outcome(c.send('Page.navigateToHistoryEntry', { entryId: plain.id }, sessionId));
        await c.send('Target.detachFromTarget', { sessionId });
        return { refused, allowed, entries: entries.length };
      }),
    cookies: (browser, allowedURL, sevenURL, sevenHost) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo('about:blank');
        const out = {
          setAllowed: await outcome(c.send('Network.setCookie', { name: 'allowed', value: '1', url: allowedURL }, sessionId)),
          setSevenURL: await outcome(c.send('Network.setCookie', { name: 'x', value: '1', url: sevenURL }, sessionId)),
          setSevenDomain: await outcome(c.send('Network.setCookie', { name: 'x', value: '1', domain: sevenHost, path: '/' }, sessionId)),
          setOtherPort: await outcome(c.send('Network.setCookie', { name: 'x', value: '1', url: `http://${sevenHost}:1/` }, sessionId)),
          setPartitioned: await outcome(c.send('Network.setCookie', { name: 'x', value: '1', url: allowedURL, secure: true, partitionKey: { topLevelSite: `http://${sevenHost}`, hasCrossSiteAncestor: false } }, sessionId)),
          setCookies: await outcome(c.send('Network.setCookies', { cookies: [{ name: 'y', value: '1', url: allowedURL }, { name: 'x', value: '1', domain: sevenHost }] }, sessionId)),
          storageSet: await outcome(c.send('Storage.setCookies', { cookies: [{ name: 'x', value: '1', domain: sevenHost, path: '/' }] })),
          deleteSeven: await outcome(c.send('Network.deleteCookies', { name: 'seven', url: sevenURL }, sessionId)),
          clearBrowser: await outcome(c.send('Network.clearBrowserCookies', {}, sessionId)),
          storageClear: await outcome(c.send('Storage.clearCookies', {})),
          storageGet: await names(c, 'Storage.getCookies', {}),
          all: await names(c, 'Network.getAllCookies', {}, sessionId),
          byURL: await names(c, 'Network.getCookies', { urls: [sevenURL, allowedURL] }, sessionId),
        };
        await c.send('Target.closeTarget', { targetId });
        return out;
      }),
    raw: (browser, messages) =>
      run(browser, async (c) => {
        const out = [];
        for (const [id, text] of messages) {
          const r = await outcome(c.sendRaw(id, text));
          if (r.ok && r.result.targetId) {
            const { targetInfo } = await c.send('Target.getTargetInfo', { targetId: r.result.targetId });
            r.url = targetInfo.url;
            await c.send('Target.closeTarget', { targetId: r.result.targetId });
          }
          out.push(r);
        }
        return { out };
      }),
    sockets: (browser, pageURL, targets) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo(pageURL);
        await c.send('Network.enable', {}, sessionId);
        const expression = `Promise.all(${JSON.stringify(targets)}.map((base) => Promise.all([
          new Promise((resolve) => { const ws = new WebSocket(base.replace(/^http/, 'ws') + '/ws'); ws.onopen = () => ws.send('hi'); ws.onmessage = () => { ws.close(); resolve(true); }; ws.onerror = () => resolve(false); }),
          new Promise((resolve) => { const es = new EventSource(base + '/sse'); es.onmessage = () => { es.close(); resolve(true); }; es.onerror = () => { es.close(); resolve(false); }; }),
        ])))`;
        const opened = await c.evaluate(expression, sessionId);
        await sleep(500);
        const owner = new Map();
        for (const e of c.events) {
          const url = e.params?.url ?? e.params?.request?.url;
          if (e.sessionId === sessionId && e.params?.requestId && url) {
            const base = targets.find((t) => url.replace(/^ws/, 'http').startsWith(t));
            if (base && !owner.has(e.params.requestId)) owner.set(e.params.requestId, base);
          }
        }
        const counts = Object.fromEntries(targets.map((t) => [t, {}]));
        let leaked = '';
        for (const e of c.events) {
          const base = owner.get(e.params?.requestId);
          if (!base) continue;
          counts[base][e.method] = (counts[base][e.method] ?? 0) + 1;
          if (base === targets[0] && /secret/.test(JSON.stringify(e))) leaked = e.method;
        }
        await c.send('Target.closeTarget', { targetId });
        return { opened, counts, leaked };
      }),
    navInit: (browser, base, tag, start, next, away) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo(start);
        await instrument(c, sessionId, base, tag);
        const before = await load(c, sessionId, next);
        const detached = await leave(c, sessionId, away);
        return { targetId, before, detached };
      }),
    navPopup: (browser, base, tag, opener, popup, away, direct) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo(opener);
        await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
        const isPopup = (e) => e.method === 'Target.attachedToTarget' && e.params.targetInfo.openerId === targetId;
        const attached = c.waitEvent(isPopup, 20000);
        await c.send('Runtime.evaluate', { expression: `Boolean(window.open(${JSON.stringify(popup)}))`, userGesture: true }, sessionId);
        const ev = await attached;
        const ps = ev.params.sessionId;
        const out = { popup: ev.params.targetInfo.targetId, url: ev.params.targetInfo.url, waiting: ev.params.waitingForDebugger };
        await instrument(c, ps, base, tag);
        await c.send('Runtime.runIfWaitingForDebugger', {}, ps);
        out.before = await load(c, ps, `${popup}&again=1`);
        out.detached = await leave(c, ps, away);
        const seen = c.events.filter(isPopup).length;
        await c.send('Runtime.evaluate', { expression: `Boolean(window.open(${JSON.stringify(direct)}))`, userGesture: true }, sessionId);
        for (let i = 0; i < 150; i += 1) {
          const { targetInfos } = await c.send('Target.getTargets');
          out.popups = targetInfos.filter((t) => t.openerId === targetId).map((t) => ({ targetId: t.targetId, url: t.url }));
          if (out.popups.length > 1 && out.popups.every((p) => p.url)) break;
          await sleep(100);
        }
        await sleep(500);
        out.direct = c.events.filter(isPopup).slice(seen).map((e) => e.params.targetInfo.url);
        await c.send('Target.setAutoAttach', { autoAttach: false, waitForDebuggerOnStart: false, flatten: true });
        return out;
      }),
    unparsedPopup: (browser, opener, popup, markers) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo(opener);
        await c.send('Target.setDiscoverTargets', { discover: true });
        await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
        const start = c.events.length;
        await c.send('Runtime.evaluate', { expression: `Boolean(window.open(${JSON.stringify(popup)}))`, userGesture: true }, sessionId);
        await sleep(3000);
        const { targetInfos } = await c.send('Target.getTargets');
        const popups = targetInfos.filter((t) => t.openerId === targetId).map(({ targetId: id, url, title }) => ({ targetId: id, url, title }));
        const out = { opener: targetId, popups, attach: {}, cookies: {} };
        for (const p of popups) {
          const r = await outcome(c.send('Target.attachToTarget', { targetId: p.targetId, flatten: true }));
          out.attach[p.targetId] = r;
          if (r.ok) out.cookies[p.targetId] = await outcome(c.evaluate('document.cookie', r.result.sessionId));
        }
        for (const e of c.events.slice(start).filter((e) => e.method === 'Target.attachedToTarget' && e.params.targetInfo.openerId === targetId)) {
          out.cookies[e.params.sessionId] = await outcome(c.evaluate('document.cookie', e.params.sessionId));
        }
        const seen = JSON.stringify(c.events.slice(start)) + JSON.stringify(out);
        out.leaked = markers.filter((m) => seen.includes(m));
        await c.send('Target.setAutoAttach', { autoAttach: false, waitForDebuggerOnStart: false, flatten: true });
        await c.send('Target.setDiscoverTargets', { discover: false });
        return out;
      }),
    watchTargets: async (browser) => {
      const c = await CdpClient.open(getLink(), browser);
      await c.send('Target.setDiscoverTargets', { discover: true });
      watching = c;
      return { ok: true };
    },
    watchFragments: async (browser) => {
      const c = await CdpClient.open(getLink(), browser);
      await c.send('Target.setDiscoverTargets', { discover: true });
      await c.send('Target.setAutoAttach', { autoAttach: true, flatten: true, waitForDebuggerOnStart: false });
      watching = c;
      return { ok: true };
    },
    watchedFragments: async (targetIds, mark) => {
      const c = watching;
      watching = null;
      const list = await c.send('Target.getTargets');
      const infos = [];
      for (const targetId of targetIds) infos.push(await outcome(c.send('Target.getTargetInfo', { targetId })));
      await c.send('Target.setAutoAttach', { autoAttach: false, flatten: true, waitForDebuggerOnStart: false });
      await c.send('Target.setDiscoverTargets', { discover: false });
      const changed = c.events.slice(mark).filter((e) => e.method === 'Target.targetInfoChanged').map((e) => ({ targetId: e.params.targetInfo.targetId, url: e.params.targetInfo.url, title: e.params.targetInfo.title }));
      const received = JSON.stringify({ events: c.events, list, infos });
      await c.close();
      return { received, listed: list.targetInfos.map(({ targetId, url, title }) => ({ targetId, url, title })), changed };
    },
    watchMark: async () => ({ mark: watching.events.length }),
    watchedTargets: async (targetId, mark) => {
      const c = watching;
      watching = null;
      const pick = (list) => list.filter((e) => (e.method === 'Target.targetCreated' || e.method === 'Target.targetInfoChanged') && e.params.targetInfo.targetId === targetId).map((e) => ({ method: e.method, url: e.params.targetInfo.url }));
      const { targetInfos } = await c.send('Target.getTargets');
      await c.send('Target.setDiscoverTargets', { discover: false });
      const out = { before: pick(c.events.slice(0, mark)), after: pick(c.events.slice(mark)), listed: targetInfos.some((t) => t.targetId === targetId) };
      await c.close();
      return out;
    },
    opaqueBlob: (browser) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo('about:blank');
        await c.send('Page.enable', {}, sessionId);
        await load(c, sessionId, 'data:text/html,<title>opaque</title>');
        await c.send('Network.enable', {}, sessionId);
        const url = await c.evaluate("URL.createObjectURL(new Blob(['<title>blob ok</title>opaque blob'], { type: 'text/html' }))", sessionId);
        const text = await c.evaluate(`fetch(${JSON.stringify(url)}).then((r) => r.text())`, sessionId);
        await sleep(500);
        const sent = c.events.find((e) => e.method === 'Network.requestWillBeSent' && e.sessionId === sessionId && e.params.request.url.startsWith('blob:'));
        const body = sent ? await outcome(c.send('Network.getResponseBody', { requestId: sent.params.requestId }, sessionId)) : { ok: false, error: { message: 'no request for the blob reached the page' } };
        const navigate = await outcome(c.send('Page.navigate', { url }, sessionId));
        await sleep(1000);
        const href = await outcome(c.evaluate('location.href + " " + document.title', sessionId));
        const { targetInfo } = await c.send('Target.getTargetInfo', { targetId });
        await outcome(c.send('Target.closeTarget', { targetId }));
        return { url, text, body, navigate, href, info: targetInfo.url };
      }),
    parseRefusals: (browser, base, hole, historyTarget, holeMarker) =>
      run(browser, async (c) => {
        const out = {};
        const { targetId, sessionId } = await c.attachTo(`${base}/doc?n=refusals`);
        out.setCookie = await outcome(c.send('Network.setCookie', { name: 'x', value: '1', url: hole }, sessionId));
        out.getCookies = await outcome(c.send('Network.getCookies', { urls: [`${base}/`, hole] }, sessionId));
        out.getCookiesOwn = await outcome(c.send('Network.getCookies', { urls: [`${base}/`] }, sessionId));
        out.pageGetCookies = await outcome(c.send('Page.getCookies', { urls: [hole] }, sessionId));
        out.deleteCookies = await outcome(c.send('Network.deleteCookies', { name: 'seven', url: hole }, sessionId));
        out.setCookies = await outcome(c.send('Network.setCookies', { cookies: [{ name: 'x', value: '1', url: hole }] }, sessionId));
        out.storageSetCookies = await outcome(c.send('Storage.setCookies', { cookies: [{ name: 'x', value: '1', url: hole }] }));
        await c.send('Fetch.enable', { patterns: [{ urlPattern: '*data?n=override*' }] }, sessionId);
        const paused = c.waitEvent((e) => e.method === 'Fetch.requestPaused' && e.sessionId === sessionId);
        const body = c.evaluate(`fetch(${JSON.stringify(`${base}/data?n=override`)}).then((r) => r.text())`, sessionId);
        const { params } = await paused;
        out.continueRequest = await outcome(c.send('Fetch.continueRequest', { requestId: params.requestId, url: hole }, sessionId));
        out.continued = await outcome(c.send('Fetch.continueRequest', { requestId: params.requestId }, sessionId));
        out.body = await body;
        await c.send('Fetch.disable', {}, sessionId);
        out.legacyEnable = await outcome(c.send('Network.setRequestInterception', { patterns: [{ urlPattern: '*data?n=legacy*' }] }, sessionId));
        if (out.legacyEnable.ok) {
          const intercepted = c.waitEvent((e) => e.method === 'Network.requestIntercepted' && e.sessionId === sessionId);
          const legacyBody = c.evaluate(`fetch(${JSON.stringify(`${base}/data?n=legacy`)}).then((r) => r.text())`, sessionId);
          const ev = await intercepted;
          out.continueIntercepted = await outcome(c.send('Network.continueInterceptedRequest', { interceptionId: ev.params.interceptionId, url: hole }, sessionId));
          out.legacyContinued = await outcome(c.send('Network.continueInterceptedRequest', { interceptionId: ev.params.interceptionId }, sessionId));
          out.legacyBody = await legacyBody;
          await c.send('Network.setRequestInterception', { patterns: [] }, sessionId);
        }
        const { frameTree } = await c.send('Page.getFrameTree', {}, sessionId);
        out.loadNetworkResource = await outcome(c.send('Network.loadNetworkResource', { frameId: frameTree.frame.id, url: hole, options: { disableCache: true, includeCredentials: true } }, sessionId));
        out.pwaInstall = await outcome(c.send('PWA.install', { manifestId: `${base}/manifest.json`, installUrlOrOrigin: hole }));
        out.pwaLaunch = await outcome(c.send('PWA.launch', { manifestId: `${base}/manifest.json`, url: hole }));
        await outcome(c.send('Target.closeTarget', { targetId }));
        const h = await c.send('Target.attachToTarget', { targetId: historyTarget, flatten: true });
        const { entries } = await c.send('Page.getNavigationHistory', {}, h.sessionId);
        const entry = entries.find((e) => e.url.includes(holeMarker)) ?? entries.find((e) => e.url === '');
        out.history = entry ? await outcome(c.send('Page.navigateToHistoryEntry', { entryId: entry.id }, h.sessionId)) : { ok: false, error: { message: `no history entry on ${holeMarker} in ${JSON.stringify(entries.map((e) => e.url))}` } };
        await outcome(c.send('Target.detachFromTarget', { sessionId: h.sessionId }));
        return out;
      }),
    navHang: (browser, opener, hang, wait) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo(opener);
        await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
        const isPopup = (e) => e.method === 'Target.attachedToTarget' && e.params.targetInfo.openerId === targetId;
        await c.send('Runtime.evaluate', { expression: `Boolean(window.open(${JSON.stringify(hang)}))`, userGesture: true }, sessionId);
        await sleep(wait);
        const attached = c.events.filter(isPopup).map((e) => e.params.targetInfo.url);
        const { targetInfos } = await c.send('Target.getTargets');
        const popups = targetInfos.filter((t) => t.openerId === targetId).map((t) => ({ targetId: t.targetId, url: t.url }));
        await c.send('Target.setAutoAttach', { autoAttach: false, waitForDebuggerOnStart: false, flatten: true });
        return { targetId, attached, popups };
      }),
    navFrame: (browser, base, tag, page, cross, away) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo(page);
        await instrument(c, sessionId, base, tag);
        await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true }, sessionId);
        const isFrame = (e) => e.method === 'Target.attachedToTarget' && e.sessionId === sessionId;
        const attached = c.waitEvent(isFrame, 20000);
        await c.evaluate(`document.getElementById('f').src = ${JSON.stringify(cross)}; true`, sessionId);
        const ev = await attached;
        const out = { targetId, cross: { type: ev.params.targetInfo.type, url: ev.params.targetInfo.url, waiting: ev.params.waitingForDebugger } };
        out.cross.href = await c.evaluate('location.href', ev.params.sessionId);
        const seen = c.events.filter(isFrame).length;
        await c.evaluate(`new Promise((resolve) => { const g = document.getElementById('g'); g.onload = () => resolve(true); g.src = ${JSON.stringify(away)}; })`, sessionId);
        await sleep(1000);
        out.later = c.events.filter(isFrame).slice(seen).map((e) => e.params.targetInfo.url);
        out.frames = await c.evaluate(`[...document.querySelectorAll('iframe')].map((f) => { try { return f.contentWindow.location.href; } catch { return 'cross-origin'; } })`, sessionId);
        return out;
      }),
    navFetch: (browser, base, start, next, away) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo(start);
        const paused = [];
        const forged = btoa('<!doctype html><title>forged</title>');
        c.on((e) => {
          if (e.method !== 'Fetch.requestPaused' || e.sessionId !== sessionId) return;
          const response = e.params.responseStatusCode !== undefined || e.params.responseErrorReason !== undefined;
          paused.push({ url: e.params.request.url, type: e.params.resourceType, response });
          if (e.params.request.url === away) {
            const headers = [{ name: 'content-type', value: 'text/html' }];
            c.send('Fetch.fulfillRequest', { requestId: e.params.requestId, responseCode: 200, responseHeaders: headers, body: forged }, sessionId).catch(() => {});
            return;
          }
          c.send('Fetch.continueRequest', { requestId: e.params.requestId }, sessionId).catch(() => {});
        });
        await c.send('Page.enable', {}, sessionId);
        await c.send('Network.enable', {}, sessionId);
        await c.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }, { urlPattern: '*', requestStage: 'Response' }] }, sessionId);
        const href = await load(c, sessionId, next);
        const title = await c.evaluate('document.title', sessionId);
        const sub = await c.evaluate(`fetch(${JSON.stringify(`${base}/doc?n=sub`)}).then((r) => r.text())`, sessionId);
        const sent = c.events.filter((e) => e.method === 'Network.requestWillBeSent' && e.sessionId === sessionId).map((e) => e.params.request.url);
        const detached = await leave(c, sessionId, away);
        return { targetId, href, title, sub, sent, paused, detached };
      }),
    extensionWorld: (browser, targetId, world) =>
      run(browser, async (c) => {
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        const read = 'globalThis.sliccFixtureWorld?.secret ?? null';
        const pull = 'function () { return this.secret ?? null; }';
        const out = {
          main: await outcome(c.send('Runtime.evaluate', { expression: read, returnByValue: true }, sessionId)),
          contextId: await outcome(c.send('Runtime.evaluate', { expression: read, returnByValue: true, contextId: world.id }, sessionId)),
          uniqueContextId: await outcome(c.send('Runtime.evaluate', { expression: read, returnByValue: true, uniqueContextId: world.uniqueId }, sessionId)),
          callOnContext: await outcome(c.send('Runtime.callFunctionOn', { functionDeclaration: 'function () { return globalThis.sliccFixtureWorld?.secret ?? null; }', executionContextId: world.id, returnByValue: true }, sessionId)),
          lexical: await outcome(c.send('Runtime.globalLexicalScopeNames', { executionContextId: world.id }, sessionId)),
          observerObjectId: await outcome(c.send('Runtime.callFunctionOn', { functionDeclaration: pull, objectId: world.objectId, returnByValue: true }, sessionId)),
        };
        const own = await outcome(c.send('Runtime.evaluate', { expression: 'globalThis.sliccFixtureWorld', contextId: world.id }, sessionId));
        const plain = await c.send('Runtime.evaluate', { expression: '({})' }, sessionId);
        const objectId = own.result?.result?.objectId ?? `${plain.result.objectId.split('.')[0]}.${world.id}.1`;
        out.objectId = await outcome(c.send('Runtime.callFunctionOn', { functionDeclaration: pull, objectId, returnByValue: true }, sessionId));
        out.properties = await outcome(c.send('Runtime.getProperties', { objectId, ownProperties: true }, sessionId));
        out.argument = await outcome(c.send('Runtime.callFunctionOn', { functionDeclaration: 'function (w) { return w.secret ?? null; }', objectId: plain.result.objectId, arguments: [{ objectId }], returnByValue: true }, sessionId));
        out.binding = await outcome(c.send('Runtime.addBinding', { name: 'sliccSevenBinding' }, sessionId));
        out.bound = await c.evaluate('typeof globalThis.sliccSevenBinding', sessionId);
        await c.send('HeapProfiler.enable', {}, sessionId);
        out.heap = await outcome(c.send('HeapProfiler.takeHeapSnapshot', { reportProgress: false }, sessionId));
        out.heapLeak = c.events.some((e) => e.method === 'HeapProfiler.addHeapSnapshotChunk' && e.params.chunk.includes('fake-value-0001'));
        out.heapChunks = c.events.filter((e) => e.method === 'HeapProfiler.addHeapSnapshotChunk').length;
        await c.send('HeapProfiler.disable', {}, sessionId);
        await c.send('Runtime.enable', {}, sessionId);
        await c.evaluate('new Promise((resolve) => setTimeout(() => resolve(true), 300))', sessionId);
        out.contexts = c.events.filter((e) => e.method === 'Runtime.executionContextCreated' && e.sessionId === sessionId).map((e) => e.params.context);
        out.console = c.events.filter((e) => e.method === 'Runtime.consoleAPICalled' && e.sessionId === sessionId).map((e) => ({ contextId: e.params.executionContextId, args: JSON.stringify(e.params.args) }));
        out.after = await outcome(c.send('Runtime.evaluate', { expression: 'location.href', returnByValue: true }, sessionId));
        out.detached = c.events.some((e) => e.method === 'Target.detachedFromTarget' && e.params.sessionId === sessionId);
        await c.send('Target.detachFromTarget', { sessionId });
        return out;
      }),
    extensionChurn: (browser, targetId, worldId, mainId, rounds, batch) =>
      run(browser, async (c) => {
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        const churn = `(async () => {
          for (let r = 0; r < ${rounds}; r += 1) {
            const frames = [];
            for (let i = 0; i < ${batch}; i += 1) {
              const f = document.createElement('iframe');
              document.body.append(f);
              frames.push(f);
            }
            const injected = () => frames.every((f) => f.contentDocument?.documentElement?.hasAttribute('data-slicc-world'));
            for (let wait = 0; !injected(); wait += 1) {
              if (wait > 500) throw new Error('the fixture never ran in a churned frame');
              await new Promise((resolve) => setTimeout(resolve, 10));
            }
            for (const f of frames) f.remove();
          }
          return true;
        })()`;
        const started = Date.now();
        await c.evaluate(churn, sessionId);
        const read = 'globalThis.sliccFixtureWorld?.secret ?? null';
        const out = {
          ms: Date.now() - started,
          world: await outcome(c.send('Runtime.evaluate', { expression: read, returnByValue: true, contextId: worldId }, sessionId)),
          main: await outcome(c.send('Runtime.evaluate', { expression: 'location.href', returnByValue: true, contextId: mainId }, sessionId)),
        };
        await c.send('Target.detachFromTarget', { sessionId });
        return out;
      }),
    extensionBreakpoint: (browser, targetId, pageURL) =>
      run(browser, async (c) => {
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        await c.send('Debugger.enable', {}, sessionId);
        await c.send('Page.enable', {}, sessionId);
        const condition = (key) => `(document.documentElement.dataset.${key} = globalThis.sliccFixtureWorld?.secret ?? "none", false)`;
        const out = {
          regex: await outcome(c.send('Debugger.setBreakpointByUrl', { urlRegex: 'chrome-extension://.*', lineNumber: 2, condition: condition('regex') }, sessionId)),
          hash: await outcome(c.send('Debugger.setBreakpointByUrl', { scriptHash: '0'.repeat(64), lineNumber: 2, condition: condition('hash') }, sessionId)),
          blank: await outcome(c.send('Debugger.setBreakpointByUrl', { url: '', lineNumber: 0, condition: condition('blank') }, sessionId)),
          page: await outcome(c.send('Debugger.setBreakpointByUrl', { url: pageURL, lineNumber: 0, condition: 'false' }, sessionId)),
          plain: await outcome(c.send('Debugger.setBreakpointByUrl', { urlRegex: 'chrome-extension://.*', lineNumber: 1 }, sessionId)),
        };
        const loaded = c.waitEvent((e) => e.method === 'Page.loadEventFired' && e.sessionId === sessionId, 20000);
        await c.send('Page.reload', {}, sessionId);
        await loaded;
        await sleep(300);
        out.leak = await c.evaluate('JSON.stringify(document.documentElement.dataset)', sessionId);
        out.paused = c.events.filter((e) => e.method === 'Debugger.paused' && e.sessionId === sessionId).length;
        await outcome(c.send('Debugger.disable', {}, sessionId));
        await c.send('Target.detachFromTarget', { sessionId });
        return out;
      }),
    extensionProfile: (browser, targetId) =>
      run(browser, async (c) => {
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        await c.send('Runtime.enable', {}, sessionId);
        await c.send('Console.enable', {}, sessionId);
        await c.send('Network.enable', {}, sessionId);
        await c.send('DOM.enable', {}, sessionId);
        await c.send('DOM.setNodeStackTracesEnabled', { enable: true }, sessionId);
        const socket = c.waitEvent((e) => e.method === 'Network.webSocketCreated' && e.sessionId === sessionId, 15000);
        const fetched = c.waitEvent((e) => e.method === 'Network.loadingFinished' && e.sessionId === sessionId && c.events.some((r) => r.method === 'Network.requestWillBeSent' && r.params.requestId === e.params.requestId && r.params.request.method === 'POST'), 15000);
        await c.send('Profiler.enable', {}, sessionId);
        await c.evaluate("(function sliccPageWarm() { return 1; })()\n//# sourceURL=slicc-warm.js", sessionId);
        const best = await c.send('Profiler.getBestEffortCoverage', {}, sessionId);
        await c.send('Profiler.setSamplingInterval', { interval: 100 }, sessionId);
        await c.send('Profiler.startPreciseCoverage', { callCount: true, detailed: true }, sessionId);
        await c.send('Profiler.start', {}, sessionId);
        await c.evaluate("(function sliccPageCode() { document.addEventListener('slicc-pong', () => console.log('slicc pong')); for (let i = 0; i < 10; i += 1) document.dispatchEvent(new CustomEvent('slicc-ping')); return true; })()\n//# sourceURL=slicc-page.js", sessionId);
        const { profile } = await c.send('Profiler.stop', {}, sessionId);
        const done = await fetched;
        const sent = c.events.find((e) => e.method === 'Network.requestWillBeSent' && e.params.requestId === done.params.requestId);
        const body = await outcome(c.send('Network.getResponseBody', { requestId: done.params.requestId }, sessionId));
        const created = await socket;
        const { root } = await c.send('DOM.getDocument', {}, sessionId);
        const { nodeId } = await c.send('DOM.querySelector', { nodeId: root.nodeId, selector: '#slicc-extension-node' }, sessionId);
        const nodeStack = await outcome(c.send('DOM.getNodeStackTraces', { nodeId }, sessionId));
        await c.send('DOM.setNodeStackTracesEnabled', { enable: false }, sessionId);
        await c.send('DOM.disable', {}, sessionId);
        await c.send('Network.disable', {}, sessionId);
        const precise = await c.send('Profiler.takePreciseCoverage', {}, sessionId);
        await c.send('Profiler.stopPreciseCoverage', {}, sessionId);
        await c.send('Profiler.disable', {}, sessionId);
        await c.send('Console.disable', {}, sessionId);
        await sleep(300);
        const pongs = c.events.filter((e) => e.method === 'Runtime.consoleAPICalled' && e.sessionId === sessionId && e.params.args[0]?.value === 'slicc pong');
        await c.send('Target.detachFromTarget', { sessionId });
        return {
          profile: JSON.stringify(profile.nodes.map((n) => n.callFrame)),
          precise: JSON.stringify(precise.result.map((r) => ({ url: r.url, functions: r.functions.map((f) => f.functionName) }))),
          best: JSON.stringify(best.result.map((r) => ({ url: r.url, functions: r.functions.map((f) => f.functionName) }))),
          stacks: JSON.stringify(pongs.map((e) => e.params.stackTrace)),
          pongs: pongs.length,
          initiator: JSON.stringify(sent.params.initiator),
          socket: JSON.stringify(created.params.initiator ?? null),
          nodeStack: JSON.stringify(nodeStack),
          body,
          console: JSON.stringify(c.events.filter((e) => e.sessionId === sessionId && (e.method === 'Console.messageAdded' || e.method.startsWith('Profiler.consoleProfile'))).map((e) => e.params)),
        };
      }),
    extensionRequests: (browser, targetId, intercept) =>
      run(browser, async (c) => {
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        const off = c.on((e) => {
          if (e.method === 'Fetch.requestPaused' && e.sessionId === sessionId) c.send('Fetch.continueRequest', { requestId: e.params.requestId }, sessionId).catch(() => {});
        });
        if (intercept) await c.send('Fetch.enable', { patterns: [{ urlPattern: '*extension-fetch*' }] }, sessionId);
        else await c.send('Network.enable', {}, sessionId);
        await c.evaluate("document.dispatchEvent(new CustomEvent('slicc-ping')); true", sessionId);
        let fetched = null;
        for (let i = 0; i < 100 && fetched === null; i += 1) {
          await sleep(100);
          fetched = await c.evaluate("document.documentElement.getAttribute('data-slicc-fetched')", sessionId);
        }
        await sleep(300);
        const events = c.events.filter((e) => e.sessionId === sessionId && (e.method.startsWith('Network.') || e.method.startsWith('Fetch.')));
        const own = events.find((e) => e.method === 'Network.requestWillBeSent' && e.params.request.method === 'POST');
        const postData = own ? await outcome(c.send('Network.getRequestPostData', { requestId: own.params.requestId }, sessionId)) : null;
        off();
        if (intercept) await c.send('Fetch.disable', {}, sessionId);
        else await c.send('Network.disable', {}, sessionId);
        await c.send('Target.detachFromTarget', { sessionId });
        return {
          fetched,
          seen: events.filter((e) => own && e.params.requestId === own.params.requestId).map((e) => e.method),
          text: JSON.stringify(events.map((e) => e.params)),
          postData,
          paused: events.filter((e) => e.method === 'Fetch.requestPaused').length,
        };
      }),
    extensionFrame: (browser, targetId, callFrameId) =>
      run(browser, async (c) => {
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        await c.send('Debugger.enable', {}, sessionId);
        const out = {
          evaluate: await outcome(c.send('Debugger.evaluateOnCallFrame', { callFrameId, expression: 'globalThis.sliccFixtureWorld?.secret ?? null', returnByValue: true }, sessionId)),
          setVariable: await outcome(c.send('Debugger.setVariableValue', { callFrameId, scopeNumber: 0, variableName: 'probe', newValue: { value: 1 } }, sessionId)),
        };
        await sleep(300);
        out.pausedFrames = c.events.filter((e) => e.method === 'Debugger.paused' && e.sessionId === sessionId).map((e) => e.params.callFrames.map((f) => f.callFrameId));
        out.scripts = c.events.filter((e) => e.method === 'Debugger.scriptParsed' && e.sessionId === sessionId).map((e) => ({ url: e.params.url, contextId: e.params.executionContextId }));
        await outcome(c.send('Debugger.disable', {}, sessionId));
        await c.send('Target.detachFromTarget', { sessionId });
        return out;
      }),
    extensionStale: (browser, targetId, uniqueIds) =>
      run(browser, async (c) => {
        const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
        const created = c.waitEvent((e) => e.method === 'Runtime.executionContextCreated' && e.sessionId === sessionId && e.params.context.auxData?.isDefault && e.params.context.auxData?.frameId === targetId, 15000);
        await c.send('Runtime.enable', {}, sessionId);
        const own = (await created).params.context.uniqueId;
        const out = { own: await outcome(c.send('Runtime.evaluate', { expression: 'location.href', returnByValue: true, uniqueContextId: own }, sessionId)), stale: [] };
        for (const uniqueContextId of uniqueIds) out.stale.push(await outcome(c.send('Runtime.evaluate', { expression: 'globalThis.sliccFixtureWorld?.secret ?? location.href', returnByValue: true, uniqueContextId }, sessionId)));
        await c.evaluate('new Promise((resolve) => setTimeout(() => resolve(true), 300))', sessionId);
        const shown = new Set(c.events.filter((e) => e.method === 'Runtime.executionContextCreated' && e.sessionId === sessionId).map((e) => e.params.context.id));
        out.numeric = [];
        const top = Math.max(0, ...shown) + 8;
        for (let id = Math.max(1, top - 40); id <= top; id += 1) {
          const r = await outcome(c.send('Runtime.evaluate', { expression: 'globalThis.sliccFixtureWorld?.secret ?? location.href', returnByValue: true, contextId: id }, sessionId));
          out.numeric.push({ id, exposed: shown.has(id), ok: r.ok && !r.result.exceptionDetails, message: r.error?.message ?? '', leaked: JSON.stringify(r).includes('fake-value-0001') });
        }
        out.after = await outcome(c.send('Runtime.evaluate', { expression: 'location.href', returnByValue: true }, sessionId));
        out.detached = c.events.some((e) => e.method === 'Target.detachedFromTarget' && e.params.sessionId === sessionId);
        await c.send('Target.detachFromTarget', { sessionId });
        return out;
      }),
    ownContext: (browser) =>
      run(browser, async (c) => {
        const { browserContextId } = await c.send('Target.createBrowserContext');
        const set = await outcome(c.send('Storage.setCookies', { cookies: [{ name: 'own', value: '1', domain: 'example.test', path: '/' }], browserContextId }));
        const clear = await outcome(c.send('Storage.clearCookies', { browserContextId }));
        const resetPerms = await outcome(c.send('Browser.resetPermissions', { browserContextId }));
        await c.send('Target.disposeBrowserContext', { browserContextId });
        return { set, clear, resetPerms };
      }),
    browserWide: (browser, sevenOrigin, allowedOrigin) =>
      run(browser, async (c) => {
        const { targetId, sessionId } = await c.attachTo('about:blank');
        const perm = { name: 'geolocation' };
        const out = {
          permSeven: await outcome(c.send('Browser.setPermission', { permission: perm, setting: 'granted', origin: sevenOrigin })),
          permAllowed: await outcome(c.send('Browser.setPermission', { permission: perm, setting: 'granted', origin: allowedOrigin })),
          grantAll: await outcome(c.send('Browser.grantPermissions', { permissions: ['geolocation'] })),
          grantSeven: await outcome(c.send('Browser.grantPermissions', { permissions: ['geolocation'], origin: sevenOrigin })),
          resetPerms: await outcome(c.send('Browser.resetPermissions', {})),
          clearData: await outcome(c.send('Storage.clearDataForOrigin', { origin: sevenOrigin, storageTypes: 'all' })),
          clearKey: await outcome(c.send('Storage.clearDataForStorageKey', { storageKey: `${sevenOrigin}/`, storageTypes: 'all' })),
          quota: await outcome(c.send('Storage.getUsageAndQuota', { origin: sevenOrigin })),
          caches: await outcome(c.send('CacheStorage.requestCacheNames', { securityOrigin: sevenOrigin }, sessionId)),
          cacheEntries: await outcome(c.send('CacheStorage.requestEntries', { cacheId: `${sevenOrigin}/|c1` }, sessionId)),
          idb: await outcome(c.send('IndexedDB.requestDatabaseNames', { securityOrigin: sevenOrigin }, sessionId)),
          fsDir: await outcome(c.send('FileSystem.getDirectory', { bucketFileSystemLocator: { storageKey: `${sevenOrigin}/`, pathComponents: [] } }, sessionId)),
          swUnregister: await outcome(c.send('ServiceWorker.unregister', { scopeURL: `${sevenOrigin}/` }, sessionId)),
          expose: await outcome(c.send('Target.exposeDevToolsProtocol', { targetId })),
          tracing: await outcome(c.send('Tracing.start', {})),
          fetchRoot: await outcome(c.send('Fetch.enable', {})),
          fetchPage: await outcome(c.send('Fetch.enable', {}, sessionId)),
          loadResource: await outcome(c.send('Network.loadNetworkResource', { url: `${sevenOrigin}/index.html`, options: { disableCache: true, includeCredentials: true } }, sessionId)),
        };
        await c.send('Fetch.disable', {}, sessionId);
        await c.send('Target.closeTarget', { targetId });
        return out;
      }),
    calls: (browser, calls) =>
      run(browser, async (c) => {
        const out = [];
        for (const [method, params, sessionId] of calls) {
          const started = Date.now();
          const r = await outcome(c.send(method, params, sessionId));
          out.push({ method, ms: Date.now() - started, ...r });
        }
        return { out };
      }),
    legacyInterception: (browser, all, last) =>
      run(browser, async (c) => {
        const done = c.waitEvent((e) => e.method === 'Network.requestIntercepted' && e.params.interceptionId === last, 15000);
        await c.send('Network.setRequestInterception', { patterns: [{ urlPattern: '*' }] });
        await done;
        await sleep(300);
        const seen = c.events.filter((e) => e.method === 'Network.requestIntercepted').map((e) => e.params.interceptionId);
        const attempts = {};
        for (const id of all) {
          attempts[id] = await outcome(c.send('Network.continueInterceptedRequest', { interceptionId: id, rawResponse: btoa('HTTP/1.1 200 OK\r\n\r\nforged') }));
        }
        return { seen, attempts };
      }),
    redirectHeaders: (browser, start, hops) =>
      run(browser, async (c) => {
        const results = [];
        const sessions = new Set();
        c.on((e) => {
          if (e.method !== 'Fetch.requestPaused' || !sessions.has(e.sessionId)) return;
          const { requestId, responseStatusCode } = e.params;
          const go = () => c.send('Fetch.continueRequest', { requestId }, e.sessionId).catch(() => {});
          if (responseStatusCode >= 300 && responseStatusCode < 400) {
            outcome(c.send('Fetch.getResponseBody', { requestId }, e.sessionId)).then((r) => {
              results.push({ call: `Fetch.getResponseBody ${e.params.request.url}`, ...r });
              go();
            });
            return;
          }
          go();
        });
        const tab = async () => {
          const { targetId, sessionId } = await c.attachTo(start);
          sessions.add(sessionId);
          await c.send('Page.enable', {}, sessionId);
          await c.send('Network.enable', {}, sessionId);
          await c.send('Network.setExtraHTTPHeaders', { headers: { Authorization: 'Basic fake-value-0042' } }, sessionId);
          await outcome(c.send('Audits.enable', {}, sessionId));
          await outcome(c.send('Log.enable', {}, sessionId));
          await c.send('Fetch.enable', { patterns: [{ urlPattern: '*', requestStage: 'Request' }, { urlPattern: '*', requestStage: 'Response' }] }, sessionId);
          return { targetId, sessionId };
        };
        const bodies = async (sessionId) => {
          const ids = [...new Set(c.events.filter((e) => e.sessionId === sessionId && e.method.startsWith('Network.') && e.params.requestId).map((e) => e.params.requestId))];
          for (const requestId of ids) results.push({ call: 'Network.getResponseBody', ...(await outcome(c.send('Network.getResponseBody', { requestId }, sessionId))) });
        };
        for (const hop of hops) {
          const a = await tab();
          results.push({ call: 'fetch', ...(await outcome(c.evaluate(`fetch(${JSON.stringify(hop)}, { mode: 'no-cors', credentials: 'include' }).then((r) => r.type + ' ' + r.status + ' ' + r.url, (e) => String(e))`, a.sessionId))) });
          const stream = await outcome(c.send('Network.loadNetworkResource', { frameId: a.targetId, url: hop, options: { disableCache: true, includeCredentials: true } }, a.sessionId));
          results.push({ call: 'Network.loadNetworkResource', ...stream });
          if (stream.ok && stream.result.resource.stream) results.push({ call: 'IO.read', ...(await outcome(c.send('IO.read', { handle: stream.result.resource.stream }, a.sessionId))) });
          await sleep(500);
          await bodies(a.sessionId);
          await outcome(c.send('Target.closeTarget', { targetId: a.targetId }));
          const b = await tab();
          results.push({ call: 'Page.navigate', ...(await outcome(c.send('Page.navigate', { url: hop }, b.sessionId))) });
          await sleep(1500);
          await bodies(b.sessionId);
          await outcome(c.send('Target.closeTarget', { targetId: b.targetId }));
        }
        const events = c.events.filter((e) => sessions.has(e.sessionId)).map((e) => ({ method: e.method, text: JSON.stringify(e.params) }));
        return { events, results: results.map((r) => ({ call: r.call, text: JSON.stringify(r) })) };
      }),
    redirectReads: (browser, embed, flip, override, redirector) =>
      run(browser, async (c) => {
        const results = [];
        const { targetId, sessionId } = await c.attachTo('about:blank');
        const record = async (call, promise) => results.push({ call, ...(await outcome(promise)) });
        c.on((e) => {
          if (e.method !== 'Fetch.requestPaused' || e.sessionId !== sessionId) return;
          const { requestId, request } = e.params;
          if (request.url.includes('n=override2')) record('Fetch.continueRequest redirector', c.send('Fetch.continueRequest', { requestId, url: redirector }, sessionId));
          else if (request.url.includes('n=override')) record('Fetch.continueRequest', c.send('Fetch.continueRequest', { requestId, url: override }, sessionId));
          c.send('Fetch.continueRequest', { requestId }, sessionId).catch(() => {});
        });
        await c.send('Page.enable', {}, sessionId);
        await c.send('Network.enable', {}, sessionId);
        await c.send('DOM.enable', {}, sessionId);
        await c.send('CSS.enable', {}, sessionId);
        await c.send('Runtime.enable', {}, sessionId);
        await outcome(load(c, sessionId, embed));
        await sleep(1000);
        const tree = await c.send('Page.getResourceTree', {}, sessionId);
        const frames = [tree.frameTree, ...(tree.frameTree.childFrames ?? [])];
        for (const f of frames) {
          for (const r of [{ url: f.frame.url }, ...(f.resources ?? [])]) {
            await record('Page.getResourceContent ' + r.url, c.send('Page.getResourceContent', { frameId: f.frame.id, url: r.url }, sessionId));
            await record('Page.searchInResource', c.send('Page.searchInResource', { frameId: f.frame.id, url: r.url, query: 'fake' }, sessionId));
          }
        }
        await record('Page.captureSnapshot', c.send('Page.captureSnapshot', { format: 'mhtml' }, sessionId));
        await record('DOM.getDocument', c.send('DOM.getDocument', { depth: -1, pierce: true }, sessionId));
        await record('Page.getFrameTree', c.send('Page.getFrameTree', {}, sessionId));
        await record('targets', c.send('Target.getTargets', {}));
        const contexts = c.events.filter((e) => e.method === 'Runtime.executionContextCreated' && e.sessionId === sessionId).map((e) => e.params.context.id);
        for (const contextId of contexts) await record('Runtime.evaluate', c.send('Runtime.evaluate', { contextId, expression: 'document.documentElement.outerHTML', returnByValue: true }, sessionId));
        for (const e of c.events.filter((x) => x.method === 'CSS.styleSheetAdded' && x.sessionId === sessionId)) {
          await record('CSS.getStyleSheetText', c.send('CSS.getStyleSheetText', { styleSheetId: e.params.header.styleSheetId }, sessionId));
        }
        await record('xhr', c.evaluate(`new Promise((resolve) => { const x = new XMLHttpRequest(); x.open('GET', ${JSON.stringify(flip)}); x.onloadend = () => resolve(x.status + ' ' + x.responseText); x.send(); })`, sessionId));
        await sleep(300);
        const xhr = c.events.find((e) => e.sessionId === sessionId && e.method === 'Network.requestWillBeSent' && e.params.request.url === flip);
        if (xhr) await record('Network.replayXHR', c.send('Network.replayXHR', { requestId: xhr.params.requestId }, sessionId));
        await sleep(1500);
        await c.send('Fetch.enable', { patterns: [{ urlPattern: '*n=override*', requestStage: 'Request' }] }, sessionId);
        await record('fetch override', c.evaluate(`fetch(${JSON.stringify(embed + '?n=override')}).then((r) => r.text(), (e) => String(e))`, sessionId));
        await record('fetch override2', c.evaluate(`fetch(${JSON.stringify(embed + '?n=override2')}).then((r) => r.text(), (e) => String(e))`, sessionId));
        await sleep(500);
        const ids = [...new Set(c.events.filter((e) => e.sessionId === sessionId && e.method.startsWith('Network.') && e.params.requestId).map((e) => e.params.requestId))];
        for (const requestId of ids) await record('Network.getResponseBody', c.send('Network.getResponseBody', { requestId }, sessionId));
        await record('Network.loadNetworkResource', c.send('Network.loadNetworkResource', { frameId: targetId, url: embed, options: { disableCache: true, includeCredentials: true } }, sessionId));
        await record('Network.loadNetworkResource browser', c.send('Network.loadNetworkResource', { url: embed, options: { disableCache: true, includeCredentials: false } }));
        await outcome(c.send('Target.closeTarget', { targetId }));
        const events = c.events.filter((e) => e.sessionId === sessionId).map((e) => ({ method: e.method, text: JSON.stringify(e.params) }));
        return { events, results: results.map((r) => ({ call: r.call, text: JSON.stringify(r) })) };
      }),
    redirectAssets: (browser, page) =>
      run(browser, async (c) => {
        const results = [];
        const record = async (call, promise) => results.push({ call, ...(await outcome(promise)) });
        const { targetId, sessionId } = await c.attachTo('about:blank');
        const sessions = new Set([sessionId]);
        const kinds = new Map([[sessionId, '']]);
        const autoAttach = { autoAttach: true, waitForDebuggerOnStart: true, flatten: true };
        c.on((e) => {
          if (e.method !== 'Target.attachedToTarget') return;
          const { sessionId: child, targetInfo } = e.params;
          let kind = null;
          if (targetInfo.type === 'worker' && kinds.get(e.sessionId) === '') kind = 'worker ';
          else if (targetInfo.type === 'worker' && kinds.get(e.sessionId) === 'worker ') kind = 'nested ';
          if (kind === null) return;
          sessions.add(child);
          kinds.set(child, kind);
          (kind === 'worker ' ? c.send('Target.setAutoAttach', autoAttach, child) : Promise.resolve())
            .then(() => c.send('Debugger.enable', {}, child))
            .then(() => c.send('Runtime.runIfWaitingForDebugger', {}, child))
            .catch(() => {});
        });
        for (const domain of ['Page', 'Network', 'DOM', 'CSS', 'Debugger', 'Runtime']) await c.send(`${domain}.enable`, {}, sessionId);
        await c.send('Target.setAutoAttach', autoAttach, sessionId);
        await outcome(load(c, sessionId, page));
        await sleep(2000);
        const tree = await c.send('Page.getResourceTree', {}, sessionId);
        const variants = (u) => {
          const out = [u, `${u}#x`];
          try {
            const p = new URL(u);
            out.push(u.replace(p.host, p.host.toUpperCase()));
          } catch {}
          return out;
        };
        for (const r of tree.frameTree.resources ?? []) {
          for (const url of variants(r.url)) {
            await record('Page.getResourceContent', c.send('Page.getResourceContent', { frameId: tree.frameTree.frame.id, url }, sessionId));
            await record('Page.searchInResource', c.send('Page.searchInResource', { frameId: tree.frameTree.frame.id, url, query: 'fake' }, sessionId));
          }
        }
        const sheets = c.events.filter((e) => e.method === 'CSS.styleSheetAdded' && e.sessionId === sessionId).map((e) => e.params.header.styleSheetId);
        for (const styleSheetId of sheets) {
          await record('CSS.getStyleSheetText', c.send('CSS.getStyleSheetText', { styleSheetId }, sessionId));
          await record('CSS.collectClassNames', c.send('CSS.collectClassNames', { styleSheetId }, sessionId));
        }
        const { root } = await c.send('DOM.getDocument', {}, sessionId);
        const body = await c.send('DOM.querySelector', { nodeId: root.nodeId, selector: 'body' }, sessionId);
        await record('CSS.getMatchedStylesForNode', c.send('CSS.getMatchedStylesForNode', { nodeId: body.nodeId }, sessionId));
        const { targetInfos } = await c.send('Target.getTargets', {});
        const shared = targetInfos.find((t) => t.type === 'shared_worker' && t.url.includes('kind=shared'));
        let sharedSession = null;
        if (shared) {
          sharedSession = (await c.send('Target.attachToTarget', { targetId: shared.targetId, flatten: true })).sessionId;
          sessions.add(sharedSession);
          await c.send('Debugger.enable', {}, sharedSession);
          await sleep(500);
        }
        const scripts = c.events.filter((e) => e.method === 'Debugger.scriptParsed' && sessions.has(e.sessionId) && e.params.url);
        for (const e of scripts) {
          const call = e.sessionId === sharedSession ? 'shared ' : kinds.get(e.sessionId);
          await record(`${call}Debugger.getScriptSource`, c.send('Debugger.getScriptSource', { scriptId: e.params.scriptId }, e.sessionId));
          await record(`${call}Debugger.searchInContent`, c.send('Debugger.searchInContent', { scriptId: e.params.scriptId, query: 'fake' }, e.sessionId));
        }
        await outcome(c.send('Target.closeTarget', { targetId }));
        const events = c.events.filter((e) => sessions.has(e.sessionId)).map((e) => ({ method: e.method, text: JSON.stringify(e.params) }));
        return { events, results: results.map((r) => ({ call: r.call, text: JSON.stringify(r) })) };
      }),
    serviceWorkerImports: (browser, page) =>
      run(browser, async (c) => {
        const results = [];
        const log = [];
        const started = Date.now();
        const step = (what) => log.push(`${Date.now() - started}ms ${what}`);
        const record = async (call, promise) => results.push({ call, ...(await outcome(promise)) });
        const { targetId, sessionId } = await c.attachTo(page);
        const workers = new Set();
        const kinds = new Map();
        c.on((e) => {
          if (e.method !== 'Target.attachedToTarget' || e.sessionId) return;
          const { sessionId: child, targetInfo } = e.params;
          step(`attached ${targetInfo.type} waiting=${e.params.waitingForDebugger}`);
          if (targetInfo.type === 'service_worker' || targetInfo.type === 'shared_worker') {
            workers.add(child);
            kinds.set(child, targetInfo.type === 'shared_worker' ? 'shared' : 'sw');
            c.send('Debugger.enable', {}, child)
              .then(() => step('sw Debugger.enable answered'), (err) => step(`sw Debugger.enable failed ${err.message}`));
          }
          c.send('Runtime.runIfWaitingForDebugger', {}, child)
            .then(() => step(`${targetInfo.type} runIfWaitingForDebugger answered`), (err) => step(`${targetInfo.type} run failed ${err.message}`));
        });
        await record('early shared', c.evaluate(`new Promise((resolve) => { new SharedWorker('/worker.js?kind=early'); setTimeout(() => resolve('started'), 1000); })`, sessionId));
        step('early shared started');
        await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
        step('auto-attach on');
        await record('sw ready', c.evaluate(`Promise.race([navigator.serviceWorker.register('/sw.js').then(() => navigator.serviceWorker.ready).then(() => 'ready'), new Promise((r) => setTimeout(() => r('timeout'), 15000))])`, sessionId));
        step('ready settled');
        await record('shared started', c.evaluate(`new Promise((resolve) => { const w = new SharedWorker('/worker.js?kind=shared2'); w.onerror = () => resolve('error'); setTimeout(() => resolve('started'), 1500); })`, sessionId));
        step('shared started');
        const { targetInfos } = await c.send('Target.getTargets', {});
        const early = targetInfos.find((t) => t.type === 'shared_worker' && t.url.includes('kind=early'));
        if (early) {
          const { sessionId: explicit } = await c.send('Target.attachToTarget', { targetId: early.targetId, flatten: true });
          step('early explicitly attached');
          await record('early Debugger.enable', Promise.race([c.send('Debugger.enable', {}, explicit), sleep(40000).then(() => { throw new Error('timeout'); })]));
          step('early Debugger.enable settled');
        }
        await record('sw redirect', c.evaluate(`navigator.serviceWorker.register('/r/sw.js', { scope: '/r/' }).then(() => 'registered')`, sessionId));
        await sleep(1000);
        const scripts = c.events.filter((e) => e.method === 'Debugger.scriptParsed' && workers.has(e.sessionId) && e.params.url);
        for (const e of scripts) {
          await record(`${kinds.get(e.sessionId)} Debugger.getScriptSource`, c.send('Debugger.getScriptSource', { scriptId: e.params.scriptId }, e.sessionId));
        }
        step('sources read');
        await outcome(c.send('Target.setAutoAttach', { autoAttach: false, waitForDebuggerOnStart: false }));
        await outcome(c.evaluate(`navigator.serviceWorker.getRegistrations().then((rs) => Promise.all(rs.map((r) => r.unregister()))).then(() => 'gone')`, sessionId));
        await outcome(c.send('Target.closeTarget', { targetId }));
        const events = c.events.filter((e) => workers.has(e.sessionId)).map((e) => ({ method: e.method, text: JSON.stringify(e.params) }));
        return { events, results: results.map((r) => ({ call: r.call, text: JSON.stringify(r) })), log };
      }),
    extraInfo: (browser, last) =>
      run(browser, async (c) => {
        const done = c.waitEvent((e) => e.method === 'Network.responseReceivedExtraInfo' && e.params.requestId === last, 15000);
        await c.send('Network.enable', {});
        await done;
        const events = c.events.filter((e) => e.method.startsWith('Network.')).map((e) => ({ method: e.method, requestId: e.params.requestId, text: JSON.stringify(e.params) }));
        return { events };
      }),
  };
}
