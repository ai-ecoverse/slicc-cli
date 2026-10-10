import { ResetError } from './link.js';

export const MAX_MESSAGE = 64 * 1024 * 1024;

const encoder = new TextEncoder();
const decoder = new TextDecoder();

class ChunkReader {
  #chunks = [];
  available = 0;

  push(chunk) {
    this.#chunks.push(chunk);
    this.available += chunk.byteLength;
  }

  take(n) {
    const out = new Uint8Array(n);
    let at = 0;
    while (at < n) {
      const head = this.#chunks[0];
      const k = Math.min(head.byteLength, n - at);
      out.set(head.subarray(0, k), at);
      at += k;
      if (k === head.byteLength) this.#chunks.shift();
      else this.#chunks[0] = head.subarray(k);
    }
    this.available -= n;
    return out;
  }
}

export function frameMessage(text) {
  const body = encoder.encode(text);
  const out = new Uint8Array(4 + body.byteLength);
  new DataView(out.buffer).setUint32(0, body.byteLength);
  out.set(body, 4);
  return out;
}

export class CdpClient {
  static async open(link, browser, options = {}) {
    return new CdpClient(await link.open({ kind: 'cdp', browser }, options));
  }

  #nextId = 1;
  #pending = new Map();
  #waiters = new Set();
  #writing = Promise.resolve();

  constructor(stream) {
    this.stream = stream;
    this.opened = stream.opened;
    this.events = [];
    this.maxMessage = 0;
    this.closed = this.#readLoop();
  }

  async #readLoop() {
    const reader = new ChunkReader();
    let want = -1;
    let outcome = { end: true };
    try {
      for (let chunk = await this.stream.read(); chunk; chunk = await this.stream.read()) {
        reader.push(chunk);
        for (;;) {
          if (want < 0 && reader.available >= 4) want = new DataView(reader.take(4).buffer).getUint32(0);
          if (want < 0 || reader.available < want) break;
          this.maxMessage = Math.max(this.maxMessage, want);
          this.#dispatch(JSON.parse(decoder.decode(reader.take(want))));
          want = -1;
        }
      }
      if (want >= 0 || reader.available > 0) outcome = { error: 'END inside a message' };
    } catch (error) {
      outcome = error instanceof ResetError ? { reset: error.code, detail: error.detail } : { error: String(error?.message ?? error) };
    }
    for (const { reject } of this.#pending.values()) reject(new Error('cdp stream closed'));
    this.#pending.clear();
    return outcome;
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
    for (const waiter of [...this.#waiters]) {
      if (waiter.match(msg)) {
        this.#waiters.delete(waiter);
        waiter.resolve(msg);
      }
    }
  }

  send(method, params = {}, sessionId) {
    const id = this.#nextId;
    this.#nextId += 1;
    const msg = { id, method, params };
    if (sessionId) msg.sessionId = sessionId;
    const reply = new Promise((resolve, reject) => this.#pending.set(id, { resolve, reject }));
    this.write(frameMessage(JSON.stringify(msg))).catch((error) => {
      this.#pending.get(id)?.reject(error);
      this.#pending.delete(id);
    });
    return reply;
  }

  write(bytes) {
    const next = this.#writing.then(() => this.stream.write(bytes));
    this.#writing = next.catch(() => {});
    return next;
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

  async attachTo(url, extra = {}) {
    const { targetId } = await this.send('Target.createTarget', { url, ...extra });
    const { sessionId } = await this.send('Target.attachToTarget', { targetId, flatten: true });
    return { targetId, sessionId };
  }

  async evaluate(expression, sessionId) {
    const res = await this.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }, sessionId);
    if (res.exceptionDetails) throw new Error(res.exceptionDetails.exception?.description ?? res.exceptionDetails.text);
    return res.result.value;
  }

  async close() {
    await this.#writing;
    this.stream.end();
    return this.closed;
  }
}

async function sha256Text(text) {
  const digest = await crypto.subtle.digest('SHA-256', encoder.encode(text));
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, '0')).join('');
}

const noise = (size) => `(() => {
  document.body.style.margin = '0';
  const canvas = document.createElement('canvas');
  canvas.width = ${size};
  canvas.height = ${size};
  document.body.append(canvas);
  const ctx = canvas.getContext('2d');
  const img = ctx.createImageData(${size}, ${size});
  for (let at = 0; at < img.data.length; at += 65536) crypto.getRandomValues(img.data.subarray(at, Math.min(img.data.length, at + 65536)));
  for (let i = 3; i < img.data.length; i += 4) img.data[i] = 255;
  ctx.putImageData(img, 0, 0);
  return new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve(true))));
})()`;

const echoSocket = (url, count) => `new Promise((resolve, reject) => {
  const ws = new WebSocket(${JSON.stringify(url)});
  const got = [];
  ws.onerror = () => reject(new Error('websocket error'));
  ws.onopen = () => { for (let i = 0; i < ${count}; i += 1) ws.send('frame ' + i); };
  ws.onmessage = (event) => {
    got.push(event.data);
    if (got.length === ${count}) { ws.close(); resolve(got.length); }
  };
})`;

export function cdpTest(getLink, describe) {
  const held = new Map();
  const run = async (fn) => {
    try {
      return await fn();
    } catch (error) {
      return { ...describe(error), cdpError: error.cdp ?? null };
    }
  };
  return {
    version: (browser) =>
      run(async () => {
        const c = await CdpClient.open(getLink(), browser);
        const v = await c.send('Browser.getVersion');
        const closed = await c.close();
        return { opened: c.opened, product: v.product, protocolVersion: v.protocolVersion, closed };
      }),
    navigate: (browser, url) =>
      run(async () => {
        const c = await CdpClient.open(getLink(), browser);
        const { targetId, sessionId } = await c.attachTo('about:blank');
        await c.send('Page.enable', {}, sessionId);
        const loaded = c.waitEvent((e) => e.method === 'Page.loadEventFired' && e.sessionId === sessionId);
        await c.send('Page.navigate', { url }, sessionId);
        await loaded;
        const title = await c.evaluate('document.title', sessionId);
        const href = await c.evaluate('location.href', sessionId);
        await c.send('Target.closeTarget', { targetId });
        return { title, href, closed: await c.close() };
      }),
    screenshot: (browser, size) =>
      run(async () => {
        const c = await CdpClient.open(getLink(), browser);
        const { targetId, sessionId } = await c.attachTo('about:blank');
        await c.evaluate(noise(size), sessionId);
        const clip = { x: 0, y: 0, width: size, height: size, scale: 1 };
        const shot = await c.send('Page.captureScreenshot', { format: 'png', clip, captureBeyondViewport: true }, sessionId);
        const result = {
          targetId,
          length: shot.data.length,
          sha256: await sha256Text(shot.data),
          png: atob(shot.data.slice(0, 8)).slice(1, 4),
          maxMessage: c.maxMessage,
        };
        await c.send('Target.detachFromTarget', { sessionId });
        result.closed = await c.close();
        return result;
      }),
    concurrent: (browser) =>
      run(async () => {
        const [a, b] = await Promise.all([CdpClient.open(getLink(), browser), CdpClient.open(getLink(), browser, { shared: true })]);
        const [va, vb] = await Promise.all([a.send('Browser.getVersion'), b.send('Browser.getVersion')]);
        const { targetId, sessionId } = await a.attachTo('about:blank');
        let foreign = null;
        try {
          await b.evaluate('1 + 1', sessionId);
          foreign = 'accepted';
        } catch (error) {
          foreign = error.cdp ? 'refused' : String(error.message);
        }
        const own = await a.evaluate('6 * 7', sessionId);
        await a.send('Target.closeTarget', { targetId });
        const [ca, cb] = await Promise.all([a.close(), b.close()]);
        return { both: Boolean(va.product && vb.product), foreign, own, closed: [ca, cb], shared: [a.stream.shared, b.stream.shared] };
      }),
    filtered: (browser, url, count) =>
      run(async () => {
        const c = await CdpClient.open(getLink(), browser);
        const { targetId, sessionId } = await c.attachTo('about:blank');
        await c.send('Network.enable', {}, sessionId);
        const echoed = await c.evaluate(echoSocket(url, count), sessionId);
        await c.waitEvent((e) => e.method === 'Network.webSocketClosed' && e.sessionId === sessionId);
        await c.send('Browser.getVersion');
        const counts = {};
        for (const e of c.events) counts[e.method] = (counts[e.method] ?? 0) + 1;
        await c.send('Target.closeTarget', { targetId });
        return { echoed, counts, closed: await c.close() };
      }),
    createTarget: (browser, url) =>
      run(async () => {
        const c = await CdpClient.open(getLink(), browser);
        const { targetId } = await c.send('Target.createTarget', { url });
        return { targetId, closed: await c.close() };
      }),
    hold: (name, browser) =>
      run(async () => {
        const c = await CdpClient.open(getLink(), browser);
        await c.send('Browser.getVersion');
        held.set(name, c);
        return { opened: c.opened };
      }),
    held: (name) => held.get(name).closed,
    tooBig: (browser) =>
      run(async () => {
        const c = await CdpClient.open(getLink(), browser);
        const header = new Uint8Array(4);
        new DataView(header.buffer).setUint32(0, MAX_MESSAGE + 1);
        await c.write(header);
        return c.closed;
      }),
  };
}
