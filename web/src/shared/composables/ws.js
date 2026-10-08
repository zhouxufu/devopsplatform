// 可复用 WebSocket：自动重连（指数退避），暴露消息回调与连接状态
export function useWS(url, { onMessage, onOpen, onClose } = {}) {
  let ws = null, retry = 0, closed = false, retryTimer = null
  const state = { connected: false }
  function connect() {
    if (closed || (ws && (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN))) return
    clearTimeout(retryTimer); retryTimer = null
    let socket
    try { socket = new WebSocket(url); ws = socket } catch { schedule(); return }
    socket.onopen = () => { if (socket !== ws || closed) return; retry = 0; state.connected = true; onOpen && onOpen() }
    socket.onmessage = ev => {
      if (socket !== ws || closed) return
      if (onMessage && onMessage(ev.data) === false) {
        closed = true
        socket.close()
      }
    }
    socket.onerror = () => { if (socket === ws) socket.close() }
    socket.onclose = () => {
      if (socket !== ws) return
      ws = null
      state.connected = false; onClose && onClose()
      if (!closed) schedule()
    }
  }
  function schedule() {
    if (closed || retryTimer) return
    const delay = Math.min(1000 * (2 ** retry), 15000)
    retry = Math.min(retry + 1, 4)
    retryTimer = setTimeout(() => { retryTimer = null; connect() }, delay)
  }
  function reconnect() {
    if (closed || state.connected || (ws && ws.readyState === WebSocket.CONNECTING)) return
    retry = 0
    clearTimeout(retryTimer); retryTimer = null
    ws = null
    connect()
  }
  const onVisible = () => { if (!document.hidden) reconnect() }
  window.addEventListener('online', reconnect)
  document.addEventListener('visibilitychange', onVisible)
  connect()
  return {
    state,
    reconnect,
    close() {
      closed = true
      clearTimeout(retryTimer)
      window.removeEventListener('online', reconnect)
      document.removeEventListener('visibilitychange', onVisible)
      ws && ws.close()
    },
    send(m) { ws && ws.send(m) },
  }
}
