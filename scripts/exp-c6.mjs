import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, existsSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const chrome = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const srv = createServer((q, s) => { s.writeHead(200, { 'content-type': 'text/html' }); s.end('<p data-canary="x">canary</p>'); });
await new Promise((r) => srv.listen(0, '127.0.0.1', r));
const fport = srv.address().port;
const dir = mkdtempSync(join(tmpdir(), 'exp-c6-'));
const extra = (process.env.EXTRA || '').split(' ').filter(Boolean);
const proc = spawn(chrome, ['--headless=new', '--remote-debugging-port=0', `--user-data-dir=${dir}`, '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-component-update', '--disable-sync', '--use-mock-keychain', '--password-store=basic', '--host-resolver-rules=MAP *.test 127.0.0.1', ...extra, 'about:blank'], { stdio: 'ignore' });
let port;
for (let i = 0; i < 200 && !port; i++) { await new Promise((r) => setTimeout(r, 100)); const f = join(dir, 'DevToolsActivePort'); if (existsSync(f)) port = readFileSync(f, 'utf8').split('\n')[0]; }
const v = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
const ws = new WebSocket(v.webSocketDebuggerUrl);
await new Promise((r) => (ws.onopen = r));
let id = 0;
const wait = new Map();
ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id && wait.has(m.id)) { wait.get(m.id)(m); wait.delete(m.id); } };
const send = (method, params = {}, sessionId) => new Promise((r) => { const i = ++id; wait.set(i, r); ws.send(JSON.stringify({ id: i, method, params, sessionId })); });
const timed = async (p) => { const s = Date.now(); const r = await Promise.race([p, new Promise((x) => setTimeout(() => x({ timeout: true }), 60000))]); return { ms: Date.now() - s, timeout: !!r.timeout, error: r.error?.message }; };
const plant = `(async()=>{localStorage.setItem('canary','x');const db=await new Promise(r=>{const q=indexedDB.open('canary',1);q.onupgradeneeded=()=>q.result.createObjectStore('canary');q.onsuccess=()=>r(q.result);q.onerror=()=>r(null)});if(!db)return false;await new Promise(r=>{const t=db.transaction('canary','readwrite');t.objectStore('canary').put('x','canary');t.oncomplete=r;t.onerror=r});db.close();return true})()`;
async function row(v) {
  const ctx = (await send('Target.createBrowserContext')).result.browserContextId;
  if (v.canary) {
    const { targetId } = (await send('Target.createTarget', { url: 'about:blank', browserContextId: ctx, ...(v.canaryBg ? { background: true } : {}) })).result;
    const cs = (await send('Target.attachToTarget', { targetId, flatten: true })).result.sessionId;
    await send('Page.enable', {}, cs);
    await send('Page.navigate', { url: `http://ordinary.guard.test:${fport}/canary/t` }, cs);
    await new Promise((r) => setTimeout(r, 500));
    if (v.plant) await send('Runtime.evaluate', { expression: plant, awaitPromise: true, returnByValue: true }, cs);
  }
  const { targetId } = (await send('Target.createTarget', { url: 'about:blank', background: v.tabBg, browserContextId: ctx })).result;
  const ps = (await send('Target.attachToTarget', { targetId, flatten: true })).result.sessionId;
  const gs = (await send('Target.attachToTarget', { targetId, flatten: true })).result.sessionId;
  const calls = [
    send('Fetch.enable', { patterns: [{ urlPattern: '*', resourceType: 'Document', requestStage: 'Request' }] }, gs),
    send('Network.enable', { maxTotalBufferSize: 0, maxResourceBufferSize: 0, maxPostDataSize: 0 }, gs),
    send('Network.setBypassServiceWorker', { bypass: true }, gs),
    send('Runtime.enable', {}, gs),
    send('Page.enable', {}, gs),
  ];
  const fetchEnable = await timed(calls[0]);
  const guard = await timed(Promise.all(calls));
  const page = await timed(send('Page.enable', {}, ps));
  await send('Target.disposeBrowserContext', { browserContextId: ctx });
  return { ...v, fetchEnable: fetchEnable.ms, guardMs: guard.ms, guardTimeout: guard.timeout, pageMs: page.ms };
}
const variants = [
  { name: 'runner', canary: true, plant: true, tabBg: true },
  { name: 'no-canary', canary: false, tabBg: true },
  { name: 'canary-no-plant', canary: true, plant: false, tabBg: true },
  { name: 'canary-bg', canary: true, canaryBg: true, plant: true, tabBg: true },
  { name: 'tab-fg', canary: true, plant: true, tabBg: false },
];
for (let rep = 0; rep < 2; rep++) for (const v of variants) console.log(JSON.stringify(await row(v)));
proc.kill();
srv.close();
process.exit(0);
