import { WebSocket } from 'ws';
import puppeteer from 'puppeteer-core';

const [upstream, ordinary, seven, chrome, mode = 'all'] = process.argv.slice(2);
const LIMIT = 64 * 1024 * 1024;
const STEP_MS = 8000;
const FIRST_MS = 300;
const CONNECT_MS = 5000;
const ALL = [{}];
const BROWSER_ATTACH = {
  autoAttach: true,
  waitForDebuggerOnStart: true,
  flatten: true,
  filter: [{ type: 'page', exclude: true }, ...ALL],
};
const TAB_ATTACH = { autoAttach: true, waitForDebuggerOnStart: true, flatten: true, filter: ALL };
const result = { steps: [], pages: {}, puppeteer: null, events: { methods: {}, attached: [], created: [] } };

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
      return frames.some((f) => f.url.includes('/inner') && f.url.includes('k=nested')) && frames.some((f) => f.url === 'about:blank' || f.parentId);
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

function innerFrameId(frames) {
  const hit = (frames || []).find((f) => f.url.includes('/inner'));
  return hit?.id || '';
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
    const answer = new Promise((resolve) => this.waiters.set(id, resolve));
    this.ws.send(JSON.stringify(message));
    return answer;
  }
  async send(method, params = {}, sessionId) {
    await this.ready;
    return this.fire(method, params, sessionId);
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
    if (m.method) {
      this.events.push({ ...m, at: Date.now() });
      result.events.methods[m.method] = (result.events.methods[m.method] || 0) + 1;
      if (m.method === 'Target.targetCreated' && m.params?.targetInfo) {
        const info = m.params.targetInfo;
        if (result.events.created.length < 32) {
          result.events.created.push({ type: info.type, id: (info.targetId || '').slice(0, 8), url: scrub(info.url || ''), attached: !!info.attached });
        }
      }
      if (m.method === 'Target.attachedToTarget' && m.params?.targetInfo) {
        const info = m.params.targetInfo;
        if (result.events.attached.length < 32) {
          result.events.attached.push({
            type: info.type,
            id: (info.targetId || '').slice(0, 8),
            url: scrub(info.url || ''),
            session: (m.params.sessionId || '').slice(0, 8),
            under: (m.sessionId || '').slice(0, 8),
            waiting: !!m.params.waitingForDebugger,
          });
        }
      }
    }
  }
  close() {
    this.ws.close();
  }
}

async function biosPipeline(c, targetId, sessionId) {
  const at = Date.now();
  const info1P = c.fire('Target.getTargetInfo', { targetId }, sessionId);
  const enableP = c.fire('Page.enable', {}, sessionId);
  const treeP = c.fire('Page.getFrameTree', {}, sessionId);
  const info2P = c.fire('Target.getTargetInfo', { targetId }, sessionId);
  const timed = async (p, label) => {
    const m = await within(FIRST_MS, () => p, label).catch((e) => ({ error: { code: 'timeout', message: e.message } }));
    return {
      ms: Date.now() - at,
      ok: !m.error,
      error: m.error ? `${m.error.code} ${scrub(m.error.message)}` : null,
      result: m.error ? null : m.result,
    };
  };
  const [info1, enable, tree, info2] = await Promise.all([
    timed(info1P, 'Target.getTargetInfo'),
    timed(enableP, 'Page.enable'),
    timed(treeP, 'Page.getFrameTree'),
    timed(info2P, 'Target.getTargetInfo#2'),
  ]);
  return {
    elapsed: Date.now() - at,
    info: { ms: info1.ms, ok: info1.ok, error: info1.error, value: info1.result ? `${info1.result.targetInfo?.type} ${scrub(info1.result.targetInfo?.url)}` : null },
    enable: { ms: enable.ms, ok: enable.ok, error: enable.error },
    tree: { ms: tree.ms, ok: tree.ok, error: tree.error, frames: tree.result ? framesOf(tree.result.frameTree) : null },
    info2: { ms: info2.ms, ok: info2.ok, error: info2.error },
  };
}

async function isolatedWorld(c, sessionId, frameId) {
  const at = Date.now();
  const m = await within(FIRST_MS, () => c.send('Page.createIsolatedWorld', { frameId, worldName: 'slicc-preexisting', grantUniveralAccess: true }, sessionId), 'Page.createIsolatedWorld').catch((e) => ({
    error: { code: 'timeout', message: e.message },
  }));
  return {
    ms: Date.now() - at,
    ok: !m.error,
    error: m.error ? `${m.error.code} ${scrub(m.error.message)}` : null,
    value: m.result ? m.result.executionContextId : null,
  };
}

const client = new Raw();
await client.ready;
await step('getBrowserContexts', () => client.call('Target.getBrowserContexts', {}));
await step('discover', () => client.call('Target.setDiscoverTargets', { discover: true, filter: ALL }));
await step('browser auto-attach', () => client.call('Target.setAutoAttach', BROWSER_ATTACH));

const wanted = mode === 'protected' ? ['protected'] : mode === 'ordinary' ? ['framed', 'nest', 'blank'] : ['framed', 'nest', 'blank', 'protected'];
const pending = new Map(wanted.map((name) => [name, true]));
const runPuppeteer = !!chrome && mode !== 'protected';
const tabs = new Map();
const tabSessions = new Set();
const seenAttach = new Set();
result.tabs = [];

const waitForTabEvent = async (ms = 5000) => {
  const at = Date.now();
  while (Date.now() - at < ms) {
    for (const e of client.events) {
      if (e.method === 'Target.targetCreated' && e.params?.targetInfo?.type === 'tab') return true;
      if (e.method === 'Target.attachedToTarget' && e.params?.targetInfo?.type === 'tab') return true;
    }
    await new Promise((r) => setTimeout(r, 20));
  }
  return false;
};

await step('wait tab targets from events', async () => {
  if (!(await waitForTabEvent(10000))) throw new Error('no Target.targetCreated/attachedToTarget tab from events after discover+auto-attach');
});

const ingest = async () => {
  for (let i = 0; i < client.events.length; i++) {
    const e = client.events[i];
    if (e.method === 'Target.targetCreated' && e.params?.targetInfo?.type === 'tab') {
      const info = e.params.targetInfo;
      if (!tabs.has(info.targetId)) {
        tabs.set(info.targetId, info);
        result.tabs.push({ id: info.targetId.slice(0, 8), url: scrub(info.url || '') });
      }
    }
    if (e.method !== 'Target.attachedToTarget' || seenAttach.has(i)) continue;
    const info = e.params?.targetInfo;
    if (!info) continue;
    seenAttach.add(i);
    if (info.type === 'tab' && !e.sessionId) {
      const tabSession = e.params.sessionId;
      tabs.set(info.targetId, info);
      if (!tabSessions.has(tabSession)) {
        tabSessions.add(tabSession);
        await step(`tab auto-attach ${info.targetId.slice(0, 8)}`, () => client.call('Target.setAutoAttach', TAB_ATTACH, tabSession));
        client.fire('Runtime.runIfWaitingForDebugger', {}, tabSession);
      }
      continue;
    }
    if (info.type !== 'page' || !e.sessionId) continue;
    let name = null;
    for (const n of pending.keys()) {
      if (matchPage(info, n)) {
        name = n;
        break;
      }
    }
    if (!name) continue;
    pending.delete(name);
    const hit = {
      sessionId: e.params.sessionId,
      targetId: info.targetId,
      url: info.url,
      under: e.sessionId || '',
      waiting: !!e.params.waitingForDebugger,
    };
    const pipeline = await biosPipeline(client, hit.targetId, hit.sessionId);
    if (hit.waiting) {
      await step(`resume page ${name}`, () => client.call('Runtime.runIfWaitingForDebugger', {}, hit.sessionId));
    } else {
      client.fire('Runtime.runIfWaitingForDebugger', {}, hit.sessionId);
    }
    const worldFrame = innerFrameId(pipeline.tree.frames);
    const world = worldFrame ? await isolatedWorld(client, hit.sessionId, worldFrame) : { ms: 0, ok: false, error: 'no /inner frame in tree', value: null };
    result.pages[name] = {
      attached: true,
      underTab: hit.under !== '',
      waiting: hit.waiting,
      url: scrub(hit.url),
      pipeline,
      childrenOk: name === 'protected' ? null : childrenOk(name, pipeline.tree.frames),
      refused: !!(pipeline.tree.error && String(pipeline.tree.error).includes("seven's origin")),
      world,
    };
  }
};

const deadline = Date.now() + 20000;
while (pending.size > 0 && Date.now() < deadline) {
  await ingest();
  if (pending.size === 0) break;
  await new Promise((r) => setTimeout(r, 20));
}
await ingest();
result.tabCount = tabs.size;
result.tabSessions = tabSessions.size;

for (const name of pending.keys()) {
  result.pages[name] = { attached: false, error: 'no tab delivered this page under a tab session' };
}

if (runPuppeteer) {
  const at = Date.now();
  try {
    const browser = await within(CONNECT_MS, () => puppeteer.connect({ browserWSEndpoint: upstream, defaultViewport: null, protocolTimeout: CONNECT_MS }), 'puppeteer.connect');
    const pages = await within(CONNECT_MS, () => browser.pages(), 'browser.pages');
    const urls = pages.map((p) => scrub(p.url()));
    result.puppeteer = { ok: true, ms: Date.now() - at, pages: urls.length, urls, hasFramed: urls.some((u) => u.includes('/framed-same')), hasNest: urls.some((u) => u.includes('/nest')) };
    await browser.disconnect();
  } catch (error) {
    result.puppeteer = { ok: false, ms: Date.now() - at, error: scrub(String(error?.message ?? error)) };
  }
}

client.close();
console.log(`RESULT ${JSON.stringify(result)}`);
process.exit(0);
