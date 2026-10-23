import { CdpClient, frameMessage } from './cdp.js';

export async function playwrightRelay(getLink, url, browser) {
  const socket = new WebSocket(url);
  const clients = new Map();
  socket.onmessage = async (event) => {
    const request = JSON.parse(event.data);
    try {
      if (request.open) {
        clients.set(request.id, CdpClient.open(getLink(), browser).then((client) => {
          client.onMessage((message) => socket.send(JSON.stringify({ id: request.id, message })));
          return client;
        }));
        return;
      }
      const client = await clients.get(request.id);
      if (request.close) { clients.delete(request.id); await client?.close(); }
      else if (request.message) await client.write(frameMessage(request.message));
    } catch (error) { socket.send(JSON.stringify({ id: request.id, error: error.message })); }
  };
  socket.onclose = () => { for (const client of clients.values()) client.then((c) => c.close()).catch(() => {}); };
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  return true;
}
