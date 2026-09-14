<template>
  <section class="deployment-credentials">
    <div class="credential-heading">
      <h2>常驻部署器凭据</h2>
      <button type="button" :disabled="loading || busy" @click="loadCredentials">刷新</button>
    </div>
    <p class="credential-hint">仅用于申请一次性设备接入令牌，不授予终端、文件或用户管理权限。默认无限期，可随时撤销。</p>
    <form class="credential-form" @submit.prevent="createCredential">
      <label for="deployment-credential-name">凭据名称
        <input id="deployment-credential-name" v-model.trim="name" maxlength="80" required placeholder="例如：机房 USB 部署器">
      </label>
      <label for="deployment-credential-ttl">凭据有效期
        <select id="deployment-credential-ttl" v-model.number="ttlSeconds">
          <option :value="0">无限期（默认）</option>
          <option :value="86400">1 天</option>
          <option :value="604800">7 天</option>
          <option :value="2592000">30 天</option>
          <option :value="7776000">90 天</option>
        </select>
      </label>
      <button type="submit" :disabled="busy || !name || !!issued">创建部署凭据</button>
    </form>
    <p v-if="error" class="credential-error" role="alert">{{ error }}</p>
    <div v-if="issued" class="issued-credential">
      <p>凭据仅显示这一次。请保存到部署器的 <code>SCRCPYCAT_DEPLOYMENT_TOKEN</code> 配置中。</p>
      <textarea aria-label="新部署凭据" :value="issued.token" readonly rows="2" spellcheck="false" @focus="$event.target.select()"></textarea>
      <div class="credential-actions">
        <button type="button" @click="$emit('copy', issued.token)">复制凭据</button>
        <button type="button" @click="issued = null">已保存，隐藏凭据</button>
      </div>
    </div>
    <div class="credential-table-wrap">
      <table class="credential-table">
        <thead><tr><th>名称 / ID</th><th>状态</th><th>到期时间</th><th>最近使用</th><th>创建人</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="credential in credentials" :key="credential.id" :data-credential-id="credential.id">
            <td>{{ credential.name }}<code class="credential-id">{{ credential.id }}</code></td>
            <td>{{ credentialStatus(credential) }}</td>
            <td>{{ formatDate(credential.expires_at, '无限期') }}</td>
            <td>{{ formatDate(credential.last_used_at, '尚未使用') }}</td>
            <td>{{ credential.created_by }}</td>
            <td><button type="button" :disabled="busy || !!credential.revoked_at" @click="revokeCredential(credential)">撤销</button></td>
          </tr>
          <tr v-if="credentials.length === 0"><td colspan="6">{{ loading ? '正在加载…' : '尚未创建部署凭据' }}</td></tr>
        </tbody>
      </table>
    </div>
    <p class="credential-hint">轮换时先创建新凭据，更新部署器后再撤销旧凭据。撤销会阻止继续接入，不影响已注册设备的正常连接。</p>
  </section>
</template>

<script setup>
import { onMounted, onUnmounted, ref } from 'vue'

defineEmits(['copy'])
const credentials = ref([])
const name = ref('')
const ttlSeconds = ref(0)
const issued = ref(null)
const error = ref('')
const loading = ref(false)
const busy = ref(false)
const now = ref(Date.now())
let clock

async function request(path = '', body) {
  const response = await fetch('/api/admin/deployment-credentials' + path, body === undefined
    ? { cache: 'no-store' }
    : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  const result = await response.json()
  if (!response.ok) throw new Error(result.error || '凭据操作失败')
  return result
}
async function loadCredentials() {
  loading.value = true
  error.value = ''
  try {
    credentials.value = await request()
    now.value = Date.now()
  } catch (failure) { error.value = failure.message }
  finally { loading.value = false }
}
async function createCredential() {
  busy.value = true
  error.value = ''
  try {
    issued.value = await request('', { name: name.value, ttl_seconds: ttlSeconds.value })
    name.value = ''
    await loadCredentials()
  } catch (failure) { error.value = failure.message }
  finally { busy.value = false }
}
async function revokeCredential(credential) {
  if (!window.confirm('撤销“' + credential.name + '”的部署凭据？已注册设备仍可正常连接。')) return
  busy.value = true
  error.value = ''
  try {
    await request('/revoke', { id: credential.id })
    if (issued.value?.credential.id === credential.id) issued.value = null
    await loadCredentials()
  } catch (failure) { error.value = failure.message }
  finally { busy.value = false }
}
function credentialStatus(credential) {
  if (credential.revoked_at) return '已撤销'
  if (credential.expires_at && Date.parse(credential.expires_at) <= now.value) return '已过期'
  return '有效'
}
function formatDate(value, empty) { return value ? new Date(value).toLocaleString() : empty }
onMounted(() => {
  loadCredentials()
  clock = setInterval(() => { now.value = Date.now() }, 30000)
})
onUnmounted(() => {
  clearInterval(clock)
  issued.value = null
})
</script>

<style scoped>
.deployment-credentials { padding: 20px; border: 1px solid var(--border); border-radius: 12px; background: var(--bg-surface, var(--bg-secondary)); }
.credential-heading, .credential-actions { display: flex; align-items: center; gap: 12px; }
.credential-heading { justify-content: space-between; }
h2 { margin: 0; font-size: 16px; color: var(--text-primary); }
.credential-hint { margin: 12px 0; font-size: 12px; line-height: 1.7; color: var(--text-secondary); }
.credential-form { display: flex; align-items: flex-end; flex-wrap: wrap; gap: 12px; }
label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; color: var(--text-secondary); }
input, select, textarea, button { border: 1px solid var(--border); border-radius: 6px; padding: 8px 10px; background: var(--bg-primary); color: var(--text-primary); font: inherit; }
button { cursor: pointer; font-size: 12px; white-space: nowrap; }
button:disabled { opacity: .5; cursor: default; }
input { min-width: 200px; }
.issued-credential { margin-top: 16px; padding: 14px; border: 1px solid var(--accent, #3b82f6); border-radius: 8px; }
.issued-credential p { margin: 0 0 10px; font-size: 12px; line-height: 1.7; }
textarea { width: 100%; box-sizing: border-box; resize: vertical; font-family: monospace; margin-bottom: 10px; }
.credential-table-wrap { overflow-x: auto; margin-top: 16px; }
.credential-table { width: 100%; border-collapse: collapse; text-align: left; font-size: 12px; }
th, td { padding: 10px 8px; border-bottom: 1px solid var(--border); white-space: nowrap; }
th { color: var(--text-secondary); font-weight: 500; }
.credential-id { display: block; margin-top: 4px; color: var(--text-secondary); }
.credential-error { color: #ef4444; font-size: 13px; }
</style>
