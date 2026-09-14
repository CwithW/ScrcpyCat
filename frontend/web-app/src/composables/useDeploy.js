import { ref } from 'vue'
import { Adb, AdbDaemonTransport } from '@yume-chan/adb'
import { AdbDaemonWebUsbDeviceManager } from '@yume-chan/adb-daemon-webusb'

const quoteShell = value => "'" + String(value).replaceAll("'", "'\\''") + "'"

export function useDeploy() {
  const isDeploying = ref(false)
  const deployStatus = ref('')
  const deployProgress = ref(0)
  const deployError = ref(null)
  const deployLog = ref([])
  const log = message => deployLog.value.push('[' + new Date().toLocaleTimeString() + '] ' + message)

  async function deployAgent(options) {
    isDeploying.value = true
    deployError.value = null
    deployProgress.value = 0
    deployLog.value = []
    let transport
    try {
      if (!navigator.usb || !window.isSecureContext) {
        throw new Error('浏览器要求 HTTPS 或 localhost 才能使用 WebUSB；当前内网 HTTP 页面可下载 ADB 部署包。')
      }
      const endpoint = new URL(options.signalingUrl)
      endpoint.protocol = endpoint.protocol === 'http:' ? 'ws:' : endpoint.protocol === 'https:' ? 'wss:' : endpoint.protocol
      if (!['ws:', 'wss:'].includes(endpoint.protocol)) throw new Error('信令地址必须使用 ws:// 或 wss://')
      endpoint.pathname = '/register_agent'
      endpoint.search = ''
      const CredentialStore = (await import('@yume-chan/adb-credential-web')).default
      deployStatus.value = '请选择 USB 设备'
      const device = await AdbDaemonWebUsbDeviceManager.BROWSER.requestDevice()
      if (!device) throw new Error('未选择设备')
      const connection = await device.connect()
      deployStatus.value = '正在认证，请在手机确认 USB 调试授权'
      transport = await AdbDaemonTransport.authenticate({ serial: device.serial || 'webadb', connection, credentialStore: new CredentialStore('scrcpycat-web') })
      const adb = new Adb(transport)
      deployProgress.value = 20
      const abi = (await adb.subprocess.noneProtocol.spawnWaitText('getprop ro.product.cpu.abi')).trim()
      if (!['arm64-v8a', 'armeabi-v7a', 'x86_64', 'x86'].includes(abi)) throw new Error('不支持的 ABI: ' + abi)
      const deviceId = options.deviceId || (device.serial || ('android-' + Date.now().toString(36)))
      if (!/^[A-Za-z0-9_.-]{1,32}$/.test(deviceId)) throw new Error('设备 ID 仅支持 1–32 位字母、数字、点、下划线或横线')
      const headers = { Authorization: 'Bearer ' + (localStorage.getItem('auth_token') || '') }
      const enrollmentResponse = await fetch('/api/agents/enrollment-tokens', {
        method: 'POST', headers: { ...headers, 'Content-Type': 'application/json' },
        body: JSON.stringify({ device_id: deviceId, ttl_seconds: 900 })
      })
      if (!enrollmentResponse.ok) throw new Error('申请设备注册令牌失败')
      const enrollment = await enrollmentResponse.json()
      const [agentResponse, jarResponse] = await Promise.all([
        fetch('/agent/' + abi + '/scrcpycat-agent', { headers }),
        fetch('/agent/scrcpy-server.jar', { headers })
      ])
      if (!agentResponse.ok || !jarResponse.ok) throw new Error('服务端 Agent 工件尚未构建或挂载')
      deployProgress.value = 40
      deployStatus.value = '推送 ScrcpyCat Agent 与 scrcpy Server'
      log('设备 ' + deviceId + '，ABI ' + abi)
      await adb.subprocess.noneProtocol.spawnWaitText('if [ -f /data/local/tmp/scrcpycat-agent.pid ]; then p=$(cat /data/local/tmp/scrcpycat-agent.pid); case "$p" in ""|*[!0-9]*) exit 1;; esac; if [ "$(cat /proc/$p/comm 2>/dev/null)" = scrcpycat-agent ]; then kill "$p"; sleep 1; fi; fi')
      const sync = await adb.sync()
      try {
        await sync.write({ filename: '/data/local/tmp/scrcpycat-agent', file: agentResponse.body, permission: 0o755 })
        deployProgress.value = 60
        await sync.write({ filename: '/data/local/tmp/scrcpy-server.jar', file: jarResponse.body, permission: 0o644 })
      } finally { await sync.dispose() }
      await adb.subprocess.noneProtocol.spawnWaitText('chmod 755 /data/local/tmp/scrcpycat-agent')
      deployProgress.value = 80
      deployStatus.value = '启动并检查 Agent'
      const args = ['--device-id', deviceId, '--signaling', endpoint.toString(), '--enrollment-token', enrollment.token].map(quoteShell).join(' ')
      await adb.subprocess.noneProtocol.spawnWaitText('nohup /data/local/tmp/scrcpycat-agent ' + args + ' >/data/local/tmp/scrcpycat-agent.log 2>&1 </dev/null &')
      await new Promise(resolve => setTimeout(resolve, 2000))
      const health = await adb.subprocess.noneProtocol.spawnWaitText('/data/local/tmp/scrcpycat-agent --health && echo SCRCPYCAT_READY')
      if (!health.includes('SCRCPYCAT_READY')) throw new Error('Agent 健康检查失败，请查看 /data/local/tmp/scrcpycat-agent.log')
      deployProgress.value = 100
      deployStatus.value = '部署成功：' + deviceId
      log('Agent 已就绪')
      return true
    } catch (error) {
      deployError.value = error.message || String(error)
      deployStatus.value = '部署失败'
      log(deployError.value)
      return false
    } finally {
      isDeploying.value = false
      if (transport) await transport.close().catch(() => {})
    }
  }
  return { isDeploying, deployStatus, deployProgress, deployError, deployLog, deployAgent }
}
