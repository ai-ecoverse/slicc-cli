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

async function navigateObserver(reader, observer, targetId, url, settleMs) {
  const start = reader.events.length;
  const nav = await outcome(reader.send('Page.navigate', { url }, observer));
  const deadline = Date.now() + settleMs;
  let info = null;
  while (Date.now() < deadline) {
    info = await pollTargetInfo(reader, targetId, () => true, 500);
    const navigated = reader.events.slice(start).find((e) => e.sessionId === observer && e.method === 'Page.frameNavigated' && e.params?.frame?.id === targetId);
    if (navigated || (info && info.url && info.url !== 'about:blank')) {
      await sleep(200);
      info = await pollTargetInfo(reader, targetId, () => true, 500);
      return { nav, info, navigated: navigated?.params ?? null };
    }
    await sleep(100);
  }
  return { nav, info, navigated: null, timedOut: true };
}

export async function measureDiagShape(getLink, opts) {
  const lines = [];
  const form = opts.form;
  const marker = opts.marker || 'DIAGSHAPE_MARKER';
  const settleMs = Math.min(Number(opts.settleMs) || 10000, 10000);
  lines.push(`form=${form} marker=${marker}`);

  if (form === 'view-source-https') {
    lines.push(`form=${form} skipped: fixture server does not offer TLS for vault/ordinary docs`);
    return { form, lines, skipped: true, reason: 'no-tls' };
  }

  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 20000;
  let client = null;
  let targetId = null;
  let context = null;
  try {
    ({ browserContextId: context } = await reader.send('Target.createBrowserContext', {}));
    const seed = opts.seed || 'about:blank';
    ({ targetId } = await reader.send('Target.createTarget', { url: seed, browserContextId: context, background: true }));
    const { sessionId: observer } = await reader.send('Target.attachToTarget', { targetId, flatten: true });
    await reader.send('Page.enable', {}, observer);
    const unguardedStart = reader.events.length;
    await reader.send('Runtime.enable', {}, observer);

    if (seed !== 'about:blank') {
      await until(async () => {
        const href = await reader.send('Runtime.evaluate', { expression: 'location.href', returnByValue: true }, observer);
        return href?.result?.value && href.result.value !== 'about:blank';
      }, 'seed document', settleMs).catch(() => null);
    }

    let wrapper = opts.wrapper;
    if (form === 'blob') {
      const vaultSeed = opts.vaultDoc;
      const toVault = await navigateObserver(reader, observer, targetId, vaultSeed, settleMs);
      lines.push(`blob seed-nav target=${JSON.stringify(toVault.info)} timedOut=${Boolean(toVault.timedOut)}`);
      try {
        wrapper = await makeBlobURL(reader, observer, marker);
        lines.push(`blob url=${JSON.stringify(wrapper)}`);
      } catch (error) {
        lines.push(`blob create failed: ${String(error?.message ?? error)}`);
        return { form, lines, failed: 'blob-create', error: String(error?.message ?? error) };
      }
    }
    if (form === 'filesystem') {
      const vaultSeed = opts.vaultDoc;
      const toVault = await navigateObserver(reader, observer, targetId, vaultSeed, settleMs);
      lines.push(`filesystem seed-nav target=${JSON.stringify(toVault.info)} timedOut=${Boolean(toVault.timedOut)}`);
      try {
        wrapper = await Promise.race([
          makeFilesystemURL(reader, observer, marker),
          sleep(settleMs).then(() => { throw new Error(`filesystem navigation setup exceeded ${settleMs}ms`); }),
        ]);
        lines.push(`filesystem url=${JSON.stringify(wrapper)}`);
      } catch (error) {
        lines.push(`filesystem unavailable or failed within ${settleMs}ms: ${String(error?.message ?? error)}`);
        return { form, lines, failed: 'filesystem', error: String(error?.message ?? error) };
      }
    }

    client = await CdpClient.open(getLink(), opts.browser);
    client.callTimeout = 20000;
    const guardedAttach = await outcome(client.send('Target.attachToTarget', { targetId, flatten: true }));
    if (!guardedAttach.ok) {
      lines.push(`guarded attach before nav refused_or_error=${JSON.stringify(guardedAttach.error)}`);
      return { form, lines, failed: 'guarded-attach', error: guardedAttach.error };
    }
    const guardedSession = guardedAttach.result.sessionId;
    await outcome(client.send('Page.enable', {}, guardedSession));
    lines.push(`existing-session: observer+guarded attached before wrapper nav targetId=${targetId}`);

    const beforeNavEvents = reader.events.length;
    const beforeGuardedEvents = client.events.length;
    const settled = await navigateObserver(reader, observer, targetId, wrapper, settleMs);
    if (settled.timedOut) {
      lines.push(`wrapper nav timed out after ${settleMs}ms wrapper=${JSON.stringify(wrapper)} lastTarget=${JSON.stringify(settled.info)}`);
      if (form === 'filesystem') {
        return { form, lines, failed: 'filesystem-nav-timeout', info: settled.info };
      }
    } else {
      lines.push(`wrapper nav settled wrapper=${JSON.stringify(wrapper)} targetUrl=${JSON.stringify(settled.info?.url ?? '')}`);
    }

    await sleep(300);
    const unguarded = await dumpShape(reader, observer, targetId, 'unguarded', lines, unguardedStart);

    const guardedEnableStart = client.events.length;
    const enable = await outcome(client.send('Runtime.enable', {}, guardedSession));
    lines.push(`guarded Runtime.enable ${JSON.stringify(enable.ok ? { ok: true } : enable.error)}`);
    await sleep(500);
    for (const e of client.events.slice(beforeGuardedEvents)) {
      if (e.sessionId !== guardedSession) continue;
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
    const guardedCreated = client.events.slice(guardedEnableStart).filter((e) => e.sessionId === guardedSession && e.method === 'Runtime.executionContextCreated').map((e) => e.params.context);
    const shown = guardedCreated.find((c) => c?.auxData?.isDefault) ?? guardedCreated[0];
    if (shown && typeof shown.id === 'number') {
      const pin = await outcome(client.send('Runtime.evaluate', {
        expression: `(document.documentElement && document.documentElement.outerHTML || '').slice(0, 120)`,
        contextId: shown.id,
        returnByValue: true,
      }, guardedSession));
      if (pin.ok) {
        const value = pin.result?.result?.value ?? pin.result;
        lines.push(`guarded pinned Runtime.evaluate contextId=${shown.id} answered prefix=${JSON.stringify(String(value).slice(0, 120))}`);
      } else {
        lines.push(`guarded pinned Runtime.evaluate contextId=${shown.id} refused_or_error=${JSON.stringify(pin.error)}`);
      }
    } else {
      lines.push('guarded pinned Runtime.evaluate skipped: no shown context received after Runtime.enable');
    }

    lines.push(`nav-observer-events-after=${reader.events.length - beforeNavEvents}`);
    return {
      form,
      lines,
      wrapper,
      unguarded,
      guardedContexts: guardedCreated.map(summarizeContext),
    };
  } finally {
    if (targetId) await outcome(reader.send('Target.closeTarget', { targetId }));
    if (client) await outcome(client.close());
    if (context) await outcome(reader.send('Target.disposeBrowserContext', { browserContextId: context }));
    reader.close();
  }
}
