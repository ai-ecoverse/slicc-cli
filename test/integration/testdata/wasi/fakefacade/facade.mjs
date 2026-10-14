const encoder = new TextEncoder()
const decoder = new TextDecoder()

function record (type, payload = new Uint8Array(0)) {
  const out = new Uint8Array(5 + payload.length)
  out[0] = type
  new DataView(out.buffer).setUint32(1, payload.length)
  out.set(payload, 5)
  return out
}

const control = value => record(1, encoder.encode(JSON.stringify(value)))
const line = event => record(2, encoder.encode(typeof event === 'string' ? event : `${JSON.stringify(event)}\n`))
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))

function reader (readable) {
  const source = readable.getReader()
  let buffer = new Uint8Array(0)
  let done = false
  const fill = async want => {
    while (buffer.length < want && !done) {
      const { value, done: finished } = await source.read()
      if (finished) {
        done = true
        break
      }
      const next = new Uint8Array(buffer.length + value.length)
      next.set(buffer)
      next.set(value, buffer.length)
      buffer = next
    }
    return buffer.length >= want
  }
  return async () => {
    if (!(await fill(5))) return null
    const type = buffer[0]
    const size = new DataView(buffer.buffer, buffer.byteOffset).getUint32(1)
    if (!(await fill(5 + size))) return null
    const payload = buffer.slice(5, 5 + size)
    buffer = buffer.slice(5 + size)
    return { type, payload }
  }
}

async function serve (conn, script, entry, save) {
  const next = reader(conn.readable)
  const writer = conn.writable.getWriter()
  const send = bytes => writer.write(bytes)
  const first = await next()
  entry.first = first ? { type: first.type, json: JSON.parse(decoder.decode(first.payload)) } : null
  await save()
  if (script.silent) {
    await sleep(script.silent)
    await conn.close()
    return
  }
  if (script.garbage) {
    await send(encoder.encode(script.garbage))
    await sleep(200)
    await conn.close()
    return
  }
  await send(control(script.answer))
  if (script.stall) {
    await sleep(300)
    await send(control({ reset: script.stall }))
    await sleep(30000)
    await conn.close()
    return
  }
  if (script.answer.reset !== undefined) {
    await sleep(100)
    await conn.close()
    return
  }
  let resolveEnd
  const clientEnded = new Promise(resolve => { resolveEnd = resolve })
  const pump = (async () => {
    const parts = []
    for (let rec = await next(); rec; rec = await next()) {
      entry.records.push(rec.type)
      await save()
      if (rec.type === 2) {
        entry.bytes += rec.payload.length
        parts.push(rec.payload)
      } else if (rec.type === 3) {
        entry.ended = true
        resolveEnd()
      } else if (rec.type === 1) {
        entry.controls.push(JSON.parse(decoder.decode(rec.payload)))
      }
    }
    const all = new Uint8Array(entry.bytes)
    let at = 0
    for (const p of parts) {
      all.set(p, at)
      at += p.length
    }
    entry.text = decoder.decode(all)
    entry.closed = true
    resolveEnd()
  })().catch(error => {
    entry.closed = true
    entry.readError = String(error?.code ?? error)
    resolveEnd()
  })
  if (script.garbageLater) {
    await sleep(200)
    await send(encoder.encode(script.garbageLater))
  }
  if (script.awaitEnd) await clientEnded
  if (script.awaitEnd && entry.first?.json?.open?.length !== undefined && entry.first.json.open.length !== entry.bytes) {
    await send(control({ reset: 'EPROTO: wrong length' }))
  }
  for (const event of script.send ?? []) {
    if (event.pause) {
      await sleep(event.pause)
      continue
    }
    await send(line(event))
  }
  if (script.control) await send(control(script.control))
  if (script.reset) await send(control({ reset: script.reset }))
  else if (script.end) await send(record(3))
  if (script.hold) {
    await pump
    return
  }
  if (script.drop) {
    await sleep(100)
    await conn.close()
    return
  }
  await Promise.race([clientEnded, sleep(5000)])
  await sleep(100)
  await conn.close()
  await Promise.race([pump, sleep(1000)])
}

export default async function main (ctx) {
  const dir = ctx.argv[1]
  const scripts = JSON.parse(decoder.decode(await ctx.fs.readFile(`${dir}/scenario.json`)))
  const listener = await ctx.net.listen({ port: 0 })
  const log = []
  const save = () => ctx.fs.writeFile(`${dir}/log.json`, JSON.stringify(log))
  await save()
  await ctx.fs.writeFile(`${dir}/port`, String(listener.port))
  for (let n = 0; ; n++) {
    const conn = await listener.accept()
    const script = scripts[Math.min(n, scripts.length - 1)]
    const entry = { records: [], controls: [], bytes: 0, ended: false, closed: false }
    log.push(entry)
    serve(conn, script, entry, save).catch(error => { entry.error = String(error?.message ?? error) }).finally(save)
    await save()
  }
}
