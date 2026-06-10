import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import App from './App'
import './index.css'
import { ApiClient } from './api/client'
import { ApiProvider } from './api/hooks'
import { ApiClientProvider } from './api/clientContext'
import { AuthProvider } from './auth/AuthProvider'
import { RangeProvider } from './range/RangeProvider'
import { WebSocketProvider } from './ws/WebSocketProvider'

const queryClient = new QueryClient()

// One ApiClient instance shared by the query hooks and direct callers (Login).
// It reads the token from localStorage and bounces to /login on a 401.
const apiClient = new ApiClient(
  () => localStorage.getItem('fluxio_token'),
  () => {
    localStorage.removeItem('fluxio_token')
    localStorage.removeItem('fluxio_expires')
    if (location.pathname !== '/login') location.assign('/login')
  },
)

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <QueryClientProvider client={queryClient}>
        <AuthProvider>
          <ApiClientProvider client={apiClient}>
            <ApiProvider client={apiClient}>
              <RangeProvider>
                <WebSocketProvider>
                  <App />
                </WebSocketProvider>
              </RangeProvider>
            </ApiProvider>
          </ApiClientProvider>
        </AuthProvider>
      </QueryClientProvider>
    </BrowserRouter>
  </React.StrictMode>,
)
