export const PROBE = 0x736c6331

const EAGAIN = 6
const EINPROGRESS = 26
const EACCES = 2
const EINVAL = 28
const EIO = 29
const ENOSYS = 52
const decoder = new TextDecoder()

export function loopbackHost (host) {
  if (host === 'localhost') return '127.0.0.1'
  return /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(host) ? host : null
}

export function createImports (ctx) {
  const bytes = () => new Uint8Array(ctx.memory().buffer)
  const view = (pointer, size) => bytes().subarray(pointer >>> 0, (pointer >>> 0) + (size >>> 0))
  const fail = error => {
    if (typeof error?.code !== 'string') throw error
    return -(ctx.errno(error) || EIO)
  }

  function connect (pointer, size, port) {
    const host = loopbackHost(decoder.decode(view(pointer, size).slice()))
    if (host === null) return -EACCES
    if (!Number.isInteger(port) || port < 1 || port > 65535) return -EINVAL
    if (typeof ctx.net?.connect !== 'function') return -ENOSYS
    try {
      return ctx.net.connect({ host, port, nonblock: true })
    } catch (error) {
      return fail(error)
    }
  }

  function connected (fd, timeoutMs) {
    try {
      let state = ctx.net.ready(fd)
      if (state === EINPROGRESS) {
        ctx.syscall({ op: 'fd-select', read: [], write: [fd], timeoutMs })
        state = ctx.net.ready(fd)
      }
      if (state === EINPROGRESS) return -EAGAIN
      return state === 0 ? 0 : -(Number(state) || EIO)
    } catch (error) {
      return fail(error)
    }
  }

  function read (fd, pointer, size, timeoutMs) {
    try {
      if (timeoutMs >= 0) {
        const ready = ctx.syscall({ op: 'fd-select', read: [fd], write: [], timeoutMs })
        if (!ready?.read?.length) return -EAGAIN
      }
      const chunk = ctx.syscall({ op: 'fd-read', fd, max: size >>> 0 })
      if (!(chunk instanceof Uint8Array) || chunk.length === 0) return 0
      bytes().set(chunk, pointer >>> 0)
      return chunk.length
    } catch (error) {
      return fail(error)
    }
  }

  function write (fd, pointer, size, timeoutMs) {
    try {
      const ready = ctx.syscall({ op: 'fd-select', read: [], write: [fd], timeoutMs })
      if (!ready?.write?.length) return -EAGAIN
      return Number(ctx.syscall({ op: 'fd-write', fd, body: view(pointer, size).slice(), nonblock: true })) || 0
    } catch (error) {
      return fail(error)
    }
  }

  function shut (fd) {
    try {
      ctx.syscall({ op: 'fd-close', fd })
      return 0
    } catch (error) {
      return fail(error)
    }
  }

  return { slicc_host: { probe: () => PROBE, connect, connected, read, write, close: shut } }
}
