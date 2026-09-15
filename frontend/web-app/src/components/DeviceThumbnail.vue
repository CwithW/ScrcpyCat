<template>
  <canvas v-show="rendered" ref="canvas" class="thumbnail-canvas" :title="error" />
  <img v-if="!rendered && device.snapshot" :src="device.snapshot" class="thumbnail-image" alt="" loading="lazy" :title="error" />
  <span v-else-if="!rendered" :title="error">📱</span>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useDeviceStore } from '@/stores/devices'
import { getDeviceSettings, buildStreamOptions } from '@/utils/settings'
import { PreviewDecoder } from '@/utils/previewDecoder'

const props = defineProps({ device: { type: Object, required: true } })
const store = useDeviceStore()
const canvas = ref(null)
const rendered = ref(false)
const visible = ref(false)
const error = ref('')
let observer, decoder
const subscriber = 'table'
const enabled = computed(() => store.globalPreviewMode && visible.value && props.device.status === 'online' && !store.activeDeviceIds.includes(props.device.id))

function stop() {
  if (!decoder) return
  decoder.close()
  decoder = null
  rendered.value = false
  store.unregisterPreviewCallback(props.device.id, subscriber)
  if (!store.hasPreviewSubscribers(props.device.id)) store.sendPreviewControl('stop_preview', props.device.id)
}

function start() {
  stop()
  if (!enabled.value) return
  error.value = ''
  const settings = getDeviceSettings(props.device.id)
  decoder = new PreviewDecoder(() => canvas.value, {
    mode: settings.previewDecoder,
    onFrame: () => { rendered.value = true },
    onError: failure => { error.value = failure.message; stop() }
  })
  store.registerPreviewCallback(props.device.id, subscriber, (data, key, timestamp) => decoder?.feed(data, key, timestamp))
  store.sendPreviewControl('start_preview', props.device.id, buildStreamOptions(settings, { preview: true }))
}

function settingsUpdated(event) {
  if (!event.detail?.deviceId || event.detail.deviceId === props.device.id) start()
}

watch(enabled, value => value ? start() : stop())
onMounted(() => {
  observer = new IntersectionObserver(entries => { visible.value = entries[0].isIntersecting })
  observer.observe(canvas.value.parentElement)
  window.addEventListener('cloudphone-settings-updated', settingsUpdated)
})
onUnmounted(() => {
  observer?.disconnect()
  window.removeEventListener('cloudphone-settings-updated', settingsUpdated)
  stop()
})
</script>

<style scoped>
.thumbnail-canvas, .thumbnail-image { display: block; width: 100%; height: 100%; object-fit: contain; }
</style>
