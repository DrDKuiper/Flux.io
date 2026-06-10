export interface Overview {
  flows: number
  bytes: number
  packets: number
  active_alerts: number
}

export interface Talker {
  ip: string
  hostname: string
  bytes: number
  packets: number
  flows: number
}

export interface AppCount {
  application_id: string
  bytes: number
  flows: number
}

export interface ThroughputPoint {
  ts: string
  bytes: number
  packets: number
}

export interface GeoCount {
  country: string
  bytes: number
  flows: number
}

export interface FlowRow {
  ts: string
  source: string
  src_ip: string
  dst_ip: string
  src_port: number
  dst_port: number
  protocol: number
  bytes: number
  packets: number
  application_id: string
  sni: string
  http_host: string
  src_country: string
  dst_country: string
  src_asn_org: string
  dst_asn_org: string
}

export interface AlertRow {
  ts: string
  source: string
  src_ip: string
  dst_ip: string
  signature: string
  category: string
  severity: number
}

export interface Source {
  id: number
  address: string
  type: string
  name: string
  group_tag: string
  enabled: boolean
  dpi_mode: string
  expected_type: string
  first_seen: string
  last_seen: string
  status: string
  mismatch: boolean
  flows_per_sec: number
  total_bytes: number
}

export interface Paginated<T> {
  total: number
  items: T[]
}

export type RangeToken = '15m' | '1h' | '6h' | '24h' | '7d'
