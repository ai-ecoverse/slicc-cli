import { CdpClient } from './cdp.js';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function outcome(promise) {
  try {
    return { ok: true, result: await promise };
  } catch (error) {
    return { ok: false, error: error.cdp?.message ?? String(error?.message ?? error) };
  }
}

function countNodes(node, acc = { nodes: 0, frameOwners: 0, contentDocuments: 0, markerHits: 0 }, marker = '') {
  if (!node || typeof node !== 'object') return acc;
  acc.nodes += 1;
  const name = String(node.nodeName || '').toUpperCase();
  if (name === 'IFRAME' || name === 'FRAME') acc.frameOwners += 1;
  if (node.contentDocument) {
    acc.contentDocuments += 1;
    countNodes(node.contentDocument, acc, marker);
  }
  const blob = JSON.stringify(node);
  if (marker && blob.includes(marker)) acc.markerHits += 1;
  for (const child of node.children || []) countNodes(child, acc, marker);
  for (const root of node.shadowRoots || []) countNodes(root, acc, marker);
  if (node.templateContent) countNodes(node.templateContent, acc, marker);
  for (const pe of node.pseudoElements || []) countNodes(pe, acc, marker);
  if (node.importedDocument) countNodes(node.importedDocument, acc, marker);
  for (const d of node.distributedNodes || []) countNodes(d, acc, marker);
  return acc;
}

export async function measureChildDocFlattened(getLink, opts) {
  const c = await CdpClient.open(getLink(), opts.browser);
  c.callTimeout = opts.callTimeoutMs || 30000;
  const started = Date.now();
  const steps = [];
  const note = (name, ok, error = '') => steps.push({ name, ok, error, at: Date.now() - started });
  try {
    const attach = await outcome(c.send('Target.attachToTarget', { targetId: opts.hostTarget, flatten: true }));
    const pageSession = attach.result?.sessionId || '';
    note('guarded-attach', attach.ok, attach.error || '');
    if (!attach.ok || !pageSession) {
      return { steps, getFlattenedOk: false, getFlattenedRefused: true, refused: true, deliveredMarkerAbsent: true, nodeCount: 0, markerHits: 0, elapsedMs: Date.now() - started };
    }
    if (opts.runtimeEnable) {
      const rt = await outcome(c.send('Runtime.enable', {}, pageSession));
      note('guarded-runtime-enable', rt.ok, rt.error || '');
      await sleep(200);
    }
    const dom = await outcome(c.send('DOM.enable', {}, pageSession));
    note('guarded-dom-enable', dom.ok, dom.error || '');
    const flat = await outcome(c.send('DOM.getFlattenedDocument', { depth: -1, pierce: true }, pageSession));
    const refused = !flat.ok;
    note('guarded-getFlattenedDocument', flat.ok, flat.error || '');
    let delivered = '';
    let nodeCount = 0;
    if (flat.ok && Array.isArray(flat.result?.nodes)) {
      delivered = JSON.stringify(flat.result);
      nodeCount = flat.result.nodes.length;
    }
    const markerHits = opts.marker && delivered ? (delivered.split(opts.marker).length - 1) : 0;
    return {
      steps,
      getFlattenedOk: flat.ok,
      getFlattenedRefused: refused,
      refused,
      deliveredMarkerAbsent: markerHits === 0,
      nodeCount,
      markerHits,
      elapsedMs: Date.now() - started,
    };
  } finally {
    await c.close();
  }
}

export async function measureChildDocPierce(getLink, opts) {
  const c = await CdpClient.open(getLink(), opts.browser);
  c.callTimeout = opts.callTimeoutMs || 30000;
  const started = Date.now();
  const steps = [];
  const note = (name, ok, error = '') => steps.push({ name, ok, error, at: Date.now() - started });
  try {
    const attach = await outcome(c.send('Target.attachToTarget', { targetId: opts.hostTarget, flatten: true }));
    const pageSession = attach.result?.sessionId || '';
    note('guarded-attach', attach.ok, attach.error || '');
    if (!attach.ok || !pageSession) {
      return { steps, getDocumentOk: false, getDocumentRefused: true, refused: true, rawMarkerPresent: false, deliveredMarkerAbsent: true, treeNodeCount: 0, frameOwners: 0, contentDocuments: 0, markerHits: 0, elapsedMs: Date.now() - started };
    }
    if (opts.runtimeEnable) {
      const rt = await outcome(c.send('Runtime.enable', {}, pageSession));
      note('guarded-runtime-enable', rt.ok, rt.error || '');
      await sleep(200);
    }
    const dom = await outcome(c.send('DOM.enable', {}, pageSession));
    note('guarded-dom-enable', dom.ok, dom.error || '');
    const doc = await outcome(c.send('DOM.getDocument', { depth: -1, pierce: true }, pageSession));
    const refused = !doc.ok;
    note('guarded-getDocument', doc.ok, doc.error || '');
    let shape = { nodes: 0, frameOwners: 0, contentDocuments: 0, markerHits: 0 };
    let delivered = '';
    if (doc.ok && doc.result?.root) {
      delivered = JSON.stringify(doc.result);
      shape = countNodes(doc.result.root, shape, opts.marker || '');
    }
    const markerHits = opts.marker && delivered ? (delivered.split(opts.marker).length - 1) : shape.markerHits;
    return {
      steps,
      getDocumentOk: doc.ok,
      getDocumentRefused: refused,
      refused,
      deliveredMarkerAbsent: markerHits === 0,
      treeNodeCount: shape.nodes,
      frameOwners: shape.frameOwners,
      contentDocuments: shape.contentDocuments,
      markerHits,
      elapsedMs: Date.now() - started,
    };
  } finally {
    await c.close();
  }
}
