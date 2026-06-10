import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { AuthProvider, useAuth } from './AuthProvider'

function Probe() {
  const { token, setSession, logout } = useAuth()
  return (
    <div>
      <span data-testid="token">{token ?? 'none'}</span>
      <button onClick={() => setSession('abc', new Date(Date.now() + 3600_000).toISOString())}>login</button>
      <button onClick={logout}>logout</button>
    </div>
  )
}

describe('AuthProvider', () => {
  beforeEach(() => localStorage.clear())

  it('stores and clears the session', () => {
    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    )
    expect(screen.getByTestId('token').textContent).toBe('none')

    act(() => screen.getByText('login').click())
    expect(screen.getByTestId('token').textContent).toBe('abc')
    expect(localStorage.getItem('fluxio_token')).toBe('abc')

    act(() => screen.getByText('logout').click())
    expect(screen.getByTestId('token').textContent).toBe('none')
    expect(localStorage.getItem('fluxio_token')).toBeNull()
  })
})
