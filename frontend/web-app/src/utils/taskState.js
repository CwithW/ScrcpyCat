export function isTaskFinished(status) {
  return ['success', 'failed', 'unknown', 'expired'].includes(status)
}
