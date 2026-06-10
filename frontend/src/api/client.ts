// ApiClient is a thin fetch wrapper: it injects the bearer token, parses JSON,
// and invokes onUnauthorized() on a 401 so the app can log the user out.
export class ApiClient {
  constructor(
    private getToken: () => string | null,
    private onUnauthorized: () => void,
  ) {}

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    const token = this.getToken()
    if (token) headers.Authorization = `Bearer ${token}`

    const resp = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    })

    if (resp.status === 401) {
      this.onUnauthorized()
      throw new Error('unauthorized')
    }
    if (!resp.ok) {
      throw new Error(`request failed: ${resp.status}`)
    }
    const text = await resp.text()
    return (text ? JSON.parse(text) : undefined) as T
  }

  get<T>(path: string): Promise<T> {
    return this.request<T>('GET', path)
  }

  patch<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>('PATCH', path, body)
  }

  post<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>('POST', path, body)
  }
}

// buildQuery turns a params object into a query string, skipping empty values.
export function buildQuery(params: Record<string, string | number | undefined>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '' && v !== 0) q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}
