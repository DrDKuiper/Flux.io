package collector

import (
	"context"
	"log"
	"strings"

	"fluxio-backend/internal/processor"
)

// alertWriter is satisfied by *storage.BatchWriter — separated as an
// interface so this package doesn't need to import storage, and so tests
// can use a simple recording fake.
type alertWriter interface {
	WriteAlert(alert processor.SuricataAlert)
}

// RunSuricataCorrelator tails eve.json via tailer, storing any DPI metadata
// it finds (TLS SNI, DNS queries, HTTP hosts) into cache keyed by 5-tuple,
// and forwarding any `alert` events to alerts for persistence. It blocks
// until ctx is cancelled or the underlying tailer's line channel closes.
func RunSuricataCorrelator(ctx context.Context, tailer *FileTailer, cache *processor.CorrelationCache, alerts alertWriter) {
	lines := tailer.Lines(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			processEveLine(line, cache, alerts)
		}
	}
}

func processEveLine(line string, cache *processor.CorrelationCache, alerts alertWriter) {
	evt, err := ParseEveLine(line)
	if err != nil {
		// Empty/whitespace-only lines are normal (e.g. trailing newline after a write
		// batch); suppress them to avoid log noise on high-throughput sensors.
		if strings.TrimSpace(line) != "" {
			log.Printf("suricata-correlator: skipping unparseable eve.json line: %v", err)
		}
		return
	}

	tuple, hasTuple := evt.FiveTuple()
	meta, hasMeta := evt.DPIMetadata()
	if hasTuple && hasMeta {
		cache.Put(tuple, meta)
	}

	if alert, ok := evt.ToAlert(); ok {
		alerts.WriteAlert(alert)
	}
}
