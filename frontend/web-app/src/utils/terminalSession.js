// getRandomValues 在局域网 HTTP 页面也可用；randomUUID 仅限安全上下文。
export function terminalSessionId() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
}

export async function openTerminalSession({ peer, socket, deviceId, sessions, rows, cols, onData, onClose, timeout = 5000 }) {
  const options = { rows, cols, onData, onClose, timeout }
  if (peer?.connectionState === 'connected') {
    try {
      return await openRtcTerminal(peer, options)
    } catch (error) {
      // 仅在 PTY 建立前回退；已执行的终端输入不跨连接重放。
      if (!socket || socket.readyState !== 1) throw error
    }
  }
  if (!socket || socket.readyState !== 1) throw new Error('设备信令尚未连接')
  return openSocketTerminal(socket, deviceId, sessions, options)
}

function openRtcTerminal(peer, { rows, cols, onData, onClose, timeout }) {
  return new Promise((resolve, reject) => {
    const channel = peer.createDataChannel(`adb-channel-${terminalSessionId()}`, { ordered: true })
    channel.binaryType = 'arraybuffer'
    let opened = false
    let closed = false
    const timer = setTimeout(() => finish(new Error('WebRTC 终端连接超时')), timeout)
    const finish = error => {
      if (closed) return
      closed = true
      clearTimeout(timer)
      channel.close()
      if (!opened) reject(error || new Error('WebRTC 终端已关闭'))
      else onClose?.(error)
    }
    channel.onopen = () => channel.send(JSON.stringify({ type: 'init', rows, cols }))
    channel.onmessage = event => {
      if (closed) return
      if (typeof event.data === 'string') {
        let message
        try { message = JSON.parse(event.data) } catch { return }
        if (message.type === 'pty_error') return finish(new Error(message.error || '创建终端失败'))
        if (message.type !== 'pty_opened' || opened) return
        opened = true
        clearTimeout(timer)
        resolve({
          sendData: data => { if (!closed) channel.send(data) },
          resize: (rows, cols) => { if (!closed) channel.send(JSON.stringify({ type: 'resize', rows, cols })) },
          close: () => finish()
        })
      } else {
        onData?.(new Uint8Array(event.data))
      }
    }
    channel.onerror = () => finish(new Error('WebRTC 终端通道错误'))
    channel.onclose = () => finish()
  })
}

function openSocketTerminal(socket, deviceId, sessions, { rows, cols, onData, onClose, timeout }) {
  return new Promise((resolve, reject) => {
    const sessionId = terminalSessionId()
    let opened = false
    let closed = false
    const send = (message_type, fields = {}) => {
      if (socket.readyState === 1) socket.send(JSON.stringify({ message_type, device_id: deviceId, session_id: sessionId, ...fields }))
    }
    const timer = setTimeout(() => finish(new Error('设备终端连接超时')), timeout)
    const finish = error => {
      if (closed) return
      closed = true
      clearTimeout(timer)
      sessions.delete(sessionId)
      send('pty_close')
      if (!opened) reject(error || new Error('终端连接已关闭'))
      else onClose?.(error)
    }
    const session = {
      sendData: data => {
        if (closed) return
        const bytes = data instanceof Uint8Array ? data : new Uint8Array(data)
        let binary = ''
        for (const byte of bytes) binary += String.fromCharCode(byte)
        send('pty_input', { data: btoa(binary) })
      },
      resize: (rows, cols) => { if (!closed) send('pty_resize', { rows, cols }) },
      close: () => finish(),
      disconnected: () => finish(new Error('设备连接已断开')),
      handleMessage: message => {
        if (closed) return
        if (message.message_type === 'pty_data') {
          onData?.(Uint8Array.from(atob(message.data), char => char.charCodeAt(0)))
        } else if (message.message_type === 'pty_opened' && !opened) {
          opened = true
          clearTimeout(timer)
          resolve(session)
        } else if (message.message_type === 'pty_error') {
          finish(new Error(message.error || '创建终端失败'))
        } else if (message.message_type === 'pty_closed') {
          finish()
        }
      }
    }
    sessions.set(sessionId, session)
    send('pty_open', { rows, cols })
  })
}
