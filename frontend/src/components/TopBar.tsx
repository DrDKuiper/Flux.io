import { RangePicker } from './RangePicker'
import { useRange } from '../range/RangeProvider'

export function TopBar({ title }: { title: string }) {
  const { source, setSource } = useRange()
  return (
    <div className="flex items-center justify-between px-6 py-4 border-b border-gray-800">
      <div className="flex items-center gap-3">
        <h1 className="text-lg font-semibold text-white">{title}</h1>
        {source && (
          <button
            onClick={() => setSource('')}
            className="text-xs px-2 py-0.5 rounded bg-blue-900/50 text-blue-200 hover:bg-blue-900"
          >
            source: {source} ✕
          </button>
        )}
      </div>
      <RangePicker />
    </div>
  )
}
