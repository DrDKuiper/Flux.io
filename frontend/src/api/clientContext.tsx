import { createContext, useContext, ReactNode } from 'react'
import { ApiClient } from './client'

const Ctx = createContext<ApiClient | null>(null)

export function ApiClientProvider({ client, children }: { client: ApiClient; children: ReactNode }) {
  return <Ctx.Provider value={client}>{children}</Ctx.Provider>
}

export function useApiClient(): ApiClient {
  const c = useContext(Ctx)
  if (!c) throw new Error('ApiClientProvider missing')
  return c
}
