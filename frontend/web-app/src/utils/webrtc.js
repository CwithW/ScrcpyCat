export function isWebRTCAvailable(scope = globalThis) {
  return (
    typeof scope.RTCPeerConnection === 'function' &&
    typeof scope.RTCSessionDescription === 'function' &&
    typeof scope.RTCIceCandidate === 'function'
  )
}

export const WEBRTC_UNAVAILABLE_MESSAGE = '浏览器未启用 WebRTC，请在浏览器设置或扩展中允许 WebRTC 后重试。'
