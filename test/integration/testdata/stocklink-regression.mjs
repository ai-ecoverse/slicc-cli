import { createServer } from 'node:http';
import puppeteer from 'puppeteer-core';
import { WebSocket, WebSocketServer } from 'ws';

const [upstream, chrome, ordinary, cross, seven, mode] = process.argv.slice(2);
const LIMIT = 64 * 1024 * 1024;
const CONNECT_MS = 10000;
const STEP_MS = 15000;
const PROBE_KEEP = 2000;
const started = Date.now();
const result = { steps: [] };
const traces = {};
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function scrub(url) {
  if (typeof url !== 'string') return url;
  return url.split(seven).join('SEVEN').split(ordinary).join('ORD').split(cross).join('XORD');
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
  return typeof url === 'string' && url.startsWith(seven);
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
  result[label] = { ok: connected.ok, ms: Date.now() - at, trace, ...pendingTabs(trace) };
  if (!connected.ok) result[label].error = scrub(connected.error);
  return connected.ok ? connected.value : null;
}

const n = Math.random().toString(36).slice(2, 8);
const nestURL = `${ordinary}/nest?n=${n}`;
const framedURL = `${ordinary}/framed-same?n=${n}`;

const browser = await connect('connect');
if (browser) {
  const pages = await step('pages()', async () => (await browser.pages()).map((p) => p.url()));
  result.connect.pages = pages.ok ? pages.value.map(scrub) : null;
  result.connect.pagesError = pages.ok ? null : scrub(pages.error);
  result.connect.targets = browser.targets().map((t) => `${t.type()} ${scrub(t.url())}`);
  result.connect.protectedVisible = [...(pages.ok ? pages.value : []), ...browser.targets().map((t) => t.url())].filter(protectedURL).map(scrub);
  const pre = pages.ok && mode === 'connect' ? (await browser.pages()).find((p) => p.url().includes('/framed-same?pre')) : null;
  result.preFramed = { found: Boolean(pre) };
  if (pre) {
    const frame = await step('pre-existing framed-same iframe frame', async () => {
      for (;;) {
        const f = pre.frames().find((x) => x.url().includes('/inner'));
        if (f) return f;
        await pause(100);
      }
    });
    if (frame.ok) {
      const read = await step('pre-existing framed-same iframe $eval', () => frame.value.$eval('#ibtn', (el) => el.textContent));
      result.preFramed.read = read.ok ? read.value : null;
      result.preFramed.error = read.ok ? null : scrub(read.error);
    }
  }
}

async function sevenFrames(tag) {
  const infos = await observerTargets();
  return infos.filter((i) => i.type === 'iframe' && protectedURL(i.url) && i.url.includes(`k=${tag}`) && i.url.includes(`n=${n}`));
}

async function installCounters(targetId) {
  const s = await observerSession(targetId);
  await observerEval(
    s,
    `(() => {
      if (!window.__hits) {
        window.__hits = { mouse: 0, touch: 0, key: 0, click: 0, text: 0 };
        for (const [type, field] of [['mousedown', 'mouse'], ['pointerdown', 'mouse'], ['touchstart', 'touch'], ['keydown', 'key'], ['click', 'click'], ['beforeinput', 'text']])
          addEventListener(type, () => { window.__hits[field] += 1; }, true);
        const i = document.createElement('input');
        i.id = 'sin';
        i.style.cssText = 'position:fixed;left:0;top:0;width:100%;height:100%;border:0';
        document.body.append(i);
      }
      return true;
    })()`
  );
  return s;
}

async function hitsIn(sessions) {
  const total = { mouse: 0, touch: 0, key: 0, click: 0, text: 0, value: '' };
  for (const s of sessions) {
    const h = await observerEval(s, '({ ...window.__hits, value: document.getElementById("sin").value })');
    for (const k of ['mouse', 'touch', 'key', 'click', 'text']) total[k] += h[k];
    total.value += h.value;
  }
  return total;
}

const delivered = (h) => h.mouse + h.touch + h.key + h.click + h.text;

if (browser && mode === 'nested') {
  const nested = { inputs: {} };
  result.nested = nested;
  const opened = await step('newPage', () => browser.newPage());
  const page = opened.ok ? opened.value : null;
  if (page) {
    const went = await step('goto nest', () => page.goto(nestURL, { waitUntil: 'load' }));
    nested.goto = went.ok ? 'ok' : scrub(went.error);
    const settled = await step('nest frames reach the client', async () => {
      for (;;) {
        const urls = page.frames().map((f) => f.url());
        if (urls.some((u) => u.includes('k=nested') && u.startsWith(ordinary)) && urls.some((u) => u.includes('k=same')) && urls.some((u) => u.startsWith(cross))) return urls;
        await pause(100);
      }
    });
    nested.frames = page.frames().map((f) => `${scrub(f.url())} parent ${scrub(f.parentFrame()?.url() ?? '-')}`);
    nested.protectedFrames = page.frames().map((f) => f.url()).filter(protectedURL).map(scrub);
    nested.framesSettled = settled.ok;
    const sevenTargets = await step('observer sees both seven frames', async () => {
      for (;;) {
        const [a, b] = [await sevenFrames('nested'), await sevenFrames('direct')];
        if (a.length && b.length) return { nestedFrame: a[0], direct: b[0] };
        await pause(100);
      }
    });
    if (sevenTargets.ok) {
      const { nestedFrame, direct } = sevenTargets.value;
      const nestedSession = await installCounters(nestedFrame.targetId);
      const directSession = await installCounters(direct.targetId);
      const pageInfo = (await observerTargets()).find((i) => i.type === 'page' && i.url === nestURL);
      const top = await observerSession(pageInfo.targetId);
      const boxes = await observerEval(
        top,
        `(() => {
          const r = (el) => { const b = el.getBoundingClientRect(); return { x: b.x + b.width / 2, y: b.y + b.height / 2 }; };
          const host = document.getElementById('host');
          const hb = host.getBoundingClientRect();
          const inner = (id) => { const b = host.contentDocument.getElementById(id).getBoundingClientRect(); return { x: hb.x + b.x + b.width / 2, y: hb.y + b.y + b.height / 2 }; };
          return { direct: r(document.getElementById('direct')), nestedSeven: inner('nseven'), nestedSame: inner('nsame'), viewport: { w: innerWidth, h: innerHeight } };
        })()`
      );
      nested.boxes = boxes;
      const both = [nestedSession, directSession];
      const record = async (name, work) => {
        const before = await hitsIn(both);
        const done = await step(`protected ${name}`, work);
        await pause(300);
        const after = await hitsIn(both);
        const delta = delivered(after) - delivered(before);
        nested.inputs[name] = { ok: done.ok, error: done.ok ? null : scrub(done.error), delivered: delta, valueChanged: after.value !== before.value };
      };
      const cdp = await step('page CDP session', () => page.createCDPSession());
      const raw = cdp.ok ? cdp.value : null;
      await record('page.mouse.click nested seven', () => page.mouse.click(boxes.nestedSeven.x, boxes.nestedSeven.y));
      await record('page.mouse.click direct seven', () => page.mouse.click(boxes.direct.x, boxes.direct.y));
      await record('page.touchscreen.tap nested seven', () => page.touchscreen.tap(boxes.nestedSeven.x, boxes.nestedSeven.y));
      if (raw) {
        await record('Input.dispatchMouseEvent nested seven', async () => {
          for (const type of ['mousePressed', 'mouseReleased']) await raw.send('Input.dispatchMouseEvent', { type, x: boxes.nestedSeven.x, y: boxes.nestedSeven.y, button: 'left', clickCount: 1 });
        });
        await record('Input.dispatchTouchEvent nested seven', async () => {
          await raw.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: boxes.nestedSeven.x, y: boxes.nestedSeven.y }] });
          await raw.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
        });
      }
      const focus = await step('observer focuses the nested seven input', async () => {
        for (const type of ['mousePressed', 'mouseReleased']) await observer.call('Input.dispatchMouseEvent', { type, x: boxes.nestedSeven.x, y: boxes.nestedSeven.y, button: 'left', clickCount: 1 }, top);
        return observerEval(nestedSession, 'document.activeElement?.id === "sin"');
      });
      nested.sevenFocused = focus.ok ? focus.value : false;
      await record('page.keyboard.type into focused nested seven', () => page.keyboard.type('zq'));
      if (raw) {
        await record('Input.dispatchKeyEvent into focused nested seven', () => raw.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Tab', code: 'Tab' }));
        await record('Input.insertText into focused nested seven', () => raw.send('Input.insertText', { text: 'k' }));
      }
      const root = raw ? raw.connection() : null;
      if (root) {
        for (const [name, info] of [['nested', nestedFrame], ['direct', direct]]) {
          const attach = await step(`attach ${name} seven frame`, () => root.send('Target.attachToTarget', { targetId: info.targetId, flatten: true }));
          nested.inputs[`attach ${name} seven frame`] = { ok: attach.ok, error: attach.ok ? null : scrub(attach.error), delivered: 0 };
          if (attach.ok) {
            const sid = attach.value.sessionId;
            await record(`Input.dispatchMouseEvent on the ${name} seven frame session`, async () => {
              const s = root.session(sid);
              for (const type of ['mousePressed', 'mouseReleased']) await s.send('Input.dispatchMouseEvent', { type, x: 5, y: 5, button: 'left', clickCount: 1 });
            });
          }
        }
      }
      for (const f of page.frames().filter((x) => protectedURL(x.url()))) {
        await record(`frame.click visible seven frame ${scrub(f.url())}`, () => f.click('body'));
      }
      const ordinaryNested = page.frames().find((f) => f.url().startsWith(ordinary) && f.url().includes('k=nested'));
      const ordinarySame = page.frames().find((f) => f.url().includes('k=same'));
      const ordinaryCross = page.frames().find((f) => f.url().startsWith(cross));
      nested.ordinary = {};
      for (const [name, f] of [['nested same-origin', ordinaryNested], ['direct same-origin', ordinarySame], ['cross-site OOPIF', ordinaryCross]]) {
        if (!f) {
          nested.ordinary[name] = { found: false };
          continue;
        }
        const clicked = await step(`ordinary ${name} frame.click`, () => f.click('#ibtn'));
        const typed = await step(`ordinary ${name} frame.type`, () => f.type('#iin', 'ok'));
        const read = await step(`ordinary ${name} read`, () => f.evaluate(() => ({ hits: window.hits, value: document.getElementById('iin').value })));
        nested.ordinary[name] = {
          found: true,
          click: clicked.ok ? 'ok' : scrub(clicked.error),
          type: typed.ok ? 'ok' : scrub(typed.error),
          hits: read.ok ? read.value.hits : null,
          value: read.ok ? read.value.value : null,
          readError: read.ok ? null : scrub(read.error),
        };
      }
      const control = await step('control: unguarded click and typing reach the nested seven frame', async () => {
        const before = await hitsIn([nestedSession]);
        for (const type of ['mousePressed', 'mouseReleased']) await observer.call('Input.dispatchMouseEvent', { type, x: boxes.nestedSeven.x, y: boxes.nestedSeven.y, button: 'left', clickCount: 1 }, top);
        await observer.call('Input.insertText', { text: 'c' }, top);
        await pause(300);
        const after = await hitsIn([nestedSession]);
        return { mouse: after.mouse - before.mouse, text: after.value !== before.value };
      });
      nested.control = control.ok ? control.value : { error: scrub(control.error) };
    }
  }
}

const probes = {};
result.probes = probes;
const probe = new Raw(null, 'probe', true);
const probeReady = await step('probe client', () => probe.call('Target.getTargets'));
if (probeReady.ok) {
  const listed = probeReady.value.targetInfos;
  const pageIds = Object.fromEntries(
    (mode === 'connect' ? [['framed-same pre', (u) => u.includes('/framed-same?pre')], ['nest pre', (u) => u.includes('/nest?pre')]] : [['nest live', (u) => u === nestURL]]).map(([name, test]) => [name, listed.find((i) => i.type === 'page' && test(i.url))?.targetId])
  );
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
  for (const [name, targetId] of Object.entries(pageIds)) {
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
    const second = await once(targetId, sessionId);
    const os = await observerSession(targetId);
    const truth = framesOf((await observer.call('Page.getFrameTree', {}, os)).frameTree);
    probes[name] = { listed: true, first, enable: enable.ok ? 'ok' : scrub(enable.error), contexts, second, chrome: truth };
  }
}

if (browser) {
  await step('disconnect', () => browser.disconnect());
}
const again = await connect('reconnect');
if (again) {
  const pages = await step('reconnect pages()', async () => (await again.pages()).map((p) => p.url()));
  result.reconnect.pages = pages.ok ? pages.value.map(scrub) : null;
  result.reconnect.protectedVisible = (pages.ok ? pages.value : []).filter(protectedURL).map(scrub);
  await step('reconnect disconnect', () => again.disconnect());
}

for (const [label, { lines }] of Object.entries(traces)) {
  for (const line of lines) console.log(`SEQ ${label} ${line}`);
}
console.log(`RESULT ${JSON.stringify(result)}`);
probe.close();
observer.close();
proxy.close();
process.exit(0);
