import { createServer } from 'node:http';
import { chromium } from 'playwright-core';
import puppeteer from 'puppeteer-core';
import { WebSocket, WebSocketServer } from 'ws';

const [upstream, ordinary, protectedURL, which] = process.argv.slice(2);
const STEP = 60000;
const LIMIT = 64 * 1024 * 1024;

function openUpstream() {
  const ws = new WebSocket(upstream, { maxPayload: LIMIT, perMessageDeflate: false });
  const queue = [];
  const send = (text) => {
    if (ws.readyState === WebSocket.OPEN) ws.send(text);
    else if (ws.readyState === WebSocket.CONNECTING) queue.push(text);
  };
  ws.on('open', () => {
    for (const text of queue.splice(0)) ws.send(text);
  });
  return { ws, send };
}

function browserSocket(client) {
  const { ws, send } = openUpstream();
  ws.on('message', (data) => {
    if (client.readyState === WebSocket.OPEN) client.send(data.toString());
  });
  ws.on('close', () => client.close(1011));
  ws.on('error', () => client.terminate());
  client.on('message', (data) => send(data.toString()));
  client.on('close', () => ws.close());
}

async function shortCall(method, params) {
  const { ws, send } = openUpstream();
  try {
    return await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`${method} timed out`)), STEP);
      ws.on('message', (data) => {
        const message = JSON.parse(data.toString());
        if (message.id !== 1) return;
        clearTimeout(timer);
        if (message.error) reject(new Error(message.error.message));
        else resolve(message.result);
      });
      ws.on('error', (error) => {
        clearTimeout(timer);
        reject(error);
      });
      send(JSON.stringify({ id: 1, method, params }));
    });
  } finally {
    ws.close();
  }
}

async function discovery(req, res) {
  const host = req.headers.host;
  const url = new URL(req.url, `http://${host}`);
  if (url.pathname === '/json/version' || url.pathname === '/json/version/') {
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
  if (url.pathname === '/json' || url.pathname === '/json/' || url.pathname === '/json/list' || url.pathname === '/json/list/') {
    const { targetInfos } = await shortCall('Target.getTargets', {});
    const list = (targetInfos ?? [])
      .filter((t) => t.type !== 'browser' && t.type !== 'tab')
      .map((t) => ({
        description: '',
        id: t.targetId,
        title: t.title,
        type: t.type,
        url: t.url,
        webSocketDebuggerUrl: `ws://${host}/devtools/page/${t.targetId}`,
      }));
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify(list));
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

async function crossSiteFrame(page, url, frames) {
  const cross = new URL('/inner', url);
  cross.hostname = '127.0.0.1';
  await page.goto(new URL('/cross', url).href, { waitUntil: 'load' });
  const deadline = Date.now() + 20000;
  for (;;) {
    const found = frames().find((f) => f.url().startsWith(cross.href));
    if (found) return found;
    if (Date.now() > deadline) throw new Error(`cross-site iframe ${cross.href} never reached the client: ${frames().map((f) => f.url()).join(' ')}`);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}

async function viaPlaywright(url, expectProtected) {
  const browser = await chromium.connectOverCDP(endpoint, { timeout: STEP });
  try {
    const context = await browser.newContext();
    const page = await context.newPage();
    if (expectProtected) {
      let refused = false;
      try {
        await page.goto(url, { timeout: 15000, waitUntil: 'domcontentloaded' });
      } catch {
        refused = true;
      }
      const body = refused ? '' : await page.content().catch(() => '');
      if (!refused && /vault|seven|sliccy/i.test(await page.title().catch(() => '')) && body.includes('beacon')) {
        throw new Error('protected page was driven');
      }
      return;
    }
    await page.goto(url, { waitUntil: 'domcontentloaded' });
    await page.fill('#name', 'playwright');
    await page.click('#go');
    if ((await page.textContent('#out')) !== 'hello playwright') throw new Error('fill/click failed');
    if ((await page.evaluate(() => 6 * 7)) !== 42) throw new Error('evaluate failed');
    const shot = await page.screenshot();
    if (shot.length < 100) throw new Error('empty screenshot');
    await context.addCookies([{ name: 's', value: '1', url }]);
    const cookies = await context.cookies();
    if (!cookies.some((c) => c.name === 's')) throw new Error('cookies missing');
    const popupPromise = page.waitForEvent('popup');
    await page.click('#pop');
    const popup = await popupPromise;
    await popup.waitForLoadState('domcontentloaded');
    await popup.close();
    await page.goto(new URL('/frame', url).href, { waitUntil: 'domcontentloaded' });
    const frame = page.frameLocator('#f');
    await frame.locator('#ibtn').click();
    const cross = await crossSiteFrame(page, url, () => page.frames());
    if ((await cross.textContent('title')) !== 'inner') throw new Error('cross-site iframe unreadable');
    await page.close();
    await context.close();
  } finally {
    await browser.close();
  }
}

async function viaPuppeteer(url, expectProtected) {
  const browser = await puppeteer.connect({ browserURL: endpoint, protocolTimeout: STEP });
  try {
    const page = await browser.newPage();
    if (expectProtected) {
      let refused = false;
      try {
        await page.goto(url, { timeout: 15000, waitUntil: 'domcontentloaded' });
      } catch {
        refused = true;
      }
      const title = refused ? '' : await page.title().catch(() => '');
      if (!refused && /vault|seven|sliccy/i.test(title)) throw new Error('protected page was driven');
      await page.close().catch(() => {});
      return;
    }
    await page.goto(url, { waitUntil: 'domcontentloaded' });
    await page.type('#name', 'puppeteer');
    await page.click('#go');
    if ((await page.$eval('#out', (el) => el.textContent)) !== 'hello puppeteer') throw new Error('type/click failed');
    if ((await page.evaluate(() => 6 * 7)) !== 42) throw new Error('evaluate failed');
    const shot = await page.screenshot();
    if (shot.length < 100) throw new Error('empty screenshot');
    await page.setCookie({ name: 's', value: '1', url });
    const cookies = await page.cookies();
    if (!cookies.some((c) => c.name === 's')) throw new Error('cookies missing');
    const popupPromise = new Promise((resolve) => page.once('popup', resolve));
    await page.click('#pop');
    const popup = await popupPromise;
    await popup.waitForNavigation({ waitUntil: 'domcontentloaded' }).catch(() => {});
    await popup.close();
    await page.goto(new URL('/frame', url).href, { waitUntil: 'domcontentloaded' });
    const frame = page.frames().find((f) => f.url().includes('/inner'));
    if (!frame) throw new Error('iframe missing');
    await frame.click('#ibtn');
    const cross = await crossSiteFrame(page, url, () => page.frames());
    if ((await cross.$eval('title', (el) => el.textContent)) !== 'inner') throw new Error('cross-site iframe unreadable');
    await page.close();
  } finally {
    await browser.disconnect();
  }
}

const run = which === 'puppeteer' ? viaPuppeteer : viaPlaywright;
await run(ordinary, false);
await run(protectedURL, true);
console.log(`${which} ok`);
proxy.close();
process.exit(0);
