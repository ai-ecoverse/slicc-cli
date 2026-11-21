import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { outcome, until } from './persistent.js';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const HOLD_METHODS = ['Page.frameNavigated', 'Page.frameStartedNavigating', 'Target.targetInfoChanged'];
const READ_TIMEOUT_MS = 15000;

function summarizeFrame(frame) {
  if (!frame) return null;
  return {
    id: frame.id ?? '',
    url: frame.url ?? '',
    securityOrigin: frame.securityOrigin ?? '',
    unreachableUrl: frame.unreachableUrl ?? '',
    loaderId: frame.loaderId ?? '',
    mimeType: frame.mimeType ?? '',
    adFrameStatus: frame.adFrameStatus ?? null,
    name: frame.name ?? '',
  };
}

function summarizeContext(c) {
  return {
    id: c?.id,
    uniqueId: c?.uniqueId ?? '',
    origin: c?.origin ?? '',
    name: c?.name ?? '',
    auxData: {
      frameId: c?.auxData?.frameId ?? '',
      isDefault: Boolean(c?.auxData?.isDefault),
      type: c?.auxData?.type ?? '',
    },
  };
}

function classifyReply(reply) {
  if (reply.ok) {
    return { status: 'answered', result: reply.result };
  }
  const message = reply.error?.message ?? JSON.stringify(reply.error);
  if (/timed out/i.test(String(message))) {
    return { status: 'timed-out', error: reply.error };
  }
  return { status: 'refused', error: reply.error };
}

async function pollTargetInfo(client, targetId, pred, ms = 10000) {
  const deadline = Date.now() + ms;
  let last = null;
  while (Date.now() < deadline) {
    const reply = await outcome(client.send('Target.getTargetInfo', { targetId }));
    if (reply.ok) {
      last = reply.result.targetInfo;
      if (pred(last)) return last;
    }
    await sleep(100);
  }
  return last;
}

async function makeBlobURL(reader, sessionId, marker) {
  return reader.send('Runtime.evaluate', {
    expression: `(() => {
      const html = '<!doctype html><title>blob ' + ${JSON.stringify(marker)} + '</title><body data-marker="' + ${JSON.stringify(marker)} + '"><p>blob ' + ${JSON.stringify(marker)} + '</p>';
      return URL.createObjectURL(new Blob([html], { type: 'text/html' }));
    })()`,
    returnByValue: true,
    awaitPromise: true,
  }, sessionId).then((r) => {
    if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails));
    return r.result.value;
  });
}

async function makeFilesystemURL(reader, sessionId, marker) {
  return reader.send('Runtime.evaluate', {
    expression: `(() => new Promise((resolve, reject) => {
      const req = window.webkitRequestFileSystem || window.requestFileSystem;
      if (!req) { reject(new Error('filesystem API unavailable')); return; }
      req(window.TEMPORARY, 1024 * 1024, (fs) => {
        fs.root.getFile('diagshape-' + ${JSON.stringify(marker)} + '.html', { create: true }, (entry) => {
          entry.createWriter((writer) => {
            writer.onwriteend = () => resolve(entry.toURL());
            writer.onerror = () => reject(writer.error || new Error('filesystem write failed'));
            const html = '<!doctype html><title>filesystem ' + ${JSON.stringify(marker)} + '</title><body data-marker="' + ${JSON.stringify(marker)} + '"><p>filesystem ' + ${JSON.stringify(marker)} + '</p>';
            writer.write(new Blob([html], { type: 'text/html' }));
          }, reject);
        }, reject);
      }, reject);
    }))()`,
    returnByValue: true,
    awaitPromise: true,
  }, sessionId).then((r) => {
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? r.exceptionDetails.text ?? JSON.stringify(r.exceptionDetails));
    return r.result.value;
  });
}

async function makeSandboxedNullBlobURL(reader, sessionId, marker) {
  return reader.send('Runtime.evaluate', {
    expression: `(() => new Promise((resolve, reject) => {
      const marker = ${JSON.stringify(marker)};
      const frame = document.createElement('iframe');
      frame.setAttribute('sandbox', 'allow-scripts');
      frame.srcdoc = '<!doctype html><script>(function(){var m=' + JSON.stringify(marker) + ';var html="<!doctype html><title>null-blob "+m+"</title><body data-marker=\\""+m+"\\"><p>null-blob</p>";window.parent.postMessage({kind:"diagshape-null-blob",url:URL.createObjectURL(new Blob([html],{type:"text/html"}))},"*");})();</'+'script>';
      const timer = setTimeout(() => { cleanup(); reject(new Error('sandboxed blob postMessage timed out')); }, 5000);
      function onMessage(ev) {
        if (!ev.data || ev.data.kind !== 'diagshape-null-blob' || typeof ev.data.url !== 'string') return;
        cleanup();
        resolve(ev.data.url);
      }
      function cleanup() {
        clearTimeout(timer);
        window.removeEventListener('message', onMessage);
        frame.remove();
      }
      window.addEventListener('message', onMessage);
      document.body.append(frame);
    }))()`,
    returnByValue: true,
    awaitPromise: true,
  }, sessionId).then((r) => {
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description ?? r.exceptionDetails.text ?? JSON.stringify(r.exceptionDetails));
    return r.result.value;
  });
}

async function navigateObserver(reader, sessionId, targetId, url, settleMs) {
  const start = reader.events.length;
  const nav = await outcome(reader.send('Page.navigate', { url }, sessionId));
  const deadline = Date.now() + settleMs;
  let info = null;
  while (Date.now() < deadline) {
    info = await pollTargetInfo(reader, targetId, () => true, 500);
    const navigated = reader.events.slice(start).find((e) => e.sessionId === sessionId && e.method === 'Page.frameNavigated' && e.params?.frame?.id === targetId);
    if (navigated || (info && info.url && info.url !== 'about:blank' && (info.url === url || info.url.startsWith(url.split('#')[0]) || String(url).startsWith('blob:') || String(url).startsWith('filesystem:') || String(url).startsWith('view-source:')))) {
      await sleep(200);
      info = await pollTargetInfo(reader, targetId, () => true, 500);
      return { nav, info, navigated: navigated?.params ?? null, timedOut: false };
    }
    await sleep(100);
  }
  return { nav, info, navigated: null, timedOut: true };
}

function logGuardedEvents(client, sessionId, from, lines, phase) {
  const prefix = phase ? `guarded ${phase}` : 'guarded';
  for (const e of client.events.slice(from)) {
    if (e.sessionId !== sessionId) continue;
    if (e.method === 'Runtime.executionContextCreated') {
      lines.push(`${prefix} received ${e.method} ${JSON.stringify(summarizeContext(e.params?.context))}`);
    } else if (e.method === 'Runtime.executionContextDestroyed') {
      lines.push(`${prefix} received ${e.method} executionContextId=${e.params?.executionContextId}`);
    } else if (e.method === 'Runtime.executionContextsCleared') {
      lines.push(`${prefix} received ${e.method}`);
    } else if (e.method === 'Target.detachedFromTarget') {
      lines.push(`${prefix} received ${e.method} ${JSON.stringify(e.params)}`);
    } else if (e.method === 'Page.frameNavigated') {
      lines.push(`${prefix} received ${e.method} frame=${JSON.stringify(summarizeFrame(e.params?.frame))}`);
    } else if (e.method === 'Page.frameStartedNavigating') {
      lines.push(`${prefix} received ${e.method} ${JSON.stringify(e.params)}`);
    } else if (e.method === 'Target.targetInfoChanged') {
      lines.push(`${prefix} received ${e.method} info=${JSON.stringify({ targetId: e.params?.targetInfo?.targetId, url: e.params?.targetInfo?.url, title: e.params?.targetInfo?.title })}`);
    }
  }
}

function heldNavMethodsFrom(client, sessionId, from) {
  return client.events.slice(from).filter((e) => e.sessionId === sessionId && HOLD_METHODS.includes(e.method));
}

async function probeGuarded(client, sessionId, shown, lines, phase) {
  const tag = phase || 'guarded';
  const reads = [];
  const contexts = [];
  if (shown && typeof shown.id === 'number') contexts.push(shown);
  for (const e of client.events) {
    if (e.sessionId !== sessionId || e.method !== 'Runtime.executionContextCreated') continue;
    const c = e.params?.context;
    if (c && typeof c.id === 'number' && !contexts.some((x) => x.id === c.id)) contexts.push(c);
  }
  if (contexts.length === 0) {
    lines.push(`${tag} pinned Runtime.evaluate status=skipped reason=no-context-received`);
    reads.push({ method: 'Runtime.evaluate', pinned: true, status: 'skipped', reason: 'no-context-received' });
  }
  for (const ctx of contexts) {
    const pin = classifyReply(await outcome(client.send('Runtime.evaluate', {
      expression: `(document.documentElement && document.documentElement.outerHTML || '').slice(0, 120)`,
      contextId: ctx.id,
      returnByValue: true,
    }, sessionId)));
    if (pin.status === 'answered') {
      const value = pin.result?.result?.value ?? pin.result;
      const prefix = String(value).slice(0, 120);
      lines.push(`${tag} pinned Runtime.evaluate contextId=${ctx.id} origin=${JSON.stringify(ctx.origin ?? '')} status=answered prefix=${JSON.stringify(prefix)}`);
      reads.push({ method: 'Runtime.evaluate', pinned: true, contextId: ctx.id, origin: ctx.origin ?? '', status: 'answered', prefix });
    } else {
      lines.push(`${tag} pinned Runtime.evaluate contextId=${ctx.id} origin=${JSON.stringify(ctx.origin ?? '')} status=${pin.status} error=${JSON.stringify(pin.error)}`);
      reads.push({ method: 'Runtime.evaluate', pinned: true, contextId: ctx.id, origin: ctx.origin ?? '', status: pin.status, error: pin.error });
    }
  }
  const unpinned = classifyReply(await outcome(client.send('Runtime.evaluate', {
    expression: `(document.documentElement && document.documentElement.outerHTML || '').slice(0, 120)`,
    returnByValue: true,
  }, sessionId)));
  if (unpinned.status === 'answered') {
    const value = unpinned.result?.result?.value ?? unpinned.result;
    const prefix = String(value).slice(0, 120);
    lines.push(`${tag} unpinned Runtime.evaluate status=answered prefix=${JSON.stringify(prefix)}`);
    reads.push({ method: 'Runtime.evaluate', pinned: false, status: 'answered', prefix });
  } else {
    lines.push(`${tag} unpinned Runtime.evaluate status=${unpinned.status} error=${JSON.stringify(unpinned.error)}`);
    reads.push({ method: 'Runtime.evaluate', pinned: false, status: unpinned.status, error: unpinned.error });
  }
  const doc = classifyReply(await outcome(client.send('DOM.getDocument', {}, sessionId)));
  if (doc.status === 'answered') {
    lines.push(`${tag} DOM.getDocument status=answered backendNodeId=${doc.result?.root?.backendNodeId ?? null} nodeName=${JSON.stringify(doc.result?.root?.nodeName ?? null)}`);
    reads.push({ method: 'DOM.getDocument', status: 'answered', backendNodeId: doc.result?.root?.backendNodeId ?? null });
  } else {
    lines.push(`${tag} DOM.getDocument status=${doc.status} error=${JSON.stringify(doc.error)}`);
    reads.push({ method: 'DOM.getDocument', status: doc.status, error: doc.error });
  }
  const shot = classifyReply(await outcome(client.send('Page.captureScreenshot', { format: 'png' }, sessionId)));
  if (shot.status === 'answered') {
    lines.push(`${tag} Page.captureScreenshot status=answered bytes=${(shot.result?.data || '').length}`);
    reads.push({ method: 'Page.captureScreenshot', status: 'answered', bytes: (shot.result?.data || '').length });
  } else {
    lines.push(`${tag} Page.captureScreenshot status=${shot.status} error=${JSON.stringify(shot.error)}`);
    reads.push({ method: 'Page.captureScreenshot', status: shot.status, error: shot.error });
  }
  const ax = classifyReply(await outcome(client.send('Accessibility.getFullAXTree', {}, sessionId)));
  if (ax.status === 'answered') {
    lines.push(`${tag} Accessibility.getFullAXTree status=answered nodes=${(ax.result?.nodes || []).length}`);
    reads.push({ method: 'Accessibility.getFullAXTree', status: 'answered', nodes: (ax.result?.nodes || []).length });
  } else {
    lines.push(`${tag} Accessibility.getFullAXTree status=${ax.status} error=${JSON.stringify(ax.error)}`);
    reads.push({ method: 'Accessibility.getFullAXTree', status: ax.status, error: ax.error });
  }
  return reads;
}

async function gateControl(gateURL, body) {
  const r = await fetch(gateURL, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  });
  const text = await r.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    json = null;
  }
  return { ok: r.ok, status: r.status, body: json, text };
}

async function measureLaggingWrapperForm(getLink, opts, lines) {
  const form = opts.form;
  const marker = opts.marker || 'DIAGSHAPE_MARKER';
  const settleMs = Math.min(Number(opts.settleMs) || 10000, 10000);
  const holdMs = Math.max(Number(opts.lagHoldMs) || 100, 0);
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 20000;
  let client = null;
  let vaultId = null;
  let ordinaryId = null;
  let context = null;
  let armedSession = null;
  try {
    lines.push(`lag=1 gate=${JSON.stringify(opts.gate || '')} holdMs=${holdMs} holdMethods=${JSON.stringify(HOLD_METHODS)}`);
    await reader.send('Target.setDiscoverTargets', { discover: true });
    ({ browserContextId: context } = await reader.send('Target.createBrowserContext', {}));

    ({ targetId: vaultId } = await reader.send('Target.createTarget', { url: 'about:blank', browserContextId: context, background: true }));
    const { sessionId: vaultSession } = await reader.send('Target.attachToTarget', { targetId: vaultId, flatten: true });
    await reader.send('Page.enable', {}, vaultSession);
    await reader.send('Runtime.enable', {}, vaultSession);
    lines.push(`tab1 vault maker: observer-only targetId=${vaultId}`);

    const vaultNav = await navigateObserver(reader, vaultSession, vaultId, opts.vaultDoc, settleMs);
    lines.push(`tab1 vault seed-nav target=${JSON.stringify(vaultNav.info)} timedOut=${Boolean(vaultNav.timedOut)} nav=${JSON.stringify(vaultNav.nav.ok ? vaultNav.nav.result : vaultNav.nav.error)}`);

    let wrapper;
    try {
      if (form === 'blob') {
        wrapper = await makeBlobURL(reader, vaultSession, marker);
        lines.push(`tab1 blob url=${JSON.stringify(wrapper)}`);
      } else if (form === 'filesystem') {
        wrapper = await Promise.race([
          makeFilesystemURL(reader, vaultSession, marker),
          sleep(settleMs).then(() => { throw new Error(`filesystem setup exceeded ${settleMs}ms`); }),
        ]);
        lines.push(`tab1 filesystem url=${JSON.stringify(wrapper)}`);
      } else if (form === 'sandbox-blob') {
        wrapper = await Promise.race([
          makeSandboxedNullBlobURL(reader, vaultSession, marker),
          sleep(settleMs).then(() => { throw new Error(`sandboxed null blob setup exceeded ${settleMs}ms`); }),
        ]);
        lines.push(`tab1 sandbox-blob url=${JSON.stringify(wrapper)}`);
      } else {
        lines.push(`unknown form ${form}`);
        return { form, lines, failed: 'unknown-form' };
      }
    } catch (error) {
      const reason = String(error?.message ?? error);
      lines.push(`tab1 ${form} create failed: ${reason}`);
      if (form === 'sandbox-blob') {
        lines.push(`sandbox-blob residual: could not create null-origin blob via sandboxed srcdoc iframe: ${reason}`);
      }
      return { form, lines, failed: `${form}-create`, error: reason, residual: form === 'sandbox-blob' };
    }

    const ordinarySeed = opts.ordinaryDoc || opts.ordinary || 'about:blank';
    ({ targetId: ordinaryId } = await reader.send('Target.createTarget', { url: ordinarySeed, browserContextId: context, background: true }));
    const { sessionId: ordinaryObserver } = await reader.send('Target.attachToTarget', { targetId: ordinaryId, flatten: true });
    await reader.send('Page.enable', {}, ordinaryObserver);
    await reader.send('Runtime.enable', {}, ordinaryObserver);
    await until(async () => {
      const info = await pollTargetInfo(reader, ordinaryId, () => true, 500);
      return info && info.url && info.url !== 'about:blank';
    }, 'ordinary tab seed', settleMs).catch(() => null);
    lines.push(`tab2 ordinary existing-session targetId=${ordinaryId} seed=${JSON.stringify(ordinarySeed)}`);

    client = await CdpClient.open(getLink(), opts.browser);
    client.callTimeout = READ_TIMEOUT_MS;
    const guardedAttach = await outcome(client.send('Target.attachToTarget', { targetId: ordinaryId, flatten: true }));
    if (!guardedAttach.ok) {
      lines.push(`tab2 guarded attach before nav refused_or_error=${JSON.stringify(guardedAttach.error)}`);
      return { form, lines, failed: 'guarded-attach', error: guardedAttach.error };
    }
    const guardedSession = guardedAttach.result.sessionId;
    await outcome(client.send('Page.enable', {}, guardedSession));
    const enableBefore = await outcome(client.send('Runtime.enable', {}, guardedSession));
    lines.push(`tab2 guarded attached+Runtime.enable before wrapper nav enable=${JSON.stringify(enableBefore.ok ? { ok: true } : enableBefore.error)} session=${guardedSession}`);

    if (!opts.gate) {
      lines.push('lag gate URL missing');
      return { form, lines, failed: 'gate-missing' };
    }
    const arm = await gateControl(opts.gate, {
      action: 'arm',
      targetId: ordinaryId,
      sessionId: guardedSession,
      ms: holdMs,
      side: 'guarded',
      holdMethods: HOLD_METHODS,
      protectedOrigins: opts.protectedOrigins || [opts.vault].filter(Boolean),
      ordinaryOrigin: opts.ordinaryOrigin || opts.ordinary || '',
    });
    lines.push(`lag gate arm status=${arm.status} body=${JSON.stringify(arm.body ?? arm.text)}`);
    if (!arm.ok || !arm.body?.gate) {
      lines.push('lag gate arm failed');
      return { form, lines, failed: 'gate-arm', arm };
    }
    armedSession = guardedSession;

    const beforeGuardedEvents = client.events.length;
    const settled = await navigateObserver(reader, ordinaryObserver, ordinaryId, wrapper, settleMs);
    lines.push(`tab2 Page.navigate under lag nav=${JSON.stringify(settled.nav.ok ? settled.nav.result : settled.nav.error)} timedOut=${Boolean(settled.timedOut)} targetUrl=${JSON.stringify(settled.info?.url ?? '')}`);

    const landed = settled.nav.ok && !settled.timedOut && settled.info?.url && (
      settled.info.url === wrapper
      || settled.info.url.startsWith('blob:')
      || settled.info.url.startsWith('filesystem:')
    );
    if (!landed) {
      const reason = `Page.navigate did not land on wrapper under lag; targetUrl=${JSON.stringify(settled.info?.url ?? '')} wrapper=${JSON.stringify(wrapper)} timedOut=${Boolean(settled.timedOut)} nav=${JSON.stringify(settled.nav.ok ? settled.nav.result : settled.nav.error)}`;
      lines.push(`tab2 ${reason}`);
      if (form === 'sandbox-blob') {
        lines.push(`sandbox-blob residual: ${reason}`);
      }
      const releasedEarly = await gateControl(opts.gate, { action: 'release', sessionId: armedSession });
      lines.push(`lag gate early-release status=${releasedEarly.status} body=${JSON.stringify(releasedEarly.body ?? releasedEarly.text)}`);
      armedSession = null;
      return {
        form,
        lines,
        failed: `${form}-nav`,
        residual: form === 'sandbox-blob',
        error: reason,
        wrapper,
        navigate: settled,
        release: releasedEarly.body,
      };
    }
    lines.push(`tab2 Page.navigate path worked under lag wrapper=${JSON.stringify(wrapper)}`);

    await sleep(500);
    logGuardedEvents(client, guardedSession, beforeGuardedEvents, lines, 'under-lag');
    const guardedCreatedUnderLag = client.events.slice(beforeGuardedEvents).filter((e) => e.sessionId === guardedSession && e.method === 'Runtime.executionContextCreated').map((e) => e.params.context);
    lines.push(`under-lag guarded executionContextCreated count=${guardedCreatedUnderLag.length}`);
    const shownUnderLag = guardedCreatedUnderLag.find((c) => c?.auxData?.isDefault) ?? guardedCreatedUnderLag[0];
    const underLagHeld = heldNavMethodsFrom(client, guardedSession, beforeGuardedEvents);
    lines.push(`under-lag held-nav-methods-delivered-early count=${underLagHeld.length}`);
    const underLagReads = await probeGuarded(client, guardedSession, shownUnderLag, lines, 'under-lag');

    const afterFrom = client.events.length;
    const released = await gateControl(opts.gate, { action: 'release', sessionId: armedSession });
    lines.push(`lag gate release status=${released.status} body=${JSON.stringify(released.body ?? released.text)}`);
    armedSession = null;

    await sleep(800);
    logGuardedEvents(client, guardedSession, afterFrom, lines, 'after-release');
    const heldAfter = heldNavMethodsFrom(client, guardedSession, afterFrom);
    lines.push(`after-release held-nav-methods-delivered count=${heldAfter.length} methods=${JSON.stringify(heldAfter.map((e) => e.method))}`);
    const guardedCreatedAfter = client.events.slice(afterFrom).filter((e) => e.sessionId === guardedSession && e.method === 'Runtime.executionContextCreated').map((e) => e.params.context);
    lines.push(`after-release guarded executionContextCreated count=${guardedCreatedAfter.length}`);
    const shownAfter = guardedCreatedAfter.find((c) => c?.auxData?.isDefault)
      ?? guardedCreatedUnderLag.find((c) => c?.auxData?.isDefault)
      ?? guardedCreatedAfter[0]
      ?? guardedCreatedUnderLag[0];
    const afterReleaseReads = await probeGuarded(client, guardedSession, shownAfter, lines, 'after-release');

    const delayedEvents = Number(released.body?.delayedEvents ?? 0);
    const firstDelayedAt = Number(released.body?.firstDelayedAt ?? 0);
    const releasedAt = Number(released.body?.releasedAt ?? 0);
    const heldDeliveredAfterRelease = heldAfter.length > 0;
    const gateCounts = delayedEvents > 0 && firstDelayedAt > 0;
    lines.push(`precondition gate delayedEvents=${delayedEvents} firstDelayedAt=${firstDelayedAt} releasedAt=${releasedAt} heldDeliveredAfterRelease=${heldDeliveredAfterRelease}`);

    return {
      form,
      lines,
      wrapper,
      via: 'Page.navigate',
      lag: 1,
      underLagReads,
      afterReleaseReads,
      guardedContextsUnderLag: guardedCreatedUnderLag.map(summarizeContext),
      guardedContextsAfterRelease: guardedCreatedAfter.map(summarizeContext),
      arm: arm.body,
      release: released.body,
      precondition: {
        delayedEvents,
        firstDelayedAt,
        releasedAt,
        heldDeliveredAfterRelease,
        gateCounts,
        underLagHeldEarly: underLagHeld.length,
      },
    };
  } finally {
    if (armedSession && opts.gate) {
      await gateControl(opts.gate, { action: 'release', sessionId: armedSession }).catch(() => null);
    }
    if (ordinaryId) await outcome(reader.send('Target.closeTarget', { targetId: ordinaryId }));
    if (vaultId) await outcome(reader.send('Target.closeTarget', { targetId: vaultId }));
    if (client) await outcome(client.close());
    if (context) await outcome(reader.send('Target.disposeBrowserContext', { browserContextId: context }));
    reader.close();
  }
}

export async function measureDiagShape(getLink, opts) {
  const lines = [];
  const form = opts.form;
  const marker = opts.marker || 'DIAGSHAPE_MARKER';
  lines.push(`form=${form} marker=${marker} lag=${opts.lag ? 1 : 0}`);
  if (form === 'blob' || form === 'filesystem' || form === 'sandbox-blob') {
    return measureLaggingWrapperForm(getLink, opts, lines);
  }
  lines.push(`form=${form} skipped: lagging diagnostic covers blob/filesystem/sandbox-blob only`);
  return { form, lines, skipped: true, reason: 'out-of-scope' };
}
