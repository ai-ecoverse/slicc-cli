import { Frame, Link, ResetError, fetchRaw, resolve } from './link.js';
import { cdpTest } from './cdp.js';

const state = { nonce: null, pc: null, link: null, hellos: 0, ready: null, resolveReady: null, browserUpdates: [] };

function onBrowsers(stream) {
  state.browserUpdates.push(stream.open.browsers);
  stream.accept({});
  stream.end();
}

function freshReady() {
  state.ready = new Promise((resolve) => {
    state.resolveReady = resolve;
  });
}

freshReady();

function send(msg) {
  globalThis.sliccLinkSend(JSON.stringify({ ...msg, nonce: state.nonce }));
}

function teardown() {
  state.link?.close('replaced');
  state.link = null;
  state.pc = null;
}

async function setup(nonce) {
  teardown();
  freshReady();
  state.nonce = nonce;
  state.hellos += 1;
  const pc = new RTCPeerConnection();
  const link = new Link(pc, { hello: { name: 'link test page', caps: ['browsers'] }, handlers: { browsers: onBrowsers } });
  state.pc = pc;
  state.link = link;
  const early = [];
  let offered = false;
  pc.addEventListener('icecandidate', (event) => {
    const msg = { t: 'candidate', candidate: event.candidate ? event.candidate.toJSON() : null };
    if (offered) send(msg);
    else early.push(msg);
  });
  await pc.setLocalDescription(await pc.createOffer());
  send({ t: 'offer', sdp: pc.localDescription.sdp });
  offered = true;
  for (const msg of early) send(msg);
  link.peerHello.then((hello) => state.resolveReady(hello), () => {});
}

globalThis.sliccLinkReceive = (raw) => {
  const msg = typeof raw === 'string' ? JSON.parse(raw) : raw;
  if (msg.t === 'hello') return void setup(msg.nonce);
  if (msg.nonce !== state.nonce || !state.pc) return;
  if (msg.t === 'answer') state.pc.setRemoteDescription({ type: 'answer', sdp: msg.sdp });
  if (msg.t === 'candidate' && msg.candidate) state.pc.addIceCandidate(msg.candidate);
  if (msg.t === 'bye') teardown();
};

function randomBytes(n) {
  const out = new Uint8Array(n);
  for (let at = 0; at < n; at += 65536) crypto.getRandomValues(out.subarray(at, Math.min(n, at + 65536)));
  return out;
}

async function sha256(bytes) {
  const digest = await crypto.subtle.digest('SHA-256', bytes);
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, '0')).join('');
}

function equal(a, b) {
  if (a.byteLength !== b.byteLength) return false;
  for (let i = 0; i < a.byteLength; i += 1) if (a[i] !== b[i]) return false;
  return true;
}

function describe(error) {
  if (error instanceof ResetError) return { reset: error.code, detail: error.detail };
  return { error: String(error?.message ?? error) };
}

function toBase64(bytes) {
  let out = '';
  for (let at = 0; at < bytes.byteLength; at += 32768) out += String.fromCharCode(...bytes.subarray(at, at + 32768));
  return btoa(out);
}

function fromBase64(text) {
  return Uint8Array.from(atob(text), (c) => c.charCodeAt(0));
}

function concat(parts, total) {
  const out = new Uint8Array(total);
  let at = 0;
  for (const part of parts) {
    out.set(part, at);
    at += part.byteLength;
  }
  return out;
}

async function* prng(size, seed) {
  let x = seed >>> 0;
  for (let at = 0; at < size; at += 65536) {
    const n = Math.min(65536, size - at);
    const words = new Uint32Array(Math.ceil(n / 4));
    for (let i = 0; i < words.length; i += 1) {
      x ^= x << 13;
      x ^= x >>> 17;
      x ^= x << 5;
      x >>>= 0;
      words[i] = x;
    }
    yield new Uint8Array(words.buffer, 0, n);
  }
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

globalThis.linkTest = {
  status() {
    return {
      nonce: state.nonce,
      hellos: state.hellos,
      up: Boolean(state.link?.helloReceived && !state.link?.error),
      signaling: state.pc?.signalingState ?? null,
      ice: state.pc?.iceConnectionState ?? null,
      connection: state.pc?.connectionState ?? null,
      linkError: state.link?.error?.message ?? null,
    };
  },
  async ready() {
    return state.ready;
  },
  fingerprint() {
    const match = (state.pc?.remoteDescription?.sdp ?? '').match(/a=fingerprint:(\S+) (\S+)/);
    return match ? `${match[1]} ${match[2].toUpperCase()}` : null;
  },
  drop() {
    teardown();
    return { dropped: true };
  },
  async open(payload, shared = false) {
    try {
      const stream = await state.link.open(payload, { shared });
      stream.reset('ECANCELED');
      return { opened: stream.opened };
    } catch (error) {
      return describe(error);
    }
  },
  async echo(host, port, size, shared = false) {
    try {
      const stream = await state.link.open({ kind: 'tcp', host, port }, { shared });
      const data = randomBytes(size);
      const writing = stream.write(data).then(() => stream.end());
      const got = await stream.readAll();
      await writing;
      await stream.done;
      return { opened: stream.opened, ok: equal(got, data), bytes: got.byteLength, shared: stream.shared };
    } catch (error) {
      return describe(error);
    }
  },
  async hello() {
    return state.ready;
  },
  async bridge(payload, url) {
    let stream;
    try {
      stream = await state.link.open(payload);
    } catch (error) {
      return describe(error);
    }
    const ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';
    try {
      await new Promise((resolve, reject) => {
        ws.onopen = resolve;
        ws.onerror = () => reject(new Error('websocket failed'));
      });
    } catch (error) {
      stream.reset('ECANCELED');
      return describe(error);
    }
    let writing = Promise.resolve();
    ws.onmessage = (event) => {
      const bytes = new Uint8Array(event.data);
      writing = writing.then(() => stream.write(bytes)).catch(() => ws.close());
    };
    ws.onclose = () => {
      writing.then(() => {
        try {
          stream.end();
        } catch {}
      });
    };
    (async () => {
      try {
        for (let chunk = await stream.read(); chunk; chunk = await stream.read()) ws.send(chunk);
      } catch {}
      ws.close();
    })();
    return { opened: stream.opened };
  },
  async many(n, host, port, size) {
    const results = await Promise.all([...Array(n).keys()].map((i) => this.echo(host, port, size + i, i % 3 === 0)));
    return { ok: results.filter((r) => r.ok).length, shared: results.filter((r) => r.shared).length, failures: results.filter((r) => !r.ok) };
  },
  async slowRead(host, port, delay) {
    try {
      const stream = await state.link.open({ kind: 'tcp', host, port });
      stream.end();
      const parts = [];
      let total = 0;
      for (let chunk = await stream.read(); chunk; chunk = await stream.read()) {
        parts.push(chunk);
        total += chunk.byteLength;
        await sleep(delay);
      }
      const all = new Uint8Array(total);
      let at = 0;
      for (const part of parts) {
        all.set(part, at);
        at += part.byteLength;
      }
      return { bytes: total, sha256: await sha256(all), maxUncredited: stream.maxUncredited };
    } catch (error) {
      return describe(error);
    }
  },
  async overCredit(host, port) {
    const stream = await state.link.open({ kind: 'tcp', host, port });
    stream.sendRaw(Frame.CREDIT, new Uint8Array([0, 0, 0, 9]));
    try {
      await stream.readAll();
      return { error: 'no reset' };
    } catch (error) {
      return describe(error);
    }
  },
  async unknownFrame() {
    await state.ready;
    state.link.sendFrame(state.link.control, new Uint8Array([9, 0, 0, 0, 1]));
    const reason = await Promise.race([state.link.closed, sleep(10000).then(() => 'still open')]);
    return { reason, linkError: String(state.link.error?.message ?? '') };
  },
  async forgeFromFrame(src) {
    const frame = document.createElement('iframe');
    await new Promise((resolve) => {
      frame.onload = resolve;
      frame.src = src;
      document.body.append(frame);
    });
    const reply = new Promise((resolve) => {
      addEventListener('message', (event) => resolve(event.data), { once: true });
    });
    frame.contentWindow.postMessage({ nonce: state.nonce }, '*');
    const result = await reply;
    frame.remove();
    return result;
  },
  async resolve(name, family, shared = true) {
    try {
      return await resolve(state.link, name, family, { shared });
    } catch (error) {
      return describe(error);
    }
  },
  async http(request, shared = false, keep = 1 << 20) {
    try {
      const req = { ...request };
      if (request.body64 !== undefined) req.body = fromBase64(request.body64);
      if (request.head64 !== undefined) req.head = fromBase64(request.head64);
      if (request.prng) req.body = prng(request.prng.size, request.prng.seed);
      const res = await fetchRaw(state.link, req, { shared });
      const parts = [];
      let total = 0;
      for await (const chunk of res.body) {
        parts.push(chunk);
        total += chunk.byteLength;
      }
      await res.sending;
      await res.stream.done;
      const all = concat(parts, total);
      return { ...res.head, shared: res.stream.shared, bytes: total, sha256: await sha256(all), body64: total <= keep ? toBase64(all) : null };
    } catch (error) {
      return describe(error);
    }
  },
  browserUpdates() {
    return state.browserUpdates;
  },
  cdp: cdpTest(() => state.link, describe),
  forgeStale() {
    globalThis.sliccLinkSend(JSON.stringify({ t: 'bye', nonce: 'stale-nonce', reason: 'stale' }));
    return { sent: true };
  },
};
