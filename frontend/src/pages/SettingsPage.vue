<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../api.js'
const form = reactive({ current: '', password: '', confirmation: '' }),
  error = ref(''),
  busy = ref(false),
  security = ref(null)
const controller = new AbortController()
onMounted(async () => {
  try {
    security.value = await api('/config', { signal: controller.signal })
  } catch {}
})
onUnmounted(() => controller.abort())
async function submit() {
  error.value = ''
  const length = new TextEncoder().encode(form.password).length
  if (!form.current || length < 12 || length > 72) {
    error.value = '请输入当前密码，新密码须为 12–72 字节'
    return
  }
  if (form.password !== form.confirmation) {
    error.value = '两次输入的新密码不一致'
    return
  }
  busy.value = true
  try {
    await api('/auth/password', {
      method: 'POST',
      body: { current_password: form.current, password: form.password }
    })
    form.current = form.password = form.confirmation = ''
    ElMessage.success('密码已修改，请重新登录')
    window.dispatchEvent(new Event('session-expired'))
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <span class="eyebrow">SECURITY</span>
        <h1>安全设置</h1>
        <p>管理登录凭据与访问安全。</p>
      </div>
    </div>
    <div class="settings-grid">
      <article class="panel">
        <div class="panel-heading"><h2>修改登录密码</h2></div>
        <div class="password-form">
          <el-form label-position="top" @submit.prevent="submit"
            ><el-form-item label="当前密码"
              ><el-input
                v-model="form.current"
                type="password"
                autocomplete="current-password"
                show-password /></el-form-item
            ><el-form-item label="新密码"
              ><el-input
                v-model="form.password"
                type="password"
                autocomplete="new-password"
                show-password
              /><span class="field-help"
                >至少 12 字节，最长 72 字节；建议使用独立的随机密码。</span
              ></el-form-item
            ><el-form-item label="确认新密码"
              ><el-input
                v-model="form.confirmation"
                type="password"
                autocomplete="new-password"
                show-password /></el-form-item
            ><el-alert
              v-if="error"
              :title="error"
              type="error"
              :closable="false"
              show-icon
            /><el-button native-type="submit" type="primary" :loading="busy"
              >保存新密码</el-button
            ></el-form
          >
          <p class="subtle">修改后所有已登录会话失效，需要重新登录。</p>
        </div>
      </article>
      <article class="panel">
        <div class="panel-heading">
          <h2>访问保护</h2>
          <el-tag type="success">已启用</el-tag>
        </div>
        <dl class="service-details">
          <div>
            <dt>传输加密</dt>
            <dd>HTTPS · TLS 1.2+</dd>
          </div>
          <div>
            <dt>当前协议</dt>
            <dd>{{ security?.protocol || '—' }}</dd>
          </div>
          <div>
            <dt>Web 防护</dt>
            <dd>{{ security?.waf || 'Coraza + OWASP CRS' }}</dd>
          </div>
          <div>
            <dt>会话有效期</dt>
            <dd>8 小时</dd>
          </div>
          <div>
            <dt>密码保存</dt>
            <dd>bcrypt 单向哈希</dd>
          </div>
        </dl>
        <div class="security-note">
          <h3>证书与配置</h3>
          <p>
            首次初始化生成自签名证书，浏览器首次访问会提示信任。可在 conf
            中配置受信任证书，客户端必须核验证书或指纹。
          </p>
          <p>请保管好服务器上的 conf 文件，其中包含 TLS 私钥。</p>
        </div>
      </article>
    </div>
  </section>
</template>
