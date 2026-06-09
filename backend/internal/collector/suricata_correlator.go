package collector

import (
	"context"
	"log"
	"strings"

	"fluxio-backend/internal/processor"
)

// RunSuricataCorrelator tails eve.json via tailer and stores any DPI
// metadata it finds (TLS SNI, DNS queries, HTTP hosts) into cache, keyed
// by each event's 5-tuple. It blocks until ctx is cancelled or the
// underlying tailer's line channel closes.
//
// This is the "suricata" DPI mode: rather than re-implementing protocol
// inspection, it reuses the analysis Suricata already performs.
func RunSuricataCorrelator(ctx context.Context, tailer *FileTailer, cache *processor.CorrelationCache) {
	lines := tailer.Lines(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			processEveLine(line, cache)
		}
	}
}

func processEveLine(line string, cache *processor.CorrelationCache) {
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
}
