import { Talker } from '../../api/types'
import { formatBytes } from '../../lib/format'

export function TopTalkersBars({ data }: { data: Talker[] }) {
  const max = Math.max(1, ...data.map((d) => d.bytes))
  return (
    <div className="flex flex-col gap-2.5">
      {data.map((d) => (
        <div key={d.ip}>
          <div className="flex justify-between text-xs mb-1">
            <span className="text-white">{d.hostname || d.ip}</span>
            <span className="text-gray-400">{formatBytes(d.bytes)}</span>
          </div>
          <div className="h-1.5 bg-gray-800 rounded">
            <div className="h-1.5 bg-blue-500 rounded" style={{ width: `${(d.bytes / max) * 100}%` }} />
          </div>
        </div>
      ))}
    </div>
  )
}
