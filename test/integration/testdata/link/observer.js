export class SocketClient {
  #nextId = 1;
  #pending = new Map();
  #waiters = new Set();
  callTimeout = 0;

  static open(url) {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url);
      ws.onopen = () => resolve(new SocketClient(ws));
      ws.onerror = () => reject(new Error(`can't reach ${url}`));
    });
  }

  constructor(ws) {
    this.ws = ws;
    this.events = [];
    this.messages = [];
    this.sent = [];
    this.transport = 'direct';
    this.listeners = new Set();
    ws.onmessage = (m) => {
      this.messages.push(m.data);
      this.#dispatch(JSON.parse(m.data));
    };
    ws.onclose = () => {
      for (const { reject, timer } of this.#pending.values()) {
        clearTimeout(timer);
        reject(new Error('direct cdp socket closed'));
      }
      this.#pending.clear();
    };
  }

  #dispatch(msg) {
    if (msg.id !== undefined) {
      const call = this.#pending.get(msg.id);
      this.#pending.delete(msg.id);
      if (!call) return;
      clearTimeout(call.timer);
      if (msg.error) {
        const error = new Error(msg.error.message);
        error.cdp = msg.error;
        call.reject(error);
      } else call.resolve(msg.result);
      return;
    }
    this.events.push(msg);
    for (const listener of this.listeners) listener(msg);
    for (const waiter of [...this.#waiters]) {
      if (waiter.match(msg)) {
        this.#waiters.delete(waiter);
        waiter.resolve(msg);
      }
    }
  }

  send(method, params = {}, sessionId) {
    const id = this.#nextId++;
    const msg = { id, method, params };
    if (sessionId) msg.sessionId = sessionId;
    this.sent.push(msg);
    return new Promise((resolve, reject) => {
      const timer = this.callTimeout > 0
        ? setTimeout(() => {
            this.#pending.delete(id);
            reject(new Error(`CDP timed out: ${method}`));
          }, this.callTimeout)
        : undefined;
      this.#pending.set(id, { resolve, reject, timer });
      try {
        this.ws.send(JSON.stringify(msg));
      } catch (error) {
        this.#pending.delete(id);
        clearTimeout(timer);
        reject(error);
      }
    });
  }

  on(listener) {
    this.listeners.add(listener);
  }

  waitEvent(match, timeout = 15000) {
    const seen = this.events.find(match);
    if (seen) return Promise.resolve(seen);
    return new Promise((resolve, reject) => {
      const waiter = { match, resolve };
      this.#waiters.add(waiter);
      setTimeout(() => {
        if (this.#waiters.delete(waiter)) reject(new Error('timed out waiting for a cdp event'));
      }, timeout);
    });
  }

  close() {
    this.ws.close();
  }
}

