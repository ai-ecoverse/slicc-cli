const encoder = new TextEncoder();
const decoder = new TextDecoder();

export const WS_TEXT = 1;
export const WS_BINARY = 2;
export const WS_CLOSE = 8;

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export function wsRecord(type, payload, length = payload.byteLength) {
  const out = new Uint8Array(5 + payload.byteLength);
  out[0] = type;
  new DataView(out.buffer).setUint32(1, length);
  out.set(payload, 5);
  return out;
}

export function closePayload(code, reason = '') {
  if (code === null || code === undefined) return new Uint8Array(0);
  const text = encoder.encode(reason);
  const out = new Uint8Array(2 + text.byteLength);
  new DataView(out.buffer).setUint16(0, code);
  out.set(text, 2);
  return out;
}

class Records {
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

  get leftover() {
    return this.#have;
  }
}

export function wsTest(getLink, describe, { sha256, fromBase64, prng }) {
  async function bytesOf(step) {
    if (step.b64 !== undefined) return fromBase64(step.b64);
    if (step.text !== undefined) return encoder.encode(step.text);
    const parts = [];
    let total = 0;
    for await (const part of prng(step.size, step.seed ?? 1)) {
      parts.push(part.slice());
      total += part.byteLength;
    }
    const out = new Uint8Array(total);
    let at = 0;
    for (const part of parts) {
      out.set(part, at);
      at += part.byteLength;
    }
    return out;
  }

  async function summary(type, payload) {
    if (type === WS_CLOSE) {
      if (payload.byteLength < 2) return { type, code: 1005, reason: '', bytes: payload.byteLength };
      return { type, code: new DataView(payload.buffer, payload.byteOffset).getUint16(0), reason: decoder.decode(payload.subarray(2)), bytes: payload.byteLength };
    }
    const out = { type, bytes: payload.byteLength, sha256: await sha256(payload) };
    if (type === WS_TEXT && payload.byteLength <= 65536) out.text = decoder.decode(payload);
    return out;
  }

  async function collect(stream, result, opts) {
    const records = new Records(stream);
    try {
      for (;;) {
        const head = await records.take(5);
        if (!head) break;
        const type = head[0];
        const length = new DataView(head.buffer).getUint32(1);
        const payload = length ? await records.take(length) : new Uint8Array(0);
        if (!payload) {
          result.truncated = true;
          break;
        }
        result.records.push(await summary(type, payload));
        if (type === WS_CLOSE && opts.answerClose && !result.closeSent) {
          result.closeSent = true;
          await stream.write(wsRecord(WS_CLOSE, closePayload(opts.answerClose.code, opts.answerClose.reason ?? '')));
          stream.end();
        }
      }
      result.ended = true;
      result.leftover = records.leftover;
    } catch (error) {
      Object.assign(result, describe(error));
    }
  }

  async function step(stream, s, result) {
    if (s.send) {
      const type = s.send === 'text' ? WS_TEXT : WS_BINARY;
      const payload = await bytesOf(s);
      const bytes = wsRecord(type, payload);
      if (s.pieces) {
        for (let at = 0; at < bytes.byteLength; at += s.pieces) {
          await stream.write(bytes.subarray(at, at + s.pieces));
          await sleep(s.pause ?? 0);
        }
      } else {
        await stream.write(bytes);
      }
    } else if ('close' in s) {
      result.closeSent = true;
      await stream.write(wsRecord(WS_CLOSE, closePayload(s.close, s.reason ?? '')));
    } else if (s.raw) {
      await stream.write(fromBase64(s.raw));
    } else if (s.head) {
      await stream.write(wsRecord(s.head[0], new Uint8Array(0), s.head[1]));
    } else if (s.end) {
      stream.end();
    } else if (s.reset) {
      stream.reset(s.reset);
    } else if (s.wait) {
      while (result.records.length < s.wait && !result.ended && !result.reset && !result.error) await sleep(20);
    } else if (s.sleep) {
      await sleep(s.sleep);
    }
  }

  return {
    async run({ open, shared = false, steps = [], answerClose = null, timeout = 60000 }) {
      let stream;
      try {
        stream = await getLink().open({ kind: 'ws', ...open }, { shared });
      } catch (error) {
        return describe(error);
      }
      const result = { opened: stream.opened, shared: stream.shared, records: [], ended: false };
      const reading = collect(stream, result, { answerClose });
      try {
        for (const s of steps) await step(stream, s, result);
      } catch (error) {
        result.stepError = String(error?.message ?? error);
      }
      const timedOut = await Promise.race([reading.then(() => false), sleep(timeout).then(() => true)]);
      if (timedOut) {
        result.timedOut = true;
        stream.reset('ETIMEDOUT');
      }
      try {
        stream.end();
      } catch {}
      await Promise.race([stream.done, sleep(2000)]);
      return result;
    },
  };
}
