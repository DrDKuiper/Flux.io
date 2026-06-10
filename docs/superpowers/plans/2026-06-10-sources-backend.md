# Sources Backend (B1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Treat telemetry as coming from multiple auto-discovered sources (hosts/sensors), stamp every flow/alert with its source, gate ingestion per source, and let each source carry its own DPI mode — replacing the single global DPI hot-swap with concurrent multi-mode capture.

**Architecture:** A `SourceRegistry` (Postgres-backed, in-memory cached) is consulted on every intake via `Observe(addr, type)`, which auto-creates sources, refreshes liveness, and returns an enable/DPI decision. Flows are stamped with their source and written to a new ClickHouse `source` column. The `DPIManager` is reworked to run the union of capture mechanisms any enabled source requests; the correlation cache tags entries by mechanism so per-source `dpi_mode` can select which metadata applies.

**Tech Stack:** Go 1.22, PostgreSQL 16 (`lib/pq`), ClickHouse 24.3 (`clickhouse-go/v2`), Fiber v2.

**Spec:** `docs/superpowers/specs/2026-06-10-sources-backend-design.md`

**Verification note:** This environment has no local Go toolchain. Each task's test commands (`go test ...`) are run by the executor/human with Go 1.22+, or inside the backend Docker build. Do NOT hand-edit `go.sum`. Run `go test ./... -race` for packages with concurrency.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `db/postgres/init-db.sql` | Add `sources` table (modify). |
| `db/clickhouse/init-db.sql` | Add `source String` column to `network_flows` + `suricata_alerts` (modify). |
| `backend/internal/sources/source.go` | `Source` struct + `Decision` + valid dpi_mode/type constants (create). |
| `backend/internal/sources/repository.go` | Postgres CRUD: `Upsert`, `List`, `Get`, `UpdateConfig` (create). |
| `backend/internal/sources/registry.go` | In-memory cache + `Observe` hot path + reconcile inputs (create). |
| `backend/internal/sources/stats.go` | Rolling per-source flow-rate/byte counters (create). |
| `backend/internal/processor/types.go` | Add `Source` field to `FlowRecord` (modify). |
| `backend/internal/processor/correlation.go` | Tag cache entries by mechanism; add `GetForMode` (modify). |
| `backend/internal/storage/clickhouse.go` | Add `source` to flow + alert INSERT (modify). |
| `backend/internal/collector/netflow.go` | Thread exporter addr; gate + stamp (modify). |
| `backend/internal/collector/dpi_manager.go` | Single hot-swap → concurrent multi-mode reconcile (modify). |
| `backend/cmd/server/main.go` | Construct registry; wire gate, stamping, reconcile loop, per-source DPI application (modify). |

The source REST endpoints (`GET/PATCH /api/sources`) belong to sub-project **B2** (they live in the API router built there); B1 only exposes the registry/repository methods they will call.

---

## Task 1: Database schema — sources table + source column

**Files:**
- Modify: `db/postgres/init-db.sql`
- Modify: `db/clickhouse/init-db.sql`
- Create: `docs/migrations/2026-06-10-add-source.md`

- [ ] **Step 1: Add the `sources` table to Postgres init**

Append to `db/postgres/init-db.sql`:

```sql
CREATE TABLE IF NOT EXISTS sources (
    id            SERIAL PRIMARY KEY,
    address       TEXT NOT NULL,
    type          TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    group_tag     TEXT NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    dpi_mode      TEXT NOT NULL DEFAULT 'auto',
    expected_type TEXT NOT NULL DEFAULT '',
    first_seen    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (address, type)
);
```

- [ ] **Step 2: Add the `source` column to both ClickHouse tables**

In `db/clickhouse/init-db.sql`, add `source String` as the first column after `timestamp` in **both** `network_flows` and `suricata_alerts` `CREATE TABLE` statements. For `network_flows`, insert after the `timestamp` line:

```sql
    timestamp DateTime64(3, 'UTC'),
    source String,
```

Do the same in `suricata_alerts`:

```sql
    timestamp DateTime64(3, 'UTC'),
    source String,
```

- [ ] **Step 3: Write the migration note for existing deployments**

Create `docs/migrations/2026-06-10-add-source.md`:

```markdown
# Migration: add `source` column (2026-06-10)

Fresh installs get this from the init scripts. Existing ClickHouse deployments must run:

\`\`\`sql
ALTER TABLE fluxio.network_flows   ADD COLUMN IF NOT EXISTS source String;
ALTER TABLE fluxio.suricata_alerts ADD COLUMN IF NOT EXISTS source String;
\`\`\`

Postgres gets the `sources` table automatically via `CREATE TABLE IF NOT EXISTS`
on container start (the init script runs only on first boot, so for an existing
Postgres volume run the `CREATE TABLE` from `db/postgres/init-db.sql` manually).
```

- [ ] **Step 4: Commit**

```bash
git add db/postgres/init-db.sql db/clickhouse/init-db.sql docs/migrations/2026-06-10-add-source.md
git commit -m "feat(sources): add sources table and source column to schemas"
```

---

## Task 2: Source type and constants

**Files:**
- Create: `backend/internal/sources/source.go`
- Test: `backend/internal/sources/source_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/sources/source_test.go`:

```go
package sources

import "testing"

func TestValidDPIMode(t *testing.T) {
	for _, m := range []string{"auto", "suricata", "tzsp", "none"} {
		if !ValidDPIMode(m) {
			t.Errorf("expected %q to be valid", m)
		}
	}
	if ValidDPIMode("bogus") {
		t.Error("expected \"bogus\" to be invalid")
	}
}

func TestValidType(t *testing.T) {
	for _, ty := range []string{"netflow", "tzsp", "suricata"} {
		if !ValidType(ty) {
			t.Errorf("expected %q to be valid", ty)
		}
	}
	if ValidType("") || ValidType("sflow") {
		t.Error("expected empty/sflow to be invalid")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/sources/ -run TestValid -v`
Expected: FAIL (undefined: ValidDPIMode, ValidType).

- [ ] **Step 3: Write the implementation**

Create `backend/internal/sources/source.go`:

```go
// Package sources tracks the hosts/sensors that send telemetry to Flux.io.
// Sources are auto-discovered by their origin address; each carries per-host
// configuration (name, group, enabled, DPI mode) and live status.
package sources

import "time"

// Source is one telemetry origin (a NetFlow/TZSP exporter or the Suricata sensor).
type Source struct {
	ID           int       `json:"id"`
	Address      string    `json:"address"`
	Type         string    `json:"type"`
	Name         string    `json:"name"`
	GroupTag     string    `json:"group_tag"`
	Enabled      bool      `json:"enabled"`
	DPIMode      string    `json:"dpi_mode"`
	ExpectedType string    `json:"expected_type"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
}

// Decision is what Observe returns to the intake hot path.
type Decision struct {
	Enabled bool
	DPIMode string
}

var validDPIModes = map[string]bool{"auto": true, "suricata": true, "tzsp": true, "none": true}
var validTypes = map[string]bool{"netflow": true, "tzsp": true, "suricata": true}

// ValidDPIMode reports whether mode is one the system can honor.
func ValidDPIMode(mode string) bool { return validDPIModes[mode] }

// ValidType reports whether ty is a recognized source type.
func ValidType(ty string) bool { return validTypes[ty] }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/sources/ -run TestValid -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/sources/source.go backend/internal/sources/source_test.go
git commit -m "feat(sources): add Source type and validation helpers"
```

---

## Task 3: Postgres repository

**Files:**
- Create: `backend/internal/sources/repository.go`
- Test: `backend/internal/sources/repository_test.go`

The repository wraps `*sql.DB`. Tests use a fake at the registry layer (Task 4); the repository itself is exercised against a real Postgres in integration only, so here we test the one piece of pure logic — the `UpdateConfig` field validation — and keep SQL methods thin.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/sources/repository_test.go`:

```go
package sources

import "testing"

func TestValidateConfigPatch(t *testing.T) {
	tests := []struct {
		name    string
		patch   ConfigPatch
		wantErr bool
	}{
		{"valid full", ConfigPatch{DPIMode: ptr("suricata"), ExpectedType: ptr("netflow")}, false},
		{"empty expected ok", ConfigPatch{ExpectedType: ptr("")}, false},
		{"bad dpi", ConfigPatch{DPIMode: ptr("nope")}, true},
		{"bad expected", ConfigPatch{ExpectedType: ptr("sflow")}, true},
		{"nothing set", ConfigPatch{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.patch.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func ptr(s string) *string { return &s }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/sources/ -run TestValidateConfigPatch -v`
Expected: FAIL (undefined: ConfigPatch).

- [ ] **Step 3: Write the implementation**

Create `backend/internal/sources/repository.go`:

```go
package sources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ConfigPatch is a partial update to a source's editable fields. A nil pointer
// means "leave unchanged"; a non-nil pointer (including empty string) sets the value.
type ConfigPatch struct {
	Name         *string
	GroupTag     *string
	Enabled      *bool
	DPIMode      *string
	ExpectedType *string
}

// Validate rejects out-of-range enum values before they reach the database.
func (p ConfigPatch) Validate() error {
	if p.DPIMode != nil && !ValidDPIMode(*p.DPIMode) {
		return fmt.Errorf("invalid dpi_mode %q", *p.DPIMode)
	}
	if p.ExpectedType != nil && *p.ExpectedType != "" && !ValidType(*p.ExpectedType) {
		return fmt.Errorf("invalid expected_type %q", *p.ExpectedType)
	}
	return nil
}

// Repository persists sources in Postgres.
type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

// Upsert inserts a source if (address,type) is new, otherwise refreshes last_seen.
// It returns the resulting row so the caller learns the stored config.
func (r *Repository) Upsert(ctx context.Context, address, typ string, seen time.Time) (Source, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO sources (address, type, first_seen, last_seen)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (address, type) DO UPDATE SET last_seen = EXCLUDED.last_seen
		RETURNING id, address, type, name, group_tag, enabled, dpi_mode, expected_type, first_seen, last_seen`,
		address, typ, seen)
	return scanSource(row)
}

// List returns all sources ordered by group then address.
func (r *Repository) List(ctx context.Context) ([]Source, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, address, type, name, group_tag, enabled, dpi_mode, expected_type, first_seen, last_seen
		FROM sources ORDER BY group_tag, address`)
	if err != nil {
		return nil, fmt.Errorf("sources: list: %w", err)
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Get returns a single source by id, or sql.ErrNoRows if absent.
func (r *Repository) Get(ctx context.Context, id int) (Source, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, address, type, name, group_tag, enabled, dpi_mode, expected_type, first_seen, last_seen
		FROM sources WHERE id = $1`, id)
	return scanSource(row)
}

// UpdateConfig applies a validated partial update and returns the updated row.
func (r *Repository) UpdateConfig(ctx context.Context, id int, p ConfigPatch) (Source, error) {
	if err := p.Validate(); err != nil {
		return Source{}, err
	}
	sets := []string{}
	args := []any{}
	i := 1
	add := func(col string, val any) { sets = append(sets, fmt.Sprintf("%s = $%d", col, i)); args = append(args, val); i++ }
	if p.Name != nil {
		add("name", *p.Name)
	}
	if p.GroupTag != nil {
		add("group_tag", *p.GroupTag)
	}
	if p.Enabled != nil {
		add("enabled", *p.Enabled)
	}
	if p.DPIMode != nil {
		add("dpi_mode", *p.DPIMode)
	}
	if p.ExpectedType != nil {
		add("expected_type", *p.ExpectedType)
	}
	if len(sets) == 0 {
		return r.Get(ctx, id)
	}
	args = append(args, id)
	query := fmt.Sprintf(`UPDATE sources SET %s WHERE id = $%d
		RETURNING id, address, type, name, group_tag, enabled, dpi_mode, expected_type, first_seen, last_seen`,
		strings.Join(sets, ", "), i)
	row := r.db.QueryRowContext(ctx, query, args...)
	return scanSource(row)
}

type scanner interface{ Scan(dest ...any) error }

func scanSource(s scanner) (Source, error) {
	var src Source
	err := s.Scan(&src.ID, &src.Address, &src.Type, &src.Name, &src.GroupTag,
		&src.Enabled, &src.DPIMode, &src.ExpectedType, &src.FirstSeen, &src.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, err
	}
	if err != nil {
		return Source{}, fmt.Errorf("sources: scan: %w", err)
	}
	return src, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/sources/ -run TestValidateConfigPatch -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/sources/repository.go backend/internal/sources/repository_test.go
git commit -m "feat(sources): add Postgres repository with upsert/list/get/update"
```

---

## Task 4: SourceRegistry — Observe hot path

**Files:**
- Create: `backend/internal/sources/registry.go`
- Test: `backend/internal/sources/registry_test.go`

The registry caches sources in memory and serves `Observe` without a DB round-trip per packet. New sources are created via the repository; config edits (from B2) call the repository and refresh the cache.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/sources/registry_test.go`:

```go
package sources

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory stand-in for the Postgres repository.
type fakeStore struct {
	mu    sync.Mutex
	byKey map[string]Source
	seq   int
}

func newFakeStore() *fakeStore { return &fakeStore{byKey: map[string]Source{}} }

func (f *fakeStore) Upsert(_ context.Context, address, typ string, seen time.Time) (Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := address + "|" + typ
	if s, ok := f.byKey[k]; ok {
		s.LastSeen = seen
		f.byKey[k] = s
		return s, nil
	}
	f.seq++
	s := Source{ID: f.seq, Address: address, Type: typ, Enabled: true, DPIMode: "auto", FirstSeen: seen, LastSeen: seen}
	f.byKey[k] = s
	return s, nil
}

func (f *fakeStore) List(context.Context) ([]Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Source, 0, len(f.byKey))
	for _, s := range f.byKey {
		out = append(out, s)
	}
	return out, nil
}

func TestObserveCreatesOnceAndDecides(t *testing.T) {
	reg := NewRegistry(newFakeStore())
	ctx := context.Background()

	d1 := reg.Observe(ctx, "10.0.0.1", "netflow")
	if !d1.Enabled || d1.DPIMode != "auto" {
		t.Fatalf("new source should be enabled+auto, got %+v", d1)
	}
	reg.Observe(ctx, "10.0.0.1", "netflow")

	if got := len(reg.snapshot()); got != 1 {
		t.Fatalf("expected 1 cached source, got %d", got)
	}
}

func TestObserveRespectsDisabled(t *testing.T) {
	reg := NewRegistry(newFakeStore())
	ctx := context.Background()
	reg.Observe(ctx, "10.0.0.9", "netflow")
	reg.setEnabledForTest("10.0.0.9", "netflow", false)

	if reg.Observe(ctx, "10.0.0.9", "netflow").Enabled {
		t.Fatal("disabled source must report Enabled=false")
	}
}

func TestRequestedMechanisms(t *testing.T) {
	reg := NewRegistry(newFakeStore())
	ctx := context.Background()
	reg.Observe(ctx, "a", "netflow") // auto -> wants suricata+tzsp
	suri, tzsp := reg.RequestedMechanisms()
	if !suri || !tzsp {
		t.Fatalf("auto source should request both, got suri=%v tzsp=%v", suri, tzsp)
	}
	reg.setDPIModeForTest("a", "netflow", "none")
	suri, tzsp = reg.RequestedMechanisms()
	if suri || tzsp {
		t.Fatalf("none source should request neither, got suri=%v tzsp=%v", suri, tzsp)
	}
}

func TestObserveRaceFree(t *testing.T) {
	reg := NewRegistry(newFakeStore())
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); reg.Observe(ctx, "10.0.0.1", "netflow") }()
	}
	wg.Wait()
	if got := len(reg.snapshot()); got != 1 {
		t.Fatalf("concurrent Observe of same key should yield 1 source, got %d", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/sources/ -run TestObserve -race -v`
Expected: FAIL (undefined: NewRegistry).

- [ ] **Step 3: Write the implementation**

Create `backend/internal/sources/registry.go`:

```go
package sources

import (
	"context"
	"log"
	"sync"
	"time"
)

// store is the subset of *Repository the registry needs. Defined as an
// interface so tests can substitute an in-memory fake.
type store interface {
	Upsert(ctx context.Context, address, typ string, seen time.Time) (Source, error)
	List(ctx context.Context) ([]Source, error)
}

// Registry caches sources in memory and serves the Observe hot path without a
// DB round-trip per packet. last_seen is flushed lazily by Upsert; config edits
// go through the repository and refresh the cache via Refresh.
type Registry struct {
	store store

	mu      sync.RWMutex
	byKey   map[string]Source // key = address|type
	dropped uint64
}

func key(address, typ string) string { return address + "|" + typ }

// NewRegistry builds an empty registry over store. Call Load once at startup to
// warm the cache from the database.
func NewRegistry(s store) *Registry {
	return &Registry{store: s, byKey: make(map[string]Source)}
}

// Load warms the cache from the database (call once at startup).
func (r *Registry) Load(ctx context.Context) error {
	list, err := r.store.List(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range list {
		r.byKey[key(s.Address, s.Type)] = s
	}
	return nil
}

// Observe is called on every intake. It returns the enable/DPI decision for the
// source, auto-creating it (enabled, dpi_mode=auto) the first time it is seen.
// The DB upsert happens only for a new key or when last_seen is stale (>10s), so
// the hot path is a cheap cache read in the common case.
func (r *Registry) Observe(ctx context.Context, address, typ string) Decision {
	k := key(address, typ)
	now := time.Now().UTC()

	r.mu.RLock()
	cached, ok := r.byKey[k]
	r.mu.RUnlock()

	if ok && now.Sub(cached.LastSeen) < 10*time.Second {
		return Decision{Enabled: cached.Enabled, DPIMode: cached.DPIMode}
	}

	// New key, or last_seen is stale enough to persist a refresh.
	s, err := r.store.Upsert(ctx, address, typ, now)
	if err != nil {
		// Fail open: never block telemetry on a DB hiccup. Use cached value if any.
		if ok {
			return Decision{Enabled: cached.Enabled, DPIMode: cached.DPIMode}
		}
		log.Printf("sources: upsert %s/%s failed, accepting by default: %v", address, typ, err)
		return Decision{Enabled: true, DPIMode: "auto"}
	}

	r.mu.Lock()
	r.byKey[k] = s
	r.mu.Unlock()
	return Decision{Enabled: s.Enabled, DPIMode: s.DPIMode}
}

// Refresh replaces a cached source after a config edit (called by B2's handler).
func (r *Registry) Refresh(s Source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byKey[key(s.Address, s.Type)] = s
}

// RequestedMechanisms reports which capture mechanisms the DPI manager should
// run: the union over all enabled sources. dpi_mode "auto" requests both;
// "suricata"/"tzsp" request that one; "none" requests neither.
func (r *Registry) RequestedMechanisms() (suricata, tzsp bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.byKey {
		if !s.Enabled {
			continue
		}
		switch s.DPIMode {
		case "auto":
			suricata, tzsp = true, true
		case "suricata":
			suricata = true
		case "tzsp":
			tzsp = true
		}
	}
	return suricata, tzsp
}

// --- test helpers (compiled in all builds; harmless, used only by tests) ---

func (r *Registry) snapshot() []Source {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Source, 0, len(r.byKey))
	for _, s := range r.byKey {
		out = append(out, s)
	}
	return out
}

func (r *Registry) setEnabledForTest(address, typ string, enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key(address, typ)
	s := r.byKey[k]
	s.Enabled = enabled
	r.byKey[k] = s
}

func (r *Registry) setDPIModeForTest(address, typ, mode string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := key(address, typ)
	s := r.byKey[k]
	s.DPIMode = mode
	r.byKey[k] = s
}
```

Note on the disabled test: `setEnabledForTest` mutates the cache directly, but `Observe` re-upserts when `last_seen` is stale (>10s). Since the test calls `Observe` immediately after the first observe, `last_seen` is fresh (<10s), so `Observe` returns the cached (now-disabled) value without hitting the store — exactly what we assert.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/sources/ -race -v`
Expected: PASS (all registry + earlier tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/sources/registry.go backend/internal/sources/registry_test.go
git commit -m "feat(sources): add SourceRegistry with Observe hot path and mechanism union"
```

---

## Task 5: Per-source live stats

**Files:**
- Create: `backend/internal/sources/stats.go`
- Test: `backend/internal/sources/stats_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/sources/stats_test.go`:

```go
package sources

import "testing"

func TestStatsRateAndTotals(t *testing.T) {
	s := NewStats()
	s.Record("10.0.0.1", 100)
	s.Record("10.0.0.1", 200)
	s.Record("10.0.0.2", 50)

	// Before any Roll, the current second holds the counts.
	if got := s.Snapshot("10.0.0.1"); got.TotalBytes != 300 || got.WindowFlows != 2 {
		t.Fatalf("10.0.0.1 snapshot wrong: %+v", got)
	}

	// Roll moves the current second into the "per-second rate" reading.
	s.Roll()
	if got := s.Snapshot("10.0.0.1"); got.FlowsPerSec != 2 {
		t.Fatalf("expected rate 2 after roll, got %d", got.FlowsPerSec)
	}
	// Totals persist across rolls.
	if got := s.Snapshot("10.0.0.1"); got.TotalBytes != 300 {
		t.Fatalf("totals should persist, got %d", got.TotalBytes)
	}
	// A second roll with no new flows drops the rate to 0.
	s.Roll()
	if got := s.Snapshot("10.0.0.1"); got.FlowsPerSec != 0 {
		t.Fatalf("expected rate 0 after idle roll, got %d", got.FlowsPerSec)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/sources/ -run TestStats -v`
Expected: FAIL (undefined: NewStats).

- [ ] **Step 3: Write the implementation**

Create `backend/internal/sources/stats.go`:

```go
package sources

import "sync"

// StatSnapshot is the live view for one source.
type StatSnapshot struct {
	FlowsPerSec uint64 // flows counted in the last completed 1s window
	WindowFlows uint64 // flows counted in the in-progress window
	TotalBytes  uint64 // cumulative bytes since process start
	TotalFlows  uint64 // cumulative flows since process start
}

type statCounter struct {
	curFlows  uint64 // flows in the in-progress second
	rateFlows uint64 // flows in the last completed second
	totBytes  uint64
	totFlows  uint64
}

// Stats holds rolling per-source counters. Record is called on the intake hot
// path; Roll is called once per second by a ticker to advance the rate window.
type Stats struct {
	mu sync.Mutex
	by map[string]*statCounter
}

func NewStats() *Stats { return &Stats{by: make(map[string]*statCounter)} }

// Record counts one flow of the given byte size for address.
func (s *Stats) Record(address string, bytes uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.by[address]
	if c == nil {
		c = &statCounter{}
		s.by[address] = c
	}
	c.curFlows++
	c.totFlows++
	c.totBytes += bytes
}

// Roll advances every counter's rate window: the in-progress second becomes the
// reported per-second rate, and a fresh window starts at zero.
func (s *Stats) Roll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.by {
		c.rateFlows = c.curFlows
		c.curFlows = 0
	}
}

// Snapshot returns the live view for address (zero value if unseen).
func (s *Stats) Snapshot(address string) StatSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.by[address]
	if c == nil {
		return StatSnapshot{}
	}
	return StatSnapshot{
		FlowsPerSec: c.rateFlows,
		WindowFlows: c.curFlows,
		TotalBytes:  c.totBytes,
		TotalFlows:  c.totFlows,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/sources/ -run TestStats -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/sources/stats.go backend/internal/sources/stats_test.go
git commit -m "feat(sources): add rolling per-source flow-rate and byte counters"
```

---

## Task 6: Add Source to FlowRecord and persist it

**Files:**
- Modify: `backend/internal/processor/types.go`
- Modify: `backend/internal/storage/clickhouse.go`
- Test: `backend/internal/storage/clickhouse_test.go` (create if absent)

- [ ] **Step 1: Add the `Source` field to FlowRecord**

In `backend/internal/processor/types.go`, add `Source` as the first field after `Timestamp` in `FlowRecord`:

```go
type FlowRecord struct {
	Timestamp       time.Time
	Source          string
	SourceIP        string
	DestinationIP   string
```

- [ ] **Step 2: Write a failing test for the INSERT column list**

Create or append to `backend/internal/storage/clickhouse_test.go`. Since `PrepareBatch` needs a live DB, test the column-list constant instead. Refactor the query into a package var so it is testable:

```go
package storage

import (
	"strings"
	"testing"
)

func TestFlowInsertIncludesSource(t *testing.T) {
	if !strings.Contains(flowInsertSQL, "source") {
		t.Fatal("flow INSERT must include the source column")
	}
	// column count in the list must match the number of Append args (24).
	cols := strings.Count(flowInsertSQL, ",") + 1
	if cols != 24 {
		t.Fatalf("expected 24 columns in flow INSERT, got %d", cols)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/storage/ -run TestFlowInsertIncludesSource -v`
Expected: FAIL (undefined: flowInsertSQL).

- [ ] **Step 4: Implement — extract the SQL and add the column + arg**

In `backend/internal/storage/clickhouse.go`, add a package var and use it; add `source` as the first column after `timestamp` and `r.Source` as the first Append arg after `r.Timestamp`:

```go
var flowInsertSQL = `INSERT INTO network_flows (
	timestamp, source, src_ip, dst_ip, src_port, dst_port, protocol, bytes, packets,
	application_id, sni, http_host, http_url,
	src_country, dst_country, src_asn, dst_asn, src_asn_org, dst_asn_org,
	src_hostname, dst_hostname, is_alert, alert_severity, alert_signature
)`
```

Replace the inline query in `InsertFlows` with `s.conn.PrepareBatch(ctx, flowInsertSQL)`, and update the `batch.Append(...)` call to insert `r.Source` right after `r.Timestamp`:

```go
		err := batch.Append(
			r.Timestamp, r.Source, r.SourceIP, r.DestinationIP, r.SourcePort, r.DestinationPort,
			r.Protocol, r.Bytes, r.Packets,
			r.Application, r.SNI, r.HTTPHost, r.HTTPURL,
			r.SourceCountry, r.DestCountry, r.SourceASN, r.DestASN, r.SourceASNOrg, r.DestASNOrg,
			r.SourceHostname, r.DestHostname, isAlert, r.AlertSeverity, r.AlertSignature,
		)
```

Also add `source` to the alert INSERT. Suricata alerts use the local sensor source `"127.0.0.1"`; the `SuricataAlert` type has no `Source` field, so set it as a constant in the alert column list and Append `"127.0.0.1"`:

```go
var alertInsertSQL = `INSERT INTO suricata_alerts (
	timestamp, source, src_ip, dst_ip, src_port, dst_port, protocol,
	alert_action, alert_gid, alert_signature_id, alert_rev,
	alert_signature, alert_category, alert_severity, payload
)`
```

In `InsertAlerts`, use `alertInsertSQL` and add `"127.0.0.1"` after `a.Timestamp`:

```go
		err := batch.Append(
			a.Timestamp, "127.0.0.1", a.SourceIP, a.DestinationIP, a.SourcePort, a.DestinationPort, a.Protocol,
			a.Action, a.GID, a.SignatureID, a.Rev,
			a.Signature, a.Category, a.Severity, a.Payload,
		)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/storage/ -run TestFlowInsertIncludesSource -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/processor/types.go backend/internal/storage/clickhouse.go backend/internal/storage/clickhouse_test.go
git commit -m "feat(sources): stamp source on flows and persist it to ClickHouse"
```

---

## Task 7: Gate + stamp in the NetFlow listener

**Files:**
- Modify: `backend/internal/collector/netflow.go`
- Test: `backend/internal/collector/netflow_gate_test.go`

The listener currently calls `toFlowRecord(flow, now)` and sends to `out`. We add a gate function the listener consults, and stamp the exporter address.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/collector/netflow_gate_test.go`:

```go
package collector

import (
	"testing"

	"fluxio-backend/internal/processor"
)

func TestApplyGateStampsAndDrops(t *testing.T) {
	rec := processor.FlowRecord{SourceIP: "1.2.3.4"}

	// Enabled gate stamps the exporter address and keeps the record.
	out, keep := applyGate("10.0.0.1", rec, func(addr string) (bool, bool) {
		return true, true // enabled, ignore dpi here
	})
	if !keep {
		t.Fatal("enabled source should be kept")
	}
	if out.Source != "10.0.0.1" {
		t.Fatalf("expected stamped source 10.0.0.1, got %q", out.Source)
	}

	// Disabled gate drops the record.
	_, keep = applyGate("10.0.0.9", rec, func(addr string) (bool, bool) {
		return false, false
	})
	if keep {
		t.Fatal("disabled source should be dropped")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/collector/ -run TestApplyGate -v`
Expected: FAIL (undefined: applyGate).

- [ ] **Step 3: Implement the gate and thread the exporter address**

In `backend/internal/collector/netflow.go`:

Add the gate helper and a `GateFunc` type:

```go
// GateFunc reports whether telemetry from exporter addr should be accepted.
// The bool result is "enabled"; the second return is unused here but keeps the
// signature aligned with the registry Decision (enabled, dpiMode-irrelevant).
type GateFunc func(addr string) (enabled bool, _ bool)

// applyGate stamps rec.Source with the exporter address and returns whether the
// record should be kept, per the gate decision.
func applyGate(addr string, rec processor.FlowRecord, gate GateFunc) (processor.FlowRecord, bool) {
	enabled, _ := gate(addr)
	if !enabled {
		return rec, false
	}
	rec.Source = addr
	return rec, true
}
```

Change `StartNetFlowListener` to accept a `GateFunc` and apply it. The exporter address is `remoteAddr.IP.String()` (strip the port):

```go
func StartNetFlowListener(port string, out chan<- processor.FlowRecord, gate GateFunc) {
	// ... unchanged setup ...
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("netflow: error reading from UDP: %v", err)
			continue
		}
		exporter := remoteAddr.IP.String()

		flows, err := decoder.Decode(remoteAddr.String(), buf[:n])
		if err != nil {
			log.Printf("netflow: dropping malformed packet from %v: %v", remoteAddr, err)
			continue
		}

		now := time.Now().UTC()
		for _, flow := range flows {
			rec, keep := applyGate(exporter, toFlowRecord(flow, now), gate)
			if !keep {
				continue
			}
			out <- rec
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/collector/ -run TestApplyGate -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/collector/netflow.go backend/internal/collector/netflow_gate_test.go
git commit -m "feat(sources): gate and stamp NetFlow records by exporter address"
```

---

## Task 8: Tag correlation cache entries by mechanism

**Files:**
- Modify: `backend/internal/processor/correlation.go`
- Modify: `backend/internal/collector/suricata_correlator.go` (Put call site)
- Modify: `backend/internal/collector/tzsp.go` (Put call site)
- Test: `backend/internal/processor/correlation_mode_test.go`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/processor/correlation_mode_test.go`:

```go
package processor

import (
	"testing"
	"time"
)

func TestGetForModeFiltersByMechanism(t *testing.T) {
	c := NewCorrelationCache(time.Minute)
	tuple := FiveTuple{SrcIP: "a", DstIP: "b", SrcPort: 1, DstPort: 2, Protocol: 6}
	c.Put(tuple, DPIMetadata{Application: "tls"}, "suricata")

	if _, ok := c.GetForMode(tuple, "none"); ok {
		t.Error("mode none must never return metadata")
	}
	if _, ok := c.GetForMode(tuple, "tzsp"); ok {
		t.Error("mode tzsp must not return a suricata-tagged entry")
	}
	if m, ok := c.GetForMode(tuple, "suricata"); !ok || m.Application != "tls" {
		t.Errorf("mode suricata should return the entry, got ok=%v m=%+v", ok, m)
	}
	if m, ok := c.GetForMode(tuple, "auto"); !ok || m.Application != "tls" {
		t.Errorf("mode auto should return any entry, got ok=%v m=%+v", ok, m)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/processor/ -run TestGetForMode -v`
Expected: FAIL (Put takes 2 args; GetForMode undefined).

- [ ] **Step 3: Implement mechanism tagging**

In `backend/internal/processor/correlation.go`, add a `mechanism` field to the entry, change `Put` to accept it, and add `GetForMode`:

```go
type correlationEntry struct {
	metadata  DPIMetadata
	mechanism string // "suricata" or "tzsp" — which capture produced this entry
	expiresAt time.Time
}
```

Change `Put`:

```go
// Put records DPI metadata for a conversation, tagged with the capture
// mechanism that produced it ("suricata" or "tzsp"), resetting its expiry.
func (c *CorrelationCache) Put(key FiveTuple, meta DPIMetadata, mechanism string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = correlationEntry{metadata: meta, mechanism: mechanism, expiresAt: time.Now().Add(c.ttl)}
}
```

Keep the existing `Get` (used nowhere after Task 10, but harmless) OR update it to ignore mechanism. Add `GetForMode`:

```go
// GetForMode returns DPI metadata for a conversation subject to the source's
// dpi_mode: "none" never matches; "auto" matches any entry; "suricata"/"tzsp"
// match only entries produced by that mechanism. Expired entries are a miss.
func (c *CorrelationCache) GetForMode(key FiveTuple, mode string) (DPIMetadata, bool) {
	if mode == "none" {
		return DPIMetadata{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return DPIMetadata{}, false
	}
	if mode != "auto" && entry.mechanism != mode {
		return DPIMetadata{}, false
	}
	return entry.metadata, true
}
```

- [ ] **Step 4: Update the two Put call sites**

In `backend/internal/collector/suricata_correlator.go`, find the `cache.Put(tuple, meta)` call and change it to:

```go
		cache.Put(tuple, meta, "suricata")
```

In `backend/internal/collector/tzsp.go`, find the `cache.Put(...)` call(s) and add the `"tzsp"` mechanism argument:

```go
		cache.Put(tuple, meta, "tzsp")
```

(If `correlation.go` had an existing `Get` used by old tests, leave it; Task 10 removes its only production caller.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/processor/ ./internal/collector/ -v`
Expected: PASS (update any existing test that called the old 2-arg `Put` to pass a mechanism).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/processor/correlation.go backend/internal/collector/suricata_correlator.go backend/internal/collector/tzsp.go backend/internal/processor/correlation_mode_test.go
git commit -m "feat(sources): tag correlation entries by mechanism; add GetForMode"
```

---

## Task 9: DPI manager — concurrent multi-mode reconcile

**Files:**
- Modify: `backend/internal/collector/dpi_manager.go`
- Test: `backend/internal/collector/dpi_manager_test.go` (extend)

The manager changes from "one active mode" to "run the set of mechanisms requested." `Reconcile(ctx, suricata, tzsp bool)` starts/stops each mechanism to match the requested set. It is idempotent: calling it with the same set twice does nothing.

- [ ] **Step 1: Write the failing test**

Add to `backend/internal/collector/dpi_manager_test.go`:

```go
func TestReconcileStartsAndStopsMechanisms(t *testing.T) {
	var mu sync.Mutex
	running := map[string]bool{}
	mk := func(name string) sourceRunFunc {
		return func(ctx context.Context) {
			mu.Lock(); running[name] = true; mu.Unlock()
			<-ctx.Done()
			mu.Lock(); running[name] = false; mu.Unlock()
		}
	}
	m := NewDPIManager(nil, DPIManagerSources{Suricata: mk("suricata"), TZSP: mk("tzsp")})
	ctx := context.Background()

	m.Reconcile(ctx, true, false) // want suricata only
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return running["suricata"] && !running["tzsp"] })

	m.Reconcile(ctx, true, true) // add tzsp
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return running["suricata"] && running["tzsp"] })

	m.Reconcile(ctx, false, false) // stop all
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return !running["suricata"] && !running["tzsp"] })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
```

Add imports `sync` and `time` to the test file if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/collector/ -run TestReconcile -v`
Expected: FAIL (undefined: Reconcile).

- [ ] **Step 3: Rework the manager**

Replace the body of `backend/internal/collector/dpi_manager.go` with a version that tracks a running mechanism set. Keep `NewDPIManager` and `DPIManagerSources` unchanged. Replace `SetMode`/`Stop`/`stopLocked`/the `mode/cancel/done` fields with per-mechanism tracking:

```go
package collector

import (
	"context"
	"log"
	"sync"

	"fluxio-backend/internal/processor"
)

type sourceRunFunc func(ctx context.Context)

type DPIManagerSources struct {
	Suricata sourceRunFunc
	TZSP     sourceRunFunc
}

type runningMech struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// DPIManager runs the set of DPI capture mechanisms requested by the sources.
// Reconcile starts/stops mechanisms to match a desired set, so adding a source
// that wants TZSP starts the TZSP listener without disturbing Suricata.
type DPIManager struct {
	cache   *processor.CorrelationCache
	sources DPIManagerSources

	mu      sync.Mutex
	running map[string]*runningMech // "suricata" / "tzsp"
}

func NewDPIManager(cache *processor.CorrelationCache, sources DPIManagerSources) *DPIManager {
	return &DPIManager{cache: cache, sources: sources, running: make(map[string]*runningMech)}
}

// Reconcile ensures exactly the requested mechanisms are running.
func (m *DPIManager) Reconcile(ctx context.Context, wantSuricata, wantTZSP bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyLocked(ctx, "suricata", wantSuricata, m.sources.Suricata)
	m.applyLocked(ctx, "tzsp", wantTZSP, m.sources.TZSP)
}

func (m *DPIManager) applyLocked(ctx context.Context, name string, want bool, run sourceRunFunc) {
	_, isRunning := m.running[name]
	switch {
	case want && !isRunning && run != nil:
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		m.running[name] = &runningMech{cancel: cancel, done: done}
		go func() {
			defer close(done)
			log.Printf("dpi-manager: starting %q", name)
			run(runCtx)
			log.Printf("dpi-manager: %q stopped", name)
		}()
	case !want && isRunning:
		rm := m.running[name]
		delete(m.running, name)
		rm.cancel()
		m.mu.Unlock()
		<-rm.done
		m.mu.Lock()
	}
}

// Stop cancels all running mechanisms and waits for them to exit.
func (m *DPIManager) Stop() {
	m.Reconcile(context.Background(), false, false)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/collector/ -run TestReconcile -race -v`
Expected: PASS.

Note: existing `dpi_manager_test.go` tests that referenced `SetMode` must be updated to use `Reconcile`. Update or remove the old `SetMode` tests as part of this step (the old hot-swap semantics no longer exist).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/collector/dpi_manager.go backend/internal/collector/dpi_manager_test.go
git commit -m "feat(sources): rework DPI manager to concurrent multi-mode reconcile"
```

---

## Task 10: Wire everything in main.go

**Files:**
- Modify: `backend/cmd/server/main.go`

This task has no new unit test (it is composition); verification is the Docker build + the manual end-to-end check in Task 11.

- [ ] **Step 1: Construct the registry and stats, warm the cache**

After `settingsRepo := settings.NewRepository(pgDB)` add:

```go
	sourceRepo := sources.NewRepository(pgDB)
	sourceReg := sources.NewRegistry(sourceRepo)
	if err := sourceReg.Load(context.Background()); err != nil {
		log.Printf("sources: failed to warm cache: %v", err)
	}
	sourceStats := sources.NewStats()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-pipelineCtx.Done():
				return
			case <-t.C:
				sourceStats.Roll()
			}
		}
	}()
```

Add `"fluxio-backend/internal/sources"` to the imports.

- [ ] **Step 2: Replace the global DPI startup with a reconcile loop**

Remove the block that reads `settingsRepo.GetDPIMode(...)` and calls `dpiManager.SetMode(...)`. Replace with a reconcile loop driven by the registry's requested mechanisms:

```go
	reconcile := func() {
		suri, tzsp := sourceReg.RequestedMechanisms()
		dpiManager.Reconcile(pipelineCtx, suri, tzsp)
	}
	reconcile() // initial
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-pipelineCtx.Done():
				return
			case <-t.C:
				reconcile()
			}
		}
	}()
```

- [ ] **Step 3: Stamp/gate NetFlow and apply per-source DPI in the pipeline**

Change the NetFlow listener call to pass a gate backed by the registry:

```go
	go collector.StartNetFlowListener(netflowPort, flowCh, func(addr string) (bool, bool) {
		d := sourceReg.Observe(context.Background(), addr, "netflow")
		return d.Enabled, true
	})
```

In the `flowCh` consumer goroutine, replace the `correlationCache.Get(tuple)` block with a per-source-mode lookup, and record stats. The flow's source is already stamped (`flow.Source`); fetch its dpi_mode via Observe (cheap cached read):

```go
	go func() {
		for flow := range flowCh {
			geoIP.EnrichFlow(&flow)

			dpiMode := sourceReg.Observe(context.Background(), flow.Source, "netflow").DPIMode
			tuple := processor.FiveTuple{
				SrcIP: flow.SourceIP, DstIP: flow.DestinationIP,
				SrcPort: flow.SourcePort, DstPort: flow.DestinationPort, Protocol: flow.Protocol,
			}
			if meta, ok := correlationCache.GetForMode(tuple, dpiMode); ok {
				flow.Application = meta.Application
				flow.SNI = meta.SNI
				flow.HTTPHost = meta.HTTPHost
				flow.HTTPURL = meta.HTTPURL
			}

			sourceStats.Record(flow.Source, flow.Bytes)
			writer.WriteFlow(flow)
		}
	}()
```

- [ ] **Step 4: Remove the now-dead global settings DPI wiring**

The per-source dpi_mode supersedes the global setting. Leave `registerSettingsRoutes` for now (B2 replaces it), but it no longer drives the DPI manager. Confirm no remaining reference to `dpiManager.SetMode` exists (it was removed in Task 9). The build will fail to compile if a stale reference remains — fix by removing it.

- [ ] **Step 5: Verify the build compiles via Docker**

Run: `docker compose build backend`
Expected: build succeeds (Go compiles, `go.sum` regenerated by the Dockerfile).

- [ ] **Step 6: Commit**

```bash
git add backend/cmd/server/main.go
git commit -m "feat(sources): wire registry, gate, stats, per-source DPI, reconcile loop"
```

---

## Task 11: End-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Bring up the stack**

Run: `docker compose up --build -d`
Expected: all containers reach `running` (use the installer's check or `docker ps`).

- [ ] **Step 2: Confirm the ClickHouse `source` column exists**

Run:
```bash
docker exec fluxio-clickhouse clickhouse-client -q "DESCRIBE fluxio.network_flows" | grep source
```
Expected: a `source String` row.

- [ ] **Step 3: Send a NetFlow packet from a known exporter and confirm a source row**

Generate NetFlow toward the host (or use a real exporter). Then:
```bash
docker exec fluxio-postgres psql -U fluxio -d fluxioclient -c "SELECT address, type, enabled, dpi_mode FROM sources;"
```
Expected: a row for the exporter IP, `enabled=t`, `dpi_mode=auto`.

- [ ] **Step 4: Confirm flows are stamped with the source**

Run:
```bash
docker exec fluxio-clickhouse clickhouse-client -q "SELECT DISTINCT source FROM fluxio.network_flows LIMIT 5"
```
Expected: the exporter IP(s) appear, not empty strings.

- [ ] **Step 5: Disable a source in Postgres and confirm its flows stop**

```bash
docker exec fluxio-postgres psql -U fluxio -d fluxioclient -c "UPDATE sources SET enabled=false WHERE address='<EXPORTER_IP>';"
```
Wait ~15s (cache refresh on stale last_seen), keep sending, then confirm no new rows with that source. (Registry refresh on disable is immediate via B2's PATCH; here the cache picks it up on the next stale re-upsert.)

- [ ] **Step 6: Tear down**

Run: `docker compose down`

- [ ] **Step 7: Final commit (if any verification fixes were needed)**

```bash
git add -A
git commit -m "test(sources): end-to-end verification fixes"
```

---

## Notes for the executor

- The source REST endpoints (`GET/PATCH /api/sources`) are **B2**, not this plan. After B1, sources are observable only via the database; that is expected.
- **Gate scope:** the ingestion gate (Task 7) applies to **NetFlow**, the only mechanism that produces stored `FlowRecord`s. TZSP and Suricata are DPI/alert mechanisms, not flow sources: TZSP feeds the correlation cache and Suricata produces alerts from the local sensor (`127.0.0.1`). They are governed by per-source `dpi_mode` via the multi-mode manager (Task 9), not by the flow gate. This is the intended scoping of the spec's "gate" for the MVP — do not add a TZSP/Suricata flow gate.
- **Mismatch detection** is *derived*, not stored: a source's `mismatch` is `expected_type != "" && expected_type != type`, computed in B2's `GET /api/sources` handler from the row B1 stores. B1 only needs to persist `expected_type` (done in Task 3). No extra logic in `Observe`.
- `Observe` is called twice per flow in main.go (once in the gate, once in the consumer for dpi_mode). Both are cheap cached reads. If profiling later shows this matters, thread the `Decision` through the channel instead — not needed for MVP.
- Do not run `go mod tidy` (it upgrades deps to Go 1.23). The Dockerfile uses `GOFLAGS=-mod=mod`. Locally, `go test` works with the committed module graph.
