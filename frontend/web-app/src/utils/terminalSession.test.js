import { test } from 'node:test'
import assert from 'node:assert/strict'
import { openTerminalSession, terminalSessionId } from './terminalSession.js'

function socketFixture() {
  const sessions = new Map()
  const sent = []
  const socket = { readyState: 1, send(raw) {
    const message = JSON.parse(raw)
    sent.push(message)
    if (message.message_type === 'pty_open') queueMicrotask(() => sessions.get(message.session_id)?.handleMessage({ message_type: 'pty_opened' }))
  } }
  return { socket, sessions, sent, deviceId: 'test-device', rows: 24, cols: 80, timeout: 20 }
}

test('HTTP-compatible session IDs use cryptographic randomness', () => {
  const ids = new Set(Array.from({ length: 100 }, terminalSessionId))
  assert.equal(ids.size, 100)
  for (const id of ids) assert.match(id, /^[a-f0-9]{32}$/)
})

test('terminal stays available while WebRTC is connecting, with padded input and resize', async () => {
  const fixture = socketFixture()
  let received
  const session = await openTerminalSession({ ...fixture, peer: { connectionState: 'connecting' }, onData: data => { received = data } })
  session.sendData(new Uint8Array([13]))
  session.resize(33, 99)
  assert.equal(fixture.sent[1].data, 'DQ==')
  assert.deepEqual([fixture.sent[2].rows, fixture.sent[2].cols], [33, 99])
  session.handleMessage({ message_type: 'pty_data', data: 'aGk' })
  assert.equal(new TextDecoder().decode(received), 'hi')
  session.close()
  assert.equal(fixture.sessions.size, 0)
  assert.equal(fixture.sent.at(-1).message_type, 'pty_close')
})

test('a failed data channel falls back before any input is sent', async () => {
  const fixture = socketFixture()
  let closed = false
  const peer = { connectionState: 'connected', createDataChannel() { return { close() { closed = true } } } }
  const session = await openTerminalSession({ ...fixture, peer })
  assert.equal(closed, true)
  assert.equal(fixture.sent[0].message_type, 'pty_open')
  session.close()
})

test('WebRTC terminal waits for PTY acknowledgement and sends resize as control text', async () => {
  const fixture = socketFixture()
  const sent = []
  let channel
  const peer = { connectionState: 'connected', createDataChannel() {
    channel = { close() { channel.onclose?.() }, send(data) {
      sent.push(data)
      if (typeof data === 'string' && JSON.parse(data).type === 'init') queueMicrotask(() => channel.onmessage({ data: JSON.stringify({ type: 'pty_opened' }) }))
    } }
    queueMicrotask(() => channel.onopen())
    return channel
  } }
  const session = await openTerminalSession({ ...fixture, peer })
  session.sendData(new Uint8Array([13]))
  session.resize(40, 120)
  assert.equal(JSON.parse(sent[0]).type, 'init')
  assert.deepEqual(sent[1], new Uint8Array([13]))
  assert.deepEqual(JSON.parse(sent[2]), { type: 'resize', rows: 40, cols: 120 })
  assert.equal(fixture.sent.length, 0)
  session.close()
})

test('disconnect closes an active terminal instead of replaying its input', async () => {
  const fixture = socketFixture()
  let error
  const session = await openTerminalSession({ ...fixture, onClose: reason => { error = reason } })
  session.sendData(new TextEncoder().encode('echo test\r'))
  fixture.socket.readyState = 3
  session.disconnected()
  session.sendData(new TextEncoder().encode('echo again\r'))
  assert.match(error.message, /断开/)
  assert.equal(fixture.sent.filter(message => message.message_type === 'pty_input').length, 1)
  assert.equal(fixture.sessions.size, 0)
})
