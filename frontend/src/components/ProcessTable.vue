<script setup>
import { useRouter } from 'vue-router'
import { state, statusType, statusLabel, uptime, performAction, nodeOnline } from '../store.js'
defineProps({ rows: { type: Array, default: () => [] }, selectable: Boolean })
const emit = defineEmits(['selection'])
const router = useRouter()
const goLog = (row) =>
  router.push({ path: '/logs', query: { name: row.name, stream: 'stdout', node: row.node } })
</script>

<template>
  <el-table
    :data="rows"
    :row-key="(row) => `${row.node}/${row.name}`"
    stripe
    empty-text="没有匹配的程序"
    @selection-change="(rows) => emit('selection', rows)"
  >
    <el-table-column v-if="selectable" type="selection" width="46" reserve-selection />
    <el-table-column label="程序 / 分组" min-width="190">
      <template #default="{ row }"
        ><button class="text-button process-name" @click="goLog(row)">{{ row.name }}</button
        ><span class="subtle block">{{ row.group }} · {{ row.node }}</span></template
      >
    </el-table-column>
    <el-table-column label="状态" width="110"
      ><template #default="{ row }"
        ><el-tag :type="statusType(row.state)" effect="light" round>{{
          statusLabel(row.state)
        }}</el-tag></template
      ></el-table-column
    >
    <el-table-column prop="pid" label="PID" width="88"
      ><template #default="{ row }"
        ><span class="mono">{{ row.pid || '—' }}</span></template
      ></el-table-column
    >
    <el-table-column label="运行时长" min-width="120"
      ><template #default="{ row }"
        ><span class="mono">{{ row.state === 20 ? uptime(row.start) : '—' }}</span></template
      ></el-table-column
    >
    <el-table-column label="操作" min-width="310" fixed="right">
      <template #default="{ row }">
        <el-button
          size="small"
          :disabled="
            state.connection !== 'connected' ||
            !nodeOnline(row.node) ||
            [10, 20, 40].includes(row.state) ||
            state.pending.has(`${row.node}/${row.name}`)
          "
          @click="performAction(row.name, 'start', row.node)"
          >启动</el-button
        >
        <el-button
          size="small"
          :disabled="
            state.connection !== 'connected' ||
            !nodeOnline(row.node) ||
            ![10, 20, 40].includes(row.state) ||
            state.pending.has(`${row.node}/${row.name}`)
          "
          @click="performAction(row.name, 'stop', row.node)"
          >停止</el-button
        >
        <el-button
          size="small"
          :disabled="
            state.connection !== 'connected' ||
            !nodeOnline(row.node) ||
            state.pending.has(`${row.node}/${row.name}`)
          "
          @click="performAction(row.name, 'restart', row.node)"
          >重启</el-button
        >
        <el-button size="small" @click="goLog(row)">日志</el-button>
        <el-dropdown
          trigger="click"
          @command="(action) => performAction(row.name, action, row.node)"
          ><el-button size="small" aria-label="更多进程操作">更多</el-button
          ><template #dropdown
            ><el-dropdown-menu
              ><el-dropdown-item
                command="kill"
                :disabled="!nodeOnline(row.node) || ![10, 20, 40].includes(row.state)"
                >强制终止</el-dropdown-item
              ></el-dropdown-menu
            ></template
          ></el-dropdown
        >
      </template>
    </el-table-column>
  </el-table>
</template>
