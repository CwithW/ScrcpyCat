export async function issueWebSocketTicket(shareToken = '', sharePassword = '') {
  const options = { method: 'POST' }
  if (shareToken) {
    options.headers = { 'Content-Type': 'application/json' }
    options.body = JSON.stringify({ share_token: shareToken, share_password: sharePassword })
  }

  const response = await fetch('/api/ws-ticket', options)
  if (!response.ok) {
    throw new Error('无法获取信令连接票据')
  }
  const body = await response.json()
  if (!body || typeof body.ticket !== 'string' || !body.ticket) {
    throw new Error('服务端返回的信令连接票据无效')
  }
  return body.ticket
}
