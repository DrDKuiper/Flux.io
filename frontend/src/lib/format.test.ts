import { describe, it, expect } from 'vitest'
import { formatBytes } from './format'

describe('formatBytes', () => {
  it('formats to human-readable units', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1024)).toBe('1.0 KB')
    expect(formatBytes(5_200_000_000)).toBe('4.8 GB')
  })
})
