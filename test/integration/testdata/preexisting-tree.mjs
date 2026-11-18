import { WebSocket } from 'ws';

const [upstream, ordinary, seven] = process.argv.slice(2);
const LIMIT = 64 * 1024 * 1024;
const STEP_MS = 8000;
const FIRST_MS = 300;
const result = { steps: [], pages: {} };

function scrub(url) {
  if (typeof url !== 'string') return url;
  return url.split(seven).join('SEVEN').split(ordinary).join('ORD');
}

function within(ms, work, what) {
  let timer;
  return Promise.race([
    Promise.resolve().then(work),
    new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${what} took over ${ms} ms`)), ms);
    }),
  ]).finally(() => clearTimeout(timer));
}

async function step(name, work, ms = STEP_MS) {
  const at = Date.now();
  try {
    const value = await within(ms, work, name);
    result.steps.push({ name, ok: true, ms: Date.now() - at });
    return { ok: true, value };
  } catch (error) {
    result.steps.push({ name, ok: false, ms: Date.now() - at, error: scrub(String(error?.message ?? error)) });
    return { ok: false, error: scrub(String(error?.message ?? error)) };
  }
}

function matchPage(info, name) {
  const u = info?.url || '';
  if (info?.type !== 'page') return false;
  switch (name) {
    case 'framed':
      return u.includes('/framed-same') && u.includes('pre=1');
    case 'nest':
      return u.includes('/nest') && u.includes('pre=1');
    case 'blank':
      return u === 'about:blank';
    case 'protected':
      return u.includes('/framed-seven') && u.includes('pre=1');
    default:
      return false;
  }
}

function childrenOk(name, frames) {
  if (!frames) return false;
  switch (name) {
    case 'framed':
      return frames.some((f) => f.url.includes('/inner'));
    case 'nest':
      return frames.some((f) => f.url.includes('/inner') && f.url.includes('k=nested'));
    case 'blank':
      return frames.length >= 1;
    default:
      return false;
  }
}

function framesOf(tree) {
  const out = [];
  const walk = (node) => {
    if (!node) return;
    out.push({ id: node.frame?.id, parentId: node.frame?.parentId || '', url: scrub(node.frame?.url || '') });
    for (const c of node.childFrames ?? []) walk(c);
  };
  walk(tree);
  return out;
}

class Raw {
  constructor() {
    this.waiters = new Map();
    this.next = 1;
    this.events = [];
    this.ws = new WebSocket(upstream, { maxPayload: LIMIT, perMessageDeflate: false });
    this.ready = new Promise((resolve, reject) => {
      this.ws.once('open', resolve);
      this.ws.once('error', reject);
    });
    this.ws.on('error', () => {});
    this.ws.on('message', (data) => this.onMessage(data.toString()));
  }
  fire(method, params = {}, sessionId) {
    const id = this.next++;
    const message = { id, method, params };
    if (sessionId) message.sessionId = sessionId;
    this.ws.send(JSON.stringify(message));
  }
  async send(method, params = {}, sessionId) {
    await this.ready;
    const id = this.next++;
    const message = { id, method, params };
    if (sessionId) message.sessionId = sessionId;
    const answer = new Promise((resolve) => this.waiters.set(id, resolve));
    this.ws.send(JSON.stringify(message));
    return answer;
  }
  async call(method, params, sessionId) {
    const m = await within(STEP_MS, () => this.send(method, params, sessionId), method);
    if (m.error) throw new Error(`${method}: ${m.error.code} ${m.error.message}`);
    return m.result;
  }
  onMessage(text) {
    const m = JSON.parse(text);
    if (m.id !== undefined && this.waiters.has(m.id)) {
      this.waiters.get(m.id)(m);
      this.waiters.delete(m.id);
      return;
    }
    if (m.method) this.events.push({ ...m, at: Date.now() });
  }
  waitPageUnderTab(name, tabSession, ms = 20000) {
    const at = Date.now();
    const seen = new Set();
    return new Promise((resolve, reject) => {
      const tick = () => {
        for (let i = 0; i < this.events.length; i++) {
          if (seen.has(i)) continue;
          const e = this.events[i];
          if (e.method !== 'Target.attachedToTarget') continue;
          if (e.sessionId !== tabSession) continue;
          const info = e.params?.targetInfo;
          if (!matchPage(info, name)) continue;
          seen.add(i);
          resolve({
            sessionId: e.params.sessionId,
            targetId: info.targetId,
            url: info.url,
            under: e.sessionId || '',
            waiting: !!e.params.waitingForDebugger,
          });
          return;
        }
        if (Date.now() - at > ms) {
          reject(new Error(`page ${name} did not attach under tab session`));
          return;
        }
        setTimeout(tick, 10);
      };
      tick();
    });
  }
  close() {
    this.ws.close();
  }
}

async function firstCommands(c, targetId, sessionId) {
  const timed = async (method, params, s) => {
    const at = Date.now();
    const m = await within(FIRST_MS, () => c.send(method, params, s), method).catch((e) => ({ error: { code: 'timeout', message: e.message } }));
    return {
      ms: Date.now() - at,
      ok: !m.error,
      error: m.error ? `${m.error.code} ${scrub(m.error.message)}` : null,
      result: m.error ? null : m.result,
    };
  };
  const [info, tree, history, resource] = await Promise.all([
    timed('Target.getTargetInfo', { targetId }, sessionId),
    timed('Page.getFrameTree', {}, sessionId),
    timed('Page.getNavigationHistory', {}, sessionId),
    timed('Page.getResourceTree', {}, sessionId),
  ]);
  return {
    info: { ms: info.ms, ok: info.ok, error: info.error, value: info.result ? `${info.result.targetInfo?.type} ${scrub(info.result.targetInfo?.url)}` : null },
    tree: { ms: tree.ms, ok: tree.ok, error: tree.error, frames: tree.result ? framesOf(tree.result.frameTree) : null },
    history: {
      ms: history.ms,
      ok: history.ok,
      error: history.error,
      value: history.result
        ? `${history.result.currentIndex}/${history.result.entries?.length} ${scrub(history.result.entries?.[history.result.currentIndex]?.url)}`
        : null,
    },
    resource: { ms: resource.ms, ok: resource.ok, error: resource.error, frames: resource.result ? framesOf(resource.result.frameTree) : null },
  };
}

function summarize(name, hit, first) {
  return {
    attached: true,
    underTab: hit.under !== '',
    waiting: hit.waiting,
    url: scrub(hit.url),
    first,
    childrenOk: name === 'protected' ? null : childrenOk(name, first.tree.frames),
    refused: !!(first.tree.error && String(first.tree.error).includes("seven's origin")),
  };
}

const client = new Raw();
await client.ready;
await step('discover', () => client.call('Target.setDiscoverTargets', { discover: true }));

const listed = await step('getTargets', () => client.call('Target.getTargets'));
const tabs = listed.ok ? (listed.value.targetInfos || []).filter((i) => i.type === 'tab') : [];
result.tabCount = tabs.length;

const wanted = ['framed', 'nest', 'blank', 'protected'];
const pending = new Map(wanted.map((name) => [name, true]));

for (const tab of tabs) {
  if (pending.size === 0) break;
  const attached = await step(`attach tab ${tab.targetId.slice(0, 8)}`, () => client.call('Target.attachToTarget', { targetId: tab.targetId, flatten: true }));
  if (!attached.ok) continue;
  const tabSession = attached.value.sessionId;
  await step(`tab auto-attach ${tab.targetId.slice(0, 8)}`, () =>
    client.call('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true }, tabSession)
  );
  client.fire('Runtime.runIfWaitingForDebugger', {}, tabSession);
  for (const name of [...pending.keys()]) {
    const page = await step(`wait page ${name} under tab`, () => client.waitPageUnderTab(name, tabSession, 3000), 4000);
    if (!page.ok) continue;
    pending.delete(name);
    const hit = page.value;
    const atAttach = await firstCommands(client, hit.targetId, hit.sessionId);
    if (hit.waiting) {
      await step(`resume page ${name}`, () => client.call('Runtime.runIfWaitingForDebugger', {}, hit.sessionId));
    } else {
      client.fire('Runtime.runIfWaitingForDebugger', {}, hit.sessionId);
    }
    const afterResume = await firstCommands(client, hit.targetId, hit.sessionId);
    result.pages[name] = {
      ...summarize(name, hit, atAttach),
      afterResume: summarize(name, hit, afterResume).first,
      afterResumeChildrenOk: name === 'protected' ? null : childrenOk(name, afterResume.tree.frames),
    };
  }
}

for (const name of pending.keys()) {
  result.pages[name] = { attached: false, error: 'no tab delivered this page under a tab session' };
}

client.close();
console.log(`RESULT ${JSON.stringify(result)}`);
process.exit(0);
