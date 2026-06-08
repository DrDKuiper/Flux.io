# Backend Data Pipeline — Design

Date: 2026-06-08
Status: Approved for planning

## Context

Flux.io is an early-stage network observability + security platform, positioned
similarly to Akvorado (NetFlow collection/visualization) and SELKS (Suricata IDS
+ SIEM forwarding). The current backend is an architectural skeleton: the
NetFlow listener, DPI extraction, GeoIP/ASN enrichment, and Wazuh forwarder are
all stubs that log messages but do not decode, enrich, or persist real data.
The ClickHouse schema (`network_flows`, `suricata_alerts`) is already well
designed and ready to receive real data.

This spec covers building the **complete backend data pipeline**: from raw
NetFlow/IPFIX packets on the wire to enriched records persisted in ClickHouse,
including DPI-based application identification and Suricata alert forwarding.

## Goals

- Decode real NetFlow v9 / IPFIX packets (via `goflow2`) into normalized
  `FlowRecord`s and persist them in ClickHouse.
- Enrich flows with real GeoIP/ASN data (MaxMind GeoLite2), replacing the
  hardcoded stub values.
- Identify L7 applications (DPI) using **either** of two configurable sources:
  correlating with Suricata's `eve.json` events, or capturing raw packets via
  TZSP and parsing them directly. The active mode is switchable from the UI.
- Parse Suricata alerts from `eve.json`, persist them to ClickHouse, and
  forward them to an external Wazuh manager via syslog.

## Architecture / Data Flow

```
NetFlow/IPFIX (UDP :2055) ──► decoder (goflow2) ──► FlowRecord
                                                         │
Suricata eve.json ──► tailer/parser ──┬──► alerts ──► ClickHouse (suricata_alerts)
                                       │                    │
                                       │                    └──► Wazuh Forwarder (syslog)
                                       │
                                       └──► 5-tuple cache (SNI/DNS/HTTP) ──┐
                                                                            │
TZSP (UDP :37008, alternate mode) ──► gopacket ──► SNI/DNS ────────────────┤
                                                                            ▼
                                                            FlowRecord enriched
                                                       (GeoIP/ASN + DPI via 5-tuple)
                                                                            │
                                                                            ▼
                                                          Batch Writer ──► ClickHouse (network_flows)
```

The central idea: a **5-tuple correlation cache** (src IP, dst IP, src port,
dst port, protocol) is where NetFlow data ("how much traffic") meets DPI data
("what kind of traffic"). Both DPI sources (Suricata correlation and TZSP
capture) feed the same cache — only the source changes, the rest of the
pipeline stays identical regardless of which mode is active.

## Components

### 1. NetFlow Collector (`internal/collector/netflow.go`)

Replace the stub with real decoding using `goflow2`'s NetFlow v9/IPFIX
decoders. Decoded records are normalized into `FlowRecord` and pushed onto a Go
channel, decoupling UDP reception (which must never block) from processing.

### 2. 5-tuple correlation cache (`internal/processor/correlation.go`, new)

An in-memory map `(srcIP, dstIP, srcPort, dstPort, proto) → {SNI, DNSQuery,
HTTPHost, expiresAt}` with a short TTL (~30s) and a periodic cleanup goroutine
to bound memory growth. It is populated by exactly one of two sources,
depending on the active configuration:

- **Suricata mode**: reuses the "tail -f" logic currently embedded in
  `wazuh_forwarder.go`, extracted into a shared `filetailer` helper. Parses
  `tls`, `dns`, `http`, and `flow` events from `eve.json` and populates the
  cache keyed by 5-tuple.
- **TZSP mode**: a new UDP listener on port 37008 (already reserved in
  `docker-compose.yml`) that decapsulates TZSP frames and uses `gopacket` to
  extract the SNI from TLS ClientHello messages and query names from DNS
  packets, feeding the same cache.

When a `FlowRecord` is processed, its 5-tuple is looked up in the cache; a hit
attaches `Application`/`SNI`/etc. to the record before it is written.

### 3. GeoIP/ASN enrichment (`internal/processor/enrichment.go`)

Replace `lookupCountry`/`lookupASN` stubs with real lookups against **MaxMind
GeoLite2** databases (`GeoLite2-City.mmdb`, `GeoLite2-ASN.mmdb`) loaded via
`oschwald/geoip2-golang`. Databases are mounted into the container via a Docker
volume (README documents how to obtain them with a free MaxMind license key).
If the files are missing at startup, enrichment logs a warning and returns
empty fields — it never crashes the service.

### 4. Configurable DPI mode (Settings)

- New `settings` table in Postgres (key/value), holding `dpi_mode = suricata |
  tzsp | none`.
- New backend endpoints `GET /api/settings` and `PUT /api/settings`.
- New **Settings** page in the frontend with a selector for the DPI mode,
  persisted via the API.
- Switching modes hot-swaps the active listener (Suricata tailer vs. TZSP
  listener) at runtime — no backend restart required.

### 5. Batch writer → ClickHouse (`internal/storage/clickhouse.go`, new)

Accumulates enriched `FlowRecord`s into batches (by size or time, e.g. 1000
records or every 5s) and inserts them via `clickhouse-go/v2` into
`network_flows`. On write failure, retries with exponential backoff; if the
in-memory buffer exceeds a cap, the oldest records are dropped with a warning
log to bound memory usage.

### 6. Suricata alerts → ClickHouse + Wazuh forwarder

- `alert` events from `eve.json` (read by the same tailer as item 2) are
  parsed and written to `suricata_alerts`.
- `wazuh_forwarder.go` is fixed and wired into `main.go` (currently it is never
  started): the syslog message format is corrected (RFC 3164), and the
  existing `log.Fatalf` on opening `eve.json` becomes a non-fatal retry loop —
  so the backend doesn't crash if Suricata hasn't created the file yet.

## Error Handling

- Malformed NetFlow packets: logged and dropped; the listener keeps running.
- ClickHouse unavailable: exponential backoff retries; bounded buffer drops
  oldest records (with warning) if the outage persists.
- Missing GeoIP databases: enrichment becomes a no-op with a startup warning,
  never fatal.
- `eve.json` not yet present / Wazuh manager unreachable: retry loop (extending
  the pattern that already exists for the Wazuh connection to the file tailer
  too) — never `log.Fatalf`.
- Correlation cache: bounded via TTL + periodic cleanup goroutine.

## Testing Strategy

- Unit tests: `FlowRecord` normalization, 5-tuple cache match/expiry logic,
  `eve.json` event parsing, Wazuh syslog message formatting.
- GeoIP enrichment: tested against a small test `.mmdb` fixture (or a mockable
  interface), so CI doesn't depend on a MaxMind license.
- Batch writer: tested via a mockable `Inserter` interface, no real ClickHouse
  required for unit tests.
- Settings API: basic CRUD tests (GET/PUT) against a test Postgres or a mocked
  repository.

## Out of Scope (future specs)

- Real-time alert push to the frontend dashboard (WebSocket wiring) — part of
  a future "real-time alerts" spec.
- Dashboard/visualization work consuming this pipeline's data.
- Authentication/authorization overhaul (JWT, Argon2id, RBAC).
