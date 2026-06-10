import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { useApiClient } from '../api/clientContext'

export function Login() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const { setSession } = useAuth()
  const client = useApiClient()
  const navigate = useNavigate()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    try {
      const res = await client.post<{ token: string; expires_at: string }>('/api/auth/login', {
        username,
        password,
      })
      setSession(res.token, res.expires_at)
      navigate('/')
    } catch {
      setError('Credenciais inválidas')
    }
  }

  return (
    <div className="flex h-screen items-center justify-center bg-black text-gray-100">
      <div className="bg-gray-900 p-8 rounded-xl shadow-lg w-96 border border-gray-800">
        <h2 className="text-2xl font-bold mb-6 text-center tracking-wider text-white">Flux.io</h2>
        {error && <div className="bg-red-900/50 text-red-200 p-2 mb-4 rounded text-sm text-center">{error}</div>}
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm text-gray-400 mb-1">Usuário</label>
            <input
              className="w-full bg-gray-950 border border-gray-800 rounded p-2 text-white focus:outline-none focus:border-blue-500"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">Senha</label>
            <input
              type="password"
              className="w-full bg-gray-950 border border-gray-800 rounded p-2 text-white focus:outline-none focus:border-blue-500"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
          <button
            type="submit"
            className="w-full bg-blue-600 hover:bg-blue-700 text-white font-semibold py-2 rounded transition-colors"
          >
            Entrar
          </button>
        </form>
      </div>
    </div>
  )
}
