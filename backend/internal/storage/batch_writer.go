package storage

import (
	"context"
	"log"
	"time"

	"fluxio-backend/internal/processor"
)

// Inserter persists batches of records. Implemented by ClickHouseStore;
// fakeable in tests so the batching logic can be verified without a database.
type Inserter interface {
	InsertFlows(ctx context.Context, records []processor.FlowRecord) error
	InsertAlerts(ctx context.Context, alerts []processor.SuricataAlert) error
}

// BatchWriter buffers FlowRecords and SuricataAlerts and flushes them to an
// Inserter whenever the buffer reaches batchSize or flushEvery elapses,
// whichever happens first. This bounds both write latency and the number
// of round-trips to ClickHouse.
type BatchWriter struct {
	inserter   Inserter
	batchSize  int
	flushEvery time.Duration

	flowCh  chan processor.FlowRecord
	alertCh chan processor.SuricataAlert
}

func NewBatchWriter(inserter Inserter, batchSize int, flushEvery time.Duration) *BatchWriter {
	return &BatchWriter{
		inserter:   inserter,
		batchSize:  batchSize,
		flushEvery: flushEvery,
		flowCh:     make(chan processor.FlowRecord, batchSize*4),
		alertCh:    make(chan processor.SuricataAlert, batchSize),
	}
}

// WriteFlow enqueues a record for the next flush. It never blocks the caller
// for long: if the buffer is saturated, the oldest pending record is dropped
// so a slow database can't back-pressure the whole collection pipeline.
func (w *BatchWriter) WriteFlow(r processor.FlowRecord) {
	select {
	case w.flowCh <- r:
	default:
		select {
		case <-w.flowCh:
		default:
		}
		log.Printf("storage: flow buffer full, dropped oldest record to make room")
		w.flowCh <- r
	}
}

func (w *BatchWriter) WriteAlert(a processor.SuricataAlert) {
	select {
	case w.alertCh <- a:
	default:
		select {
		case <-w.alertCh:
		default:
		}
		log.Printf("storage: alert buffer full, dropped oldest record to make room")
		w.alertCh <- a
	}
}

// Run drains both channels, accumulating batches and flushing on size or
// time, until ctx is cancelled (flushing whatever remains before returning).
func (w *BatchWriter) Run(ctx context.Context) {
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()

	flows := make([]processor.FlowRecord, 0, w.batchSize)
	alerts := make([]processor.SuricataAlert, 0, w.batchSize)

	flush := func() {
		if len(flows) > 0 {
			w.flushFlows(ctx, flows)
			flows = flows[:0]
		}
		if len(alerts) > 0 {
			w.flushAlerts(ctx, alerts)
			alerts = alerts[:0]
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case rec := <-w.flowCh:
			flows = append(flows, rec)
			if len(flows) >= w.batchSize {
				w.flushFlows(ctx, flows)
				flows = flows[:0]
			}
		case alert := <-w.alertCh:
			alerts = append(alerts, alert)
			if len(alerts) >= w.batchSize {
				w.flushAlerts(ctx, alerts)
				alerts = alerts[:0]
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (w *BatchWriter) flushFlows(ctx context.Context, batch []processor.FlowRecord) {
	toSend := append([]processor.FlowRecord(nil), batch...)
	if err := w.insertWithRetry(ctx, func(ctx context.Context) error {
		return w.inserter.InsertFlows(ctx, toSend)
	}); err != nil {
		log.Printf("storage: giving up on flow batch of %d records: %v", len(toSend), err)
	}
}

func (w *BatchWriter) flushAlerts(ctx context.Context, batch []processor.SuricataAlert) {
	toSend := append([]processor.SuricataAlert(nil), batch...)
	if err := w.insertWithRetry(ctx, func(ctx context.Context) error {
		return w.inserter.InsertAlerts(ctx, toSend)
	}); err != nil {
		log.Printf("storage: giving up on alert batch of %d records: %v", len(toSend), err)
	}
}

// insertWithRetry retries a write up to 3 times with exponential backoff
// (200ms, 400ms, 800ms) before giving up — ClickHouse outages are usually
// transient (restarts, brief network blips).
func (w *BatchWriter) insertWithRetry(ctx context.Context, fn func(context.Context) error) error {
	backoff := 200 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := fn(ctx); err == nil {
			return nil
		} else {
			lastErr = err
			log.Printf("storage: insert attempt %d failed: %v", attempt+1, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}
	return lastErr
}
