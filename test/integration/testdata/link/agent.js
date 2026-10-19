const encoder = new TextEncoder();
const decoder = new TextDecoder();
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export const agent = {
  allow: true,
  busy: false,
  log: [],
  watches: new Set(),
  opens: [],
  prompts: [],
  aborts: 0,
  scripts: [],
  hold: null,
  users: 0,
  reportsBusy: true,
  scoops: new Set(),
  lastBusy: false,
};

function busyNow() {
  return agent.busy || agent.scoops.size > 0;
}

function busyLine(busy) {
  const out = { t: 'busy', busy };
  if (busy) out.why = [...(agent.busy ? ['cone'] : []), ...[...agent.scoops].map((s) => `scoop:${s}`)];
  return out;
}

function busyChanged() {
  const now = busyNow();
  if (now === agent.lastBusy) return;
  agent.lastBusy = now;
  for (const w of agent.watches) if (w.busy) w.send(busyLine(now));
}

function setBusy(busy) {
  agent.busy = busy;
  busyChanged();
}

function line(event) {
  return encoder.encode(`${JSON.stringify(event)}\n`);
}

function sink(stream, thinking) {
  let chain = Promise.resolve();
  return {
    stream,
    thinking,
    send(event) {
      if (event.t === 'thinking' && !thinking) return chain;
      chain = chain.then(() => stream.write(line(event))).catch(() => {});
      return chain;
    },
    flush() {
      return chain;
    },
  };
}

function emit(event, prompt) {
  if (event.id) agent.log.push(event);
  if (prompt) prompt.send(event);
  for (const w of agent.watches) w.send(event);
}

function entryOrder() {
  const order = [];
  for (const e of agent.log) if (!order.includes(e.id)) order.push(e.id);
  return order;
}

function replay(since) {
  const order = entryOrder();
  const at = order.indexOf(since);
  if (at < 0) return null;
  const out = [];
  const own = agent.log.filter((e) => e.id === since);
  if (own.length && (own[0].t === 'text' || own[0].t === 'thinking')) {
    out.push({ ...own[0], delta: own.map((e) => e.delta).join('') });
  }
  const after = new Set(order.slice(at + 1));
  for (const e of agent.log) if (after.has(e.id)) out.push(e);
  return out;
}

async function readText(stream, length) {
  const parts = [];
  let total = 0;
  for (let chunk = await stream.read(); chunk; chunk = await stream.read()) {
    parts.push(chunk);
    total += chunk.byteLength;
  }
  if (total !== length) return null;
  const all = new Uint8Array(total);
  let at = 0;
  for (const p of parts) {
    all.set(p, at);
    at += p.byteLength;
  }
  return decoder.decode(all);
}

function holdUntilReleased() {
  return new Promise((resolve) => {
    agent.hold = resolve;
  });
}

async function runScript(prompt, script) {
  emit({ t: 'run', state: 'start' }, prompt);
  let reason = script.end?.reason ?? 'stop';
  let message = script.end?.message;
  for (const step of script.steps ?? []) {
    if (step.wait) await sleep(step.wait);
    if (step.hold) {
      const how = await holdUntilReleased();
      if (how === 'aborted') {
        reason = 'aborted';
        message = undefined;
        break;
      }
    }
    if (step.scoop) {
      agent.scoops.add(step.scoop);
      busyChanged();
    }
    if (step.event) emit(step.event, prompt);
  }
  emit({ t: 'run', state: 'end', reason }, prompt);
  const end = { t: 'end', reason };
  if (message) end.message = message;
  prompt.send(end);
  await prompt.flush();
  try {
    prompt.stream.end();
  } catch {}
}

async function onPrompt(stream) {
  const open = stream.open;
  const long = typeof open.text !== 'string';
  if (long) stream.accept({});
  const text = long ? await readText(stream, open.length) : open.text;
  if (text === null) return stream.reset('EPROTO: length mismatch');
  agent.prompts.push(text);
  if (agent.busy && open.whenBusy === 'reject') return stream.reset('EBUSY');
  const script = agent.scripts.shift() ?? { steps: [{ event: { t: 'text', id: `a${agent.users + 1}`, delta: 'ok' } }] };
  if (script.reset) return stream.reset(script.reset);
  let delivered = 'run';
  if (agent.busy) delivered = open.whenBusy === 'steer' ? 'steer' : 'follow-up';
  const prompt = sink(stream, Boolean(open.thinking));
  if (long) prompt.send({ t: 'delivered', delivered });
  else stream.accept({ delivered });
  agent.users += 1;
  const wasBusy = agent.busy;
  setBusy(true);
  emit({ t: 'user', id: `u${agent.users}`, text, delivered }, prompt);
  await runScript(prompt, script);
  setBusy(wasBusy);
}

function onWatch(stream) {
  const open = stream.open;
  const order = entryOrder();
  const opened = { head: order[order.length - 1] ?? '' };
  const busy = Boolean(open.busy) && agent.reportsBusy;
  if (busy) opened.busy = busyNow();
  let events = [];
  if (open.since) {
    const replayed = replay(open.since);
    if (replayed === null) opened.gap = true;
    else events = replayed;
  }
  stream.accept(opened);
  const w = sink(stream, Boolean(open.thinking));
  w.busy = busy;
  for (const e of events) w.send(e);
  agent.watches.add(w);
  stream.done.then(() => agent.watches.delete(w), () => agent.watches.delete(w));
  (async () => {
    try {
      while (await stream.read());
    } catch {}
  })();
}

function onAbort(stream) {
  agent.aborts += 1;
  const aborted = agent.busy;
  if (agent.hold) {
    const release = agent.hold;
    agent.hold = null;
    release('aborted');
  }
  stream.accept({ aborted });
  stream.end();
}

export function onAgent(stream) {
  agent.opens.push({ ...stream.open, text: undefined, textLength: stream.open?.text?.length });
  if (!agent.allow) return stream.reset('EACCES');
  switch (stream.open.op) {
    case 'prompt':
      return void onPrompt(stream).catch(() => {});
    case 'watch':
      return onWatch(stream);
    case 'abort':
      return onAbort(stream);
    default:
      return stream.reset('EINVAL: unknown op');
  }
}

export const agentTest = {
  configure({ allow, busy, reportsBusy } = {}) {
    if (allow !== undefined) agent.allow = allow;
    if (reportsBusy !== undefined) agent.reportsBusy = reportsBusy;
    if (busy !== undefined) setBusy(busy);
    return this.state();
  },
  scoop(name, working) {
    if (working) agent.scoops.add(name);
    else agent.scoops.delete(name);
    busyChanged();
    return agent.scoops.size;
  },
  queue(script) {
    agent.scripts.push(script);
    return agent.scripts.length;
  },
  emit(events) {
    for (const e of events) emit(e, null);
    return agent.log.length;
  },
  clear() {
    agent.log = [];
    return 0;
  },
  release() {
    const release = agent.hold;
    agent.hold = null;
    if (release) release('released');
    return Boolean(release);
  },
  resetWatches(reason = 'ECONNRESET') {
    const n = agent.watches.size;
    for (const w of agent.watches) w.stream.reset(reason);
    agent.watches.clear();
    return n;
  },
  state() {
    return {
      allow: agent.allow,
      busy: agent.busy,
      opens: agent.opens,
      prompts: agent.prompts,
      aborts: agent.aborts,
      watches: agent.watches.size,
      held: Boolean(agent.hold),
      scoops: agent.scoops.size,
      log: agent.log.length,
    };
  },
};
