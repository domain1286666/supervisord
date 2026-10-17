import { reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import 'element-plus/es/components/message/style/css'
import 'element-plus/es/components/message-box/style/css'
import { api } from './api.js'

export const state = reactive({
  processes: [],
  nodes: [],
  node: '',
  timestamp: 0,
  runtime: {},
  cpu_percent: 0,
  memory_percent: 0,
  memory_total: 0,
  configuration_error: '',
  connection: 'connecting',
  pending: new Set()
})
export const statusType = (value) =>
  ({ 20: 'success', 10: 'warning', 30: 'warning', 40: 'warning', 200: 'danger' })[value] || 'info'
export const statusLabel = (value) =>
  ({
    0: '已停止',
    10: '启动中',
    20: '运行中',
    30: '重试中',
    40: '停止中',
    100: '已退出',
    200: '启动失败'
  })[value] || '未知'
export function bytes(value = 0) {
  const units = ['B', 'KiB', 'MiB', 'GiB']
  let i = 0
  while (value >= 1024 && i < 3) {
    value /= 1024
    i++
  }
  return `${value.toFixed(i ? 1 : 0)} ${units[i]}`
}
export function uptime(start) {
  if (!start) return '—'
  const seconds = Math.max(0, state.timestamp - start)
  return seconds > 86400
    ? `${Math.floor(seconds / 86400)} 天`
    : `${Math.floor(seconds / 3600)} 时 ${Math.floor((seconds % 3600) / 60)} 分`
}
export function nodeOnline(node) {
  return (
    !node || node === state.node || state.nodes.some((item) => item.name === node && item.online)
  )
}
function waitForCapacity(signal) {
  return new Promise((resolve, reject) => {
    const abort = () => {
      clearTimeout(timer)
      reject(new DOMException('已取消', 'AbortError'))
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', abort)
      resolve()
    }, 2000)
    if (signal?.aborted) abort()
    else signal?.addEventListener('abort', abort, { once: true })
  })
}
export async function performAction(
  name,
  action,
  node = '',
  { confirm = true, quiet = false, signal, retryBusy = false } = {}
) {
  if (state.connection !== 'connected') {
    ElMessage.warning('连接恢复后可执行操作')
    return
  }
  const labels = { start: '启动', stop: '优雅停止', restart: '重启', kill: '强制终止' }
  if (confirm && action !== 'start') {
    try {
      await ElMessageBox.confirm(
        action === 'kill'
          ? `立即强制终止 ${name}？程序将没有保存数据的退出时间。`
          : `确认${labels[action]} ${name}？${action === 'stop' ? '先发送配置的停止信号，超时后强制终止。' : ''}`,
        labels[action],
        {
          type: action === 'kill' ? 'error' : 'warning',
          confirmButtonText: `确认${labels[action]}`,
          cancelButtonText: '取消'
        }
      )
    } catch {
      return
    }
  }
  state.pending.add(`${node}/${name}`)
  try {
    for (let attempt = 0; ; attempt++) {
      try {
        await api('/process/action', { method: 'POST', body: { name, action, node }, signal })
        break
      } catch (error) {
        if (!retryBusy || error.status !== 429 || attempt >= 30 || signal?.aborted) throw error
        await waitForCapacity(signal)
      }
    }
    if (!quiet) ElMessage.success(`${name}：已接受${labels[action]}请求`)
    return true
  } catch (error) {
    if (error.name !== 'AbortError') ElMessage.error(`${name}：${error.message}`)
    return false
  } finally {
    state.pending.delete(`${node}/${name}`)
  }
}
