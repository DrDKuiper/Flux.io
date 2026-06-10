import { QueryClient } from '@tanstack/react-query'
import { AlertRow } from '../api/types'

const MAX_LIVE_ALERTS = 100

export interface WsMessage {
  type: string
  data: unknown
}

// applyWsMessage routes a decoded WebSocket message into the query cache.
export function applyWsMessage(qc: QueryClient, msg: WsMessage): void {
  switch (msg.type) {
    case 'metrics':
      qc.setQueryData(['metrics', 'live'], msg.data)
      break
    case 'alert': {
      const prev = (qc.getQueryData(['alerts', 'live']) as AlertRow[]) ?? []
      qc.setQueryData(['alerts', 'live'], [msg.data as AlertRow, ...prev].slice(0, MAX_LIVE_ALERTS))
      break
    }
    default:
      break
  }
}
