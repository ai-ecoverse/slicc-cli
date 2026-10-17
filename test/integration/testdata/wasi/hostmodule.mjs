import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const input = JSON.parse(readFileSync(0, 'utf8'))
const { createImports } = await import(pathToFileURL(join(input.package, 'host', 'slicc-host.mjs')).href)
const memory = new WebAssembly.Memory({ initial: 1 })
const asked = []
const ctx = {
  memory: () => memory,
  errno: () => 0,
  net: {
    connect: options => {
      asked.push(options.host)
      return 7
    },
  },
}
const { slicc_host: host } = createImports(ctx)
const encoder = new TextEncoder()
const results = input.hosts.map(name => {
  const bytes = encoder.encode(name)
  new Uint8Array(memory.buffer).set(bytes, 64)
  const before = asked.length
  const fd = host.connect(64, bytes.length, 4242)
  return { name, fd, asked: asked.length > before ? asked[asked.length - 1] : null }
})
process.stdout.write(JSON.stringify(results))
