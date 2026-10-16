import { createServer } from 'node:http';
import { fixtureServer } from '@ai-ecoverse/slicc-browsers/fixture';

const fixture = await fixtureServer({ host: '127.0.0.1' });
const entries = [];
const drain = () => {
  entries.push(...fixture.log());
  fixture.reset();
};
const logs = createServer((req, res) => {
  drain();
  const since = Number(new URL(req.url, 'http://log').searchParams.get('since') ?? 0);
  res.writeHead(200, { 'content-type': 'application/json', 'access-control-allow-origin': '*' });
  res.end(JSON.stringify({ from: since, entries: entries.slice(since) }));
});
await new Promise((resolve) => logs.listen(0, '127.0.0.1', resolve));
process.stdout.write(`${JSON.stringify({ fixture: fixture.port, log: logs.address().port })}\n`);
process.stdin.resume();
process.stdin.on('end', async () => {
  logs.close();
  await fixture.close();
  process.exit(0);
});
