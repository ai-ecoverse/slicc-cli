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

function sessionBound(c, name, { inject } = {}) {
  const calls = new Map();
  const events = [];
  const fake = new Set();
  const onEvent = (e) => {
    noteSession(name, e);
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
  return {
    events,
    messages: c.messages,
    sent: c.sent,
    transport: c.transport,
    close: () => c.close(),
    waitEvent: (match, timeout) => c.waitEvent(match, timeout),
    send(method, params, sessionId) {
      if (fake.has(sessionId)) return new Promise(() => {});
      if (!sessionId) {
        const sent = c.send(method, params);
        if (inject === 'browser_ui' && method === 'Target.setAutoAttach' && params?.autoAttach) return sent.then(injectChromeUI);
        return sent;
      }
      return new Promise((resolve, reject) => {
        const set = calls.get(sessionId) ?? new Set();
        calls.set(sessionId, set);
        set.add(reject);
        foregroundMouseCall(c, method, params, sessionId)
          .then(resolve, reject)
          .finally(() => set.delete(reject));
      });
    },
  };
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
    for (const v of set.vectors) if (opts.skips?.[v.id]) v.skip = opts.skips[v.id];
    const mouseDiagnostics = new Map();
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
        await unguarded.send('Target.setDiscoverTargets', { discover: true });
        const guarded = sessionBound(client, 'client', { inject: opts.inject });
        guarded.inboundDelay = delayedInbound(b.gate, opts);
        return {
          client: guarded,
          unguarded: sessionBound(unguarded, 'unguarded'),
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
