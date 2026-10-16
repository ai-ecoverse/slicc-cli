import { run } from '/vendor/slicc-browsers/guard-runner.mjs';
import { CdpClient } from './cdp.js';

class SocketClient {
  #nextId = 1;
  #pending = new Map();
  #waiters = new Set();

  static open(url) {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url);
      ws.onopen = () => resolve(new SocketClient(ws));
      ws.onerror = () => reject(new Error(`can't reach ${url}`));
    });
  }

  constructor(ws) {
    this.ws = ws;
    this.events = [];
    this.transport = 'direct';
    this.listeners = new Set();
    ws.onmessage = (m) => this.#dispatch(JSON.parse(m.data));
    ws.onclose = () => {
      for (const { reject } of this.#pending.values()) reject(new Error('direct cdp socket closed'));
      this.#pending.clear();
    };
  }

  #dispatch(msg) {
    if (msg.id !== undefined) {
      const call = this.#pending.get(msg.id);
      this.#pending.delete(msg.id);
      if (!call) return;
      if (msg.error) {
        const error = new Error(msg.error.message);
        error.cdp = msg.error;
        call.reject(error);
      } else call.resolve(msg.result);
      return;
    }
    this.events.push(msg);
    for (const listener of this.listeners) listener(msg);
    for (const waiter of [...this.#waiters]) {
      if (waiter.match(msg)) {
        this.#waiters.delete(waiter);
        waiter.resolve(msg);
      }
    }
  }

  send(method, params = {}, sessionId) {
    const id = this.#nextId++;
    const msg = { id, method, params };
    if (sessionId) msg.sessionId = sessionId;
    return new Promise((resolve, reject) => {
      this.#pending.set(id, { resolve, reject });
      try {
        this.ws.send(JSON.stringify(msg));
      } catch (error) {
        this.#pending.delete(id);
        reject(error);
      }
    });
  }

  on(listener) {
    this.listeners.add(listener);
  }

  waitEvent(match, timeout = 15000) {
    const seen = this.events.find(match);
    if (seen) return Promise.resolve(seen);
    return new Promise((resolve, reject) => {
      const waiter = { match, resolve };
      this.#waiters.add(waiter);
      setTimeout(() => {
        if (this.#waiters.delete(waiter)) reject(new Error('timed out waiting for a cdp event'));
      }, timeout);
    });
  }

  close() {
    this.ws.close();
  }
}

function sessionBound(c, name) {
  const calls = new Map();
  const tracked = { name, last: null, pending: new Set() };
  c.on?.((e) => {
    if (e.method !== 'Target.detachedFromTarget') return;
    for (const reject of calls.get(e.params.sessionId) ?? []) reject(new Error('the session detached'));
    calls.delete(e.params.sessionId);
  });
  const sent = (method, sessionId, promise) => {
    const call = { method, session: sessionId ?? '', at: Date.now(), answered: false };
    tracked.last = call;
    tracked.pending.add(call);
    return promise.finally(() => {
      call.answered = true;
      tracked.pending.delete(call);
    });
  };
  return {
    tracked,
    events: c.events,
    transport: c.transport,
    waitEvent: (match, timeout) => c.waitEvent(match, timeout),
    send(method, params, sessionId) {
      if (!sessionId) return sent(method, sessionId, c.send(method, params));
      return sent(
        method,
        sessionId,
        new Promise((resolve, reject) => {
          const set = calls.get(sessionId) ?? new Set();
          calls.set(sessionId, set);
          set.add(reject);
          c.send(method, params, sessionId)
            .then(resolve, reject)
            .finally(() => set.delete(reject));
        }),
      );
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

const state = { rows: [], inflight: new Map(), done: false, error: null, summary: null };

export function guardStart(getLink, opts) {
  if (state.started) return { started: false };
  state.started = true;
  const browsers = opts.browsers;
  (async () => {
    const set = await (await fetch('/vendor/slicc-browsers/guard-vectors.json')).json();
    const execution = await run(set, {
      clientFactory: async ({ slot, vector, target, variant }) => {
        const b = browsers[slot % browsers.length];
        const client = await retried(() => CdpClient.open(getLink(), b.id));
        client.transport = 'cli';
        const unguarded = await retried(() => SocketClient.open(b.ws));
        const row = { id: vector.id, target, variant, slot, browser: b.id, started: Date.now() };
        const guarded = sessionBound(client, 'guarded');
        const observer = sessionBound(unguarded, 'unguarded');
        row.clients = [guarded.tracked, observer.tracked];
        state.inflight.set(slot, row);
        return {
          client: guarded,
          unguarded: observer,
          close: async () => {
            state.inflight.delete(slot);
            unguarded.close();
            await client.close().catch(() => {});
          },
        };
      },
      targets: opts.targets,
      capabilities: opts.capabilities,
      variant: opts.variants,
      parallel: opts.parallel,
      fixture: fixtureLog(opts.log),
      log: (row) => state.rows.push(row),
    });
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
  return [...state.inflight.values()].map(({ clients, started, ...row }) => ({
    ...row,
    ms: now - started,
    last: clients.map((c) => {
      const call = [...c.pending][0] ?? c.last;
      return call ? { client: c.name, method: call.method, session: call.session, answered: call.answered, ms: now - call.at } : { client: c.name };
    }),
  }));
}

export function guardProgress(since) {
  return { done: state.done, error: state.error, rows: state.rows.slice(since), inflight: inflight(), summary: state.done ? state.summary : null };
}
