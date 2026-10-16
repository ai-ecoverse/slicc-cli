const encoder = new TextEncoder();
const decoder = new TextDecoder();

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export function udpRecord(type, host, port, payload, length = payload.byteLength) {
  const h = encoder.encode(host);
  const out = new Uint8Array(9 + h.byteLength + payload.byteLength);
  const view = new DataView(out.buffer);
  out[0] = type;
  view.setUint16(1, h.byteLength);
  out.set(h, 3);
  view.setUint16(3 + h.byteLength, port);
  view.setUint32(5 + h.byteLength, length);
  out.set(payload, 9 + h.byteLength);
  return out;
}

class Bytes {
  #stream;
  #parts = [];
  #have = 0;

  constructor(stream) {
    this.#stream = stream;
  }

  async take(n) {
    while (this.#have < n) {
      const chunk = await this.#stream.read();
      if (!chunk) return null;
      this.#parts.push(chunk);
      this.#have += chunk.byteLength;
    }
    const out = new Uint8Array(n);
    let at = 0;
    while (at < n) {
      const part = this.#parts[0];
      const use = Math.min(part.byteLength, n - at);
      out.set(part.subarray(0, use), at);
      at += use;
      if (use === part.byteLength) this.#parts.shift();
      else this.#parts[0] = part.subarray(use);
    }
    this.#have -= n;
    return out;
  }
}

function pattern(size, seed) {
  const out = new Uint8Array(size);
  for (let i = 0; i < size; i++) out[i] = (i * 7 + seed) % 251;
  return out;
}

export function udpTest(getLink, describe, { sha256, fromBase64 }) {
  const sessions = new Map();
  let next = 1;

  async function collect(sess) {
    const bytes = new Bytes(sess.stream);
    try {
      for (;;) {
        while (sess.paused) await sleep(10);
        const head = await bytes.take(3);
        if (!head) break;
        const hl = new DataView(head.buffer).getUint16(1);
        const host = await bytes.take(hl + 6);
        const view = new DataView(host.buffer);
        const port = view.getUint16(hl);
        const length = view.getUint32(hl + 2);
        const payload = length ? await bytes.take(length) : new Uint8Array(0);
        const rec = { type: head[0], host: decoder.decode(host.subarray(0, hl)), port, bytes: payload.byteLength, sha256: await sha256(payload) };
        if (payload.byteLength <= 4096) rec.text = decoder.decode(payload);
        sess.records.push(rec);
      }
      sess.ended = true;
    } catch (error) {
      Object.assign(sess, describe(error));
    }
  }

  function payloadOf(d) {
    if (d.b64 !== undefined) return fromBase64(d.b64);
    if (d.text !== undefined) return encoder.encode(d.text);
    return pattern(d.size ?? 0, d.seed ?? 1);
  }

  function view(sess) {
    return { records: sess.records, ended: sess.ended, reset: sess.reset ?? '', detail: sess.detail ?? '', error: sess.error ?? '' };
  }

  return {
    async open(open, { shared = false, paused = false } = {}) {
      let stream;
      try {
        stream = await getLink().open({ kind: 'udp', ...open }, { shared });
      } catch (error) {
        return describe(error);
      }
      const id = next++;
      const sess = { stream, records: [], ended: false, paused };
      sess.reading = collect(sess);
      sessions.set(id, sess);
      return { id, opened: stream.opened, shared: stream.shared };
    },
    async send(id, datagrams) {
      const sess = sessions.get(id);
      try {
        for (const d of datagrams) {
          const bytes = d.raw !== undefined ? fromBase64(d.raw) : udpRecord(d.type ?? 1, d.host, d.port, payloadOf(d), d.length);
          await sess.stream.write(bytes);
          if (d.pause) await sleep(d.pause);
        }
        return { ok: true };
      } catch (error) {
        return describe(error);
      }
    },
    resume(id) {
      sessions.get(id).paused = false;
      return { ok: true };
    },
    async wait(id, n = 0, timeout = 10000) {
      const sess = sessions.get(id);
      const until = Date.now() + timeout;
      while (sess.records.length < n && !sess.ended && !sess.reset && !sess.error && Date.now() < until) await sleep(20);
      return view(sess);
    },
    async waitEnd(id, timeout = 10000) {
      const sess = sessions.get(id);
      await Promise.race([sess.reading, sleep(timeout)]);
      return view(sess);
    },
    async end(id, timeout = 10000) {
      const sess = sessions.get(id);
      try {
        sess.stream.end();
      } catch (error) {
        return describe(error);
      }
      await Promise.race([sess.reading, sleep(timeout)]);
      const done = await Promise.race([sess.stream.done.then(() => true, () => true), sleep(timeout).then(() => false)]);
      return { ...view(sess), done };
    },
  };
}
