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
