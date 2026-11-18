import { WebSocket } from 'ws';
import puppeteer from 'puppeteer-core';

const [upstream, ordinary, seven, chrome] = process.argv.slice(2);
const LIMIT = 64 * 1024 * 1024;
const STEP_MS = 8000;
const FIRST_MS = 300;
const CONNECT_MS = 5000;
const result = { steps: [], pages: {}, puppeteer: null };

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
}

for (const name of pending.keys()) {
  result.pages[name] = { attached: false, error: 'no tab delivered this page under a tab session' };
}

if (chrome) {
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
