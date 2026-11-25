import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { evaluate, outcome, until } from './persistent.js';

export async function measureContextMark(getLink, opts) {
  const start = Date.now();
  const steps = [];
  const step = (name, data = {}) => steps.push({ name, ms: Date.now() - start, ...data });
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 15000;
  const client = await CdpClient.open(getLink(), opts.browser);
  client.callTimeout = 20000;
  let targetId;
  const fresh = async (label) => {
    const attach = await outcome(client.send('Target.attachToTarget', { targetId, flatten: true }));
    let probe = null;
    if (attach.ok) {
      probe = await outcome(client.send('Runtime.evaluate', { expression: 'location.href', returnByValue: true }, attach.result.sessionId));
      await outcome(client.send('Target.detachFromTarget', { sessionId: attach.result.sessionId }));
    }
    const row = { label, attached: attach.ok, attachError: attach.ok ? null : attach.error?.message ?? null, evaluated: Boolean(probe?.ok && !probe.result.exceptionDetails), href: probe?.ok ? probe.result.result?.value ?? null : null, evaluateError: probe && !probe.ok ? probe.error?.message ?? null : null };
    step(`fresh-${label}`, row);
    return row;
  };
  try {
    ({ targetId } = await reader.send('Target.createTarget', { url: opts.first, background: true }));
    const { sessionId: observer } = await reader.send('Target.attachToTarget', { targetId, flatten: true });
    await until(async () => await evaluate(reader, observer, 'location.href') === opts.first && await evaluate(reader, observer, 'document.readyState') === 'complete', 'first ordinary document', 15000);
    step('first-loaded');
    const control = await fresh('control');
    const { sessionId } = await client.send('Target.attachToTarget', { targetId, flatten: true });
    step('client-attached', { sessionId });
    const ready = await outcome(client.send('Runtime.evaluate', { expression: '1', returnByValue: true }, sessionId));
    step('client-ready', { ok: ready.ok });
    const armed = await (await fetch(`${opts.arm}?session=${encodeURIComponent(sessionId)}&target=${encodeURIComponent(targetId)}`)).json();
    step('armed', { armed });
    const enable = await outcome(client.send('Runtime.enable', {}, sessionId));
    step('client-runtime-enable', { ok: enable.ok, error: enable.ok ? null : enable.error?.message ?? null });
    const detached = await outcome(client.waitEvent((e) => e.method === 'Target.detachedFromTarget' && e.params?.sessionId === sessionId, 10000));
    step('client-detached', { ok: detached.ok });
    const rewritten = await (await fetch(`${opts.arm}?read=1`)).json();
    step('rewrite-count', { rewritten });
    const marked = await fresh('marked');
    await evaluate(reader, observer, 'document.title = "context-mark-title-change"');
    await until(async () => (await reader.send('Target.getTargetInfo', { targetId })).targetInfo.title === 'context-mark-title-change', 'title change in target info', 10000);
    await new Promise((resolve) => setTimeout(resolve, 750));
    step('title-changed');
    const titled = await fresh('after-title-change');
    const nav = await outcome(reader.send('Page.navigate', { url: opts.second }, observer));
    step('navigate-sent', { ok: nav.ok, loaderId: nav.ok ? nav.result.loaderId ?? null : null, error: nav.ok ? null : nav.error?.message ?? null });
    await until(async () => await evaluate(reader, observer, 'location.href') === opts.second && await evaluate(reader, observer, 'document.readyState') === 'complete', 'second ordinary document', 20000);
    step('second-loaded');
    let cleared = null;
    const attempts = [];
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      const row = await fresh(`after-navigation-${attempts.length + 1}`);
      attempts.push(row);
      if (row.attached && row.evaluated && row.href === opts.second) {
        cleared = row;
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    step('done', { cleared: Boolean(cleared) });
    return { steps, control, marked, titled, cleared, attempts, rewritten, detached: detached.ok };
  } finally {
    if (targetId) await outcome(reader.send('Target.closeTarget', { targetId }));
    await client.close();
    reader.close();
  }
}
