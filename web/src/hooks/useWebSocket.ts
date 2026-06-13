import { useCallback, useEffect, useRef, useState } from 'react'

interface UseWebSocketOptions {
  url: string
  enabled?: boolean
  onMessage?: (data: unknown) => void
}

export function useWebSocket({ url, enabled = true, onMessage }: UseWebSocketOptions) {
  const [connected, setConnected] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Holds the latest connect() so the reconnect timer can call it without the
  // callback referencing itself before it is declared.
  const connectRef = useRef<() => void>(() => {})

  const connect = useCallback(() => {
    if (!enabled) return
    // Token is sent via httpOnly cookie (mcpgate_session), never as a URL param.
    // URL params appear in access logs and proxy logs, leaking bearer tokens.
    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onopen = () => setConnected(true)

    ws.onmessage = (ev) => {
      try {
        const data = JSON.parse(ev.data)
        onMessage?.(data)
      } catch {
        // ignore non-JSON frames
      }
    }

    ws.onclose = () => {
      setConnected(false)
      if (enabled) {
        reconnectTimer.current = setTimeout(() => connectRef.current(), 3000)
      }
    }

    ws.onerror = () => ws.close()
  }, [url, enabled, onMessage])

  useEffect(() => {
    connectRef.current = connect
    connect()
    return () => {
      wsRef.current?.close()
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current)
    }
  }, [connect])

  const send = useCallback((data: unknown) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(data))
    }
  }, [])

  return { connected, send }
}
