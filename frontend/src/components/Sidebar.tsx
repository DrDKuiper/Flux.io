import { Link, useLocation } from 'react-router-dom'
import { Activity, Table, Map as MapIcon, AlertTriangle, Server, LogOut } from 'lucide-react'
import { useAuth } from '../auth/AuthProvider'

const NAV = [
  { to: '/', label: 'Dashboard', icon: Activity },
  { to: '/flows', label: 'Flows', icon: Table },
  { to: '/map', label: 'Geo Map', icon: MapIcon },
  { to: '/alerts', label: 'Alerts', icon: AlertTriangle },
  { to: '/sources', label: 'Sources', icon: Server },
]

export function Sidebar() {
  const { logout } = useAuth()
  const { pathname } = useLocation()
  return (
    <div className="w-60 bg-gray-950 border-r border-gray-800 flex flex-col">
      <div className="p-6 flex items-center space-x-3">
        <div className="w-8 h-8 bg-blue-600 rounded-lg flex items-center justify-center">
          <Activity className="w-5 h-5 text-white" />
        </div>
        <span className="text-xl font-bold tracking-wider">Flux.io</span>
      </div>
      <nav className="flex-1 px-3 space-y-1 mt-2">
        {NAV.map(({ to, label, icon: Icon }) => (
          <Link
            key={to}
            to={to}
            className={`flex items-center space-x-3 px-4 py-2.5 rounded-lg transition-colors ${
              pathname === to ? 'bg-gray-900 text-white' : 'text-gray-400 hover:bg-gray-900 hover:text-white'
            }`}
          >
            <Icon className="w-5 h-5" />
            <span>{label}</span>
          </Link>
        ))}
      </nav>
      <div className="p-3 border-t border-gray-800">
        <button
          onClick={logout}
          className="flex items-center w-full space-x-3 px-4 py-2.5 text-gray-400 hover:bg-gray-900 hover:text-white rounded-lg"
        >
          <LogOut className="w-5 h-5" />
          <span>Sair</span>
        </button>
      </div>
    </div>
  )
}
