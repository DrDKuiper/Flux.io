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

	mu    sync.RWMutex
	byKey map[string]Source // key = address|type
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
