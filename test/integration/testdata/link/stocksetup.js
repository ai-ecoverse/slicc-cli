import { CdpClient } from './cdp.js';
import { outcome } from './persistent.js';

export async function stockIsolatedWorld(getLink, opts) {
  const client = await CdpClient.open(getLink(), opts.browser);
  client.callTimeout = 15000;
  const { targetId, sessionId } = await client.attachTo(`${opts.ordinary}/doc`);
  try {
    await client.send('Page.enable', {}, sessionId);
    const { frameTree } = await client.send('Page.getFrameTree', {}, sessionId);
    const frameId = frameTree.frame.id;
    const before = client.sent.length;
    const placed = await outcome(
      client.send(
        'Page.createIsolatedWorld',
        {
          frameId,
          worldName: 'slicc-stock-strip',
          grantUniveralAccess: true,
          contentSecurityPolicy: "default-src 'none'",
        },
        sessionId
      )
    );
    const params = JSON.stringify(client.sent[before]?.params ?? {});
    return {
      ok: placed.ok === true,
      error: placed.error?.message ?? '',
      executionContextId: placed.result?.executionContextId ?? 0,
      params,
    };
  } finally {
    await outcome(client.send('Target.closeTarget', { targetId }));
    await client.close();
  }
}

export async function stockScriptRemoveTwice(getLink, opts) {
  const client = await CdpClient.open(getLink(), opts.browser);
  client.callTimeout = 15000;
  const { targetId, sessionId } = await client.attachTo(`${opts.ordinary}/doc`);
  try {
    await client.send('Page.enable', {}, sessionId);
    const added = await client.send(
      'Page.addScriptToEvaluateOnNewDocument',
      { source: '  // stock no-code admit\n' },
      sessionId
    );
    const identifier = added.identifier;
    const first = await outcome(
      client.send('Page.removeScriptToEvaluateOnNewDocument', { identifier }, sessionId)
    );
    const second = await outcome(
      client.send('Page.removeScriptToEvaluateOnNewDocument', { identifier }, sessionId)
    );
    return { identifier, first, second };
  } finally {
    await outcome(client.send('Target.closeTarget', { targetId }));
    await client.close();
  }
}
