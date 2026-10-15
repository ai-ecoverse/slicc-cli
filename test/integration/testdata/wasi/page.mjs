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
const line = event => record(2, encoder.encode(`${JSON.stringify(event)}\n`))

export function linkPage ({ sudo }) {
  const page = { scenario: [], log: [] }
  const allowed = stamp => stamp.users === 'enforce' && (stamp.uid === 0 || stamp.groups.includes(sudo))

  page.hook = (stamp, info) => {
    const entry = { stamp, facade: info.name, records: [], controls: [], bytes: 0, ended: false, closed: false, first: null }
    const script = page.scenario[Math.min(page.log.length, page.scenario.length - 1)] ?? { answer: { reset: 'EIO' } }
    page.log.push(entry)
    let buffer = new Uint8Array(0)
    let finished = false
    const parts = []
    const conn = { onmessage: null, onclose: null }
    const send = bytes => conn.onmessage?.(bytes)
    const finish = () => {
      if (finished) return
      finished = true
      entry.closed = true
      entry.text = decoder.decode(concat(parts))
      conn.onclose?.()
    }
    const reset = reason => {
      send(control({ reset: reason }))
      finish()
    }
    const answer = () => {
      if (!allowed(stamp)) return reset('EACCES: only root and members of sudo may follow a tray')
      send(control(script.answer))
      if (script.answer.reset !== undefined) return finish()
      if (!script.awaitEnd) reply()
    }
    const reply = () => {
      for (const event of script.send ?? []) send(line(event))
      if (script.end) send(record(3))
      if (entry.ended) finish()
    }
    const take = rec => {
      if (entry.first === null) {
        if (rec.type !== 1) return reset('EPROTO: the first record must be control')
        entry.first = { type: rec.type, json: JSON.parse(decoder.decode(rec.payload)) }
        return answer()
      }
      entry.records.push(rec.type)
      if (rec.type === 2) {
        entry.bytes += rec.payload.length
        parts.push(rec.payload)
      } else if (rec.type === 3) {
        entry.ended = true
        if (script.awaitEnd) reply()
        else if (script.end) finish()
      } else if (rec.type === 1) {
        entry.controls.push(JSON.parse(decoder.decode(rec.payload)))
      } else {
        reset('EPROTO: unknown record type')
      }
    }
    conn.send = bytes => {
      if (finished) return
      buffer = concat([buffer, bytes])
      while (!finished && buffer.length >= 5) {
        const size = new DataView(buffer.buffer, buffer.byteOffset).getUint32(1)
        if (buffer.length < 5 + size) break
        const rec = { type: buffer[0], payload: buffer.slice(5, 5 + size) }
        buffer = buffer.slice(5 + size)
        take(rec)
      }
    }
    conn.close = () => {
      entry.closed = true
      finished = true
      entry.text = decoder.decode(concat(parts))
    }
    return conn
  }
  return page
}

function concat (chunks) {
  const out = new Uint8Array(chunks.reduce((n, c) => n + c.length, 0))
  let at = 0
  for (const c of chunks) {
    out.set(c, at)
    at += c.length
  }
  return out
}
