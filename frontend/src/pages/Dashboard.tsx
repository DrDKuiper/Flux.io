import { ReactNode } from 'react'
import { TopBar } from '../components/TopBar'
import { StatCard } from '../components/StatCard'
import { QueryState } from '../components/QueryState'
import { SeverityBadge } from '../components/SeverityBadge'
import { ThroughputChart } from '../components/charts/ThroughputChart'
import { TopTalkersBars } from '../components/charts/TopTalkersBars'
import { TopAppsDonut } from '../components/charts/TopAppsDonut'
import { useOverview, useTopTalkers, useTopApps, useThroughput } from '../api/hooks'
import { useRange } from '../range/RangeProvider'
import { useLiveAlerts } from '../ws/useLiveAlerts'
import { formatBytes, formatNumber } from '../lib/format'

function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
      <div className="text-gray-400 text-sm mb-3">{title}</div>
      {children}
    </div>
  )
}

export function Dashboard() {
  const { range, source } = useRange()
  const overview = useOverview(range, source)
  const talkers = useTopTalkers(range, source)
  const apps = useTopApps(range, source)
  const tput = useThroughput(range, source)
  const liveAlerts = useLiveAlerts()

  return (
    <div>
      <TopBar title="Network dashboard" />
      <div className="p-6 space-y-4">
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <StatCard label="Flows" value={formatNumber(overview.data?.flows ?? 0)} />
          <StatCard label="Tráfego" value={formatBytes(overview.data?.bytes ?? 0)} />
          <StatCard label="Pacotes" value={formatNumber(overview.data?.packets ?? 0)} />
          <StatCard label="Alertas ativos" value={String(overview.data?.active_alerts ?? 0)} danger />
        </div>

        <Panel title="Throughput">
          <QueryState isLoading={tput.isLoading} isError={tput.isError} isEmpty={(tput.data?.length ?? 0) === 0} onRetry={tput.refetch}>
            <ThroughputChart data={tput.data ?? []} />
          </QueryState>
        </Panel>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Panel title="Top talkers">
            <QueryState isLoading={talkers.isLoading} isError={talkers.isError} isEmpty={(talkers.data?.length ?? 0) === 0} onRetry={talkers.refetch}>
              <TopTalkersBars data={talkers.data ?? []} />
            </QueryState>
          </Panel>
          <Panel title="Top aplicações (DPI)">
            <QueryState isLoading={apps.isLoading} isError={apps.isError} isEmpty={(apps.data?.length ?? 0) === 0} onRetry={apps.refetch}>
              <TopAppsDonut data={apps.data ?? []} />
            </QueryState>
          </Panel>
        </div>

        <Panel title="Alertas recentes (ao vivo)">
          {liveAlerts.length === 0 ? (
            <div className="text-gray-500 text-sm">Nenhum alerta recente.</div>
          ) : (
            <div className="flex flex-col gap-2">
              {liveAlerts.slice(0, 5).map((a, i) => (
                <div key={i} className="flex items-center gap-3 text-xs">
                  <SeverityBadge severity={a.severity} />
                  <span className="text-white flex-1 truncate">{a.signature}</span>
                  <span className="text-gray-400">{a.src_ip} → {a.dst_ip}</span>
                </div>
              ))}
            </div>
          )}
        </Panel>
      </div>
    </div>
  )
}
