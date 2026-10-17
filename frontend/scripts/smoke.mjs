import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import net from 'node:net'
import https from 'node:https'
import { spawn, execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { chromium } from '@playwright/test'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const binary = path.join(
  root,
  '.local',
  os.platform() === 'win32' ? 'supervisord.exe' : 'supervisord'
)
const runtime = fs.mkdtempSync(path.join(root, '.local', 'ui-smoke-'))
const output = path.join(root, '.local', 'screenshots')
fs.mkdirSync(output, { recursive: true })
const listener = net.createServer()
await new Promise((resolve) => listener.listen(0, '127.0.0.1', resolve))
const port = listener.address().port
await new Promise((resolve) => listener.close(resolve))
const conf = path.join(runtime, 'supervisord.conf')
const init = execFileSync(
  binary,
  ['init', '--output', conf, '--port', String(port), '--bind', '127.0.0.1'],
  { encoding: 'utf8', windowsHide: true }
)
const username = init.match(/Administrator: (.+)/)[1].trim(),
  password = init.match(/Initial password: (.+)/)[1].trim()
fs.writeFileSync(
  conf,
  fs
    .readFileSync(conf, 'utf8')
    .replace('[inet_http_server]', '[inet_http_server]\nnodename=demo-host')
)
const fixture = path.join(runtime, 'fixture.mjs')
fs.writeFileSync(
  fixture,
  "console.log('demo service ready'); console.error('stderr is separate'); setInterval(() => console.log('heartbeat: process healthy'), 1000); process.on('SIGTERM', () => { console.log('graceful shutdown'); process.exit(0) })\n"
)
fs.appendFileSync(
  conf,
  `\n[program:demo-worker]\ncommand="${process.execPath}" "${fixture}"\ndirectory=${runtime}\nstartsecs=0\nstopwaitsecs=1\nautostart=true\nautorestart=unexpected\nstdout_logfile=logs/stdout.log\nstderr_logfile=logs/stderr.log\nstdout_logfile_timestamp_suffix=false\nstderr_logfile_timestamp_suffix=false\n`
)
const cert = Buffer.from(
  fs
    .readFileSync(conf, 'utf8')
    .match(/^tls_certificate\s*=\s*(.+)$/m)[1]
    .trim(),
  'base64'
)
let child,
  browser,
  page,
  tail = ''
const errors = []
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
try {
  child = spawn(binary, ['-n', '-c', conf], {
    cwd: runtime,
    windowsHide: true,
    stdio: ['ignore', 'pipe', 'pipe']
  })
  for (const stream of [child.stdout, child.stderr])
    stream.on('data', (chunk) => {
      tail = (tail + chunk.toString()).slice(-8192)
    })
  let ready = false
  for (let i = 0; i < 150; i++) {
    if (child.exitCode !== null) throw new Error(`server exited ${child.exitCode}: ${tail}`)
    ready = await new Promise((resolve) => {
      const request = https.get(
        { host: '127.0.0.1', port, path: '/api/auth/session', ca: cert, timeout: 500 },
        (response) => {
          response.resume()
          resolve(response.statusCode === 401)
        }
      )
      request.on('error', () => resolve(false))
      request.on('timeout', () => {
        request.destroy()
        resolve(false)
      })
    })
    if (ready) break
    await sleep(100)
  }
  assert.ok(ready, `server never became ready: ${tail}`)
  const cachedChrome = path.join(
    os.homedir(),
    'AppData/Local/ms-playwright/chromium-1208/chrome-win64/chrome.exe'
  )
  browser = await chromium.launch({
    headless: true,
    ...(fs.existsSync(cachedChrome) ? { executablePath: cachedChrome } : {})
  })
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1440, height: 960 }
  })
  await context.addInitScript(() => {
    const Native = window.EventSource,
      active = new Set()
    window.EventSource = class extends Native {
      constructor(...args) {
        super(...args)
        active.add(this)
      }
      close() {
        active.delete(this)
        super.close()
      }
    }
    window.activeSSECount = () => active.size
  })
  page = await context.newPage()
  page.on('pageerror', (error) => {
    errors.push(error.message)
    console.error('Browser runtime error:', error.message)
  })
  page.on('request', (request) => {
    if (request.url().includes('/api/auth/') || request.isNavigationRequest())
      console.log('Request', request.method(), new URL(request.url()).pathname)
  })
  page.on('response', (response) => {
    if (response.url().includes('/api/auth/') || response.url().includes('/api/events'))
      console.log('Response', response.status(), new URL(response.url()).pathname)
  })
  page.on('console', (message) => {
    if (message.type() === 'error') console.log('Browser console:', message.text())
  })
  page.on('response', async (response) => {
    if (response.url().endsWith('/api/auth/login') && response.status() !== 200)
      console.error('Login response:', response.status(), await response.text())
  })
  const url = `https://127.0.0.1:${port}`
  await page.goto(url)
  await page.getByRole('button', { name: '安全登录', exact: true }).waitFor()
  await page.screenshot({ path: path.join(output, 'login.png'), fullPage: true })
  await page.getByLabel('账号', { exact: true }).fill(username)
  await page.getByLabel('密码', { exact: true }).fill(password)
  await page.getByRole('button', { name: '安全登录', exact: true }).click()
  await page.getByRole('heading', { name: '运行概览', exact: true }).waitFor()
  await page.getByText('demo-worker', { exact: true }).first().waitFor()
  await sleep(350)
  console.log(
    'Process status tags:',
    await page.locator('.el-table__row .el-tag').allTextContents()
  )
  await page.screenshot({ path: path.join(output, 'overview.png'), fullPage: true })
  await page.locator('.nav-item').filter({ hasText: '进程管理' }).click()
  await page.getByRole('button', { name: '日志', exact: true }).first().click()
  await page.getByText('demo service ready', { exact: false }).waitFor()
  await page.screenshot({ path: path.join(output, 'logs.png'), fullPage: true })
  for (let i = 0; i < 5; i++) {
    await page.locator('.nav-item').filter({ hasText: '进程管理' }).click()
    await page.getByRole('button', { name: '日志', exact: true }).first().click()
    await page.getByText('demo service ready', { exact: false }).waitFor()
  }
  assert.equal(
    await page.evaluate(() => activeSSECount()),
    2,
    'state and active log must own exactly two streams'
  )
  await page.locator('.nav-item').filter({ hasText: '运行概览' }).click()
  assert.equal(await page.evaluate(() => activeSSECount()), 1, 'inactive log stream must be closed')
  await page.goto(`${url}/log?program=demo-worker`)
  await page.getByRole('heading', { name: 'demo-worker', exact: false }).waitFor()
  await page.getByText('demo service ready', { exact: false }).waitFor()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: '展开导航', exact: true }).click()
  await page.locator('.nav-item').filter({ hasText: '进程管理' }).click()
  await page.waitForFunction(
    () => document.querySelector('.sidebar').getBoundingClientRect().right <= 0
  )
  await page.screenshot({ path: path.join(output, 'mobile.png'), fullPage: true })
  assert.ok(
    await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
    'mobile document must not overflow horizontally'
  )
  const status = execFileSync(binary, ['-c', conf, 'ctl', 'status'], {
    encoding: 'utf8',
    windowsHide: true
  })
  assert.ok(status.includes('demo-worker'), 'local signed CLI must list the managed process')
  await page.setViewportSize({ width: 1440, height: 960 })
  await page.locator('.nav-item').filter({ hasText: '安全设置' }).click()
  const newPassword = 'test-rotation-password-2026'
  await page.getByLabel('当前密码', { exact: true }).fill('incorrect-current-password')
  await page.getByLabel('新密码', { exact: true }).fill(newPassword)
  await page.getByLabel('确认新密码', { exact: true }).fill(newPassword)
  await page.getByRole('button', { name: '保存新密码', exact: true }).click()
  await page.getByText('当前密码错误', { exact: true }).waitFor()
  assert.equal(
    await page.locator('.console-shell').count(),
    1,
    'wrong current password must preserve the session'
  )
  await page.getByLabel('当前密码', { exact: true }).fill(password)
  await page.getByRole('button', { name: '保存新密码', exact: true }).click()
  await page.getByRole('button', { name: '安全登录', exact: true }).waitFor()
  async function loginAgain() {
    await page.getByLabel('账号', { exact: true }).fill(username)
    await page.getByLabel('密码', { exact: true }).fill(newPassword)
    await page.getByRole('button', { name: '安全登录', exact: true }).click()
    await page.locator('.console-shell').waitFor()
    await page.locator('.sidebar-bottom').getByText('实时连接正常', { exact: true }).waitFor()
  }
  await loginAgain()
  await context.setOffline(true)
  await sleep(1500)
  await context.setOffline(false)
  await page
    .locator('.sidebar-bottom')
    .getByText('实时连接正常', { exact: true })
    .waitFor({ timeout: 35000 })
  assert.equal(
    await page.evaluate(() => activeSSECount()),
    1,
    'network recovery must not duplicate streams'
  )
  await page.locator('.nav-item').filter({ hasText: '进程管理' }).click()
  await page.locator('.el-table__body .el-checkbox').first().click()
  await page.getByRole('button', { name: '批量停止', exact: true }).click()
  await page.getByRole('button', { name: '确认执行', exact: true }).click()
  await page
    .locator('.el-table__row .el-tag')
    .filter({ hasText: '已停止' })
    .waitFor({ timeout: 15000 })
  await page.locator('.nav-item').filter({ hasText: '配置管理' }).click()
  await page.getByRole('button', { name: '加载配置变更', exact: true }).click()
  await page.getByRole('button', { name: '加载配置', exact: true }).click()
  await page.getByRole('button', { name: '安全登录', exact: true }).waitFor({ timeout: 35000 })
  assert.equal(
    await page.evaluate(() => activeSSECount()),
    0,
    'reload must release the invalid session stream'
  )
  await loginAgain()
  const reloadedStatus = execFileSync(binary, ['-c', conf, 'ctl', 'status'], {
    encoding: 'utf8',
    windowsHide: true
  })
  assert.match(reloadedStatus, /\bstopped\b/i, 'changed-only reload must preserve a manual stop')
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await page.getByRole('button', { name: '安全登录', exact: true }).waitFor()
  assert.equal(await page.evaluate(() => activeSSECount()), 0, 'logout must release every stream')
  assert.deepEqual(errors, [], 'browser runtime errors')
  console.log(
    JSON.stringify(
      {
        result: 'passed',
        checks: [
          'login',
          'overview',
          'processes',
          'logs',
          'tab stream cleanup',
          'legacy log link',
          'mobile layout',
          'signed CLI',
          'password change and rejection',
          'network recovery',
          'batch stop',
          'reload session cleanup and manual stop',
          'logout'
        ],
        screenshots: output
      },
      null,
      2
    )
  )
} catch (error) {
  if (page) {
    await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true })
    console.error(
      JSON.stringify({
        url: page.url(),
        alerts: await page.locator('.el-alert').allTextContents(),
        errors
      })
    )
  }
  throw error
} finally {
  await browser?.close()
  if (child && child.exitCode === null) {
    try {
      execFileSync(binary, ['-c', conf, 'ctl', 'shutdown'], {
        timeout: 10000,
        windowsHide: true,
        stdio: 'pipe'
      })
    } catch {}
    for (let i = 0; i < 50 && child.exitCode === null; i++) await sleep(100)
    if (child.exitCode === null) child.kill()
  }
}
