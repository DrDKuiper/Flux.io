package processor

import (
	"context"
	"testing"
	"time"
)

func TestCorrelationCache_PutThenGet(t *testing.T) {
	cache := NewCorrelationCache(time.Minute)
	key := FiveTuple{SrcIP: "10.0.0.1", DstIP: "93.184.216.34", SrcPort: 51000, DstPort: 443, Protocol: 6}

	cache.Put(key, DPIMetadata{SNI: "example.com", Application: "TLS/example.com"}, "suricata")

	got, ok := cache.Get(key)
	if !ok {
		t.Fatal("expected a cache hit for a key that was just stored")
	}
	if got.SNI != "example.com" {
		t.Errorf("expected SNI %q, got %q", "example.com", got.SNI)
	}
}

func TestCorrelationCache_MissForUnknownKey(t *testing.T) {
	cache := NewCorrelationCache(time.Minute)
	_, ok := cache.Get(FiveTuple{SrcIP: "10.0.0.9", DstIP: "10.0.0.10", SrcPort: 1, DstPort: 2, Protocol: 6})
	if ok {
		t.Fatal("expected a cache miss for a key that was never stored")
	}
}

func TestCorrelationCache_EntriesExpire(t *testing.T) {
	cache := NewCorrelationCache(50 * time.Millisecond)
	key := FiveTuple{SrcIP: "10.0.0.1", DstIP: "10.0.0.2", SrcPort: 1111, DstPort: 2222, Protocol: 17}
	cache.Put(key, DPIMetadata{Application: "DNS/example.com"}, "tzsp")

	if _, ok := cache.Get(key); !ok {
		t.Fatal("expected a hit immediately after Put")
	}

	time.Sleep(200 * time.Millisecond) // 4× the 50ms TTL — generous margin against scheduler jitter

	if _, ok := cache.Get(key); ok {
		t.Fatal("expected the entry to have expired after its TTL elapsed")
	}
}

func TestCorrelationCache_CleanupLoopRemovesExpiredEntries(t *testing.T) {
	cache := NewCorrelationCache(30 * time.Millisecond)
	key := FiveTuple{SrcIP: "10.0.0.1", DstIP: "10.0.0.2", SrcPort: 1, DstPort: 2, Protocol: 6}
	cache.Put(key, DPIMetadata{Application: "TLS/expiring.example.com"}, "suricata")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cache.CleanupLoop(ctx, 20*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for cache.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := cache.Len(); got != 0 {
		t.Fatalf("expected cleanup loop to remove the expired entry, but cache still has %d entries", got)
	}
}
