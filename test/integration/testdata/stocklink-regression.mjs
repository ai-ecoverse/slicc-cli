import { createServer } from 'node:http';
import puppeteer from 'puppeteer-core';
import { WebSocket, WebSocketServer } from 'ws';

const [upstream, chrome, ordinary, cross, seven, mode, extensionFrame] = process.argv.slice(2);
const LIMIT = 64 * 1024 * 1024;
const CONNECT_MS = 10000;
const STEP_MS = 15000;
const PROBE_KEEP = 2000;
const HOLD_MS = 11000;
const started = Date.now();
const result = { steps: [] };
const traces = {};
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function scrub(url) {
  if (typeof url !== 'string') return url;
  return url.split(seven).join('SEVEN').split(ordinary).join('ORD').split(cross).join('XORD').split(extensionFrame).join('EXT');
}

function short(id) {
  return typeof id === 'string' ? id.slice(0, 8) : id;
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
    const message = String(error?.message ?? error);
    result.steps.push({ name, ok: false, ms: Date.now() - at, error: scrub(message) });
    return { ok: false, error: message };
  }
}

const KEEP_EVENTS = new Set([
  'Target.targetCreated',
  'Target.targetDestroyed',
  'Target.targetInfoChanged',
  'Target.attachedToTarget',
  'Target.detachedFromTarget',
  'Target.targetCrashed',
  'Page.frameAttached',
  'Page.frameDetached',
  'Page.frameNavigated',
  'Runtime.executionContextCreated',
  'Runtime.executionContextDestroyed',
  'Runtime.executionContextsCleared',
  'Inspector.detached',
]);

function tracer(label) {
  const lines = [];
  const asked = new Map();
  const raw = [];
  traces[label] = { lines, raw };
  const t = () => `+${String(Date.now() - started).padStart(6)}`;
  return {
    out(text) {
      let m;
      try {
        m = JSON.parse(text);
      } catch {
        return;
      }
      asked.set(m.id, { method: m.method, at: Date.now(), session: m.sessionId });
      const on = m.sessionId ? ` on ${short(m.sessionId)}` : '';
      const extra = m.method === 'Target.attachToTarget' || m.method === 'Target.getTargetInfo' ? ` target ${short(m.params?.targetId)}` : '';
      lines.push(`${t()} > ${m.method} #${m.id}${on}${extra}`);
    },
    in(text) {
      let m;
      try {
        m = JSON.parse(text);
      } catch {
        return;
      }
      raw.push(m);
      const on = m.sessionId ? ` on ${short(m.sessionId)}` : '';
      if (m.id !== undefined) {
        const was = asked.get(m.id);
        const ms = was ? ` ${Date.now() - was.at}ms` : '';
        const err = m.error ? ` ERR ${m.error.code} ${scrub(m.error.message)}` : '';
        const session = m.result?.sessionId ? ` session ${short(m.result.sessionId)}` : '';
        lines.push(`${t()} < #${m.id}${on} ${was?.method ?? '?'}${ms}${err}${session}`);
        return;
      }
      if (!KEEP_EVENTS.has(m.method)) return;
      const p = m.params ?? {};
      const info = p.targetInfo;
      let detail = '';
      if (info) detail = ` ${info.type} ${short(info.targetId)} ${scrub(info.url)}${info.parentFrameId ? ` parentFrame ${short(info.parentFrameId)}` : ''}${info.attached ? ' attached' : ''}`;
      if (p.sessionId && m.method !== 'Target.attachedToTarget') detail = ` session ${short(p.sessionId)}${p.targetId ? ` target ${short(p.targetId)}` : ''}${detail}`;
      if (m.method === 'Target.attachedToTarget') detail = ` session ${short(p.sessionId)}${detail}${p.waitingForDebugger ? ' waiting' : ''}`;
      if (m.method === 'Target.targetDestroyed' || m.method === 'Target.targetCrashed') detail = ` target ${short(p.targetId)}`;
      if (p.frame) detail = ` frame ${short(p.frame.id)} parent ${short(p.frame.parentId) ?? '-'} ${scrub(p.frame.url)}`;
      if (m.method === 'Page.frameAttached') detail = ` frame ${short(p.frameId)} parent ${short(p.parentFrameId)}`;
      if (m.method === 'Page.frameDetached') detail = ` frame ${short(p.frameId)} ${p.reason ?? ''}`;
      if (p.context) detail = ` ctx ${p.context.id} frame ${short(p.context.auxData?.frameId)} ${p.context.auxData?.isDefault ? 'main' : (p.context.name || 'world')} ${scrub(p.context.origin)}`;
      if (m.method === 'Runtime.executionContextDestroyed') detail = ` ctx ${p.executionContextId}`;
      lines.push(`${t()} < ${m.method}${on}${detail}`);
    },
  };
}

function openUpstream(trace) {
  const ws = new WebSocket(upstream, { maxPayload: LIMIT, perMessageDeflate: false });
  const queue = [];
  const send = (text) => {
    trace?.out(text);
    if (ws.readyState === WebSocket.OPEN) ws.send(text);
    else if (ws.readyState === WebSocket.CONNECTING) queue.push(text);
  };
  ws.on('open', () => {
    for (const text of queue.splice(0)) ws.send(text);
  });
  return { ws, send };
}

class Raw {
  constructor(url, label, guarded) {
    this.label = label;
    this.trace = label ? tracer(label) : null;
    this.events = [];
    this.waiters = new Map();
    this.next = 1;
    if (guarded) {
      const { ws, send } = openUpstream(this.trace);
      this.ws = ws;
      this.write = send;
    } else {
      this.ws = new WebSocket(url, { maxPayload: LIMIT, perMessageDeflate: false });
      this.write = (text) => this.ws.send(text);
    }
    this.ready = new Promise((resolve, reject) => {
      this.ws.once('open', resolve);
      this.ws.once('error', reject);
    });
    this.ws.on('error', () => {});
    this.ws.on('message', (data) => {
      const text = data.toString();
      this.trace?.in(text);
      const m = JSON.parse(text);
      if (m.id !== undefined && this.waiters.has(m.id)) {
        this.waiters.get(m.id)(m);
        this.waiters.delete(m.id);
        return;
      }
      if (m.method) this.events.push({ ...m, at: Date.now() });
    });
  }
  async send(method, params = {}, sessionId) {
    await this.ready;
    const id = this.next++;
    const message = { id, method, params };
    if (sessionId) message.sessionId = sessionId;
    const answer = new Promise((resolve) => this.waiters.set(id, resolve));
    this.write(JSON.stringify(message));
    return answer;
  }
  async call(method, params, sessionId) {
    const m = await within(STEP_MS, () => this.send(method, params, sessionId), `${this.label ?? 'observer'} ${method}`);
    if (m.error) throw new Error(`${method}: ${m.error.message}`);
    return m.result;
  }
  close() {
    this.ws.close();
  }
}

async function shortCall(method, params) {
  const c = new Raw(null, null, true);
  try {
    return await c.call(method, params);
  } finally {
    c.close();
  }
}

let clientCount = 0;
function browserSocket(client) {
  clientCount += 1;
  const trace = tracer(`puppeteer-${clientCount}`);
  const { ws, send } = openUpstream(trace);
  ws.on('message', (data) => {
    const text = data.toString();
    trace.in(text);
    if (client.readyState === WebSocket.OPEN) client.send(text);
  });
  ws.on('close', () => client.close(1011));
  ws.on('error', () => client.terminate());
  client.on('message', (data) => send(data.toString()));
  client.on('close', () => ws.close());
}

async function discovery(req, res) {
  const host = req.headers.host;
  const url = new URL(req.url, `http://${host}`);
  if (url.pathname.startsWith('/json/version')) {
    const version = await shortCall('Browser.getVersion', {});
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify({
      Browser: version.product,
      'Protocol-Version': version.protocolVersion,
      'User-Agent': version.userAgent,
      'V8-Version': version.jsVersion,
      'WebKit-Version': '',
      webSocketDebuggerUrl: `ws://${host}/devtools/browser/stock`,
    }));
    return;
  }
  res.writeHead(404);
  res.end('missing');
}

const proxy = createServer((req, res) => {
  discovery(req, res).catch((error) => {
    res.writeHead(500);
    res.end(String(error?.message ?? error));
  });
});
const wss = new WebSocketServer({ noServer: true, maxPayload: LIMIT, perMessageDeflate: false });
proxy.on('upgrade', (req, socket, head) => {
  wss.handleUpgrade(req, socket, head, (client) => browserSocket(client));
});
await new Promise((resolve) => proxy.listen(0, '127.0.0.1', resolve));
const endpoint = `http://127.0.0.1:${proxy.address().port}`;

const version = await (await fetch(`${chrome}/json/version`)).json();
const observer = new Raw(version.webSocketDebuggerUrl, null, false);
await observer.ready;

async function observerTargets() {
  return (await observer.call('Target.getTargets')).targetInfos;
}

async function observerSession(targetId) {
  return (await observer.call('Target.attachToTarget', { targetId, flatten: true })).sessionId;
}

async function observerEval(sessionId, expression) {
  const r = await observer.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }, sessionId);
  if (r.exceptionDetails) throw new Error(`observer: ${r.exceptionDetails.text}`);
  return r.result.value;
}

function protectedURL(url) {
  return typeof url === 'string' && (url.startsWith(seven) || url.startsWith('chrome-extension://'));
}

function pendingTabs(label) {
  const raw = traces[label]?.raw ?? [];
  const tabs = new Map();
  const children = new Set();
  for (const m of raw) {
    if (m.method !== 'Target.attachedToTarget') continue;
    const info = m.params.targetInfo;
    if (info.type === 'tab' && !m.sessionId) tabs.set(m.params.sessionId, info.targetId);
    if (m.sessionId && tabs.has(m.sessionId)) children.add(m.sessionId);
  }
  const destroyed = new Set(raw.filter((m) => m.method === 'Target.targetDestroyed').map((m) => m.params.targetId));
  const pages = new Map();
  for (const m of raw) {
    if (m.method === 'Target.targetCreated' && m.params.targetInfo.type === 'page') pages.set(m.params.targetInfo.targetId, scrub(m.params.targetInfo.url));
  }
  const attachedPages = new Set(raw.filter((m) => m.method === 'Target.attachedToTarget').map((m) => m.params.targetInfo.targetId));
  return {
    tabsWithoutPage: [...tabs].filter(([s, id]) => !children.has(s) && !destroyed.has(id)).map(([s, id]) => `${short(id)} session ${short(s)}`),
    pagesNeverAttached: [...pages].filter(([id]) => !attachedPages.has(id) && !destroyed.has(id)).map(([id, url]) => `${short(id)} ${url}`),
  };
}

async function connect(label) {
  const at = Date.now();
  const connected = await step(`${label} connect`, () => puppeteer.connect({ browserURL: endpoint, protocolTimeout: STEP_MS }), CONNECT_MS);
  const trace = `puppeteer-${clientCount}`;
  result[label] = { ok: connected.ok, ms: Date.now() - at, trace, atConnect: pendingTabs(trace) };
  if (!connected.ok) result[label].error = scrub(connected.error);
  return connected.ok ? connected.value : null;
}

const n = Math.random().toString(36).slice(2, 8);
const CHILDREN = { framed: ['framed'], nest: ['same', 'cross', 'nested'] };

function kindOf(url) {
  if (url.includes('/framed-same')) return 'framed';
  if (url.includes('/nest')) return 'nest';
  return null;
}

function childFrame(page, k) {
  return page.frames().find((f) => f.url().includes('/inner') && f.url().includes(`k=${k}`));
}

async function readPreexisting(label, browser) {
  const out = {};
  const pages = (await browser.pages()).filter((p) => p.url().includes('pre=1'));
  for (const page of pages) {
    const kind = kindOf(page.url());
    if (!kind) continue;
    const row = { url: scrub(page.url()), frames: {} };
    out[kind] = row;
    for (const k of CHILDREN[kind]) {
      const found = await step(`${label} ${kind} frame ${k} reaches the client`, async () => {
        for (;;) {
          const f = childFrame(page, k);
          if (f) return f;
          await pause(100);
        }
      });
      if (!found.ok) {
        row.frames[k] = { found: false, frames: page.frames().map((f) => scrub(f.url())) };
        continue;
      }
      const read = await step(`${label} ${kind} frame ${k} $eval`, () => found.value.$eval('#ibtn', (el) => el.textContent));
      row.frames[k] = { found: true, read: read.ok ? read.value : null, error: read.ok ? null : scrub(read.error) };
    }
  }
  return out;
}

async function connectAndRead(label) {
  const browser = await connect(label);
  if (!browser) return null;
  const r = result[label];
  const pages = await step(`${label} pages()`, async () => (await browser.pages()).map((p) => p.url()));
  r.pages = pages.ok ? pages.value.map(scrub) : null;
  r.pagesError = pages.ok ? null : scrub(pages.error);
  r.targets = browser.targets().map((t) => `${t.type()} ${scrub(t.url())}`);
  r.protectedVisible = [...(pages.ok ? pages.value : []), ...browser.targets().map((t) => t.url())].filter(protectedURL).map(scrub);
  if (mode === 'connect' && pages.ok) r.preexisting = await readPreexisting(label, browser);
  return browser;
}

const probes = {};
result.probes = probes;

async function probePages() {
  const probe = new Raw(null, 'probe', true);
  const probeReady = await step('probe client', () => probe.call('Target.getTargets'));
  if (!probeReady.ok) return probe;
  const listed = probeReady.value.targetInfos;
  probes.listed = listed.map((i) => `${i.type} ${scrub(i.url)}`);
  const framesOf = (tree) => {
    const out = [];
    const walk = (node) => {
      if (!node) return;
      out.push(`${short(node.frame.id)}<${short(node.frame.parentId) ?? '-'} ${scrub(node.frame.url)}`);
      for (const c of node.childFrames ?? []) walk(c);
    };
    walk(tree);
    return out;
  };
  const once = async (targetId, sessionId) => {
    const timed = async (method, params, s) => {
      const at = Date.now();
      const m = await within(STEP_MS, () => probe.send(method, params, s), method).catch((e) => ({ error: { code: 'timeout', message: e.message } }));
      return { ms: Date.now() - at, m };
    };
    const [info, tree, history] = await Promise.all([
      timed('Target.getTargetInfo', { targetId }),
      timed('Page.getFrameTree', {}, sessionId),
      timed('Page.getNavigationHistory', {}, sessionId),
    ]);
    const show = ({ ms, m }, pick) => ({ ms, error: m.error ? `${m.error.code} ${scrub(m.error.message)}` : null, value: m.error ? null : pick(m.result) });
    return {
      info: show(info, (r) => `${r.targetInfo?.type} ${scrub(r.targetInfo?.url)}`),
      tree: show(tree, (r) => framesOf(r.frameTree)),
      history: show(history, (r) => `${r.currentIndex}/${r.entries?.length} ${scrub(r.entries?.[r.currentIndex]?.url)}`),
    };
  };
  const wanted = [['framed-same pre', (u) => u.includes('/framed-same') && u.includes('pre=1')], ['nest pre', (u) => u.includes('/nest') && u.includes('pre=1')]];
  for (const [name, test] of wanted) {
    const targetId = listed.find((i) => i.type === 'page' && test(i.url))?.targetId;
    if (!targetId) {
      probes[name] = { listed: false };
      continue;
    }
    const attached = await step(`probe attach ${name}`, () => probe.call('Target.attachToTarget', { targetId, flatten: true }));
    if (!attached.ok) {
      probes[name] = { listed: true, attach: scrub(attached.error) };
      continue;
    }
    const sessionId = attached.value.sessionId;
    const first = await once(targetId, sessionId);
    const enable = await step(`probe enable ${name}`, async () => {
      await probe.call('Page.enable', {}, sessionId);
      await probe.call('Runtime.enable', {}, sessionId);
    });
    await pause(PROBE_KEEP);
    const contexts = probe.events.filter((e) => e.sessionId === sessionId && e.method === 'Runtime.executionContextCreated').map((e) => `${short(e.params.context.auxData?.frameId)} ${e.params.context.auxData?.isDefault ? 'main' : 'world'} ${scrub(e.params.context.origin)}`);
    const world = await step(`probe createIsolatedWorld in every child frame of ${name}`, async () => {
      const os = await observerSession(targetId);
      const tree = (await observer.call('Page.getFrameTree', {}, os)).frameTree;
      const ids = [];
      const walk = (node) => {
        for (const c of node.childFrames ?? []) {
          ids.push(c.frame.id);
          walk(c);
        }
      };
      walk(tree);
      const out = [];
      for (const frameId of ids) {
        const at = Date.now();
        const m = await within(STEP_MS, () => probe.send('Page.createIsolatedWorld', { frameId, worldName: 'probe' }, sessionId), 'createIsolatedWorld').catch((e) => ({ error: { code: 'timeout', message: e.message } }));
        out.push({ frame: short(frameId), ms: Date.now() - at, error: m.error ? `${m.error.code} ${scrub(m.error.message)}` : null });
      }
      return out;
    });
    const second = await once(targetId, sessionId);
    const os = await observerSession(targetId);
    const truth = framesOf((await observer.call('Page.getFrameTree', {}, os)).frameTree);
    probes[name] = { listed: true, first, enable: enable.ok ? 'ok' : scrub(enable.error), contexts, worlds: world.ok ? world.value : scrub(world.error), second, chrome: truth };
  }
  return probe;
}

const browser = await connectAndRead('connect');
const probe = await probePages();
if (browser) await step('disconnect', () => browser.disconnect());
const again = await connectAndRead('reconnect');
if (again) {
  await pause(HOLD_MS);
  await step('reconnect disconnect', () => again.disconnect());
}

for (const label of ['connect', 'reconnect']) if (result[label]) Object.assign(result[label], pendingTabs(result[label].trace));
for (const [label, { lines }] of Object.entries(traces)) {
  for (const line of lines) console.log(`SEQ ${label} ${line}`);
}
console.log(`RESULT ${JSON.stringify(result)}`);
probe.close();
observer.close();
proxy.close();
process.exit(0);
