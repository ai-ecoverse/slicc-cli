import { FsClient } from './fs.js';

export function fsTest(link, describe, { sha256, toBase64, fromBase64, prng }) {
  const watches = new Map();
  const opened = new Map();
  let nextWatch = 1;
  const client = () => new FsClient(link());
  const wrap =
    (fn) =>
    async (...args) => {
      try {
        return await fn(...args);
      } catch (error) {
        return describe(error);
      }
    };
  const source = (data) => {
    if (data.body64 !== undefined) return [fromBase64(data.body64)];
    if (data.prng) return prng(data.prng.size, data.prng.seed);
    if (data.text !== undefined) return [new TextEncoder().encode(data.text)];
    return [];
  };
  const summary = async (bytes, keep) => ({
    bytes: bytes.byteLength,
    sha256: await sha256(bytes),
    body64: bytes.byteLength <= keep ? toBase64(bytes) : null,
  });
  return {
    call: wrap((mount, op, args = {}, options = {}) => client().call(mount, op, args, options)),
    raw: wrap(async (payload, shared = true) => {
      const stream = await link().open(payload, { shared });
      const extra = await stream.readAll();
      stream.end();
      await stream.done;
      return { opened: stream.opened, extra: extra.byteLength };
    }),
    list: wrap((mount, path, options = {}) => client().list(mount, path, options)),
    read: wrap(async (mount, fh, offset, size, ifMatch, options = {}, keep = 1 << 16) => {
      const { head, bytes } = await client().read(mount, fh, offset, size, ifMatch ?? undefined, options);
      return { head, ...(await summary(bytes, keep)) };
    }),
    readPages: wrap(async (mount, fh, total, page, ifMatch) => {
      const parts = [];
      let size = 0;
      const heads = [];
      for (let offset = 0; offset < total; offset += page) {
        const { head, bytes } = await client().read(mount, fh, offset, page, ifMatch ?? undefined);
        heads.push(head);
        parts.push(bytes);
        size += bytes.byteLength;
      }
      const all = new Uint8Array(size);
      let at = 0;
      for (const part of parts) {
        all.set(part, at);
        at += part.byteLength;
      }
      return { heads, ...(await summary(all, 0)) };
    }),
    write: wrap((mount, fh, offset, data, options = {}) => client().write(mount, fh, offset, source(data), options)),
    watch: wrap(async (mounts) => {
      const w = await client().watch(mounts);
      const id = nextWatch;
      nextWatch += 1;
      watches.set(id, w);
      return { id, opened: w.stream.opened };
    }),
    openMany: wrap(async (mount, path, n) => {
      const c = client();
      const results = await Promise.all(
        [...Array(n).keys()].map((i) =>
          c.call(mount, 'open', { path }, { shared: i % 2 === 0 }).then(
            (r) => ({ fh: r.fh }),
            (error) => ({ code: error.code ?? String(error) }),
          ),
        ),
      );
      const out = { ok: 0, fhs: [] };
      for (const r of results) {
        if (r.fh) {
          out.ok += 1;
          out.fhs.push(r.fh);
        } else out[r.code] = (out[r.code] ?? 0) + 1;
      }
      opened.set(mount, out.fhs);
      return { ok: out.ok, EMFILE: out.EMFILE ?? 0, other: Object.keys(out).filter((k) => !['ok', 'fhs', 'EMFILE'].includes(k)) };
    }),
    releaseAll: wrap(async (mount) => {
      const c = client();
      const fhs = opened.get(mount) ?? [];
      const results = await Promise.allSettled(fhs.map((fh) => c.call(mount, 'release', { fh })));
      return { ok: results.filter((r) => r.status === 'fulfilled').length };
    }),
    race: wrap(async (mount, outside, rounds) => {
      const c = client();
      const out = { leaks: 0, reads: 0, swaps: 0, codes: {} };
      let running = true;
      const swapper = (async () => {
        try {
          for (let i = 0; i < rounds; i += 1) {
            await c.call(mount, 'rename', { from: 'swap', to: 'swap-real' });
            await c.call(mount, 'symlink', { target: outside, path: 'swap' });
            await c.call(mount, 'unlink', { path: 'swap' });
            await c.call(mount, 'rename', { from: 'swap-real', to: 'swap' });
            out.swaps += 1;
          }
        } finally {
          running = false;
        }
      })();
      const reader = async () => {
        while (running) {
          try {
            const { fh } = await c.call(mount, 'open', { path: 'swap/key.txt' });
            const { bytes } = await c.read(mount, fh, 0, 100);
            out.reads += 1;
            if (new TextDecoder().decode(bytes).includes('top secret')) out.leaks += 1;
            await c.call(mount, 'release', { fh });
          } catch (error) {
            out.codes[error.code] = (out.codes[error.code] ?? 0) + 1;
          }
          try {
            const { entries } = await c.list(mount, 'swap');
            if (entries.some((e) => e.name === 'key.txt' && e.attr.size === 10)) out.leaks += 1;
          } catch (error) {
            out.codes[error.code] = (out.codes[error.code] ?? 0) + 1;
          }
          try {
            const attr = await c.call(mount, 'stat', { path: 'swap/key.txt' });
            if (attr.size === 10) out.leaks += 1;
          } catch (error) {
            out.codes[error.code] = (out.codes[error.code] ?? 0) + 1;
          }
        }
      };
      await Promise.all([swapper, reader(), reader(), reader()]);
      return out;
    }),
    watchState(id) {
      const w = watches.get(id);
      return { lines: w.lines, ended: w.ended };
    },
    watchStop(id) {
      const w = watches.get(id);
      try {
        w.stream.end();
      } catch {}
      return { stopped: true };
    },
  };
}
