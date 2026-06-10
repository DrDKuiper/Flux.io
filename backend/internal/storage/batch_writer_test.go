package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"fluxio-backend/internal/processor"
)

type fakeInserter struct {
	mu    sync.Mutex
	flows [][]processor.FlowRecord
}

func (f *fakeInserter) InsertFlows(_ context.Context, records []processor.FlowRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	batch := append([]processor.FlowRecord(nil), records...)
	f.flows = append(f.flows, batch)
	return nil
}

func (f *fakeInserter) InsertAlerts(_ context.Context, _ []processor.SuricataAlert) error {
	return nil
}

func (f *fakeInserter) batchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.flows)
}

func TestBatchWriter_FlushesWhenBatchSizeReached(t *testing.T) {
	fake := &fakeInserter{}
	writer := NewBatchWriter(fake, 2, time.Hour) // huge interval: only size-based flush should fire

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go writer.Run(ctx)

	writer.WriteFlow(processor.FlowRecord{SourceIP: "10.0.0.1"})
	writer.WriteFlow(processor.FlowRecord{SourceIP: "10.0.0.2"})

	deadline := time.Now().Add(2 * time.Second)
	for fake.batchCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := fake.batchCount(); got != 1 {
		t.Fatalf("expected exactly 1 flushed batch of size 2, got %d batches", got)
	}
}

func TestBatchWriter_FlushesOnTimer(t *testing.T) {
	fake := &fakeInserter{}
	writer := NewBatchWriter(fake, 100, 50*time.Millisecond) // huge size: only timer should fire

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go writer.Run(ctx)

	writer.WriteFlow(processor.FlowRecord{SourceIP: "10.0.0.1"})

	deadline := time.Now().Add(2 * time.Second)
	for fake.batchCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := fake.batchCount(); got < 1 {
		t.Fatalf("expected timer to flush at least 1 batch, got %d", got)
	}
}
