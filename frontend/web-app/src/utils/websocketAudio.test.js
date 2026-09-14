import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parseAudioFrame, WebSocketAudioPlayer } from './websocketAudio.js'

function envelope(id = 'phone') {
  const buffer = new ArrayBuffer(52)
  const bytes = new Uint8Array(buffer)
  bytes.set(new TextEncoder().encode('OPUS'), 0)
  bytes.set(new TextEncoder().encode(id), 4)
  bytes[36] = 2
  new DataView(buffer).setBigUint64(37, 123456n)
  new DataView(buffer).setUint32(45, 3)
  bytes.set([0xf8, 0xff, 0xfe], 49)
  return buffer
}

test('audio packets are bound to a device and validate payload boundaries', () => {
  const packet = parseAudioFrame(envelope(), 'phone')
  assert.equal(packet.pts, 123456)
  assert.deepEqual([...packet.data], [0xf8, 0xff, 0xfe])
  assert.equal(parseAudioFrame(envelope(), 'another-phone'), null)
  for (const size of [0, 4, 48, 49]) assert.equal(parseAudioFrame(new ArrayBuffer(size), 'phone'), null)
  const malformed = envelope()
  new DataView(malformed).setUint32(45, 999)
  assert.equal(parseAudioFrame(malformed, 'phone'), null)
})

class FakeContext {
  currentTime = 1
  state = 'running'
  destination = {}
  starts = []
  stopped = 0
  createGain() { return { gain: { value: 1 }, connect() {}, disconnect() {} } }
  createBuffer(channels, samples, rate) { return { duration: samples / rate, copyToChannel() {} } }
  createBufferSource() {
    return {
      connect() {}, disconnect() {},
      start: time => { this.starts.push(time) },
      stop: () => { this.stopped++ }
    }
  }
  resume() { return Promise.resolve() }
  close() { this.state = 'closed'; return Promise.resolve() }
}

test('WebSocket audio bounds buffered playback, mutes real gain, and releases resources', async () => {
  const original = globalThis.AudioContext
  globalThis.AudioContext = FakeContext
  let terminated = false
  const decoder = { terminate() { terminated = true } }
  const player = new WebSocketAudioPlayer({ decoderFactory: async () => decoder })
  try {
    await player.ready
    const decoded = { channelData: [new Float32Array(960), new Float32Array(960)], samplesDecoded: 960, sampleRate: 48000 }
    for (let i = 0; i < 200; i++) player.schedule(decoded)
    assert(player.stats().bufferMs <= 161, 'a network burst must not create seconds of playback delay')
    assert(player.context.stopped > 0, 'old scheduled audio must be discarded')
    player.setMuted(true)
    assert.equal(player.gain.gain.value, 0)
    player.setMuted(false)
    assert.equal(player.gain.gain.value, 1)
    player.close()
    assert.equal(terminated, true)
    assert.equal(player.context.state, 'closed')
    assert.equal(player.sources.size, 0)
  } finally {
    player.close()
    globalThis.AudioContext = original
  }
})

test('decoder initialization and suspended playback never accumulate an unbounded queue', async () => {
  const original = globalThis.AudioContext
  globalThis.AudioContext = FakeContext
  let resolveDecoder
  let terminated = false
  const player = new WebSocketAudioPlayer({ decoderFactory: () => new Promise(resolve => { resolveDecoder = resolve }) })
  try {
    for (let i = 0; i < 1000; i++) player.feed({ data: new Uint8Array([0xf8, 0xff, 0xfe]), pts: i * 20000 })
    assert.equal(player.queue.length, 6)
    assert.equal(player.droppedPackets, 994)
    player.close()
    resolveDecoder({ terminate() { terminated = true } })
    await player.ready
    assert.equal(terminated, true)
    assert.equal(player.queue.length, 0)
  } finally {
    player.close()
    globalThis.AudioContext = original
  }
})
