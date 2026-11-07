import { writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { chromium } from 'playwright-core';
import puppeteer from 'puppeteer-core';
import { WebSocket, WebSocketServer } from 'ws';

const require = createRequire(import.meta.url);
const [out, ...pairs] = process.argv.slice(2);
const targets = pairs.map((pair) => {
  const at = pair.indexOf('=');
  return { name: pair.slice(0, at), upstream: pair.slice(at + 1) };
});
const FRAME_HOST = process.env.G0_FRAME_HOST || 'frame.test';
const HELD = 5000;
const STEP = Number(process.env.G0_STEP_MS || 20000);
const COMMENT_ONLY = /^(?:[ \t\n]|\/\/[^\n\r\u2028\u2029]*)*$/;
const LIMIT = 256 * 1024 * 1024;

let fixturePort = 0;
const fixture = createServer((req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const html = (body) => {
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(`<!doctype html><html><head><meta charset="utf-8">${body}</body></html>`);
  };
  if (url.pathname === '/') {
    return html(
      `<title>G0 page</title></head><body><h1>Ordinary page</h1><button id="btn" onclick="document.body.dataset.clicked='yes'">Click me</button><input id="name" aria-label="Name"><a id="pop" href="/popup" target="_blank">Open popup</a>`
    );
  }
  if (url.pathname === '/popup') return html('<title>G0 popup</title></head><body><p>popup</p>');
  if (url.pathname === '/frame') {
    return html(
      `<title>G0 frame</title></head><body><iframe id="f" src="http://${FRAME_HOST}:${fixturePort}/inner"></iframe>`
    );
  }
  if (url.pathname === '/inner') {
    return html(
      `<title>G0 inner</title></head><body><button id="ibtn" onclick="document.body.dataset.clicked='yes'">Inner</button><p id="inner">inner text</p>`
    );
  }
  if (url.pathname === '/data') {
    res.writeHead(200, { 'content-type': 'text/plain', 'access-control-allow-origin': '*' });
    return res.end('real');
  }
  res.writeHead(404);
  res.end();
});
await new Promise((resolve) => fixture.listen(0, '127.0.0.1', resolve));
fixturePort = fixture.address().port;
const PAGE = `http://localhost:${fixturePort}/`;
const FRAME = `http://localhost:${fixturePort}/frame`;
const DATA = `http://localhost:${fixturePort}/data`;

const current = { target: '', client: '', flow: '', step: '' };
const records = [];

function describe(key, value) {
  if (value === null) return { type: 'null' };
  if (Array.isArray(value)) {
    const first = value.find((item) => item && typeof item === 'object');
    return { type: 'array', length: value.length, ...(first ? { itemKeys: Object.keys(first) } : {}), ...(value.length && !first ? { items: value.slice(0, 8) } : {}) };
  }
  if (typeof value === 'object') return { type: 'object', keys: Object.keys(value) };
  if (typeof value !== 'string') return { type: typeof value, value };
  if (key === 'source') {
    if (Buffer.byteLength(value) < 200) return { type: 'string', value };
    return { type: 'string', length: value.length, empty: value === '', commentOnly: COMMENT_ONLY.test(value) };
  }
  if (key === 'expression' || key === 'functionDeclaration') return { type: 'string', length: value.length };
  return value.length <= 120 ? { type: 'string', value } : { type: 'string', length: value.length };
}

function shape(params) {
  if (!params || typeof params !== 'object') return {};
  return Object.fromEntries(Object.entries(params).map(([key, value]) => [key, describe(key, value)]));
}

function tracker() {
  const targetTypes = new Map();
  const sessionTypes = new Map();
  const learn = (message) => {
    const info = message.params?.targetInfo;
    if (info?.targetId) targetTypes.set(info.targetId, info.type);
    if (message.method === 'Target.attachedToTarget' && message.params?.sessionId) {
      sessionTypes.set(message.params.sessionId, info?.type ?? 'unknown');
    }
    for (const t of message.result?.targetInfos ?? []) targetTypes.set(t.targetId, t.type);
  };
  return {
    learn,
    sessionType: (sessionId) => (sessionId ? (sessionTypes.get(sessionId) ?? 'unknown') : 'browser'),
    attached: (targetId, sessionId) => sessionId && sessionTypes.set(sessionId, targetTypes.get(targetId) ?? 'unknown'),
  };
}

function recorder(label) {
  const types = tracker();
  const pending = new Map();
  const events = {};
  return {
    sent(text) {
      let message;
      try {
        message = JSON.parse(text);
      } catch {
        return;
      }
      if (message.id === undefined || typeof message.method !== 'string') return;
      const record = {
        target: current.target,
        client: current.client,
        flow: current.flow,
        step: current.step,
        via: label,
        method: message.method,
        session: types.sessionType(message.sessionId),
        params: shape(message.params),
        at: Date.now(),
        ms: null,
        outcome: 'unanswered',
      };
      records.push(record);
      pending.set(`${message.sessionId ?? ''}#${message.id}`, { record, params: message.params });
    },
    received(text) {
      let message;
      try {
        message = JSON.parse(text);
      } catch {
        return;
      }
      types.learn(message);
      if (message.id === undefined) {
        if (message.method) events[message.method] = (events[message.method] ?? 0) + 1;
        return;
      }
      const key = `${message.sessionId ?? ''}#${message.id}`;
      const entry = pending.get(key) ?? pending.get(`#${message.id}`);
      if (!entry) return;
      pending.delete(key);
      pending.delete(`#${message.id}`);
      entry.record.ms = Date.now() - entry.record.at;
      if (message.error) {
        entry.record.outcome = 'error';
        entry.record.error = message.error.message;
      } else {
        entry.record.outcome = 'ok';
        if (entry.record.method === 'Target.attachToTarget') types.attached(entry.params?.targetId, message.result?.sessionId);
      }
    },
    events,
  };
}

const allEvents = [];

function proxy(upstream) {
  const server = createServer((req, res) => discovery(req, res, upstream).catch((error) => {
    res.writeHead(500);
    res.end(String(error?.message ?? error));
  }));
  const wss = new WebSocketServer({ noServer: true, maxPayload: LIMIT, perMessageDeflate: false });
  server.on('upgrade', (req, socket, head) => {
    wss.handleUpgrade(req, socket, head, (client) => {
      const page = /^\/devtools\/page\/(.+)$/.exec(new URL(req.url, 'http://x').pathname);
      if (page) pageSocket(client, upstream, decodeURIComponent(page[1]));
      else browserSocket(client, upstream);
    });
  });
  return new Promise((resolve) =>
    server.listen(0, '127.0.0.1', () => resolve({ server, port: server.address().port }))
  );
}

function openUpstream(upstream, label) {
  const ws = new WebSocket(upstream, { maxPayload: LIMIT, perMessageDeflate: false });
  const rec = recorder(label);
  allEvents.push({ target: current.target, client: current.client, flow: current.flow, via: label, events: rec.events });
  const queue = [];
  const send = (text) => {
    rec.sent(text);
    if (ws.readyState === WebSocket.OPEN) ws.send(text);
    else if (ws.readyState === WebSocket.CONNECTING) queue.push(text);
  };
  ws.on('open', () => {
    for (const text of queue.splice(0)) ws.send(text);
  });
  return { ws, rec, send };
}

function browserSocket(client, upstream) {
  const { ws, rec, send } = openUpstream(upstream, 'browser-socket');
  ws.on('message', (data) => {
    const text = data.toString();
    rec.received(text);
    if (client.readyState === WebSocket.OPEN) client.send(text);
  });
  ws.on('close', (code, reason) => client.close(code === 1005 || code === 1006 ? 1011 : code, reason.toString().slice(0, 120)));
  ws.on('error', () => client.terminate());
  client.on('message', (data) => send(data.toString()));
  client.on('close', () => ws.close());
}

function pageSocket(client, upstream, targetId) {
  const { ws, rec, send } = openUpstream(upstream, 'page-socket');
  let session = null;
  const waiting = [];
  ws.on('message', (data) => {
    const text = data.toString();
    rec.received(text);
    const message = JSON.parse(text);
    if (message.id === -2) {
      if (message.error) return client.close(1011, message.error.message.slice(0, 120));
      session = message.result.sessionId;
      for (const queued of waiting.splice(0)) forward(queued);
      return;
    }
    if (message.method === 'Target.detachedFromTarget' && message.params?.sessionId === session) return client.close(1000);
    if (message.method === 'Target.targetDestroyed' && message.params?.targetId === targetId) return client.close(1000);
    if (!message.sessionId) return;
    if (message.sessionId === session) delete message.sessionId;
    if (client.readyState === WebSocket.OPEN) client.send(JSON.stringify(message));
  });
  ws.on('close', () => client.close(1011));
  ws.on('error', () => client.terminate());
  const forward = (text) => {
    const message = JSON.parse(text);
    if (!message.sessionId) message.sessionId = session;
    send(JSON.stringify(message));
  };
  send(JSON.stringify({ id: -2, method: 'Target.attachToTarget', params: { targetId, flatten: true } }));
  client.on('message', (data) => {
    if (session) forward(data.toString());
    else waiting.push(data.toString());
  });
  client.on('close', () => ws.close());
}

async function shortCall(upstream, method, params, label) {
  const { ws, rec, send } = openUpstream(upstream, label);
  try {
    return await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`${method} got no answer`)), STEP);
      ws.on('message', (data) => {
        const text = data.toString();
        rec.received(text);
        const message = JSON.parse(text);
        if (message.id !== 1) return;
        clearTimeout(timer);
        if (message.error) reject(new Error(message.error.message));
        else resolve(message.result);
      });
      ws.on('error', (error) => {
        clearTimeout(timer);
        reject(error);
      });
      ws.on('close', (code, reason) => {
        clearTimeout(timer);
        reject(new Error(`closed ${code} ${reason}`));
      });
      send(JSON.stringify({ id: 1, method, params }));
    });
  } finally {
    ws.close();
  }
}

function entry(host, info) {
  return {
    description: '',
    id: info.targetId,
    title: info.title,
    type: info.type,
    url: info.url,
    webSocketDebuggerUrl: `ws://${host}/devtools/page/${info.targetId}`,
  };
}

async function discovery(req, res, upstream) {
  const url = new URL(req.url, `http://${req.headers.host}`);
  const json = (value) => {
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify(value));
  };
  const host = req.headers.host;
  if (url.pathname === '/json/version' || url.pathname === '/json/version/') {
    const v = await shortCall(upstream, 'Browser.getVersion', {}, 'json-version');
    return json({ Browser: v.product, 'Protocol-Version': v.protocolVersion, 'User-Agent': v.userAgent, 'V8-Version': v.jsVersion, 'WebKit-Version': v.revision, webSocketDebuggerUrl: `ws://${host}/devtools/browser/g0` });
  }
  if (url.pathname === '/json' || url.pathname === '/json/list' || url.pathname === '/json/list/') {
    const { targetInfos } = await shortCall(upstream, 'Target.getTargets', {}, 'json-list');
    return json(targetInfos.filter((t) => t.type !== 'browser' && t.type !== 'tab').map((t) => entry(host, t)));
  }
  if (url.pathname === '/json/new') {
    const target = decodeURIComponent(url.search.slice(1)) || 'about:blank';
    const { targetId } = await shortCall(upstream, 'Target.createTarget', { url: target }, 'json-new');
    return json(entry(host, { targetId, title: target, type: 'page', url: target }));
  }
  const close = /^\/json\/close\/(.+)$/.exec(url.pathname);
  if (close) {
    await shortCall(upstream, 'Target.closeTarget', { targetId: close[1] }, 'json-close');
    res.writeHead(200);
    return res.end('Target is closing');
  }
  res.writeHead(404);
  res.end();
}

function within(promise, ms, what) {
  let timer;
  return Promise.race([
    promise,
    new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${what} timed out after ${ms} ms`)), ms);
    }),
  ]).finally(() => clearTimeout(timer));
}

const results = [];

async function flow(client, name, body) {
  current.client = client;
  current.flow = name;
  const steps = [];
  const state = {};
  const step = async (label, fn, ms = STEP) => {
    current.step = label;
    const started = Date.now();
    try {
      const value = await within(Promise.resolve().then(fn), ms, label);
      steps.push({ step: label, ok: true, ms: Date.now() - started, value: value === undefined ? null : value });
      return value;
    } catch (error) {
      steps.push({ step: label, ok: false, ms: Date.now() - started, error: String(error?.message ?? error).split('\n')[0].slice(0, 400) });
      throw error;
    }
  };
  let failed = null;
  try {
    await body(step, state);
  } catch (error) {
    failed = String(error?.message ?? error).split('\n')[0].slice(0, 400);
  }
  current.step = 'cleanup';
  for (const cleanup of (state.cleanup ?? []).reverse()) await within(Promise.resolve().then(cleanup), 10000, 'cleanup').catch(() => {});
  await new Promise((resolve) => setTimeout(resolve, 300));
  results.push({ target: current.target, client, flow: name, ok: !failed, failed, steps });
}

const PW = (endpoint) => {
  const connect = async (step, state) => {
    const browser = await step('chromium.connectOverCDP', () => chromium.connectOverCDP(endpoint, { timeout: STEP }));
    state.cleanup = [() => browser.close()];
    return browser;
  };
  const page = async (step, state) => {
    const browser = await connect(step, state);
    const context = await step('browser.contexts()[0]', () => browser.contexts()[0]);
    const p = await step('context.newPage', () => context.newPage());
    p.setDefaultTimeout(STEP);
    state.cleanup.push(() => p.close());
    return { browser, context, page: p };
  };
  const loaded = async (step, state, url = PAGE) => {
    const opened = await page(step, state);
    await step('page.goto', () => opened.page.goto(url).then(() => opened.page.title()));
    return opened;
  };
  return [
    ['connect', async (step, state) => {
      const browser = await connect(step, state);
      await step('browser.contexts', () => browser.contexts().map((c) => c.pages().length));
    }],
    ['default context first page', async (step, state) => {
      const browser = await connect(step, state);
      const context = await step('browser.contexts()[0]', () => browser.contexts()[0]);
      const existing = await step('context.pages()', () => context.pages().map((p) => p.url()));
      const first = context.pages()[0] ?? (await step('context.newPage', () => context.newPage()));
      if (!existing.length) state.cleanup.push(() => first.close());
      await step('page.evaluate(location.href)', () => first.evaluate(() => location.href));
    }],
    ['newContext + newPage', async (step, state) => {
      const browser = await connect(step, state);
      const context = await step('browser.newContext', () => browser.newContext());
      const p = await step('context.newPage', () => context.newPage());
      p.setDefaultTimeout(STEP);
      await step('page.goto', () => p.goto(PAGE).then(() => p.title()));
    }],
    ['goto', (step, state) => loaded(step, state)],
    ['click', async (step, state) => {
      const { page: p } = await loaded(step, state);
      await step('page.click', () => p.click('#btn'));
      await step('page.evaluate(dataset)', () => p.evaluate(() => document.body.dataset.clicked));
    }],
    ['fill', async (step, state) => {
      const { page: p } = await loaded(step, state);
      await step('page.fill', () => p.fill('#name', 'Ada'));
      await step('page.inputValue', () => p.inputValue('#name'));
    }],
    ['evaluate', async (step, state) => {
      const { page: p } = await loaded(step, state);
      await step('page.evaluate', () => p.evaluate(() => 6 * 7));
    }],
    ['screenshot', async (step, state) => {
      const { page: p } = await loaded(step, state);
      await step('page.screenshot', () => p.screenshot().then((b) => b.length));
    }],
    ['cookies', async (step, state) => {
      const { context } = await loaded(step, state);
      await step('context.addCookies', () => context.addCookies([{ name: 'g0', value: 'one', url: PAGE }]));
      await step('context.cookies', () => context.cookies(PAGE).then((c) => c.map((x) => x.name)));
    }],
    ['route', async (step, state) => {
      const { page: p } = await page(step, state);
      await step('page.route', () => p.route('**/data', (route) => route.fulfill({ body: 'routed' })));
      await step('page.goto', () => p.goto(PAGE).then(() => p.title()));
      await step('page.evaluate(fetch)', () => p.evaluate((u) => fetch(u).then((r) => r.text()), DATA));
    }],
    ['addInitScript', async (step, state) => {
      const { page: p } = await page(step, state);
      await step('page.addInitScript', () => p.addInitScript(() => { window.g0init = 7; }));
      await step('page.goto', () => p.goto(PAGE).then(() => p.title()));
      await step('page.evaluate(g0init)', () => p.evaluate(() => window.g0init ?? null));
    }],
    ['exposeFunction', async (step, state) => {
      const { page: p } = await page(step, state);
      await step('page.exposeFunction', () => p.exposeFunction('g0add', (a, b) => a + b));
      await step('page.goto', () => p.goto(PAGE).then(() => p.title()));
      await step('page.evaluate(g0add)', () => p.evaluate(() => window.g0add(2, 3)));
    }],
    ["waitForEvent('popup')", async (step, state) => {
      const { page: p } = await loaded(step, state);
      const popup = await step("page.waitForEvent('popup') + click", () => Promise.all([p.waitForEvent('popup'), p.click('#pop')]).then(([x]) => x));
      state.cleanup.push(() => popup.close());
      await step('popup.waitForLoadState', () => popup.waitForLoadState());
      await step('popup.title', () => popup.title());
    }],
    ['cross-site iframe', async (step, state) => {
      const { page: p } = await loaded(step, state, FRAME);
      const frame = await step('elementHandle.contentFrame', async () => {
        const handle = await p.waitForSelector('iframe');
        const f = await handle.contentFrame();
        await f.waitForSelector('#ibtn');
        return f;
      });
      await step('frame.click', () => frame.click('#ibtn'));
      await step('frame.evaluate', () => frame.evaluate(() => [location.host, document.body.dataset.clicked]));
    }],
  ];
};

const PP = (endpoint) => {
  const connect = async (step, state) => {
    const browser = await step('puppeteer.connect', () => puppeteer.connect({ browserWSEndpoint: endpoint, protocolTimeout: STEP }));
    state.cleanup = [() => browser.disconnect()];
    return browser;
  };
  const page = async (step, state) => {
    const browser = await connect(step, state);
    const p = await step('browser.newPage', () => browser.newPage());
    p.setDefaultTimeout(STEP);
    state.cleanup.push(() => p.close());
    return { browser, page: p };
  };
  const loaded = async (step, state, url = PAGE) => {
    const opened = await page(step, state);
    await step('page.goto', () => opened.page.goto(url).then(() => opened.page.title()));
    return opened;
  };
  return [
    ['connect', async (step, state) => {
      const browser = await connect(step, state);
      await step('browser.pages', () => browser.pages().then((ps) => ps.length));
    }],
    ['newPage', (step, state) => page(step, state)],
    ['goto', (step, state) => loaded(step, state)],
    ['click', async (step, state) => {
      const { page: p } = await loaded(step, state);
      await step('page.click', () => p.click('#btn'));
      await step('page.evaluate(dataset)', () => p.evaluate(() => document.body.dataset.clicked));
    }],
    ['type', async (step, state) => {
      const { page: p } = await loaded(step, state);
      await step('page.type', () => p.type('#name', 'Ada'));
      await step('page.$eval(value)', () => p.$eval('#name', (e) => e.value));
    }],
    ['evaluate', async (step, state) => {
      const { page: p } = await loaded(step, state);
      await step('page.evaluate', () => p.evaluate(() => 6 * 7));
    }],
    ['screenshot', async (step, state) => {
      const { page: p } = await loaded(step, state);
      await step('page.screenshot', () => p.screenshot().then((b) => b.length));
    }],
    ['cookies', async (step, state) => {
      const { browser } = await loaded(step, state);
      await step('browser.setCookie', () => browser.setCookie({ name: 'g0', value: 'one', domain: 'localhost', path: '/' }));
      await step('browser.cookies', () => browser.cookies().then((c) => c.map((x) => x.name)));
    }],
    ['page cookies (deprecated API)', async (step, state) => {
      const { page: p } = await loaded(step, state);
      await step('page.setCookie', () => p.setCookie({ name: 'g0p', value: 'one' }));
      await step('page.cookies', () => p.cookies().then((c) => c.map((x) => x.name)));
    }],
    ['setRequestInterception', async (step, state) => {
      const { page: p } = await page(step, state);
      await step('page.setRequestInterception(true)', () => p.setRequestInterception(true));
      p.on('request', (r) => (r.url().endsWith('/data') ? r.respond({ body: 'routed', headers: { 'access-control-allow-origin': '*' } }) : r.continue()));
      await step('page.goto', () => p.goto(PAGE).then(() => p.title()));
      await step('page.evaluate(fetch)', () => p.evaluate((u) => fetch(u).then((r) => r.text()), DATA));
    }],
    ['evaluateOnNewDocument', async (step, state) => {
      const { page: p } = await page(step, state);
      await step('page.evaluateOnNewDocument', () => p.evaluateOnNewDocument(() => { window.g0init = 7; }).then(() => null));
      await step('page.goto', () => p.goto(PAGE).then(() => p.title()));
      await step('page.evaluate(g0init)', () => p.evaluate(() => window.g0init ?? null));
    }],
    ['exposeFunction', async (step, state) => {
      const { page: p } = await page(step, state);
      await step('page.exposeFunction', () => p.exposeFunction('g0add', (a, b) => a + b));
      await step('page.goto', () => p.goto(PAGE).then(() => p.title()));
      await step('page.evaluate(g0add)', () => p.evaluate(() => window.g0add(2, 3)));
    }],
    ['popup', async (step, state) => {
      const { page: p } = await loaded(step, state);
      const popup = await step("page.once('popup') + click", () => Promise.all([new Promise((resolve) => p.once('popup', resolve)), p.click('#pop')]).then(([x]) => x));
      state.cleanup.push(() => popup?.close());
      await step('popup.waitForFunction(readyState)', () => popup.waitForFunction(() => document.readyState === 'complete').then(() => null));
      await step('popup.title', () => popup.title());
    }],
    ['iframe', async (step, state) => {
      const { page: p } = await loaded(step, state, FRAME);
      const frame = await step('page.waitForFrame', () => p.waitForFrame((f) => f.url().includes('/inner')));
      await step('frame.waitForSelector', () => frame.waitForSelector('#ibtn').then(() => null));
      await step('frame.click', () => frame.click('#ibtn'));
      await step('frame.evaluate', () => frame.evaluate(() => [location.host, document.body.dataset.clicked]));
    }],
  ];
};

const RAW = (http, ws) => {
  const socket = async (step, state, url, label) => {
    const s = await step(label, () => new Promise((resolve, reject) => {
      const c = new WebSocket(url, { maxPayload: LIMIT, perMessageDeflate: false });
      c.once('open', () => resolve(c));
      c.once('error', reject);
      c.once('close', (code, reason) => reject(new Error(`closed ${code} ${reason}`)));
    }));
    let next = 0;
    const pending = new Map();
    const events = [];
    s.on('message', (data) => {
      const m = JSON.parse(data.toString());
      if (m.id !== undefined && pending.has(m.id)) {
        pending.get(m.id)(m);
        pending.delete(m.id);
      } else events.push(m);
    });
    s.on('close', (code, reason) => {
      for (const resolve of pending.values()) resolve({ error: { message: `closed ${code} ${reason}` } });
      pending.clear();
    });
    state.cleanup.push(() => s.close());
    const call = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
      next += 1;
      const id = next;
      pending.set(id, (m) => (m.error ? reject(new Error(m.error.message)) : resolve(m.result)));
      s.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
    });
    const until = (match, ms = STEP) => new Promise((resolve, reject) => {
      const started = Date.now();
      const tick = () => {
        const found = events.find(match);
        if (found) return resolve(found);
        if (Date.now() - started > ms) return reject(new Error('event timed out'));
        setTimeout(tick, 50);
      };
      tick();
    });
    return { call, until };
  };
  return [
    ['/json/version', async (step, state) => {
      state.cleanup = [];
      await step('GET /json/version', () => fetch(`${http}/json/version`).then(async (r) => { if (!r.ok) throw new Error(`${r.status} ${await r.text()}`); const v = await r.json(); return [v.Browser, Object.keys(v)]; }));
    }],
    ['/json/list', async (step, state) => {
      state.cleanup = [];
      await step('GET /json/list', () => fetch(`${http}/json/list`).then(async (r) => { if (!r.ok) throw new Error(`${r.status} ${await r.text()}`); return (await r.json()).map((t) => t.type); }));
    }],
    ['page socket: Runtime.evaluate + Page.navigate', async (step, state) => {
      state.cleanup = [];
      const created = await step('PUT /json/new', () => fetch(`${http}/json/new?about:blank`, { method: 'PUT' }).then(async (r) => { if (!r.ok) throw new Error(`${r.status} ${await r.text()}`); return r.json(); }));
      state.cleanup.push(() => fetch(`${http}/json/close/${created.id}`));
      const listed = await step('GET /json/list (find the new page)', () => fetch(`${http}/json/list`).then((r) => r.json()).then((l) => l.find((t) => t.id === created.id)?.webSocketDebuggerUrl ?? created.webSocketDebuggerUrl));
      const { call, until } = await socket(step, state, listed, 'page socket open');
      await step('Runtime.evaluate', () => call('Runtime.evaluate', { expression: '6 * 7', returnByValue: true }).then((r) => r.result.value));
      await step('Page.enable', () => call('Page.enable'));
      await step('Page.navigate', () => call('Page.navigate', { url: PAGE }));
      await step('Page.loadEventFired', () => until((e) => e.method === 'Page.loadEventFired').then(() => null));
      await step('Runtime.evaluate(title)', () => call('Runtime.evaluate', { expression: 'document.title', returnByValue: true }).then((r) => r.result.value));
    }],
    ['browser socket, flat sessions', async (step, state) => {
      state.cleanup = [];
      const { call, until } = await socket(step, state, ws, 'browser socket open');
      await step('Browser.getVersion', () => call('Browser.getVersion').then((v) => v.product));
      await step('Target.getTargets', () => call('Target.getTargets').then((r) => r.targetInfos.map((t) => t.type)));
      const { targetId } = await step('Target.createTarget', () => call('Target.createTarget', { url: 'about:blank' }));
      state.cleanup.push(() => call('Target.closeTarget', { targetId }));
      const { sessionId } = await step('Target.attachToTarget(flatten)', () => call('Target.attachToTarget', { targetId, flatten: true }));
      await step('Runtime.evaluate', () => call('Runtime.evaluate', { expression: '6 * 7', returnByValue: true }, sessionId).then((r) => r.result.value));
      await step('Page.enable', () => call('Page.enable', {}, sessionId));
      await step('Page.navigate', () => call('Page.navigate', { url: PAGE }, sessionId));
      await step('Page.loadEventFired', () => until((e) => e.method === 'Page.loadEventFired' && e.sessionId === sessionId).then(() => null));
      await step('Runtime.evaluate(title)', () => call('Runtime.evaluate', { expression: 'document.title', returnByValue: true }, sessionId).then((r) => r.result.value));
    }],
  ];
};

const only = process.env.G0_CLIENTS?.split(',').filter(Boolean);
for (const target of targets) {
  current.target = target.name;
  const { server, port } = await proxy(target.upstream);
  const ws = `ws://127.0.0.1:${port}/devtools/browser/g0`;
  const http = `http://127.0.0.1:${port}`;
  const clients = [
    ['playwright', PW(ws)],
    ['puppeteer', PP(ws)],
    ['raw', RAW(http, ws)],
  ].filter(([name]) => !only?.length || only.includes(name));
  for (const [client, flows] of clients) {
    for (const [name, body] of flows) {
      await flow(client, name, body);
      process.stderr.write(`g0 ${target.name} ${client} ${name}: ${results.at(-1).ok ? 'ok' : `FAILED ${results.at(-1).failed}`}\n`);
    }
  }
  server.closeAllConnections?.();
  server.close();
}
fixture.closeAllConnections?.();
fixture.close();

const flagged = records.filter((r) => (r.outcome === 'error' || r.ms > HELD || (r.outcome === 'unanswered' && r.step !== 'cleanup')));
const versions = {
  'playwright-core': require('playwright-core/package.json').version,
  'puppeteer-core': require('puppeteer-core/package.json').version,
  node: process.version,
};
await writeFile(out, JSON.stringify({ versions, page: PAGE, frameHost: FRAME_HOST, results, flagged, records, events: allEvents }, null, 1));
const summary = results.map((r) => `${r.target}\t${r.client}\t${r.flow}\t${r.ok ? 'ok' : `FAILED: ${r.failed}`}`).join('\n');
process.stdout.write(`${JSON.stringify({ versions })}\n${summary}\n`);
process.stdout.write(`flagged ${flagged.length} of ${records.length} commands\n`);
for (const r of flagged) process.stdout.write(`${JSON.stringify({ t: r.target, c: r.client, f: r.flow, s: r.step, m: r.method, on: r.session, o: r.outcome, ms: r.ms, e: r.error, p: r.params })}\n`);
process.exit(0);
