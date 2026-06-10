# Sources Backend Design — Flux.io

**Date:** 2026-06-10
**Status:** Approved
**Sub-project:** B1 (of A → B1 → B2 → C)

---

## Goal

Treat telemetry as coming from multiple **sources** (hosts/sensors), each exporting
differently (NetFlow v9, TZSP, or Suricata). Auto-discover sources by their
origin address, stamp every flow/alert with its source, allow per-host
configuration (name, group, enable/disable ingestion, expected type, DPI mode),
and expose source inventory + live stats through an API.

This is **sub-project B1**. It comes before the read APIs (B2) because flows need
the `source` dimension before B2 can filter/group by it.

---

## Scope

**In scope:**
- `sources` table in Postgres + a `SourceRegistry` (Postgres-backed, in-memory cached).
- Auto-discovery: first packet from a new origin lazily creates a source (enabled by default).
- `source` column stamped on `network_flows` and `suricata_alerts` (ClickHouse).
- Ingestion gate: flows/alerts from a disabled source are dropped at intake.
- Per-host config: `name`, `group_tag`, `enabled`, `dpi_mode`, `expected_type` +
  mismatch detection.
- DPI manager evolves from single global hot-swap to **concurrent multi-mode**; the
  per-source `dpi_mode` governs which DPI metadata is preferred for that source.
- Live per-source stats (flows/s, bytes, last_seen, status).
- Source REST APIs (list, detail, update config).

**Out of scope:**
- The Sources management UI (sub-project C).
- Manual pre-registration / approval workflow (auto-discovery defaults to enabled).
- Per-source data retention policies.
- Credentialed/authenticated exporters (IPFIX templates, sFlow) — only the existing
  NetFlow v9 / TZSP / Suricata inputs.

---

## Architecture

```
NetFlow pkt ─┐                         ┌─ SourceRegistry (Postgres + cache) ─┐
TZSP pkt    ─┼─► Observe(addr, type) ──┤  upsert source, last_seen, counters │
Suricata    ─┘        │                │  returns { enabled, dpi_mode }      │
                      ▼                └─────────────────────────────────────┘
              enabled?  ──no──► drop (gate)
                      │ yes
                      ▼
            stamp flow.source = addr ──► enrichment ──► ClickHouse (source column)
                                              ▲
                              DPI manager (multi-mode): runs the union of
                              capture mechanisms any source requests; the
                              correlation cache is shared; per-source dpi_mode
                              selects which metadata is applied.
```

---

## Components

### 1. Source identity & data model

**Identity key:** `(address, type)`.
- NetFlow / TZSP → `address` is the exporter's source IP (from the UDP packet).
- Suricata → a fixed local sensor source (`address = "127.0.0.1"`, `type = "suricata"`).

**`type`** ∈ `{ netflow, tzsp, suricata }`.

**Postgres `sources` table** (added to `db/postgres/init-db.sql`):
```sql
CREATE TABLE IF NOT EXISTS sources (
    id            SERIAL PRIMARY KEY,
    address       TEXT NOT NULL,
    type          TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    group_tag     TEXT NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    dpi_mode      TEXT NOT NULL DEFAULT 'auto',   -- auto | suricata | tzsp | none
    expected_type TEXT NOT NULL DEFAULT '',       -- '' = no expectation
    first_seen    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (address, type)
);
```

**ClickHouse:** add `source String` to `network_flows` and `suricata_alerts`
(`db/clickhouse/init-db.sql`). For existing deployments, an `ALTER TABLE ... ADD
COLUMN IF NOT EXISTS source String` is documented in the migration note. The batch
writer includes `source` in its INSERT.

### 2. SourceRegistry (`backend/internal/sources/`)

| File | Responsibility |
|------|----------------|
| `registry.go` | In-memory cache of sources keyed by `(address,type)` over the Postgres repo. `Observe(addr, type) (Decision)` is called on every intake: upserts the source if new (enabled, dpi_mode=auto), refreshes `last_seen`, bumps live counters, flags an `expected_type` mismatch, and returns `{ Enabled bool, DPIMode string }`. Thread-safe. |
| `repository.go` | Postgres CRUD: `Upsert`, `List`, `Get`, `UpdateConfig`. |
| `stats.go` | In-memory rolling counters per source: flows in the last second (rate), total bytes, last_seen. Read by the API for the live view. |

`Observe` is hot-path; it must be lock-cheap (RWMutex + periodic async flush of
`last_seen`/counters to Postgres, e.g. every 10s, rather than a write per packet).

**Auto-discovery default:** a newly observed source is created `enabled=true`,
`dpi_mode='auto'`, `name=''` (UI falls back to showing the address). The user can
later disable it (→ gate drops its data) or rename it.

**Mismatch detection:** if `expected_type` is set and the observed `type` differs,
the source is flagged (surfaced via a `mismatch` boolean in the API); data is still
ingested unless the source is disabled.

### 3. Ingestion gate & stamping

In the NetFlow and TZSP intake paths (and the Suricata correlator), before a flow
or alert is written:
1. `decision := registry.Observe(addr, type)`
2. If `!decision.Enabled` → drop (increment a dropped counter for visibility).
3. Else stamp `flow.Source = addr` and continue through enrichment → batch writer.

The exporter address is already available at the UDP read in the NetFlow/TZSP
listeners; it is threaded through to the flow record.

### 4. DPI manager: single hot-swap → concurrent multi-mode

Today `DPIManager` runs exactly one of {Suricata correlator, TZSP listener} and
hot-swaps on a global setting. It changes to:
- Run the **union** of capture mechanisms that any enabled source requests via its
  `dpi_mode` (e.g. if any source is `suricata` or `auto`, run the correlator; if any
  is `tzsp` or `auto`, run the TZSP listener). Both feed the **same** shared
  correlation cache.
- During enrichment, a flow looks up its 5-tuple in the cache. The source's
  `dpi_mode` filters what is applied: `none` → skip DPI for that source's flows;
  `suricata`/`tzsp` → only apply metadata originating from that capture mechanism;
  `auto` → apply whatever the cache holds.
- To support `suricata`/`tzsp` filtering, cache entries are tagged with the
  capture mechanism that produced them.

When the set of requested modes changes (a source's `dpi_mode` is edited), the
manager starts/stops capture mechanisms to match the new union — reusing the
existing start/stop machinery, now driven by the union instead of a single value.

The legacy global `dpi_mode` setting in the `settings` table is superseded by
per-source `dpi_mode`; the old global setting is removed.

### 5. Source REST APIs (`backend/internal/api/sources.go`, part of B2's router)

```
GET /api/sources
  → [ { id, address, type, name, group_tag, enabled, dpi_mode, expected_type,
        mismatch, status, flows_per_sec, total_bytes, first_seen, last_seen } ]
  // status ∈ active | silent | disabled  (silent = enabled but no data for >N min)

GET /api/sources/:id
  → same shape + transport detail (e.g. "UDP :2055") and recent counters

PATCH /api/sources/:id   { name?, group_tag?, enabled?, dpi_mode?, expected_type? }
  → 200 updated source   (400 on invalid dpi_mode/expected_type)
```

These endpoints live behind the same JWT auth middleware as the rest of `/api`
(defined in B2). Listed here because they belong to the sources feature; they are
implemented alongside B2's router.

---

## Data Flow

1. A NetFlow packet arrives from `172.16.10.1` → `registry.Observe("172.16.10.1",
   "netflow")` upserts the source (first time), returns `enabled=true,
   dpi_mode=auto`.
2. Flow is stamped `source="172.16.10.1"`, enriched (GeoIP + DPI per dpi_mode),
   written to ClickHouse with the `source` column.
3. The user opens Sources → `GET /api/sources` shows the host with live
   `flows_per_sec` and `status=active`.
4. The user disables a noisy host → `PATCH /api/sources/:id {enabled:false}` →
   subsequent `Observe` returns `enabled=false` → its flows are dropped at intake.

---

## Error Handling

- Invalid `dpi_mode` or `expected_type` on PATCH → `400` with clear message.
- Unknown source id → `404`.
- Postgres unavailable at intake → registry serves from in-memory cache and logs;
  intake never blocks on the DB (fail-open to avoid dropping all telemetry).
- A disabled source's dropped flows are counted, not errored.

---

## Testing (TDD)

- **registry:** `Observe` creates a source once, refreshes last_seen, returns the
  stored enabled/dpi_mode; concurrent `Observe` calls are race-free (run with `-race`).
- **gate:** disabled source → flow dropped + counter incremented; enabled → stamped.
- **mismatch:** `expected_type` set and differing observed type → `mismatch=true`.
- **multi-mode DPI:** union computed from a set of per-source modes; `none` skips
  enrichment; `suricata`/`tzsp` only apply matching-tagged cache entries.
- **API:** list/detail/update against a fake registry (same interface pattern as the
  existing `alertWriter`); invalid dpi_mode → 400.
- **stats:** rolling per-second rate computes correctly across a tick boundary.

---

## Files Changed

| File | Change |
|------|--------|
| `backend/internal/sources/{registry,repository,stats}.go` | New — sources package. |
| `db/postgres/init-db.sql` | Add `sources` table. |
| `db/clickhouse/init-db.sql` | Add `source String` to `network_flows` + `suricata_alerts`. |
| `backend/internal/storage/batch_writer.go` | Include `source` in INSERT. |
| `backend/internal/processor/types.go` | Add `Source` field to the flow type. |
| `backend/internal/collector/netflow.go`, `tzsp.go` | Thread exporter addr; call gate; stamp source. |
| `backend/internal/collector/suricata_correlator.go` | Local sensor source + gate. |
| `backend/internal/collector/dpi_manager.go` | Single hot-swap → concurrent multi-mode driven by per-source modes. |
| `backend/internal/processor/correlation.go` | Tag cache entries with capture mechanism. |
| `backend/cmd/server/main.go` | Construct registry; wire into intake + DPI manager. Remove global dpi_mode setting usage. |

Test files alongside each new/changed source file.

---

## Migration Note

Fresh installs get the `source` column and `sources` table from the init scripts.
Existing ClickHouse deployments need:
```sql
ALTER TABLE fluxio.network_flows  ADD COLUMN IF NOT EXISTS source String;
ALTER TABLE fluxio.suricata_alerts ADD COLUMN IF NOT EXISTS source String;
```
This will be added to a short `docs/migrations` note and referenced from the README.

---

## Non-Goals

- No manual approval workflow (auto-discovery defaults to enabled).
- No new export protocols (sFlow, IPFIX-with-options) in this iteration.
- No per-source retention or alerting thresholds yet.
