import { ReactNode, useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useAuth } from '../auth/AuthProvider'
import { applyWsMessage } from './cache'

// WebSocketProvider opens a single /ws connection while authenticated, writing
// live messages into the query cache. It auto-reconnects with capped backoff.
export function WebSocketProvider({ children }: { children: ReactNode }) {
  const { token } = useAuth()
  const qc = useQueryClient()
  const wsRef = useRef<WebSocket | null>(null)

  useEffect(() => {
    if (!token) return
    let closed = false
    let backoff = 1000

    const connect = () => {
      if (closed) return
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      const ws = new WebSocket(`${proto}://${location.host}/ws?token=${encodeURIComponent(token)}`)
      wsRef.current = ws

      ws.onmessage = (ev) => {
        try {
          applyWsMessage(qc, JSON.parse(ev.data))
        } catch {
          // ignore malformed frames
        }
      }
      ws.onopen = () => {
        backoff = 1000
      }
      ws.onclose = () => {
        if (closed) return
        setTimeout(connect, backoff)
        backoff = Math.min(backoff * 2, 30_000)
      }
    }
    connect()

    return () => {
      closed = true
      wsRef.current?.close()
    }
  }, [token, qc])

  return <>{children}</>
}
