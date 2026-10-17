import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import './style.css'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/overview' },
    {
      path: '/overview',
      component: () => import('./pages/OverviewPage.vue'),
      meta: { title: '运行概览' }
    },
    {
      path: '/processes',
      component: () => import('./pages/ProcessesPage.vue'),
      meta: { title: '进程管理' }
    },
    { path: '/logs', component: () => import('./pages/LogsPage.vue'), meta: { title: '实时日志' } },
    {
      path: '/configuration',
      component: () => import('./pages/ConfigPage.vue'),
      meta: { title: '配置管理' }
    },
    {
      path: '/settings',
      component: () => import('./pages/SettingsPage.vue'),
      meta: { title: '安全设置' }
    },
    { path: '/login', redirect: '/overview' },
    {
      path: '/log',
      redirect: (route) => ({
        path: '/logs',
        query: {
          name: route.query.name || route.query.program || route.query.process,
          node: route.query.node || '',
          stream: route.query.stream || 'stdout'
        }
      })
    },
    {
      path: '/log/:program(.*)',
      redirect: (route) => ({ path: '/logs', query: { name: route.params.program.split('/')[0] } })
    },
    { path: '/confFile', redirect: '/configuration' },
    { path: '/:pathMatch(.*)*', redirect: '/overview' }
  ]
})
createApp(App).use(router).mount('#app')
