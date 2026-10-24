// Secrets enter only through stdin and the child environment, never a file.
// Build the test binary first; pass its path and run from the repository root.
// CYBER_BASE_URL and CYBER_MODEL select the exact provider under test.
import { createInterface } from 'node:readline'
import { spawn } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'

const executable = process.argv[2]
if (!executable || !process.env.CYBER_BASE_URL || !process.env.CYBER_MODEL) {
  throw new Error('Pass the test binary path and set CYBER_BASE_URL and CYBER_MODEL')
}
if (process.stdin.isTTY) process.stdin.setRawMode(true)
const keep = setInterval(() => {}, 1000)
const input = createInterface({ input: process.stdin, terminal: false })
console.log('Waiting for credential JSON on stdin; terminal echo disabled.')
const credentials = await new Promise(done => input.once('line', line => done(JSON.parse(line))))
input.close()
process.stdin.pause()
clearInterval(keep)
if (!credentials.llm || !credentials.jev) throw new Error('Both llm and jev credentials are required')
const redacted = text => [credentials.llm, credentials.jev].reduce((s, key) => s.split(key).join('[REDACTED]'), text)
const stamp = new Date().toISOString().replaceAll(/[-:.]/g, '')
const root = resolve(process.env.JEV_TAKEOVER_REPORT_DIR || '.runlogs/jev-playwright/live-' + stamp)
await mkdir(root, { recursive: true })
console.log('model=' + process.env.CYBER_MODEL + ' report=' + root)
const env = { ...process.env, CYBER_API_KEY: credentials.llm, TYPESAFE_API_KEY: credentials.jev,
  JEV_TAKEOVER_LIVE: '1', JEV_TAKEOVER_REPORT_DIR: root }
const child = spawn(resolve(executable), ['-test.run=^TestLivePlaywrightTakeoverMatrix$', '-test.v', '-test.count=1', '-test.parallel=3', '-test.timeout=90m'], {
  cwd: resolve('exts/jev'), env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe']
})
let log = ''
for (const stream of [child.stdout, child.stderr]) {
  const lines = createInterface({ input: stream, crlfDelay: Infinity })
  lines.on('line', line => { const safe = redacted(line) + '\n'; log += safe; process.stdout.write(safe) })
}
const code = await new Promise((done, reject) => { child.once('error', reject); child.once('exit', done) })
await writeFile(resolve(root, 'test.log'), log)
console.log('acceptance exit=' + code + ' report=' + root)
process.exitCode = code
