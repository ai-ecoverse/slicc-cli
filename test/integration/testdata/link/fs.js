const decoder = new TextDecoder();

function lines(bytes) {
  return decoder
    .decode(bytes)
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => JSON.parse(line));
}

export class FsClient {
  constructor(link) {
    this.link = link;
  }

  #open(mount, op, args, { readonly = false, shared = true } = {}) {
    const payload = { kind: 'fs', mount, op, ...args };
    if (readonly) payload.readonly = true;
    return this.link.open(payload, { shared });
  }

  async call(mount, op, args = {}, options = {}) {
    const stream = await this.#open(mount, op, args, options);
    const extra = await stream.readAll();
    stream.end();
    await stream.done;
    return { ...stream.opened, ...(extra.byteLength ? { extra: extra.byteLength } : {}) };
  }

  async list(mount, path, options = {}) {
    const stream = await this.#open(mount, 'list', { path }, options);
    const bytes = await stream.readAll();
    stream.end();
    await stream.done;
    return { opened: stream.opened, entries: lines(bytes) };
  }

  async read(mount, fh, offset, size, ifMatch, options = {}) {
    const args = { fh, offset, size };
    if (ifMatch !== undefined) args.ifMatch = ifMatch;
    const stream = await this.#open(mount, 'read', args, { shared: size <= 65536, ...options });
    const bytes = await stream.readAll();
    stream.end();
    await stream.done;
    return { head: stream.opened, bytes };
  }

  async write(mount, fh, offset, chunks, options = {}) {
    const stream = await this.#open(mount, 'write', { fh, offset }, { shared: false, ...options });
    try {
      for await (const chunk of chunks) await stream.write(chunk);
      stream.end();
    } catch (error) {
      await stream.done;
      throw error;
    }
    await stream.readAll();
    await stream.done;
    return stream.opened;
  }

  async watch(mounts) {
    const stream = await this.link.open({ kind: 'fs', op: 'watch', mounts }, { shared: false });
    const watch = { stream, lines: [], ended: null };
    (async () => {
      let buffered = '';
      try {
        for (let chunk = await stream.read(); chunk; chunk = await stream.read()) {
          buffered += decoder.decode(chunk, { stream: true });
          const parts = buffered.split('\n');
          buffered = parts.pop();
          for (const part of parts) if (part) watch.lines.push(JSON.parse(part));
        }
        watch.ended = 'end';
      } catch (error) {
        watch.ended = error.code ? `reset ${error.code}` : String(error.message ?? error);
      }
    })();
    return watch;
  }
}
