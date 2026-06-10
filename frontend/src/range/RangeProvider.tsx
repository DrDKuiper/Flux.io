import { createContext, useContext, useMemo, useState, ReactNode } from 'react'
import { RangeToken } from '../api/types'

interface RangeState {
  range: RangeToken
  setRange: (r: RangeToken) => void
  source: string // '' = all sources
  setSource: (s: string) => void
}

const RangeContext = createContext<RangeState | null>(null)

export function RangeProvider({ children }: { children: ReactNode }) {
  const [range, setRange] = useState<RangeToken>('1h')
  const [source, setSource] = useState('')
  const value = useMemo(() => ({ range, setRange, source, setSource }), [range, source])
  return <RangeContext.Provider value={value}>{children}</RangeContext.Provider>
}

export function useRange(): RangeState {
  const ctx = useContext(RangeContext)
  if (!ctx) throw new Error('useRange must be used within RangeProvider')
  return ctx
}
