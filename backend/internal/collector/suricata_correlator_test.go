package collector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fluxio-backend/internal/processor"
)

func TestRunSuricataCorrelator_PopulatesCacheFromTLSEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eve.json")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatalf("failed to create eve.json: %v", err)
	}

	cache := processor.NewCorrelationCache(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := NewFileTailer(path)
	tailer.ready = make(chan struct{})

	go RunSuricataCorrelator(ctx, tailer, cache)

	// Wait for the tailer goroutine to open the file and seek to EOF before appending
	select {
	case <-tailer.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for tailer to open file")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open eve.json for append: %v", err)
	}
	_, _ = f.WriteString(tlsEventJSON + "\n")
	_, _ = f.WriteString(flowEventJSON + "\n") // no DPI metadata — must NOT populate the cache
	f.Close()

	key := processor.FiveTuple{SrcIP: "10.0.0.1", DstIP: "93.184.216.34", SrcPort: 51000, DstPort: 443, Protocol: 6}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if meta, ok := cache.Get(key); ok {
			if meta.SNI != "example.com" {
				t.Fatalf("expected SNI %q, got %q", "example.com", meta.SNI)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the TLS event to populate the correlation cache")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The flow event carries no DPI metadata, so cache must still hold exactly one entry
	// (the TLS event). Verifying Len() == 1 is the definitive check.
	if cache.Len() != 1 {
		t.Errorf("expected exactly 1 cache entry (from TLS event only), got %d", cache.Len())
	}
}
