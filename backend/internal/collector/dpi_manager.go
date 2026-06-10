package collector

import (
	"context"
	"fmt"
	"log"
	"sync"

	"fluxio-backend/internal/processor"
)

// sourceRunFunc runs one DPI source until ctx is cancelled. Both
// RunSuricataCorrelator (Task 14) and a TZSP-listener wrapper (Task 15)
// satisfy this shape once their cache/path/port arguments are bound by
// a closure — see the wiring in main.go.
type sourceRunFunc func(ctx context.Context)

// DPIManagerSources binds each named mode to the function that runs it.
// Exposed as a struct (rather than a map) so call sites are type-checked
// and self-documenting about which modes exist.
type DPIManagerSources struct {
	Suricata sourceRunFunc
	TZSP     sourceRunFunc
}

// DPIManager owns the currently-active DPI source and can hot-swap it:
// SetMode cancels whatever is running and starts the requested mode,
// so a change made on the Settings page (via PUT /api/settings)
// takes effect immediately without a backend restart.
type DPIManager struct {
	cache   *processor.CorrelationCache
	sources DPIManagerSources

	mu     sync.Mutex
	mode   string
	cancel context.CancelFunc
	done   chan struct{}
}

func NewDPIManager(cache *processor.CorrelationCache, sources DPIManagerSources) *DPIManager {
	return &DPIManager{cache: cache, sources: sources, mode: "none"}
}

// SetMode stops the currently-running source (if any) and starts the one
// matching mode ("none" stops without starting anything). It blocks until
// the previous source has fully stopped, so callers can rely on there never
// being two sources running concurrently.
func (m *DPIManager) SetMode(ctx context.Context, mode string) error {
	var run sourceRunFunc
	switch mode {
	case "none":
		run = nil
	case "suricata":
		run = m.sources.Suricata
	case "tzsp":
		run = m.sources.TZSP
	default:
		return fmt.Errorf("dpi-manager: unknown mode %q", mode)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.stopLocked()
	m.mode = mode

	if run == nil {
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.cancel = cancel
	m.done = done

	go func() {
		defer close(done)
		log.Printf("dpi-manager: starting %q DPI source", mode)
		run(runCtx)
		log.Printf("dpi-manager: %q DPI source stopped", mode)
	}()

	return nil
}

// Stop cancels the active source, if any, and waits for it to finish.
func (m *DPIManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	m.mode = "none"
}

// stopLocked cancels the current source and waits for it to exit.
// It briefly releases m.mu while waiting so a source goroutine that calls
// back into the manager (or any other caller) is not deadlocked.
// Caller must hold m.mu on entry; it will still hold m.mu on return.
func (m *DPIManager) stopLocked() {
	if m.cancel == nil {
		return
	}
	cancel := m.cancel
	done := m.done
	m.cancel = nil
	m.done = nil
	cancel()
	m.mu.Unlock()
	<-done // wait without holding the lock
	m.mu.Lock()
}
