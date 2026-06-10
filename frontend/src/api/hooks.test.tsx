import { describe, it, expect, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiClient } from './client'
import { ApiProvider, useOverview } from './hooks'
import { ReactNode } from 'react'

function wrapper(client: ApiClient) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>
      <ApiProvider client={client}>{children}</ApiProvider>
    </QueryClientProvider>
  )
}

describe('useOverview', () => {
  it('fetches overview with range + source in the path', async () => {
    const get = vi.fn().mockResolvedValue({ flows: 3, bytes: 0, packets: 0, active_alerts: 0 })
    const client = { get } as unknown as ApiClient

    const { result } = renderHook(() => useOverview('24h', '10.0.0.1'), { wrapper: wrapper(client) })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.flows).toBe(3)
    expect(get).toHaveBeenCalledWith('/api/metrics/overview?range=24h&source=10.0.0.1')
  })
})
