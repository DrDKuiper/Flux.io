import { MapContainer, TileLayer, CircleMarker, Popup } from 'react-leaflet'
import 'leaflet/dist/leaflet.css'
import { TopBar } from '../components/TopBar'
import { QueryState } from '../components/QueryState'
import { useGeo } from '../api/hooks'
import { useRange } from '../range/RangeProvider'
import { COUNTRY_CENTROIDS } from '../lib/countryCentroids'
import { formatBytes } from '../lib/format'

export function FlowMap() {
  const { range, source } = useRange()
  const geo = useGeo(range, source)
  const points = (geo.data ?? []).filter((g) => COUNTRY_CENTROIDS[g.country])
  const max = Math.max(1, ...points.map((p) => p.bytes))

  return (
    <div className="h-full flex flex-col">
      <TopBar title="Geo Map" />
      <div className="flex-1 p-6">
        <div className="h-full bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
          <QueryState isLoading={geo.isLoading} isError={geo.isError} isEmpty={points.length === 0} onRetry={geo.refetch}>
            <MapContainer center={[20, 0]} zoom={2} style={{ height: '100%', width: '100%', background: '#09090b' }}>
              <TileLayer
                url="https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png"
                attribution='&copy; OpenStreetMap &copy; CARTO'
              />
              {points.map((p) => {
                const [lat, lon] = COUNTRY_CENTROIDS[p.country]
                const radius = 6 + (p.bytes / max) * 24
                return (
                  <CircleMarker key={p.country} center={[lat, lon]} radius={radius} pathOptions={{ color: '#378ADD', fillColor: '#378ADD', fillOpacity: 0.4 }}>
                    <Popup>{p.country}: {formatBytes(p.bytes)} ({p.flows} flows)</Popup>
                  </CircleMarker>
                )
              })}
            </MapContainer>
          </QueryState>
        </div>
      </div>
    </div>
  )
}
