import { describe, it, expect } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { applyWsMessage } from './cache'
import { AlertRow } from '../api/types'

describe('applyWsMessage', () => {
  it('writes a metrics snapshot to the live key', () => {
    const qc = new QueryClient()
    applyWsMessage(qc, { type: 'metrics', data: { overview: { flows: 9 } } })
    expect((qc.getQueryData(['metrics', 'live']) as any).overview.flows).toBe(9)
  })

  it('prepends an alert and caps the live list', () => {
    const qc = new QueryClient()
    const mk = (sig: string): AlertRow => ({
      ts: '', source: '', src_ip: '', dst_ip: '', signature: sig, category: '', severity: 1,
    })
    applyWsMessage(qc, { type: 'alert', data: mk('first') })
    applyWsMessage(qc, { type: 'alert', data: mk('second') })
    const list = qc.getQueryData(['alerts', 'live']) as AlertRow[]
    expect(list[0].signature).toBe('second')
    expect(list[1].signature).toBe('first')
  })

  it('ignores unknown message types', () => {
    const qc = new QueryClient()
    applyWsMessage(qc, { type: 'bogus', data: {} })
    expect(qc.getQueryData(['metrics', 'live'])).toBeUndefined()
  })
})
