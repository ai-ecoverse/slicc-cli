const encoder = new TextEncoder();
const decoder = new TextDecoder();
const MAX = 64 * 1024 * 1024;

const state = { target: null, refuse: null, opens: 0, live: new Set(), resets: [], ends: 0, methods: [] };

function frame(text) {
  const body = encoder.encode(text);
  const out = new Uint8Array(4 + body.byteLength);
  new DataView(out.buffer).setUint32(0, body.byteLength);
  out.set(body, 4);
  return out;
}

async function* messages(stream) {
  let parts = [];
  let have = 0;
  const take = (n) => {
    const out = new Uint8Array(n);
    let at = 0;
    while (at < n) {
      const head = parts[0];
      const k = Math.min(head.byteLength, n - at);
      out.set(head.subarray(0, k), at);
      at += k;
      if (k === head.byteLength) parts.shift();
      else parts[0] = head.subarray(k);
    }
    have -= n;
    return out;
  };
  let need = null;
  for (let chunk = await stream.read(); chunk; chunk = await stream.read()) {
    parts.push(chunk);
    have += chunk.byteLength;
    for (;;) {
      if (need === null) {
        if (have < 4) break;
        const len = take(4);
        need = new DataView(len.buffer).getUint32(0);
        if (need > MAX) throw new Error('EMSGSIZE');
      }
      if (have < need) break;
      const body = take(need);
      need = null;
      yield decoder.decode(body);
    }
  }
  if (have > 0 || need !== null) throw new Error('EPROTO');
  parts = [];
}

function connect(url) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(url);
    ws.onopen = () => resolve(ws);
    ws.onerror = () => reject(new Error('the test browser refused the socket'));
  });
}

function version(ws) {
  return new Promise((resolve, reject) => {
    ws.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      if (msg.id !== -2) return;
      if (msg.error) reject(new Error(msg.error.message));
      else resolve({ ...msg.result, slicc: { route: 'test' } });
    };
    ws.send(JSON.stringify({ id: -2, method: 'Browser.getVersion' }));
  });
}

export async function onCdpIn(stream) {
  state.opens += 1;
  if (state.refuse) {
    stream.reset(state.refuse);
    return;
  }
  if (!state.target) {
    stream.reset('ENOENT: no browser to drive: install slicc-extension, or link a computer with npx sliccy@next');
    return;
  }
  let ws;
  let opened;
  try {
    ws = await connect(state.target);
    opened = await version(ws);
  } catch (error) {
    ws?.close();
    stream.reset(`ENOENT: ${error.message}`);
    return;
  }
  stream.accept({ version: opened });
  state.live.add(stream);
  let ended = false;
  let writing = Promise.resolve();
  ws.onmessage = (event) => {
    if (/^\{"method":"Network\.webSocketFrame(Received|Sent)"/.test(event.data)) return;
    writing = writing.then(() => stream.write(frame(event.data))).catch(() => {});
  };
  ws.onclose = (event) => {
    if (ended) return;
    writing.then(() => {
      try {
        stream.reset(`ECONNRESET: ${event.reason || 'the browser closed the connection'}`);
      } catch {}
    });
  };
  try {
    for await (const text of messages(stream)) {
      const method = /"method"\s*:\s*"([^"]+)"/.exec(text)?.[1];
      if (method) state.methods.push(method);
      ws.send(text);
    }
    ended = true;
    state.ends += 1;
    ws.close();
    await writing;
    stream.end();
  } catch (error) {
    ended = true;
    state.resets.push(error.code ?? error.message);
    ws.close();
  } finally {
    state.live.delete(stream);
  }
}

export const cdpInTest = {
  target(url) {
    state.target = url;
    return { target: url };
  },
  refuse(reason) {
    state.refuse = reason || null;
    return { refuse: state.refuse };
  },
  revoke(reason) {
    state.refuse = reason;
    const n = state.live.size;
    for (const stream of [...state.live]) stream.reset(reason);
    state.live.clear();
    return { reset: n };
  },
  state() {
    return { opens: state.opens, live: state.live.size, resets: [...state.resets], ends: state.ends, methods: [...state.methods] };
  },
};
