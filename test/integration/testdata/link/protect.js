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

async function instrumentedPopup(c, openerId, openerSession, base, tag, url) {
  await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
  const isPopup = (e) => e.method === 'Target.attachedToTarget' && e.params.targetInfo.openerId === openerId;
  const attached = c.waitEvent(isPopup, 20000);
  await c.send('Runtime.evaluate', { expression: `Boolean(window.open(${JSON.stringify(url)}))`, userGesture: true }, openerSession);
  const ev = await attached;
  const ps = ev.params.sessionId;
  await instrument(c, ps, base, tag);
  await c.send('Runtime.runIfWaitingForDebugger', {}, ps);
  return { ps, popup: ev.params.targetInfo.targetId };
}

const wide = 'iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAIAAAB7QOjdAAAADUlEQVR4nGP4z8AARAAI/gH/xp559wAAAABJRU5ErkJggg==';

const postLater = (url) =>
  `setTimeout(() => { const f = document.createElement('form'); f.method = 'POST'; f.action = ${JSON.stringify(url)}; const i = document.createElement('input'); i.name = 'x'; i.value = '1'; f.append(i); document.body.append(f); f.submit(); }, 50); true`;

const hostOf = (base) => new URL(base).host;
const originOf = (base) => new URL(base).origin;
const hop = (hops, end) => hops.reduceRight((to, [base, code]) => `${base}/hop?code=${code}&to=${encodeURIComponent(to)}`, end);
const isRefused = (r) => !r.ok && /^slicc: refused/.test(r.error?.message ?? '');

async function vlog(env, tag) {
  const r = await fetch(`${env.log}/log?n=${encodeURIComponent(tag)}`);
  return r.json();
}

const hit = (log, base, path) => log.hits.some((h) => h.startsWith(`${hostOf(base)} ${path}`));
const beaconed = (log, base) => log.beacons.includes(originOf(base));

async function until(fn, timeout = 8000) {
  const end = Date.now() + timeout;
  for (;;) {
    const v = await fn().catch(() => false);
    if (v || Date.now() > end) return v;
    await sleep(100);
  }
}

function detachedFrom(c, sessionId, timeout) {
  return c.waitEvent((e) => e.method === 'Target.detachedFromTarget' && e.params.sessionId === sessionId, timeout).then(
    () => true,
    () => false,
  );
}

async function settle(ctx, sessionId, landed, timeout = 10000) {
  let detached = false;
  const gone = detachedFrom(ctx.c, sessionId, timeout).then((d) => {
    detached = d;
    return d;
  });
  await Promise.race([gone, until(landed, timeout)]);
  await sleep(600);
  return detached;
}

async function leaveTo(ctx, sessionId, url, how, landing) {
  const { c, env, tag } = ctx;
  const expression = how === 'post' ? postLater(url) : later(url);
  const settled = settle(ctx, sessionId, async () => beaconed(await vlog(env, tag), landing));
  await c.send('Runtime.evaluate', { expression }, sessionId);
  return settled;
}

async function instrumentedTab(ctx) {
  const { c, env, tag } = ctx;
  const { targetId, sessionId } = await c.attachTo(`${env.allowed}/doc?n=start`);
  ctx.targets.push(targetId);
  ctx.errorPage.push(targetId);
  await instrument(c, sessionId, env.log, tag);
  await load(c, sessionId, `${env.allowed}/doc?n=next-${tag}`);
  return sessionId;
}

const landedOn = (ctx, log) => ({ beacon: beaconed(log, ctx.dest), loaded: hit(log, ctx.dest, `/doc?n=${ctx.tag}`) });

const navLanded = { allowedBeacon: true, beacon: true, loaded: true, detached: false };
const navBlocked = { allowedBeacon: true, beacon: false, loaded: false, detached: true };

function navVector(id, how, hops) {
  return {
    id,
    setup: 'an allowed tab with an init script that beacons its origin',
    trigger: async (ctx) => {
      const sessionId = await instrumentedTab(ctx);
      const away = hop(hops(ctx), `${ctx.dest}/doc?n=${ctx.tag}`);
      const detached = await leaveTo(ctx, sessionId, away, how, ctx.dest);
      const log = await vlog(ctx.env, ctx.tag);
      const hopped = hops(ctx).every(([base, code]) => hit(log, base, `/hop?code=${code}`));
      return { allowedBeacon: beaconed(log, ctx.env.allowed), ...landedOn(ctx, log), hopped, detached };
    },
    control: { origin: 'ordinary', expected: { ...navLanded, hopped: true } },
    expected: { ...navBlocked, hopped: true },
  };
}

function throughVector(id, code) {
  return {
    id,
    setup: 'an allowed tab with an init script that beacons its origin',
    trigger: async (ctx) => {
      const { env, tag } = ctx;
      const sessionId = await instrumentedTab(ctx);
      const after = `/doc?n=${tag}-after`;
      const away = hop([[env.allowed, 301], [ctx.dest, code]], `${env.cross}${after}`);
      const detached = await leaveTo(ctx, sessionId, away, 'href', env.cross);
      const log = await vlog(env, tag);
      return {
        allowedBeacon: beaconed(log, env.allowed),
        protectedHop: hit(log, ctx.dest, `/hop?code=${code}`),
        landed: hit(log, env.cross, after),
        afterBeacon: beaconed(log, env.cross),
        detached,
      };
    },
    control: { origin: 'ordinary', expected: { allowedBeacon: true, protectedHop: true, landed: true, afterBeacon: true, detached: false } },
    expected: { allowedBeacon: true, protectedHop: false, landed: false, afterBeacon: false, detached: true },
  };
}

async function popupOf(c, openerId, timeout = 10000) {
  const end = Date.now() + timeout;
  for (;;) {
    const { targetInfos } = await c.send('Target.getTargets');
    const p = targetInfos.find((t) => t.openerId === openerId && t.url);
    if (p || Date.now() > end) return p;
    await sleep(100);
  }
}

function popupRedirectVector(id, codes) {
  return {
    id,
    setup: 'an allowed opener with auto-attach that instruments every popup it gets',
    trigger: async (ctx) => {
      const { c, env, tag } = ctx;
      const { targetId, sessionId } = await c.attachTo(`${env.allowed}/doc?n=opener`);
      ctx.targets.push(targetId);
      await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
      const isPopup = (e) => e.method === 'Target.attachedToTarget' && e.params.targetInfo.openerId === targetId;
      const bases = [env.allowed, env.cross];
      const url = hop(codes.map((code, i) => [bases[i % 2], code]), `${ctx.dest}/doc?n=${tag}`);
      await c.send('Runtime.evaluate', { expression: `Boolean(window.open(${JSON.stringify(url)}))`, userGesture: true }, sessionId);
      const end = Date.now() + 10000;
      let p = await popupOf(c, targetId);
      while (p && !p.url.startsWith(originOf(ctx.dest)) && Date.now() < end) {
        await sleep(100);
        p = await popupOf(c, targetId);
      }
      if (p) ctx.targets.push(p.targetId);
      const ev = await c.waitEvent(isPopup, 3000).catch(() => null);
      if (ev) {
        await instrument(c, ev.params.sessionId, env.log, tag);
        await c.send('Page.reload', {}, ev.params.sessionId).catch(() => {});
        await until(async () => beaconed(await vlog(env, tag), ctx.dest), 5000);
      }
      await c.send('Target.setAutoAttach', { autoAttach: false, waitForDebuggerOnStart: false, flatten: true });
      const log = await vlog(env, tag);
      return { committed: Boolean(p?.url.startsWith(originOf(ctx.dest))), attached: Boolean(ev), beacon: beaconed(log, ctx.dest) };
    },
    control: { origin: 'ordinary', expected: { committed: true, attached: true, beacon: true } },
    expected: { committed: true, attached: false, beacon: false },
  };
}

async function autoAttachRound(c, targetId, waitForDebuggerOnStart) {
  const mine = (e) => e.method === 'Target.attachedToTarget' && e.params.targetInfo.targetId === targetId;
  for (const e of c.events.filter(mine)) await outcome(c.send('Target.detachFromTarget', { sessionId: e.params.sessionId }));
  await c.send('Target.setAutoAttach', { autoAttach: false, waitForDebuggerOnStart: false, flatten: true });
  const before = c.events.filter(mine).length;
  await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart, flatten: true });
  const ev = await c.waitEvent((e) => mine(e) && c.events.filter(mine).indexOf(e) >= before, 2000).catch(() => null);
  if (ev?.params.waitingForDebugger) await outcome(c.send('Runtime.runIfWaitingForDebugger', {}, ev.params.sessionId));
  return Boolean(ev);
}

function staysOutVector(id, viaRedirect) {
  return {
    id,
    setup: `an allowed opener with auto-attach opens a popup ${viaRedirect ? 'on a redirect chain that ends' : 'straight'} on a page that later navigates itself to another path of its origin; a second client runs its own auto-attach rounds alongside`,
    trigger: async (ctx) => {
      const { c, env, tag } = ctx;
      const [c2, { targetId, sessionId }] = await Promise.all([ctx.open(), c.attachTo(`${env.allowed}/doc?n=opener`)]);
      ctx.targets.push(targetId);
      try {
        await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
        const later = `${ctx.dest}/doc?n=${tag}-later`;
        const page = `${ctx.dest}/selfnav?n=${tag}&ms=6000&to=${encodeURIComponent(later)}`;
        const url = viaRedirect ? hop([[env.allowed, 302], [env.cross, 307]], page) : page;
        await c.send('Runtime.evaluate', { expression: `Boolean(window.open(${JSON.stringify(url)}))`, userGesture: true }, sessionId);
        let p = await popupOf(c, targetId);
        for (const end = Date.now() + 10000; p && !p.url.startsWith(originOf(ctx.dest)) && Date.now() < end; p = await popupOf(c, targetId)) await sleep(100);
        if (!p) return { opened: false };
        ctx.targets.push(p.targetId);
        const mine = (e) => e.method === 'Target.attachedToTarget' && e.params.targetInfo.targetId === p.targetId;
        const [atOpen, noWait] = await Promise.all([c.waitEvent(mine, 2000).then(() => true, () => false), autoAttachRound(c2, p.targetId, false)]);
        for (const e of c.events.filter(mine)) await outcome(c.send('Target.detachFromTarget', { sessionId: e.params.sessionId }));
        const attach = await outcome(c.send('Target.attachToTarget', { targetId: p.targetId, flatten: true }));
        if (attach.ok) await outcome(c.send('Target.detachFromTarget', { sessionId: attach.result.sessionId }));
        const activate = await outcome(c.send('Target.activateTarget', { targetId: p.targetId }));
        const info = await outcome(c.send('Target.getTargetInfo', { targetId: p.targetId }));
        const navigatedLater = await until(async () => hit(await vlog(env, tag), ctx.dest, `/doc?n=${tag}-later`), 15000);
        await sleep(500);
        const [laterWait, laterNoWait] = await Promise.all([autoAttachRound(c, p.targetId, true), autoAttachRound(c2, p.targetId, false)]);
        const attachLater = await outcome(c.send('Target.attachToTarget', { targetId: p.targetId, flatten: true }));
        const infoLater = await outcome(c.send('Target.getTargetInfo', { targetId: p.targetId }));
        await c.send('Target.setAutoAttach', { autoAttach: false, waitForDebuggerOnStart: false, flatten: true });
        await c2.send('Target.setAutoAttach', { autoAttach: false, waitForDebuggerOnStart: false, flatten: true });
        const close = await outcome(c.send('Target.closeTarget', { targetId: p.targetId }));
        const masked = (r) => r.ok && [originOf(ctx.dest), `${originOf(ctx.dest)}/`].includes(r.result.targetInfo.url);
        return {
          opened: true,
          autoAttachedAtOpen: atOpen,
          autoAttachedNoWait: noWait,
          attach: attach.ok,
          activate: activate.ok,
          infoMasked: masked(info),
          navigatedLater: Boolean(navigatedLater),
          autoAttachedLaterWait: laterWait,
          autoAttachedLaterNoWait: laterNoWait,
          attachLater: attachLater.ok,
          infoMaskedLater: masked(infoLater),
          close: close.ok,
          everAttached: c.events.some(mine) || c2.events.some(mine),
          refused: [attach, activate, attachLater, close].every(isRefused),
          url: info.ok ? info.result.targetInfo.url : '',
        };
      } finally {
        await c2.close();
      }
    },
    control: {
      origin: 'ordinary',
      expected: {
        opened: true, autoAttachedAtOpen: true, autoAttachedNoWait: true, attach: true, activate: true, infoMasked: false, navigatedLater: true,
        autoAttachedLaterWait: true, autoAttachedLaterNoWait: true, attachLater: true, infoMaskedLater: false, close: true, everAttached: true, refused: false,
      },
    },
    expected: {
      opened: true, autoAttachedAtOpen: false, autoAttachedNoWait: false, attach: false, activate: false, infoMasked: true, navigatedLater: true,
      autoAttachedLaterWait: false, autoAttachedLaterNoWait: false, attachLater: false, infoMaskedLater: true, close: false, everAttached: false, refused: true,
    },
  };
}

function frameVector(id, mode, sameSite) {
  return {
    id,
    setup: `a plain ${sameSite ? 'same-site' : 'cross-site'} page with an init script, auto-attaching and instrumenting its frames`,
    skip: sameSite ? { vault: 'every sliccy.ai host is protected, so no unprotected page is same-site with the vault' } : {},
    trigger: async (ctx) => {
      const { c, env, tag } = ctx;
      const page = sameSite ? env.sameSite[ctx.origin] : env.allowed;
      const { targetId, sessionId } = await c.attachTo(`${page}/doc?n=frame-start`);
      ctx.targets.push(targetId);
      await instrument(c, sessionId, env.log, tag);
      const children = [];
      c.on((e) => {
        if (e.method !== 'Target.attachedToTarget' || e.sessionId !== sessionId) return;
        const child = e.params.sessionId;
        children.push(e.params.targetInfo.url);
        const waiting = e.params.waitingForDebugger;
        (async () => {
          const steps = await Promise.all([
            outcome(c.send('Page.enable', {}, child)),
            outcome(c.send('Page.addScriptToEvaluateOnNewDocument', { source: beacon(env.log, tag) }, child)),
          ]);
          children.push(steps.map((r) => (r.ok ? 'ok' : r.error.message)).join(','));
          if (waiting) await c.send('Runtime.runIfWaitingForDebugger', {}, child);
          else await c.send('Runtime.evaluate', { expression: 'location.reload(); true' }, child);
        })().catch((error) => children.push(String(error?.message ?? error)));
      });
      await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true }, sessionId);
      const src = `${ctx.dest}/doc?n=${tag}`;
      if (mode === 'static') {
        await load(c, sessionId, `${page}/framed?src=${encodeURIComponent(src)}`);
      } else {
        await load(c, sessionId, `${page}/doc?n=frame-host`);
        await c.evaluate(
          `new Promise((resolve) => { const f = document.createElement('iframe'); f.onload = () => resolve(true); setTimeout(() => resolve(false), 10000); f.src = ${JSON.stringify(src)}; document.body.append(f); })`,
          sessionId,
        );
      }
      await until(async () => beaconed(await vlog(env, tag), ctx.dest), 4000);
      const { frameTree } = await c.send('Page.getFrameTree', {}, sessionId);
      const frames = (frameTree.childFrames ?? []).map((f) => f.frame);
      const { targetInfos } = await c.send('Target.getTargets');
      const frameTargets = targetInfos.filter((t) => t.type === 'iframe').map((t) => t.url);
      const log = await vlog(env, tag);
      return {
        pageBeacon: beaconed(log, page),
        ...landedOn(ctx, log),
        frameOnDest: [...frames.map((f) => f.url), ...frameTargets].some((u) => u.startsWith(originOf(ctx.dest))),
        errorFrame: frames.some((f) => f.url.startsWith('chrome-error://') && (f.unreachableUrl ?? '').startsWith(src)),
        children: children.join(' '),
      };
    },
    control: { origin: 'ordinary', expected: { pageBeacon: true, beacon: true, loaded: true, frameOnDest: true, errorFrame: false } },
    expected: { pageBeacon: true, beacon: false, loaded: false, frameOnDest: false, errorFrame: true },
  };
}

const bounded = (expression, ms = 10000) => `Promise.race([${expression}, new Promise((resolve) => setTimeout(() => resolve('timeout'), ${ms}))])`;

const textMarker = { fulfilled: 'fulfilled', real: (tag) => `real-${tag}` };
const subresources = {
  fetch: { path: 'data', run: (url) => `fetch(${JSON.stringify(url)}).then((r) => r.text(), () => 'error')`, fulfilled: 'fulfilled', real: textMarker.real },
  xhr: {
    path: 'data',
    run: (url) => `new Promise((resolve) => { const x = new XMLHttpRequest(); x.open('GET', ${JSON.stringify(url)}); x.onload = () => resolve(x.responseText); x.onerror = () => resolve('error'); x.send(); })`,
    fulfilled: 'fulfilled',
    real: textMarker.real,
  },
  image: {
    path: 'img',
    run: (url) => `new Promise((resolve) => { const i = new Image(); i.onload = () => resolve('width ' + i.naturalWidth); i.onerror = () => resolve('error'); i.src = ${JSON.stringify(url)}; })`,
    fulfilled: 'width 2',
    real: () => 'width 1',
  },
};

async function pagedFetch(ctx, enable, onEvent) {
  const { c, env, tag } = ctx;
  const { targetId, sessionId } = await c.attachTo(`${env.allowed}/doc?n=fetcher-${tag}`);
  ctx.targets.push(targetId);
  c.on((e) => {
    if (e.sessionId === sessionId) onEvent(e, sessionId);
  });
  await c.send(enable.method, enable.params, sessionId);
  return { targetId, sessionId };
}

const pausedURL = (e) => e.params.request?.url;
const bothStages = { method: 'Fetch.enable', params: { patterns: [{ urlPattern: '*', requestStage: 'Request' }, { urlPattern: '*', requestStage: 'Response' }] } };
const atResponse = (e) => e.params.responseStatusCode !== undefined || e.params.responseErrorReason !== undefined;
const corsHeaders = (type) => [
  { name: 'content-type', value: type },
  { name: 'access-control-allow-origin', value: '*' },
];

function subresourceVector(id, kind) {
  const r = subresources[kind];
  return {
    id,
    setup: "a plain page whose own Fetch.enable pauses every request at both stages, and fulfils the probe's",
    trigger: async (ctx) => {
      const { c, tag } = ctx;
      const url = `${ctx.dest}/${r.path}?n=${tag}`;
      const seen = [];
      const { sessionId } = await pagedFetch(ctx, bothStages, (e, s) => {
        if (e.method !== 'Fetch.requestPaused') return;
        const id = e.params.requestId;
        if (pausedURL(e) === url) seen.push(atResponse(e) ? 'response' : 'request');
        if (pausedURL(e) === url && !atResponse(e)) {
          const body = kind === 'image' ? wide : btoa(r.fulfilled);
          c.send('Fetch.fulfillRequest', { requestId: id, responseCode: 200, responseHeaders: corsHeaders(kind === 'image' ? 'image/png' : 'text/plain'), body }, s).catch(() => {});
          return;
        }
        c.send('Fetch.continueRequest', { requestId: id }, s).catch(() => {});
      });
      const body = await c.evaluate(bounded(r.run(url)), sessionId);
      return { seen: seen.length > 0, fulfilled: body === r.fulfilled, answered: body === r.real(tag) };
    },
    control: { origin: 'ordinary', expected: { seen: true, fulfilled: true, answered: false } },
    expected: { seen: false, fulfilled: false, answered: true },
  };
}

const vectors = [
  navVector('C1.script-nav', 'href', () => []),
  navVector('C2.chain-301-302-303', 'href', (ctx) => [
    [ctx.env.allowed, 301],
    [ctx.env.cross, 302],
    [ctx.env.allowed, 303],
  ]),
  navVector('C2.chain-307-308-302', 'href', (ctx) => [
    [ctx.env.allowed, 307],
    [ctx.env.cross, 308],
    [ctx.env.allowed, 302],
  ]),
  navVector('C2.chain-303-301-308', 'href', (ctx) => [
    [ctx.env.cross, 303],
    [ctx.env.allowed, 301],
    [ctx.env.cross, 308],
  ]),
  navVector('C2.post-307-308', 'post', (ctx) => [
    [ctx.env.allowed, 307],
    [ctx.env.cross, 308],
  ]),
  navVector('C2.post-308-307', 'post', (ctx) => [
    [ctx.env.cross, 308],
    [ctx.env.allowed, 307],
  ]),
  throughVector('C2.through-302', 302),
  throughVector('C2.through-307', 307),
  {
    id: 'C3.popup-self-nav',
    setup: 'an allowed opener whose auto-attached popup gets an init script, then loads an allowed page',
    trigger: async (ctx) => {
      const { c, env, tag } = ctx;
      const { targetId, sessionId } = await c.attachTo(`${env.allowed}/doc?n=opener`);
      ctx.targets.push(targetId);
      const popup = `${env.allowed}/doc?n=popup-${tag}`;
      const p = await instrumentedPopup(c, targetId, sessionId, env.log, tag, popup);
      ctx.targets.push(p.popup);
      ctx.errorPage.push(p.popup);
      await load(c, p.ps, `${popup}&again=1`);
      const detached = await leaveTo(ctx, p.ps, `${ctx.dest}/doc?n=${tag}`, 'href', ctx.dest);
      await c.send('Target.setAutoAttach', { autoAttach: false, waitForDebuggerOnStart: false, flatten: true });
      const log = await vlog(env, tag);
      return { allowedBeacon: beaconed(log, env.allowed), ...landedOn(ctx, log), detached };
    },
    control: { origin: 'ordinary', expected: navLanded },
    expected: navBlocked,
  },
  popupRedirectVector('C3.popup-on-redirect-302', [302]),
  popupRedirectVector('C3.popup-on-redirect-chain', [301, 307, 303]),
  staysOutVector('C3.redirect-popup-stays-out', true),
  staysOutVector('C3.direct-popup-stays-out', false),
  frameVector('C4.static-cross-site', 'static', false),
  frameVector('C4.script-cross-site', 'script', false),
  frameVector('C4.static-same-site', 'static', true),
  frameVector('C4.script-same-site', 'script', true),
  {
    id: 'C5.document',
    setup: "a plain page whose own Fetch.enable pauses every request at both stages, and fulfils the probe's document with a forged one",
    trigger: async (ctx) => {
      const { c, tag } = ctx;
      const url = `${ctx.dest}/doc?n=${tag}`;
      const seen = [];
      const { targetId, sessionId } = await pagedFetch(ctx, bothStages, (e, s) => {
        if (e.method !== 'Fetch.requestPaused') return;
        const id = e.params.requestId;
        if (pausedURL(e) === url) {
          seen.push(atResponse(e) ? 'response' : 'request');
          if (!atResponse(e)) {
            c.send('Fetch.fulfillRequest', { requestId: id, responseCode: 200, responseHeaders: corsHeaders('text/html'), body: btoa('<!doctype html><title>forged</title>') }, s).catch(() => {});
            return;
          }
        }
        c.send('Fetch.continueRequest', { requestId: id }, s).catch(() => {});
      });
      ctx.errorPage.push(targetId);
      await c.send('Page.enable', {}, sessionId);
      const settled = settle(ctx, sessionId, async () => (await c.evaluate('document.title', sessionId)) === 'forged');
      await c.send('Runtime.evaluate', { expression: later(url) }, sessionId);
      const detached = await settled;
      const title = detached ? '' : await c.evaluate('document.title', sessionId).catch(() => '');
      return { seen: seen.length > 0, forged: title === 'forged', detached };
    },
    control: { origin: 'ordinary', expected: { seen: true, forged: true, detached: false } },
    expected: { seen: false, forged: false, detached: true },
  },
  subresourceVector('C5.fetch', 'fetch'),
  subresourceVector('C5.xhr', 'xhr'),
  subresourceVector('C5.image', 'image'),
  {
    id: 'C5.auth',
    setup: "a plain page whose own Fetch.enable handles auth requests, and answers the probe's challenge with credentials; a second tab without Fetch shows the untouched outcome",
    trigger: async (ctx) => {
      const { c, tag } = ctx;
      const url = `${ctx.dest}/auth?n=${tag}`;
      const seen = [];
      const enable = { method: 'Fetch.enable', params: { patterns: [{ urlPattern: '*' }], handleAuthRequests: true } };
      const { targetId: pt, sessionId: plainS } = await c.attachTo(`${ctx.env.allowed}/doc?n=plain-${tag}`);
      ctx.targets.push(pt);
      const probe = (u) => bounded(`fetch(${JSON.stringify(u)}, { credentials: 'include' }).then(async (r) => r.status + ' ' + (await r.text()), () => 'error')`, 3000);
      const plain = await c.evaluate(probe(`${url}&plain=1`), plainS);
      const { sessionId } = await pagedFetch(ctx, enable, (e, s) => {
        const id = e.params.requestId;
        if (e.method === 'Fetch.requestPaused') {
          if (pausedURL(e) === url) seen.push('paused');
          c.send('Fetch.continueRequest', { requestId: id }, s).catch(() => {});
        } else if (e.method === 'Fetch.authRequired') {
          const mine = pausedURL(e) === url;
          if (mine) seen.push('auth');
          const authChallengeResponse = mine ? { response: 'ProvideCredentials', username: 'u', password: 'p' } : { response: 'Default' };
          c.send('Fetch.continueWithAuth', { requestId: id, authChallengeResponse }, s).catch(() => {});
        }
      });
      const body = await c.evaluate(probe(url), sessionId);
      return { plain, body, seen: seen.includes('auth'), pausedSeen: seen.includes('paused'), authed: body === '200 authed', untouched: body === plain };
    },
    control: { origin: 'ordinary', expected: { seen: true, pausedSeen: true, authed: true, untouched: false } },
    expected: { seen: false, pausedSeen: false, authed: false, untouched: true },
  },
  {
    id: 'C5.guessed-id',
    setup: "a plain page whose own Fetch.enable holds the probe's request, then fulfils every request id between two of its own",
    trigger: async (ctx) => {
      const { c, env, tag } = ctx;
      const url = `${ctx.dest}/data?n=${tag}`;
      const ids = new Map();
      const enable = { method: 'Fetch.enable', params: { patterns: [{ urlPattern: '*', requestStage: 'Request' }] } };
      const { sessionId } = await pagedFetch(ctx, enable, (e, s) => {
        if (e.method !== 'Fetch.requestPaused') return;
        ids.set(pausedURL(e), e.params.requestId);
        if (pausedURL(e) !== url) c.send('Fetch.continueRequest', { requestId: e.params.requestId }, s).catch(() => {});
      });
      const before = `${env.allowed}/data?n=a-${tag}`;
      const after = `${env.allowed}/data?n=b-${tag}`;
      await c.evaluate(bounded(subresources.fetch.run(before)), sessionId);
      await c.evaluate(`window.__probe = ${bounded(subresources.fetch.run(url))}; true`, sessionId);
      await sleep(300);
      await c.evaluate(bounded(subresources.fetch.run(after)), sessionId);
      const num = (id) => Number(/(\d+)\.\d+$/.exec(id ?? '')?.[1]);
      const [lo, hi] = [num(ids.get(before)), num(ids.get(after))];
      const prefix = (ids.get(before) ?? '').replace(/\d+\.\d+$/, '');
      const attempts = [];
      for (let n = lo + 1; n < hi; n += 1) {
        attempts.push(await outcome(c.send('Fetch.fulfillRequest', { requestId: `${prefix}${n}.0`, responseCode: 200, responseHeaders: corsHeaders('text/plain'), body: btoa('guessed') }, sessionId)));
      }
      const body = await c.evaluate('window.__probe', sessionId);
      return {
        candidates: attempts.length > 0,
        fulfilled: body === 'guessed',
        refused: attempts.length > 0 && attempts.every(isRefused),
        answered: body === `real-${tag}`,
      };
    },
    control: { origin: 'ordinary', expected: { candidates: true, fulfilled: true, refused: false, answered: false } },
    expected: { candidates: true, fulfilled: false, refused: true, answered: true },
  },
  {
    id: 'C6.read-cookies',
    setup: 'the runner plants a cookie named <env.cookie><origin> on each origin with its own, unfiltered CDP',
    trigger: async (ctx) => {
      const { c, env, origin } = ctx;
      const { targetId, sessionId } = await c.attachTo('about:blank');
      ctx.targets.push(targetId);
      const name = env.cookie + origin;
      const has = async (method, params, s) => {
        const r = await outcome(c.send(method, params, s));
        return r.ok && r.result.cookies.some((k) => k.name === name);
      };
      return {
        byURL: await has('Network.getCookies', { urls: [`${ctx.dest}/`] }, sessionId),
        mixedURLs: await has('Network.getCookies', { urls: [`${env.dest.vault}/`, `${env.dest.seven}/`, `${env.dest.ordinary}/`] }, sessionId),
        all: await has('Network.getAllCookies', {}, sessionId),
        storage: await has('Storage.getCookies', {}),
      };
    },
    control: { origin: 'ordinary', expected: { byURL: true, mixedURLs: true, all: true, storage: true } },
    expected: { byURL: false, mixedURLs: false, all: false, storage: false },
  },
  {
    id: 'C6.write-cookies',
    setup: 'a session on about:blank',
    trigger: async (ctx) => {
      const { c, tag } = ctx;
      const { targetId, sessionId } = await c.attachTo('about:blank');
      ctx.targets.push(targetId);
      const url = `${ctx.dest}/`;
      const domain = new URL(ctx.dest).hostname;
      const n = (i) => `w${i}-${tag}`;
      const ops = {
        setURL: await outcome(c.send('Network.setCookie', { name: n(1), value: '1', url }, sessionId)),
        setDomain: await outcome(c.send('Network.setCookie', { name: n(2), value: '1', domain, path: '/' }, sessionId)),
        setMany: await outcome(c.send('Network.setCookies', { cookies: [{ name: n(3), value: '1', url }] }, sessionId)),
        storageSet: await outcome(c.send('Storage.setCookies', { cookies: [{ name: n(4), value: '1', domain, path: '/' }] })),
        remove: await outcome(c.send('Network.deleteCookies', { name: n(1), url }, sessionId)),
      };
      const read = await outcome(c.send('Network.getCookies', { urls: [url] }, sessionId));
      const names = read.ok ? read.result.cookies.map((k) => k.name) : [];
      for (const i of [2, 3, 4]) await outcome(c.send('Network.deleteCookies', { name: n(i), url }, sessionId));
      return {
        setURL: ops.setURL.ok,
        setDomain: ops.setDomain.ok,
        setMany: ops.setMany.ok,
        storageSet: ops.storageSet.ok,
        deleted: ops.remove.ok && ops.setURL.ok && !names.includes(n(1)),
        written: [2, 3, 4].every((i) => names.includes(n(i))),
        refused: Object.values(ops).every((r) => isRefused(r) && /cookies on seven's origin/.test(r.error.message)),
      };
    },
    control: { origin: 'ordinary', expected: { setURL: true, setDomain: true, setMany: true, storageSet: true, deleted: true, written: true, refused: false } },
    expected: { setURL: false, setDomain: false, setMany: false, storageSet: false, deleted: false, written: false, refused: true },
  },
  {
    id: 'C6.clear-cookies',
    setup: 'the control clears a browser context the stream created; seven and the vault live in the default context',
    trigger: async (ctx) => {
      const { c } = ctx;
      const own = ctx.origin === 'ordinary' ? (await c.send('Target.createBrowserContext')).browserContextId : undefined;
      const { targetId, sessionId } = await c.attachTo('about:blank', own ? { browserContextId: own } : {});
      ctx.targets.push(targetId);
      const network = await outcome(c.send('Network.clearBrowserCookies', {}, sessionId));
      const storage = await outcome(c.send('Storage.clearCookies', own ? { browserContextId: own } : {}));
      if (own) {
        await c.send('Target.closeTarget', { targetId });
        await c.send('Target.disposeBrowserContext', { browserContextId: own });
      }
      return { network: network.ok, storage: storage.ok, refused: isRefused(network) && isRefused(storage) };
    },
    control: { origin: 'ordinary', expected: { network: true, storage: true, refused: false } },
    expected: { network: false, storage: false, refused: true },
  },
  {
    id: 'C6.storage',
    setup: 'a session on an allowed page; each command names the origin or its storage key',
    trigger: async (ctx) => {
      const { c, env } = ctx;
      const { targetId, sessionId } = await c.attachTo(`${env.allowed}/doc?n=storage`);
      ctx.targets.push(targetId);
      const origin = originOf(ctx.dest);
      const storageKey = `${origin}/`;
      const calls = {
        clearDataForOrigin: ['Storage.clearDataForOrigin', { origin, storageTypes: 'local_storage' }],
        getUsageAndQuota: ['Storage.getUsageAndQuota', { origin }],
        clearDataForStorageKey: ['Storage.clearDataForStorageKey', { storageKey, storageTypes: 'local_storage' }],
        domStorageItems: ['DOMStorage.getDOMStorageItems', { storageId: { securityOrigin: origin, isLocalStorage: true } }, true],
        domStorageByKey: ['DOMStorage.getDOMStorageItems', { storageId: { storageKey, isLocalStorage: true } }, true],
        domStorageSet: ['DOMStorage.setDOMStorageItem', { storageId: { securityOrigin: origin, isLocalStorage: true }, key: 'k', value: 'v' }, true],
        indexedDBNames: ['IndexedDB.requestDatabaseNames', { securityOrigin: origin }, true],
        indexedDBByKey: ['IndexedDB.requestDatabaseNames', { storageKey }, true],
        cacheNames: ['CacheStorage.requestCacheNames', { securityOrigin: origin }, true],
        cacheByKey: ['CacheStorage.requestCacheNames', { storageKey }, true],
      };
      const refused = {};
      for (const [name, [method, params, onPage]] of Object.entries(calls)) {
        refused[name] = isRefused(await outcome(c.send(method, params, onPage ? sessionId : undefined)));
      }
      const values = Object.values(refused);
      return { reachedAll: values.every((r) => !r), refusedAll: values.every((r) => r), refused: Object.keys(refused).filter((k) => refused[k]).join(' ') };
    },
    control: { origin: 'ordinary', expected: { reachedAll: true, refusedAll: false } },
    expected: { reachedAll: false, refusedAll: true },
  },
];

export const protectVectors = vectors.map(({ id, setup, control, expected, skip }) => ({ id, setup, control, expected, skip: skip ?? {} }));

async function runVector(c, id, origin, env, open) {
  const v = vectors.find((x) => x.id === id);
  if (!v) throw new Error(`no vector ${id}`);
  if (v.skip?.[origin]) return { skipped: v.skip[origin], pass: true, targets: [] };
  const tag = `${id}-${origin}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`.replace(/[^A-Za-z0-9-]/g, '-');
  const ctx = { c, open, env, origin, dest: env.dest[origin], tag, targets: [], errorPage: [] };
  const control = origin === v.control.origin;
  try {
    const observed = await v.trigger(ctx);
    const expected = control ? v.control.expected : v.expected;
    const mismatches = Object.entries(expected)
      .filter(([k, want]) => observed[k] !== want)
      .map(([k, want]) => `${k}: got ${JSON.stringify(observed[k])}, want ${JSON.stringify(want)}`);
    return { pass: mismatches.length === 0, observed, expected, mismatches, targets: ctx.targets, errorPage: control ? [] : ctx.errorPage };
  } catch (error) {
    error.targets = ctx.targets;
    throw error;
  }
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
    vectorList: () => protectVectors.map(({ id, control, skip }) => ({ id, case: id.split('.')[0], control: control.origin, skip })),
    vector: (browser, id, origin, env) => run(browser, (c) => runVector(c, id, origin, env, () => CdpClient.open(getLink(), browser))),
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
