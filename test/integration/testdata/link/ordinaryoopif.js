import { CdpClient } from './cdp.js';

const WORKERLESS = [
  { type: 'worker', exclude: true },
  { type: 'shared_worker', exclude: true },
  { type: 'service_worker', exclude: true },
  { type: 'browser', exclude: true },
  { type: 'tab', exclude: true },
  {},
];
const FRAME_CHILDREN = [{ type: 'iframe', exclude: false }, { exclude: true }];

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function outcome(promise) {
  try {
    return { ok: true, result: await promise };
  } catch (error) {
    return { ok: false, error: error.cdp?.message ?? String(error?.message ?? error) };
  }
}

export async function measureOrdinaryOOPIF(getLink, opts) {
  const c = await CdpClient.open(getLink(), opts.browser);
  c.callTimeout = 15000;
  const started = Date.now();
  const log = [];
  const explicit = {};
  const lookups = {};
  const note = (entry) => log.push({ at: Date.now() - started, ...entry });
  let page = '';
  let pageSession = '';
  let privateSession = '';
  const sessions = new Map();
  c.on((e) => {
    if (!e.method.startsWith('Target.')) return;
    const info = e.params?.targetInfo ?? {};
    note({ event: e.method, session: e.sessionId ?? '', child: e.params?.sessionId ?? '', target: info.targetId ?? e.params?.targetId ?? '', type: info.type ?? '', url: info.url ?? '', parentId: info.parentId ?? '', parentFrameId: info.parentFrameId ?? '', waiting: e.params?.waitingForDebugger ?? null });
    if (e.method === 'Target.targetCreated' && info.type === 'iframe' && opts.shape === 'bios' && !explicit[info.targetId]) {
      const asked = Date.now() - started;
      lookups[info.targetId] = outcome(c.send('Target.getTargetInfo', { targetId: info.targetId })).then((o) => ({ asked, at: Date.now() - started, ok: o.ok, url: o.ok ? o.result.targetInfo?.url ?? '' : '', error: o.ok ? '' : o.error }));
      explicit[info.targetId] = outcome(c.send('Target.attachToTarget', { targetId: info.targetId, flatten: true }));
    }
    if (e.method !== 'Target.attachedToTarget') return;
    sessions.set(e.params.sessionId, { target: info.targetId, type: info.type, url: info.url, parent: e.sessionId ?? '' });
    if (info.type !== 'iframe') return;
    if (opts.shape === 'bios' && !explicit[info.targetId]) {
      explicit[info.targetId] = outcome(c.send('Target.attachToTarget', { targetId: info.targetId, flatten: true }));
    }
    if (e.params.waitingForDebugger) void outcome(c.send('Runtime.runIfWaitingForDebugger', {}, e.params.sessionId));
  });
  try {
    await c.send('Target.setDiscoverTargets', { discover: true });
    await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true, filter: WORKERLESS });
    page = (await c.send('Target.createTarget', { url: 'about:blank' })).targetId;
    const attached = await c.waitEvent((e) => e.method === 'Target.attachedToTarget' && !e.sessionId && e.params.targetInfo.targetId === page);
    pageSession = attached.params.sessionId;
    await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true }, pageSession);
    if (opts.pageEnable) await c.send('Page.enable', {}, pageSession);
    await outcome(c.send('Runtime.runIfWaitingForDebugger', {}, pageSession));
    if (opts.shape === 'bios') {
      privateSession = (await c.send('Target.attachToTarget', { targetId: page, flatten: true })).sessionId;
      await c.send('Page.enable', {}, privateSession);
      await c.send('Page.getFrameTree', {}, privateSession);
      await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: false, flatten: true, filter: FRAME_CHILDREN }, privateSession);
    }
    const navigated = await outcome(c.send('Page.navigate', { url: opts.url }, pageSession));
    let child = null;
    for (let waited = 0; waited < opts.waitMs && !child; waited += 100) {
      for (const [id, s] of sessions) {
        if (s.type === 'iframe' && s.parent === pageSession && s.url.startsWith(opts.child)) child = { id, ...s };
      }
      if (!child) await sleep(100);
    }
    const listed = (await c.send('Target.getTargets')).targetInfos.filter((t) => t.type === 'iframe').map((t) => ({ targetId: t.targetId, url: t.url, parentId: t.parentId ?? '', parentFrameId: t.parentFrameId ?? '' }));
    const read = child ? await outcome(c.send('Runtime.evaluate', { expression: "document.querySelector('#inner')?.textContent ?? ''", returnByValue: true }, child.id)) : null;
    const explicitAnswers = {};
    for (const [id, p] of Object.entries(explicit)) {
      const o = await p;
      explicitAnswers[id] = o.ok ? { ok: true } : { ok: false, error: o.error };
    }
    const lookupAnswers = {};
    for (const [id, p] of Object.entries(lookups)) lookupAnswers[id] = await p;
    return {
      lookups: lookupAnswers,
      shape: opts.shape,
      pageEnable: Boolean(opts.pageEnable),
      page,
      pageSession,
      privateSession,
      navigated: navigated.ok ? { ok: true } : { ok: false, error: navigated.error },
      child,
      read: read ? (read.ok ? { ok: true, value: read.result.result?.value ?? '' } : { ok: false, error: read.error }) : null,
      listed,
      explicit: explicitAnswers,
      log,
    };
  } finally {
    await c.close();
  }
}

export async function measureProtectedOOPIF(getLink, opts) {
  const c = await CdpClient.open(getLink(), opts.browser);
  c.callTimeout = 15000;
  try {
    await c.send('Target.setDiscoverTargets', { discover: true });
    await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: false, flatten: true, filter: WORKERLESS });
    await sleep(500);
    const listed = (await c.send('Target.getTargets')).targetInfos.map((t) => t.targetId);
    const rows = {};
    for (const id of opts.frames) {
      const info = await outcome(c.send('Target.getTargetInfo', { targetId: id }));
      const attach = await outcome(c.send('Target.attachToTarget', { targetId: id, flatten: true }));
      rows[id] = { listed: listed.includes(id), info: info.ok ? { ok: true } : { ok: false, error: info.error }, attach: attach.ok ? { ok: true } : { ok: false, error: attach.error } };
    }
    const seen = c.events.filter((e) => opts.frames.includes(e.params?.targetInfo?.targetId) || opts.frames.includes(e.params?.targetId)).map((e) => e.method);
    return { rows, seen };
  } finally {
    await c.close();
  }
}

function hrExplicitState() {
  if (!globalThis.__hrExplicit) globalThis.__hrExplicit = {};
  return globalThis.__hrExplicit;
}

export async function measureHistoryRestoreLockedOrdinaryExplicitOpen(getLink, opts) {
  const prev = hrExplicitState().client;
  if (prev) await prev.close().catch(() => {});
  const c = await CdpClient.open(getLink(), opts.browser);
  c.callTimeout = opts.callTimeoutMs || 60000;
  const started = Date.now();
  const at = () => Date.now() - started;
  const steps = [];
  const noteStep = (name, o) => {
    const row = { name, at: at(), ok: o.ok, error: o.ok ? '' : o.error || '' };
    steps.push(row);
    return row;
  };
  const created = [];
  c.on((e) => {
    if (e.method !== 'Target.targetCreated' && e.method !== 'Target.targetInfoChanged') return;
    const info = e.params?.targetInfo ?? {};
    if (info.type === 'iframe' && typeof info.url === 'string' && info.url.startsWith(opts.framePrefix)) {
      created.push({ at: at(), target: info.targetId ?? '', method: e.method });
    }
  });
  const discover = await outcome(c.send('Target.setDiscoverTargets', { discover: true }));
  noteStep('guarded-setDiscoverTargets', discover);
  if (!discover.ok) {
    await c.close();
    return { steps, elapsedMs: at() };
  }
  const hostAttach = await outcome(c.send('Target.attachToTarget', { targetId: opts.hostTarget, flatten: true }));
  noteStep('guarded-attach-host', hostAttach);
  if (!hostAttach.ok) {
    await c.close();
    return { steps, hostAttachError: hostAttach.error, elapsedMs: at() };
  }
  const pageSession = hostAttach.result?.sessionId ?? '';
  const pageEnable = await outcome(c.send('Page.enable', {}, pageSession));
  noteStep('guarded-page-enable', pageEnable);
  const runtimeEnable = await outcome(c.send('Runtime.enable', {}, pageSession));
  noteStep('guarded-runtime-enable', runtimeEnable);
  const listedOut = await outcome(c.send('Target.getTargets'));
  const beforeIframes = (listedOut.ok ? listedOut.result?.targetInfos ?? [] : [])
    .filter((t) => t.type === 'iframe')
    .map((t) => t.targetId);
  noteStep('guarded-getTargets-before', listedOut.ok ? { ok: true } : listedOut);
  hrExplicitState().client = c;
  hrExplicitState().pageSession = pageSession;
  hrExplicitState().created = created;
  hrExplicitState().started = started;
  hrExplicitState().steps = steps;
  hrExplicitState().opts = opts;
  hrExplicitState().beforeIframes = beforeIframes;
  return { steps, pageSession, hostAttached: true, beforeIframes, elapsedMs: at() };
}

export async function measureHistoryRestoreLockedOrdinaryExplicitLockProbe(opts) {
  const st = hrExplicitState();
  const c = st.client;
  if (!c) return { steps: [{ name: 'guarded-lock-engaged', at: 0, ok: false, error: 'no open guarded client' }], elapsedMs: 0 };
  const started = st.started || Date.now();
  const at = () => Date.now() - started;
  const steps = st.steps || [];
  const pageSession = st.pageSession;
  await sleep(opts.lockSettleMs ?? st.opts?.lockSettleMs ?? 800);
  const ev = await outcome(c.send('Runtime.evaluate', { expression: 'location.href', returnByValue: true }, pageSession));
  const err = ev.ok ? '' : (ev.error || '');
  const historyRefused = !ev.ok && typeof err === 'string' && err.includes("history may restore a protected document");
  steps.push({
    name: 'guarded-lock-engaged',
    at: at(),
    ok: historyRefused,
    error: historyRefused ? err : (ev.ok ? 'Runtime.evaluate unexpectedly allowed on locked ordinary host' : err),
  });
  st.steps = steps;
  return { steps, lockEngaged: historyRefused, error: err, elapsedMs: at() };
}

export async function measureHistorySetupWaitConfirmSettled(opts) {
  const st = hrExplicitState();
  const c = st.client;
  if (!c) return { steps: [{ name: 'guarded-wait-confirm-settled', at: 0, ok: false, error: 'no open guarded client' }], ok: false, error: 'no open guarded client', elapsedMs: 0 };
  const started = st.started || Date.now();
  const at = () => Date.now() - started;
  const steps = st.steps || [];
  const pageSession = st.pageSession;
  const hist = await outcome(c.send('Page.getNavigationHistory', {}, pageSession));
  const row = { name: 'guarded-wait-confirm-settled', at: at(), ok: hist.ok, error: hist.ok ? '' : hist.error || '' };
  steps.push(row);
  st.steps = steps;
  return { steps, ok: hist.ok, error: hist.ok ? '' : hist.error || '', elapsedMs: at() };
}

export async function measureHistorySetupLockedOrdinarySetup(opts) {
  const st = hrExplicitState();
  const c = st.client;
  if (!c) return { steps: [{ name: 'guarded-setup', at: 0, ok: false, error: 'no open guarded client' }], elapsedMs: 0 };
  const started = st.started || Date.now();
  const at = () => Date.now() - started;
  const steps = st.steps || [];
  const pageSession = st.pageSession;
  const protectedPrefix = opts.protectedPrefix || '';
  const noteStep = (name, o) => {
    const row = { name, at: at(), ok: o.ok, error: o.ok ? '' : o.error || '' };
    steps.push(row);
    return row;
  };
  const bypass = await outcome(c.send('Network.setBypassServiceWorker', { bypass: true }, pageSession));
  noteStep('guarded-setBypassServiceWorker', bypass);
  const auto = await outcome(c.send('Target.setAutoAttach', {
    autoAttach: true,
    flatten: true,
    waitForDebuggerOnStart: false,
    filter: FRAME_CHILDREN,
  }, pageSession));
  noteStep('guarded-setAutoAttach', auto);
  const evaluate = await outcome(c.send('Runtime.evaluate', { expression: 'location.href', returnByValue: true }, pageSession));
  const evaluateHistory = !evaluate.ok && typeof evaluate.error === 'string' && evaluate.error.includes('history may restore a protected document');
  steps.push({
    name: 'guarded-evaluate-history-refused',
    at: at(),
    ok: evaluateHistory,
    error: evaluateHistory ? (evaluate.error || '') : (evaluate.ok ? 'Runtime.evaluate unexpectedly allowed' : evaluate.error || ''),
  });
  const getDocument = await outcome(c.send('DOM.getDocument', { depth: -1, pierce: true }, pageSession));
  const getDocumentOk = getDocument.ok === true;
  const replyText = JSON.stringify(getDocument.result ?? getDocument.value ?? getDocument);
  const replyClean =
    !protectedPrefix ||
    (!replyText.includes(`${protectedPrefix}/`) &&
      !replyText.includes('/seven-host') &&
      !/"documentURL":"http:\/\/seven\.protected\.test[^"]+\//.test(replyText));
  steps.push({
    name: 'guarded-getDocument-admitted',
    at: at(),
    ok: getDocumentOk,
    error: getDocumentOk ? '' : getDocument.error || 'DOM.getDocument refused',
  });
  steps.push({
    name: 'guarded-getDocument-reply-clean',
    at: at(),
    ok: getDocumentOk && replyClean,
    error:
      getDocumentOk && replyClean
        ? ''
        : getDocumentOk
          ? 'probe reply carried a protected document path'
          : getDocument.error || 'DOM.getDocument refused',
  });
  const disable = await outcome(c.send('DOM.disable', {}, pageSession));
  noteStep('guarded-dom-disable', disable);
  const wrongDepth = await outcome(c.send('DOM.getDocument', { depth: 0, pierce: true }, pageSession));
  const wrongDepthRefused =
    !wrongDepth.ok &&
    typeof wrongDepth.error === 'string' &&
    wrongDepth.error.includes('history may restore a protected document');
  steps.push({
    name: 'guarded-getDocument-wrong-depth-refused',
    at: at(),
    ok: wrongDepthRefused,
    error: wrongDepthRefused
      ? wrongDepth.error || ''
      : wrongDepth.ok
        ? 'DOM.getDocument depth 0 unexpectedly allowed'
        : wrongDepth.error || '',
  });
  st.steps = steps;
  st.autoAttachArmed = auto.ok;
  return {
    steps,
    bypassOk: bypass.ok,
    bypassError: bypass.ok ? '' : bypass.error,
    autoAttachOk: auto.ok,
    autoAttachError: auto.ok ? '' : auto.error,
    evaluateRefused: evaluateHistory,
    getDocumentOk,
    getDocumentError: getDocumentOk ? '' : getDocument.error || '',
    getDocumentReplyClean: getDocumentOk && replyClean,
    disableOk: disable.ok,
    disableError: disable.ok ? '' : disable.error || '',
    wrongDepthRefused,
    elapsedMs: at(),
  };
}

function countDomNodes(node, acc = { nodes: 0, markers: 0, marker: '' }) {
  if (!node || typeof node !== 'object') return acc;
  acc.nodes += 1;
  const marker = acc.marker || '';
  const fields = [node.nodeValue, node.documentURL, node.baseURL, ...(node.attributes || [])];
  for (const v of fields) {
    if (marker && typeof v === 'string' && v.includes(marker)) acc.markers += 1;
  }
  for (const child of node.children || []) countDomNodes(child, acc);
  for (const root of node.shadowRoots || []) countDomNodes(root, acc);
  if (node.contentDocument) countDomNodes(node.contentDocument, acc);
  if (node.templateContent) countDomNodes(node.templateContent, acc);
  return acc;
}

export async function measureHistorySetupProbeMarkers(opts) {
  const st = hrExplicitState();
  const c = st.client;
  if (!c) return { steps: [{ name: 'guarded-probe-markers', at: 0, ok: false, error: 'no open guarded client' }], elapsedMs: 0 };
  const started = st.started || Date.now();
  const at = () => Date.now() - started;
  const steps = st.steps || [];
  const pageSession = st.pageSession;
  const marker = opts.marker || 'HS_B2_MARKER_7f3a9c';
  const protectedPrefix = opts.protectedPrefix || '';
  const noteStep = (name, o) => {
    const row = { name, at: at(), ok: o.ok, error: o.ok ? '' : o.error || '' };
    steps.push(row);
    return row;
  };
  const bypass = await outcome(c.send('Network.setBypassServiceWorker', { bypass: true }, pageSession));
  noteStep('guarded-setBypassServiceWorker', bypass);
  const auto = await outcome(c.send('Target.setAutoAttach', {
    autoAttach: true,
    flatten: true,
    waitForDebuggerOnStart: false,
    filter: FRAME_CHILDREN,
  }, pageSession));
  noteStep('guarded-setAutoAttach', auto);
  const getDocument = await outcome(c.send('DOM.getDocument', { depth: -1, pierce: true }, pageSession));
  noteStep('guarded-getDocument', getDocument);
  const root = getDocument.result?.root;
  const counts = countDomNodes(root, { nodes: 0, markers: 0, marker });
  const replyText = JSON.stringify(getDocument.result ?? getDocument.value ?? getDocument.error ?? getDocument);
  const replyClean =
    !protectedPrefix ||
    (!replyText.includes(`${protectedPrefix}/`) &&
      !replyText.includes('/seven-marked') &&
      !/"documentURL":"http:\/\/seven\.protected\.test[^"]+\//.test(replyText));
  const pathLeak = Boolean(protectedPrefix) && getDocument.ok && !replyClean;
  const markerAbsent = counts.markers === 0 && !replyText.includes(marker);
  const getDocumentError = getDocument.error || '';
  const targetTextRefusal = !getDocument.ok && /target is on seven's origin/.test(getDocumentError);
  steps.push({
    name: 'guarded-getDocument-reply-clean',
    at: at(),
    ok: getDocument.ok && replyClean,
    error:
      getDocument.ok && replyClean
        ? ''
        : getDocument.ok
          ? 'probe reply carried a protected document path'
          : getDocumentError || 'DOM.getDocument refused',
  });
  steps.push({
    name: 'guarded-getDocument-markers-absent',
    at: at(),
    ok: markerAbsent,
    error: markerAbsent ? '' : `marker substring present in serialized reply; treeHits=${counts.markers}`,
  });
  const disable = await outcome(c.send('DOM.disable', {}, pageSession));
  noteStep('guarded-dom-disable', disable);
  st.steps = steps;
  return {
    steps,
    bypassOk: bypass.ok,
    autoAttachOk: auto.ok,
    getDocumentOk: getDocument.ok,
    getDocumentError,
    targetTextRefusal,
    disableOk: disable.ok,
    disableError: disable.error || '',
    replyClean: getDocument.ok && replyClean,
    markerAbsent,
    pathLeak,
    treeNodeCount: counts.nodes,
    markerHits: counts.markers,
    elapsedMs: at(),
  };
}

export async function measureHistorySetupLockedOrdinaryAutoChild(opts) {
  const st = hrExplicitState();
  const c = st.client;
  if (!c) return { steps: [{ name: 'guarded-auto-child', at: 0, ok: false, error: 'no open guarded client' }], elapsedMs: 0 };
  const started = st.started || Date.now();
  const at = () => Date.now() - started;
  const steps = st.steps || [];
  const noteStep = (name, o) => {
    const row = { name, at: at(), ok: o.ok, error: o.ok ? '' : o.error || '' };
    steps.push(row);
    return row;
  };
  const framePrefix = opts.framePrefix || st.opts?.framePrefix || '';
  const before = new Set(Array.isArray(opts.beforeIframes) ? opts.beforeIframes : (st.beforeIframes || []));
  const created = st.created || [];
  const attached = [];
  const targetEvents = [];
  const noteTargetEvent = (e) => {
    if (e.method !== 'Target.attachedToTarget' && e.method !== 'Target.detachedFromTarget') return;
    const info = e.params?.targetInfo ?? {};
    const targetId = info.targetId ?? e.params?.targetId ?? '';
    targetEvents.push({
      at: at(),
      method: e.method,
      sessionPrefix: typeof e.sessionId === 'string' ? e.sessionId.slice(0, 8) : '',
      childSessionPrefix: typeof e.params?.sessionId === 'string' ? e.params.sessionId.slice(0, 8) : '',
      targetPrefix: typeof targetId === 'string' ? targetId.slice(0, 8) : '',
      type: info.type || '',
    });
    if (e.method !== 'Target.attachedToTarget') return;
    if (info.type === 'iframe' && typeof info.targetId === 'string' && info.targetId && !before.has(info.targetId)) {
      const childSession = typeof e.params?.sessionId === 'string' ? e.params.sessionId : '';
      if (childSession) {
        attached.push({
          at: at(),
          target: info.targetId,
          sessionId: childSession,
          waiting: e.params?.waitingForDebugger ?? false,
          onSession: e.sessionId ?? '',
          url: typeof info.url === 'string' ? info.url : '',
        });
      }
    }
  };
  for (const e of c.events) noteTargetEvent(e);
  c.on(noteTargetEvent);
  const formatSeen = () => targetEvents.map((e) => `${e.at}ms ${e.method} sess=${e.sessionPrefix || '-'} child=${e.childSessionPrefix || '-'} target=${e.targetPrefix || '-'} type=${e.type || '-'}`).join('; ') || '(none)';
  try {
    let childId = '';
    const findDeadline = Date.now() + (opts.findWaitMs ?? st.opts?.findWaitMs ?? 20000);
    while (Date.now() < findDeadline && !childId) {
      for (const row of created) {
        if (row.target && !before.has(row.target)) {
          childId = row.target;
          break;
        }
      }
      if (!childId) {
        const listedOut = await outcome(c.send('Target.getTargets'));
        const listed = listedOut.ok ? listedOut.result?.targetInfos ?? [] : [];
        for (const t of listed) {
          if (t.type === 'iframe' && typeof t.url === 'string' && t.url.startsWith(framePrefix) && !before.has(t.targetId)) {
            childId = t.targetId;
            break;
          }
        }
      }
      if (!childId) await sleep(100);
    }
    noteStep('guarded-discover-iframe', { ok: Boolean(childId), error: childId ? '' : 'no fresh third-party iframe target' });
    if (!childId) return { steps, childId: '', targetEvents, elapsedMs: at() };
    let childSession = '';
    const attachDeadline = Date.now() + (opts.attachWaitMs ?? st.opts?.attachWaitMs ?? 20000);
    while (Date.now() < attachDeadline && !childSession) {
      for (const row of attached) {
        if (row.target === childId && row.sessionId) {
          childSession = row.sessionId;
          break;
        }
      }
      if (!childSession) await sleep(50);
    }
    const deliveredAt = at();
    const childPrefix = childId.slice(0, 8);
    const attachedDesc = attached.map((r) => `target=${(r.target || '').slice(0, 8)} sess=${(r.sessionId || '').slice(0, 8)} on=${(r.onSession || '').slice(0, 8) || '-'}`).join('; ') || '(none)';
    const miss = childSession ? '' : `auto-attached child session not delivered; childIdPrefix=${childPrefix}; attached=[${attachedDesc}]; seen=[${formatSeen()}]`;
    noteStep('guarded-auto-attach-iframe', {
      ok: Boolean(childSession),
      error: miss,
    });
    let childEval = { ok: false, error: 'no child session', value: '' };
    if (childSession) {
      const ev = await outcome(c.send('Runtime.evaluate', { expression: "document.querySelector('#inner')?.textContent ?? ''", returnByValue: true }, childSession));
      childEval = {
        ok: ev.ok,
        error: ev.ok ? '' : ev.error,
        value: ev.ok ? ev.result?.result?.value ?? '' : '',
      };
      noteStep('guarded-child-evaluate', childEval);
    }
    return {
      steps,
      childId,
      targetEvents,
      autoAttach: {
        ok: Boolean(childSession),
        error: miss,
        sessionId: childSession,
        deliveredAt,
      },
      childEvaluate: childEval,
      elapsedMs: at(),
    };
  } catch (error) {
    return {
      steps,
      childId: '',
      targetEvents,
      autoAttach: { ok: false, error: `${String(error?.message ?? error)}; seen=[${formatSeen()}]`, sessionId: '', deliveredAt: at() },
      childEvaluate: { ok: false, error: String(error?.message ?? error), value: '' },
      elapsedMs: at(),
    };
  }
}

export async function measureHistoryRestoreLockedOrdinaryExplicitAttach(opts) {
  const st = hrExplicitState();
  const c = st.client;
  if (!c) return { steps: [{ name: 'guarded-explicit-attach', at: 0, ok: false, error: 'no open guarded client' }], elapsedMs: 0 };
  const started = st.started || Date.now();
  const at = () => Date.now() - started;
  const steps = st.steps || [];
  const noteStep = (name, o) => {
    const row = { name, at: at(), ok: o.ok, error: o.ok ? '' : o.error || '' };
    steps.push(row);
    return row;
  };
  const framePrefix = opts.framePrefix || st.opts?.framePrefix || '';
  const created = st.created || [];
  const before = new Set(Array.isArray(opts.beforeIframes) ? opts.beforeIframes : (st.beforeIframes || []));
  try {
    let childId = '';
    const findDeadline = Date.now() + (opts.findWaitMs ?? st.opts?.findWaitMs ?? 15000);
    while (Date.now() < findDeadline && !childId) {
      for (const row of created) {
        if (row.target && !before.has(row.target)) {
          childId = row.target;
          break;
        }
      }
      if (!childId) {
        const listedOut = await outcome(c.send('Target.getTargets'));
        const listed = listedOut.ok ? listedOut.result?.targetInfos ?? [] : [];
        for (const t of listed) {
          if (t.type === 'iframe' && typeof t.url === 'string' && t.url.startsWith(framePrefix) && !before.has(t.targetId)) {
            childId = t.targetId;
            break;
          }
        }
      }
      if (!childId) await sleep(100);
    }
    noteStep('guarded-discover-iframe', { ok: Boolean(childId), error: childId ? '' : 'no fresh third-party iframe target' });
    if (!childId) return { steps, childId: '', elapsedMs: at() };
    const attachAskedAt = at();
    const childAttach = await outcome(c.send('Target.attachToTarget', { targetId: childId, flatten: true }));
    const attachRepliedAt = at();
    noteStep('guarded-explicit-attach-iframe', childAttach);
    let childEval = { ok: false, error: 'attach failed', value: '' };
    if (childAttach.ok) {
      const sid = childAttach.result?.sessionId ?? '';
      const ev = await outcome(c.send('Runtime.evaluate', { expression: "document.querySelector('#inner')?.textContent ?? ''", returnByValue: true }, sid));
      childEval = {
        ok: ev.ok,
        error: ev.ok ? '' : ev.error,
        value: ev.ok ? ev.result?.result?.value ?? '' : '',
      };
      noteStep('guarded-child-evaluate', childEval);
    }
    return {
      steps,
      childId,
      explicitAttach: {
        ok: childAttach.ok,
        error: childAttach.ok ? '' : childAttach.error,
        sessionId: childAttach.ok ? childAttach.result?.sessionId ?? '' : '',
        askedAt: attachAskedAt,
        repliedAt: attachRepliedAt,
        replyMs: attachRepliedAt - attachAskedAt,
      },
      childEvaluate: childEval,
      elapsedMs: at(),
    };
  } finally {
    await c.close().catch(() => {});
    hrExplicitState().client = null;
  }
}

async function answered(promise) {
  try {
    return { ok: true, result: await promise };
  } catch (error) {
    return { ok: false, error: error.cdp ?? { message: String(error?.message ?? error) } };
  }
}

export async function measureURLLessIframes(getLink, opts) {
  const c = await CdpClient.open(getLink(), opts.browser);
  c.callTimeout = 20000;
  try {
    await c.send('Target.setDiscoverTargets', { discover: true });
    await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
    await c.send('Browser.getHistogram', { name: 'arm-urlless-pages' });
    await sleep(300);
    const mark = c.events.length;
    await c.send('Browser.getHistogram', { name: 'arm-urlless-iframes' });
    await sleep(300);
    const asked = {};
    for (const id of opts.frames) {
      asked[id] = {
        info: answered(c.send('Target.getTargetInfo', { targetId: id })),
        attach: answered(c.send('Target.attachToTarget', { targetId: id, flatten: true })),
      };
    }
    await sleep(300);
    await c.send('Browser.getHistogram', { name: 'commit-urlless-iframes' });
    const rows = {};
    for (const [id, p] of Object.entries(asked)) {
      const info = await p.info;
      const attach = await p.attach;
      rows[id] = {
        info: info.ok ? { ok: true, url: info.result.targetInfo?.url ?? '' } : { ok: false, error: info.error },
        attach: attach.ok ? { ok: true } : { ok: false, error: attach.error },
      };
    }
    await sleep(300);
    const list = (await c.send('Target.getTargets')).targetInfos.map((t) => t.targetId);
    const events = c.events.slice(mark).map((e) => ({
      method: e.method,
      sessionId: e.sessionId ?? '',
      child: e.params?.sessionId ?? '',
      targetId: e.params?.targetInfo?.targetId ?? e.params?.targetId ?? '',
      url: e.params?.targetInfo?.url ?? '',
      waiting: e.params?.waitingForDebugger ?? null,
      reason: e.params?.reason ?? '',
    }));
    return { rows, list, events };
  } finally {
    await c.close();
  }
}

export async function measurePendingAncestorIframes(getLink, opts) {
  const c = await CdpClient.open(getLink(), opts.browser);
  c.callTimeout = 20000;
  const settle = opts.settle || 'ordinary';
  try {
    await c.send('Target.setDiscoverTargets', { discover: true });
    await c.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true });
    await c.send('Browser.getHistogram', { name: 'arm-pending-pages' });
    await sleep(300);
    const mark = c.events.length;
    await c.send('Browser.getHistogram', { name: 'arm-pending-iframes' });
    await sleep(300);
    const asked = {};
    for (const id of opts.frames) {
      asked[id] = {
        info: answered(c.send('Target.getTargetInfo', { targetId: id })),
        attach: answered(c.send('Target.attachToTarget', { targetId: id, flatten: true })),
      };
    }
    await sleep(300);
    if (settle === 'ordinary') {
      await c.send('Browser.getHistogram', { name: 'commit-pending-iframes' });
    } else if (settle === 'ordinary-confirm' || settle.endsWith('-confirm')) {
      await c.send('Browser.getHistogram', { name: 'arm-page-confirm' });
      await sleep(300);
      await c.send('Browser.getHistogram', { name: 'commit-pending-iframes' });
      await sleep(300);
      await c.send('Browser.getHistogram', { name: 'flush:page-confirm' });
    } else if (settle.endsWith('-url')) {
      await c.send('Browser.getHistogram', { name: 'settle-owner-url' });
      await sleep(300);
      await c.send('Browser.getHistogram', { name: 'flush:page-confirm' });
    } else if (settle.endsWith('-nav')) {
      await c.send('Browser.getHistogram', { name: 'settle-owner-nav' });
    } else {
      throw new Error(`unknown pending-ancestor settle ${settle}`);
    }
    await sleep(300);
    const rows = {};
    for (const [id, p] of Object.entries(asked)) {
      const info = await p.info;
      const attach = await p.attach;
      rows[id] = {
        info: info.ok ? { ok: true, url: info.result.targetInfo?.url ?? '' } : { ok: false, error: info.error },
        attach: attach.ok ? { ok: true } : { ok: false, error: attach.error },
      };
    }
    await sleep(300);
    const list = (await c.send('Target.getTargets')).targetInfos.map((t) => t.targetId);
    const events = c.events.slice(mark).map((e) => ({
      method: e.method,
      sessionId: e.sessionId ?? '',
      child: e.params?.sessionId ?? '',
      targetId: e.params?.targetInfo?.targetId ?? e.params?.targetId ?? '',
      url: e.params?.targetInfo?.url ?? '',
      waiting: e.params?.waitingForDebugger ?? null,
      reason: e.params?.reason ?? '',
    }));
    return { rows, list, events, settle };
  } finally {
    await c.close();
  }
}
