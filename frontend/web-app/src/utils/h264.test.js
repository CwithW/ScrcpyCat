import test from 'node:test'
import assert from 'node:assert/strict'
import { parseAnnexB, readH264Crop, drawYUV420 } from './h264.js'

test('Baseline SPS removes Android macroblock padding', () => {
  const sps = Uint8Array.from(Buffer.from('6742801fda05416f9f9a8003000368509a80', 'hex'))
  assert.deepEqual(readH264Crop(sps), { codedWidth: 336, codedHeight: 720, width: 324, height: 720, left: 0, top: 0 })
  assert.equal(readH264Crop(sps.subarray(0, 4)), null)
})

test('Annex B accepts mixed three and four byte start codes', () => {
  const units = parseAnnexB(Uint8Array.from([0,0,0,1,0x67,12,0,0,1,0x68,14,0,0,0,1,0x65,16]))
  assert.deepEqual(units.map(unit => [...unit]), [[0x67,12],[0x68,14],[0x65,16]])
})

test('YUV rendering keeps source stride when cropping', () => {
  let drawn
  const canvas = { width: 0, height: 0, getContext: () => ({
    createImageData: (width, height) => ({data: new Uint8ClampedArray(width * height * 4)}),
    putImageData: image => { drawn = image.data }
  }) }
  const yuv = Uint8Array.from([10,20,30,40,50,60,70,80,128,128,128,128])
  drawYUV420(canvas, yuv, 4, 2, {codedWidth:4,codedHeight:2,width:2,height:2,left:2,top:0})
  assert.equal(canvas.width, 2)
  assert.deepEqual([drawn[0],drawn[4],drawn[8],drawn[12]], [30,40,70,80])
})
