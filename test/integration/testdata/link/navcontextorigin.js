import { CdpClient } from './cdp.js';
import { SocketClient } from './observer.js';
import { evaluate, outcome, until } from './persistent.js';

export async function measureNavContextOrigin(getLink, opts) {
  const start = Date.now();
  const steps = [];
  const step = (name, data = {}) => steps.push({ name, ms: Date.now() - start, ...data });
  const reader = await SocketClient.open(opts.ws);
  reader.callTimeout = 15000;
  const client = await CdpClient.open(getLink(), opts.browser);
  client.callTimeout = 20000;
  const targets = [];
  const listed = async (targetId) => {
    const list = await outcome(client.send('Target.getTargets', {}));
    const info = list.ok ? (list.result.targetInfos ?? []).find((t) => t.targetId === targetId) : null;
    return info ? info.url : null;
  };
  const fresh = async (targetId, label) => {
    const attach = await outcome(client.send('Target.attachToTarget', { targetId, flatten: true }));
    let probe = null;
    if (attach.ok) {
      probe = await outcome(client.send('Runtime.evaluate', { expression: 'location.href', returnByValue: true }, attach.result.sessionId));
      await outcome(client.send('Target.detachFromTarget', { sessionId: attach.result.sessionId }));
    }
    const row = { label, listedURL: await listed(targetId), attached: attach.ok, attachError: attach.ok ? null : attach.error?.message ?? null, evaluated: Boolean(probe?.ok && !probe.result.exceptionDetails), href: probe?.ok ? probe.result.result?.value ?? null : null, evaluateError: probe && !probe.ok ? probe.error?.message ?? null : null };
    step(`fresh-${label}`, row);
    return row;
  };
  const observe = async (url) => {
    const { targetId } = await reader.send('Target.createTarget', { url, background: true });
    targets.push(targetId);
    const { sessionId } = await reader.send('Target.attachToTarget', { targetId, flatten: true });
    await reader.send('Page.enable', {}, sessionId);
    await reader.send('Runtime.enable', {}, sessionId);
    await until(async () => await evaluate(reader, sessionId, 'location.href') === url && await evaluate(reader, sessionId, 'document.readyState') === 'complete', `document ${url}`, 15000);
    return { targetId, sessionId };
  };
  const navigate = async (tab, url, label) => {
    const from = reader.events.length;
    const nav = await outcome(reader.send('Page.navigate', { url }, tab.sessionId));
    step(`navigate-${label}`, { ok: nav.ok, error: nav.ok ? null : nav.error?.message ?? null });
    await until(async () => await evaluate(reader, tab.sessionId, 'location.href') === url && await evaluate(reader, tab.sessionId, 'document.readyState') === 'complete', `document ${url}`, 20000);
    const origin = new URL(url).origin;
    const events = reader.events.slice(from).filter((e) => e.sessionId === tab.sessionId);
    const commit = events.findIndex((e) => e.method === 'Page.frameNavigated' && e.params.frame.id === tab.targetId && !e.params.frame.parentId && e.params.frame.url === url);
    const context = events.findIndex((e) => e.method === 'Runtime.executionContextCreated' && e.params.context.auxData?.isDefault && e.params.context.auxData.frameId === tab.targetId && e.params.context.origin === origin);
    let order = 'none';
    if (commit >= 0 && context >= 0) order = commit < context ? 'commit-then-context' : 'context-before-commit';
    else if (commit >= 0) order = 'commit-only';
    else if (context >= 0) order = 'context-only';
    const trace = events.filter((e) => /^(Page\.frameNavigated|Page\.frameStartedNavigating|Page\.frameRequestedNavigation|Runtime\.executionContext)/.test(e.method) && (e.params.frame?.id ?? e.params.frameId ?? e.params.context?.auxData?.frameId ?? tab.targetId) === tab.targetId).map((e) => e.method + (e.method === 'Runtime.executionContextCreated' ? `(${e.params.context.auxData?.isDefault ? 'default' : 'other'} ${e.params.context.origin})` : e.method === 'Page.frameNavigated' ? `(${e.params.frame.url})` : ''));
    const measured = { label, order, commitIndex: commit, contextIndex: context, trace };
    step(`loaded-${label}`, measured);
    return measured;
  };
  const released = async (targetId, url, label) => {
    const attempts = [];
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      const row = await fresh(targetId, `${label}-${attempts.length + 1}`);
      attempts.push(row);
      if (row.listedURL === url && row.attached) return { row, attempts };
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    return { row: null, attempts };
  };
  try {
    const seven = await observe(opts.seven);
    step('seven-loaded', { targetId: seven.targetId });
    const sevenBefore = await fresh(seven.targetId, 'seven');
    const sevenOrder = await navigate(seven, opts.afterSeven, 'seven-to-ordinary');
    const sevenAfter = await released(seven.targetId, opts.afterSeven, 'after-seven');
    const tab = await observe(opts.first);
    step('first-loaded', { targetId: tab.targetId });
    const { sessionId: clientSession } = await client.send('Target.attachToTarget', { targetId: tab.targetId, flatten: true });
    const control = await outcome(client.send('Runtime.evaluate', { expression: 'location.href', returnByValue: true }, clientSession));
    step('client-control', { ok: control.ok, href: control.ok ? control.result.result?.value ?? null : null });
    const armed = await (await fetch(`${opts.arm}?skip=${encodeURIComponent(tab.sessionId)}&target=${encodeURIComponent(tab.targetId)}`)).json();
    step('armed', { armed });
    const detached = client.waitEvent((e) => e.method === 'Target.detachedFromTarget' && e.params?.sessionId === clientSession, 20000);
    const markOrder = await navigate(tab, opts.marked, 'to-marked');
    const cut = await outcome(detached);
    const rewritten = await (await fetch(`${opts.arm}?disarm=1`)).json();
    step('disarmed', { rewritten, clientCut: cut.ok });
    const marked = await fresh(tab.targetId, 'marked');
    const releaseOrder = await navigate(tab, opts.second, 'marked-to-ordinary');
    const after = await released(tab.targetId, opts.second, 'after-mark');
    let evaluatedAfter = after.row;
    if (after.row && !(after.row.evaluated && after.row.href === opts.second)) {
      const deadline = Date.now() + 5000;
      while (Date.now() < deadline && !(evaluatedAfter.evaluated && evaluatedAfter.href === opts.second)) {
        await new Promise((resolve) => setTimeout(resolve, 250));
        evaluatedAfter = await fresh(tab.targetId, 'after-mark-evaluate');
      }
    }
    step('done');
    return { steps, sevenBefore, sevenOrder, sevenAfter: sevenAfter.row, sevenAttempts: sevenAfter.attempts.length, control: control.ok, rewritten, clientCut: cut.ok, markOrder, marked, releaseOrder, after: evaluatedAfter, afterAttempts: after.attempts.length };
  } finally {
    for (const targetId of targets) await outcome(reader.send('Target.closeTarget', { targetId }));
    await client.close();
    reader.close();
  }
}
