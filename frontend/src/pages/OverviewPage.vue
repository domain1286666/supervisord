<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { Monitor, CircleCheck, Warning, DataLine } from '@element-plus/icons-vue'
import { state, bytes } from '../store.js'
import ProcessTable from '../components/ProcessTable.vue'
const router = useRouter()
const running = computed(() => state.processes.filter((p) => p.state === 20).length)
const attention = computed(() => state.processes.filter((p) => [30, 200].includes(p.state)).length)
const metrics = computed(() => [
  {
    label: '受管程序',
    value: state.processes.length,
    unit: '个',
    icon: Monitor,
    note: '当前配置中的程序'
  },
  {
    label: '正常运行',
    value: running.value,
    unit: '个',
    icon: CircleCheck,
    note: '进程状态 RUNNING',
    tone: 'success'
  },
  {
    label: '需要关注',
    value: attention.value,
    unit: '个',
    icon: Warning,
    note: '启动失败或正在重试',
    tone: attention.value ? 'danger' : ''
  },
  {
    label: '主机 CPU',
    value: Number(state.cpu_percent || 0).toFixed(1),
    unit: '%',
    icon: DataLine,
    note: '主机总使用率'
  }
])
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <span class="eyebrow">OVERVIEW</span>
        <h1>运行概览</h1>
        <p>主机与程序的实时状态，尽在掌握。</p>
      </div>
      <el-tag :type="state.connection === 'connected' ? 'success' : 'warning'" round>{{
        state.connection === 'connected' ? '实时更新中' : '等待连接'
      }}</el-tag>
    </div>
    <div class="metric-grid">
      <article v-for="metric in metrics" :key="metric.label" class="metric-card">
        <div class="metric-top">
          <span>{{ metric.label }}</span
          ><el-icon :class="metric.tone"><component :is="metric.icon" /></el-icon>
        </div>
        <div class="metric-value" :class="metric.tone">
          {{ state.timestamp ? metric.value : '—' }}<small>{{ metric.unit }}</small>
        </div>
        <span class="subtle">{{ metric.note }}</span>
      </article>
    </div>
    <div class="overview-grid">
      <article class="panel">
        <div class="panel-heading">
          <h2>主机资源</h2>
          <span class="subtle mono">{{ state.node || '—' }}</span>
        </div>
        <div class="resource-body">
          <div class="resource-heading">
            <span>CPU 使用率</span
            ><strong class="mono">{{ Number(state.cpu_percent || 0).toFixed(1) }}%</strong>
          </div>
          <el-progress
            :percentage="Math.min(100, state.cpu_percent || 0)"
            :show-text="false"
            :stroke-width="8"
            color="#196b4d"
          />
          <div class="resource-heading">
            <span>内存使用率</span
            ><strong class="mono">{{ Number(state.memory_percent || 0).toFixed(1) }}%</strong>
          </div>
          <el-progress
            :percentage="Math.min(100, state.memory_percent || 0)"
            :show-text="false"
            :stroke-width="8"
            color="#668a75"
          />
          <p class="subtle">主机内存 {{ bytes(state.memory_total) }}</p>
        </div>
      </article>
      <article class="panel">
        <div class="panel-heading">
          <h2>管理服务</h2>
          <el-tag type="success" size="small">在线</el-tag>
        </div>
        <dl class="service-details">
          <div>
            <dt>运行平台</dt>
            <dd>Go · 单文件部署</dd>
          </div>
          <div>
            <dt>管理器堆内存</dt>
            <dd class="mono">{{ bytes(state.runtime.heap_bytes) }}</dd>
          </div>
          <div>
            <dt>协程数量</dt>
            <dd class="mono">{{ state.runtime.goroutines || '—' }}</dd>
          </div>
          <div>
            <dt>最近状态</dt>
            <dd class="mono">
              {{
                state.timestamp ? new Date(state.timestamp * 1000).toLocaleTimeString() : '等待数据'
              }}
            </dd>
          </div>
        </dl>
      </article>
    </div>
    <article class="panel">
      <div class="panel-heading">
        <div>
          <h2>程序状态</h2>
          <span class="subtle">{{ running }} / {{ state.processes.length }} 个程序正在运行</span>
        </div>
        <el-button @click="router.push('/processes')">管理全部程序</el-button>
      </div>
      <ProcessTable :rows="state.processes.slice(0, 10)" />
    </article>
  </section>
</template>
