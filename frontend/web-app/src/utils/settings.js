export const defaultSettings = {
  fps: 0,
  size: 0,
  bitrate: 4,
  bwe: true,
  minBitrate: 8,
  maxBitrate: 20,
  audio: false,
  audioGain: 1,
  audioSource: 'output',
  audioDup: true,
  audioLowLatency: false,
  pageAudioMuted: false,
  debug: false,
  snapshotInterval: 10,
  powerOff: false,
  connectionPath: 'auto',
  ipPreference: 'auto',
  showStats: false,
  videoCodecOptions: '',
  camera: false,
  previewFps: 10,
  previewSize: 360,
  previewDecoder: 'wasm',
  previewBitrate: 1,
  renderEngine: 'video',
  connectionStayAwake: true,
  previewStayAwake: false,
  videoSource: 'display',
  cameraFacing: 'back',
  cameraId: '',
  cameraSize: '',
  cameraFps: 0,
  cameraHighSpeed: false,
  cameraAr: ''
}

export function parseSettings(value) {
  const parsed = value && typeof value === 'object' && !Array.isArray(value) ? { ...value } : {}
  // 拆分后的两个开关独立采用新默认值，旧的合并开关不再覆盖它们。
  delete parsed.stayAwake
  const hasAudioDup = Object.prototype.hasOwnProperty.call(parsed, 'audioDup')
  if (parsed.bitrate > 1000) {
    parsed.bitrate = Math.max(0.1, Math.round(parsed.bitrate / 100000) / 10)
    if (parsed.minBitrate > 1000) parsed.minBitrate = Math.max(1, Math.round(parsed.minBitrate / 1000000))
    if (parsed.maxBitrate > 1000) parsed.maxBitrate = Math.max(1, Math.round(parsed.maxBitrate / 1000000))
  }
  if (parsed.previewBitrate > 1000) {
    parsed.previewBitrate = Math.max(1, Math.round(parsed.previewBitrate / 1000000))
  }
  if (!hasAudioDup && parsed.audioSource === 'output') parsed.audioSource = defaultSettings.audioSource
  return parsed
}

export function getDeviceSettings(deviceId) {
  let globalSettings = { ...defaultSettings }
  try {
    const storedGlobal = localStorage.getItem('cloudphone_settings')
    if (storedGlobal) {
      globalSettings = { ...globalSettings, ...parseSettings(JSON.parse(storedGlobal)) }
      globalSettings.videoSource = 'display'
      localStorage.setItem('cloudphone_settings', JSON.stringify(globalSettings))
    }
  } catch(e) {}

  if (!deviceId) return globalSettings

  try {
    const remote = JSON.parse(localStorage.getItem('cloudphone_device_defaults') || '{}')
    globalSettings = { ...globalSettings, ...parseSettings(remote[deviceId]) }
    const storedDev = localStorage.getItem(`cloudphone_settings_${deviceId}`)
    const serverManaged = localStorage.getItem('auth_role') === 'admin' && Object.keys(remote[deviceId] || {}).length > 0
    if (storedDev && !serverManaged) {
      const devSettings = { ...globalSettings, ...parseSettings(JSON.parse(storedDev)) }
      // 默认持久化配置中视频源恒为屏幕 display，避免单机配置残留导致卡片误连摄像头
      devSettings.videoSource = 'display'
      return devSettings
    }
  } catch(e) {}
  
  return globalSettings
}

function settingsHeaders() {
  return { 'Content-Type': 'application/json', Authorization: `Bearer ${localStorage.getItem('auth_token') || ''}` }
}

export function updateRemoteDeviceSettings(deviceId, settings, notify = true) {
  let remote = {}
  try { remote = JSON.parse(localStorage.getItem('cloudphone_device_defaults') || '{}') } catch {}
  if (settings && Object.keys(settings).length) remote[deviceId] = settings
  else {
    delete remote[deviceId]
    if (localStorage.getItem('auth_role') === 'admin') localStorage.removeItem(`cloudphone_settings_${deviceId}`)
  }
  localStorage.setItem('cloudphone_device_defaults', JSON.stringify(remote))
  if (notify) window.dispatchEvent(new CustomEvent('cloudphone-settings-updated', { detail: { deviceId } }))
}

export async function loadRemoteDeviceSettings() {
  const response = await fetch('/api/device_settings', { headers: settingsHeaders() })
  if (!response.ok) throw new Error('无法读取设备设置')
  const remote = await response.json()
  if (localStorage.getItem('auth_role') === 'admin') {
    let previous = {}
    try { previous = JSON.parse(localStorage.getItem('cloudphone_device_defaults') || '{}') } catch {}
    // 其他浏览器恢复默认后，删除该设备以前同步过的本地副本。
    for (const deviceId of Object.keys(previous || {})) {
      if (!Object.prototype.hasOwnProperty.call(remote, deviceId)) localStorage.removeItem(`cloudphone_settings_${deviceId}`)
    }
  }
  localStorage.setItem('cloudphone_device_defaults', JSON.stringify(remote))
  window.dispatchEvent(new CustomEvent('cloudphone-settings-updated', { detail: { deviceId: '' } }))
}

export async function saveDeviceSettings(deviceId, newSettings) {
  // 持久化存储时，视频源始终默认为 display（相机镜头/分辨率等参数完整保留），仅由运行时意图动态激活 camera
  const settingsToStore = { ...parseSettings(newSettings), videoSource: 'display' }
  if (!deviceId || localStorage.getItem('auth_role') === 'admin') {
    const response = await fetch(deviceId ? `/api/devices/${encodeURIComponent(deviceId)}/settings` : '/api/default_settings', {
      method: 'POST', headers: settingsHeaders(), body: JSON.stringify(settingsToStore)
    })
    if (!response.ok) throw new Error(`保存设置失败 (${response.status})`)
    if (deviceId) updateRemoteDeviceSettings(deviceId, settingsToStore, false)
  }
  if (!deviceId) {
    localStorage.setItem('cloudphone_settings', JSON.stringify(settingsToStore))
  } else {
    localStorage.setItem(`cloudphone_settings_${deviceId}`, JSON.stringify(settingsToStore))
  }
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent('cloudphone-settings-updated', { detail: { deviceId } }))
  }
}

export function hasCustomSettings(deviceId) {
  if (!deviceId) return false
  if (localStorage.getItem(`cloudphone_settings_${deviceId}`) !== null) return true
  if (localStorage.getItem('auth_role') === 'admin') {
    try {
      const remote = JSON.parse(localStorage.getItem('cloudphone_device_defaults') || '{}')
      return Object.keys(remote[deviceId] || {}).length > 0
    } catch {}
  }
  return false
}

export async function deleteDeviceSettings(deviceId) {
  if (deviceId) {
    if (localStorage.getItem('auth_role') === 'admin') {
      const response = await fetch(`/api/devices/${encodeURIComponent(deviceId)}/settings`, { method: 'DELETE', headers: settingsHeaders() })
      if (!response.ok) throw new Error(`恢复设备设置失败 (${response.status})`)
      updateRemoteDeviceSettings(deviceId, {}, false)
    }
    localStorage.removeItem(`cloudphone_settings_${deviceId}`)
    localStorage.removeItem(`cloudphone_camera_pref_${deviceId}`)
    window.dispatchEvent(new CustomEvent('cloudphone-settings-updated', { detail: { deviceId } }))
  }
}

// 所有连接入口共用参数映射，避免 WebSocket、分享页与主控页各自遗漏选项。
export function buildStreamOptions(settings, { preview = false } = {}) {
  const s = { ...defaultSettings, ...parseSettings(settings) }
  return {
    preview,
    max_fps: preview ? s.previewFps : s.fps,
    max_size: preview ? s.previewSize : s.size,
    bitrate: (preview ? s.previewBitrate : s.bitrate) * 1000000,
    min_bitrate: s.minBitrate * 1000000,
    max_bitrate: s.maxBitrate * 1000000,
    bwe: preview ? false : s.bwe,
    audio: preview ? false : s.audio,
    audio_gain: s.audioGain,
    audio_source: s.audioSource,
    audio_dup: s.audioDup,
    audio_low_latency: s.audioLowLatency,
    debug: s.debug,
    snapshot_interval: s.snapshotInterval,
    power_off: preview ? false : s.powerOff,
    video_codec_options: s.videoCodecOptions,
    camera: s.camera,
    stay_awake: preview ? s.previewStayAwake : s.connectionStayAwake,
    video_source: preview ? 'display' : s.videoSource,
    camera_facing: s.cameraFacing,
    camera_id: s.cameraId,
    camera_size: s.cameraSize,
    camera_fps: s.cameraFps,
    camera_high_speed: s.cameraHighSpeed,
    camera_ar: s.cameraAr,
    camera_zoom: s.cameraZoomRatio ?? 1,
    camera_orientation: s.cameraOrientation ?? 'auto',
    connectionPath: s.connectionPath,
    ipPreference: s.ipPreference,
    renderEngine: s.renderEngine
  }
}

// --- 摄像头监控专属偏好记忆（按单机隔离，独立于屏幕连接配置） ---
export const defaultCameraPreferences = {
  cameraFacing: 'back',
  cameraId: '',
  cameraSize: '1920x1080',
  cameraFps: 30,
  cameraZoomRatio: 1.0,
  cameraOrientation: 'auto',
  audioSource: 'mic'
}

export function getCameraPreferences(deviceId) {
  let pref = { ...defaultCameraPreferences }
  if (!deviceId) return pref
  try {
    const stored = localStorage.getItem(`cloudphone_camera_pref_${deviceId}`)
    if (stored) {
      pref = { ...pref, ...JSON.parse(stored) }
    }
  } catch (e) {}
  return pref
}

export function saveCameraPreferences(deviceId, pref) {
  if (!deviceId) return
  try {
    const current = getCameraPreferences(deviceId)
    const updated = { ...current, ...pref }
    localStorage.setItem(`cloudphone_camera_pref_${deviceId}`, JSON.stringify(updated))
  } catch (e) {}
}

// --- 用户级设置管控策略（管理员在用户管理中配置，服务端信令层同步强制） ---

// 设置维度 -> settings 键
export const POLICY_DIM_KEYS = {
  bitrate: ['bitrate', 'minBitrate', 'maxBitrate', 'bwe'],
  fps: ['fps'],
  resolution: ['size'],
  audio: ['audio', 'audioGain', 'audioSource', 'audioDup', 'audioLowLatency']
}

// policy(authStore.userPolicy) -> SettingsModal 的 lockedSections 数组
export function policyLockedSections(policy) {
  if (!policy) return []
  const arr = []
  if (policy.forbid_bitrate) arr.push('bitrate')
  if (policy.forbid_fps) arr.push('fps')
  if (policy.forbid_resolution) arr.push('size')
  if (policy.forbid_audio) arr.push('audio')
  return arr
}

// 将管控策略叠加到本地设置上：被禁维度使用管理员配置值
// （无配置值时保留本地显示值，服务端仍会剥离并回落设备默认）
export function applyPolicyToSettings(settings, policy) {
  if (!policy) return settings
  const enforced = policy.settings || {}
  const merged = { ...settings }
  const applyDim = (forbidden, keys) => {
    if (!forbidden) return
    for (const k of keys) {
      if (enforced[k] !== undefined) merged[k] = enforced[k]
    }
  }
  applyDim(policy.forbid_bitrate, POLICY_DIM_KEYS.bitrate)
  applyDim(policy.forbid_fps, POLICY_DIM_KEYS.fps)
  applyDim(policy.forbid_resolution, POLICY_DIM_KEYS.resolution)
  applyDim(policy.forbid_audio, POLICY_DIM_KEYS.audio)
  return merged
}
