import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ApiClient } from './client'

describe('ApiClient', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('injects the bearer token and parses JSON', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ flows: 7 }), { status: 200 }),
    )
    vi.stubGlobal('fetch', fetchMock)
    const onUnauthorized = vi.fn()
    const client = new ApiClient(() => 'tok-123', onUnauthorized)

    const data = await client.get<{ flows: number }>('/api/metrics/overview')

    expect(data.flows).toBe(7)
    const [, init] = fetchMock.mock.calls[0]
    expect(init.headers.Authorization).toBe('Bearer tok-123')
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it('calls onUnauthorized on a 401', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 401 })))
    const onUnauthorized = vi.fn()
    const client = new ApiClient(() => 'tok', onUnauthorized)

    await expect(client.get('/api/metrics/overview')).rejects.toThrow()
    expect(onUnauthorized).toHaveBeenCalledOnce()
  })
})
