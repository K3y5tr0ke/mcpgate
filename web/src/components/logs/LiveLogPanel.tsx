import { useEffect, useRef, useState, useCallback } from 'react'
import type { AuditLogEntry } from '../../api/types'
import { Badge } from '../ui/Badge'
import { statusBadge } from '../ui/badges'
import { Button } from '../ui/Button'
import { format } from 'date-fns'

const MAX_ENTRIES = 200
const WS_URL = `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/api/v1/admin/logs/stream`

interface Props {
  serverFilter?: string
}

export function LiveLogPanel({ serverFilter }: Props) {
  const [running, setRunning] = useState(false)
  const [entries, setEntries] = useState<AuditLogEntry[]>([])
  const [error, setError] = useState<string | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const bottomRef = useRef<HTMLDivElement | null>(null)

  const stop = useCallback(() => {
    wsRef.current?.close()
    wsRef.current = null
    setRunning(false)
  }, [])

  const start = useCallback(() => {
    setError(null)
    const ws = new WebSocket(WS_URL)
    wsRef.current = ws

    ws.onopen = () => {
      setRunning(true)
      if (serverFilter) {
        ws.send(JSON.stringify({ server_name: serverFilter }))
      }
    }

    ws.onmessage = (ev) => {
      try {
        const entry = JSON.parse(ev.data) as AuditLogEntry
        setEntries((prev) => {
          const next = [...prev, entry]
          return next.length > MAX_ENTRIES ? next.slice(next.length - MAX_ENTRIES) : next
        })
      } catch {
        // ignore malformed frames
      }
    }

    ws.onerror = () => setError('WebSocket error — check that you are authenticated and the server is reachable.')

    ws.onclose = () => {
      setRunning(false)
      wsRef.current = null
    }
  }, [serverFilter])

  // Auto-scroll to bottom when new entries arrive.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [entries])

  // Clean up on unmount.
  useEffect(() => () => { wsRef.current?.close() }, [])

  return (
    <div className="rounded-lg border border-gray-200 bg-white">
      <div className="flex items-center justify-between border-b border-gray-100 px-4 py-3">
        <div className="flex items-center gap-3">
          <span className="text-sm font-medium text-gray-900">Live stream</span>
          {running && (
            <span className="flex items-center gap-1.5 text-xs text-green-600">
              <span className="inline-block h-2 w-2 animate-pulse rounded-full bg-green-500" />
              live
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="secondary" onClick={() => setEntries([])}>
            Clear
          </Button>
          {running ? (
            <Button size="sm" variant="danger" onClick={stop}>
              Stop
            </Button>
          ) : (
            <Button size="sm" onClick={start}>
              Start
            </Button>
          )}
        </div>
      </div>

      {error && (
        <p className="px-4 py-2 text-xs text-red-500">{error}</p>
      )}

      <div className="h-64 overflow-y-auto bg-gray-950 px-3 py-2 font-mono text-xs text-gray-200">
        {entries.length === 0 ? (
          <p className="text-gray-500 py-1">{running ? 'Waiting for events…' : 'Press Start to begin streaming.'}</p>
        ) : (
          entries.map((e, i) => (
            <div key={i} className="flex items-start gap-2 py-0.5 hover:bg-gray-900 rounded px-1">
              <span className="shrink-0 text-gray-500 tabular-nums">
                {format(new Date(e.timestamp), 'HH:mm:ss.SSS')}
              </span>
              <Badge color="blue">{e.server_name}</Badge>
              <span className="text-gray-300">{e.mcp_method}</span>
              {e.tool_name && <span className="text-gray-400">({e.tool_name})</span>}
              {statusBadge(e.status)}
              <span className="tabular-nums text-gray-500">{e.latency_ms}ms</span>
            </div>
          ))
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}
