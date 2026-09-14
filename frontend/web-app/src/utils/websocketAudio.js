export function isWebSocketAudioAvailable() {
  return typeof (globalThis.AudioContext || globalThis.webkitAudioContext) === 'function' &&
    typeof WebAssembly !== 'undefined'
}

export function parseAudioFrame(buffer, deviceId) {
  if (!(buffer instanceof ArrayBuffer) || buffer.byteLength <= 49 || buffer.byteLength > 65585) return null
  const bytes = new Uint8Array(buffer)
  if (bytes[0] !== 79 || bytes[1] !== 80 || bytes[2] !== 85 || bytes[3] !== 83) return null
  const view = new DataView(buffer)
  const embeddedId = new TextDecoder().decode(bytes.subarray(4, 36)).replace(/\0+$/, '')
  const channels = bytes[36]
  if (embeddedId !== deviceId || (channels !== 1 && channels !== 2) || view.getUint32(45) !== buffer.byteLength - 49) return null
  return { data: bytes.subarray(49), channels, pts: Number(view.getBigUint64(37)) }
}

const TARGET_LEAD = 0.04
const MAX_LEAD = 0.16
const MAX_PENDING_PACKETS = 6

async function createOpusDecoder() {
  const { OpusDecoderWebWorker } = await import('opus-decoder')
  const decoder = new OpusDecoderWebWorker({ sampleRate: 48000, channels: 2 })
  let timer
  try {
    await Promise.race([
      decoder.ready,
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('音频解码器初始化超时')), 8000) })
    ])
    return decoder
  } catch (error) {
    decoder.terminate()
    throw error
  } finally {
    clearTimeout(timer)
  }
}

// 解码在 Worker 中进行；Web Audio 在 HTTP 下也可用，不依赖 WebRTC、
// AudioDecoder 或要求安全上下文的 AudioWorklet。队列有界，卡顿后丢弃旧声音。
export class WebSocketAudioPlayer {
  constructor({ muted = false, gain = 1, onError = () => {}, onPlaying = () => {}, decoderFactory = createOpusDecoder } = {}) {
    const Context = globalThis.AudioContext || globalThis.webkitAudioContext
    this.context = new Context({ latencyHint: 'interactive', sampleRate: 48000 })
    this.gain = this.context.createGain()
    this.gain.connect(this.context.destination)
    this.gainValue = Math.max(0.5, Math.min(3, Number(gain) || 1))
    this.muted = muted
    this.gain.gain.value = muted ? 0 : this.gainValue
    this.onError = onError
    this.onPlaying = onPlaying
    this.queue = []
    this.sources = new Set()
    this.nextTime = 0
    this.lastPTS = 0
    this.closed = false
    this.processing = false
    this.needsReset = false
    this.samplesDecoded = 0
    this.droppedPackets = 0
    this.unlock = () => this.resume()
    for (const event of ['pointerdown', 'keydown', 'touchstart']) globalThis.addEventListener?.(event, this.unlock, { capture: true })
    this.resume()
    this.ready = decoderFactory().then(decoder => {
      if (this.closed) { decoder.terminate(); return }
      this.decoder = decoder
      this.pump()
    }).catch(error => this.fail(error))
  }

  resume() {
    if (this.closed) return
    this.context.resume().then(() => {
      if (this.context.state === 'running') this.removeUnlock()
    }).catch(() => {})
  }

  removeUnlock() {
    for (const event of ['pointerdown', 'keydown', 'touchstart']) globalThis.removeEventListener?.(event, this.unlock, { capture: true })
  }

  setMuted(muted) {
    this.muted = Boolean(muted)
    this.gain.gain.value = this.muted ? 0 : this.gainValue
    if (!this.muted) this.resume()
  }

  feed(packet) {
    if (this.closed) return
    if (this.queue.length >= MAX_PENDING_PACKETS) {
      this.queue.shift()
      this.droppedPackets++
      this.needsReset = true
    }
    this.queue.push({ ...packet, receivedAt: performance.now() })
    this.pump()
  }

  async pump() {
    if (!this.decoder || this.processing || this.closed) return
    this.processing = true
    try {
      while (this.queue.length && !this.closed) {
        const packet = this.queue.shift()
        if (this.context.state !== 'running' || performance.now() - packet.receivedAt > MAX_LEAD * 1000) {
          this.droppedPackets++
          this.needsReset = true
          continue
        }
        if (this.needsReset || (this.lastPTS && (packet.pts < this.lastPTS || packet.pts - this.lastPTS > 300000))) {
          await this.decoder.reset()
          if (this.closed) return
          this.needsReset = false
          this.flush()
        }
        this.lastPTS = packet.pts
        const decoded = await this.decoder.decodeFrame(packet.data)
        if (this.closed) return
        if (decoded.errors?.length) throw new Error('Opus 音频解码失败')
        if (!decoded.samplesDecoded) continue
        this.samplesDecoded += decoded.samplesDecoded
        if (performance.now() - packet.receivedAt > MAX_LEAD * 1000) {
          this.droppedPackets++
          continue
        }
        this.schedule(decoded)
      }
    } catch (error) {
      this.fail(error)
    } finally {
      this.processing = false
    }
  }

  schedule({ channelData, samplesDecoded, sampleRate }) {
    const now = this.context.currentTime
    if (this.nextTime - now + samplesDecoded / sampleRate > MAX_LEAD) this.flush()
    if (this.nextTime < now + 0.005) this.nextTime = now + TARGET_LEAD
    const buffer = this.context.createBuffer(channelData.length, samplesDecoded, sampleRate)
    channelData.forEach((channel, index) => buffer.copyToChannel(channel, index))
    const source = this.context.createBufferSource()
    source.buffer = buffer
    source.connect(this.gain)
    source.onended = () => { this.sources.delete(source); source.disconnect() }
    this.sources.add(source)
    source.start(this.nextTime)
    this.nextTime += samplesDecoded / sampleRate
    this.onPlaying()
  }

  flush() {
    for (const source of this.sources) {
      try { source.stop() } catch {}
      source.disconnect()
    }
    this.sources.clear()
    this.nextTime = 0
  }

  stats() {
    return {
      samplesDecoded: this.samplesDecoded,
      bufferMs: Math.max(0, (this.nextTime - this.context.currentTime) * 1000),
      droppedPackets: this.droppedPackets,
      state: this.context.state
    }
  }

  fail(error) {
    if (this.closed) return
    this.onError(error.message || '音频播放失败')
    this.close()
  }

  close() {
    if (this.closed) return
    this.closed = true
    this.removeUnlock()
    this.queue = []
    this.flush()
    this.decoder?.terminate()
    this.gain.disconnect()
    this.context.close().catch(() => {})
  }
}
