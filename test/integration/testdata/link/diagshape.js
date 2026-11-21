import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { outcome, until } from './persistent.js';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

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

function summarizeTree(node) {
  if (!node) return null;
  return {
    frame: summarizeFrame(node.frame),
    childFrames: (node.childFrames ?? []).map(summarizeTree),
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

async function dumpShape(client, sessionId, targetId, label, lines, eventStart) {
  const infoReply = await outcome(client.send('Target.getTargetInfo', { targetId }));
  const info = infoReply.ok ? infoReply.result.targetInfo : { error: infoReply.error };
  lines.push(`${label} Target.getTargetInfo url=${JSON.stringify(info.url ?? '')} type=${JSON.stringify(info.type ?? '')} title=${JSON.stringify(info.title ?? '')} raw=${JSON.stringify(info)}`);

  const treeReply = await outcome(client.send('Page.getFrameTree', {}, sessionId));
  const tree = treeReply.ok ? summarizeTree(treeReply.result.frameTree) : { error: treeReply.error };
  lines.push(`${label} Page.getFrameTree ${JSON.stringify(tree)}`);

  const histReply = await outcome(client.send('Page.getNavigationHistory', {}, sessionId));
  if (histReply.ok) {
    const entries = (histReply.result.entries ?? []).map((e) => e.url ?? '');
    lines.push(`${label} Page.getNavigationHistory currentIndex=${histReply.result.currentIndex} entries=${JSON.stringify(entries)}`);
  } else {
    lines.push(`${label} Page.getNavigationHistory error=${JSON.stringify(histReply.error)}`);
  }

  const runtimeEvents = client.events.slice(eventStart).filter((e) => e.sessionId === sessionId && (
    e.method === 'Runtime.executionContextCreated'
    || e.method === 'Runtime.executionContextDestroyed'
    || e.method === 'Runtime.executionContextsCleared'
  ));
  for (const e of runtimeEvents) {
    if (e.method === 'Runtime.executionContextCreated') {
      lines.push(`${label} ${e.method} ${JSON.stringify(summarizeContext(e.params?.context))}`);
    } else if (e.method === 'Runtime.executionContextDestroyed') {
      lines.push(`${label} ${e.method} executionContextId=${e.params?.executionContextId} uniqueId=${JSON.stringify(e.params?.executionContextUniqueId ?? '')}`);
    } else {
      lines.push(`${label} ${e.method}`);
    }
  }

  const created = runtimeEvents.filter((e) => e.method === 'Runtime.executionContextCreated').map((e) => e.params.context);
  const main = created.find((c) => c?.auxData?.isDefault && c?.auxData?.frameId === targetId) ?? created.find((c) => c?.auxData?.isDefault) ?? created[created.length - 1];
  if (!main) {
    lines.push(`${label} Runtime.evaluate skipped: no executionContextCreated seen`);
    return { info, tree, main: null, evaluate: null };
  }
  const evalReply = await outcome(client.send('Runtime.evaluate', {
    expression: `(document.documentElement && document.documentElement.outerHTML || document.body && document.body.innerText || '').slice(0, 120)`,
    contextId: main.id,
    returnByValue: true,
  }, sessionId));
  if (evalReply.ok) {
    const value = evalReply.result?.result?.value ?? evalReply.result?.exceptionDetails ?? evalReply.result;
    lines.push(`${label} Runtime.evaluate contextId=${main.id} prefix=${JSON.stringify(String(value).slice(0, 120))}`);
    return { info, tree, main: summarizeContext(main), evaluate: { ok: true, prefix: String(value).slice(0, 120) } };
  }
  lines.push(`${label} Runtime.evaluate contextId=${main.id} refused_or_error=${JSON.stringify(evalReply.error)}`);
  return { info, tree, main: summarizeContext(main), evaluate: { ok: false, error: evalReply.error } };
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

function logGuardedEvents(client, sessionId, from, lines) {
  for (const e of client.events.slice(from)) {
    if (e.sessionId !== sessionId) continue;
    if (e.method === 'Runtime.executionContextCreated') {
      lines.push(`guarded received ${e.method} ${JSON.stringify(summarizeContext(e.params?.context))}`);
    } else if (e.method === 'Runtime.executionContextDestroyed') {
      lines.push(`guarded received ${e.method} executionContextId=${e.params?.executionContextId}`);
    } else if (e.method === 'Runtime.executionContextsCleared') {
      lines.push(`guarded received ${e.method}`);
    } else if (e.method === 'Target.detachedFromTarget') {
      lines.push(`guarded received ${e.method} ${JSON.stringify(e.params)}`);
    }
  }
}

async function probeGuarded(client, sessionId, shown, lines) {
  if (shown && typeof shown.id === 'number') {
    const pin = await outcome(client.send('Runtime.evaluate', {
      expression: `(document.documentElement && document.documentElement.outerHTML || '').slice(0, 120)`,
      contextId: shown.id,
      returnByValue: true,
    }, sessionId));
    if (pin.ok) {
      const value = pin.result?.result?.value ?? pin.result;
      lines.push(`guarded pinned Runtime.evaluate contextId=${shown.id} answered prefix=${JSON.stringify(String(value).slice(0, 120))}`);
    } else {
      lines.push(`guarded pinned Runtime.evaluate contextId=${shown.id} refused_or_error=${JSON.stringify(pin.error)}`);
    }
  } else {
    lines.push('guarded pinned Runtime.evaluate skipped: no shown context received after Runtime.enable');
  }
  const unpinned = await outcome(client.send('Runtime.evaluate', {
    expression: `(document.documentElement && document.documentElement.outerHTML || '').slice(0, 120)`,
    returnByValue: true,
  }, sessionId));
  if (unpinned.ok) {
    const value = unpinned.result?.result?.value ?? unpinned.result;
    lines.push(`guarded unpinned Runtime.evaluate answered prefix=${JSON.stringify(String(value).slice(0, 120))}`);
  } else {
    lines.push(`guarded unpinned Runtime.evaluate refused_or_error=${JSON.stringify(unpinned.error)}`);
  }
  const doc = await outcome(client.send('DOM.getDocument', {}, sessionId));
  lines.push(`guarded DOM.getDocument ${doc.ok ? JSON.stringify({ ok: true, backendNodeId: doc.result?.root?.backendNodeId ?? null, nodeName: doc.result?.root?.nodeName ?? null }) : JSON.stringify({ ok: false, error: doc.error })}`);
  const shot = await outcome(client.send('Page.captureScreenshot', { format: 'png' }, sessionId));
  if (shot.ok) {
    lines.push(`guarded Page.captureScreenshot answered bytes=${(shot.result?.data || '').length}`);
  } else {
    lines.push(`guarded Page.captureScreenshot refused_or_error=${JSON.stringify(shot.error)}`);
  }
}

async function measureViewSourceForm(getLink, opts, lines) {
  const form = opts.form;
  const settleMs = Math.min(Number(opts.settleMs) || 10000, 10000);
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 20000;
  let client = null;
  let targetId = null;
  let context = null;
  try {
    ({ browserContextId: context } = await reader.send('Target.createBrowserContext', {}));
    ({ targetId } = await reader.send('Target.createTarget', { url: 'about:blank', browserContextId: context, background: true }));
    const { sessionId: observer } = await reader.send('Target.attachToTarget', { targetId, flatten: true });
    await reader.send('Page.enable', {}, observer);
    const unguardedStart = reader.events.length;
    await reader.send('Runtime.enable', {}, observer);

    client = await CdpClient.open(getLink(), opts.browser);
    client.callTimeout = 20000;
    const guardedAttach = await outcome(client.send('Target.attachToTarget', { targetId, flatten: true }));
    if (!guardedAttach.ok) {
      lines.push(`guarded attach before nav refused_or_error=${JSON.stringify(guardedAttach.error)}`);
      return { form, lines, failed: 'guarded-attach', error: guardedAttach.error };
    }
    const guardedSession = guardedAttach.result.sessionId;
    await outcome(client.send('Page.enable', {}, guardedSession));
    const enableBefore = await outcome(client.send('Runtime.enable', {}, guardedSession));
    lines.push(`existing-session: observer+guarded attached+Runtime.enable before wrapper nav targetId=${targetId} enable=${JSON.stringify(enableBefore.ok ? { ok: true } : enableBefore.error)}`);

    const beforeNavEvents = reader.events.length;
    const beforeGuardedEvents = client.events.length;
    const settled = await navigateObserver(reader, observer, targetId, opts.wrapper, settleMs);
    if (settled.timedOut) {
      lines.push(`wrapper nav timed out after ${settleMs}ms wrapper=${JSON.stringify(opts.wrapper)} lastTarget=${JSON.stringify(settled.info)}`);
    } else {
      lines.push(`wrapper nav settled wrapper=${JSON.stringify(opts.wrapper)} targetUrl=${JSON.stringify(settled.info?.url ?? '')}`);
    }

    await sleep(300);
    const unguarded = await dumpShape(reader, observer, targetId, 'unguarded', lines, unguardedStart);
    logGuardedEvents(client, guardedSession, beforeGuardedEvents, lines);
    const guardedCreated = client.events.slice(beforeGuardedEvents).filter((e) => e.sessionId === guardedSession && e.method === 'Runtime.executionContextCreated').map((e) => e.params.context);
    const shown = guardedCreated.find((c) => c?.auxData?.isDefault) ?? guardedCreated[0];
    await probeGuarded(client, guardedSession, shown, lines);
    lines.push(`nav-observer-events-after=${reader.events.length - beforeNavEvents}`);
    return { form, lines, wrapper: opts.wrapper, unguarded, guardedContexts: guardedCreated.map(summarizeContext) };
  } finally {
    if (targetId) await outcome(reader.send('Target.closeTarget', { targetId }));
    if (client) await outcome(client.close());
    if (context) await outcome(reader.send('Target.disposeBrowserContext', { browserContextId: context }));
    reader.close();
  }
}

async function openWrapperFromVault(reader, vaultSession, vaultTargetId, wrapper, settleMs, lines) {
  const before = reader.events.length;
  const opened = await outcome(reader.send('Runtime.evaluate', {
    expression: `Boolean(window.open(${JSON.stringify(wrapper)}, '_blank'))`,
    returnByValue: true,
    userGesture: true,
  }, vaultSession));
  lines.push(`window.open from vault tab result=${JSON.stringify(opened.ok ? opened.result?.result?.value ?? opened.result : opened.error)}`);
  const deadline = Date.now() + settleMs;
  while (Date.now() < deadline) {
    const created = reader.events.slice(before).find((e) => e.method === 'Target.targetCreated' && e.params?.targetInfo?.openerId === vaultTargetId);
    const changed = reader.events.slice(before).find((e) => e.method === 'Target.targetInfoChanged' && e.params?.targetInfo?.openerId === vaultTargetId);
    const info = created?.params?.targetInfo || changed?.params?.targetInfo;
    if (info?.targetId) {
      const settled = await pollTargetInfo(reader, info.targetId, (t) => t.url && t.url !== 'about:blank', settleMs);
      return { targetId: info.targetId, info: settled || info, via: 'window.open' };
    }
    const listed = await outcome(reader.send('Target.getTargets'));
    if (listed.ok) {
      const hit = (listed.result.targetInfos || []).find((t) => t.openerId === vaultTargetId && t.targetId !== vaultTargetId);
      if (hit) {
        const settled = await pollTargetInfo(reader, hit.targetId, (t) => t.url && t.url !== 'about:blank', Math.min(2000, settleMs));
        return { targetId: hit.targetId, info: settled || hit, via: 'window.open' };
      }
    }
    await sleep(100);
  }
  return { targetId: null, info: null, via: 'window.open', timedOut: true };
}

async function measureWrapperForm(getLink, opts, lines) {
  const form = opts.form;
  const marker = opts.marker || 'DIAGSHAPE_MARKER';
  const settleMs = Math.min(Number(opts.settleMs) || 10000, 10000);
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 20000;
  let client = null;
  let vaultId = null;
  let ordinaryId = null;
  let openedId = null;
  let context = null;
  try {
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
      } else {
        wrapper = await Promise.race([
          makeFilesystemURL(reader, vaultSession, marker),
          sleep(settleMs).then(() => { throw new Error(`filesystem setup exceeded ${settleMs}ms`); }),
        ]);
        lines.push(`tab1 filesystem url=${JSON.stringify(wrapper)}`);
      }
    } catch (error) {
      lines.push(`tab1 ${form} create failed: ${String(error?.message ?? error)}`);
      return { form, lines, failed: `${form}-create`, error: String(error?.message ?? error) };
    }

    const ordinarySeed = opts.ordinaryDoc || opts.ordinary || 'about:blank';
    ({ targetId: ordinaryId } = await reader.send('Target.createTarget', { url: ordinarySeed, browserContextId: context, background: true }));
    const { sessionId: ordinaryObserver } = await reader.send('Target.attachToTarget', { targetId: ordinaryId, flatten: true });
    await reader.send('Page.enable', {}, ordinaryObserver);
    const unguardedStart = reader.events.length;
    await reader.send('Runtime.enable', {}, ordinaryObserver);
    await until(async () => {
      const info = await pollTargetInfo(reader, ordinaryId, () => true, 500);
      return info && info.url && info.url !== 'about:blank';
    }, 'ordinary tab seed', settleMs).catch(() => null);
    lines.push(`tab2 ordinary existing-session targetId=${ordinaryId} seed=${JSON.stringify(ordinarySeed)}`);

    client = await CdpClient.open(getLink(), opts.browser);
    client.callTimeout = 20000;
    const guardedAttach = await outcome(client.send('Target.attachToTarget', { targetId: ordinaryId, flatten: true }));
    if (!guardedAttach.ok) {
      lines.push(`tab2 guarded attach before nav refused_or_error=${JSON.stringify(guardedAttach.error)}`);
      return { form, lines, failed: 'guarded-attach', error: guardedAttach.error };
    }
    const guardedSession = guardedAttach.result.sessionId;
    await outcome(client.send('Page.enable', {}, guardedSession));
    const enableBefore = await outcome(client.send('Runtime.enable', {}, guardedSession));
    lines.push(`tab2 guarded attached+Runtime.enable before wrapper nav enable=${JSON.stringify(enableBefore.ok ? { ok: true } : enableBefore.error)}`);

    const beforeNavEvents = reader.events.length;
    const beforeGuardedEvents = client.events.length;
    let subjectId = ordinaryId;
    let subjectObserver = ordinaryObserver;
    let path = 'Page.navigate';
    const settled = await navigateObserver(reader, ordinaryObserver, ordinaryId, wrapper, settleMs);
    lines.push(`tab2 Page.navigate to wrapper nav=${JSON.stringify(settled.nav.ok ? settled.nav.result : settled.nav.error)} timedOut=${Boolean(settled.timedOut)} targetUrl=${JSON.stringify(settled.info?.url ?? '')}`);

    const navFailed = !settled.nav.ok || settled.timedOut || !(settled.info?.url && (settled.info.url.startsWith('blob:') || settled.info.url.startsWith('filesystem:') || settled.info.url === wrapper));
    if (navFailed) {
      lines.push(`tab2 Page.navigate did not land on wrapper; trying window.open from vault tab with opener`);
      const opened = await openWrapperFromVault(reader, vaultSession, vaultId, wrapper, settleMs, lines);
      if (!opened.targetId) {
        lines.push(`window.open from vault also failed timedOut=${Boolean(opened.timedOut)}`);
        return { form, lines, failed: `${form}-nav`, wrapper, navigate: settled, opened };
      }
      openedId = opened.targetId;
      subjectId = opened.targetId;
      path = 'window.open';
      const attachOpened = await outcome(reader.send('Target.attachToTarget', { targetId: subjectId, flatten: true }));
      if (!attachOpened.ok) {
        lines.push(`observer attach to window.open tab refused_or_error=${JSON.stringify(attachOpened.error)}`);
        return { form, lines, failed: 'open-attach', error: attachOpened.error, wrapper, via: path };
      }
      subjectObserver = attachOpened.result.sessionId;
      await reader.send('Page.enable', {}, subjectObserver);
      await reader.send('Runtime.enable', {}, subjectObserver);
      lines.push(`window.open path worked targetId=${subjectId} info=${JSON.stringify(opened.info)}`);
    } else {
      lines.push(`tab2 Page.navigate path worked wrapper=${JSON.stringify(wrapper)}`);
    }

    await sleep(300);
    const shapeStart = path === 'window.open' ? reader.events.length : unguardedStart;
    if (path === 'window.open') {
      await sleep(100);
    }
    const unguarded = await dumpShape(reader, subjectObserver, subjectId, 'unguarded', lines, path === 'Page.navigate' ? unguardedStart : shapeStart);

    if (path === 'Page.navigate') {
      logGuardedEvents(client, guardedSession, beforeGuardedEvents, lines);
      const guardedCreated = client.events.slice(beforeGuardedEvents).filter((e) => e.sessionId === guardedSession && e.method === 'Runtime.executionContextCreated').map((e) => e.params.context);
      const shown = guardedCreated.find((c) => c?.auxData?.isDefault) ?? guardedCreated[0];
      await probeGuarded(client, guardedSession, shown, lines);
      lines.push(`nav-observer-events-after=${reader.events.length - beforeNavEvents} via=${path}`);
      return { form, lines, wrapper, via: path, unguarded, guardedContexts: guardedCreated.map(summarizeContext) };
    }

    lines.push(`guarded existing-session path was Page.navigate; window.open created a new tab so guarded attach on tab2 does not cover it — reporting unguarded open-tab shape only plus a fresh guarded attach attempt`);
    const lateAttach = await outcome(client.send('Target.attachToTarget', { targetId: subjectId, flatten: true }));
    lines.push(`guarded attach to window.open tab ${lateAttach.ok ? 'ok session=' + lateAttach.result.sessionId : 'refused_or_error=' + JSON.stringify(lateAttach.error)}`);
    if (lateAttach.ok) {
      const lateSession = lateAttach.result.sessionId;
      const lateFrom = client.events.length;
      await outcome(client.send('Page.enable', {}, lateSession));
      await outcome(client.send('Runtime.enable', {}, lateSession));
      await sleep(500);
      logGuardedEvents(client, lateSession, lateFrom, lines);
      const guardedCreated = client.events.slice(lateFrom).filter((e) => e.sessionId === lateSession && e.method === 'Runtime.executionContextCreated').map((e) => e.params.context);
      const shown = guardedCreated.find((c) => c?.auxData?.isDefault) ?? guardedCreated[0];
      await probeGuarded(client, lateSession, shown, lines);
      return { form, lines, wrapper, via: path, unguarded, guardedContexts: guardedCreated.map(summarizeContext) };
    }
    return { form, lines, wrapper, via: path, unguarded };
  } finally {
    if (openedId) await outcome(reader.send('Target.closeTarget', { targetId: openedId }));
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
  lines.push(`form=${form} marker=${marker}`);

  if (form === 'view-source-https') {
    lines.push(`form=${form} skipped: fixture server does not offer TLS for vault/ordinary docs`);
    return { form, lines, skipped: true, reason: 'no-tls' };
  }
  if (form === 'blob' || form === 'filesystem') {
    return measureWrapperForm(getLink, opts, lines);
  }
  return measureViewSourceForm(getLink, opts, lines);
}
