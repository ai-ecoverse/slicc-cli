import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createNodeKernel } from '@ai-ecoverse/slicc-kernel/node';

const [endpoint, page] = process.argv.slice(2);
const kernel = await createNodeKernel({ cdp: async () => {
  const socket = new WebSocket(endpoint);
  const connection = { onmessage: null, onclose: null, send: (message) => socket.send(message), close: () => socket.close() };
  socket.onmessage = (event) => connection.onmessage?.(event.data);
  socket.onclose = () => connection.onclose?.();
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  return connection;
} });
try {
  const manifest = await readFile(new URL('../../../node_modules/@ai-ecoverse/slicc-cdp/package.json', import.meta.url));
  const wasm = await readFile(new URL('../../../node_modules/@ai-ecoverse/slicc-cdp/bin/playwright-cli.wasm', import.meta.url));
  await kernel.writeFile('/node_modules/@ai-ecoverse/slicc-cdp/package.json', manifest);
  await kernel.writeFile('/node_modules/@ai-ecoverse/slicc-cdp/bin/playwright-cli.wasm', wasm, { mode: 0o755 });
  const steps = [];
  const run = async (...args) => {
    const out = await kernel.run(['playwright-cli', ...args], { cwd: '/tmp' });
    steps.push({ args, ...out });
    assert.equal(out.status, 0, JSON.stringify(steps));
    return out.stdout;
  };
  await run('open', 'about:blank');
  await run('goto', page);
  let snapshot = await run('snapshot');
  const ref = (name) => {
    const found = new RegExp(`"${name}"[^\\n]*\\[ref=(e\\d+)\\]`).exec(snapshot)?.[1];
    assert.ok(found, snapshot);
    return found;
  };
  await run('click', ref('Click me'));
  snapshot = await run('snapshot');
  await run('fill', ref('Name'), 'Ada');
  const value = await run('eval', "JSON.stringify({clicked:document.body.dataset.clicked,name:document.querySelector('input').value})");
  assert.match(value, /clicked/);
  assert.match(value, /yes/);
  assert.match(value, /Ada/);
  await run('screenshot', '--filename', '/tmp/relay.png');
  const png = await kernel.readFile('/tmp/relay.png');
  assert.deepEqual([...png.slice(0, 8)], [137, 80, 78, 71, 13, 10, 26, 10]);
  await run('close');
  process.stdout.write(JSON.stringify({ steps, screenshotBytes: png.length })+'\n');
} finally { kernel.terminate(); }
