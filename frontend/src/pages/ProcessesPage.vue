<script setup>
import { computed, onUnmounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import { state, performAction } from '../store.js'
import ProcessTable from '../components/ProcessTable.vue'

const search = ref(''),
  filter = ref('all'),
  group = ref(''),
  node = ref('')
const selected = ref([]),
  batchBusy = ref(false),
  page = ref(1)
const pageSize = 50
let batchController
onUnmounted(() => batchController?.abort())
const groups = computed(() => [...new Set(state.processes.map((row) => row.group))])
const rows = computed(() => {
  const query = search.value.toLowerCase()
  return state.processes.filter(
    (row) =>
      (!query || row.name.toLowerCase().includes(query)) &&
      (!group.value || row.group === group.value) &&
      (!node.value || row.node === node.value) &&
      (filter.value === 'all' || (filter.value === 'running' ? row.state === 20 : row.state !== 20))
  )
})
const visible = computed(() => rows.value.slice((page.value - 1) * pageSize, page.value * pageSize))
watch([search, filter, group, node], () => {
  page.value = 1
})
watch(
  () => rows.value.length,
  (count) => {
    page.value = Math.min(page.value, Math.max(1, Math.ceil(count / pageSize)))
  }
)
async function batch(action) {
  if (batchBusy.value) return
  const targets = [...selected.value]
  try {
    await ElMessageBox.confirm(
      `确认批量${action === 'stop' ? '优雅停止' : '启动'}所选的 ${targets.length} 个程序？`,
      '批量操作',
      { confirmButtonText: '确认执行', cancelButtonText: '取消', type: 'warning' }
    )
  } catch {
    return
  }
  batchController = new AbortController()
  const signal = batchController.signal
  batchBusy.value = true
  let accepted = 0,
    failed = 0
  try {
    for (const row of targets) {
      if (signal.aborted) break
      if (
        await performAction(row.name, action, row.node, {
          confirm: false,
          quiet: true,
          retryBusy: true,
          signal
        })
      )
        accepted++
      else failed++
    }
    if (!signal.aborted)
      ElMessage({
        type: failed ? 'warning' : 'success',
        message: `已接受 ${accepted} 个操作${failed ? `，${failed} 个失败` : ''}，结果通过实时状态更新。`
      })
  } finally {
    batchBusy.value = false
  }
}
function cancelBatch() {
  batchController?.abort()
}
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
        <span class="eyebrow">PROCESS MANAGEMENT</span>
        <h1>进程管理</h1>
        <p>集中管理程序生命周期，实时查看运行状态。</p>
      </div>
      <span class="subtle">共 {{ state.processes.length }} 个程序</span>
    </div>
    <article class="panel">
      <div class="filter-bar">
        <div class="filter-start">
          <el-input
            v-model="search"
            :prefix-icon="Search"
            placeholder="搜索程序名称"
            clearable
            aria-label="搜索程序"
          />
          <el-select v-model="node" clearable placeholder="全部节点" aria-label="按节点筛选">
            <el-option
              v-for="item in state.nodes"
              :key="item.name"
              :value="item.name"
              :label="`${item.name}${item.online ? '' : '（离线）'}`"
            />
          </el-select>
          <el-select v-model="group" clearable placeholder="全部分组" aria-label="按分组筛选">
            <el-option v-for="value in groups" :key="value" :label="value" :value="value" />
          </el-select>
        </div>
        <el-radio-group v-model="filter" aria-label="按运行状态筛选">
          <el-radio-button value="all">全部</el-radio-button
          ><el-radio-button value="running">运行中</el-radio-button
          ><el-radio-button value="other">未运行</el-radio-button>
        </el-radio-group>
      </div>
      <div v-if="selected.length" class="selection-bar">
        <span>已选 {{ selected.length }} 个程序</span>
        <el-button size="small" :loading="batchBusy" @click="batch('start')">批量启动</el-button>
        <el-button size="small" :disabled="batchBusy" @click="batch('stop')">批量停止</el-button>
        <el-button v-if="batchBusy" size="small" @click="cancelBatch">取消剩余提交</el-button>
      </div>
      <ProcessTable :rows="visible" selectable @selection="(value) => (selected = value)" />
      <div v-if="rows.length > pageSize" class="table-pagination">
        <el-pagination
          v-model:current-page="page"
          :page-size="pageSize"
          :total="rows.length"
          layout="total, prev, pager, next"
        />
      </div>
      <div class="table-note">
        “停止”会先通知程序优雅退出，超时后强制终止。“更多”中可立即强制终止。
      </div>
    </article>
  </section>
</template>
