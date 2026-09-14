import { test } from 'node:test'
import assert from 'node:assert/strict'
import { isWebRTCAvailable } from './webrtc.js'

const names = ['RTCPeerConnection', 'RTCSessionDescription', 'RTCIceCandidate']
const supported = () => Object.fromEntries(names.map(name => [name, class {}]))

test('WebRTC requires all three browser constructors', () => {
  assert.equal(isWebRTCAvailable(supported()), true)
  assert.equal(isWebRTCAvailable({}), false)
  for (const name of names) {
    for (const value of [undefined, null, {}, false]) {
      assert.equal(isWebRTCAvailable({ ...supported(), [name]: value }), false, name)
    }
  }
})

test('WebRTC detection handles browser extensions removing an API after startup', () => {
  const browser = supported()
  assert.equal(isWebRTCAvailable(browser), true)
  delete browser.RTCPeerConnection
  assert.equal(isWebRTCAvailable(browser), false)
  browser.RTCPeerConnection = class {}
  assert.equal(isWebRTCAvailable(browser), true)
})
