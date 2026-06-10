import { createContext, useCallback, useContext, useMemo, useState, ReactNode } from 'react'

interface AuthState {
  token: string | null
  expiresAt: string | null
  setSession: (token: string, expiresAt: string) => void
  logout: () => void
}

const AuthContext = createContext<AuthState | null>(null)

const TOKEN_KEY = 'fluxio_token'
const EXP_KEY = 'fluxio_expires'

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem(TOKEN_KEY))
  const [expiresAt, setExpiresAt] = useState<string | null>(() => localStorage.getItem(EXP_KEY))

  const setSession = useCallback((t: string, exp: string) => {
    localStorage.setItem(TOKEN_KEY, t)
    localStorage.setItem(EXP_KEY, exp)
    setToken(t)
    setExpiresAt(exp)
  }, [])

  const logout = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(EXP_KEY)
    setToken(null)
    setExpiresAt(null)
  }, [])

  const value = useMemo(
    () => ({ token, expiresAt, setSession, logout }),
    [token, expiresAt, setSession, logout],
  )
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
