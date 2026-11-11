import { run } from '/vendor/slicc-browsers/guard-runner.mjs';
import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { foregroundMouseCall } from './foreground.js';

const CALL_TIMEOUT_MS = 10000;

const seenSessions = new Map();

function noteSession(name, e) {
  if (e.method === 'Target.attachedToTarget') {
    const info = e.params.targetInfo ?? {};
    seenSessions.set(e.params.sessionId, {
      client: name,
      type: info.type,
      url: info.url,
      opener: info.openerId ?? '',
      context: info.browserContextId ?? '',
      waitingForDebugger: e.params.waitingForDebugger,
      parent: e.sessionId ?? '',
      attached: Date.now(),
    });
  } else if (e.method === 'Target.detachedFromTarget' && seenSessions.has(e.params.sessionId)) {
    seenSessions.get(e.params.sessionId).detached = Date.now();
  }
}

function describeSessions(row) {
  const now = Date.now();
  const out = [];
  for (const calls of Object.values(row.inFlight?.clients ?? {})) {
    for (const call of calls ?? []) {
      if (!call.session) continue;
      const seen = seenSessions.get(call.session);
      if (!seen) {
        out.push(`session ${call.session}: the page never saw it attach`);
        continue;
      }
      const { attached, detached, ...info } = seen;
      out.push(`session ${call.session}: ${JSON.stringify({ ...info, attachedMsAgo: now - attached, detachedMsAgo: detached ? now - detached : null })}`);
    }
  }
  return out;
}

async function noteForwarded(gateURL, forwarded, msg) {
  if (!gateURL || !msg || !Number.isInteger(msg.id)) return;
  try {
    const r = await fetch(gateURL, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        action: 'forwarded',
        wireId: msg.id,
        method: msg.method,
        sessionId: msg.sessionId ?? '',
      }),
    });
    if (!r.ok) return;
    const { entry } = await r.json();
    if (entry?.method) forwarded.push(entry);
  } catch {
    return;
  }
}

const attachRefusedText = 'slicc: refused: target is on seven\'s origin';

function bareOrigin(url) {
  try {
    return new URL(url).origin;
  } catch {
    return '';
  }
}

function resetFrameLeak(frameLeak) {
  frameLeak.attached.length = 0;
  frameLeak.listings.length = 0;
  frameLeak.attachAttempts.length = 0;
  frameLeak.sessions.length = 0;
}

function frameHostMatch(frameLeak, url) {
  if (!url) return false;
  if (url.includes('/doc?n=frame-')) return true;
  const origin = bareOrigin(url);
  return (frameLeak.hosts ?? []).some((host) => {
    if (typeof host !== 'string' || !host) return false;
    if (url.startsWith(host)) return true;
    const hostOrigin = bareOrigin(host);
    return Boolean(origin && hostOrigin && origin === hostOrigin);
  });
}

function rememberFrame(frameLeak, targetId, url, origin) {
  const prev = frameLeak.ids.get(targetId);
  frameLeak.ids.set(targetId, {
    url: url || prev?.url || '',
    origin: origin || prev?.origin || '',
  });
  frameLeak.pending?.delete(targetId);
}

function noteFrameTarget(frameLeak, info, { attached } = {}) {
  if (!frameLeak || !info?.targetId) return;
  const url = typeof info.url === 'string' ? info.url : '';
  const type = info.type ?? '';
  if (url && frameHostMatch(frameLeak, url)) {
    const origin = bareOrigin(url);
    if (!origin) return;
    rememberFrame(frameLeak, info.targetId, url, origin);
    return;
  }
  if (attached && type === 'iframe') {
    rememberFrame(frameLeak, info.targetId, url, bareOrigin(url));
    return;
  }
  if (type === 'iframe' && (!url || url === 'about:blank')) {
    (frameLeak.pending ??= new Set()).add(info.targetId);
  }
}

function harvestFrameTargets(frameLeak) {
  const events = frameLeak?.observer?.events;
  if (!Array.isArray(events)) return;
  for (const e of events) {
    const info = e.params?.targetInfo;
    if (!info) continue;
    if (e.method === 'Target.attachedToTarget') noteFrameTarget(frameLeak, info, { attached: true });
    else if (e.method === 'Target.targetCreated' || e.method === 'Target.targetInfoChanged') noteFrameTarget(frameLeak, info);
  }
}

function noteClientListing(frameLeak, source, info) {
  if (!frameLeak || !info?.targetId || !frameLeak.ids.has(info.targetId)) return;
  const meta = frameLeak.ids.get(info.targetId);
  let origin = meta.origin;
  if (!origin) {
    origin = bareOrigin(info.url ?? '');
    if (origin) rememberFrame(frameLeak, info.targetId, info.url ?? meta.url, origin);
  }
  frameLeak.listings.push({
    source,
    targetId: info.targetId,
    type: info.type ?? '',
    url: info.url ?? '',
    title: info.title ?? '',
    origin,
  });
}

function frameLeakEvidence(frameLeak) {
  const frames = [...frameLeak.ids.entries()].map(([targetId, meta]) => ({ targetId, url: meta.url, origin: meta.origin }));
  const listings = frameLeak.listings.map((row) => ({ ...row }));
  const attached = frameLeak.attached.map((row) => ({ ...row }));
  const sessions = frameLeak.sessions.map((row) => ({ ...row }));
  const attachAttempts = frameLeak.attachAttempts.map((row) => ({ ...row }));
  const scrubbed = listings.length > 0 && listings.every((row) => row.url === row.origin && row.title === row.origin);
  const attachRefused =
    attachAttempts.length > 0 && attachAttempts.every((row) => row.refused && String(row.message ?? '').includes(attachRefusedText));
  return {
    frames,
    attached,
    listings,
    sessions,
    attachAttempts,
    iframeAttached: attached.length > 0,
    frameScrubbed: scrubbed,
    attachRefused,
  };
}

function frameLeakIncomplete(evidence) {
  if (!evidence.frames.length) return 'unguarded observer never saw the framed target';
  return '';
}

function frameLeakFailure(evidence) {
  if (!evidence.frames.length) return '';
  if (evidence.iframeAttached) return 'guarded Target.attachedToTarget reached the framed target';
  if (evidence.sessions.length) return 'guarded session opened for the framed targetId';
  if (!evidence.listings.length) return 'guarded client never listed the framed target';
  if (!evidence.frameScrubbed) return 'guarded listing left a path, query or page title on the framed target';
  if (!evidence.attachRefused) return 'Target.attachToTarget on the framed targetId was not refused with slicc: refused: target is on seven\'s origin';
  return '';
}

function sessionBound(c, name, { inject, gate, frameLeak } = {}) {
  const calls = new Map();
  const events = [];
  const fake = new Set();
  const forwarded = [];
  let probingAttach = false;
  const onEvent = (e) => {
    noteSession(name, e);
    const info = e.params?.targetInfo;
    if (frameLeak && (e.method === 'Target.attachedToTarget' || e.method === 'Target.targetCreated' || e.method === 'Target.targetInfoChanged')) {
      if (name === 'unguarded') noteFrameTarget(frameLeak, info, { attached: e.method === 'Target.attachedToTarget' });
      else {
        if (e.method === 'Target.attachedToTarget' && frameLeak.ids.has(info?.targetId)) {
          frameLeak.attached.push({ targetId: info.targetId, type: info.type ?? '', url: info.url ?? '', sessionId: e.params.sessionId ?? '' });
          frameLeak.sessions.push({ targetId: info.targetId, sessionId: e.params.sessionId ?? '' });
        }
        if (e.method === 'Target.targetCreated' || e.method === 'Target.targetInfoChanged') {
          noteClientListing(frameLeak, e.method, info);
        }
      }
    }
    events.push(e);
    if (e.method !== 'Target.detachedFromTarget') return;
    for (const reject of calls.get(e.params.sessionId) ?? []) reject(new Error('the session detached'));
    calls.delete(e.params.sessionId);
  };
  c.on?.(onEvent);
  const injectChromeUI = async (result) => {
    const id = `CHROME-UI-${name}-${fake.size + 1}`;
    fake.add(id);
    onEvent({
      method: 'Target.attachedToTarget',
      params: {
        sessionId: id,
        targetInfo: { targetId: id, type: 'browser_ui', title: '', url: 'chrome://omnibox-popup.top-chrome/', attached: true, canAccessOpener: false },
        waitingForDebugger: false,
      },
    });
    await new Promise((resolve) => setTimeout(resolve, 100));
    return result;
  };
  const refreshFrames = async () => {
    harvestFrameTargets(frameLeak);
    if (!frameLeak?.observer?.send) return;
    try {
      const listed = await frameLeak.observer.send('Target.getTargets', { filter: [{}] });
      const infos = listed?.targetInfos ?? [];
      frameLeak.observerListed = infos.map((info) => ({ targetId: info.targetId, type: info.type, url: info.url, title: info.title }));
      for (const info of infos) noteFrameTarget(frameLeak, info);
    } catch (error) {
      frameLeak.observerError = String(error?.message ?? error);
    }
  };
  const waitForFrames = async () => {
    const ms = frameLeak?.waitMs ?? 8000;
    const deadline = Date.now() + ms;
    for (;;) {
      await refreshFrames();
      if (frameLeak.ids.size) return;
      if (Date.now() >= deadline) {
        await refreshFrames();
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
  };
  const recordClientInfos = (infos) => {
    for (const info of infos ?? []) {
      if (!frameLeak.ids.has(info.targetId)) {
        const origin = bareOrigin(info.url ?? '');
        for (const [, meta] of frameLeak.ids) {
          if (origin && origin === meta.origin && (info.type === 'iframe' || (info.url ?? '') === origin)) {
            frameLeak.ids.set(info.targetId, meta);
          }
        }
      }
      noteClientListing(frameLeak, 'Target.getTargets', info);
    }
  };
  const probeAttach = async () => {
    harvestFrameTargets(frameLeak);
    if (!frameLeak.ids.size) {
      frameLeak.snapshot = frameLeakEvidence(frameLeak);
      return;
    }
    for (const targetId of frameLeak.ids.keys()) {
      if (frameLeak.attachAttempts.some((row) => row.targetId === targetId)) continue;
      try {
        const attached = await c.send('Target.attachToTarget', { targetId, flatten: true });
        frameLeak.attachAttempts.push({ targetId, refused: false, message: '', sessionId: attached?.sessionId ?? '' });
        if (attached?.sessionId) {
          frameLeak.sessions.push({ targetId, sessionId: attached.sessionId });
          await c.send('Target.detachFromTarget', { sessionId: attached.sessionId }).catch(() => {});
        }
      } catch (error) {
        frameLeak.attachAttempts.push({ targetId, refused: true, message: String(error?.message ?? error) });
      }
    }
    frameLeak.snapshot = frameLeakEvidence(frameLeak);
  };
  const afterSend = async (before, result) => {
    const msg = c.sent[before];
    if (!msg) return result;
    if (gate) await noteForwarded(gate, forwarded, msg);
    else {
      const entry = { method: msg.method, id: msg.id, params: msg.params };
      if (msg.sessionId) entry.sessionId = msg.sessionId;
      forwarded.push(entry);
    }
    if (frameLeak && msg.method === 'Target.setAutoAttach' && !probingAttach && !msg.sessionId) resetFrameLeak(frameLeak);
    if (frameLeak && msg.method === 'Target.getTargets' && Array.isArray(result?.targetInfos)) {
      if (name === 'unguarded') {
        for (const info of result.targetInfos) noteFrameTarget(frameLeak, info);
      } else if (probingAttach) {
        recordClientInfos(result.targetInfos);
      } else {
        probingAttach = true;
        try {
          await waitForFrames();
          const again = await c.send('Target.getTargets', { filter: [{}] }).catch(() => null);
          recordClientInfos(again?.targetInfos ?? result.targetInfos);
          await probeAttach();
        } finally {
          probingAttach = false;
        }
      }
    }
    if (frameLeak && name === 'client' && msg.method === 'Target.getTargetInfo' && result?.targetInfo) {
      noteClientListing(frameLeak, 'Target.getTargetInfo', result.targetInfo);
    }
    return result;
  };
  return {
    events,
    messages: c.messages,
    sent: c.sent,
    forwarded,
    transport: c.transport,
    close: () => c.close(),
    waitEvent: (match, timeout) => c.waitEvent(match, timeout),
    send(method, params, sessionId) {
      if (fake.has(sessionId)) return new Promise(() => {});
      const before = c.sent.length;
      if (!sessionId) {
        const sent = c.send(method, params);
        if (inject === 'browser_ui' && method === 'Target.setAutoAttach' && params?.autoAttach) {
          return sent.then(injectChromeUI).then((result) => afterSend(before, result));
        }
        return sent.then((result) => afterSend(before, result));
      }
      return new Promise((resolve, reject) => {
        const set = calls.get(sessionId) ?? new Set();
        calls.set(sessionId, set);
        set.add(reject);
        foregroundMouseCall(c, method, params, sessionId)
          .then((result) => afterSend(before, result))
          .then(resolve, reject)
          .finally(() => set.delete(reject));
      });
    },
  };
}

function deepRewrite(value, from, to) {
  if (typeof value === 'string') return value.split(from).join(to);
  if (Array.isArray(value)) return value.map((item) => deepRewrite(item, from, to));
  if (value && typeof value === 'object') {
    const out = {};
    for (const [key, child] of Object.entries(value)) out[key] = deepRewrite(child, from, to);
    return out;
  }
  return value;
}

function specializeTabIframeLeak(vector, frameKey, targets) {
  const clone = deepRewrite(JSON.parse(JSON.stringify(vector)), '{seven}', `{${frameKey}}`);
  clone.targets = targets;
  const frameURL = `{${frameKey}}/doc?n=frame-`;
  clone.trigger = [
    ...clone.trigger.map((step) => (step.wait === 2000 ? { wait: 4000 } : step)),
    {
      cdp: 'Target.getTargets',
      params: { filter: [{}] },
      session: 'browser',
      as: 'listed',
      requires: ['browserSession'],
    },
  ];
  clone.observe = {
    ...clone.observe,
    iframeAttached: {
      means: 'the client got Target.attachedToTarget for the framed target under any type',
      check: {
        any: [
          {
            event: 'Target.attachedToTarget',
            where: { params: { targetInfo: { type: 'iframe' } } },
            observer: 'client',
          },
          {
            event: 'Target.attachedToTarget',
            where: { params: { targetInfo: { url: { $contains: frameURL } } } },
            observer: 'client',
          },
        ],
        observer: 'client',
      },
    },
  };
  clone.control.expected = {
    ...clone.control.expected,
    iframeAttached: true,
  };
  clone.expected = {
    ...clone.expected,
    iframeAttached: false,
  };
  return clone;
}

function fixtureLog(base) {
  const entries = [];
  return {
    async log() {
      const r = await fetch(`${base}/log?since=${entries.length}`);
      const { from, entries: more } = await r.json();
      more.forEach((entry, i) => {
        entries[from + i] = entry;
      });
      return entries;
    },
  };
}

async function retried(open) {
  for (let i = 1; ; i += 1) {
    try {
      return await open();
    } catch (error) {
      if (i === 3) throw error;
      await new Promise((resolve) => setTimeout(resolve, 500 * i));
    }
  }
}

function delayedInbound(gateURL, opts) {
  const methods = new Set(['Runtime.evaluate', 'Runtime.callFunctionOn', 'DOM.getDocument', 'Accessibility.getFullAXTree', 'Input.dispatchKeyEvent', 'Page.captureScreenshot', 'IO.read']);
  const control = async (data) => {
    const r = await fetch(gateURL, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(data) });
    if (!r.ok) throw new Error(`inbound gate ${data.action}: ${r.status}`);
    return r.json();
  };
  return async ({ targetId, sessionId, ms, side, client, gate }) => {
    if (!gate) throw new Error('explicit inbound gate required');
    const start = client.sent.length;
    const ack = await control({ action: 'arm', targetId, sessionId, ms, side, protectedOrigins: [opts.targets.seven, opts.targets.vault], ordinaryOrigin: opts.targets.ordinary });
    return {
      ...ack,
      release: () => control({ action: 'release', sessionId }),
      audit: async ({ probes }) => {
        const sent = client.sent.slice(start).filter((m) => m.sessionId === sessionId && methods.has(m.method));
        const matched = probes.length === sent.length && probes.every((p, i) => p.method === sent[i].method && JSON.stringify(p.params) === JSON.stringify(sent[i].params));
        if (!matched) return { complete: false, probes: [] };
        const ledger = await control({ action: 'audit', sessionId, probes: probes.map((p, i) => ({ id: p.id, wireId: sent[i].id })) });
        const complete = ledger.probes.length === probes.length && ledger.probes.every((entry, i) => entry.id === probes[i].id && typeof entry.forwarded === 'boolean' && (entry.forwarded || probes[i].reply?.ok === true || Number.isInteger(probes[i].reply?.error?.code)));
        return { ...ledger, complete };
      },
    };
  };
}

const state = { launched: false, rows: [], started: new Map(), running: null, done: false, error: null, summary: null };

const rowKey = (row) => `${row.id} ${row.target} ${row.variant}`;

export function guardStart(getLink, opts) {
  if (state.launched) return { started: false };
  state.launched = true;
  const browsers = opts.browsers;
  const callTimeout = CALL_TIMEOUT_MS * (opts.settleScale ?? 1);
  (async () => {
    const set = await (await fetch('/vendor/slicc-browsers/guard-vectors.json')).json();
    if (opts.only) set.vectors = set.vectors.filter((v) => v.id === opts.only);
    if (opts.errorPageVault) {
      set.vectors = set.vectors.filter((v) => v.trigger?.some((s) => s.errorPage));
      for (const v of set.vectors) v.targets = ['vault'];
    }
    for (const v of set.vectors) {
      if (opts.skips?.[v.id]) v.skip = opts.skips[v.id];
      if (v.id === 'C3.tab-protected' || v.id === 'C3.tab-protected.synthetic') {
        v.liveCheck = {
          any: [
            { loaded: '{target}/doc?n={tag}', observer: 'fixture' },
            { pre: '{target}/doc?n={tag}', observer: 'fixture' },
            {
              all: [
                {
                  event: 'Target.attachedToTarget',
                  where: { params: { targetInfo: { type: 'tab' } } },
                  observer: 'client',
                },
                {
                  not: {
                    event: 'Target.attachedToTarget',
                    where: {
                      params: { targetInfo: { type: 'page', url: { $contains: '{target}/doc' } } },
                    },
                    observer: 'client',
                  },
                },
              ],
              observer: 'client',
            },
          ],
          observer: 'client',
        };
      }
    }
    set.vectors = set.vectors.flatMap((v) => {
      if (v.id !== 'C3.tab-iframe-leak') return [v];
      return [specializeTabIframeLeak(v, 'sevenOopif', ['seven']), specializeTabIframeLeak(v, 'vault', ['vault'])];
    });
    const mouseDiagnostics = new Map();
    const frameLeakByRow = new Map();
    const running = run(set, {
      clientFactory: async ({ slot, vector, target, variant }) => {
        const b = browsers[slot % browsers.length];
        const client = await retried(() => CdpClient.open(getLink(), b.id));
        client.transport = 'cli';
        client.callTimeout = callTimeout;
        const unguarded = await retried(() => SocketClient.open(b.ws));
        unguarded.callTimeout = callTimeout;
        if (vector.id === 'C9.page-drivable') {
          const evidence = { control: [], guarded: [] };
          client.mouseEvidence = evidence.guarded;
          unguarded.mouseEvidence = evidence.control;
          mouseDiagnostics.set(rowKey({ id: vector.id, target, variant }), evidence);
        }
        const frameLeak =
          vector.id === 'C3.tab-iframe-leak'
            ? {
                ids: new Map(),
                attached: [],
                listings: [],
                attachAttempts: [],
                sessions: [],
                observer: unguarded,
                hosts: [opts.targets.sevenOopif, opts.targets.vault].filter(Boolean),
                waitMs: Math.round(8000 * (opts.settleScale ?? 1)),
              }
            : null;
        await unguarded.send('Target.setDiscoverTargets', { discover: true });
        const guarded = sessionBound(client, 'client', { inject: opts.inject, gate: b.gate, frameLeak });
        guarded.inboundDelay = delayedInbound(b.gate, opts);
        if (frameLeak) frameLeakByRow.set(rowKey({ id: vector.id, target, variant }), frameLeak);
        return {
          client: guarded,
          unguarded: sessionBound(unguarded, 'unguarded', { frameLeak }),
          close: async () => {
            unguarded.close();
            await client.close().catch(() => {});
          },
        };
      },
      onStart: (row) => state.started.set(rowKey(row), Date.now()),
      rowTimeoutMs: opts.rowTimeoutMs,
      settleScale: opts.settleScale,
      targets: opts.targets,
      capabilities: opts.capabilities,
      variant: opts.variants,
      parallel: opts.parallel,
      fixture: fixtureLog(opts.log),
      log: (row) => {
        const evidence = mouseDiagnostics.get(rowKey(row));
        if (evidence) { row.diagnostics = evidence; mouseDiagnostics.delete(rowKey(row)); }
        const frameLeak = frameLeakByRow.get(rowKey(row));
        if (frameLeak) {
          harvestFrameTargets(frameLeak);
          const iframeLeak =
            frameLeak.snapshot?.frames?.length > 0 ? frameLeak.snapshot : frameLeakEvidence(frameLeak);
          row.diagnostics = { ...(row.diagnostics && typeof row.diagnostics === 'object' ? row.diagnostics : {}), iframeLeak };
          const incomplete = frameLeakIncomplete(iframeLeak);
          const fail = frameLeakFailure(iframeLeak);
          if (incomplete) {
            row.control = 'incomplete';
            row.guarded = 'incomplete';
            row.live = 'incomplete';
            row.why = [row.why, `incomplete: ${incomplete}`].filter(Boolean).join('; ');
            row.errors = [...(row.errors ?? []), `incomplete: ${incomplete}`];
          } else if (fail) {
            row.guarded = 'fail';
            row.why = [row.why, fail].filter(Boolean).join('; ');
            row.errors = [...(row.errors ?? []), fail];
          }
          frameLeakByRow.delete(rowKey(row));
        }
        state.started.delete(rowKey(row));
        if (row.inFlight) row.errors = [...(row.errors ?? []), ...describeSessions(row)];
        state.rows.push(row);
      },
    });
    state.running = running;
    const execution = await running;
    state.summary = execution.summary({ guard: 'slicc-cli', sha: opts.sha });
  })().then(
    () => {
      state.done = true;
    },
    (error) => {
      state.error = String(error?.stack ?? error);
      state.done = true;
    },
  );
  return { started: true };
}

function inflight() {
  const now = Date.now();
  const rows = state.running?.inFlight() ?? [];
  return rows.map((row) => ({ ...row, ms: now - (state.started.get(rowKey(row)) ?? now) }));
}

export function guardProgress(since) {
  return { done: state.done, error: state.error, rows: state.rows.slice(since), inflight: inflight(), summary: state.done ? state.summary : null };
}
