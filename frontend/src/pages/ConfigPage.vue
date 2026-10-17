<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api.js'
const data = ref(null),
  error = ref(''),
  busy = ref(false)
const controller = new AbortController()
onMounted(async () => {
  try {
    data.value = await api('/config', { signal: controller.signal })
  } catch (err) {
    if (!controller.signal.aborted) error.value = err.message
  }
})
onUnmounted(() => controller.abort())
async function reload() {
  try {
    await ElMessageBox.confirm(
      '请先保存 conf 文件。仅新增、移除或配置有变化的程序会被更新，连接将自动重建；需要重新登录。',
      '重新加载配置',
      { type: 'warning', confirmButtonText: '加载配置', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  busy.value = true
  try {
    await api('/config/reload', { method: 'POST' })
    ElMessage.success('已接受配置重载请求')
  } catch (err) {
    ElMessage.error(err.message)
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <span class="eyebrow">CONFIGURATION</span>
        <h1>配置管理</h1>
        <p>沿用熟悉的 conf 文件，明确控制每个程序的行为。</p>
      </div>
      <el-button type="primary" :loading="busy" @click="reload">加载配置变更</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <article class="panel">
      <div class="panel-heading">
        <h2>当前配置</h2>
        <el-tag type="info">只读预览</el-tag>
      </div>
      <div class="config-note">
        <span class="subtle">配置文件路径</span><code>{{ data?.file || '加载中…' }}</code>
        <p>在服务器编辑配置后，点击“加载配置变更”。账号、私钥和环境变量不会在此页面展示。</p>
      </div>
      <el-table :data="data?.programs || []" empty-text="尚未配置程序"
        ><el-table-column prop="name" label="程序" min-width="160" /><el-table-column
          prop="command"
          label="启动命令"
          min-width="260"
          show-overflow-tooltip
        /><el-table-column
          prop="directory"
          label="工作目录"
          min-width="220"
          show-overflow-tooltip
        /><el-table-column prop="autorestart" label="自动重启" width="140" /><el-table-column
          label="停止信号"
          width="120"
          prop="stopsignal"
        /><el-table-column label="退出等待" width="110"
          ><template #default="{ row }">{{ row.stopwaitsecs }} 秒</template></el-table-column
        ></el-table
      >
    </article>
    <article class="panel config-example">
      <div class="panel-heading"><h2>程序配置示例</h2></div>
      <pre>
[program:my-app]
directory = %(here)s/apps/my-app
command = ./my-app --name "hello world" --label 'single quotes'
autostart = true
autorestart = unexpected
stdout_logfile = logs/stdout.log
stderr_logfile = logs/stderr.log
stdout_logfile_timestamp_suffix = false
stderr_logfile_timestamp_suffix = false
stdout_logfile_maxbytes = 50MB
stderr_logfile_maxbytes = 50MB
stdout_logfile_backups = 5
stderr_logfile_backups = 5
stopsignal = TERM
stopwaitsecs = 10
stopasgroup = true
killasgroup = true</pre>
      <div class="table-note">
        程序工作目录相对于 conf 所在目录解析；相对日志路径基于程序工作目录。需要 Shell
        功能时显式使用 /bin/sh -c。
      </div>
    </article>
  </section>
</template>
