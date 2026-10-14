export const CONTROL_LABEL = 'slicc.link.v1';
export const STREAM_LABEL = 'slicc.link.v1.stream';
export const WINDOW = 256 * 1024;
export const MAX_PAYLOAD = 65536;
export const MAX_CHANNELS = 256;
export const Frame = Object.freeze({ OPEN: 1, OPENED: 2, DATA: 3, END: 4, RESET: 5, CREDIT: 6 });

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export class LinkError extends Error {}

export class ResetError extends Error {
  constructor(reason, remote) {
    super(`stream reset (${remote ? 'remote' : 'local'}): ${reason}`);
    const at = reason.indexOf(':');
    this.reason = reason;
    this.code = (at < 0 ? reason : reason.slice(0, at)).trim();
    this.detail = at < 0 ? '' : reason.slice(at + 1).trim();
    this.remote = remote;
  }
}

export function encodeFrame(type, id, payload) {
  const body = payload ?? new Uint8Array(0);
  const out = new Uint8Array(5 + body.byteLength);
  out[0] = type;
  new DataView(out.buffer).setUint32(1, id);
  out.set(body, 5);
  return out;
}

export function decodeFrame(data) {
  const bytes = new Uint8Array(data);
  if (bytes.byteLength < 5) throw new LinkError(`short frame of ${bytes.byteLength} bytes`);
  const type = bytes[0];
  if (type < Frame.OPEN || type > Frame.CREDIT) throw new LinkError(`unknown frame type ${type}`);
  const id = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(1);
  if (id === 0) throw new LinkError('stream id 0');
  const payload = bytes.subarray(5);
  if (type === Frame.END && payload.byteLength !== 0) throw new LinkError('END with a payload');
  if (type === Frame.CREDIT && payload.byteLength !== 4) throw new LinkError('CREDIT without a u32');
  return { type, id, payload };
}

function u32(n) {
  const out = new Uint8Array(4);
  new DataView(out.buffer).setUint32(0, n);
  return out;
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

export class Stream {
  #link;
  #chunks = [];
  #established = false;
  #peerEnded = false;
  #sentEnd = false;
  #outstanding = 0;
  #uncredited = 0;
  #reset = null;
  #finished = false;
  #wakers = [];
  #answered = deferred();
  #done = deferred();

  constructor(link, channel, id, open, local) {
    this.#link = link;
    this.channel = channel;
    this.id = id;
    this.open = open;
    this.kind = open?.kind;
    this.local = local;
    this.opened = null;
    this.maxUncredited = 0;
    this.#answered.promise.catch(() => {});
  }

  get answered() {
    return this.#answered.promise;
  }

  get done() {
    return this.#done.promise;
  }

  get shared() {
    return this.channel === this.#link.control;
  }

  accept(opened = {}) {
    if (this.local || this.#established) throw new LinkError('stream already answered');
    this.#send(Frame.OPENED, encoder.encode(JSON.stringify(opened)));
    this.opened = opened;
    this.#established = true;
    this.#answered.resolve(this);
  }

  async read() {
    while (this.#chunks.length === 0 && !this.#peerEnded && !this.#reset) await this.#sleep();
    if (this.#reset) throw this.#reset;
    if (this.#chunks.length === 0) return null;
    const chunk = this.#chunks.shift();
    this.#credit(chunk.byteLength);
    return chunk;
  }

  async readAll() {
    const parts = [];
    let total = 0;
    for (let chunk = await this.read(); chunk; chunk = await this.read()) {
      parts.push(chunk);
      total += chunk.byteLength;
    }
    const out = new Uint8Array(total);
    let at = 0;
    for (const part of parts) {
      out.set(part, at);
      at += part.byteLength;
    }
    return out;
  }

  async write(bytes) {
    let at = 0;
    while (at < bytes.byteLength) {
      while (!this.#reset && this.#outstanding >= WINDOW) await this.#sleep();
      if (this.#reset) throw this.#reset;
      if (this.#sentEnd) throw new LinkError('write after END');
      if (!this.#established) throw new LinkError('stream not answered');
      const n = Math.min(bytes.byteLength - at, MAX_PAYLOAD, WINDOW - this.#outstanding);
      this.#send(Frame.DATA, bytes.subarray(at, at + n));
      this.#outstanding += n;
      at += n;
    }
  }

  end() {
    if (this.#reset) throw this.#reset;
    if (this.#sentEnd) return;
    this.#send(Frame.END);
    this.#sentEnd = true;
    if (this.#peerEnded) this.#finish();
  }

  reset(reason = 'ECONNRESET') {
    if (this.#finished) return;
    this.#send(Frame.RESET, encoder.encode(reason));
    this.#fail(new ResetError(reason, false));
  }

  sendRaw(type, payload) {
    this.#send(type, payload);
  }

  onFrame(frame) {
    if (this.#finished) return null;
    switch (frame.type) {
      case Frame.OPENED:
        if (!this.local || this.#established) return 'unexpected OPENED';
        this.opened = JSON.parse(decoder.decode(frame.payload));
        this.#established = true;
        this.#answered.resolve(this);
        return null;
      case Frame.DATA:
        if (!this.#established) return 'DATA before OPENED';
        if (this.#peerEnded) return 'DATA after END';
        if (frame.payload.byteLength === 0) return 'empty DATA';
        if (this.#uncredited + frame.payload.byteLength > WINDOW) return 'DATA past the window';
        this.#uncredited += frame.payload.byteLength;
        this.maxUncredited = Math.max(this.maxUncredited, this.#uncredited);
        this.#chunks.push(frame.payload.slice());
        this.#wake();
        return null;
      case Frame.END:
        if (!this.#established) return 'END before OPENED';
        if (this.#peerEnded) return 'END after END';
        this.#peerEnded = true;
        this.#wake();
        if (this.#sentEnd) this.#finish();
        return null;
      case Frame.RESET:
        this.#fail(new ResetError(decoder.decode(frame.payload), true));
        return null;
      case Frame.CREDIT: {
        const n = new DataView(frame.payload.buffer, frame.payload.byteOffset, 4).getUint32(0);
        if (n < 1 || n > this.#outstanding) return 'CREDIT past what was sent';
        this.#outstanding -= n;
        this.#wake();
        return null;
      }
      default:
        return null;
    }
  }

  abort(reason) {
    if (!this.#finished) this.#fail(new ResetError(reason, false));
  }

  #credit(n) {
    this.#uncredited -= n;
    if (!this.#finished && !this.#peerEnded) this.#send(Frame.CREDIT, u32(n));
  }

  #send(type, payload) {
    this.#link.sendFrame(this.channel, encodeFrame(type, this.id, payload));
  }

  #fail(error) {
    this.#reset = error;
    this.#chunks = [];
    this.#answered.reject(error);
    this.#finish();
  }

  #finish() {
    if (this.#finished) return;
    this.#finished = true;
    this.#wake();
    this.#done.resolve();
    this.#link.release(this);
  }

  #sleep() {
    return new Promise((resolve) => this.#wakers.push(resolve));
  }

  #wake() {
    const wakers = this.#wakers;
    this.#wakers = [];
    for (const wake of wakers) wake();
  }
}

export class Link {
  #pc;
  #hello;
  #handlers;
  #nextId = 1;
  #ownChannels = 0;
  #streams = new Map();
  #peerIds = new Set();
  #claimed = new WeakSet();
  #waiting = [];
  #peerHello = deferred();
  #closed = deferred();
  #error = null;

  constructor(pc, { hello = {}, handlers = {} } = {}) {
    this.#pc = pc;
    this.#hello = { v: 1, role: 'page', caps: [], ...hello };
    this.#handlers = handlers;
    this.helloReceived = false;
    this.dropped = [];
    this.#peerHello.promise.catch(() => {});
    this.control = pc.createDataChannel(CONTROL_LABEL);
    this.#wireControl(this.control);
    pc.addEventListener('datachannel', (event) => this.#onDataChannel(event.channel));
    pc.addEventListener('connectionstatechange', () => {
      if (pc.connectionState === 'failed' || pc.connectionState === 'closed') this.close(`connection ${pc.connectionState}`);
    });
  }

  get peerHello() {
    return this.#peerHello.promise;
  }

  get closed() {
    return this.#closed.promise;
  }

  get error() {
    return this.#error;
  }

  get streamCount() {
    return this.#streams.size;
  }

  async open(payload, { shared = false } = {}) {
    await this.#peerHello.promise;
    if (this.#error) throw this.#error;
    const id = this.#nextId;
    this.#nextId += 2;
    let channel = this.control;
    if (!shared && this.#ownChannels < MAX_CHANNELS) {
      channel = await this.#openChannel();
    }
    const stream = new Stream(this, channel, id, payload, true);
    this.#streams.set(id, stream);
    stream.sendRaw(Frame.OPEN, encoder.encode(JSON.stringify(payload)));
    return stream.answered;
  }

  sendFrame(channel, bytes) {
    if (channel.readyState !== 'open') {
      this.dropped.push({ channel: channel.id, state: channel.readyState, frame: bytes[0] });
      return;
    }
    channel.send(bytes);
  }

  release(stream) {
    if (this.#streams.get(stream.id) === stream) this.#streams.delete(stream.id);
    if (stream.channel !== this.control) {
      if (stream.local) this.#ownChannels -= 1;
      stream.channel.close();
    }
  }

  close(reason = 'closed') {
    if (this.#error) return;
    this.#error = new LinkError(reason);
    this.#peerHello.reject(this.#error);
    for (const stream of [...this.#streams.values()]) stream.abort(`ECONNRESET: link ${reason}`);
    this.#streams.clear();
    this.#pc.close();
    this.#closed.resolve(reason);
  }

  #violation(message) {
    this.close(`protocol violation: ${message}`);
  }

  #wireControl(channel) {
    channel.binaryType = 'arraybuffer';
    channel.addEventListener('open', () => channel.send(JSON.stringify(this.#hello)));
    channel.addEventListener('close', () => this.close('control channel closed'));
    channel.addEventListener('message', (event) => {
      if (!this.helloReceived) {
        if (typeof event.data !== 'string') return this.#violation('first control message is not HELLO');
        return this.#onHello(event.data);
      }
      if (typeof event.data === 'string') return this.#violation('text message after HELLO');
      this.#onFrame(channel, event.data);
    });
  }

  #onHello(text) {
    let hello;
    try {
      hello = JSON.parse(text);
    } catch {
      return this.#violation('HELLO is not JSON');
    }
    if (hello.role !== 'node' || !(hello.v >= 1)) return this.#violation('bad HELLO');
    this.helloReceived = true;
    this.#peerHello.resolve(hello);
    const waiting = this.#waiting;
    this.#waiting = [];
    for (const [channel, data] of waiting) this.#onFrame(channel, data);
  }

  #wireStreamChannel(channel) {
    channel.binaryType = 'arraybuffer';
    channel.addEventListener('message', (event) => {
      if (typeof event.data === 'string') return this.#violation('text message on a stream channel');
      if (!this.helloReceived) return this.#waiting.push([channel, event.data]);
      this.#onFrame(channel, event.data);
    });
    channel.addEventListener('close', () => {
      for (const stream of this.#streams.values()) {
        if (stream.channel === channel) stream.abort('ECONNRESET: stream channel closed');
      }
    });
  }

  #onDataChannel(channel) {
    if (channel.label === STREAM_LABEL) return this.#wireStreamChannel(channel);
    channel.close();
  }

  #openChannel() {
    this.#ownChannels += 1;
    const channel = this.#pc.createDataChannel(STREAM_LABEL);
    this.#claimed.add(channel);
    this.#wireStreamChannel(channel);
    return new Promise((resolve, reject) => {
      let opened = false;
      channel.addEventListener(
        'open',
        () => {
          opened = true;
          resolve(channel);
        },
        { once: true },
      );
      channel.addEventListener(
        'close',
        () => {
          if (opened) return;
          this.#ownChannels -= 1;
          reject(new LinkError('stream channel closed'));
        },
        { once: true },
      );
    });
  }

  #onFrame(channel, data) {
    let frame;
    try {
      frame = decodeFrame(data);
    } catch (error) {
      return this.#violation(error.message);
    }
    if (frame.type === Frame.OPEN) return this.#onOpen(channel, frame);
    const stream = this.#streams.get(frame.id);
    if (!stream || stream.channel !== channel) return;
    const violation = stream.onFrame(frame);
    if (violation) stream.reset(`EPROTO: ${violation}`);
  }

  #onOpen(channel, frame) {
    if (frame.id % 2 !== 0) return this.#violation(`OPEN ${frame.id} has the wrong parity`);
    if (this.#peerIds.has(frame.id)) return this.#violation(`duplicate stream id ${frame.id}`);
    this.#peerIds.add(frame.id);
    if (channel !== this.control) {
      if (this.#claimed.has(channel)) return this.#violation('a second OPEN on a stream channel');
      this.#claimed.add(channel);
    }
    let open = null;
    try {
      open = JSON.parse(decoder.decode(frame.payload));
    } catch {
      open = null;
    }
    const stream = new Stream(this, channel, frame.id, open, false);
    this.#streams.set(frame.id, stream);
    const handler = open?.kind ? this.#handlers[open.kind] : null;
    if (!open?.kind) return stream.reset('EPROTO: bad OPEN');
    if (!handler) return stream.reset(`EOPNOTSUPP: ${open.kind}`);
    handler(stream);
  }
}

export function encodeHead(head) {
  const json = encoder.encode(JSON.stringify(head));
  const out = new Uint8Array(4 + json.byteLength);
  new DataView(out.buffer).setUint32(0, json.byteLength);
  out.set(json, 4);
  return out;
}

export async function readHead(stream) {
  let buf = new Uint8Array(0);
  const need = async (n) => {
    while (buf.byteLength < n) {
      const chunk = await stream.read();
      if (!chunk) throw new LinkError('the response ended inside its head');
      const next = new Uint8Array(buf.byteLength + chunk.byteLength);
      next.set(buf);
      next.set(chunk, buf.byteLength);
      buf = next;
    }
  };
  await need(4);
  const size = new DataView(buf.buffer, buf.byteOffset, 4).getUint32(0);
  await need(4 + size);
  return { head: JSON.parse(decoder.decode(buf.subarray(4, 4 + size))), rest: buf.slice(4 + size) };
}

export async function resolve(link, name, family = 0, { shared = true } = {}) {
  const stream = await link.open({ kind: 'resolve', name, family }, { shared });
  const extra = await stream.readAll();
  stream.end();
  await stream.done;
  return { ...stream.opened, extra: extra.byteLength, shared: stream.shared };
}

export async function fetchRaw(link, { url, method = 'GET', headers = [], body = null, head = null }, { shared = false, open = {} } = {}) {
  const stream = await link.open({ ...open, kind: 'http' }, { shared });
  const sending = (async () => {
    await stream.write(head ?? encodeHead({ url, method, headers }));
    if (body instanceof Uint8Array) {
      if (body.byteLength > 0) await stream.write(body);
    } else if (body) {
      for await (const chunk of body) await stream.write(chunk);
    }
    stream.end();
  })();
  sending.catch(() => {});
  const { head: response, rest } = await readHead(stream);
  async function* chunks() {
    if (rest.byteLength > 0) yield rest;
    for (let chunk = await stream.read(); chunk; chunk = await stream.read()) yield chunk;
  }
  return { stream, head: response, sending, body: chunks() };
}
