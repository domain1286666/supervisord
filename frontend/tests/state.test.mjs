import test from 'node:test'
import assert from 'node:assert/strict'
import { restoreTabs, tabForRoute, appendLog } from '../src/tabs.mjs'

test('restored tabs cannot navigate to an external site or an unknown route', () => {
  const tabs = restoreTabs(
    JSON.stringify([
      { url: 'https://evil.example' },
      { url: '//evil.example/logs' },
      { url: '/api/process/action' },
      { url: '/processes' }
    ])
  )
  assert.deepEqual(
    tabs.map((tab) => tab.url),
    ['/overview', '/processes']
  )
})
test('log tab safely encodes program names and shares stdout/stderr identity', () => {
  const stdout = tabForRoute({ path: '/logs', query: { name: 'a b&c/#', stream: 'stdout' } })
  const stderr = tabForRoute({ path: '/logs', query: { name: 'a b&c/#', stream: 'stderr' } })
  assert.equal(stdout.key, stderr.key)
  assert.equal(new URL(stdout.url, 'https://localhost').searchParams.get('name'), 'a b&c/#')
})
test('log reset after reconnect replaces prior text and bounds retained lines', () => {
  assert.equal(appendLog('old\n', 'new\n', true), 'new\n')
  assert.equal(appendLog('x'.repeat(400000), 'tail', false).length, 256 * 1024)
  assert.ok(appendLog('', 'line\n'.repeat(5000), false).split('\n').length <= 2000)
})
