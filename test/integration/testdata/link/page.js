import { kernel, onKernel } from './kernel.js';
import { Frame, Link, ResetError, fetchRaw, resolve } from './link.js';
import { cdpTest } from './cdp.js';
import { protectTest } from './protect.js';
import { fsTest } from './fs-test.js';
import { agentTest, onAgent } from './agent.js';
import { wsTest } from './ws.js';
import { udpTest } from './udp.js';
import { LoopbackError, backoff, certificateFingerprint, loadCertificate, pair as loopbackPair, post as loopbackPost, signal as loopbackSignal, takePairing, unlink as loopbackUnlink } from './loopback.js';

const params = new URLSearchParams(location.search);
const kernelIn = params.has('kernel');
const pendingPairing = takePairing();

const state = { nonce: null, pc: null, link: null, hellos: 0, ready: null, resolveReady: null, browserUpdates: [], mountUpdates: [], entries: new Map(), current: null };

function onBrowsers(stream) {
  state.browserUpdates.push(stream.open.browsers);
  stream.accept({});
  stream.end();
}

function onMounts(stream) {
  state.mountUpdates.push(stream.open.mounts);
  stream.accept({});
  stream.end();
}

function freshReady(entry) {
  entry.ready = new Promise((resolve) => {
    entry.resolveReady = resolve;
  });
}

function select(entry) {
  state.current = entry;
  state.nonce = entry?.nonce ?? null;
  state.pc = entry?.pc ?? null;
  state.link = entry?.link ?? null;
  if (entry) state.ready = entry.ready;
}

select(null);

function sendFor(entry, msg) {
  globalThis.sliccLinkSend(JSON.stringify({ ...msg, nonce: entry.nonce }));
}

function send(msg) {
  if (state.current) sendFor(state.current, msg);
}

function live() {
  return [...state.entries.values()].filter((e) => !e.link?.error);
}

function successor(entry) {
  const others = live().filter((e) => e !== entry);
  return others.filter((e) => e.link?.helloReceived).pop() ?? others.pop() ?? null;
}

function remove(entry) {
  if (!entry) return;
  entry.link?.close('replaced');
  if (state.entries.get(entry.key) === entry) state.entries.delete(entry.key);
  if (state.current === entry) select(successor(entry));
}

function closed(entry) {
  const fallback = successor(entry);
  if (state.current === entry && !fallback) return;
  if (state.entries.get(entry.key) === entry) state.entries.delete(entry.key);
  if (state.current === entry) select(fallback);
}

function teardown() {
  remove(state.current);
}

function pageCaps() {
  const caps = ['browsers', 'mounts', 'agent'];
  if (kernelIn) caps.push('kernel-in');
  return caps;
}

function pageHandlers() {
  const handlers = { browsers: onBrowsers, mounts: onMounts, agent: onAgent };
  if (kernelIn) handlers.kernel = onKernel;
  return handlers;
}

async function setup(nonce, node) {
  const key = node ?? `nonce:${nonce}`;
  remove(state.entries.get(key));
  const entry = { key, node: node ?? null, nonce, pc: null, link: null };
  freshReady(entry);
  state.hellos += 1;
  const pc = new RTCPeerConnection();
  const caps = pageCaps().filter((c) => c !== 'agent' || agentTest.state().allow);
  const link = new Link(pc, { hello: { name: 'link test page', caps }, handlers: pageHandlers() });
  entry.pc = pc;
  entry.link = link;
  state.entries.set(key, entry);
  select(entry);
  const early = [];
  let offered = false;
  pc.addEventListener('icecandidate', (event) => {
    const msg = { t: 'candidate', candidate: event.candidate ? event.candidate.toJSON() : null };
    if (offered) sendFor(entry, msg);
    else early.push(msg);
  });
  await pc.setLocalDescription(await pc.createOffer());
  sendFor(entry, { t: 'offer', sdp: pc.localDescription.sdp });
  offered = true;
  for (const msg of early) sendFor(entry, msg);
  link.closed.then(() => closed(entry), () => closed(entry));
  link.peerHello.then((hello) => {
    entry.hello = hello;
    if (!entry.node) replaceSameDevice(entry);
    entry.resolveReady(hello);
  }, () => {});
}

function remoteFingerprint(entry) {
  return (entry.pc?.remoteDescription?.sdp ?? '').match(/a=fingerprint:\S+ (\S+)/)?.[1] ?? null;
}

function replaceSameDevice(entry) {
  const fp = remoteFingerprint(entry);
  for (const other of [...state.entries.values()]) {
    if (other === entry || other.node || !other.hello) continue;
    if (remoteFingerprint(other) === fp && (other.hello.node ?? null) === (entry.hello.node ?? null)) remove(other);
  }
}

function byNonce(nonce) {
  for (const entry of state.entries.values()) if (entry.nonce === nonce) return entry;
  return null;
}

globalThis.sliccLinkReceive = (raw) => {
  const msg = typeof raw === 'string' ? JSON.parse(raw) : raw;
  if (msg.t === 'hello') return void setup(msg.nonce, msg.node);
  const entry = byNonce(msg.nonce);
  if (!entry?.pc) return;
  if (msg.t === 'answer') entry.pc.setRemoteDescription({ type: 'answer', sdp: msg.sdp });
  if (msg.t === 'candidate' && msg.candidate) entry.pc.addIceCandidate(msg.candidate);
  if (msg.t === 'bye') remove(entry);
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
  if (error instanceof LoopbackError) return { error: error.message, code: error.code, status: error.status };
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

const PINS = 'slicc-loopback-pins';

function loadPins() {
  try {
    return JSON.parse(localStorage.getItem(PINS) ?? '{}');
  } catch {
    return {};
  }
}

function savePin(pin) {
  const pins = loadPins();
  pins[pin.fingerprint] = pin;
  localStorage.setItem(PINS, JSON.stringify(pins));
}

function scanPorts(pin) {
  const range = /^(\d+)-(\d+)$/.exec(params.get('ports') ?? '');
  if (!range) return undefined;
  const ports = [pin.port];
  for (let p = Number(range[1]); p <= Number(range[2]); p += 1) if (p !== pin.port) ports.push(p);
  return ports;
}

let certificate = null;

async function loopbackLink(run) {
  certificate ??= await loadCertificate();
  const pc = new RTCPeerConnection({ certificates: [certificate] });
  const caps = pageCaps().filter((c) => c !== 'agent' || agentTest.state().allow);
  const link = new Link(pc, { hello: { name: 'link test page', caps }, handlers: pageHandlers() });
  let info;
  try {
    info = await run(pc);
  } catch (error) {
    link.close('setup failed');
    pc.close();
    return describe(error);
  }
  const key = `loopback:${info.fingerprint}`;
  remove(state.entries.get(key));
  const entry = { key, node: null, nonce: null, pc, link, loopback: info };
  freshReady(entry);
  state.hellos += 1;
  state.entries.set(key, entry);
  select(entry);
  link.closed.then(() => closed(entry), () => closed(entry));
  link.peerHello.then((hello) => {
    entry.hello = hello;
    entry.resolveReady(hello);
  }, () => {});
  return { ...info, linked: true };
}

const loopbackTest = {
  pending() {
    return pendingPairing;
  },
  async fingerprint() {
    certificate ??= await loadCertificate();
    return certificateFingerprint(certificate);
  },
  async confirm(pairing = pendingPairing) {
    if (!pairing) return { error: 'no pairing' };
    const res = await loopbackLink((pc) => loopbackPair(pc, pairing));
    if (res.linked) savePin({ fingerprint: pairing.fingerprint, port: res.port, name: res.name });
    return res;
  },
  pins() {
    return loadPins();
  },
  close() {
    const entry = state.current;
    if (!entry?.loopback) return { error: 'not linked over loopback' };
    entry.link.close('closed by the test');
    entry.pc.close();
    return { closed: true };
  },
  forget() {
    localStorage.removeItem(PINS);
    return { forgot: true };
  },
  async unlink() {
    const entry = state.current;
    const fp = entry?.loopback?.fingerprint;
    if (!fp) return { error: 'not linked over loopback' };
    const pins = loadPins();
    delete pins[fp];
    localStorage.setItem(PINS, JSON.stringify(pins));
    try {
      const sent = await loopbackUnlink(entry.link, await entry.ready);
      const reason = await Promise.race([entry.link.closed.then(() => 'closed', () => 'closed'), sleep(10000).then(() => 'still open')]);
      return { sent, reason };
    } catch (error) {
      return describe(error);
    }
  },
  async signal(fingerprint) {
    const pins = loadPins();
    const pin = fingerprint ? pins[fingerprint] : Object.values(pins).pop();
    if (!pin) return { error: 'not paired' };
    const res = await loopbackLink((pc) => loopbackSignal(pc, pin, { ports: scanPorts(pin) }));
    if (res.linked) savePin({ ...pin, port: res.port, name: res.name });
    return res;
  },
  async reconnect(fingerprint, deadline = 60000) {
    const until = Date.now() + deadline;
    for (let attempt = 0; ; attempt += 1) {
      const res = await this.signal(fingerprint);
      if (res.linked || Date.now() > until) return { ...res, attempts: attempt + 1 };
      await sleep(Math.min(backoff(attempt), 2000));
    }
  },
  async post(port, path, body) {
    try {
      return { ok: true, body: await loopbackPost(port, path, body) };
    } catch (error) {
      return describe(error);
    }
  },
  async offerSdp() {
    certificate ??= await loadCertificate();
    const pc = new RTCPeerConnection({ certificates: [certificate] });
    pc.createDataChannel('slicc.link.v1');
    await pc.setLocalDescription(await pc.createOffer());
    const sdp = pc.localDescription.sdp;
    pc.close();
    return sdp;
  },
};

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
    while (!state.current) await sleep(50);
    return state.current.ready;
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
    return this.ready();
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
  async http(request, shared = false, keep = 1 << 20, open = {}) {
    try {
      const req = { ...request };
      if (request.body64 !== undefined) req.body = fromBase64(request.body64);
      if (request.head64 !== undefined) req.head = fromBase64(request.head64);
      if (request.prng) req.body = prng(request.prng.size, request.prng.seed);
      const res = await fetchRaw(state.link, req, { shared, open });
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
  async jerk(open, shared = false) {
    try {
      const stream = await state.link.open({ ...open, kind: 'jerk' }, { shared });
      stream.end();
      const text = new TextDecoder().decode(await stream.readAll());
      await stream.done;
      return { opened: stream.opened, events: text.split('\n').filter(Boolean).map((l) => JSON.parse(l)) };
    } catch (error) {
      return describe(error);
    }
  },
  async jerkPair(a, b, delay = 500) {
    const first = this.jerk(a);
    await new Promise((resolve) => setTimeout(resolve, delay));
    return Promise.all([first, this.jerk(b)]);
  },
  browserUpdates() {
    return state.browserUpdates;
  },
  mountUpdates() {
    return state.mountUpdates;
  },
  links() {
    return [...state.entries.values()].map((e) => ({
      node: e.node,
      helloNode: e.hello?.node ?? null,
      up: Boolean(e.link?.helloReceived && !e.link?.error),
      caps: e.hello?.caps ?? null,
      current: e === state.current,
    }));
  },
  agent: agentTest,
  loopback: loopbackTest,
  cdp: cdpTest(() => state.link, describe),
  playwrightRelay: (url, browser) => import('./playwright.js').then((m) => m.playwrightRelay(() => state.link, url, browser)),
  laterAdmission: (opts) => import('./persistent.js').then((m) => m.laterAdmissionProof(() => state.link, opts)),
  stickyAdmission: (opts) => import('./sticky.js').then((m) => m.stickyAdmissionProof(() => state.link, opts)),
  resourceBlock: (opts) => import('./resource-block.js').then((m) => m.resourceBlockRow(() => state.link, opts)),
  sticky: (opts) => import('./sticky.js').then((m) => m.measureSticky(() => state.link, opts)),
  persistent: (opts) => import('./persistent.js').then((m) => m.measurePersistent(() => state.link, opts)),
  protect: protectTest(() => state.link, describe),
  guard: {
    start: (opts) => import('./guard.js').then((m) => m.guardStart(() => state.link, opts)),
    progress: (since) => import('./guard.js').then((m) => m.guardProgress(since)),
  },
  fs: fsTest(() => state.link, describe, { sha256, toBase64, fromBase64, prng }),
  ws: wsTest(() => state.link, describe, { sha256, fromBase64, prng }),
  udp: udpTest(() => state.link, describe, { sha256, fromBase64 }),
  async transport() {
    const out = { dropped: state.link?.dropped ?? [], streams: state.link?.streamCount ?? 0, channels: [], pairs: [] };
    const stats = await state.pc?.getStats();
    stats?.forEach((r) => {
      if (r.type === 'data-channel') {
        out.channels.push({ id: r.dataChannelIdentifier, label: r.label, state: r.state, sent: r.messagesSent, received: r.messagesReceived });
      } else if (r.type === 'candidate-pair' && (r.nominated || r.state === 'succeeded')) {
        out.pairs.push({ id: r.id, state: r.state, nominated: r.nominated, sent: r.packetsSent, received: r.packetsReceived, rtt: r.currentRoundTripTime, lastSent: r.lastPacketSentTimestamp, lastReceived: r.lastPacketReceivedTimestamp });
      } else if (r.type === 'transport') {
        out.pairChanges = r.selectedCandidatePairChanges;
        out.pair = r.selectedCandidatePairId;
      }
    });
    return out;
  },
  kernel() {
    return { opens: kernel.opens, hanging: kernel.hanging.length, requests: kernel.requests };
  },
  drop() {
    send({ t: 'bye', reason: 'dropped by the test' });
    teardown();
    return { dropped: true };
  },
  forgeStale() {
    globalThis.sliccLinkSend(JSON.stringify({ t: 'bye', nonce: 'stale-nonce', reason: 'stale' }));
    return { sent: true };
  },
};
