<script setup>
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Download, VideoPause, VideoPlay, Delete, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { liveStream } from '../live.js'
import { appendLog } from '../tabs.mjs'
import { api } from '../api.js'
import { state, statusType, statusLabel } from '../store.js'
const route = useRoute(),
  router = useRouter()
const name = computed(() => String(route.query.name || ''))
const node = computed(() => String(route.query.node || ''))
const stream = computed(() => (route.query.stream === 'stderr' ? 'stderr' : 'stdout'))
const text = ref(''),
  paused = ref(false),
  follow = ref(true),
  wrap = ref(false),
  connection = ref('connecting'),
  error = ref(''),
  viewport = ref(null)
const process = computed(() =>
  state.processes.find((p) => p.name === name.value && (!node.value || p.node === node.value))
)
let stopLive
function connect() {
  stopLive?.()
  stopLive = undefined
  error.value = ''
  if (paused.value || !name.value) return
  const query = new URLSearchParams({ name: name.value, stream: stream.value, node: node.value })
  stopLive = liveStream(
    `/logs/events?${query}`,
    {
      log: async (data) => {
        text.value = appendLog(text.value, data.text, data.reset)
        if (follow.value) {
          await nextTick()
          if (viewport.value) viewport.value.scrollTop = viewport.value.scrollHeight
        }
      },
      unavailable: (data) => {
        error.value = data.message
      }
    },
    (status) => {
      connection.value = status
    }
  )
}
watch([name, node, stream, paused], connect, { immediate: true })
onUnmounted(() => stopLive?.())
async function clearFile() {
  try {
    await ElMessageBox.confirm(
      '清空服务端此程序的当前与轮转日志？此操作无法撤销。',
      '清空日志文件',
      { type: 'warning', confirmButtonText: '确认清空', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  try {
    await api('/logs/clear', {
      method: 'POST',
      body: { name: name.value, stream: stream.value, node: node.value }
    })
    text.value = ''
    ElMessage.success('日志已清空')
  } catch (err) {
    ElMessage.error(err.message)
  }
}
const download = computed(
  () =>
    `/api/logs?${new URLSearchParams({ name: name.value, stream: stream.value, node: node.value, download: '1' })}`
)
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <span class="eyebrow">LIVE LOGS</span>
        <h1>
          {{ name || '程序日志' }}
          <el-tag v-if="process" :type="statusType(process.state)" round>{{
            statusLabel(process.state)
          }}</el-tag>
        </h1>
        <p>直接查看程序输出，无需跳转或刷新页面。</p>
      </div>
      <span class="live-label" :class="{ success: connection === 'connected' && !paused }"
        ><span
          class="connection-dot"
          :class="{ online: connection === 'connected' && !paused }"
        ></span
        >{{
          paused ? '已暂停' : connection === 'connected' ? '日志实时连接正常' : '等待日志连接'
        }}</span
      >
    </div>
    <el-empty v-if="!name" description="从进程管理中选择一个程序查看日志" />
    <article v-else class="panel log-panel">
      <div class="log-toolbar">
        <el-radio-group
          :model-value="stream"
          @change="
            (value) => router.replace({ path: '/logs', query: { name, node, stream: value } })
          "
          ><el-radio-button value="stdout">标准输出</el-radio-button
          ><el-radio-button value="stderr">错误输出</el-radio-button></el-radio-group
        >
        <div class="log-controls">
          <el-switch v-model="follow" active-text="自动滚动" /><el-switch
            v-model="wrap"
            active-text="自动换行"
          /><el-button :icon="paused ? VideoPlay : VideoPause" @click="paused = !paused">{{
            paused ? '继续' : '暂停'
          }}</el-button
          ><el-button :icon="Refresh" @click="connect">重连</el-button
          ><el-button :icon="Delete" @click="text = ''">清屏</el-button
          ><el-button tag="a" :href="download" :icon="Download">下载片段</el-button
          ><el-dropdown trigger="click" @command="clearFile"
            ><el-button>更多</el-button
            ><template #dropdown
              ><el-dropdown-menu
                ><el-dropdown-item command="clear"
                  >清空服务端日志文件</el-dropdown-item
                ></el-dropdown-menu
              ></template
            ></el-dropdown
          >
        </div>
      </div>
      <el-alert v-if="error" :title="error" type="error" :closable="false" />
      <div ref="viewport" class="log-viewport" tabindex="0" aria-label="实时日志输出">
        <pre :class="{ wrap }">{{
          text || (paused ? '日志已暂停，继续时加载最新片段。' : '等待程序输出…')
        }}</pre>
      </div>
      <div class="log-footer">
        <span class="mono">{{ stream }} · UTF-8</span
        ><span>页面保留最近 2,000 行 / 256 KiB；重连加载最新 64 KiB。</span>
      </div>
    </article>
  </section>
</template>
