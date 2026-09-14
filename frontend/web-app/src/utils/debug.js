export function isDebugEnabled() {
  try {
    if (typeof window === 'undefined') return false
    const params = new URLSearchParams(window.location.search)
    return params.get('debug') === '1' || window.localStorage?.getItem('cloudphoneDebug') === '1'
  } catch (_) {
    return false
  }
}

export function debugLog(...args) {
  if (isDebugEnabled()) console.log(...args)
}

export function debugInfo(...args) {
  if (isDebugEnabled()) console.info(...args)
}

export function debugWarn(...args) {
  if (isDebugEnabled()) console.warn(...args)
}
