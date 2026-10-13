export const FIRST_PORT = 17117;
export const LAST_PORT = 17126;
export const PAIR_KEY = 'slicc-pair';
export const PAIR_PATH = '/slicc/v1/pair';
export const SIGNAL_PATH = '/slicc/v1/signal';
export const BACKOFF_MIN = 1000;
export const BACKOFF_MAX = 30000;

const DB_STORE = 'keys';
const CERT_KEY = 'certificate';
const DAY = 24 * 60 * 60 * 1000;

export class LoopbackError extends Error {
  constructor(code, message, status = null) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

export function parsePairing(hash) {
  const m = /^#?slicc-pair=(\d{1,5})\.([A-Za-z0-9_-]{22})\.([0-9a-f]{64})$/.exec(hash ?? '');
  if (!m) return null;
  const port = Number(m[1]);
  if (port < 1 || port > 65535) return null;
  return { port, token: m[2], fingerprint: m[3] };
}

export function takePairing(loc = globalThis.location, hist = globalThis.history) {
  if (!loc.hash.startsWith(`#${PAIR_KEY}=`)) return null;
  const pairing = parsePairing(loc.hash);
  hist.replaceState(hist.state, '', loc.pathname + loc.search);
  return pairing;
}

export function normalizeFingerprint(value) {
  return value.replaceAll(':', '').toLowerCase();
}

export function shortFingerprint(fp) {
  return `${fp.slice(0, 4)}…${fp.slice(-2)}`;
}

export function sdpFingerprint(sdp) {
  let fp = null;
  for (const line of sdp.split(/\r?\n/)) {
    const m = /^a=fingerprint:(\S+) (\S+)$/.exec(line);
    if (!m || m[1].toLowerCase() !== 'sha-256') continue;
    const value = normalizeFingerprint(m[2]);
    if (!/^[0-9a-f]{64}$/.test(value) || (fp && fp !== value)) return null;
    fp = value;
  }
  return fp;
}

function request(req) {
  return new Promise((resolve, reject) => {
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

async function openStore(name) {
  const open = indexedDB.open(name, 1);
  open.onupgradeneeded = () => open.result.createObjectStore(DB_STORE);
  return request(open);
}

export async function loadCertificate(name = 'slicc-loopback') {
  const db = await openStore(name);
  try {
    const stored = await request(db.transaction(DB_STORE).objectStore(DB_STORE).get(CERT_KEY));
    if (stored && stored.expires > Date.now() + DAY) return stored;
    const cert = await RTCPeerConnection.generateCertificate({ name: 'ECDSA', namedCurve: 'P-256' });
    await request(db.transaction(DB_STORE, 'readwrite').objectStore(DB_STORE).put(cert, CERT_KEY));
    return cert;
  } finally {
    db.close();
  }
}

export function certificateFingerprint(cert) {
  const fp = cert.getFingerprints().find((f) => f.algorithm.toLowerCase() === 'sha-256');
  return fp ? normalizeFingerprint(fp.value) : null;
}

export async function gatheredOffer(pc) {
  await pc.setLocalDescription(await pc.createOffer());
  if (pc.iceGatheringState !== 'complete') {
    await new Promise((resolve) => {
      const check = () => {
        if (pc.iceGatheringState !== 'complete') return;
        pc.removeEventListener('icegatheringstatechange', check);
        resolve();
      };
      pc.addEventListener('icegatheringstatechange', check);
    });
  }
  return pc.localDescription.sdp;
}

export async function post(port, path, body, fetchImpl = globalThis.fetch) {
  let res;
  try {
    res = await fetchImpl(`http://127.0.0.1:${port}${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      cache: 'no-store',
      credentials: 'omit',
      mode: 'cors',
    });
  } catch (error) {
    throw new LoopbackError('unreachable', `nothing answered on port ${port}: ${error?.message ?? error}`);
  }
  if (!res.ok) throw new LoopbackError('refused', `slicc on port ${port} refused with ${res.status}`, res.status);
  try {
    return await res.json();
  } catch {
    throw new LoopbackError('malformed', `slicc on port ${port} sent no JSON`, res.status);
  }
}

async function accept(pc, res, pinned, port) {
  if (typeof res?.answer !== 'string') throw new LoopbackError('malformed', `slicc on port ${port} sent no answer`);
  const fp = sdpFingerprint(res.answer);
  if (fp !== pinned) throw new LoopbackError('fingerprint', `the answer on port ${port} is not from the paired slicc (${fp ? shortFingerprint(fp) : 'no fingerprint'})`);
  await pc.setRemoteDescription({ type: 'answer', sdp: res.answer });
  return { port, fingerprint: fp, node: res.node ?? null, name: res.name ?? null };
}

export async function pair(pc, pairing, { fetch } = {}) {
  const offer = await gatheredOffer(pc);
  const res = await post(pairing.port, PAIR_PATH, { token: pairing.token, offer }, fetch);
  return accept(pc, res, pairing.fingerprint, pairing.port);
}

export function signalPorts(pin) {
  const ports = [];
  if (pin.port) ports.push(pin.port);
  for (let p = FIRST_PORT; p <= LAST_PORT; p += 1) if (p !== pin.port) ports.push(p);
  return ports;
}

export async function signal(pc, pin, { fetch, ports = signalPorts(pin) } = {}) {
  const offer = await gatheredOffer(pc);
  let best = null;
  for (const port of ports) {
    try {
      const res = await post(port, SIGNAL_PATH, { offer }, fetch);
      return await accept(pc, res, pin.fingerprint, port);
    } catch (error) {
      if (!(error instanceof LoopbackError)) throw error;
      if (!best || (best.code === 'unreachable' && error.code !== 'unreachable')) best = error;
    }
  }
  throw best ?? new LoopbackError('unreachable', 'no slicc answered');
}

export function backoff(attempt) {
  return Math.min(BACKOFF_MAX, BACKOFF_MIN * 2 ** attempt);
}

export async function unlink(link, hello) {
  if (!hello?.caps?.includes('unlink-loopback')) return false;
  const stream = await link.open({ kind: 'unlink' }, { shared: true });
  stream.end();
  await stream.readAll();
  await stream.done;
  return true;
}
