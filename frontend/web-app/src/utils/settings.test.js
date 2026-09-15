import test from 'node:test'
import assert from 'node:assert/strict'
import { defaultSettings, buildStreamOptions, getDeviceSettings, parseSettings, saveDeviceSettings, deleteDeviceSettings, hasCustomSettings, updateRemoteDeviceSettings, loadRemoteDeviceSettings } from './settings.js'

function storage() {
  const values = new Map()
  return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)), removeItem: key => values.delete(key) }
}

test('connection and preview defaults have separate wake policies and preserve zero limits', () => {
  const connection = buildStreamOptions(defaultSettings)
  const preview = buildStreamOptions(defaultSettings, { preview: true })
  assert.equal(defaultSettings.showStats, false)
  assert.equal(connection.stay_awake, true)
  assert.equal(preview.stay_awake, false)
  assert.equal(connection.max_fps, 0)
  assert.equal(connection.max_size, 0)
  assert.equal(preview.max_size, 360)
  assert.equal(buildStreamOptions({ previewSize: 0 }, { preview: true }).max_size, 0)
  assert.equal(parseSettings({ stayAwake: false }).connectionStayAwake, undefined)
})

test('shared mapping carries browser, encoder, audio and device options', () => {
  const options = buildStreamOptions({ connectionPath: 'relay', ipPreference: 'ipv6', renderEngine: 'webcodecs',
    powerOff: true, videoCodecOptions: 'i-frame-interval=2', connectionStayAwake: false,
    debug: true, snapshotInterval: -1, audio: true, audioSource: 'mic', audioGain: 2, audioDup: false })
  assert.equal(options.connectionPath, 'relay')
  assert.equal(options.ipPreference, 'ipv6')
  assert.equal(options.renderEngine, 'webcodecs')
  assert.equal(options.power_off, true)
  assert.equal(options.video_codec_options, 'i-frame-interval=2')
  assert.equal(options.stay_awake, false)
  assert.equal(options.debug, true)
  assert.equal(options.snapshot_interval, -1)
  assert.equal(options.audio_source, 'mic')
  assert.equal(options.audio_gain, 2)
  assert.equal(options.audio_dup, false)
})

test('partial device overrides inherit the global settings', t => {
  t.mock.method(globalThis, 'fetch', async () => { throw new Error('unexpected request') })
  const previous = globalThis.localStorage
  globalThis.localStorage = storage()
  t.after(() => { globalThis.localStorage = previous })
  localStorage.setItem('cloudphone_settings', JSON.stringify({ connectionStayAwake: false, renderEngine: 'webcodecs', previewStayAwake: true }))
  localStorage.setItem('cloudphone_settings_phone', JSON.stringify({ bitrate: 2 }))
  const settings = getDeviceSettings('phone')
  assert.equal(settings.connectionStayAwake, false)
  assert.equal(settings.previewStayAwake, true)
  assert.equal(settings.renderEngine, 'webcodecs')
  assert.equal(settings.bitrate, 2)
})

test('failed remote saves leave the previous local settings intact', async t => {
  const previous = globalThis.localStorage
  globalThis.localStorage = storage()
  t.after(() => { globalThis.localStorage = previous })
  localStorage.setItem('auth_role', 'admin')
  localStorage.setItem('cloudphone_settings_phone', JSON.stringify({ bitrate: 2 }))
  t.mock.method(globalThis, 'fetch', async () => ({ ok: false, status: 503 }))
  await assert.rejects(saveDeviceSettings('phone', { bitrate: 4 }), /503/)
  assert.equal(JSON.parse(localStorage.getItem('cloudphone_settings_phone')).bitrate, 2)
})

test('save and reset notify previews only after local and remote defaults are consistent', async t => {
  const previousStorage = globalThis.localStorage, previousWindow = globalThis.window
  globalThis.localStorage = storage()
  const observed = []
  globalThis.window = { dispatchEvent: () => observed.push(getDeviceSettings('phone').bitrate) }
  t.after(() => { globalThis.localStorage = previousStorage; globalThis.window = previousWindow })
  localStorage.setItem('auth_role', 'admin')
  localStorage.setItem('cloudphone_settings', JSON.stringify({ bitrate: 4 }))
  localStorage.setItem('cloudphone_settings_phone', JSON.stringify({ bitrate: 2 }))
  t.mock.method(globalThis, 'fetch', async () => ({ ok: true }))
  await saveDeviceSettings('phone', { bitrate: 6 })
  await deleteDeviceSettings('phone')
  assert.deepEqual(observed, [6, 4])
})

test('administrator device defaults remain editable from another browser and supersede stale local copies', t => {
  const previous = globalThis.localStorage
  globalThis.localStorage = storage()
  t.after(() => { globalThis.localStorage = previous })
  localStorage.setItem('auth_role', 'admin')
  localStorage.setItem('cloudphone_device_defaults', JSON.stringify({ phone: { bitrate: 6 } }))
  assert.equal(hasCustomSettings('phone'), true)
  localStorage.setItem('cloudphone_settings_phone', JSON.stringify({ bitrate: 2 }))
  assert.equal(getDeviceSettings('phone').bitrate, 6)
  localStorage.setItem('auth_role', 'user')
  assert.equal(getDeviceSettings('phone').bitrate, 2)
})

test('administrator resets remove stale local copies through live updates and offline reloads', async t => {
  const previousStorage = globalThis.localStorage, previousWindow = globalThis.window
  globalThis.localStorage = storage()
  globalThis.window = { dispatchEvent() {} }
  t.after(() => { globalThis.localStorage = previousStorage; globalThis.window = previousWindow })
  localStorage.setItem('auth_role', 'admin')
  localStorage.setItem('cloudphone_settings', JSON.stringify({ bitrate: 4 }))
  const previousDevice = () => {
    localStorage.setItem('cloudphone_device_defaults', JSON.stringify({ phone: { bitrate: 6 } }))
    localStorage.setItem('cloudphone_settings_phone', JSON.stringify({ bitrate: 6 }))
  }
  previousDevice()
  updateRemoteDeviceSettings('phone', {})
  assert.equal(getDeviceSettings('phone').bitrate, 4)
  assert.equal(hasCustomSettings('phone'), false)
  previousDevice()
  t.mock.method(globalThis, 'fetch', async () => ({ ok: true, json: async () => ({}) }))
  await loadRemoteDeviceSettings()
  assert.equal(getDeviceSettings('phone').bitrate, 4)
  assert.equal(hasCustomSettings('phone'), false)
})
