<script setup>
import { ref } from 'vue'
import { api } from '../api.js'
const emit = defineEmits(['login'])
const username = ref(''),
  password = ref(''),
  busy = ref(false),
  error = ref('')
async function submit() {
  if (busy.value) return
  error.value = ''
  if (!username.value || !password.value) {
    error.value = '请输入账号和密码'
    return
  }
  busy.value = true
  try {
    const session = await api('/auth/login', {
      method: 'POST',
      body: { username: username.value, password: password.value }
    })
    password.value = ''
    emit('login', session)
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <main class="login-page">
    <section class="login-intro">
      <div class="login-brand"><span class="brand-mark">S</span> SUPERVISORD</div>
      <div>
        <span class="eyebrow">PROCESS CONTROL, SIMPLIFIED</span>
        <h1>让每一个进程，<br />稳定运行。</h1>
        <p>从启动到停止，从状态到日志。<br />一个清晰、实时的进程工作台。</p>
      </div>
      <span class="login-footer">轻量部署 · 实时掌握 · 持续运行</span>
    </section>
    <section class="login-form-wrap">
      <div class="login-form">
        <span class="eyebrow">欢迎回来</span>
        <h2>登录控制台</h2>
        <p class="subtle">使用安装或初始化时生成的管理账号。</p>
        <el-form label-position="top" @submit.prevent="submit"
          ><el-form-item label="账号"
            ><el-input
              v-model="username"
              size="large"
              autocomplete="username"
              autofocus
              maxlength="128"
              placeholder="管理员账号" /></el-form-item
          ><el-form-item label="密码"
            ><el-input
              v-model="password"
              size="large"
              type="password"
              autocomplete="current-password"
              show-password
              maxlength="72"
              placeholder="输入密码" /></el-form-item
          ><el-alert
            v-if="error"
            :title="error"
            type="error"
            show-icon
            :closable="false"
          /><el-button
            native-type="submit"
            type="primary"
            size="large"
            :loading="busy"
            class="login-submit"
            >安全登录</el-button
          ></el-form
        >
        <p class="secure-note">HTTPS 加密连接 · 密码不写入浏览器存储</p>
      </div>
    </section>
  </main>
</template>
