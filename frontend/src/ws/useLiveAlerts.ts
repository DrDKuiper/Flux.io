import { useQuery } from '@tanstack/react-query'
import { AlertRow } from '../api/types'

// useLiveAlerts reads the WebSocket-populated live alert list from the cache.
// queryFn returns the existing data (or []), so it never fetches — the
// WebSocketProvider is the only writer.
export function useLiveAlerts(): AlertRow[] {
  const { data } = useQuery<AlertRow[]>({
    queryKey: ['alerts', 'live'],
    queryFn: () => [],
    staleTime: Infinity,
  })
  return data ?? []
}
