<template>
  <div ref="consoleMainRef" class="device-console" :class="{ 'is-maximized': isMaximized }" :style="{ height: isMaximized ? '100vh' : height }">
    <!-- 顶部拖拽拉伸手柄 -->
    <div class="console-resizer" v-if="!isMaximized" @mousedown="startResizingConsole" title="拖动调整控制台高度"></div>

    <!-- 控制台顶部 Tab 导航 -->
    <header class="console-tabs-bar">
      <div class="tabs-group">
        <button 
          :class="{ active: activeTab === 'shell' }" 
          @click="activeTab = 'shell'"
          title="ADB Shell 命令行模式"
        >
          <svg class="tab-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="4 17 10 11 4 5"></polyline><line x1="12" y1="19" x2="20" y2="19"></line></svg>
          终端 (Shell)
        </button>
        <button 
          :class="{ active: activeTab === 'adb' }" 
          @click="activeTab = 'adb'"
          title="ADB 交互式终端 (xterm.js)"
        >
          <svg class="tab-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="4" width="20" height="16" rx="2" ry="2"></rect><path d="M12 18h.01"></path></svg>
          ADB 调试
        </button>
      </div>
      
      <!-- 设备状态指示器与隐藏控制台按钮 -->
      <div class="console-right-tools">
        <div class="console-device-badge" v-if="deviceId">
          <span class="status-indicator" :class="statusClass"></span>
          <select 
            :value="deviceId" 
            @change="onDeviceSelectChange"
            class="device-selector-dropdown"
          >
            <option 
              v-for="d in deviceStore.devices" 
              :key="d.id" 
              :value="d.id"
            >
              {{ d.id }}{{ d.status === 'online' ? '' : ' (离线)' }}
            </option>
          </select>
        </div>
        
        <!-- 全屏最大化切换按钮 -->
        <button 
          class="console-tool-btn" 
          @click="toggleMaximize" 
          :title="isMaximized ? '还原窗口' : '全屏显示'"
        >
          <svg v-if="!isMaximized" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3"></path>
          </svg>
          <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4 14h6v6m10-6h-6v6M4 10h6V4m10 6h-6V4"></path>
          </svg>
        </button>

        <!-- 最小化隐藏按钮 (保持连接) -->
        <button class="console-tool-btn" @click="deviceStore.closeGlobalConsole()" title="收起隐藏控制台 (终端继续后台运行)">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <line x1="5" y1="12" x2="19" y2="12"></line>
          </svg>
        </button>

        <!-- 彻底关闭断开按钮 -->
        <button class="console-close-btn" @click="deviceStore.destroyGlobalConsole()" title="关闭控制台 (断开所有终端连接)">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <line x1="18" y1="6" x2="6" y2="18"></line>
            <line x1="6" y1="6" x2="18" y2="18"></line>
          </svg>
        </button>
      </div>
    </header>

    <!-- 选项卡内容区 -->
    <div class="console-tab-content">
      <!-- 1. Shell 视图 -->
      <div v-show="activeTab === 'shell'" class="shell-tab-panel">
        <div class="console-history" ref="consoleRef">
          <div v-for="(log, idx) in consoleLogs" :key="idx" :class="['log-item', log.type]">
            <template v-if="log.type === 'batch_result'">
              <span class="log-cmd">$ [批量] {{ log.cmd }} (发送至 {{ Object.keys(log.results).length }} 台设备)</span>
              <div class="batch-outputs-list">
                <div 
                  v-for="(res, devId) in log.results" 
                  :key="devId" 
                  class="batch-output-row"
                  :class="res.status"
                >
                  <div class="row-header">
                    <span class="dev-tag">[{{ devId }}]</span>
                    <span class="status-tag" :class="res.status">
                      {{ res.status === 'running' ? '⏳ 执行中' : (res.status === 'success' ? '✅ 成功' : '❌ 失败') }}
                    </span>
                  </div>
                  <pre class="dev-output">{{ res.output }}</pre>
                </div>
              </div>
            </template>
            <template v-else>
              <span class="log-cmd" v-if="log.cmd">$ {{ log.cmd }}</span>
              <pre class="log-out">{{ log.text }}</pre>
            </template>
          </div>
          <div v-if="consoleLogs.length === 0" class="console-empty">
            <svg class="empty-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>
            等待命令下发...
          </div>
        </div>
        <div class="console-shortcuts">
          <button 
            v-for="(item, idx) in consoleShortcuts" 
            :key="idx" 
            @click="quickCmd(item.cmd)"
          >
            {{ item.name }}
          </button>
          <button @click="consoleLogs = []" class="system-btn">清屏</button>
          <button @click="showShortcutModal = true" class="system-btn edit-btn">⚙️ 自定义</button>
        </div>

        <!-- 并发下发目标设备选择 -->
        <div class="shell-targets-bar">
          <div class="targets-control-row">
            <span class="label">并发目标：</span>
            <label class="select-all-check" v-if="deviceStore.devices.filter(dev => dev.status === 'online' && dev.id !== deviceId).length > 0">
              <input type="checkbox" v-model="isAllDevicesSelected" />
              <span class="checkbox-custom"></span>
              <span class="name">全选</span>
            </label>
            <div class="tag-filters" v-if="tagStore.tags.length > 0">
              <span class="tag-filter-label">按标签选择：</span>
              <button 
                v-for="tag in tagStore.tags" 
                :key="tag.id" 
                class="tag-filter-btn"
                :style="{ 
                  borderColor: tag.color,
                  backgroundColor: isTagAllSelected(tag.id) ? tag.color : 'transparent',
                  color: isTagAllSelected(tag.id) ? '#fff' : tag.color 
                }"
                @click="toggleTagDevices(tag.id)"
              >
                {{ tag.name }}
              </button>
            </div>
          </div>
          <div class="targets-list">
            <label class="target-check current">
              <input type="checkbox" checked disabled />
              <span class="checkbox-custom"></span>
              <span class="name">{{ deviceId }} (当前)</span>
            </label>
            <label 
              v-for="d in deviceStore.devices.filter(dev => dev.status === 'online' && dev.id !== deviceId)" 
              :key="d.id" 
              class="target-check"
            >
              <input type="checkbox" :value="d.id" v-model="batchShellSelectedIds" />
              <span class="checkbox-custom"></span>
              <span class="name">{{ d.id }}</span>
            </label>
          </div>
        </div>

        <div class="console-input-group">
          <input 
            v-model="inputCmd" 
            @keyup.enter="execCmd"
            @keydown.up.prevent="navigateHistory('up')"
            @keydown.down.prevent="navigateHistory('down')"
            placeholder="输入 Android Shell 命令分发执行以获取回复..."
            class="cmd-input"
          />
          <button @click="execCmd" class="send-btn" :disabled="!inputCmd.trim()">{{ sendBtnText }}</button>
        </div>
      </div>

      <!-- 2. ADB 交互终端 (xterm.js) -->
      <div v-show="activeTab === 'adb'" class="adb-tab-panel">
        <!-- 终端会话 Tab 栏 -->
        <div class="adb-sessions-bar" v-if="adbSessions.length > 0">
          <div class="adb-tabs-group">
            <button 
              v-for="sess in adbSessions" 
              :key="sess.id"
              :class="{ active: activeSessionId === sess.id }"
              @click="switchAdbSession(sess.id)"
              class="adb-session-tab"
            >
              <span class="tab-status-dot" :class="{ connected: sess.isConnected }"></span>
              <span class="sess-name">{{ sess.name }}</span>
              <span class="close-sess-btn" @click.stop="closeAdbSession(sess.id)" title="关闭会话">×</span>
            </button>
            <button 
              class="add-sess-btn" 
              @click="addAdbSession" 
              title="新建终端会话" 
              :disabled="!terminalReady || adbSessions.length >= 5"
            >
              +
            </button>
          </div>
          <span class="max-sess-tip">最多支持开启 5 个终端页</span>
        </div>

        <div v-if="adbSessions.length === 0" class="adb-placeholder">
          <svg class="adb-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="4" y="4" width="16" height="16" rx="2" ry="2"></rect><polyline points="9 9 9 15 12 12 15 15 15 9"></polyline></svg>
          <h3>开启交互式 ADB Web 终端</h3>
          <p>支持 Tab 补全和独立多会话，视频连接不可用时仍可使用终端</p>
          <button class="adb-connect-btn" @click="addAdbSession" :disabled="!terminalReady">初始化 ADB 终端</button>
        </div>

        <!-- 渲染各会话的多容器 -->
        <div 
          v-for="sess in adbSessions" 
          :key="sess.id"
          :ref="el => { if (el) sessionContainers[sess.id] = el }"
          class="xterm-view-container" 
          v-show="activeSessionId === sess.id"
        ></div>
      </div>

    </div>

    <!-- 隐藏的 dummy video，用来满足 useWebRTC 在没有主视频时的画面要求 -->
    <video ref="dummyVideo" style="display: none;" autoplay playsinline muted></video>

    <!-- 自定义快捷指令弹窗 -->
    <div v-if="showShortcutModal" class="shortcut-modal-overlay" @click.self="showShortcutModal = false">
      <div class="shortcut-modal-card">
        <div class="modal-header">
          <h3>自定义快捷指令</h3>
          <button class="close-btn" @click="showShortcutModal = false">✕</button>
        </div>
        <div class="modal-body custom-scrollbar">
          <div class="shortcut-list">
            <div v-for="(item, idx) in modalShortcuts" :key="idx" class="shortcut-item-row">
              <div class="input-col name-col">
                <label>名称</label>
                <input v-model="item.name" placeholder="请输入指令名称，如 型号" />
              </div>
              <div class="input-col cmd-col">
                <label>Shell 命令</label>
                <input v-model="item.cmd" placeholder="请输入 Shell 命令，如 getprop ro.product.model" />
              </div>
              <button class="delete-btn" @click="deleteModalShortcut(idx)" title="删除指令">✕</button>
            </div>
            <div v-if="modalShortcuts.length === 0" class="no-shortcuts">
              暂无自定义快捷指令
            </div>
          </div>
          <button class="add-row-btn" @click="addModalShortcut">+ 添加快捷指令</button>
        </div>
        <div class="modal-footer">
          <button class="btn btn-reset" @click="resetToDefaultShortcuts">恢复默认</button>
          <div class="footer-actions">
            <button class="btn btn-cancel" @click="showShortcutModal = false">取消</button>
            <button class="btn btn-save" @click="saveShortcuts">保存</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, shallowRef, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useDeviceStore } from '@/stores/devices'
import { useAuthStore } from '@/stores/auth'
import { useTagStore } from '@/stores/tags'
import { useAdb } from '@/composables/useAdb'
import { useWebRTC } from '@/composables/useWebRTC'
import { isWebRTCAvailable } from '@/utils/webrtc'
import { getDeviceSettings } from '@/utils/settings'

const props = defineProps({
  deviceId: {
    type: String,
    required: true
  },
  isDrawer: {
    type: Boolean,
    default: false
  },
  height: {
    type: String,
    default: '100%'
  },
  shareToken: {
    type: String,
    default: ''
  },
  sharePassword: {
    type: String,
    default: ''
  },
  accessMode: {
    type: String,
    default: 'full'
  }
})

const emit = defineEmits(['close'])

const deviceStore = useDeviceStore()
const authStore = useAuthStore()
const tagStore = useTagStore()
const dummyVideo = ref(null)
const consoleRef = ref(null)
const chatRef = ref(null)

const activeTab = ref('shell')
const consoleLogs = ref([])
const inputCmd = ref('')

// --- 批量 Shell 与单台整合状态 ---
const batchShellSelectedIds = ref([])

const defaultShortcuts = [
  { name: '三方应用', cmd: 'pm list packages -3' },
  { name: '型号', cmd: 'getprop ro.product.model' },
  { name: '当前页面', cmd: 'dumpsys window | grep mCurrentFocus | grep -v null' },
  { name: '存储空间', cmd: 'df -h /data' },
  { name: '开启触控轨迹', cmd: 'settings put system pointer_location 1' },
  { name: '关闭轨迹', cmd: 'settings put system pointer_location 0' }
]

const consoleShortcuts = ref([])
const showShortcutModal = ref(false)
const modalShortcuts = ref([])

async function loadShortcuts() {
  try {
    const token = localStorage.getItem('auth_token') || ''
    const res = await fetch('/api/shortcuts', {
      headers: {
        'Authorization': token ? `Bearer ${token}` : ''
      }
    })
    if (res.ok) {
      const data = await res.json()
      if (Array.isArray(data) && data.length > 0) {
        consoleShortcuts.value = data
        try {
          localStorage.setItem('cloudphone_console_shortcuts', JSON.stringify(data))
        } catch (e) {}
        return
      }
    }
  } catch (e) {
    print("[Shortcuts] Failed to load from server, fallback to local:", e)
  }

  const saved = localStorage.getItem('cloudphone_console_shortcuts')
  if (saved) {
    try {
      consoleShortcuts.value = JSON.parse(saved)
    } catch (e) {
      consoleShortcuts.value = [...defaultShortcuts]
    }
  } else {
    consoleShortcuts.value = [...defaultShortcuts]
  }
}

async function saveShortcuts() {
  const filtered = modalShortcuts.value.filter(item => item.name.trim() && item.cmd.trim())
  consoleShortcuts.value = filtered
  
  try {
    localStorage.setItem('cloudphone_console_shortcuts', JSON.stringify(filtered))
  } catch (e) {}

  try {
    const token = localStorage.getItem('auth_token') || ''
    const res = await fetch('/api/shortcuts', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': token ? `Bearer ${token}` : ''
      },
      body: JSON.stringify(filtered)
    })
    if (!res.ok) {
      print("[Shortcuts] Failed to sync shortcuts to server")
    }
  } catch (e) {
    print("[Shortcuts] Failed to sync shortcuts to server:", e)
  }

  showShortcutModal.value = false
}

function addModalShortcut() {
  modalShortcuts.value.push({ name: '', cmd: '' })
}

function deleteModalShortcut(idx) {
  modalShortcuts.value.splice(idx, 1)
}

function resetToDefaultShortcuts() {
  modalShortcuts.value = defaultShortcuts.map(item => ({ ...item }))
}

watch(showShortcutModal, (newVal) => {
  if (newVal) {
    modalShortcuts.value = consoleShortcuts.value.map(item => ({ ...item }))
  }
})

const isAllDevicesSelected = computed({
  get() {
    const onlineOthers = deviceStore.devices.filter(dev => dev.status === 'online' && dev.id !== props.deviceId)
    if (onlineOthers.length === 0) return false
    return onlineOthers.every(dev => batchShellSelectedIds.value.includes(dev.id))
  },
  set(val) {
    const onlineOthers = deviceStore.devices.filter(dev => dev.status === 'online' && dev.id !== props.deviceId)
    if (val) {
      batchShellSelectedIds.value = onlineOthers.map(dev => dev.id)
    } else {
      batchShellSelectedIds.value = []
    }
  }
})

function getOnlineDevicesByTag(tagId) {
  return deviceStore.devices.filter(dev => {
    if (dev.status !== 'online' || dev.id === props.deviceId) return false
    const devTags = tagStore.deviceTags[dev.id] || []
    return devTags.includes(tagId)
  })
}

function isTagAllSelected(tagId) {
  const devs = getOnlineDevicesByTag(tagId)
  if (devs.length === 0) return false
  return devs.every(dev => batchShellSelectedIds.value.includes(dev.id))
}

function toggleTagDevices(tagId) {
  const devs = getOnlineDevicesByTag(tagId)
  if (devs.length === 0) return
  
  if (isTagAllSelected(tagId)) {
    const devIds = devs.map(d => d.id)
    batchShellSelectedIds.value = batchShellSelectedIds.value.filter(id => !devIds.includes(id))
  } else {
    const newIds = new Set(batchShellSelectedIds.value)
    devs.forEach(d => newIds.add(d.id))
    batchShellSelectedIds.value = Array.from(newIds)
  }
}
const targetDeviceIds = computed(() => [props.deviceId, ...batchShellSelectedIds.value])
const sendBtnText = computed(() => targetDeviceIds.value.length > 1 ? `并发发送 (${targetDeviceIds.value.length}台)` : '发送')

// 监听当前主设备变化，若新设备在 batchShellSelectedIds 中，则将其过滤掉以防重复
watch(() => props.deviceId, (newId) => {
  if (newId) {
    batchShellSelectedIds.value = batchShellSelectedIds.value.filter(id => id !== newId)
  }
})

// 监听 Pinia Store 里的当前任务进度更新结果，直接定位并写入 consoleLogs 里的 batch_result 项
watch(() => deviceStore.currentTask, (newTask) => {
  if (newTask && newTask.type === 'shell') {
    const logItem = consoleLogs.value.find(item => item.type === 'batch_result' && item.taskId === newTask.task_id)
    if (logItem) {
      Object.entries(newTask.devices).forEach(([devId, info]) => {
        logItem.results[devId] = {
          status: info.status,
          output: info.result || (info.status === 'running' ? 'Running...' : '无输出回复')
        }
      })
    }
  }
}, { deep: true })

// WebRTC 状态绑定
const webrtc = shallowRef(null)
const terminalReady = computed(() => !!webrtc.value?.signalingReady?.value)
const isSharedConnection = ref(false)
const webrtcConnecting = ref(false)
const webrtcStatus = ref('disconnected')
const webrtcError = ref(null)

// 多终端 Tab 与全屏支持相关状态
const adbSessions = ref([])
const activeSessionId = ref(null)
const nextSessionId = ref(1)
const sessionContainers = {}
const isMaximized = ref(false)
const consoleMainRef = ref(null)

const isAdbConnected = computed(() => {
  if (activeSessionId.value === null) return false
  const sess = adbSessions.value.find(s => s.id === activeSessionId.value)
  return sess ? sess.isConnected : false
})

function onDeviceSelectChange(e) {
  const newId = e.target.value
  if (newId) {
    deviceStore.openGlobalConsole(newId)
  }
}

let unwatchStatus = null
let unwatchError = null

// 安全访问 localStorage 的帮助函数
const safeStorageGet = (key, fallback = '') => {
  try {
    return localStorage.getItem(key) || fallback
  } catch (e) {
    return fallback
  }
}

const safeStorageSet = (key, val) => {
  try {
    localStorage.setItem(key, val)
  } catch (e) {
    console.warn('[Console] Failed to save to localStorage:', e)
  }
}

const statusText = computed(() => {
  if (webrtcError.value) return '错误'
  if (webrtcStatus.value === 'connected') return '在线'
  if (webrtcStatus.value === 'connecting') return '连接中'
  return '未连接'
})

const statusClass = computed(() => {
  return {
    connected: webrtcStatus.value === 'connected' && !webrtcError.value,
    connecting: webrtcStatus.value === 'connecting' && !webrtcError.value,
    disconnected: webrtcStatus.value === 'disconnected' || !!webrtcError.value,
    error: !!webrtcError.value
  }
})

// 初始化与连接管理
async function setupDeviceConnection(deviceId) {
  if (!deviceId) return
  
  cleanupConnection()
  webrtcConnecting.value = true
  webrtcError.value = null
  
  // 检查是否已有活跃的控制面板 WebRTC 实例
  let activeInstance = deviceStore.getWebRTC(deviceId)
  if (typeof activeInstance?.createAdbSessionChannel !== 'function') activeInstance = null
  if (activeInstance) {
    console.log('[Console] Reusing active WebRTC session for device:', deviceId)
  }
  
  if (activeInstance) {
    webrtc.value = activeInstance
    isSharedConnection.value = true
    webrtcStatus.value = webrtc.value.status.value || 'connected'
    webrtcError.value = webrtc.value.error?.value || null
    webrtcConnecting.value = false
    
    // 监听 WebRTC 状态变化
    unwatchStatus = watch(() => webrtc.value.status.value, (newStatus) => {
      webrtcStatus.value = newStatus || 'connected'
    }, { immediate: true })

    unwatchError = watch(() => webrtc.value.error?.value, (newErr) => {
      webrtcError.value = newErr || null
    }, { immediate: true })
    
    // 设置命令结果监听 (支持 Shell 终端打印)
    webrtc.value.onCommandResult(onCommandResultHandler)
  } else {
    // 创建 headless WebRTC 连接并绑定到隐藏的 dummyVideo 元素
    console.log('[Console] Connecting WebRTC (headless) for device:', deviceId)
    webrtcStatus.value = 'connecting'
    isSharedConnection.value = false
    try {
      const settings = getDeviceSettings(deviceId)
      const scrcpyOptions = {
        max_fps: settings.fps,
        max_size: settings.size,
        bitrate: settings.bitrate * 1000000,
        min_bitrate: settings.minBitrate * 1000000,
        max_bitrate: settings.maxBitrate * 1000000,
        bwe: settings.bwe,
        audio: settings.audio,
        audio_gain: settings.audioGain,
        audio_source: settings.audioSource,
        audio_dup: settings.audioDup,
        audio_low_latency: settings.audioLowLatency,
        debug: settings.debug,
        snapshot_interval: settings.snapshotInterval,
        power_off: settings.powerOff,
        video_source: settings.videoSource,
        camera_facing: settings.cameraFacing,
        camera_id: settings.cameraId,
        camera_size: settings.cameraSize,
        camera_fps: settings.cameraFps,
        camera_high_speed: settings.cameraHighSpeed,
        camera_ar: settings.cameraAr,
        // 无 WebRTC 时，终端直接使用已认证的信令 WebSocket。
        signalingOnly: !isWebRTCAvailable(),
        // 只读分享：屏蔽触控/键盘/剪贴板等一切输入注入
        view_only: props.accessMode === 'view_only'
      }

      webrtc.value = useWebRTC(deviceId, scrcpyOptions)
      
      unwatchStatus = watch(() => webrtc.value.status.value, (newStatus) => {
        webrtcStatus.value = newStatus || 'disconnected'
        if (newStatus === 'connected') {
          webrtcConnecting.value = false
        } else if (newStatus === 'failed' || newStatus === 'error' || newStatus === 'disconnected') {
          webrtcConnecting.value = false
        }
      }, { immediate: true })

      unwatchError = watch(() => webrtc.value.error?.value, (newErr) => {
        webrtcError.value = newErr || null
      }, { immediate: true })

      setTimeout(() => {
        if (webrtc.value && dummyVideo.value) {
          webrtc.value.setVideoGetter(() => dummyVideo.value)
          webrtc.value.connect(props.shareToken, props.sharePassword)
          webrtc.value.onCommandResult(onCommandResultHandler)
        }
      }, 50)
    } catch (e) {
      console.error('[Console] Failed to connect device:', e)
      webrtcStatus.value = 'disconnected'
      webrtcError.value = e.message || '初始化失败'
      webrtcConnecting.value = false
    }
  }
}

function onCommandResultHandler(res) {
  const text = [res.stdout, res.stderr].filter(Boolean).join('\n')
  const output = res.exit_code === 0 ? (text || '[Success]') : `${text}\n[Failed] ExitCode: ${res.exit_code}`.trim()
  consoleLogs.value.push({
    type: res.exit_code === 0 ? 'success' : 'error',
    text: output
  })
  scrollToBottom()
}

function cleanupConnection() {
  closeAdb()
  
  webrtcStatus.value = 'disconnected'
  webrtcError.value = null
  
  if (unwatchStatus) unwatchStatus()
  if (unwatchError) unwatchError()
  
  if (webrtc.value) {
    webrtc.value.onCommandResult(null)
    if (!isSharedConnection.value) {
      console.log('[Console] Disconnecting custom headless connection')
      try { webrtc.value.disconnect() } catch (e) {}
    }
  }
  webrtc.value = null
}

// --- 终端命令历史翻阅记录 ---
const cmdHistory = ref([])
const historyIndex = ref(-1)
let tempInput = ''

try {
  const saved = localStorage.getItem('cloudphone_shell_history')
  if (saved) {
    cmdHistory.value = JSON.parse(saved)
  }
} catch (e) {}

function navigateHistory(direction) {
  if (cmdHistory.value.length === 0) return
  
  if (direction === 'up') {
    if (historyIndex.value === -1) {
      tempInput = inputCmd.value
      historyIndex.value = cmdHistory.value.length - 1
    } else if (historyIndex.value > 0) {
      historyIndex.value--
    }
    inputCmd.value = cmdHistory.value[historyIndex.value]
  } else if (direction === 'down') {
    if (historyIndex.value === -1) return
    if (historyIndex.value === cmdHistory.value.length - 1) {
      historyIndex.value = -1
      inputCmd.value = tempInput
    } else {
      historyIndex.value++
      inputCmd.value = cmdHistory.value[historyIndex.value]
    }
  }
}

// 终端指令 (支持单台 WebRTC 实时指令及多台 HTTP 批量指令下发双通路)
async function execCmd() {
  if (!inputCmd.value.trim()) return
  const cmd = inputCmd.value.trim()
  
  // 将命令存入历史记录
  if (cmd) {
    if (cmdHistory.value.length === 0 || cmdHistory.value[cmdHistory.value.length - 1] !== cmd) {
      cmdHistory.value.push(cmd)
      if (cmdHistory.value.length > 100) {
        cmdHistory.value.shift()
      }
      try {
        localStorage.setItem('cloudphone_shell_history', JSON.stringify(cmdHistory.value))
      } catch (e) {}
    }
  }
  historyIndex.value = -1
  tempInput = ''

  inputCmd.value = ''

  const targets = targetDeviceIds.value

  if (targets.length === 1) {
    // 仅当前一台设备，走原生 WebRTC 极速实时通道
    if (!webrtc.value) {
      consoleLogs.value.push({ type: 'error', text: 'WebRTC 连接未就绪，无法发送指令。' })
      scrollToBottom()
      return
    }
    consoleLogs.value.push({ type: 'info', cmd: cmd, text: '执行中...' })
    webrtc.value.sendCommand(cmd)
    scrollToBottom()
  } else {
    // 多台目标设备，走批量 HTTP 任务下发通道
    const logItem = ref({
      type: 'batch_result',
      cmd: cmd,
      taskId: '',
      results: {}
    })
    
    // 初始化所有目标设备的状态
    targets.forEach(id => {
      logItem.value.results[id] = { status: 'running', output: 'Pending...' }
    })
    
    consoleLogs.value.push(logItem.value)
    scrollToBottom()

    try {
      const token = localStorage.getItem('auth_token') || ''
      const res = await fetch('/api/tasks', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': 'Bearer ' + token
        },
        body: JSON.stringify({
          type: 'shell',
          targets: targets,
          payload: cmd
        })
      })

      const data = await res.json()
      if (res.ok && data.status === 'success') {
        logItem.value.taskId = data.task_id
        deviceStore.startTrackingTask(data.task_id)
      } else {
        targets.forEach(id => {
          logItem.value.results[id] = { status: 'failed', output: data.error || '下发任务失败' }
        })
      }
    } catch (e) {
      targets.forEach(id => {
        logItem.value.results[id] = { status: 'failed', output: e.message }
      })
    }
  }
}

function quickCmd(cmd) {
  inputCmd.value = cmd
  execCmd()
}

function scrollToBottom() {
  nextTick(() => {
    if (consoleRef.value) {
      consoleRef.value.scrollTop = consoleRef.value.scrollHeight
    }
  })
}

// 全屏最大化切换
function toggleMaximize() {
  isMaximized.value = !isMaximized.value
  nextTick(() => {
    const activeSess = adbSessions.value.find(s => s.id === activeSessionId.value)
    if (activeSess && activeSess.isConnected && activeSess.adbInstance) {
      activeSess.adbInstance.resize()
    }
  })
}

// 二级多会话终端管理
function addAdbSession() {
  if (!terminalReady.value || adbSessions.value.length >= 5) return
  const id = nextSessionId.value++
  const newSession = {
    id,
    name: `Shell ${id}`,
    isConnected: false,
    adbInstance: null,
    unwatch: null
  }
  adbSessions.value.push(newSession)
  activeSessionId.value = id
  
  nextTick(() => {
    startAdbForSession(adbSessions.value.find(sess => sess.id === id))
  })
}

function startAdbForSession(sess) {
  if (webrtc.value) {
    const container = sessionContainers[sess.id]
    if (!container) return

    const { isAdbConnected: adbConnected, initAdb, closeAdb: close, resize: termResize } = useAdb(webrtc.value)
    sess.adbInstance = { initAdb, closeAdb: close, resize: termResize }
    
    sess.unwatch = watch(adbConnected, (val) => {
      sess.isConnected = val
    }, { immediate: true })
    
    sess.adbInstance.initAdb(container)
  }
}

function switchAdbSession(id) {
  activeSessionId.value = id
  nextTick(() => {
    const sess = adbSessions.value.find(s => s.id === id)
    if (sess && sess.adbInstance && sess.isConnected) {
      sess.adbInstance.resize()
    }
  })
}

function closeAdbSession(id) {
  const idx = adbSessions.value.findIndex(s => s.id === id)
  if (idx > -1) {
    const sess = adbSessions.value[idx]
    if (sess.adbInstance) {
      try { sess.adbInstance.closeAdb() } catch (e) {}
    }
    if (sess.unwatch) sess.unwatch()
    
    adbSessions.value.splice(idx, 1)
    delete sessionContainers[id]
    
    if (activeSessionId.value === id) {
      if (adbSessions.value.length > 0) {
        activeSessionId.value = adbSessions.value[adbSessions.value.length - 1].id
        nextTick(() => {
          const activeSess = adbSessions.value.find(s => s.id === activeSessionId.value)
          if (activeSess && activeSess.adbInstance && activeSess.isConnected) {
            activeSess.adbInstance.resize()
          }
        })
      } else {
        activeSessionId.value = null
      }
    }
  }
}

function cleanupAllAdbSessions() {
  adbSessions.value.forEach(sess => {
    if (sess.adbInstance) {
      try { sess.adbInstance.closeAdb() } catch (e) {}
    }
    if (sess.unwatch) sess.unwatch()
  })
  adbSessions.value = []
  activeSessionId.value = null
  nextSessionId.value = 1
  for (const k in sessionContainers) {
    delete sessionContainers[k]
  }
}

function closeAdb() {
  cleanupAllAdbSessions()
}

// 侦听变化
watch(() => props.deviceId, (newId) => {
  if (newId) {
    setupDeviceConnection(newId)
  }
})

// 监听活动连接的重建，保持同步
watch(() => deviceStore.activeWebRTCMap, (newMap) => {
  if (props.deviceId && newMap.has(props.deviceId)) {
    console.log('[Console] Active WebRTC changed, updating console connection...')
    setupDeviceConnection(props.deviceId)
  }
}, { deep: true })

function startResizingConsole(e) {
  e.preventDefault()
  const startY = e.clientY
  const startHeight = deviceStore.globalConsoleHeight

  const onMouseMove = (ev) => {
    const dy = ev.clientY - startY
    // 向上拉 dy 为负，所以 startHeight - dy 就是变大
    const newHeight = startHeight - dy
    deviceStore.setConsoleHeight(newHeight)
  }

  const onMouseUp = () => {
    document.removeEventListener('mousemove', onMouseMove)
    document.removeEventListener('mouseup', onMouseUp)
  }

  document.addEventListener('mousemove', onMouseMove)
  document.addEventListener('mouseup', onMouseUp)
}

let resizeObserver = null

onMounted(() => {
  setupDeviceConnection(props.deviceId)
  loadShortcuts()
  tagStore.load()
  
  if (typeof ResizeObserver !== 'undefined' && consoleMainRef.value) {
    resizeObserver = new ResizeObserver(() => {
      // 当容器高度或宽度变化时，让当前连接活跃的 ADB 终端自适应
      const activeSess = adbSessions.value.find(s => s.id === activeSessionId.value)
      if (activeSess && activeSess.isConnected && activeSess.adbInstance) {
        activeSess.adbInstance.resize()
      }
    })
    resizeObserver.observe(consoleMainRef.value)
  }
})

onUnmounted(() => {
  cleanupConnection()
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }
})
</script>

<style scoped>
.device-console {
  display: flex;
  flex-direction: column;
  width: 100%;
  flex: 1;
  background: #0f0f1a;
  color: #f0f6fc;
  border-radius: 12px 12px 0 0;
  overflow: hidden;
  box-shadow: inset 0 0 20px rgba(255, 255, 255, 0.02), 0 -8px 32px rgba(0, 0, 0, 0.5);
  border-top: 1px solid rgba(255, 255, 255, 0.08);
  position: relative;
}

.console-resizer {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 6px;
  cursor: ns-resize;
  z-index: 100;
  background: transparent;
  transition: background 0.2s;
}

.console-resizer:hover {
  background: rgba(88, 166, 255, 0.45);
}

/* 选项卡头部栏 */
.console-tabs-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: #161b22;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
  padding: 0 16px;
  flex-shrink: 0;
  user-select: none;
}

.console-right-tools {
  display: flex;
  align-items: center;
  gap: 12px;
}

.console-close-btn {
  background: none;
  border: none;
  color: #8b949e;
  cursor: pointer;
  padding: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  transition: all 0.2s;
}

.console-close-btn:hover {
  color: #f85149;
  background: rgba(248, 81, 73, 0.15);
}

.console-close-btn svg {
  width: 16px;
  height: 16px;
}

.tabs-group {
  display: flex;
  gap: 4px;
}

.tabs-group button {
  background: none;
  border: none;
  color: #8b949e;
  padding: 14px 16px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 8px;
  border-bottom: 2px solid transparent;
  transition: all 0.2s ease;
}

.tabs-group button.active {
  color: #58a6ff;
  border-bottom-color: #58a6ff;
  background: rgba(88, 166, 255, 0.06);
}

.tabs-group button:hover:not(.active) {
  color: #c9d1d9;
  background: rgba(255, 255, 255, 0.03);
}

.tab-icon {
  width: 16px;
  height: 16px;
}

.beta-badge {
  background: rgba(233, 69, 96, 0.2);
  color: #e94560;
  font-size: 9px;
  padding: 1px 4px;
  border-radius: 4px;
  border: 1px solid rgba(233, 69, 96, 0.3);
  margin-left: 2px;
}

.console-device-badge {
  display: flex;
  align-items: center;
  gap: 8px;
  background: rgba(0, 0, 0, 0.3);
  padding: 6px 12px;
  border-radius: 6px;
  border: 1px solid rgba(255, 255, 255, 0.05);
  font-family: monospace;
  font-size: 12px;
}

.status-indicator {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #8b949e;
}

.status-indicator.connected {
  background: #3fb950;
  box-shadow: 0 0 8px rgba(63, 185, 80, 0.6);
}

.status-indicator.connecting {
  background: #dbb32d;
  animation: pulse 1s infinite alternate;
}

.status-indicator.error {
  background: #f85149;
  box-shadow: 0 0 8px rgba(248, 81, 73, 0.6);
}

@keyframes pulse {
  0% { opacity: 0.4; }
  100% { opacity: 1; }
}

/* 选项卡内容区 */
.console-tab-content {
  min-width: 0;
  min-height: 0;
  flex: 1;
  overflow: hidden;
  display: flex;
  position: relative;
  background: #000000;
}

/* 1. Shell 选项卡样式 */
.shell-tab-panel {
  display: flex;
  flex-direction: column;
  flex: 1;
  height: 100%;
  overflow: hidden;
}

.console-history {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
  font-family: 'Fira Code', 'Courier New', monospace;
  font-size: 12px;
  background: #06060c;
  line-height: 1.5;
}

.console-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 70%;
  color: #44445c;
  gap: 12px;
}

.empty-icon {
  width: 40px;
  height: 40px;
}

.log-item {
  margin-bottom: 12px;
  animation: fadeIn 0.2s ease-out;
}

.log-cmd {
  color: #58a6ff;
  font-weight: bold;
}

.log-out {
  white-space: pre-wrap;
  word-break: break-all;
  margin: 4px 0 0 0;
  color: #c9d1d9;
}

.log-item.error .log-out {
  color: #ff5555;
  background: rgba(255, 85, 85, 0.05);
  padding: 4px 8px;
  border-radius: 4px;
}

.log-item.success .log-out {
  color: #50fa7b;
}

.console-shortcuts {
  display: flex;
  padding: 8px 16px;
  gap: 8px;
  background: #161b22;
  overflow-x: auto;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.console-shortcuts button {
  background: #21262d;
  border: 1px solid rgba(255, 255, 255, 0.08);
  color: #8b949e;
  padding: 5px 12px;
  border-radius: 6px;
  font-size: 11px;
  white-space: nowrap;
  cursor: pointer;
  transition: all 0.2s ease;
}

.console-shortcuts button:hover {
  background: #30363d;
  color: #f0f6fc;
  border-color: #8b949e;
}

.console-input-group {
  display: flex;
  padding: 12px 16px;
  gap: 8px;
  background: #161b22;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
}

.cmd-input {
  flex: 1;
  background: #0d1117;
  border: 1px solid rgba(255, 255, 255, 0.12);
  color: #fff;
  padding: 8px 12px;
  border-radius: 6px;
  outline: none;
  font-size: 13px;
}

.cmd-input:focus {
  border-color: #58a6ff;
  box-shadow: 0 0 8px rgba(88, 166, 255, 0.2);
}

.send-btn {
  background: #58a6ff;
  color: white;
  border: none;
  padding: 0 18px;
  border-radius: 6px;
  font-weight: 600;
  cursor: pointer;
  font-size: 13px;
  transition: background 0.2s;
}

.send-btn:hover:not(:disabled) {
  background: #1f85ff;
}

.send-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* 2. ADB 交互终端样式 */
.adb-tab-panel {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  flex: 1;
  height: 100%;
  display: flex;
  flex-direction: column;
}

.adb-placeholder {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 40px;
  color: #8b949e;
  background: #0c0c14;
}

.adb-icon {
  width: 64px;
  height: 64px;
  color: #58a6ff;
  margin-bottom: 16px;
  opacity: 0.7;
}

.adb-placeholder h3 {
  color: #f0f6fc;
  font-size: 16px;
  margin-bottom: 8px;
}

.adb-placeholder p {
  font-size: 13px;
  max-width: 420px;
  margin-bottom: 20px;
  line-height: 1.6;
}

.adb-connect-btn {
  background: #58a6ff;
  color: white;
  border: none;
  padding: 10px 24px;
  border-radius: 8px;
  font-weight: 600;
  font-size: 14px;
  cursor: pointer;
  transition: background 0.2s;
}

.adb-connect-btn:hover:not(:disabled) {
  background: #1f85ff;
}

.adb-connect-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.xterm-view-container {
  flex: 1;
  width: 100%;
  height: 0;
  min-height: 0;
  min-width: 0;
  overflow: hidden;
  box-sizing: border-box;
  background: #000;
}

.xterm-view-container :deep(.xterm) {
  height: 100%;
  padding: 12px;
  box-sizing: border-box;
}

.device-selector-dropdown {
  background: #21262d;
  border: 1px solid rgba(255, 255, 255, 0.15);
  border-radius: 6px;
  color: #c9d1d9;
  font-family: monospace;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  outline: none;
  padding: 4px 8px;
  transition: border-color 0.2s;
}

.device-selector-dropdown:hover {
  border-color: #58a6ff;
}

.device-selector-dropdown option {
  background: #161b22;
  color: #c9d1d9;
}

/* 全屏最大化样式 */
.device-console.is-maximized {
  position: fixed;
  top: 0;
  left: 0;
  width: 100vw !important;
  height: 100vh !important;
  z-index: 2100;
  border-radius: 0;
  border-top: none;
}

/* 控制台顶部工具按钮 */
.console-tool-btn {
  background: none;
  border: none;
  color: #8b949e;
  cursor: pointer;
  padding: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  transition: all 0.2s;
}

.console-tool-btn:hover {
  color: #58a6ff;
  background: rgba(88, 166, 255, 0.15);
}

.console-tool-btn svg {
  width: 15px;
  height: 15px;
}

/* 二级 ADB 多会话终端 Tab 栏样式 */
.adb-sessions-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: #0d1117;
  border-bottom: 1px solid rgba(255, 255, 255, 0.05);
  padding: 0 12px;
  height: 36px;
  flex-shrink: 0;
  user-select: none;
}

.adb-tabs-group {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 100%;
}

.adb-session-tab {
  background: none;
  border: none;
  color: #8b949e;
  padding: 0 12px;
  height: 28px;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 8px;
  border-radius: 4px;
  transition: all 0.2s ease;
}

.adb-session-tab:hover {
  color: #c9d1d9;
  background: rgba(255, 255, 255, 0.03);
}

.adb-session-tab.active {
  color: #58a6ff;
  background: rgba(88, 166, 255, 0.1);
  font-weight: 600;
}

.tab-status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #8b949e;
}

.tab-status-dot.connected {
  background: #3fb950;
  box-shadow: 0 0 8px rgba(63, 185, 80, 0.5);
}

.close-sess-btn {
  font-size: 14px;
  line-height: 1;
  color: #8b949e;
  transition: color 0.2s;
  padding: 2px;
  border-radius: 50%;
}

.close-sess-btn:hover {
  color: #f85149;
  background: rgba(248, 81, 73, 0.15);
}

.add-sess-btn {
  background: none;
  border: 1px dashed rgba(255, 255, 255, 0.15);
  color: #8b949e;
  width: 22px;
  height: 22px;
  font-size: 14px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  transition: all 0.2s;
  padding: 0;
}

.add-sess-btn:hover:not(:disabled) {
  color: #58a6ff;
  border-color: #58a6ff;
  background: rgba(88, 166, 255, 0.05);
}

.add-sess-btn:disabled {
  opacity: 0.3;
  cursor: not-allowed;
}

.max-sess-tip {
  font-size: 11px;
  color: #484f58;
}

/* 批量 Shell 样式系统 */
.batch-shell-tab-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: 16px;
  box-sizing: border-box;
  overflow: hidden;
}

.batch-shell-devices-selector {
  display: flex;
  align-items: center;
  gap: 10px;
}

/* 批量与单台整合终端样式 */
.shell-targets-bar {
  display: flex;
  flex-direction: column;
  gap: 8px;
  background: #161b22;
  border-top: 1px solid #30363d;
  border-bottom: 1px solid #30363d;
  padding: 8px 16px;
  box-sizing: border-box;
}

.targets-control-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 16px;
  width: 100%;
}

.select-all-check {
  display: inline-flex;
  align-items: center;
  font-size: 12px;
  color: #c9d1d9;
  cursor: pointer;
  user-select: none;
}

.select-all-check input {
  margin-right: 6px;
  cursor: pointer;
}

.tag-filters {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.tag-filter-label {
  font-size: 12px;
  color: #8b949e;
}

.tag-filter-btn {
  padding: 2px 8px;
  font-size: 11px;
  border-radius: 12px;
  border: 1px solid;
  cursor: pointer;
  transition: all 0.2s ease;
  outline: none;
  font-weight: 500;
  background: transparent;
}

.tag-filter-btn:hover {
  filter: brightness(1.2);
}

.shell-targets-bar .label {
  font-size: 12px;
  font-weight: 600;
  color: #8b949e;
}

.shell-targets-bar .targets-list {
  display: flex;
  align-items: center;
  gap: 12px;
}

.target-check {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: #c9d1d9;
  cursor: pointer;
  user-select: none;
}

.target-check.current {
  opacity: 0.8;
  cursor: default;
}

.target-check input[type="checkbox"] {
  width: 13px;
  height: 13px;
  accent-color: #58a6ff;
  cursor: pointer;
}

.target-check.current input[type="checkbox"] {
  cursor: default;
}

/* 整合历史面板中的批量输出渲染 */
.log-item.batch_result {
  border-left: 2px solid #58a6ff;
  padding-left: 8px;
  margin: 12px 0;
  background: rgba(88, 166, 255, 0.02);
}

.batch-outputs-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 8px;
}

.batch-output-row {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 4px;
  padding: 8px 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.batch-output-row.running {
  border-color: rgba(210, 153, 34, 0.4);
}

.batch-output-row.success {
  border-color: rgba(46, 160, 67, 0.4);
}

.batch-output-row.failed {
  border-color: rgba(248, 81, 73, 0.4);
}

.batch-output-row .row-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.batch-output-row .dev-tag {
  font-size: 12px;
  font-weight: 600;
  color: #c9d1d9;
}

.batch-output-row .status-tag {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 10px;
  font-weight: 600;
}

.batch-output-row .status-tag.running {
  background: rgba(210, 153, 34, 0.15);
  color: #d29922;
}

.batch-output-row .status-tag.success {
  background: rgba(46, 160, 67, 0.15);
  color: #56d364;
}

.batch-output-row .status-tag.failed {
  background: rgba(248, 81, 73, 0.15);
  color: #ff7b72;
}

.batch-output-row .dev-output {
  margin: 0;
  font-family: SFMono-Regular, Consolas, Liberation Mono, Menlo, monospace;
  font-size: 12px;
  line-height: 1.5;
  color: #c9d1d9;
  white-space: pre-wrap;
  word-break: break-all;
  background: #0d1117;
  padding: 6px 10px;
  border-radius: 4px;
}

/* 覆盖 xterm.js 视口与屏幕渲染的底部留白，防输入行遮挡 */
:deep(.xterm-viewport) {
  padding-bottom: 32px !important;
}
:deep(.xterm-screen) {
  padding-bottom: 32px !important;
}

.console-shortcuts button.system-btn {
  background: rgba(88, 166, 255, 0.1);
  color: #58a6ff;
  border-color: rgba(88, 166, 255, 0.2);
}

.console-shortcuts button.system-btn:hover {
  background: rgba(88, 166, 255, 0.2);
  color: #58a6ff;
  border-color: #58a6ff;
}

.console-shortcuts button.edit-btn {
  margin-left: auto;
}

/* 自定义快捷指令弹窗 */
.shortcut-modal-overlay {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  background: rgba(0, 0, 0, 0.7);
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  backdrop-filter: blur(4px);
}

.shortcut-modal-card {
  width: 90%;
  max-width: 600px;
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
  display: flex;
  flex-direction: column;
  max-height: 80%;
  animation: modalEnter 0.2s cubic-bezier(0.16, 1, 0.3, 1) forwards;
}

.shortcut-modal-card .modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px;
  border-bottom: 1px solid #30363d;
}

.shortcut-modal-card .modal-header h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: #f0f6fc;
}

.shortcut-modal-card .modal-header .close-btn {
  background: transparent;
  border: none;
  color: #8b949e;
  font-size: 16px;
  cursor: pointer;
  padding: 4px;
}

.shortcut-modal-card .modal-header .close-btn:hover {
  color: #f0f6fc;
}

.shortcut-modal-card .modal-body {
  padding: 16px;
  overflow-y: auto;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.shortcut-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.shortcut-item-row {
  display: flex;
  align-items: flex-end;
  gap: 10px;
  background: #0d1117;
  padding: 10px;
  border-radius: 6px;
  border: 1px solid #21262d;
}

.shortcut-item-row .input-col {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.shortcut-item-row .input-col label {
  font-size: 11px;
  color: #8b949e;
}

.shortcut-item-row .input-col input {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 4px;
  color: #c9d1d9;
  padding: 6px 10px;
  font-size: 12px;
  outline: none;
}

.shortcut-item-row .input-col input:focus {
  border-color: #58a6ff;
}

.shortcut-item-row .name-col {
  flex: 1;
}

.shortcut-item-row .cmd-col {
  flex: 2;
}

.shortcut-item-row .delete-btn {
  background: transparent;
  border: none;
  color: #f85149;
  cursor: pointer;
  padding: 8px;
  font-size: 14px;
}

.shortcut-item-row .delete-btn:hover {
  opacity: 0.8;
}

.no-shortcuts {
  text-align: center;
  color: #8b949e;
  padding: 20px 0;
  font-size: 12px;
}

.add-row-btn {
  background: transparent;
  border: 1px dashed #30363d;
  color: #58a6ff;
  padding: 8px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 12px;
  transition: all 0.2s ease;
  text-align: center;
}

.add-row-btn:hover {
  background: rgba(88, 166, 255, 0.05);
  border-color: #58a6ff;
}

.shortcut-modal-card .modal-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-top: 1px solid #30363d;
  background: #0d1117;
  border-bottom-left-radius: 8px;
  border-bottom-right-radius: 8px;
}

.shortcut-modal-card .modal-footer .btn {
  padding: 6px 12px;
  font-size: 12px;
  border-radius: 4px;
  cursor: pointer;
  border: 1px solid transparent;
}

.shortcut-modal-card .modal-footer .btn-reset {
  background: transparent;
  border-color: #30363d;
  color: #f85149;
}

.shortcut-modal-card .modal-footer .btn-reset:hover {
  background: rgba(248, 81, 73, 0.05);
}

.shortcut-modal-card .modal-footer .footer-actions {
  display: flex;
  gap: 8px;
}

.shortcut-modal-card .modal-footer .btn-cancel {
  background: transparent;
  border-color: #30363d;
  color: #c9d1d9;
}

.shortcut-modal-card .modal-footer .btn-cancel:hover {
  background: rgba(255, 255, 255, 0.05);
}

.shortcut-modal-card .modal-footer .btn-save {
  background: #238636;
  color: #fff;
}

.shortcut-modal-card .modal-footer .btn-save:hover {
  background: #2ea043;
}
</style>
