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

export function protectTest(getLink, describe) {
  const run = async (browser, fn) => {
    let c;
    try {
      c = await CdpClient.open(getLink(), browser);
      const result = await fn(c);
      result.closed = await c.close();
      return result;
    } catch (error) {
      return { ...describe(error), cdpError: error.cdp ?? null };
    }
  };
  return {
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
  };
}
