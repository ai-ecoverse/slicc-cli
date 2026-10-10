const encoder = new TextEncoder();
const decoder = new TextDecoder();
const WS_GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11';

export const kernel = { opens: [], hanging: [], requests: 0 };

class Reader {
  #stream;
  #buf = new Uint8Array(0);
  #eof = false;

  constructor(stream) {
    this.#stream = stream;
  }

  async #fill() {
    if (this.#eof) return false;
    const chunk = await this.#stream.read();
    if (!chunk) {
      this.#eof = true;
      return false;
    }
    const next = new Uint8Array(this.#buf.byteLength + chunk.byteLength);
    next.set(this.#buf);
    next.set(chunk, this.#buf.byteLength);
    this.#buf = next;
    return true;
  }

  async head() {
    for (;;) {
      const at = indexOf(this.#buf, [13, 10, 13, 10]);
      if (at >= 0) {
        const text = decoder.decode(this.#buf.subarray(0, at));
        this.#buf = this.#buf.slice(at + 4);
        return text;
      }
      if (!(await this.#fill())) return null;
    }
  }

  async take(n) {
    while (this.#buf.byteLength < n) {
      if (!(await this.#fill())) return null;
    }
    const out = this.#buf.slice(0, n);
    this.#buf = this.#buf.slice(n);
    return out;
  }
}

function indexOf(bytes, needle) {
  outer: for (let i = 0; i + needle.length <= bytes.byteLength; i += 1) {
    for (let j = 0; j < needle.length; j += 1) if (bytes[i + j] !== needle[j]) continue outer;
    return i;
  }
  return -1;
}

function parse(text) {
  const [line, ...fields] = text.split('\r\n');
  const [method, path] = line.split(' ');
  const headers = {};
  for (const field of fields) {
    const at = field.indexOf(':');
    headers[field.slice(0, at).trim().toLowerCase()] = field.slice(at + 1).trim();
  }
  return { method, path, headers };
}

async function wsAccept(key) {
  const digest = await crypto.subtle.digest('SHA-1', encoder.encode(key + WS_GUID));
  return btoa(String.fromCharCode(...new Uint8Array(digest)));
}

function wsFrame(opcode, payload) {
  const n = payload.byteLength;
  const head = n < 126 ? [0x80 | opcode, n] : n < 65536 ? [0x80 | opcode, 126, n >> 8, n & 255] : null;
  if (!head) throw new Error('frame too large');
  const out = new Uint8Array(head.length + n);
  out.set(head);
  out.set(payload, head.length);
  return out;
}

async function websocket(stream, reader, req) {
  const accept = await wsAccept(req.headers['sec-websocket-key']);
  await stream.write(
    encoder.encode(
      `HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`,
    ),
  );
  for (;;) {
    const h = await reader.take(2);
    if (!h) break;
    const opcode = h[0] & 15;
    let len = h[1] & 127;
    if (len === 126) {
      const ext = await reader.take(2);
      len = (ext[0] << 8) | ext[1];
    } else if (len === 127) {
      const ext = await reader.take(8);
      len = Number(new DataView(ext.buffer).getBigUint64(0));
    }
    const mask = h[1] & 128 ? await reader.take(4) : null;
    const payload = len ? await reader.take(len) : new Uint8Array(0);
    if (mask) for (let i = 0; i < payload.byteLength; i += 1) payload[i] ^= mask[i & 3];
    if (opcode === 8) {
      await stream.write(wsFrame(8, payload));
      break;
    }
    if (opcode === 9) await stream.write(wsFrame(10, payload));
    else if (opcode === 1) await stream.write(wsFrame(1, encoder.encode(`echo: ${decoder.decode(payload)}`)));
    else if (opcode === 2) await stream.write(wsFrame(2, payload));
  }
  stream.end();
}

async function http(stream, port) {
  const reader = new Reader(stream);
  let served = 0;
  for (;;) {
    const text = await reader.head();
    if (text === null) break;
    const req = parse(text);
    if ((req.headers.upgrade ?? '').toLowerCase() === 'websocket') return websocket(stream, reader, req);
    const length = Number(req.headers['content-length'] ?? 0);
    const body = length ? decoder.decode(await reader.take(length)) : '';
    served += 1;
    kernel.requests += 1;
    const out = encoder.encode(
      JSON.stringify({ port, stream: stream.id, served, method: req.method, path: req.path, host: req.headers.host, body }),
    );
    const close = (req.headers.connection ?? '').toLowerCase() === 'close';
    await stream.write(
      encoder.encode(
        `HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: ${out.byteLength}\r\nConnection: ${close ? 'close' : 'keep-alive'}\r\n\r\n`,
      ),
    );
    await stream.write(out);
    if (close) break;
  }
  stream.end();
}

export function onKernel(stream) {
  const port = stream.open?.port;
  kernel.opens.push(port);
  if (port === 5173) {
    stream.accept({});
    http(stream, port).catch(() => stream.reset('EIO'));
  } else if (port === 5174) {
    stream.reset('EACCES: reserved \u0007by the kernel');
  } else if (port === 5175) {
    kernel.hanging.push(stream);
  } else {
    stream.reset('ECONNREFUSED');
  }
}
