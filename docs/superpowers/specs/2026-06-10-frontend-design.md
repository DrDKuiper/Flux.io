# Frontend Design — Flux.io

**Date:** 2026-06-10
**Status:** Approved
**Sub-project:** C (of A → B1 → B2 → C: merge → sources → read APIs → frontend)

---

## Goal

Replace the placeholder React scaffold with a real, authenticated dashboard that
consumes the B2 REST + WebSocket API: live network dashboard, geographic map,
Suricata alert feed, flow explorer, and source management — all behind JWT login.

This is **sub-project C**, the last of the four. It depends on B2's API surface.

---

## Scope

**In scope:**
- Extend the existing `frontend/` React 18 + Vite 5 + Tailwind 3 app (keep its deps:
  react-router-dom, recharts, react-leaflet, lucide-react).
- JWT auth: real login, token in localStorage, route guard, 401 → logout.
- Data layer: `apiClient` fetch wrapper + TanStack Query hooks per endpoint.
- A single WebSocket provider that writes live `metrics`/`alert` messages into the
  react-query cache (Approach 1).
- Screens: Login, Dashboard, Geo Map, Alerts feed, Flow explorer, Sources.
- Dark "security console" theme; shared loading/empty/error states.
- Vitest + React Testing Library unit tests.

**Out of scope:**
- A standalone Settings screen (removed; per-source config lives on the Sources
  screen, global DPI mode no longer exists).
- Account management (no change-password/user-CRUD API yet).
- Token refresh (no refresh endpoint; expiry → logout).
- SSR / Next.js / a different UI toolkit.

---

## Architecture

### App shell & routing
`react-router-dom`. A `<RequireAuth>` wrapper redirects to `/login` when there is
no token. The authenticated layout is a sidebar (Dashboard, Flows, Map, Alerts,
Sources) + content area, with a global "live" indicator and a range picker
(`15m|1h|6h|24h|7d`, default `1h`) in the top bar. The selected range is shared
app-wide (lifted to a small context or the query-key factory) so every screen
honors it.

### Auth
`AuthProvider` (React Context) holds `{token, expiresAt}` persisted in
localStorage. `login(username, password)` calls `POST /api/auth/login`, stores the
result, and navigates to `/`. `logout()` clears storage, closes the WebSocket, and
navigates to `/login`. `/login` is a username/password form replacing the old mock.

### Data layer
- **`apiClient`** (`api/client.ts`): a `fetch` wrapper that prepends the API base,
  injects `Authorization: Bearer <token>`, parses JSON, and on a `401` response
  triggers `logout()` (so an expired/invalid token bounces the user to login).
- **TanStack Query**: `QueryClientProvider` at the root. One hook per endpoint
  (`useOverview`, `useTopTalkers`, `useTopApps`, `useThroughput`, `useGeo`,
  `useAlerts`, `useFlows`, `useSources`, `useSource`). Query keys include the
  active `range` and any filters so changing them refetches. A `usePatchSource`
  mutation calls `PATCH /api/sources/:id` and invalidates `['sources']`.
- **WebSocket provider** (`ws/WebSocketProvider.tsx`, Approach 1): opens
  `ws://<host>/ws?token=<jwt>` once after login, with auto-reconnect (exponential
  backoff capped at ~30s). On `{type:"metrics"}` it calls
  `queryClient.setQueryData(['metrics','live'], data)`; on `{type:"alert"}` it
  prepends to `['alerts','live']` (bounded list). It reopens when the token
  changes and closes on logout.

### Shared states
Every `useQuery` consumer renders through a `<QueryState>` helper: a skeleton
while loading, an error banner with a retry button on failure, and an empty-state
message when the result is empty.

---

## Screens

| Route | Screen | Data |
|-------|--------|------|
| `/login` | Login | `POST /api/auth/login`; on success store token → `/`; on error show "invalid credentials". |
| `/` | Dashboard | `GET /api/metrics/overview` (4 KPIs), `/top-talkers` (bars), `/top-apps` (donut), `/throughput` (Recharts line), recent alerts from `['alerts','live']` + `GET /api/alerts?limit=5`. Live mode overlays WS metrics. |
| `/map` | Geo Map | `GET /api/geo/flows` → aggregate by country; map ISO code → centroid via `lib/countryCentroids.ts`; Leaflet markers sized by volume. |
| `/alerts` | Alerts feed | Live alerts from WS (`['alerts','live']`) on top + paginated history `GET /api/alerts?range=&limit=&offset=`. Severity badges. |
| `/flows` | Flow explorer | Paginated `GET /api/flows` with filters (src_ip, dst_ip, port, app, country, source) + range. Columns: ts, source, src→dst, ports, proto, bytes, app, SNI/host, countries. |
| `/sources` | Sources | `GET /api/sources` grouped by `group_tag`, each with status/rate/mismatch. Edit name/group, toggle enabled, pick DPI mode → `PATCH /api/sources/:id`. Detail (`GET /api/sources/:id`) shows transport, expected-vs-actual, last-seen. |

Clicking a source filters the dashboard/flows by that `source` (passed as the
`source` query param).

---

## Data Flow

1. User logs in → token stored → `RequireAuth` admits them → WebSocket opens.
2. A screen mounts → its `useQuery` hook fetches via `apiClient` with the current
   range/filters → renders through `<QueryState>`.
3. The WebSocket pushes `metrics`/`alert` → provider writes them into the query
   cache → the Dashboard and Alerts screens re-render with live data, no refetch.
4. Editing a source → `usePatchSource` mutation → `['sources']` invalidated →
   list refetches with the new config.

---

## Error Handling

- `401` from any call → `apiClient` calls `logout()` → redirect to `/login`.
- Network/5xx → `<QueryState>` error banner with retry (react-query `refetch`).
- WebSocket drop → provider auto-reconnects with backoff; a small "reconnecting"
  indicator shows in the top bar while disconnected.
- Empty results → friendly empty state, not an error.
- Login failure → inline error on the form.

---

## File Structure

```
frontend/src/
├── main.tsx                  # root: QueryClientProvider + AuthProvider + Router
├── App.tsx                   # routes + RequireAuth + layout/sidebar
├── api/
│   ├── client.ts             # fetch wrapper (bearer, 401→logout)
│   ├── types.ts              # TS types mirroring backend DTOs
│   └── hooks.ts              # useOverview, useFlows, useSources, usePatchSource, ...
├── auth/
│   ├── AuthProvider.tsx      # context: token, login, logout
│   └── RequireAuth.tsx
├── ws/
│   └── WebSocketProvider.tsx # single connection, reconnect, writes to query cache
├── components/
│   ├── Sidebar.tsx, RangePicker.tsx, SeverityBadge.tsx
│   ├── StatCard.tsx, QueryState.tsx
│   └── charts/ThroughputChart.tsx, TopTalkersBars.tsx, TopAppsDonut.tsx
├── pages/
│   ├── Login.tsx, Dashboard.tsx, FlowMap.tsx
│   ├── Alerts.tsx, Flows.tsx, Sources.tsx
└── lib/
    └── countryCentroids.ts   # ISO country code → lat/lon
```

---

## Theme

Dark "security console" (the existing app's base): near-black background,
zinc/gray-900 surfaces, blue accent, severity colors (high=red, medium=amber,
low=blue). Tailwind utility classes; complete the CSS variables already declared
in `index.css` by wiring the missing ones into `tailwind.config.js` (card, muted,
accent, destructive — currently only border/background/foreground/primary are
mapped).

---

## Testing

Vitest + React Testing Library (new dev deps). `npm run build` (tsc + vite)
remains the type gate.

- **`apiClient`:** injects the bearer header; a 401 response invokes the logout
  callback.
- **hooks:** rendered with a test `QueryClientProvider` and a mocked fetch —
  assert the response is parsed and the query key includes range/filters.
- **`WebSocketProvider`:** an `alert` message prepends to the `['alerts','live']`
  cache; a `metrics` message updates `['metrics','live']`; the provider reconnects
  after a close.
- **components:** `RangePicker` changing value updates the shared range; the
  Sources enable toggle fires the PATCH mutation (RTL + mocked mutation).

---

## Files Changed

| File | Change |
|------|--------|
| `frontend/src/App.tsx`, `main.tsx` | Rewrite: providers, routes, layout. |
| `frontend/src/{api,auth,ws,components,pages,lib}/*` | New — per the structure above. |
| `frontend/src/pages/Settings.tsx` | Remove (superseded by Sources). |
| `frontend/tailwind.config.js` | Wire the remaining CSS-variable colors. |
| `frontend/package.json` | Add `@tanstack/react-query`; dev: `vitest`, `@testing-library/react`, `@testing-library/jest-dom`, `jsdom`. |
| `frontend/vite.config.ts` | Add Vitest test config (jsdom environment). |

---

## New Dependencies

| Dependency | Purpose |
|------------|---------|
| `@tanstack/react-query` | Server-state: caching, polling, loading/error. |
| `vitest` (dev) | Test runner. |
| `@testing-library/react` + `@testing-library/jest-dom` (dev) | Component tests. |
| `jsdom` (dev) | DOM environment for Vitest. |

---

## Non-Goals

- No standalone Settings screen, no account management, no token refresh.
- No new charting/UI libraries beyond what the scaffold already has.
- No backend changes (C consumes B2 as-is).
