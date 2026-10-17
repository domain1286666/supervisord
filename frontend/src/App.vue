<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Odometer,
  Monitor,
  Document,
  Setting,
  Menu,
  SwitchButton,
  Lock
} from '@element-plus/icons-vue'
import LoginPanel from './components/LoginPanel.vue'
import { api, setCSRF } from './api.js'
import { liveStream } from './live.js'
import { state } from './store.js'
import { restoreTabs, tabForRoute } from './tabs.mjs'

const route = useRoute(),
  router = useRouter()
const session = ref(null),
  booting = ref(true),
  mobileOpen = ref(false)
const tabs = ref(restoreTabs(localStorage.getItem('supervisord.tabs')))
const items = [
  { path: '/overview', label: '运行概览', icon: Odometer },
  { path: '/processes', label: '进程管理', icon: Monitor },
  { path: '/configuration', label: '配置管理', icon: Document },
  { path: '/settings', label: '安全设置', icon: Setting }
]
const tab = computed(() => tabForRoute(route))
const connectedLabel = computed(
  () =>
    ({
      connected: '实时连接正常',
      connecting: '正在连接',
      reconnecting: '连接中断，正在重连',
      paused: '后台已暂停连接',
      unavailable: '连接不可用'
    })[state.connection] || '连接已断开'
)
let stopLive
function clearSession() {
  stopLive?.()
  stopLive = undefined
  setCSRF('')
  session.value = null
  state.connection = 'connecting'
  state.processes = []
  state.nodes = []
  state.timestamp = 0
  state.runtime = {}
  state.configuration_error = ''
  state.pending.clear()
}
function login(value) {
  session.value = value
  setCSRF(value.csrf_token)
  stopLive?.()
  stopLive = liveStream('/events', { snapshot: (data) => Object.assign(state, data) }, (status) => {
    state.connection = status
  })
  rememberTab()
}
async function logout() {
  try {
    await api('/auth/logout', { method: 'POST' })
  } finally {
    clearSession()
  }
}
function rememberTab() {
  if (!session.value || !tab.value) return
  if (!tabs.value.some((item) => item.key === tab.value.key)) {
    if (tabs.value.length >= 16) tabs.value.splice(1, 1)
    tabs.value.push(tab.value)
  } else
    Object.assign(
      tabs.value.find((item) => item.key === tab.value.key),
      tab.value
    )
  localStorage.setItem('supervisord.tabs', JSON.stringify(tabs.value))
  mobileOpen.value = false
}
function closeTab(key) {
  const index = tabs.value.findIndex((item) => item.key === key)
  if (index <= 0) return
  tabs.value.splice(index, 1)
  localStorage.setItem('supervisord.tabs', JSON.stringify(tabs.value))
  if (tab.value?.key === key) router.push(tabs.value[Math.max(0, index - 1)].url)
}
function closeOthers() {
  tabs.value = tabs.value.filter((item) => item.key === '/overview' || item.key === tab.value?.key)
  localStorage.setItem('supervisord.tabs', JSON.stringify(tabs.value))
}
watch(() => route.fullPath, rememberTab)
onMounted(async () => {
  window.addEventListener('session-expired', clearSession)
  try {
    login(await api('/auth/session'))
  } catch {
    clearSession()
  } finally {
    booting.value = false
  }
})
onUnmounted(() => {
  stopLive?.()
  window.removeEventListener('session-expired', clearSession)
})
</script>

<template>
  <div v-if="booting" class="boot-screen" role="status">正在连接 Supervisord…</div>
  <LoginPanel v-else-if="!session" @login="login" />
  <div v-else class="console-shell">
    <button
      v-if="mobileOpen"
      class="sidebar-overlay"
      aria-label="收起导航"
      @click="mobileOpen = false"
    ></button>
    <aside class="sidebar" :class="{ open: mobileOpen }">
      <a class="brand" href="/overview" @click.prevent="router.push('/overview')"
        ><span class="brand-mark">S</span><span>SUPERVISORD<small>进程控制台</small></span></a
      >
      <span class="nav-caption">工作空间</span>
      <nav aria-label="主导航">
        <button
          v-for="item in items"
          :key="item.path"
          class="nav-item"
          :class="{ active: route.path === item.path }"
          :aria-current="route.path === item.path ? 'page' : undefined"
          @click="router.push(item.path)"
        >
          <el-icon><component :is="item.icon" /></el-icon><span>{{ item.label }}</span
          ><span v-if="item.path === '/processes'" class="nav-count">{{
            state.processes.length
          }}</span>
        </button>
      </nav>
      <div class="sidebar-bottom">
        <div>
          <span class="connection-dot" :class="{ online: state.connection === 'connected' }"></span
          >{{ connectedLabel }}
        </div>
        <span class="sidebar-host">{{ state.node || '连接主机中…' }}</span>
      </div>
    </aside>
    <div class="workspace">
      <header class="topbar">
        <div class="breadcrumb">
          <el-button
            class="mobile-menu"
            text
            :icon="Menu"
            aria-label="展开导航"
            @click="mobileOpen = true"
          /><span>工作空间</span><span class="breadcrumb-separator">/</span
          ><strong>{{ tab?.title }}</strong>
        </div>
        <div class="topbar-right">
          <el-tag type="success" effect="plain" round
            ><el-icon><Lock /></el-icon> HTTPS</el-tag
          ><span class="account-avatar">{{ session.username.slice(0, 1).toUpperCase() }}</span
          ><span class="account-name">{{ session.username }}</span
          ><el-button text :icon="SwitchButton" aria-label="退出登录" @click="logout"
            >退出</el-button
          >
        </div>
      </header>
      <div class="workspace-tabs">
        <el-tabs
          :model-value="tab?.key"
          type="card"
          @tab-change="
            (key) => router.push(tabs.find((item) => item.key === key)?.url || '/overview')
          "
          @tab-remove="closeTab"
          ><el-tab-pane
            v-for="item in tabs"
            :key="item.key"
            :label="item.title"
            :name="item.key"
            :closable="item.key !== '/overview'" /></el-tabs
        ><el-button text class="close-others" @click="closeOthers">关闭其他</el-button>
      </div>
      <main class="page-content">
        <el-alert
          v-if="state.configuration_error"
          class="connection-alert"
          :title="state.configuration_error"
          type="error"
          show-icon
          :closable="false"
        /><el-alert
          v-if="state.connection !== 'connected'"
          class="connection-alert"
          :title="`${connectedLabel}。下方保留最近数据，连接恢复后自动更新。`"
          type="warning"
          show-icon
          :closable="false"
        /><router-view v-slot="{ Component }"
          ><component
            :is="Component"
            :key="route.path + (route.query.node || '') + (route.query.name || '')"
        /></router-view>
        <footer class="page-footer">
          <span>Supervisord · 持续运行，清晰掌握</span><span>HTTPS / SSE</span>
        </footer>
      </main>
    </div>
  </div>
</template>
