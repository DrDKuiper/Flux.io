import { TopBar } from '../components/TopBar'
import { QueryState } from '../components/QueryState'
import { useSources, usePatchSource } from '../api/hooks'
import { useRange } from '../range/RangeProvider'
import { Source } from '../api/types'
import { formatBytes } from '../lib/format'

const DPI_MODES = ['auto', 'suricata', 'tzsp', 'none']

function statusDot(status: string) {
  const color = status === 'active' ? 'bg-green-500' : status === 'silent' ? 'bg-amber-500' : 'bg-gray-600'
  return <span className={`w-2 h-2 rounded-full ${color}`} />
}

export function Sources() {
  const sources = useSources()
  const patch = usePatchSource()
  const { setSource } = useRange()

  // Group by group_tag (blank → "Ungrouped").
  const groups: Record<string, Source[]> = {}
  for (const s of sources.data ?? []) {
    const g = s.group_tag || 'Ungrouped'
    ;(groups[g] ??= []).push(s)
  }

  return (
    <div>
      <TopBar title="Sources" />
      <div className="p-6">
        <QueryState
          isLoading={sources.isLoading}
          isError={sources.isError}
          isEmpty={(sources.data?.length ?? 0) === 0}
          onRetry={sources.refetch}
        >
          {Object.entries(groups).map(([group, items]) => (
            <div key={group} className="mb-6">
              <div className="text-xs text-gray-500 mb-2">{group}</div>
              <div className="space-y-2">
                {items.map((s) => (
                  <div key={s.id} className="bg-gray-900 border border-gray-800 rounded-lg p-3 flex items-center gap-3">
                    {statusDot(s.status)}
                    <button onClick={() => setSource(s.address)} className="flex-1 min-w-0 text-left">
                      <div className="text-sm font-medium text-white">{s.name || s.address}</div>
                      <div className="text-xs text-gray-400">
                        {s.address} · {s.type}
                        {s.mismatch && <span className="text-amber-400"> · divergência</span>}
                      </div>
                    </button>
                    <span className="text-xs text-gray-400 w-20 text-right">{s.flows_per_sec} fl/s</span>
                    <span className="text-xs text-gray-500 w-20 text-right">{formatBytes(s.total_bytes)}</span>
                    <select
                      value={s.dpi_mode}
                      onChange={(e) => patch.mutate({ id: s.id, patch: { DPIMode: e.target.value } })}
                      className="bg-gray-950 border border-gray-800 rounded text-xs text-white px-1.5 py-1"
                    >
                      {DPI_MODES.map((m) => <option key={m} value={m}>{m}</option>)}
                    </select>
                    <button
                      onClick={() => patch.mutate({ id: s.id, patch: { Enabled: !s.enabled } })}
                      className={`w-10 h-5 rounded-full relative transition-colors ${s.enabled ? 'bg-blue-600' : 'bg-gray-700'}`}
                      aria-label={s.enabled ? 'disable' : 'enable'}
                    >
                      <span className={`absolute top-0.5 w-4 h-4 rounded-full bg-white transition-all ${s.enabled ? 'right-0.5' : 'left-0.5'}`} />
                    </button>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </QueryState>
      </div>
    </div>
  )
}
