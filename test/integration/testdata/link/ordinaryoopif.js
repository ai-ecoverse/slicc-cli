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
