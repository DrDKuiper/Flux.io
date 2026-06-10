import { LineChart, Line, XAxis, YAxis, ResponsiveContainer, Tooltip } from 'recharts'
import { ThroughputPoint } from '../../api/types'
import { formatBytes } from '../../lib/format'

export function ThroughputChart({ data }: { data: ThroughputPoint[] }) {
  const points = data.map((p) => ({ t: new Date(p.ts).getTime(), bytes: p.bytes }))
  return (
    <ResponsiveContainer width="100%" height={140}>
      <LineChart data={points}>
        <XAxis dataKey="t" tickFormatter={(t) => new Date(t).toLocaleTimeString()} stroke="#52525b" fontSize={11} />
        <YAxis tickFormatter={(v) => formatBytes(v)} stroke="#52525b" fontSize={11} width={60} />
        <Tooltip formatter={(v: number) => formatBytes(v)} labelFormatter={(t) => new Date(t).toLocaleString()} />
        <Line type="monotone" dataKey="bytes" stroke="#378ADD" strokeWidth={2} dot={false} />
      </LineChart>
    </ResponsiveContainer>
  )
}
