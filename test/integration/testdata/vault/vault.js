const b64 = (bytes) => btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
const hex = (bytes) => [...bytes].map((b) => b.toString(16).padStart(2, '0')).join('');
const fromB64 = (s) => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0));

const state = { pairs: [], mode: sessionStorage.getItem('mode') ?? 'pair' };

function stored() {
  const raw = localStorage.getItem('pairing');
  return raw ? JSON.parse(raw) : null;
}

async function keyId(key) {
  return hex(new Uint8Array(await crypto.subtle.digest('SHA-256', fromB64(key)))).slice(0, 32);
}

async function answer(msg) {
  state.pairs.push(msg);
  if (state.mode === 'refuse') {
    globalThis.sliccVaultSend(JSON.stringify({ t: 'refused', nonce: msg.nonce, reason: 'the test refuses' }));
    return;
  }
  if (state.mode === 'stale') {
    const old = sessionStorage.getItem('lastNonce');
    sessionStorage.setItem('lastNonce', msg.nonce);
    if (old) globalThis.sliccVaultSend(JSON.stringify({ t: 'paired', nonce: old, key: b64(crypto.getRandomValues(new Uint8Array(32))) }));
    return;
  }
  if (state.mode === 'forge') {
    globalThis.sliccVaultSend(JSON.stringify({ t: 'paired', nonce: 'not-the-nonce', key: b64(crypto.getRandomValues(new Uint8Array(32))) }));
    return;
  }
  const have = stored();
  if (have && msg.keyId && msg.keyId === (await keyId(have.key))) {
    localStorage.setItem('pairing', JSON.stringify({ ...have, port: msg.port }));
    globalThis.sliccVaultSend(JSON.stringify({ t: 'paired', nonce: msg.nonce }));
    return;
  }
  const key = b64(crypto.getRandomValues(new Uint8Array(32)));
  localStorage.setItem('pairing', JSON.stringify({ key, port: msg.port, node: msg.node }));
  globalThis.sliccVaultSend(JSON.stringify({ t: 'paired', nonce: msg.nonce, key }));
}

globalThis.sliccVaultReceive = (msg) => {
  answer(msg).catch((e) => state.pairs.push({ error: String(e) }));
};

function base(port) {
  return `http://127.0.0.1:${port ?? stored()?.port}`;
}

async function proofOf(key, challenge) {
  const k = await crypto.subtle.importKey('raw', fromB64(key), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
  const te = new TextEncoder();
  const data = new Uint8Array([...te.encode('slicc-vault/1'), 0, ...te.encode(location.origin), 0, ...te.encode(challenge)]);
  return b64(new Uint8Array(await crypto.subtle.sign('HMAC', k, data)));
}

async function probe({ port, challenge } = {}) {
  const c = challenge ?? b64(crypto.getRandomValues(new Uint8Array(24)));
  const res = await fetch(`${base(port)}/api/fetch-proxy`, { method: 'POST', headers: { 'X-Slicc-Raw-Probe': '1', 'X-Slicc-Vault-Challenge': c } });
  const body = await res.json();
  const have = stored();
  return { status: res.status, body, verified: Boolean(have && body.proof && body.proof === (await proofOf(have.key, c))) };
}

async function rawFetch({ port, req, key, body64 }) {
  const headers = { 'X-Slicc-Raw-Request': JSON.stringify(req).replace(/[\u007f-\uffff]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`) };
  const token = key === undefined ? stored()?.key : key;
  if (token) headers['X-Bridge-Token'] = token;
  const res = await fetch(`${base(port)}/api/fetch-proxy`, { method: 'POST', headers, body: body64 ? fromB64(body64) : undefined });
  const bytes = new Uint8Array(await res.arrayBuffer());
  if (res.headers.get('content-type') !== 'application/vnd.slicc.raw-fetch') {
    return { status: res.status, proxyError: res.headers.get('x-proxy-error'), text: new TextDecoder().decode(bytes) };
  }
  const n = new DataView(bytes.buffer).getUint32(0);
  const head = JSON.parse(new TextDecoder().decode(bytes.subarray(4, 4 + n)));
  return { status: res.status, head, text: new TextDecoder().decode(bytes.subarray(4 + n)) };
}

async function jerk({ port, body, key }) {
  const token = key === undefined ? stored()?.key : key;
  const res = await fetch(`${base(port)}/api/jerk`, { method: 'POST', headers: token ? { 'X-Bridge-Token': token } : {}, body: JSON.stringify(body) });
  return { status: res.status, body: await res.json() };
}

async function raw({ port, path, method = 'POST', headers = {} }) {
  const res = await fetch(`${base(port)}${path}`, { method, headers });
  return { status: res.status, text: await res.text(), acao: res.headers.get('access-control-allow-origin') };
}

const ops = {
  state: async () => ({ origin: location.origin, pairs: state.pairs, stored: stored(), binding: typeof globalThis.sliccVaultSend }),
  mode: async ({ mode }) => {
    sessionStorage.setItem('mode', mode);
    state.mode = mode;
    return { mode };
  },
  forget: async () => {
    localStorage.clear();
    return {};
  },
  probe,
  fetch: rawFetch,
  jerk,
  raw,
};

addEventListener('message', async (ev) => {
  const { id, op, args } = ev.data ?? {};
  if (!ops[op]) return;
  let result;
  try {
    result = await ops[op](args ?? {});
  } catch (e) {
    result = { error: String(e) };
  }
  ev.source.postMessage({ id, result }, ev.origin);
});
parent.postMessage({ vaultReady: location.origin }, '*');
