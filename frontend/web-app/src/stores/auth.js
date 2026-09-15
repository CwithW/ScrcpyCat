import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export const useAuthStore = defineStore('auth', () => {
  const isDemo = import.meta.env.VITE_DEMO_MODE === 'true'
  const token = ref(localStorage.getItem('auth_token') || (isDemo ? 'demo-token-xyz' : ''))
  const username = ref(localStorage.getItem('auth_user') || (isDemo ? 'demo_admin' : ''))
  const role = ref(localStorage.getItem('auth_role') || (isDemo ? 'admin' : ''))
  const assignedDevices = ref(JSON.parse(localStorage.getItem('auth_devices') || (isDemo ? '["*"]' : '[]')))
  const permissions = ref(JSON.parse(localStorage.getItem('auth_permissions') || (isDemo ? '{"shell":true,"file_library":true,"batch_tasks":true}' : '{}')))
  const noAuthMode = ref(isDemo)
  // 当前用户的设置管控策略（/api/me 下发，null = 未加载）：
  // { forbid_bitrate, forbid_fps, forbid_resolution, forbid_audio, settings, expires_at }
  const userPolicy = ref(null)

  const isLoggedIn = computed(() => noAuthMode.value || !!token.value)
  const isAdmin = computed(() => noAuthMode.value || role.value === 'admin')
  const canShell = computed(() => isAdmin.value || permissions.value.shell === true)
  const canFileLibrary = computed(() => isAdmin.value || permissions.value.file_library === true)
  const canBatchTasks = computed(() => isAdmin.value || permissions.value.batch_tasks === true)

  async function login(user, pass) {
    try {
      const response = await fetch('/api/login', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({ username: user, password: pass })
      })

      if (!response.ok) {
        const errText = await response.text()
        throw new Error(errText || '登录失败')
      }

      const data = await response.json()
      token.value = data.token
      username.value = data.username
      role.value = data.role || 'user'
      assignedDevices.value = data.assigned_devices || []

      localStorage.setItem('auth_token', data.token)
      localStorage.setItem('auth_user', data.username)
      localStorage.setItem('auth_role', data.role || 'user')
      localStorage.setItem('auth_devices', JSON.stringify(data.assigned_devices || []))
      await fetchMe()
      return true
    } catch (error) {
      console.error('Login error:', error)
      throw error
    }
  }

  async function register(user, pass) {
    try {
      const response = await fetch('/api/register', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({ username: user, password: pass })
      })

      if (!response.ok) {
        const errText = await response.text()
        throw new Error(errText || '注册失败')
      }
      return true
    } catch (error) {
      console.error('Register error:', error)
      throw error
    }
  }

  async function logout() {
    try {
      if (token.value) {
        await fetch('/api/logout', {
          method: 'POST',
          headers: { Authorization: `Bearer ${token.value}` }
        })
      }
    } catch (error) {
      console.error('Logout error:', error)
    } finally {
      token.value = ''
      username.value = ''
      role.value = ''
      assignedDevices.value = []
      noAuthMode.value = false
      userPolicy.value = null
      permissions.value = {}
      localStorage.removeItem('auth_token')
      localStorage.removeItem('auth_user')
      localStorage.removeItem('auth_role')
      localStorage.removeItem('auth_devices')
      localStorage.removeItem('auth_permissions')
      window.location.href = '/login'
    }
  }

  async function checkNoAuthStatus() {
    try {
      const res = await fetch('/api/auth-status')
      if (res.ok) {
        const data = await res.json()
        if (data.noAuth) {
          noAuthMode.value = true
          username.value = 'admin'
          role.value = 'admin'
          assignedDevices.value = ['*']
          permissions.value = { shell: true, file_library: true, batch_tasks: true }
        }
      }
    } catch (e) {
      // 请求失败说明需要正常认证，忽略
    }
  }

  async function fetchMe() {
    if (noAuthMode.value || !token.value) return
    try {
      const res = await fetch('/api/me', {
        headers: {
          'Authorization': `Bearer ${token.value}`
        }
      })
      if (res.ok) {
        const data = await res.json()
        username.value = data.username
        role.value = data.role || 'user'
        assignedDevices.value = data.assigned_devices || []
        permissions.value = data.permissions || {}
        
        localStorage.setItem('auth_user', data.username)
        localStorage.setItem('auth_role', data.role || 'user')
        localStorage.setItem('auth_devices', JSON.stringify(data.assigned_devices || []))
        localStorage.setItem('auth_permissions', JSON.stringify(permissions.value))

        // 缓存用户的设置管控策略（画质/音频锁定 + 管理员配置值 + 账号有效期）
        userPolicy.value = {
          forbid_bitrate: !!data.forbid_bitrate,
          forbid_fps: !!data.forbid_fps,
          forbid_resolution: !!data.forbid_resolution,
          forbid_audio: !!data.forbid_audio,
          settings: data.settings || null,
          expires_at: data.expires_at || null
        }
      }
    } catch (e) {
      console.error('Fetch me error:', e)
    }
  }

  return {
    token,
    username,
    role,
    assignedDevices,
    permissions,
    noAuthMode,
    userPolicy,
    isLoggedIn,
    isAdmin,
    canShell,
    canFileLibrary,
    canBatchTasks,
    login,
    register,
    logout,
    checkNoAuthStatus,
    fetchMe
  }
})
