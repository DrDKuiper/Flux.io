package collector

import (
	"context"
	"sync"
	"testing"
	"time"

	"fluxio-backend/internal/processor"
)

type modeRun struct {
	mode      string
	cancelled bool
}

type fakeRunner struct {
	mu   sync.Mutex
	runs []*modeRun
}

func (r *fakeRunner) start(mode string) func(ctx context.Context) {
	return func(ctx context.Context) {
		run := &modeRun{mode: mode}
		r.mu.Lock()
		r.runs = append(r.runs, run)
		r.mu.Unlock()

		<-ctx.Done()

		r.mu.Lock()
		run.cancelled = true
		r.mu.Unlock()
	}
}

func (r *fakeRunner) snapshot() []modeRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]modeRun, len(r.runs))
	for i, run := range r.runs {
		out[i] = *run
	}
	return out
}

func TestDPIManager_SwitchingModesStopsThePreviousListener(t *testing.T) {
	runner := &fakeRunner{}
	cache := processor.NewCorrelationCache(time.Minute)
	mgr := NewDPIManager(cache, DPIManagerSources{
		Suricata: runner.start("suricata"),
		TZSP:     runner.start("tzsp"),
	})

	if err := mgr.SetMode(context.Background(), "suricata"); err != nil {
		t.Fatalf("SetMode(suricata) returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := mgr.SetMode(context.Background(), "tzsp"); err != nil {
		t.Fatalf("SetMode(tzsp) returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	mgr.Stop()
	time.Sleep(50 * time.Millisecond)

	runs := runner.snapshot()
	if len(runs) != 2 {
		t.Fatalf("expected exactly 2 listener runs (one per mode switch), got %d: %+v", len(runs), runs)
	}
	if runs[0].mode != "suricata" || !runs[0].cancelled {
		t.Errorf("expected the suricata run to have been started then cancelled on switch: %+v", runs[0])
	}
	if runs[1].mode != "tzsp" || !runs[1].cancelled {
		t.Errorf("expected the tzsp run to have been started then cancelled on Stop: %+v", runs[1])
	}
}

func TestDPIManager_SettingModeNoneStopsWithoutStartingAnything(t *testing.T) {
	runner := &fakeRunner{}
	cache := processor.NewCorrelationCache(time.Minute)
	mgr := NewDPIManager(cache, DPIManagerSources{
		Suricata: runner.start("suricata"),
		TZSP:     runner.start("tzsp"),
	})

	if err := mgr.SetMode(context.Background(), "suricata"); err != nil {
		t.Fatalf("SetMode(suricata) returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := mgr.SetMode(context.Background(), "none"); err != nil {
		t.Fatalf("SetMode(none) returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	runs := runner.snapshot()
	if len(runs) != 1 {
		t.Fatalf("expected only the suricata run to have started, got %d: %+v", len(runs), runs)
	}
	if !runs[0].cancelled {
		t.Error("expected switching to 'none' to cancel the running suricata listener")
	}
}
