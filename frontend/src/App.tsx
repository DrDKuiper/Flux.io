import { Routes, Route, Outlet } from 'react-router-dom'
import { RequireAuth } from './auth/RequireAuth'
import { Sidebar } from './components/Sidebar'
import { Login } from './pages/Login'
import { Dashboard } from './pages/Dashboard'
import { FlowMap } from './pages/FlowMap'
import { Alerts } from './pages/Alerts'
import { Flows } from './pages/Flows'
import { Sources } from './pages/Sources'

function Layout() {
  return (
    <div className="flex h-screen bg-black text-gray-100 font-sans">
      <Sidebar />
      <div className="flex-1 overflow-auto bg-black">
        <Outlet />
      </div>
    </div>
  )
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route path="/" element={<Dashboard />} />
        <Route path="/flows" element={<Flows />} />
        <Route path="/map" element={<FlowMap />} />
        <Route path="/alerts" element={<Alerts />} />
        <Route path="/sources" element={<Sources />} />
      </Route>
    </Routes>
  )
}
