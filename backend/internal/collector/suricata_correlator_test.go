package collector

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fluxio-backend/internal/processor"
)

// fakeAlertWriter is a thread-safe in-memory sink for WriteAlert calls.
type fakeAlertWriter struct {
	mu     sync.Mutex
	alerts []processor.SuricataAlert
}

func (f *fakeAlertWriter) WriteAlert(alert processor.SuricataAlert) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alerts = append(f.alerts, alert)
}

func (f *fakeAlertWriter) snapshot() []processor.SuricataAlert {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]processor.SuricataAlert, len(f.alerts))
	copy(out, f.alerts)
	return out
}

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

	aw := &fakeAlertWriter{}
	go RunSuricataCorrelator(ctx, tailer, cache, aw)

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
	_, _ = f.WriteString(alertEventJSON + "\n")
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

	// Poll until the alert event is forwarded to the fakeAlertWriter.
	alertDeadline := time.Now().Add(3 * time.Second)
	for {
		snaps := aw.snapshot()
		if len(snaps) >= 1 {
			got := snaps[0]
			if got.SignatureID != 2024897 {
				t.Fatalf("expected SignatureID 2024897, got %d", got.SignatureID)
			}
			if got.Signature != "ET MALWARE Possible C2 Beacon" {
				t.Fatalf("expected signature %q, got %q", "ET MALWARE Possible C2 Beacon", got.Signature)
			}
			break
		}
		if time.Now().After(alertDeadline) {
			t.Fatal("timed out waiting for the alert event to be forwarded to alertWriter")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
