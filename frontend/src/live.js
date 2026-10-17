// One owner, one EventSource and one retry timer per stream.
export function liveStream(path, handlers, status = () => {}) {
  let source,
    timer,
    watchdog,
    probe,
    closed = false,
    attempt = 0
  const clear = () => {
    clearTimeout(timer)
    clearTimeout(watchdog)
    probe?.abort()
    probe = undefined
    source?.close()
    source = undefined
  }
  const arm = () => {
    clearTimeout(watchdog)
    watchdog = setTimeout(retry, 45000)
  }
  function retry() {
    clear()
    if (closed || document.hidden) return
    status('reconnecting')
    probe = new AbortController()
    fetch('/api/auth/session', { credentials: 'same-origin', signal: probe.signal })
      .then((response) => {
        if (response.status === 401 && !closed) {
          dispose()
          window.dispatchEvent(new Event('session-expired'))
        }
      })
      .catch(() => {})
    timer = setTimeout(
      connect,
      Math.min(30000, 1000 * 2 ** Math.min(attempt++, 5)) + Math.random() * 400
    )
  }
  function connect() {
    clear()
    if (closed || document.hidden) return
    status('connecting')
    source = new EventSource(`/api${path}`)
    source.onopen = () => {
      attempt = 0
      status('connected')
      arm()
    }
    source.onerror = retry
    source.addEventListener('heartbeat', arm)
    source.addEventListener('session-expired', () => {
      dispose()
      window.dispatchEvent(new Event('session-expired'))
    })
    source.addEventListener('unavailable', (event) => {
      handlers.unavailable?.(JSON.parse(event.data))
      dispose()
      status('unavailable')
    })
    for (const [name, handler] of Object.entries(handlers)) {
      if (name !== 'unavailable')
        source.addEventListener(name, (event) => {
          arm()
          try {
            handler(JSON.parse(event.data))
          } catch {
            retry()
          }
        })
    }
    arm()
  }
  function visibility() {
    if (document.hidden) {
      clear()
      status('paused')
    } else connect()
  }
  function dispose() {
    closed = true
    clear()
    document.removeEventListener('visibilitychange', visibility)
  }
  document.addEventListener('visibilitychange', visibility)
  connect()
  return dispose
}
