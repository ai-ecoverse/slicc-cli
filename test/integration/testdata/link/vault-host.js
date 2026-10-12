const pending = new Map();
let frame = null;
let ready = null;
let nextId = 1;

addEventListener('message', (ev) => {
  if (ev.data?.vaultReady && ready) {
    ready.resolve(ev.data.vaultReady);
    return;
  }
  const p = pending.get(ev.data?.id);
  if (p) {
    pending.delete(ev.data.id);
    p(ev.data.result);
  }
});

export const vaultHost = {
  async embed(url) {
    if (frame) frame.remove();
    let resolve;
    const loaded = new Promise((r) => (resolve = r));
    ready = { resolve };
    frame = document.createElement('iframe');
    frame.allow = 'local-network-access; local-network; loopback-network';
    frame.src = url;
    document.body.append(frame);
    return { origin: await loaded };
  },
  async reload() {
    return this.embed(frame.src);
  },
  remove() {
    if (frame) frame.remove();
    frame = null;
    return {};
  },
  call(op, args) {
    const id = nextId++;
    return new Promise((resolve) => {
      pending.set(id, resolve);
      frame.contentWindow.postMessage({ id, op, args }, new URL(frame.src).origin);
    });
  },
};
