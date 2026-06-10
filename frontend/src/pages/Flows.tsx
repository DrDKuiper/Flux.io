import { useState } from 'react'
import { TopBar } from '../components/TopBar'
import { QueryState } from '../components/QueryState'
import { useFlows, FlowQuery } from '../api/hooks'
import { useRange } from '../range/RangeProvider'
import { formatBytes } from '../lib/format'

const PAGE = 50

export function Flows() {
  const { range, source } = useRange()
  const [filters, setFilters] = useState({ src_ip: '', dst_ip: '', port: '', app: '', country: '' })
  const [offset, setOffset] = useState(0)

  const query: FlowQuery = {
    range,
    source: source || undefined,
    src_ip: filters.src_ip || undefined,
    dst_ip: filters.dst_ip || undefined,
    port: filters.port ? Number(filters.port) : undefined,
    app: filters.app || undefined,
    country: filters.country || undefined,
    limit: PAGE,
    offset,
  }
  const flows = useFlows(query)

  const input = (key: keyof typeof filters, ph: string) => (
    <input
      placeholder={ph}
      value={filters[key]}
      onChange={(e) => { setOffset(0); setFilters({ ...filters, [key]: e.target.value }) }}
      className="bg-gray-950 border border-gray-800 rounded px-2 py-1 text-xs text-white w-28 focus:outline-none focus:border-blue-500"
    />
  )

  return (
    <div>
      <TopBar title="Flows" />
      <div className="p-6 space-y-4">
        <div className="flex flex-wrap gap-2">
          {input('src_ip', 'IP origem')}
          {input('dst_ip', 'IP destino')}
          {input('port', 'Porta')}
          {input('app', 'Aplicação')}
          {input('country', 'País (US)')}
        </div>

        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <QueryState
            isLoading={flows.isLoading}
            isError={flows.isError}
            isEmpty={(flows.data?.items.length ?? 0) === 0}
            onRetry={flows.refetch}
          >
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead className="text-gray-500 text-left">
                  <tr>
                    <th className="py-1">Quando</th><th>Source</th><th>Origem</th><th>Destino</th>
                    <th>Proto</th><th>Bytes</th><th>App</th><th>SNI/Host</th><th>Países</th>
                  </tr>
                </thead>
                <tbody>
                  {flows.data?.items.map((f, i) => (
                    <tr key={i} className="border-t border-gray-800 text-gray-300">
                      <td className="py-1.5 text-gray-500">{new Date(f.ts).toLocaleTimeString()}</td>
                      <td>{f.source}</td>
                      <td>{f.src_ip}:{f.src_port}</td>
                      <td>{f.dst_ip}:{f.dst_port}</td>
                      <td>{f.protocol}</td>
                      <td>{formatBytes(f.bytes)}</td>
                      <td>{f.application_id}</td>
                      <td className="truncate max-w-[160px]">{f.sni || f.http_host}</td>
                      <td>{f.src_country}→{f.dst_country}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </QueryState>
          <div className="flex justify-between items-center mt-3 text-xs text-gray-400">
            <span>{flows.data?.total ?? 0} flows</span>
            <div className="flex gap-2">
              <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))} className="px-2 py-1 border border-gray-800 rounded disabled:opacity-40">Anterior</button>
              <button disabled={(offset + PAGE) >= (flows.data?.total ?? 0)} onClick={() => setOffset(offset + PAGE)} className="px-2 py-1 border border-gray-800 rounded disabled:opacity-40">Próximo</button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
