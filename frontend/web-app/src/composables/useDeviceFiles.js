import { ref } from 'vue'
import { issueWebSocketTicket } from '@/utils/websocketTicket'

// Preserve the file-view protocol while all device access goes through the
// authenticated control plane, independently of video or peer connectivity.
export function useDeviceFiles(deviceId) {
  const status = ref('disconnected')
  const error = ref(null)
  const fileChannelReady = ref(false)
  let socket = null
  let callback = null
  let connectTimer = null

  function disconnect() {
    clearTimeout(connectTimer)
    const current = socket
    socket = null
    current?.close()
    status.value = 'disconnected'
    fileChannelReady.value = false
  }

  function send(value) {
    if (!socket || socket.readyState !== WebSocket.OPEN) return false
    socket.send(JSON.stringify({ ...value, device_id: deviceId }))
    return true
  }

  async function connect() {
	  disconnect()
	  error.value = null
	  status.value = 'connecting'
	  let ticket
	  try {
		  ticket = await issueWebSocketTicket()
	  } catch (err) {
		  error.value = err.message
		  status.value = 'error'
		  return
	  }
	  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
	  const current = new WebSocket(`${protocol}//${location.host}/connect_client?ticket=${encodeURIComponent(ticket)}`)
    socket = current
    connectTimer = setTimeout(() => {
      if (!fileChannelReady.value) { error.value = '文件通道连接超时'; current.close() }
    }, 15000)
    current.onopen = () => send({ message_type: 'connect' })
    current.onmessage = event => {
      if (socket !== current || typeof event.data !== 'string') return
      let message
      try { message = JSON.parse(event.data) } catch { return }
      if (message.message_type === 'config') {
        send({ message_type: 'file_open' })
      } else if (message.message_type === 'file_message') {
        const payload = message.payload
        if (payload.type === 'ready') {
          clearTimeout(connectTimer)
          fileChannelReady.value = true
          status.value = 'connected'
        } else if (payload.type === 'download_chunk') {
          callback?.(Uint8Array.from(atob(payload.data), char => char.charCodeAt(0)).buffer)
        } else {
          callback?.(JSON.stringify(payload))
        }
      } else if (['error', 'file_error'].includes(message.message_type)) {
        error.value = message.error || '文件操作失败'
        current.close()
      }
    }
    current.onerror = () => { error.value = '文件通道连接失败' }
    current.onclose = () => {
      if (socket !== current) return
      clearTimeout(connectTimer)
      fileChannelReady.value = false
      status.value = error.value ? 'error' : 'disconnected'
    }
  }

  return {
    status, error, fileChannelReady, connect, disconnect,
    onFileChannelMessage(handler) { callback = handler },
    sendFileChannelCmd(payload) { return send({ message_type: 'file_command', payload }) },
    sendFileChannelChunk(data) {
      const bytes = new Uint8Array(data)
      if (bytes.length > 65536) throw new Error('文件分块超过 64 KiB')
      let binary = ''
      for (const byte of bytes) binary += String.fromCharCode(byte)
      return send({ message_type: 'file_chunk', data: btoa(binary) })
    },
    getFileChannelBufferedAmount() { return socket?.bufferedAmount || 0 }
  }
}
