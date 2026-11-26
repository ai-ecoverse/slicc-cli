export function gateAuditComplete(ledger, probes) {
  if (typeof ledger?.invalidFrames !== 'number') return false;
  if (!Array.isArray(ledger?.probes) || !Array.isArray(probes)) return false;
  return (
    ledger.invalidFrames === 0 &&
    ledger.probes.length === probes.length &&
    ledger.probes.every(
      (entry, i) =>
        entry.id === probes[i].id &&
        typeof entry.forwarded === 'boolean' &&
        (entry.forwarded ||
          probes[i].reply?.ok === true ||
          Number.isInteger(probes[i].reply?.error?.code))
    )
  );
}

export async function delayedInboundAudit(control, methods, sessionId, clientSent, start, probes) {
  const sent = clientSent
    .slice(start)
    .filter((m) => m.sessionId === sessionId && methods.has(m.method));
  const matched =
    probes.length === sent.length &&
    probes.every(
      (p, i) =>
        p.method === sent[i].method && JSON.stringify(p.params) === JSON.stringify(sent[i].params)
    );
  if (!matched) return { complete: false, probes: [] };
  const ledger = await control({
    action: 'audit',
    sessionId,
    probes: probes.map((p, i) => ({ id: p.id, wireId: sent[i].id })),
  });
  return { ...ledger, complete: gateAuditComplete(ledger, probes) };
}
