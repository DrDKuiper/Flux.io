import { createContext, useContext, ReactNode, createElement } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ApiClient, buildQuery } from './client'
import {
  Overview, Talker, AppCount, ThroughputPoint, GeoCount,
  FlowRow, AlertRow, Source, Paginated, RangeToken,
} from './types'

const ClientContext = createContext<ApiClient | null>(null)

export function ApiProvider({ client, children }: { client: ApiClient; children: ReactNode }) {
  return createElement(ClientContext.Provider, { value: client }, children)
}

function useClient(): ApiClient {
  const c = useContext(ClientContext)
  if (!c) throw new Error('ApiProvider missing')
  return c
}

const LIVE_REFETCH = 10_000 // ms; dashboard polling cadence

export function useOverview(range: RangeToken, source: string) {
  const client = useClient()
  return useQuery({
    queryKey: ['overview', range, source],
    queryFn: () => client.get<Overview>('/api/metrics/overview' + buildQuery({ range, source })),
    refetchInterval: LIVE_REFETCH,
  })
}

export function useTopTalkers(range: RangeToken, source: string, limit = 10) {
  const client = useClient()
  return useQuery({
    queryKey: ['top-talkers', range, source, limit],
    queryFn: () => client.get<Talker[]>('/api/metrics/top-talkers' + buildQuery({ range, source, limit })),
    refetchInterval: LIVE_REFETCH,
  })
}

export function useTopApps(range: RangeToken, source: string, limit = 10) {
  const client = useClient()
  return useQuery({
    queryKey: ['top-apps', range, source, limit],
    queryFn: () => client.get<AppCount[]>('/api/metrics/top-apps' + buildQuery({ range, source, limit })),
    refetchInterval: LIVE_REFETCH,
  })
}

export function useThroughput(range: RangeToken, source: string, buckets = 60) {
  const client = useClient()
  return useQuery({
    queryKey: ['throughput', range, source, buckets],
    queryFn: () => client.get<ThroughputPoint[]>('/api/metrics/throughput' + buildQuery({ range, source, buckets })),
    refetchInterval: LIVE_REFETCH,
  })
}

export function useGeo(range: RangeToken, source: string) {
  const client = useClient()
  return useQuery({
    queryKey: ['geo', range, source],
    queryFn: () => client.get<GeoCount[]>('/api/geo/flows' + buildQuery({ range, source })),
    refetchInterval: LIVE_REFETCH,
  })
}

export interface FlowQuery {
  range: RangeToken
  source?: string
  src_ip?: string
  dst_ip?: string
  port?: number
  app?: string
  country?: string
  limit?: number
  offset?: number
}

export function useFlows(q: FlowQuery) {
  const client = useClient()
  return useQuery({
    queryKey: ['flows', q],
    queryFn: () => client.get<Paginated<FlowRow>>('/api/flows' + buildQuery({ ...q })),
  })
}

export function useAlerts(range: RangeToken, source: string, limit = 50, offset = 0) {
  const client = useClient()
  return useQuery({
    queryKey: ['alerts', range, source, limit, offset],
    queryFn: () => client.get<Paginated<AlertRow>>('/api/alerts' + buildQuery({ range, source, limit, offset })),
  })
}

export function useSources() {
  const client = useClient()
  return useQuery({
    queryKey: ['sources'],
    queryFn: () => client.get<Source[]>('/api/sources'),
    refetchInterval: LIVE_REFETCH,
  })
}

export interface SourcePatch {
  Name?: string
  GroupTag?: string
  Enabled?: boolean
  DPIMode?: string
  ExpectedType?: string
}

export function usePatchSource() {
  const client = useClient()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: SourcePatch }) =>
      client.patch<Source>(`/api/sources/${id}`, patch),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['sources'] }),
  })
}
