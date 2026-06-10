package collector

import (
	"context"
	"log"
	"sync"

	"fluxio-backend/internal/processor"
)

// sourceRunFunc runs one DPI capture mechanism until ctx is cancelled. Both
// RunSuricataCorrelator and a TZSP-listener wrapper satisfy this shape once
// their cache/path/port arguments are bound by a closure — see main.go.
type sourceRunFunc func(ctx context.Context)

// DPIManagerSources binds each capture mechanism to the function that runs it.
type DPIManagerSources struct {
	Suricata sourceRunFunc
	TZSP     sourceRunFunc
}

type runningMech struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// DPIManager runs the set of DPI capture mechanisms requested by the sources.
// Unlike the previous single-mode hot-swap, mechanisms run concurrently: a
// source asking for TZSP can start the TZSP listener without disturbing the
// Suricata correlator another source relies on. Reconcile starts/stops
// mechanisms to match a desired set and is idempotent.
type DPIManager struct {
	cache   *processor.CorrelationCache
	sources DPIManagerSources

	mu      sync.Mutex
	running map[string]*runningMech // "suricata" / "tzsp"
}

func NewDPIManager(cache *processor.CorrelationCache, sources DPIManagerSources) *DPIManager {
	return &DPIManager{cache: cache, sources: sources, running: make(map[string]*runningMech)}
}

// Reconcile ensures exactly the requested mechanisms are running, starting or
// stopping each as needed. Calling it with an unchanged set is a no-op.
func (m *DPIManager) Reconcile(ctx context.Context, wantSuricata, wantTZSP bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyLocked(ctx, "suricata", wantSuricata, m.sources.Suricata)
	m.applyLocked(ctx, "tzsp", wantTZSP, m.sources.TZSP)
}

func (m *DPIManager) applyLocked(ctx context.Context, name string, want bool, run sourceRunFunc) {
	_, isRunning := m.running[name]
	switch {
	case want && !isRunning && run != nil:
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		m.running[name] = &runningMech{cancel: cancel, done: done}
		go func() {
			defer close(done)
			log.Printf("dpi-manager: starting %q", name)
			run(runCtx)
			log.Printf("dpi-manager: %q stopped", name)
		}()
	case !want && isRunning:
		rm := m.running[name]
		delete(m.running, name)
		rm.cancel()
		// Release the lock while waiting so a mechanism goroutine that calls
		// back into the manager cannot deadlock.
		m.mu.Unlock()
		<-rm.done
		m.mu.Lock()
	}
}

// Stop cancels all running mechanisms and waits for them to exit.
func (m *DPIManager) Stop() {
	m.Reconcile(context.Background(), false, false)
}
