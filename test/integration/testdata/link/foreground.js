export async function foregroundMouseCall(client, method, params = {}, sessionId) {
  if (sessionId && method === 'Input.dispatchMouseEvent') {
    await client.send('Page.bringToFront', {}, sessionId);
  }
  return client.send(method, params, sessionId);
}
