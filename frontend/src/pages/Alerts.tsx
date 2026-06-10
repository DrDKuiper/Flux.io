import { useState } from 'react'
import { TopBar } from '../components/TopBar'
import { QueryState } from '../components/QueryState'
import { SeverityBadge } from '../components/SeverityBadge'
import { useAlerts } from '../api/hooks'
import { useRange } from '../range/RangeProvider'
import { useLiveAlerts } from '../ws/useLiveAlerts'

const PAGE = 50

export function Alerts() {
  const { range, source } = useRange()
  const [offset, setOffset] = useState(0)
  const history = useAlerts(range, source, PAGE, offset)
  const live = useLiveAlerts()

  return (
    <div>
      <TopBar title="Alertas" />
      <div className="p-6 space-y-4">
        {live.length > 0 && (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-400 text-sm mb-3">Ao vivo</div>
            <div className="flex flex-col gap-2">
              {live.map((a, i) => (
                <div key={i} className="flex items-center gap-3 text-xs">
                  <SeverityBadge severity={a.severity} />
                  <span className="text-white flex-1 truncate">{a.signature}</span>
                  <span className="text-gray-400">{a.src_ip} → {a.dst_ip}</span>
                </div>
              ))}
            </div>
          </div>
        )}

        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <div className="text-gray-400 text-sm mb-3">Histórico</div>
          <QueryState
            isLoading={history.isLoading}
            isError={history.isError}
            isEmpty={(history.data?.items.length ?? 0) === 0}
            onRetry={history.refetch}
          >
            <table className="w-full text-xs">
              <thead className="text-gray-500 text-left">
                <tr>
                  <th className="py-1">Sev</th><th>Assinatura</th><th>Origem</th><th>Destino</th><th>Quando</th>
                </tr>
              </thead>
              <tbody>
                {history.data?.items.map((a, i) => (
                  <tr key={i} className="border-t border-gray-800">
                    <td className="py-1.5"><SeverityBadge severity={a.severity} /></td>
                    <td className="text-white">{a.signature}</td>
                    <td className="text-gray-400">{a.src_ip}</td>
                    <td className="text-gray-400">{a.dst_ip}</td>
                    <td className="text-gray-500">{new Date(a.ts).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </QueryState>
          <div className="flex justify-between items-center mt-3 text-xs text-gray-400">
            <span>{history.data?.total ?? 0} alertas</span>
            <div className="flex gap-2">
              <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))} className="px-2 py-1 border border-gray-800 rounded disabled:opacity-40">Anterior</button>
              <button disabled={(offset + PAGE) >= (history.data?.total ?? 0)} onClick={() => setOffset(offset + PAGE)} className="px-2 py-1 border border-gray-800 rounded disabled:opacity-40">Próximo</button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
