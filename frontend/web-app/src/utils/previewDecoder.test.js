import test from 'node:test'
import assert from 'node:assert/strict'
import { PreviewDecoder } from './previewDecoder.js'

// Chrome 编码的 32×32 纯灰色合成图，含 SPS/PPS/IDR，无设备画面。
const frame = Uint8Array.from(Buffer.from('000000016742d00b8c68949a80808083c2211a800000000168ce3c800000000165b8000409fffff87afc9d75e0', 'hex'))

test('WASM preview decodes a complete Annex B access unit and waits for a keyframe', () => {
  let image, frames = 0
  const canvas = { width: 0, height: 0, getContext: () => ({
    createImageData: (width, height) => ({ data: new Uint8ClampedArray(width * height * 4) }),
    putImageData: value => { image = value }
  }) }
  const decoder = new PreviewDecoder(() => canvas, { onFrame: () => { frames++ }, onError: error => { throw error } })
  decoder.feed(frame, false, 0)
  assert.equal(frames, 0)
  decoder.feed(frame, true, 1000)
  assert.equal(frames, 1)
  assert.equal(canvas.width, 32)
  assert.equal(canvas.height, 32)
  assert.deepEqual([...image.data.slice(0, 4)], [126, 126, 126, 255])
  decoder.close()
  decoder.feed(frame, true, 2000)
  assert.equal(frames, 1)
})

test('WASM preview rejects input larger than its allocated bitstream buffer', () => {
  const errors = []
  const decoder = new PreviewDecoder(() => null, { onError: error => errors.push(error.message) })
  decoder.feed(new Uint8Array(1024 * 1024 + 1), true, 0)
  assert.match(errors[0], /预览帧过大/)
  decoder.close()
})
