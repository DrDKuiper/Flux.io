import { RangeToken } from '../api/types'
import { useRange } from '../range/RangeProvider'

const OPTIONS: RangeToken[] = ['15m', '1h', '6h', '24h', '7d']

export function RangePicker() {
  const { range, setRange } = useRange()
  return (
    <div className="flex gap-1 border border-gray-800 rounded-lg p-1">
      {OPTIONS.map((opt) => (
        <button
          key={opt}
          onClick={() => setRange(opt)}
          className={`text-xs px-2.5 py-1 rounded ${
            range === opt ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'
          }`}
        >
          {opt}
        </button>
      ))}
    </div>
  )
}
