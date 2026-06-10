import { PieChart, Pie, Cell, ResponsiveContainer, Tooltip } from 'recharts'
import { AppCount } from '../../api/types'
import { formatBytes } from '../../lib/format'

const COLORS = ['#378ADD', '#1D9E75', '#EF9F27', '#D4537E', '#7F77DD', '#888780']

export function TopAppsDonut({ data }: { data: AppCount[] }) {
  return (
    <ResponsiveContainer width="100%" height={160}>
      <PieChart>
        <Pie data={data} dataKey="bytes" nameKey="application_id" innerRadius={45} outerRadius={70} paddingAngle={2}>
          {data.map((_, i) => (
            <Cell key={i} fill={COLORS[i % COLORS.length]} />
          ))}
        </Pie>
        <Tooltip formatter={(v: number, n) => [formatBytes(v), n]} />
      </PieChart>
    </ResponsiveContainer>
  )
}
