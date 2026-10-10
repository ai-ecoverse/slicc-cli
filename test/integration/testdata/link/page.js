import { Frame, Link, ResetError } from './link.js';

const state = { nonce: null, pc: null, link: null, hellos: 0, ready: null, resolveReady: null };

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
  const link = new Link(pc, { hello: { name: 'link test page', caps: [] } });
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
  forgeStale() {
    globalThis.sliccLinkSend(JSON.stringify({ t: 'bye', nonce: 'stale-nonce', reason: 'stale' }));
    return { sent: true };
  },
};
