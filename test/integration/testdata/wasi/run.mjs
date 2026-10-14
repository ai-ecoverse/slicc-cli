import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { attachKernel, createNodeKernel } from '@ai-ecoverse/slicc-kernel/node'

const decoder = new TextDecoder()
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))
const signal = (proc, name) => Promise.resolve().then(() => proc.signal(name)).catch(() => {})
const input = JSON.parse(readFileSync(0, 'utf8'))

function files (dir) {
  return readdirSync(dir).flatMap(name => {
    const path = join(dir, name)
    return statSync(path).isDirectory() ? files(path) : [path]
  })
}

async function install (kernel, from, to) {
  for (const path of files(from)) await kernel.writeFile(`${to}/${relative(from, path).split('\\').join('/')}`, readFileSync(path))
}

async function readJSON (kernel, path) {
  try {
    return JSON.parse(decoder.decode(await kernel.readFile(path)))
  } catch {
    return null
  }
}

async function waitFor (what, check, ms = 30000) {
  const deadline = Date.now() + ms
  for (;;) {
    const value = await check()
    if (value) return value
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`)
    await sleep(50)
  }
}

async function runCase (kernel, client, c, n) {
  const dir = `/tmp/case${n}`
  await kernel.writeFile(`${dir}/scenario.json`, JSON.stringify(c.scenario ?? [{ answer: { reset: 'EIO' } }]))
  const facade = await client.spawn(['slicc-fake-facade', dir], { cwd: '/tmp' })
  const port = await waitFor('the fake facade', async () => {
    try {
      return Number(decoder.decode(await kernel.readFile(`${dir}/port`)))
    } catch {
      return 0
    }
  })
  const env = { PATH: '/usr/bin:/bin', HOME: '/root', ...(c.env ?? {}) }
  if (c.env?.SLICC_LINK_URL === undefined) env.SLICC_LINK_URL = `tcp://127.0.0.1:${port}`
  if (c.env?.SLICC_LINK_URL === '') delete env.SLICC_LINK_URL
  let stdout = ''
  let stderr = ''
  const started = Date.now()
  const proc = c.tty
    ? await kernel.openTerminal(c.argv, { cwd: '/tmp', env, onData: b => { stdout += decoder.decode(b, { stream: true }) } })
    : await client.spawn(c.argv, {
      cwd: '/tmp',
      env,
      ...(c.stdin !== undefined ? { stdin: c.stdin } : {}),
      onStdout: b => { stdout += decoder.decode(b, { stream: true }) },
      onStderr: b => { stderr += decoder.decode(b, { stream: true }) },
    })
  let interrupted = false
  if (c.interruptAfter) {
    await waitFor('the facade connections', async () => {
      const log = await readJSON(kernel, `${dir}/log.json`)
      return log && log.length >= c.interruptAfter && log.slice(0, c.interruptAfter).every(e => e.first) && stdout.length + stderr.length >= (c.outputBefore ?? 0)
    }, 60000)
    await sleep(c.settle ?? 500)
    interrupted = true
    await signal(proc, 'SIGINT')
  }
  const ended = await Promise.race([proc.ended, sleep(c.timeout ?? 60000).then(() => null)])
  if (!ended) await signal(proc, 'SIGKILL')
  await sleep(200)
  const log = await readJSON(kernel, `${dir}/log.json`)
  await signal(facade, 'SIGKILL')
  await Promise.race([facade.ended, sleep(2000)]).catch(() => {})
  return { name: c.name, status: ended ? ended.status : -1, interrupted, ms: Date.now() - started, stdout, stderr, log }
}

const kernel = await createNodeKernel()
const client = await attachKernel(await kernel.connect())
await install(kernel, input.package, '/node_modules/@ai-ecoverse/wasi-slicc')
await install(kernel, input.facade, '/node_modules/slicc-fake-facade')
const manifest = JSON.parse(readFileSync(join(input.package, 'package.json'), 'utf8'))
const bare = { name: 'wasi-slicc-bare', version: '0.0.0', slicc: { abi: 'wasi', commands: { 'slicc-bare': { wasm: manifest.slicc.commands.slicc.wasm } } } }
await kernel.writeFile('/node_modules/wasi-slicc-bare/package.json', JSON.stringify(bare))
await kernel.writeFile(`/node_modules/wasi-slicc-bare/${bare.slicc.commands['slicc-bare'].wasm}`, readFileSync(join(input.package, manifest.slicc.commands.slicc.wasm)))
const results = []
for (const [n, c] of input.cases.entries()) {
  try {
    results.push(await runCase(kernel, client, c, n))
  } catch (error) {
    results.push({ name: c.name, error: String(error?.stack ?? error) })
  }
}
await client.close({ kill: true }).catch(() => {})
kernel.terminate()
process.stdout.write(JSON.stringify(results), () => process.exit(0))
