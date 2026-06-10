package processor

import (
	"context"
	"sync"
	"time"
)

type correlationEntry struct {
	metadata  DPIMetadata
	mechanism string // "suricata" or "tzsp" — which capture produced this entry
	expiresAt time.Time
}

// CorrelationCache is a TTL'd map from FiveTuple to DPIMetadata. It is the
// hand-off point between the DPI capture mechanisms (Suricata correlation and
// TZSP capture, which may run concurrently) and the main flow-processing
// pipeline: both write Put() tagged with their mechanism; the pipeline calls
// GetForMode() while enriching each flow, honoring the source's dpi_mode.
//
// Entries expire after ttl — conversations are short-lived, and an unbounded
// map would grow forever on a busy network. CleanupLoop runs a periodic
// sweep so memory is reclaimed even for keys that are never looked up again.
type CorrelationCache struct {
	mu      sync.RWMutex
	entries map[FiveTuple]correlationEntry
	ttl     time.Duration
}

func NewCorrelationCache(ttl time.Duration) *CorrelationCache {
	return &CorrelationCache{
		entries: make(map[FiveTuple]correlationEntry),
		ttl:     ttl,
	}
}

// Put records DPI metadata for a conversation, tagged with the capture
// mechanism that produced it ("suricata" or "tzsp"), resetting its expiry.
func (c *CorrelationCache) Put(key FiveTuple, meta DPIMetadata, mechanism string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = correlationEntry{metadata: meta, mechanism: mechanism, expiresAt: time.Now().Add(c.ttl)}
}

// Get returns the DPI metadata for a conversation if it's present and not
// expired. An expired-but-not-yet-swept entry is treated as a miss.
func (c *CorrelationCache) Get(key FiveTuple) (DPIMetadata, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return DPIMetadata{}, false
	}
	return entry.metadata, true
}

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

// Len reports the current number of entries, including any not-yet-swept
// expired ones. Exposed primarily so tests can observe cleanup progress.
func (c *CorrelationCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// CleanupLoop periodically removes expired entries until ctx is cancelled.
// Run it once, in a background goroutine, for the lifetime of the cache.
func (c *CorrelationCache) CleanupLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.removeExpired()
		}
	}
}

func (c *CorrelationCache) removeExpired() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}
