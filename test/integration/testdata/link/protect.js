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
        for (const [method, params] of calls) {
          const started = Date.now();
          const r = await outcome(c.send(method, params));
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
