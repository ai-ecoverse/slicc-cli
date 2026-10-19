import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { attachKernel, createNodeKernel } from '@ai-ecoverse/slicc-kernel/node'
import { linkPage } from './page.mjs'

const decoder = new TextDecoder()
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))
const signal = (proc, name) => Promise.resolve().then(() => proc.signal(name)).catch(() => {})
const input = JSON.parse(readFileSync(0, 'utf8'))
const stepMS = 30000
let at = 'reading the input'

class Stuck extends Error {}

function step (what) {
  at = what
  process.stderr.write(`run.mjs: ${what}\n`)
}

function within (what, promise, ms = stepMS) {
  let timer
  const limit = new Promise((resolve, reject) => {
    timer = setTimeout(() => reject(new Stuck(`${what}: no answer in ${ms} ms`)), ms)
  })
  return Promise.race([promise, limit]).finally(() => clearTimeout(timer))
}

setInterval(() => process.stderr.write(`run.mjs: alive at ${at}\n`), 5000).unref()

function files (dir) {
  return readdirSync(dir).flatMap(name => {
    const path = join(dir, name)
    return statSync(path).isDirectory() ? files(path) : [path]
  })
}

async function install (kernel, from, to) {
  for (const path of files(from)) await within(`writing ${path} into the kernel`, kernel.writeFile(`${to}/${relative(from, path).split('\\').join('/')}`, readFileSync(path)))
}

async function readJSON (kernel, path) {
  try {
    return JSON.parse(decoder.decode(await within(`reading ${path}`, kernel.readFile(path))))
  } catch (error) {
    if (error instanceof Stuck) throw error
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

async function fakeFacade (kernel, client, c, n) {
  const dir = `/tmp/case${n}`
  step(`case ${n} ${c.name}: starting the fake facade`)
  await within('writing the scenario', kernel.writeFile(`${dir}/scenario.json`, JSON.stringify(c.scenario ?? [{ answer: { reset: 'EIO' } }])))
  const facade = await within('spawning the fake facade', client.spawn(['slicc-fake-facade', dir], { cwd: '/tmp' }))
  const port = await waitFor('the fake facade', async () => {
    try {
      return Number(decoder.decode(await within('reading the fake facade\'s port', kernel.readFile(`${dir}/port`))))
    } catch (error) {
      if (error instanceof Stuck) throw error
      return 0
    }
  })
  const env = { PATH: '/usr/bin:/bin', HOME: '/root', ...(c.env ?? {}) }
  if (c.env?.SLICC_LINK_URL === undefined) env.SLICC_LINK_URL = `tcp://${c.linkHost ?? '127.0.0.1'}:${port}`
  if (c.env?.SLICC_LINK_URL === '') delete env.SLICC_LINK_URL
  return {
    env,
    log: () => readJSON(kernel, `${dir}/log.json`),
    settles: true,
    stop: async () => {
      await within('killing the fake facade', signal(facade, 'SIGKILL'))
      await Promise.race([facade.ended, sleep(2000)]).catch(() => {})
    },
  }
}

function realFacade (page, c) {
  page.scenario = c.scenario ?? []
  page.log = []
  return { env: { ...(c.env ?? {}) }, log: async () => page.log, stop: async () => {} }
}

function groupId (text, name) {
  for (const row of text.split('\n')) {
    const [group, , gid] = row.split(':')
    if (group === name) return Number(gid)
  }
  throw new Error(`no group ${name} in /etc/group`)
}

async function runCase (kernel, client, c, n) {
  const facade = page ? realFacade(page, c) : await fakeFacade(kernel, client, c, n)
  const env = facade.env
  step(`case ${n} ${c.name}: writing its files`)
  const hosts = c.hosts === undefined ? undefined : await within('reading /etc/hosts', kernel.readFile('/etc/hosts').catch(() => null))
  if (c.hosts !== undefined) await within('writing /etc/hosts', kernel.writeFile('/etc/hosts', c.hosts))
  for (const [path, content] of Object.entries(c.files ?? {})) await within(`writing ${path}`, kernel.writeFile(path, content))
  let stdout = ''
  let stderr = ''
  const started = Date.now()
  step(`case ${n} ${c.name}: starting ${c.argv.join(' ')}`)
  const proc = await within(`starting ${c.argv[0]}`, c.tty
    ? kernel.openTerminal(c.argv, { cwd: '/tmp', env, onData: b => { stdout += decoder.decode(b, { stream: true }) } })
    : client.spawn(c.argv, {
      cwd: '/tmp',
      env,
      ...(c.user ? { user: c.user } : {}),
      ...(c.stdin !== undefined ? { stdin: c.stdin } : {}),
      onStdout: b => { stdout += decoder.decode(b, { stream: true }) },
      onStderr: b => { stderr += decoder.decode(b, { stream: true }) },
    }))
  let interrupted = false
  if (c.interruptAfter) {
    step(`case ${n} ${c.name}: waiting for ${c.interruptAfter} facade connections`)
    await waitFor('the facade connections', async () => {
      const log = await facade.log()
      return log && log.length >= c.interruptAfter && log.slice(0, c.interruptAfter).every(e => e.first) && stdout.length + stderr.length >= (c.outputBefore ?? 0)
    }, 60000)
    await sleep(c.settle ?? 500)
    interrupted = true
    await within('interrupting the command', signal(proc, 'SIGINT'))
  }
  step(`case ${n} ${c.name}: waiting for ${c.argv[0]} to exit`)
  const ended = await Promise.race([proc.ended, sleep(c.timeout ?? 60000).then(() => null)])
  if (!ended) await within(`killing ${c.argv[0]}`, signal(proc, 'SIGKILL'))
  step(`case ${n} ${c.name}: reading the facade's log`)
  if (facade.settles) {
    await waitFor('the facade to record the connections', async () => {
      const log = await facade.log()
      return log && log.every(e => e.settled)
    }, 15000).catch(error => { if (error instanceof Stuck) throw error })
  } else await sleep(200)
  if (hosts !== undefined) await within('restoring /etc/hosts', kernel.writeFile('/etc/hosts', hosts ?? ''))
  const log = await facade.log()
  await facade.stop()
  return { name: c.name, status: ended ? ended.status : -1, interrupted, ms: Date.now() - started, stdout, stderr, log }
}

step('starting the kernel')
const kernel = await within('starting the kernel', createNodeKernel(input.real ? { facades: { link: { port: input.real.port } } } : {}), 60000)
const client = await within('attaching to the kernel', kernel.connect().then(attachKernel))
step('setting up the kernel')
for (const user of input.real?.users ?? []) await within(`adding the user ${user.name}`, kernel.users.add(user))
const sudo = input.real ? groupId(decoder.decode(await within('reading /etc/group', kernel.readFile('/etc/group'))), 'sudo') : undefined
const page = input.real ? linkPage({ sudo }) : null
if (page) await within('serving the link facade', client.serveFacade('link', page.hook))
step('installing slicc')
await install(kernel, input.package, '/node_modules/@ai-ecoverse/wasi-slicc')
await install(kernel, input.facade, '/node_modules/slicc-fake-facade')
const manifest = JSON.parse(readFileSync(join(input.package, 'package.json'), 'utf8'))
const bare = { name: 'wasi-slicc-bare', version: '0.0.0', slicc: { abi: 'wasi', commands: { 'slicc-bare': { wasm: manifest.slicc.commands.slicc.wasm } } } }
await within('writing wasi-slicc-bare', kernel.writeFile('/node_modules/wasi-slicc-bare/package.json', JSON.stringify(bare)))
await within('writing wasi-slicc-bare\'s wasm', kernel.writeFile(`/node_modules/wasi-slicc-bare/${bare.slicc.commands['slicc-bare'].wasm}`, readFileSync(join(input.package, manifest.slicc.commands.slicc.wasm))))
const results = []
let stuck = ''
for (const [n, c] of input.cases.entries()) {
  if (stuck) {
    results.push({ name: c.name, error: `not run: ${stuck}` })
    continue
  }
  try {
    results.push(await runCase(kernel, client, c, n))
  } catch (error) {
    if (error instanceof Stuck) stuck = `the kernel stopped answering in case ${n} (${c.name}): ${error.message}`
    results.push({ name: c.name, error: error instanceof Stuck ? error.message : String(error?.stack ?? error) })
  }
}
step('closing the kernel')
await within('closing the kernel', client.close({ kill: true }), 10000).catch(() => {})
kernel.terminate()
process.stdout.write(JSON.stringify(results), () => process.exit(0))
