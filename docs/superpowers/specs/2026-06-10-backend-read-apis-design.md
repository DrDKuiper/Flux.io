# Backend Read APIs Design — Flux.io

**Date:** 2026-06-10
**Status:** Approved
**Sub-project:** B2 (of A → B1 → B2 → C: merge pipeline → sources backend → read APIs → frontend)

> Depends on **B1 (Sources backend)** — flows carry a `source` dimension, so every
> read endpoint below additionally accepts an optional `source` filter, and this
> router also implements the source endpoints (`GET /api/sources`,
> `GET /api/sources/:id`, `PATCH /api/sources/:id`) defined in the B1 spec.

---

## Goal

Expose the data the pipeline already collects (in ClickHouse + Postgres) through an
authenticated REST + WebSocket API, so the frontend can render a real dashboard,
geographic map, live alert feed, and flow explorer.

This is **sub-project B**. Sub-project A (merging `feature/backend-data-pipeline`
into `main`) is complete. Sub-project C (the frontend) is brainstormed separately
once this is built.

---

## Scope

**In scope:**
- Real JWT authentication backed by a Postgres `users` table (replaces the
  `admin/admin` mock and fake JWT).
- REST read endpoints for dashboard metrics, geo aggregation, alert history, and
  a filterable flow explorer.
- A single authenticated WebSocket endpoint that pushes periodic metrics snapshots
  and live Suricata alerts.
- ClickHouse read-query methods on the storage layer.
- New env vars (`JWT_SECRET`, `ADMIN_USERNAME`, `ADMIN_PASSWORD`) wired into
  `.env.example` and `docker-compose.yml`.

**Out of scope:**
- The frontend itself (sub-project C).
- ClickHouse materialized views / pre-aggregation (deferred; query-on-demand is
  sufficient for MVP volume).
- Multi-user management UI, roles/RBAC (single admin seed for now).
- Rate limiting, API keys, refresh tokens.

---

## Architecture

Chosen approach: **query-on-demand REST + a WebSocket hub broadcaster**.

```
                         ┌────────────────────────────────────────┐
   Browser  ──REST──►    │  Fiber router (/api/*)                  │
            ◄─JSON──     │   ├─ auth middleware (JWT)              │
                         │   ├─ /api/metrics/*  ─┐                 │
                         │   ├─ /api/geo/flows   │                 │
                         │   ├─ /api/alerts      ├─► ClickHouseStore│──► ClickHouse
                         │   └─ /api/flows       ┘   (read queries) │
                         │                                          │
   Browser  ──WS──►      │  /ws?token=<jwt>                         │
            ◄─push──     │   └─ Hub ◄── metrics broadcaster (5s tick)│──► ClickHouse
                         │          ◄── alert bridge (from correlator)│
                         │                                          │
                         │  /api/auth/login ──► auth repo (bcrypt)  │──► Postgres
                         └────────────────────────────────────────┘
```

- **REST** powers initial load, time-range (`?range=`) changes, pagination, and
  flow-explorer filters.
- **WebSocket** pushes live dashboard updates ("live" mode) and new alerts over a
  single connection.

---

## Components

### 1. Authentication (`backend/internal/auth/`)

| File | Responsibility |
|------|----------------|
| `repository.go` | Postgres-backed user repo: `GetByUsername`, `Count`, `Create`. Admin seed on boot. |
| `password.go` | bcrypt `HashPassword` / `CheckPassword`. |
| `jwt.go` | `IssueToken(username) (string, expiresAt)` and `ParseToken(string) (claims, error)`. HS256, 24h expiry. |
| `middleware.go` | Fiber middleware that requires `Authorization: Bearer <jwt>`; helper to validate a token string (reused for WS handshake). |

**Users table** (added to `db/postgres/init-db.sql`):
```sql
CREATE TABLE IF NOT EXISTS users (
    id            SERIAL PRIMARY KEY,
    username      TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Admin seed** — on startup, if `users` is empty, create one user from
`ADMIN_USERNAME` (default `admin`) and `ADMIN_PASSWORD`. If `ADMIN_PASSWORD` is
empty, generate a random password and log it exactly once. Password stored bcrypt.

**JWT secret** — read from `JWT_SECRET`. If empty, generate a random 32-byte secret
and persist it to a file (`/root/.fluxio_jwt_secret` inside the container, on a
mounted volume) so tokens survive restarts. Log a warning recommending an explicit
`JWT_SECRET`.

**Login**
```
POST /api/auth/login   { "username": "...", "password": "..." }
  → 200 { "token": "<jwt>", "expires_at": "2026-06-11T12:00:00Z" }
  → 401 { "error": "invalid credentials" }
```

**Middleware** applies to all `/api/*` except `/api/health` and `/api/auth/login`.
Missing/invalid/expired token → `401`.

**WebSocket auth** — token passed as `/ws?token=<jwt>`, validated during the
handshake. No valid token → connection refused (close before upgrade completes).

### 2. ClickHouse read queries (`backend/internal/storage/`)

New methods on `ClickHouseStore` (SQL kept here, isolated and testable behind an
interface the API package consumes):

| Method | Returns |
|--------|---------|
| `Overview(ctx, since time.Time)` | `{ Flows, Bytes, Packets, ActiveAlerts uint64 }` |
| `TopTalkers(ctx, since, limit)` | `[]{ IP, Hostname string; Bytes, Packets, Flows uint64 }` |
| `TopApps(ctx, since, limit)` | `[]{ ApplicationID string; Bytes, Flows uint64 }` |
| `Throughput(ctx, since, buckets)` | `[]{ TS time.Time; Bytes, Packets uint64 }` |
| `GeoByCountry(ctx, since)` | `[]{ Country string; Bytes, Flows uint64 }` |
| `FlowsFiltered(ctx, filter, limit, offset)` | `(total uint64, items []FlowRow)` |
| `AlertsHistory(ctx, since, limit, offset)` | `(total uint64, items []AlertRow)` |

`since` is computed from the `range` parameter. `Throughput` buckets the window
into N equal time buckets via ClickHouse `toStartOfInterval`.

Each method also takes an optional `source string` (empty = all sources); when set,
the generated SQL adds `AND source = ?`. `FlowsFiltered`'s filter struct includes
`Source` alongside the other filters.

The API package depends on a `reader` interface (these method signatures) so tests
can supply a fake — same pattern as the existing `alertWriter` interface.

### 3. REST handlers (`backend/internal/api/`)

| File | Endpoints |
|------|-----------|
| `router.go` | `RegisterRoutes(app, deps)`: mounts all routes, CORS, auth middleware. |
| `metrics.go` | `GET /api/metrics/overview`, `/top-talkers`, `/top-apps`, `/throughput`. |
| `geo.go` | `GET /api/geo/flows`. |
| `alerts.go` | `GET /api/alerts` (paginated history). |
| `flows.go` | `GET /api/flows` (filterable, paginated). |

**Common query params**
- `range` — one of `15m|1h|6h|24h|7d` (default `1h`). Invalid → `400`.
- `source` — optional source address (from B1). When present, the query is scoped
  to flows/alerts stamped with that source via a `WHERE source = ?` clause. Applies
  to all metrics, geo, alerts, and flows endpoints. Omitted → all sources.
- `limit` — default 50, max 500. `offset` — default 0.

**Endpoint contracts**
```
GET /api/metrics/overview?range=1h
  → { "flows": 12834, "bytes": 5200000000, "packets": 8100000, "active_alerts": 3 }

GET /api/metrics/top-talkers?range=1h&limit=10
  → [ { "ip": "10.0.0.5", "hostname": "host-a", "bytes": 9e8, "packets": 1e6, "flows": 412 }, ... ]

GET /api/metrics/top-apps?range=1h&limit=10
  → [ { "application_id": "tls", "bytes": 3e9, "flows": 8201 }, ... ]

GET /api/metrics/throughput?range=1h&buckets=60
  → [ { "ts": "2026-06-10T11:00:00Z", "bytes": 8.2e7, "packets": 1.1e5 }, ... ]

GET /api/geo/flows?range=1h
  → [ { "country": "US", "bytes": 4e9, "flows": 5102 }, ... ]

GET /api/alerts?range=24h&limit=50&offset=0
  → { "total": 87, "items": [ { "ts": "...", "src_ip": "...", "dst_ip": "...",
       "signature": "...", "severity": 2, "category": "..." }, ... ] }

GET /api/flows?range=1h&src_ip=&dst_ip=&port=&app=&country=&limit=50&offset=0
  → { "total": 12834, "items": [ { "ts", "src_ip", "dst_ip", "src_port", "dst_port",
       "protocol", "bytes", "packets", "application_id", "sni", "http_host",
       "src_country", "dst_country", "src_asn_org", "dst_asn_org" }, ... ] }
```

**Geo note:** `network_flows` stores ISO country codes, not lat/lon. The geo
endpoint returns aggregates keyed by country code; the frontend maps codes to map
coordinates with a static centroid lookup. No schema change.

### 4. WebSocket hub & broadcaster (`backend/internal/api/`)

| File | Responsibility |
|------|----------------|
| `hub.go` | Connected-client registry via channels (`register`, `unregister`, `broadcast`). `Broadcast(msg)` fans out; a client whose send buffer is full is dropped (non-blocking) so one slow client can't stall the hub. |
| `stream.go` | `GET /ws` handler (validates token, registers client) + the metrics broadcaster goroutine. |

**Message envelope** (one connection carries both types):
```jsonc
{ "type": "metrics", "data": {
    "overview": { ... }, "top_talkers": [ ... ],
    "top_apps": [ ... ], "throughput_point": { "ts", "bytes", "packets" } } }

{ "type": "alert", "data": {
    "ts", "src_ip", "dst_ip", "signature", "severity", "category" } }
```

**Producers**
1. **Metrics broadcaster** — goroutine ticking every 5s; runs the aggregation
   queries over a rolling 5m window; `Broadcast({type:"metrics"})`.
2. **Alert bridge** — the Suricata correlator already emits `SuricataAlert` values.
   Add a hook so each alert is also `Broadcast({type:"alert"})`. This wires the
   currently-stubbed `/ws/alerts` echo to the real alert stream. The old echo
   handler is removed.

### 5. Wiring (`backend/cmd/server/main.go`)

- Construct the `auth` repo + seed admin; construct JWT issuer with `JWT_SECRET`.
- Construct the `Hub`; start the metrics broadcaster goroutine under `pipelineCtx`.
- Inject the alert-bridge callback into the Suricata correlator path.
- Replace ad-hoc route registration with `api.RegisterRoutes(app, deps)`.
- Remove the stub `/ws/alerts` echo and the mock `/api/auth/login`.

---

## Data Flow

1. User logs in → `POST /api/auth/login` → JWT returned, stored client-side.
2. Frontend loads a screen → REST calls with `?range=` (and filters) → ClickHouse
   query-on-demand → JSON.
3. Frontend opens `/ws?token=` → receives `metrics` every 5s and `alert` on each
   new Suricata detection → overlays live updates on the REST-loaded state.

---

## Error Handling

- Invalid `range` or filter value → `400` with a clear JSON `{ "error": "..." }`.
- ClickHouse query error → log full detail server-side, return generic `500`
  (never leak SQL or internal detail to the client).
- Missing/invalid/expired JWT → `401`.
- Empty result set → `200` with an empty array/zeroed object (not an error).
- WebSocket: invalid token → refuse upgrade; client buffer full → drop that client.

---

## Testing (TDD)

- **auth:** bcrypt hash/verify round-trip; JWT issue/parse including expired and
  tampered tokens; middleware rejects bad/missing tokens; admin seed creates a user
  only when the table is empty.
- **param parsing:** `range` → `since` mapping and flow-explorer filter parsing,
  table-driven, including invalid inputs → `400`.
- **read queries:** API handlers tested against a fake `reader` interface (same
  pattern as the existing `alertWriter` fake) — asserts correct params passed and
  JSON shape returned, no live ClickHouse needed.
- **hub:** register/unregister/broadcast; a slow client (full buffer) is dropped
  without blocking the broadcast.

---

## Files Changed

| File | Change |
|------|--------|
| `backend/internal/auth/{repository,password,jwt,middleware}.go` | New — auth package. |
| `backend/internal/api/{router,metrics,geo,alerts,flows,hub,stream}.go` | New — API + WS package. |
| `backend/internal/api/sources.go` | New — source endpoints (`GET /api/sources`, `/:id`, `PATCH /:id`) over the B1 registry. |
| `backend/internal/storage/clickhouse.go` (or `queries.go`) | Add read-query methods. |
| `backend/cmd/server/main.go` | Wire auth, hub, broadcaster, alert bridge, routes. Remove stubs. |
| `db/postgres/init-db.sql` | Add `users` table. |
| `.env.example`, `docker-compose.yml` | Add `JWT_SECRET`, `ADMIN_USERNAME`, `ADMIN_PASSWORD`. |
| `backend/internal/collector/suricata_correlator.go` | Add alert-bridge hook (small). |

Test files alongside each new source file.

---

## New Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `JWT_SECRET` | *(generated + persisted if empty)* | HS256 signing secret. |
| `ADMIN_USERNAME` | `admin` | Seed admin username (only used when users table empty). |
| `ADMIN_PASSWORD` | *(random + logged once if empty)* | Seed admin password. |

The installer wizard will prompt for these in a later iteration.

---

## Non-Goals

- No frontend work (sub-project C).
- No materialized views (add later if query latency demands).
- No RBAC, multi-tenant, refresh tokens, or rate limiting in this iteration.
