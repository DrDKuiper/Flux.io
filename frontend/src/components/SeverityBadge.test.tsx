import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SeverityBadge } from './SeverityBadge'

describe('SeverityBadge', () => {
  it('labels severities', () => {
    render(<SeverityBadge severity={1} />)
    expect(screen.getByText('high')).toBeInTheDocument()
    render(<SeverityBadge severity={2} />)
    expect(screen.getByText('medium')).toBeInTheDocument()
    render(<SeverityBadge severity={3} />)
    expect(screen.getByText('low')).toBeInTheDocument()
  })
})
