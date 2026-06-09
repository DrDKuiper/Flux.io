import { useState } from 'react';
import { BrowserRouter as Router, Routes, Route, Link, Navigate } from 'react-router-dom';
import { Activity, ShieldAlert, Map as MapIcon, GitCommit, LogOut, Settings as SettingsIcon } from 'lucide-react';
import Settings from './pages/Settings';
import { MapContainer, TileLayer, Marker, Popup } from 'react-leaflet';
import 'leaflet/dist/leaflet.css';

// Fix for leaflet marker icon missing in React
import L from 'leaflet';
import icon from 'leaflet/dist/images/marker-icon.png';
import iconShadow from 'leaflet/dist/images/marker-shadow.png';
let DefaultIcon = L.icon({
    iconUrl: icon,
    shadowUrl: iconShadow
});
L.Marker.prototype.options.icon = DefaultIcon;

function Login({ onLogin }: { onLogin: (token: string) => void }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    if (username === 'admin' && password === 'admin') {
      onLogin("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.mock.token");
    } else {
      setError("Credenciais inválidas");
    }
  };

  return (
    <div className="flex h-screen items-center justify-center bg-black text-gray-100">
      <div className="bg-gray-900 p-8 rounded-xl shadow-lg w-96 border border-gray-800">
        <h2 className="text-2xl font-bold mb-6 text-center tracking-wider text-white">Flux.io</h2>
        {error && <div className="bg-red-900/50 text-red-200 p-2 mb-4 rounded text-sm text-center">{error}</div>}
        <form onSubmit={handleLogin} className="space-y-4">
          <div>
            <label className="block text-sm text-gray-400 mb-1">Usuário</label>
            <input 
              type="text" 
              className="w-full bg-gray-950 border border-gray-800 rounded p-2 text-white focus:outline-none focus:border-blue-500" 
              value={username} 
              onChange={e => setUsername(e.target.value)} 
            />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">Senha</label>
            <input 
              type="password" 
              className="w-full bg-gray-950 border border-gray-800 rounded p-2 text-white focus:outline-none focus:border-blue-500" 
              value={password} 
              onChange={e => setPassword(e.target.value)} 
            />
          </div>
          <button type="submit" className="w-full bg-blue-600 hover:bg-blue-700 text-white font-semibold py-2 rounded transition-colors">
            Entrar
          </button>
        </form>
      </div>
    </div>
  );
}

function Dashboard() {
  return (
    <div className="p-6">
      <h1 className="text-2xl font-bold mb-4">Network Dashboard</h1>
      <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
        <div className="bg-gray-900 border border-gray-800 p-4 rounded-xl shadow-lg">
          <h2 className="text-gray-400 text-sm font-semibold">Top Talkers</h2>
          <div className="mt-4 h-48 flex items-center justify-center text-gray-500 bg-gray-950 rounded border border-gray-800">Recharts BarChart</div>
        </div>
        <div className="bg-gray-900 border border-gray-800 p-4 rounded-xl shadow-lg">
          <h2 className="text-gray-400 text-sm font-semibold">Top Applications (DPI)</h2>
          <div className="mt-4 h-48 flex items-center justify-center text-gray-500 bg-gray-950 rounded border border-gray-800">Recharts PieChart</div>
        </div>
        <div className="bg-gray-900 border border-gray-800 p-4 rounded-xl shadow-lg">
          <h2 className="text-gray-400 text-sm font-semibold">Bandwidth Consumption</h2>
          <div className="mt-4 h-48 flex items-center justify-center text-gray-500 bg-gray-950 rounded border border-gray-800">Recharts LineChart</div>
        </div>
      </div>
    </div>
  );
}

function FlowMap() {
  return (
    <div className="p-6 h-full flex flex-col">
      <h1 className="text-2xl font-bold mb-4">Global Flow Map</h1>
      <div className="flex-1 bg-gray-900 border border-gray-800 rounded-xl shadow-lg overflow-hidden">
        <MapContainer center={[20, 0]} zoom={2} style={{ height: '100%', width: '100%', background: '#09090b' }}>
          <TileLayer
            url="https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png"
            attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors &copy; <a href="https://carto.com/attributions">CARTO</a>'
          />
          <Marker position={[-23.5505, -46.6333]}>
            <Popup>Tráfego detectado: São Paulo, BR</Popup>
          </Marker>
          <Marker position={[40.7128, -74.0060]}>
            <Popup>C2 Connection: New York, US</Popup>
          </Marker>
        </MapContainer>
      </div>
    </div>
  );
}

function App() {
  const [token, setToken] = useState<string | null>(localStorage.getItem('jwt'));

  const handleLogin = (newToken: string) => {
    localStorage.setItem('jwt', newToken);
    setToken(newToken);
  };

  const handleLogout = () => {
    localStorage.removeItem('jwt');
    setToken(null);
  };

  if (!token) {
    return <Login onLogin={handleLogin} />;
  }

  return (
    <Router>
      <div className="flex h-screen bg-black text-gray-100 font-sans">
        {/* Sidebar */}
        <div className="w-64 bg-gray-950 border-r border-gray-800 flex flex-col">
          <div className="p-6 flex items-center space-x-3">
            <div className="w-8 h-8 bg-blue-600 rounded-lg flex items-center justify-center">
              <Activity className="w-5 h-5 text-white" />
            </div>
            <span className="text-xl font-bold tracking-wider">Flux.io</span>
          </div>
          
          <nav className="flex-1 px-4 space-y-2 mt-4">
            <Link to="/" className="flex items-center space-x-3 px-4 py-3 text-gray-300 hover:bg-gray-900 hover:text-white rounded-lg transition-colors">
              <Activity className="w-5 h-5" />
              <span>Dashboard</span>
            </Link>
            <Link to="/map" className="flex items-center space-x-3 px-4 py-3 text-gray-300 hover:bg-gray-900 hover:text-white rounded-lg transition-colors">
              <MapIcon className="w-5 h-5" />
              <span>Geo Map</span>
            </Link>
            <Link to="/settings" className="flex items-center space-x-3 px-4 py-3 text-gray-300 hover:bg-gray-900 hover:text-white rounded-lg transition-colors">
              <SettingsIcon className="w-5 h-5" />
              <span>Configurações</span>
            </Link>
          </nav>
          
          <div className="p-4 border-t border-gray-800">
            <button onClick={handleLogout} className="flex items-center w-full space-x-3 px-4 py-3 text-gray-400 hover:bg-gray-900 hover:text-white rounded-lg transition-colors">
              <LogOut className="w-5 h-5" />
              <span>Sair</span>
            </button>
          </div>
        </div>

        {/* Main Content */}
        <div className="flex-1 overflow-auto bg-black flex flex-col">
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/map" element={<FlowMap />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="*" element={<Navigate to="/" />} />
          </Routes>
        </div>
      </div>
    </Router>
  );
}

export default App;
