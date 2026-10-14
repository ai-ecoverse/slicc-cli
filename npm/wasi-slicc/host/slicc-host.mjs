export const PROBE = 0x736c6331

const EAGAIN = 6
const EINPROGRESS = 26
const SOL_SOCKET = 1
const SO_ERROR = 4
const EACCES = 2
const EINVAL = 28
const EIO = 29
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
  const close = fd => {
    try {
      ctx.syscall({ op: 'fd-close', fd })
    } catch (error) {
      fail(error)
    }
  }

  function connect (pointer, size, port) {
    const host = loopbackHost(decoder.decode(view(pointer, size).slice()))
    if (host === null) return -EACCES
    if (!Number.isInteger(port) || port < 1 || port > 65535) return -EINVAL
    let fd
    try {
      fd = ctx.syscall({ op: 'sock-open', domain: 'inet' })
      ctx.syscall({ op: 'sock-connect', fd, addr: { family: 'inet', host, port }, nonblock: true })
      return fd
    } catch (error) {
      if (error?.code === 'EINPROGRESS' && fd !== undefined) return fd
      if (fd !== undefined) close(fd)
      return fail(error)
    }
  }

  function connected (fd, timeoutMs) {
    try {
      const ready = ctx.syscall({ op: 'fd-select', read: [], write: [fd], timeoutMs })
      if (!ready?.write?.length) return -EAGAIN
      const failed = Number(ctx.syscall({ op: 'sock-getopt', fd, level: SOL_SOCKET, name: SO_ERROR }))
      return failed === 0 ? 0 : -(failed === EINPROGRESS ? EAGAIN : failed)
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
