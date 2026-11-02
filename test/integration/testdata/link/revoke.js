const LEASES = 'link-test-leases';
const NOTICES = 'link-test-notices';
const MODE = 'link-test-revoke-mode';

function load(key, empty) {
  return JSON.parse(sessionStorage.getItem(key) ?? empty);
}

function save(key, value) {
  sessionStorage.setItem(key, JSON.stringify(value));
}

export function recordLeases(open, events, node) {
  if (open.op !== 'seal') return;
  const leases = load(LEASES, '{}');
  for (const e of events) {
    if (e.event === 'lease' && e.lease_id) leases[e.lease_id] = { provider: open.provider, node };
  }
  save(LEASES, leases);
}

export function onJerkRevoked(ref) {
  return async (stream) => {
    const mode = sessionStorage.getItem(MODE) ?? 'accept';
    const hello = await ref.link.peerHello;
    const { provider, lease_id: leaseId, by } = stream.open;
    const leases = load(LEASES, '{}');
    const lease = leases[leaseId];
    const acted = mode === 'accept' && Boolean(lease) && lease.provider === provider && lease.node === (hello.node ?? null);
    if (acted) {
      delete leases[leaseId];
      save(LEASES, leases);
    }
    const notices = load(NOTICES, '[]');
    notices.push({ provider, leaseId, by, node: hello.node ?? null, mode, acted, keys: Object.keys(stream.open).sort() });
    save(NOTICES, notices);
    if (mode === 'reset') return stream.reset('EIO: the test page refuses');
    if (mode === 'silent') return undefined;
    stream.accept({});
    stream.end();
    return undefined;
  };
}

export const revokeTest = {
  mode(mode) {
    sessionStorage.setItem(MODE, mode);
    return mode;
  },
  leases() {
    return load(LEASES, '{}');
  },
  notices() {
    return load(NOTICES, '[]');
  },
  rebind(leaseId, node) {
    const leases = load(LEASES, '{}');
    if (leases[leaseId]) leases[leaseId].node = node;
    save(LEASES, leases);
    return leases[leaseId] ?? null;
  },
};
