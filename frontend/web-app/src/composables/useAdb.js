import { ref } from 'vue'
import { Terminal } from 'xterm'
import { FitAddon } from 'xterm-addon-fit'
import 'xterm/css/xterm.css'

export function useAdb(webrtc) {
  const isAdbConnected = ref(false)
  let term = null
  let fitAddon = null
  let session = null
  let resizeObserver = null
  let generation = 0

  async function initAdb(container) {
    if (term) return
    const attempt = ++generation
    term = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: 'Consolas, "Liberation Mono", Menlo, Courier, monospace',
      theme: { background: '#1e1e1e', foreground: '#d4d4d4', cursor: '#aeafad' },
      scrollback: 10000
    })
    fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(container)
    resize()
    term.onData(data => session?.sendData(new TextEncoder().encode(data)))
    term.onResize(({ rows, cols }) => session?.resize(rows, cols))
    resizeObserver = new ResizeObserver(resize)
    resizeObserver.observe(container)
    term.writeln('\x1b[33m[Shell] 正在连接设备终端...\x1b[0m')
    try {
      const opened = await webrtc.createAdbSessionChannel({
        rows: term.rows || 24,
        cols: term.cols || 80,
        onData: data => { if (attempt === generation) term?.write(data) },
        onClose: error => {
          if (attempt !== generation) return
          isAdbConnected.value = false
          term?.writeln(`\r\n[Shell] ${error?.message || '终端已关闭'}`)
        }
      })
      if (attempt !== generation) { opened.close(); return }
      session = opened
      isAdbConnected.value = true
      term.writeln('\r\n\x1b[32m[Shell] 终端已就绪\x1b[0m')
      resize()
      session.resize(term.rows, term.cols)
      term.focus()
    } catch (error) {
      if (attempt !== generation) return
      console.error('[Shell] Connection failed:', error)
      term?.writeln(`\r\n\x1b[31m[Shell] 连接失败: ${error.message}\x1b[0m`)
      isAdbConnected.value = false
    }
  }

  function closeAdb() {
    generation++
    isAdbConnected.value = false
    session?.close()
    session = null
    resizeObserver?.disconnect()
    resizeObserver = null
    term?.dispose()
    term = null
    fitAddon = null
  }

  function resize() {
    if (fitAddon) try { fitAddon.fit() } catch {}
  }

  return { isAdbConnected, initAdb, closeAdb, resize }
}
